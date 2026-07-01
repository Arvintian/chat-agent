package providers

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// MixedChatModel wraps multiple ToolCallingChatModel instances and selects
// among them using weighted random selection. Models with weight 0 are never
// selected. When all active weights are equal, it falls back to round-robin.
type MixedChatModel struct {
	models      []model.ToolCallingChatModel
	activeIdxs  []int       // indices into models that have weight > 0
	weights     []int      // cumulative weights for active models only
	totalWeight int
	weighted    bool // true if active weights are non-uniform
	next        atomic.Uint64
	rng         *rand.Rand
	rngMu       sync.Mutex
}

// NewMixedChatModel creates a new MixedChatModel that selects among the given
// models using the provided weights. At least one model is required.
// Models with weight <= 0 are excluded from selection.
// When weights is nil or empty, equal weight (round-robin) is used for all models.
func NewMixedChatModel(models []model.ToolCallingChatModel, weights []int) *MixedChatModel {
	m := &MixedChatModel{
		models: models,
	}

	if len(weights) == len(models) {
		// Filter out models with weight <= 0
		var activeWeights []int
		for i, w := range weights {
			if w > 0 {
				m.activeIdxs = append(m.activeIdxs, i)
				activeWeights = append(activeWeights, w)
			}
		}

		if len(activeWeights) == 0 {
			// All weights are 0 or negative, fall back to all models with equal weight
			for i := range models {
				m.activeIdxs = append(m.activeIdxs, i)
				activeWeights = append(activeWeights, 1)
			}
		}

		// Build cumulative weights for active models
		m.weights = make([]int, len(activeWeights))
		allSame := true
		first := activeWeights[0]
		for i, w := range activeWeights {
			if i == 0 {
				m.weights[i] = w
			} else {
				m.weights[i] = m.weights[i-1] + w
			}
			if w != first {
				allSame = false
			}
		}
		m.totalWeight = m.weights[len(m.weights)-1]
		m.weighted = !allSame
	} else {
		// No weights provided or length mismatch: all models active with equal weight
		for i := range models {
			m.activeIdxs = append(m.activeIdxs, i)
		}
		m.weights = make([]int, len(models))
		for i := range m.weights {
			m.weights[i] = 1
		}
		m.totalWeight = len(models)
		m.weighted = false
	}

	return m
}

// selectModel returns the index of the model to use for this call.
// Uses weighted random selection among active models when weights are non-uniform,
// otherwise round-robin. Models with weight 0 are never selected.
func (m *MixedChatModel) selectModel() int {
	idx := 0
	if !m.weighted {
		// Round-robin among active models
		idx = int(m.next.Add(1)-1) % len(m.activeIdxs)
	} else {
		// Weighted random selection among active models
		m.rngMu.Lock()
		if m.rng == nil {
			m.rng = rand.New(rand.NewSource(0))
		}
		target := m.rng.Intn(m.totalWeight) + 1
		m.rngMu.Unlock()

		// Binary search for the target in cumulative weights
		left, right := 0, len(m.weights)-1
		for left < right {
			mid := (left + right) / 2
			if m.weights[mid] < target {
				left = mid + 1
			} else {
				right = mid
			}
		}
		idx = left
	}
	return m.activeIdxs[idx]
}

// Generate implements BaseChatModel. It selects a model based on weights.
func (m *MixedChatModel) Generate(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	cm := m.models[m.selectModel()]
	return cm.Generate(ctx, messages, opts...)
}

// Stream implements BaseChatModel. It selects a model based on weights.
func (m *MixedChatModel) Stream(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	cm := m.models[m.selectModel()]
	return cm.Stream(ctx, messages, opts...)
}

// WithTools creates a new MixedChatModel where every underlying model has the
// given tools bound. Each call to WithTools returns a fresh MixedChatModel so
// the original is not mutated (safe for sharing across goroutines).
func (m *MixedChatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	newModels := make([]model.ToolCallingChatModel, len(m.models))
	for i, cm := range m.models {
		withTools, err := cm.WithTools(tools)
		if err != nil {
			return nil, fmt.Errorf("failed to bind tools to model %d: %w", i, err)
		}
		newModels[i] = withTools
	}
	return &MixedChatModel{
		models:      newModels,
		activeIdxs:  m.activeIdxs,
		weights:     m.weights,
		totalWeight: m.totalWeight,
		weighted:    m.weighted,
	}, nil
}
