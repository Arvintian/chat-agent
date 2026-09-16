package manager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// fakeSummaryModel is a minimal ToolCallingChatModel that records whether its
// Generate was called and returns a fixed summary.
type fakeSummaryModel struct {
	model.ToolCallingChatModel
	generated bool
	reply     string
}

func (f *fakeSummaryModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	f.generated = true
	return &schema.Message{Role: schema.Assistant, Content: f.reply}, nil
}

func (f *fakeSummaryModel) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("not implemented")
}

func (f *fakeSummaryModel) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return f, nil
}

func (f *fakeSummaryModel) BindTools(_ []*schema.ToolInfo) error { return nil }

func toolIDs(messages []*schema.Message) []string {
	ids := make([]string, 0)
	for _, m := range messages {
		if m.Role == schema.Tool {
			ids = append(ids, m.ToolCallID)
		}
	}
	return ids
}

func TestOrderToolMessages_ReordersToToolCallOrder(t *testing.T) {
	assistant := &schema.Message{
		Role:      schema.Assistant,
		ToolCalls: []schema.ToolCall{{ID: "a"}, {ID: "b"}, {ID: "c"}},
	}
	// Persisted in (nondeterministic) parallel completion order.
	msgs := []*schema.Message{
		schema.UserMessage("hi"),
		assistant,
		schema.ToolMessage("result c", "c"),
		schema.ToolMessage("result a", "a"),
		schema.ToolMessage("result b", "b"),
		schema.AssistantMessage("final", nil),
	}

	got := orderToolMessages(msgs)
	want := []string{"a", "b", "c"}
	gotIDs := []string{}
	for _, m := range got {
		if m.Role == schema.Tool {
			gotIDs = append(gotIDs, m.ToolCallID)
		}
	}
	if len(gotIDs) != 3 {
		t.Fatalf("expected 3 tool messages, got %d", len(gotIDs))
	}
	for i := range want {
		if gotIDs[i] != want[i] {
			t.Fatalf("tool order = %v, want %v", gotIDs, want)
		}
	}
	// Content must follow the tool_call id.
	if got[2].Content != "result a" || got[3].Content != "result b" || got[4].Content != "result c" {
		t.Fatalf("tool content not aligned with tool-call order: %q %q %q", got[2].Content, got[3].Content, got[4].Content)
	}
	// Non-tool message order preserved.
	if got[0].Role != schema.User || got[1] != assistant || got[5].Role != schema.Assistant {
		t.Fatalf("non-tool message positions changed")
	}
}

func TestOrderToolMessages_Idempotent(t *testing.T) {
	assistant := &schema.Message{
		Role:      schema.Assistant,
		ToolCalls: []schema.ToolCall{{ID: "a"}, {ID: "b"}},
	}
	msgs := []*schema.Message{
		assistant,
		schema.ToolMessage("result a", "a"),
		schema.ToolMessage("result b", "b"),
	}
	once := toolIDs(orderToolMessages(msgs))
	twice := toolIDs(orderToolMessages(orderToolMessages(msgs)))
	if len(once) != 2 || once[0] != "a" || once[1] != "b" {
		t.Fatalf("not canonically ordered: %v", once)
	}
	if len(twice) != len(once) {
		t.Fatalf("not idempotent: %v vs %v", once, twice)
	}
	for i := range once {
		if once[i] != twice[i] {
			t.Fatalf("not idempotent: %v vs %v", once, twice)
		}
	}
}

func TestOrderToolMessages_KeepsUnmatchedTool(t *testing.T) {
	assistant := &schema.Message{
		Role:      schema.Assistant,
		ToolCalls: []schema.ToolCall{{ID: "a"}},
	}
	// "orphan" tool with an id not in tool_calls (incomplete round) is preserved.
	msgs := []*schema.Message{
		assistant,
		schema.ToolMessage("orphan", "orphan"),
		schema.ToolMessage("result a", "a"),
	}
	got := orderToolMessages(msgs)
	ids := toolIDs(got)
	if len(ids) != 2 {
		t.Fatalf("expected 2 tool messages, got %v", ids)
	}
	if ids[0] != "a" {
		t.Fatalf("matched tool should come first in tool-call order, got %v", ids)
	}
}

