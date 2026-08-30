package manager

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/schema"
)

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
