package providers

import (
	"chat-agent/pkg/config"
	"context"
	"fmt"

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

	// Get provider configuration
	providerCfg, ok := f.cfg.Providers[modelCfg.Provider]
	if !ok {
		return nil, fmt.Errorf("provider configuration does not exist: %s", modelCfg.Provider)
	}

	// Create corresponding model based on provider type
	switch providerCfg.Type {
	case "openai":
		return f.createOpenAIModel(ctx, &modelCfg, &providerCfg)
	case "claude":
		return f.createClaudeModel(ctx, &modelCfg, &providerCfg)
	case "gemini":
		return f.createGeminiModel(ctx, &modelCfg, &providerCfg)
	case "qwen":
		return f.createQwenModel(ctx, &modelCfg, &providerCfg)
	case "qianfan":
		return f.createQianfanModel(ctx, &modelCfg, &providerCfg)
	case "ark":
		return f.createArkModel(ctx, &modelCfg, &providerCfg)
	case "deepseek":
		return f.createDeepSeekModel(ctx, &modelCfg, &providerCfg)
	case "ollama":
		return f.createOllamaModel(ctx, &modelCfg, &providerCfg)
	default:
		return nil, fmt.Errorf("unsupported provider type: %s", providerCfg.Type)
	}
}