func TestOrderToolMessages_FastPathReusesSlice(t *testing.T) {
	assistant := &schema.Message{
		Role:      schema.Assistant,
		ToolCalls: []schema.ToolCall{{ID: "a"}, {ID: "b"}},
	}
	ordered := []*schema.Message{
		schema.UserMessage("hi"),
		assistant,
		schema.ToolMessage("result a", "a"),
		schema.ToolMessage("result b", "b"),
	}
	got := orderToolMessages(ordered)
	// Already canonical: must return the very same slice (no allocation).
	if len(got) == 0 || &got[0] != &ordered[0] {
		t.Fatalf("expected the same slice to be reused, got %p vs %p", &got[0], &ordered[0])
	}

	// Now make a copy that is out of order; it must be reordered (new slice).
	reordered := []*schema.Message{
		schema.UserMessage("hi"),
		assistant,
		schema.ToolMessage("result b", "b"),
		schema.ToolMessage("result a", "a"),
	}
	got2 := orderToolMessages(reordered)
	if got2[0] != reordered[0] {
		t.Fatalf("user message position changed")
	}
	if toolIDs(got2)[0] != "a" || toolIDs(got2)[1] != "b" {
		t.Fatalf("not reordered: %v", toolIDs(got2))
	}
	// After the write-back that GetMessages performs, it must be stable.
	stable := orderToolMessages(got2)
	if len(stable) == 0 || &stable[0] != &got2[0] {
		t.Fatalf("expected stable slice to be reused after reorder")
	}
}

// TestTruncatePersistsSortedOrder verifies the truncation persistence path
// normalizes tool order even when the in-memory rounds are still in the
// (unsorted) completion order — i.e. the first turn after a restart, before
// any GetMessages has sorted them.
func TestTruncatePersistsSortedOrder(t *testing.T) {
	m := NewManager(2, ContextModeTruncate)
	ctx := context.Background()

	var captured []*schema.Message
	m.SetCompressionCompleteCallback(func(msgs []*schema.Message) error {
		captured = msgs
		return nil
	})

	// Two leading plain rounds so truncation must drop one (maxMessageRound=2).
	m.AddMessage(ctx, schema.UserMessage("u0"))
	m.IncRound(ctx)
	m.AddMessage(ctx, schema.UserMessage("u1"))
	m.IncRound(ctx)
	// Current round: tool results appended in completion order (not tool-call order).
	m.AddMessage(ctx, &schema.Message{
		Role:      schema.Assistant,
		ToolCalls: []schema.ToolCall{{ID: "a"}, {ID: "b"}},
	})
	m.AddMessage(ctx, schema.ToolMessage("result b", "b"))
	m.AddMessage(ctx, schema.ToolMessage("result a", "a"))

	m.GetMessages() // triggers truncateLocked → normalize → persist overwrite

	if captured == nil {
		t.Fatal("expected the truncate path to persist")
	}
	ids := toolIDs(captured)
	if len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Fatalf("persisted tool order = %v, want [a b]", ids)
	}
}

// TestGetMessages_ReloadStable reproduces the reported bug: tool results are
// appended in completion order [b, a] (as persisted to the jsonl), while the
// model was fed them in tool-call order [a, b]. GetMessages must normalize to
// tool-call order so the reloaded prefix is byte-identical to the live one.
func TestGetMessages_ReloadStable(t *testing.T) {
	m := NewManager(10, ContextModeCompress)
	ctx := context.Background()

	m.AddMessage(ctx, schema.UserMessage("hi"))
	m.AddMessage(ctx, &schema.Message{
		Role:      schema.Assistant,
		ToolCalls: []schema.ToolCall{{ID: "a"}, {ID: "b"}},
	})
	// Persisted / completed in this (non-canonical) order.
	m.AddMessage(ctx, schema.ToolMessage("result b", "b"))
	m.AddMessage(ctx, schema.ToolMessage("result a", "a"))

	got := toolIDs(m.GetMessages())
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("reloaded tool order = %v, want [a b]", got)
	}
}

// buildRounds appends n single-user-message rounds (each followed by IncRound)
// so the manager ends with n completed rounds plus one empty current round.
func buildRounds(m *Manager, ctx context.Context, n int) {
	for i := 0; i < n; i++ {
		m.AddMessage(ctx, schema.UserMessage(fmt.Sprintf("u%d", i)))
		m.IncRound(ctx)
	}
}

// TestWindowMode_CompressesAtThreshold verifies window mode stays inert below
// the threshold and summarizes the oldest rounds once the reported prompt
// tokens reach it.
func TestWindowMode_CompressesAtThreshold(t *testing.T) {
	m := NewManager(0, ContextModeWindow)
	fm := &fakeSummaryModel{reply: "summary text"}
	m.SetChatModel(fm)
	m.SetContextWindow(1000, 0.5) // threshold = 500

	ctx := context.Background()
	buildRounds(m, ctx, 6) // 6 completed rounds + empty current = 7

	// Below threshold: no compression.
	m.ReportUsage(&schema.TokenUsage{PromptTokens: 400, CompletionTokens: 10, TotalTokens: 410})
	m.IncRound(ctx) // 8 rounds
	if fm.generated {
		t.Fatal("compression ran below the threshold")
	}

	// Above threshold: compression must run. The window is now 9 rounds,
	// halved to 4 (>= minCompressRounds), so 4 rounds become one summary.
	m.ReportUsage(&schema.TokenUsage{PromptTokens: 600, CompletionTokens: 20, TotalTokens: 620})
	m.IncRound(ctx) // 10 rounds → compress 5 → summary + 4 + empty

	if !fm.generated {
		t.Fatal("expected compression to run above the threshold")
	}
	msgs := m.GetMessages()
	if msgs[0].Role != schema.Assistant || !strings.HasPrefix(msgs[0].Content, "[Previous Conversation Summary]") {
		t.Fatalf("first message is not the summary: %+v", msgs[0])
	}
}

