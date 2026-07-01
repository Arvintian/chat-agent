package providers

import (
	"context"
	"sort"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// -----------------------------------------------------------------------
// mockModel - minimal implementation of model.ToolCallingChatModel
// -----------------------------------------------------------------------

type mockModel struct {
	name string
}

func (m *mockModel) Generate(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	return &schema.Message{Role: schema.Assistant, Content: m.name}, nil
}

func (m *mockModel) Stream(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	panic("not implemented")
}

func (m *mockModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

// -----------------------------------------------------------------------
// helpers
// -----------------------------------------------------------------------

// newMockModels converts string names into []model.ToolCallingChatModel
func newMockModels(names ...string) []model.ToolCallingChatModel {
	out := make([]model.ToolCallingChatModel, len(names))
	for i, n := range names {
		out[i] = &mockModel{name: n}
	}
	return out
}

// collectSelections runs selectModel n times and returns a sorted slice of
// the returned indices.
func collectSelections(m *MixedChatModel, n int) []int {
	results := make([]int, n)
	for i := 0; i < n; i++ {
		results[i] = m.selectModel()
	}
	sort.Ints(results)
	return results
}

// -----------------------------------------------------------------------
// Tests
// -----------------------------------------------------------------------

func TestNewMixedChatModel_NoWeights(t *testing.T) {
	models := newMockModels("A", "B", "C")
	m := NewMixedChatModel(models, nil)

	if len(m.activeIdxs) != 3 {
		t.Fatalf("expected 3 active models, got %d", len(m.activeIdxs))
	}
	if m.weighted {
		t.Fatal("expected weighted=false when no weights provided")
	}
	if m.totalWeight != 3 {
		t.Fatalf("expected totalWeight=3, got %d", m.totalWeight)
	}
}

func TestNewMixedChatModel_EmptyWeights(t *testing.T) {
	models := newMockModels("A", "B")
	m := NewMixedChatModel(models, []int{})

	// Length mismatch → fallback to equal-weight round-robin
	if len(m.activeIdxs) != 2 {
		t.Fatalf("expected 2 active models, got %d", len(m.activeIdxs))
	}
	if m.weighted {
		t.Fatal("expected weighted=false")
	}
}

func TestNewMixedChatModel_EqualWeights(t *testing.T) {
	models := newMockModels("A", "B", "C")
	m := NewMixedChatModel(models, []int{2, 2, 2})

	if len(m.activeIdxs) != 3 {
		t.Fatalf("expected 3 active models, got %d", len(m.activeIdxs))
	}
	if m.weighted {
		t.Fatal("expected weighted=false when all weights are equal")
	}
	if m.totalWeight != 6 {
		t.Fatalf("expected totalWeight=6, got %d", m.totalWeight)
	}
}

func TestNewMixedChatModel_DifferentWeights(t *testing.T) {
	models := newMockModels("A", "B", "C")
	m := NewMixedChatModel(models, []int{3, 1, 6})

	if len(m.activeIdxs) != 3 {
		t.Fatalf("expected 3 active models, got %d", len(m.activeIdxs))
	}
	if !m.weighted {
		t.Fatal("expected weighted=true when weights differ")
	}
	// cumulative: [3, 4, 10]
	if m.totalWeight != 10 {
		t.Fatalf("expected totalWeight=10, got %d", m.totalWeight)
	}
	if len(m.weights) != 3 || m.weights[0] != 3 || m.weights[1] != 4 || m.weights[2] != 10 {
		t.Fatalf("unexpected cumulative weights: %v", m.weights)
	}
}

func TestNewMixedChatModel_ZeroWeightExcluded(t *testing.T) {
	models := newMockModels("A", "B", "C")
	m := NewMixedChatModel(models, []int{3, 0, 1})

	// B (index 1) has weight 0 → excluded
	if len(m.activeIdxs) != 2 {
		t.Fatalf("expected 2 active models (A and C), got %d", len(m.activeIdxs))
	}
	if m.activeIdxs[0] != 0 || m.activeIdxs[1] != 2 {
		t.Fatalf("expected activeIdxs=[0,2], got %v", m.activeIdxs)
	}
	// cumulative for active: [3, 4]
	if m.totalWeight != 4 {
		t.Fatalf("expected totalWeight=4, got %d", m.totalWeight)
	}
}

func TestNewMixedChatModel_NegativeWeightExcluded(t *testing.T) {
	models := newMockModels("A", "B")
	m := NewMixedChatModel(models, []int{-1, 5})

	if len(m.activeIdxs) != 1 {
		t.Fatalf("expected 1 active model, got %d", len(m.activeIdxs))
	}
	if m.activeIdxs[0] != 1 {
		t.Fatalf("expected activeIdxs=[1], got %v", m.activeIdxs)
	}
}

func TestNewMixedChatModel_AllZeroWeights_Fallback(t *testing.T) {
	models := newMockModels("A", "B", "C")
	m := NewMixedChatModel(models, []int{0, 0, 0})

	// All zero → fallback to all models equal weight
	if len(m.activeIdxs) != 3 {
		t.Fatalf("expected 3 active models (fallback), got %d", len(m.activeIdxs))
	}
	if m.weighted {
		t.Fatal("expected weighted=false after fallback")
	}
}

func TestNewMixedChatModel_AllNegativeWeights_Fallback(t *testing.T) {
	models := newMockModels("A", "B")
	m := NewMixedChatModel(models, []int{-1, -5})

	if len(m.activeIdxs) != 2 {
		t.Fatalf("expected 2 active models (fallback), got %d", len(m.activeIdxs))
	}
}

// -----------------------------------------------------------------------
// Round-robin tests
// -----------------------------------------------------------------------

func TestSelectModel_RoundRobin(t *testing.T) {
	models := newMockModels("A", "B", "C")
	m := NewMixedChatModel(models, []int{1, 1, 1})

	// Should cycle through 0, 1, 2, 0, 1, 2, ...
	expected := []int{0, 1, 2, 0, 1, 2}
	for i, want := range expected {
		got := m.selectModel()
		if got != want {
			t.Fatalf("call %d: expected index %d, got %d", i, want, got)
		}
	}
}

func TestSelectModel_RoundRobinWithZeroWeight(t *testing.T) {
	models := newMockModels("A", "B", "C")
	m := NewMixedChatModel(models, []int{1, 0, 1})

	// Only A(0) and C(2) are active, equal weight → round-robin between them
	// Expected cycle: 0, 2, 0, 2, ...
	expected := []int{0, 2, 0, 2}
	for i, want := range expected {
		got := m.selectModel()
		if got != want {
			t.Fatalf("call %d: expected index %d, got %d", i, want, got)
		}
	}
}

// -----------------------------------------------------------------------
// Weighted random tests
// -----------------------------------------------------------------------

func TestSelectModel_WeightedDistribution(t *testing.T) {
	models := newMockModels("A", "B", "C")
	m := NewMixedChatModel(models, []int{1, 2, 3})

	n := 10000
	results := collectSelections(m, n)
	counts := make(map[int]int)
	for _, idx := range results {
		counts[idx]++
	}

	// Expected ratios: A=1/6≈16.7%, B=2/6≈33.3%, C=3/6=50%
	// Allow ±5% tolerance
	tolerance := n / 20

	check := func(idx int, expectedRatio float64, label string) {
		expected := int(expectedRatio * float64(n))
		actual := counts[idx]
		diff := actual - expected
		if diff < 0 {
			diff = -diff
		}
		if diff > tolerance {
			t.Errorf("%s: expected ~%d (%.1f%%), got %d (%.1f%%)",
				label, expected, expectedRatio*100, actual, float64(actual)/float64(n)*100)
		}
	}

	check(0, 1.0/6.0, "A")
	check(1, 2.0/6.0, "B")
	check(2, 3.0/6.0, "C")
}

func TestSelectModel_WeightedWithZeroExcluded(t *testing.T) {
	models := newMockModels("A", "B", "C")
	m := NewMixedChatModel(models, []int{1, 0, 3})

	n := 10000
	results := collectSelections(m, n)
	counts := make(map[int]int)
	for _, idx := range results {
		counts[idx]++
	}

	// B (index 1) should NEVER be selected
	if counts[1] > 0 {
		t.Errorf("model B (weight=0) was selected %d times, expected 0", counts[1])
	}

	// A ≈ 25%, C ≈ 75%
	aCount := counts[0]
	cCount := counts[2]
	ratio := float64(cCount) / float64(aCount+1) // avoid div by zero
	if ratio < 2.0 || ratio > 4.0 {
		t.Errorf("A:C ratio expected ~1:3, got %d:%d (ratio=%.2f)", aCount, cCount, ratio)
	}
}

func TestSelectModel_OneActiveModel(t *testing.T) {
	models := newMockModels("A", "B", "C")
	m := NewMixedChatModel(models, []int{0, 5, 0})

	if len(m.activeIdxs) != 1 || m.activeIdxs[0] != 1 {
		t.Fatalf("expected activeIdxs=[1], got %v", m.activeIdxs)
	}

	for i := 0; i < 10; i++ {
		idx := m.selectModel()
		if idx != 1 {
			t.Fatalf("call %d: expected index 1, got %d", i, idx)
		}
	}
}

func TestSelectModel_SingleModel(t *testing.T) {
	models := newMockModels("only")
	m := NewMixedChatModel(models, []int{1})

	for i := 0; i < 5; i++ {
		idx := m.selectModel()
		if idx != 0 {
			t.Fatalf("expected index 0, got %d", idx)
		}
	}
}

func TestSelectModel_NegativeWeightExcluded(t *testing.T) {
	models := newMockModels("A", "B", "C")
	m := NewMixedChatModel(models, []int{1, -5, 3})

	if len(m.activeIdxs) != 2 {
		t.Fatalf("expected 2 active models, got %d", len(m.activeIdxs))
	}
	if m.activeIdxs[0] != 0 || m.activeIdxs[1] != 2 {
		t.Fatalf("expected activeIdxs=[0,2], got %v", m.activeIdxs)
	}

	n := 10000
	results := collectSelections(m, n)
	counts := make(map[int]int)
	for _, idx := range results {
		counts[idx]++
	}

	if counts[1] > 0 {
		t.Errorf("model B (weight=-5) was selected %d times, expected 0", counts[1])
	}
}

// -----------------------------------------------------------------------
// WithTools tests
// -----------------------------------------------------------------------

func TestWithTools_PreservesConfig(t *testing.T) {
	models := newMockModels("A", "B", "C")
	m := NewMixedChatModel(models, []int{3, 0, 1})

	withTools, err := m.WithTools(nil)
	if err != nil {
		t.Fatalf("WithTools failed: %v", err)
	}

	mt, ok := withTools.(*MixedChatModel)
	if !ok {
		t.Fatal("WithTools did not return *MixedChatModel")
	}

	if len(mt.activeIdxs) != 2 {
		t.Fatalf("expected 2 active models after WithTools, got %d", len(mt.activeIdxs))
	}
	if mt.activeIdxs[0] != 0 || mt.activeIdxs[1] != 2 {
		t.Fatalf("expected activeIdxs=[0,2] after WithTools, got %v", mt.activeIdxs)
	}
	if mt.totalWeight != 4 {
		t.Fatalf("expected totalWeight=4 after WithTools, got %d", mt.totalWeight)
	}
	if !mt.weighted {
		t.Fatal("expected weighted=true after WithTools")
	}
}

func TestWithTools_OriginalUnchanged(t *testing.T) {
	models := newMockModels("A", "B")
	m := NewMixedChatModel(models, []int{1, 2})

	_, err := m.WithTools(nil)
	if err != nil {
		t.Fatalf("WithTools failed: %v", err)
	}

	// Original should still work correctly
	for i := 0; i < 10; i++ {
		idx := m.selectModel()
		if idx < 0 || idx >= 2 {
			t.Fatalf("original model broken after WithTools, got index %d", idx)
		}
	}
}

// -----------------------------------------------------------------------
// Generate integration test
// -----------------------------------------------------------------------

func TestGenerate_SelectsCorrectModel(t *testing.T) {
	models := newMockModels("model-A", "model-B")
	m := NewMixedChatModel(models, []int{1, 0})

	ctx := context.Background()
	msgs := []*schema.Message{{Role: schema.User, Content: "hello"}}

	resp, err := m.Generate(ctx, msgs)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if resp.Content != "model-A" {
		t.Fatalf("expected content 'model-A', got '%s'", resp.Content)
	}
}

func TestGenerate_MultipleCalls_RoundRobin(t *testing.T) {
	models := newMockModels("A", "B")
	m := NewMixedChatModel(models, []int{1, 1})

	ctx := context.Background()
	msgs := []*schema.Message{{Role: schema.User, Content: "hi"}}

	for i := 0; i < 4; i++ {
		resp, err := m.Generate(ctx, msgs)
		if err != nil {
			t.Fatalf("call %d failed: %v", i, err)
		}
		expected := []string{"A", "B", "A", "B"}
		if resp.Content != expected[i] {
			t.Fatalf("call %d: expected '%s', got '%s'", i, expected[i], resp.Content)
		}
	}
}
