package providers

import (
	"context"

	"github.com/Arvintian/chat-agent/pkg/config"
	"github.com/eino-contrib/ollama/api"

	localopenrouter "github.com/Arvintian/chat-agent/pkg/providers/openrouter"
	"github.com/cloudwego/eino-ext/components/model/ark"
	"github.com/cloudwego/eino-ext/components/model/claude"
	"github.com/cloudwego/eino-ext/components/model/deepseek"
	"github.com/cloudwego/eino-ext/components/model/gemini"
	"github.com/cloudwego/eino-ext/components/model/ollama"
	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino-ext/components/model/openrouter"
	"github.com/cloudwego/eino-ext/components/model/qianfan"
	"github.com/cloudwego/eino-ext/components/model/qwen"
	"github.com/cloudwego/eino/components/model"
)

// createOpenAIModel creates OpenAI model
func (f *Factory) createOpenAIModel(ctx context.Context, modelCfg *config.Model, providerCfg *config.Provider) (model.ToolCallingChatModel, error) {
	effort := openai.ReasoningEffortLevelMedium
	if !modelCfg.Thinking {
		effort = openai.ReasoningEffortLevel("none")
	}
	cfg := &openai.ChatModelConfig{
		Model:           modelCfg.Model,
		BaseURL:         providerCfg.BaseURL,
		APIKey:          providerCfg.APIKey,
		ReasoningEffort: effort,
	}

	if modelCfg.MaxTokens > 0 {
		cfg.MaxTokens = &modelCfg.MaxTokens
	}
	if modelCfg.Temperature > 0 {
		temp := float32(modelCfg.Temperature)
		cfg.Temperature = &temp
	}
	if modelCfg.TopP > 0 {
		topP := float32(modelCfg.TopP)
		cfg.TopP = &topP
	}

	return openai.NewChatModel(ctx, cfg)
}

// createClaudeModel creates Claude model
func (f *Factory) createClaudeModel(ctx context.Context, modelCfg *config.Model, providerCfg *config.Provider) (model.ToolCallingChatModel, error) {
	cfg := &claude.Config{
		Model:   modelCfg.Model,
		BaseURL: &(providerCfg.BaseURL),
		APIKey:  providerCfg.APIKey,
		Thinking: &claude.Thinking{
			Enable: modelCfg.Thinking,
		},
	}
	if modelCfg.MaxTokens > 0 {
		cfg.MaxTokens = modelCfg.MaxTokens
	}
	if modelCfg.Temperature > 0 {
		temp := float32(modelCfg.Temperature)
		cfg.Temperature = &temp
	}
	if modelCfg.TopP > 0 {
		topP := float32(modelCfg.TopP)
		cfg.TopP = &topP
	}

	return claude.NewChatModel(ctx, cfg)
}

// createGeminiModel creates Gemini model
func (f *Factory) createGeminiModel(ctx context.Context, modelCfg *config.Model, providerCfg *config.Provider) (model.ToolCallingChatModel, error) {
	cfg := &gemini.Config{
		Model: modelCfg.Model,
	}

	// Gemini thinking support through thinking budget
	if modelCfg.Thinking {
		// For Gemini models that support thinking, we can set the thinking budget
		// This is typically done through the API request parameters
		// Note: Not all Gemini models support thinking
	}

	if modelCfg.MaxTokens > 0 {
		cfg.MaxTokens = &modelCfg.MaxTokens
	}
	if modelCfg.Temperature > 0 {
		temp := float32(modelCfg.Temperature)
		cfg.Temperature = &temp
	}
	if modelCfg.TopP > 0 {
		topP := float32(modelCfg.TopP)
		cfg.TopP = &topP
	}

	return gemini.NewChatModel(ctx, cfg)
}

// createQwenModel creates Qwen model
func (f *Factory) createQwenModel(ctx context.Context, modelCfg *config.Model, providerCfg *config.Provider) (model.ToolCallingChatModel, error) {
	cfg := &qwen.ChatModelConfig{
		Model:          modelCfg.Model,
		BaseURL:        providerCfg.BaseURL,
		APIKey:         providerCfg.APIKey,
		EnableThinking: &modelCfg.Thinking,
	}

	if modelCfg.MaxTokens > 0 {
		cfg.MaxTokens = &modelCfg.MaxTokens
	}
	if modelCfg.Temperature > 0 {
		temp := float32(modelCfg.Temperature)
		cfg.Temperature = &temp
	}
	if modelCfg.TopP > 0 {
		topP := float32(modelCfg.TopP)
		cfg.TopP = &topP
	}

	return qwen.NewChatModel(ctx, cfg)
}

// createQianfanModel creates Qianfan model
func (f *Factory) createQianfanModel(ctx context.Context, modelCfg *config.Model, providerCfg *config.Provider) (model.ToolCallingChatModel, error) {
	cfg := &qianfan.ChatModelConfig{
		Model: modelCfg.Model,
	}

	// Qianfan thinking support through thinking_budget parameter
	// For ERNIE Bot models that support thinking (e.g., ERNIE Bot 4.5)
	if modelCfg.Thinking {
		// Set thinking budget for models that support it
		// The actual implementation depends on the specific model
	}

	if modelCfg.MaxTokens > 0 {
		cfg.MaxCompletionTokens = &modelCfg.MaxTokens
	}
	if modelCfg.Temperature > 0 {
		temp := float32(modelCfg.Temperature)
		cfg.Temperature = &temp
	}
	if modelCfg.TopP > 0 {
		topP := float32(modelCfg.TopP)
		cfg.TopP = &topP
	}

	return qianfan.NewChatModel(ctx, cfg)
}

