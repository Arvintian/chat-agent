package providers

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// MixedChatModel wraps multiple ToolCallingChatModel instances and round-robins
// between them on each Generate/Stream call.
type MixedChatModel struct {
	models []model.ToolCallingChatModel
	next   atomic.Uint64
}

// NewMixedChatModel creates a new MixedChatModel that round-robins across the
// given models. At least one model is required.
func NewMixedChatModel(models []model.ToolCallingChatModel) *MixedChatModel {
	return &MixedChatModel{
		models: models,
	}
}

// nextModel returns the next model index atomically, round-robin style.
func (m *MixedChatModel) nextModel() int {
	return int(m.next.Add(1)-1) % len(m.models)
}

// currentModel returns the model to use for this call.
func (m *MixedChatModel) currentModel() model.ToolCallingChatModel {
	return m.models[m.nextModel()]
}

// Generate implements BaseChatModel. It round-robins to the next model.
func (m *MixedChatModel) Generate(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	cm := m.currentModel()
	return cm.Generate(ctx, messages, opts...)
}

// Stream implements BaseChatModel. It round-robins to the next model.
func (m *MixedChatModel) Stream(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	cm := m.currentModel()
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
		models: newModels,
	}, nil
}