// TestWindowMode_NoWindowNeverCompresses verifies that without a configured
// window the manager only tracks usage and never calls the model.
func TestWindowMode_NoWindowNeverCompresses(t *testing.T) {
	m := NewManager(0, ContextModeWindow)
	fm := &fakeSummaryModel{reply: "summary text"}
	m.SetChatModel(fm)
	// SetContextWindow(0, ...) disables triggering (measurement-only).
	m.SetContextWindow(0, 0.5)

	ctx := context.Background()
	buildRounds(m, ctx, 6)

	m.ReportUsage(&schema.TokenUsage{PromptTokens: 999999, CompletionTokens: 1, TotalTokens: 1000000})
	m.IncRound(ctx)
	if fm.generated {
		t.Fatal("compression ran without a configured window")
	}

	// Usage is still tracked and reportable.
	_, _, total, contextTokens, window := m.GetTokenUsage()
	if total != 1000000 || contextTokens != 999999 || window != 0 {
		t.Fatalf("GetTokenUsage = (%d, %d, %d), want (1000000, 999999, 0)", total, contextTokens, window)
	}
}

// TestWindowMode_ClearResetsUsage verifies Clear() resets both the context-size
// measurement and the cumulative counters, so a fresh conversation starts clean.
func TestWindowMode_ClearResetsUsage(t *testing.T) {
	m := NewManager(0, ContextModeWindow)
	m.SetContextWindow(1000, 0.5)
	ctx := context.Background()
	buildRounds(m, ctx, 3)

	m.ReportUsage(&schema.TokenUsage{PromptTokens: 300, CompletionTokens: 50, TotalTokens: 350})
	m.Clear()

	prompt, completion, total, contextTokens, _ := m.GetTokenUsage()
	if prompt != 0 || completion != 0 || total != 0 || contextTokens != 0 {
		t.Fatalf("usage not reset after Clear: (%d, %d, %d, %d)", prompt, completion, total, contextTokens)
	}
}

// TestWindowMode_LatestUsageWins verifies a smaller prompt after compression
// lowers the measurement (latest value wins, not the max).
func TestWindowMode_LatestUsageWins(t *testing.T) {
	m := NewManager(0, ContextModeWindow)
	m.SetContextWindow(1000, 0.5)

	m.ReportUsage(&schema.TokenUsage{PromptTokens: 900})
	if _, _, _, cur, _ := m.GetTokenUsage(); cur != 900 {
		t.Fatalf("contextTokens = %d, want 900", cur)
	}
	m.ReportUsage(&schema.TokenUsage{PromptTokens: 200})
	if _, _, _, cur, _ := m.GetTokenUsage(); cur != 200 {
		t.Fatalf("contextTokens = %d, want 200 (latest must win)", cur)
	}
}

// TestWindowMode_OverfullWindowCompressesSmallBatch verifies that when the
// measured context already exceeds the full window (not just the threshold),
// window mode compresses even a single oldest round instead of waiting for
// the normal minimum batch.
func TestWindowMode_OverfullWindowCompressesSmallBatch(t *testing.T) {
	m := NewManager(0, ContextModeWindow)
	fm := &fakeSummaryModel{reply: "summary text"}
	m.SetChatModel(fm)
	m.SetContextWindow(1000, 0.5) // threshold 500, full window 1000

	ctx := context.Background()
	buildRounds(m, ctx, 2) // 2 completed rounds + empty current = 3 (half = 1 < min batch 3)

	// Only above the threshold (500) but below the full window: NOT overfull,
	// so the small batch is still skipped.
	m.ReportUsage(&schema.TokenUsage{PromptTokens: 600})
	m.IncRound(ctx) // 4 rounds, half = 2 < 3 → skip
	if fm.generated {
		t.Fatal("compressed a small batch that was only above the threshold")
	}

	// Now over the full window: the oldest round must be compressed even though
	// the halved batch is below the normal minimum.
	m.ReportUsage(&schema.TokenUsage{PromptTokens: 1200})
	m.IncRound(ctx) // 5 rounds, half = 2; overfull → minBatch 1, compress 2
	if !fm.generated {
		t.Fatal("expected compression when over the full window")
	}
	msgs := m.GetMessages()
	if msgs[0].Role != schema.Assistant || !strings.HasPrefix(msgs[0].Content, "[Previous Conversation Summary]") {
		t.Fatalf("first message is not the summary: %+v", msgs[0])
	}
}