// createArkModel creates Ark model
func (f *Factory) createArkModel(ctx context.Context, modelCfg *config.Model, providerCfg *config.Provider) (model.ToolCallingChatModel, error) {
	cfg := &ark.ChatModelConfig{
		Model:   modelCfg.Model,
		BaseURL: providerCfg.BaseURL,
		APIKey:  providerCfg.APIKey,
	}

	if modelCfg.Thinking {
		cfg.Thinking = &ark.Thinking{
			Type: "enabled",
		}
	} else {
		cfg.Thinking = &ark.Thinking{
			Type: "disabled",
		}
	}

	if modelCfg.MaxTokens > 0 {
		cfg.MaxTokens = &modelCfg.MaxTokens
	}
	if modelCfg.Temperature > 0 {
		temp := float32(modelCfg.Temperature)
		cfg.Temperature = &temp
	}
	if modelCfg.TopP > 0 {
		topP := float32(modelCfg.TopP)
		cfg.TopP = &topP
	}

	return ark.NewChatModel(ctx, cfg)
}

// createDeepSeekModel creates DeepSeek model
func (f *Factory) createDeepSeekModel(ctx context.Context, modelCfg *config.Model, providerCfg *config.Provider) (model.ToolCallingChatModel, error) {
	cfg := &deepseek.ChatModelConfig{
		Model:   modelCfg.Model,
		BaseURL: providerCfg.BaseURL,
		APIKey:  providerCfg.APIKey,
	}

	// Note: DeepSeek thinking support requires enabling reasoning in the API request
	// The current library version may not fully support thinking configuration
	// For DeepSeek R1 and other reasoning models, thinking is typically enabled by default
	// when using those specific model names (e.g., "deepseek-reasoner")

	if modelCfg.MaxTokens > 0 {
		cfg.MaxTokens = modelCfg.MaxTokens
	}
	if modelCfg.Temperature > 0 {
		temp := float32(modelCfg.Temperature)
		cfg.Temperature = temp
	}
	if modelCfg.TopP > 0 {
		topP := float32(modelCfg.TopP)
		cfg.TopP = topP
	}

	return deepseek.NewChatModel(ctx, cfg)
}

// createOllamaModel creates Ollama model
func (f *Factory) createOllamaModel(ctx context.Context, modelCfg *config.Model, providerCfg *config.Provider) (model.ToolCallingChatModel, error) {
	cfg := &ollama.ChatModelConfig{
		Model:   modelCfg.Model,
		BaseURL: providerCfg.BaseURL,
		Thinking: &api.ThinkValue{
			Value: modelCfg.Thinking,
		},
	}
	options := api.Options{}
	if modelCfg.Temperature > 0 {
		temp := float32(modelCfg.Temperature)
		options.Temperature = temp
	}
	if modelCfg.TopP > 0 {
		topP := float32(modelCfg.TopP)
		options.TopP = topP
	}
	if modelCfg.TopK > 0 {
		options.TopK = modelCfg.TopK
	}
	if modelCfg.Temperature > 0 || modelCfg.TopP > 0 || modelCfg.TopK > 0 {
		cfg.Options = &options
	}
	return ollama.NewChatModel(ctx, cfg)
}

func (f *Factory) createOpenRouterModel(ctx context.Context, modelCfg *config.Model, providerCfg *config.Provider) (model.ToolCallingChatModel, error) {
	effort := openrouter.EffortOfMedium
	if !modelCfg.Thinking {
		effort = openrouter.EffortOfNone
	}
	cfg := &openrouter.Config{
		Model:   modelCfg.Model,
		BaseURL: providerCfg.BaseURL,
		APIKey:  providerCfg.APIKey,
		Reasoning: &openrouter.Reasoning{
			Effort:  effort,
			Exclude: !modelCfg.Thinking,
			Enabled: &modelCfg.Thinking,
		},
	}

	if modelCfg.MaxTokens > 0 {
		cfg.MaxTokens = &modelCfg.MaxTokens
	}
	if modelCfg.Temperature > 0 {
		temp := float32(modelCfg.Temperature)
		cfg.Temperature = &temp
	}
	if modelCfg.TopP > 0 {
		topP := float32(modelCfg.TopP)
		cfg.TopP = &topP
	}

	return openrouter.NewChatModel(ctx, cfg)
}

func (f *Factory) createLocalOpenRouterModel(ctx context.Context, modelCfg *config.Model, providerCfg *config.Provider) (model.ToolCallingChatModel, error) {
	effort := localopenrouter.EffortOfMedium
	if !modelCfg.Thinking {
		effort = localopenrouter.EffortOfNone
	}
	cfg := localopenrouter.Config{
		Model:   modelCfg.Model,
		BaseURL: providerCfg.BaseURL,
		APIKey:  providerCfg.APIKey,
		Reasoning: &localopenrouter.ReasoningConfig{
			Effort:  effort,
			Exclude: !modelCfg.Thinking,
			Enabled: &modelCfg.Thinking,
		},
	}

	if modelCfg.MaxTokens > 0 {
		cfg.MaxTokens = &modelCfg.MaxTokens
	}
	if modelCfg.Temperature > 0 {
		temp := float32(modelCfg.Temperature)
		cfg.Temperature = &temp
	}
	if modelCfg.TopP > 0 {
		topP := float32(modelCfg.TopP)
		cfg.TopP = &topP
	}

	return localopenrouter.NewChatModel(cfg)
}
