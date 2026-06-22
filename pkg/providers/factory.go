package providers

import (
	"context"
	"fmt"

	"github.com/Arvintian/chat-agent/pkg/config"

	"github.com/cloudwego/eino/components/model"
)

// Factory is used to create ChatModel for different providers
type Factory struct {
	cfg *config.Config
}

// NewFactory creates a new Factory
func NewFactory(cfg *config.Config) *Factory {
	return &Factory{cfg: cfg}
}

// CreateChatModel creates corresponding ChatModel based on model name
func (f *Factory) CreateChatModel(ctx context.Context, modelName string) (model.ToolCallingChatModel, error) {
	// Get model configuration
	modelCfg, ok := f.cfg.Models[modelName]
	if !ok {
		return nil, fmt.Errorf("model configuration does not exist: %s", modelName)
	}

	// Handle mixed (round-robin) model type
	if len(modelCfg.Mixed) > 0 {
		return f.createMixedModel(ctx, &modelCfg)
	}

	// Get provider configuration
	providerCfg, ok := f.cfg.Providers[modelCfg.Provider]
	if !ok {
		return nil, fmt.Errorf("provider configuration does not exist: %s", modelCfg.Provider)
	}

	return f.createSingleModel(ctx, &modelCfg, &providerCfg)
}

// createMixedModel creates a MixedChatModel that round-robins across all
// sub-models defined in the model's Mixed configuration.
func (f *Factory) createMixedModel(ctx context.Context, modelCfg *config.Model) (model.ToolCallingChatModel, error) {
	if len(modelCfg.Mixed) == 0 {
		return nil, fmt.Errorf("mixed model requires at least one sub-model")
	}

	models := make([]model.ToolCallingChatModel, 0, len(modelCfg.Mixed))
	for i, entry := range modelCfg.Mixed {
		providerCfg, ok := f.cfg.Providers[entry.Provider]
		if !ok {
			return nil, fmt.Errorf("mixed model[%d]: provider configuration does not exist: %s", i, entry.Provider)
		}

		// Build a synthetic model config for the sub-model
		subCfg := config.Model{
			ModelParams: entry.ModelParams,
		}

		cm, err := f.createSingleModel(ctx, &subCfg, &providerCfg)
		if err != nil {
			return nil, fmt.Errorf("mixed model[%d]: %w", i, err)
		}
		models = append(models, cm)
	}

	return NewMixedChatModel(models), nil
}

// createSingleModel creates a ChatModel for a single provider configuration.
func (f *Factory) createSingleModel(ctx context.Context, modelCfg *config.Model, providerCfg *config.Provider) (model.ToolCallingChatModel, error) {
	switch providerCfg.Type {
	case "openai":
		return f.createOpenAIModel(ctx, modelCfg, providerCfg)
	case "claude":
		return f.createClaudeModel(ctx, modelCfg, providerCfg)
	case "gemini":
		return f.createGeminiModel(ctx, modelCfg, providerCfg)
	case "qwen":
		return f.createQwenModel(ctx, modelCfg, providerCfg)
	case "qianfan":
		return f.createQianfanModel(ctx, modelCfg, providerCfg)
	case "ark":
		return f.createArkModel(ctx, modelCfg, providerCfg)
	case "deepseek":
		return f.createDeepSeekModel(ctx, modelCfg, providerCfg)
	case "ollama":
		return f.createOllamaModel(ctx, modelCfg, providerCfg)
	case "openrouter":
		return f.createOpenRouterModel(ctx, modelCfg, providerCfg)
	default:
		return nil, fmt.Errorf("unsupported provider type: %s", providerCfg.Type)
	}
}
