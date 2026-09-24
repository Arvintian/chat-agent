package manager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
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

	// Usage is still tracked and reportable. The context measurement is the
	// call's TOTAL tokens (prompt + completion): the next request's prompt
	// will carry the history plus this call's completion.
	_, _, total, contextTokens, window := m.GetTokenUsage()
	if total != 1000000 || contextTokens != 1000000 || window != 0 {
		t.Fatalf("GetTokenUsage = (%d, %d, %d), want (1000000, 1000000, 0)", total, contextTokens, window)
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

	m.ReportUsage(&schema.TokenUsage{PromptTokens: 900, CompletionTokens: 5, TotalTokens: 905})
	if _, _, _, cur, _ := m.GetTokenUsage(); cur != 905 {
		t.Fatalf("contextTokens = %d, want 905 (total)", cur)
	}
	m.ReportUsage(&schema.TokenUsage{PromptTokens: 200, CompletionTokens: 2, TotalTokens: 202})
	if _, _, _, cur, _ := m.GetTokenUsage(); cur != 202 {
		t.Fatalf("contextTokens = %d, want 202 (latest must win)", cur)
	}
}

// TestWindowMode_OverThresholdCompressesSmallBatch verifies that once the
// measured context is over the threshold, window mode compresses even a small
// batch (a single oldest round) instead of waiting for the normal minimum
// batch — a verbose single-round tool loop must not run into the window
// before enough rounds accumulate.
func TestWindowMode_OverThresholdCompressesSmallBatch(t *testing.T) {
	m := NewManager(0, ContextModeWindow)
	fm := &fakeSummaryModel{reply: "summary text"}
	m.SetChatModel(fm)
	m.SetContextWindow(1000, 0.5) // threshold 500, full window 1000

	ctx := context.Background()
	buildRounds(m, ctx, 2) // 2 completed rounds + empty current = 3 (half = 1 < min batch 3)

	// Above the threshold (500) but below the full window: the small batch
	// must still be compressed — the threshold is the trigger line.
	m.ReportUsage(&schema.TokenUsage{PromptTokens: 590, CompletionTokens: 10, TotalTokens: 600})
	m.IncRound(ctx) // 4 rounds, half = 2; over threshold → minBatch 1
	if !fm.generated {
		t.Fatal("expected compression once over the threshold")
	}
	msgs := m.GetMessages()
	if msgs[0].Role != schema.Assistant || !strings.HasPrefix(msgs[0].Content, "[Previous Conversation Summary]") {
		t.Fatalf("first message is not the summary: %+v", msgs[0])
	}
}


// TestUsagePersistenceRoundTrip simulates the restart flow: usage updates are
// persisted via the callback into a (file-backed) store, a fresh manager
// restores the counters with SetTokenUsage, and later usage keeps accumulating
// on top of the restored base.
func TestUsagePersistenceRoundTrip(t *testing.T) {
	var (
		mu      sync.Mutex
		stored  map[string]string
		cbCalls int
	)
	store := func(key string, value []byte) error {
		mu.Lock()
		defer mu.Unlock()
		if stored == nil {
			stored = make(map[string]string)
		}
		stored[key] = string(value)
		return nil
	}

	m := NewManager(0, ContextModeCompress)
	m.SetUsageUpdateCallback(func(p, c, t, last int) {
		mu.Lock()
		cbCalls++
		mu.Unlock()
		_ = store("tokenUsage", []byte(fmt.Sprintf(`{"prompt":%d,"completion":%d,"total":%d,"lastPrompt":%d}`, p, c, t, last)))
	})
	m.ReportUsage(&schema.TokenUsage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120})
	m.ReportUsage(&schema.TokenUsage{PromptTokens: 50, CompletionTokens: 10, TotalTokens: 60})

	if cbCalls != 2 {
		t.Fatalf("expected 2 callback invocations, got %d", cbCalls)
	}
	raw, ok := stored["tokenUsage"]
	if !ok || raw != `{"prompt":150,"completion":30,"total":180,"lastPrompt":60}` {
		t.Fatalf("unexpected persisted usage: %q (ok=%v)", raw, ok)
	}

	// Restart: fresh manager, restore from the persisted value.
	m2 := NewManager(0, ContextModeCompress)
	m2.SetTokenUsage(150, 30, 180, 60)
	p, c, tot, last, _ := m2.GetTokenUsage()
	if p != 150 || c != 30 || tot != 180 || last != 60 {
		t.Fatalf("restored usage = %d/%d/%d (last=%d), want 150/30/180 (last=60)", p, c, tot, last)
	}

	// New calls accumulate on top of the restored base and re-persist.
	m2.SetUsageUpdateCallback(func(p, c, t, last int) {
		_ = store("tokenUsage", []byte(fmt.Sprintf(`{"prompt":%d,"completion":%d,"total":%d,"lastPrompt":%d}`, p, c, t, last)))
	})
	m2.ReportUsage(&schema.TokenUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15})
	if raw := stored["tokenUsage"]; raw != `{"prompt":160,"completion":35,"total":195,"lastPrompt":15}` {
		t.Fatalf("post-restart persisted usage: %q", raw)
	}
}

// TestWindowMode_RestoredUsageTriggersCompression verifies that a restored
// lastPrompt measurement (over the threshold) makes the next IncRound
// compress the restored history instead of waiting for a fresh model call.
func TestWindowMode_RestoredUsageTriggersCompression(t *testing.T) {
	ctx := context.Background()

	m := NewManager(0, ContextModeWindow)
	m.SetContextWindow(1000, 0.5) // threshold 500
	buildRounds(m, ctx, 5)        // 6 rounds incl. empty current one

	// Restart: no model call yet, only the persisted measurement.
	m.SetTokenUsage(300, 80, 380, 600) // lastPrompt 600 >= threshold 500

	fm := &fakeSummaryModel{reply: "summary text"}
	m.SetChatModel(fm)
	m.IncRound(ctx)
	if !fm.generated {
		t.Fatal("expected compression of the long restored history after restart")
	}
}
