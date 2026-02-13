package openrouter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

const defaultBaseURL = "https://openrouter.ai/api/v1"

// New creates a new OpenRouter ToolCallingChatModel instance.
func NewChatModel(cfg Config) (*ChatModel, error) {
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		return nil, errors.New("openrouter: API key required")
	}

	modelName := strings.TrimSpace(cfg.Model)
	if modelName == "" {
		return nil, errors.New("openrouter: model name required")
	}

	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout: cfg.Timeout,
		}
	}

	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+apiKey)
	headers.Set("Content-Type", "application/json")
	if cfg.AppName != "" {
		headers.Set("X-Title", cfg.AppName)
	}
	if cfg.Referer != "" {
		headers.Set("HTTP-Referer", cfg.Referer)
	}

	var reasoning *reasoningConfig
	if cfg.Reasoning != nil {
		reasoning = normalizeReasoningConfig(cfg.Reasoning)
	}

	return &ChatModel{
		headers:   headers,
		client:    client,
		reasoning: reasoning,
		config:    &cfg,
	}, nil
}

func (c *ChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	reqBody, err := c.buildRequestBody(input, opts...)
	if err != nil {
		return nil, err
	}

	reqBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("openrouter: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.BaseURL+"/chat/completions", bytes.NewReader(reqBytes))
	if err != nil {
		return nil, fmt.Errorf("openrouter: create request: %w", err)
	}
	req.Header = c.headers.Clone()

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openrouter: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("openrouter: API error %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var apiResp chatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("openrouter: decode response: %w", err)
	}

	if len(apiResp.Choices) == 0 {
		return nil, errors.New("openrouter: empty response choices")
	}

	return convertAPIMessageToSchema(apiResp.Choices[0].Message), nil
}

func (c *ChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	reqBody, err := c.buildRequestBody(input, opts...)
	if err != nil {
		return nil, err
	}
	reqBody.Stream = true

	reqBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("openrouter: encode request: %w", err)
	}

	ctx = callbacks.OnStart(ctx, reqBytes)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.BaseURL+"/chat/completions", bytes.NewReader(reqBytes))
	if err != nil {
		return nil, fmt.Errorf("openrouter: create request: %w", err)
	}
	req.Header = c.headers.Clone()

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openrouter: request failed: %w", err)
	}

	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("openrouter: API error %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	sr, sw := schema.Pipe[*model.CallbackOutput](1)

	go func() {
		defer resp.Body.Close()
		defer sw.Close()

		reader := bufio.NewReader(resp.Body)

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			line, err := reader.ReadString('\n')
			if err != nil {
				if err == io.EOF {
					break
				}
				sw.Send(nil, fmt.Errorf("openrouter: read stream: %w", err))
				return
			}

			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}

			var chunk chatCompletionResponseChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				sw.Send(nil, err)
			}

			msg := convertAPIMessageToSchema(chunk.Choices[0].Delta)

			sw.Send(&model.CallbackOutput{
				Message: msg,
			}, nil)
		}
	}()

	ctx, nsr := callbacks.OnEndWithStreamOutput(ctx, schema.StreamReaderWithConvert(sr,
		func(src *model.CallbackOutput) (callbacks.CallbackOutput, error) {
			return src, nil
		}))

	outStream := schema.StreamReaderWithConvert(nsr,
		func(src callbacks.CallbackOutput) (*schema.Message, error) {
			s := src.(*model.CallbackOutput)
			if s.Message == nil {
				return nil, schema.ErrNoValue
			}

			return s.Message, nil
		},
	)
	return outStream, nil
}

func (c *ChatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	copyModel := *c
	if len(tools) > 0 {
		copyModel.boundTools = append([]*schema.ToolInfo(nil), tools...)
	} else {
		copyModel.boundTools = nil
	}
	return &copyModel, nil
}

func (c *ChatModel) buildRequestBody(input []*schema.Message, _ ...model.Option) (*chatCompletionRequest, error) {
	messages := make([]chatMessage, 0, len(input))
	for _, msg := range input {
		if msg == nil {
			continue
		}
		messages = append(messages, convertSchemaMessageToAPI(msg))
	}

	req := &chatCompletionRequest{
		Model:       c.config.Model,
		Messages:    messages,
		Reasoning:   c.reasoning,
		MaxTokens:   c.config.MaxTokens,
		TopP:        c.config.TopP,
		Temperature: c.config.Temperature,
	}

	if c.reasoning != nil {
		req.IncludeReasoning = true
	}

	if len(c.boundTools) > 0 {
		req.Tools = convertSchemaToolsToAPI(c.boundTools)
		req.ToolChoice = "auto"
	}

	return req, nil
}

func normalizeReasoningConfig(cfg *ReasoningConfig) *reasoningConfig {
	if cfg == nil {
		return nil
	}

	normalized := &reasoningConfig{
		Effort:    strings.TrimSpace(cfg.Effort),
		MaxTokens: cfg.MaxTokens,
		Exclude:   cfg.Exclude,
	}
	if cfg.Enabled != nil {
		value := *cfg.Enabled
		normalized.Enabled = &value
	}

	if normalized.Effort == "" && normalized.MaxTokens == 0 && !normalized.Exclude && normalized.Enabled == nil {
		return nil
	}
	return normalized
}

func convertSchemaMessageToAPI(msg *schema.Message) chatMessage {
	role := strings.ToLower(string(msg.Role))
	apiMsg := chatMessage{
		Role:    role,
		Content: msg.Content,
		Name:    msg.Name,
	}

	if len(msg.ToolCalls) > 0 {
		apiMsg.ToolCalls = make([]chatToolCall, len(msg.ToolCalls))
		for i, tc := range msg.ToolCalls {
			apiMsg.ToolCalls[i] = chatToolCall{
				ID:   tc.ID,
				Type: tc.Type,
				Function: chatFunctionCall{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			}
		}
	}

	if msg.Role == schema.Tool {
		apiMsg.ToolCallID = msg.ToolCallID
	}

	return apiMsg
}

func convertAPIMessageToSchema(apiMsg chatMessage) *schema.Message {
	role := schema.RoleType(apiMsg.Role)
	result := &schema.Message{
		Role:       role,
		Content:    apiMsg.Content,
		Name:       apiMsg.Name,
		ToolCallID: apiMsg.ToolCallID,
	}

	if len(apiMsg.ToolCalls) > 0 {
		result.ToolCalls = make([]schema.ToolCall, len(apiMsg.ToolCalls))
		for i, tc := range apiMsg.ToolCalls {
			result.ToolCalls[i] = schema.ToolCall{
				Index: tc.Index,
				ID:    tc.ID,
				Type:  tc.Type,
				Function: schema.FunctionCall{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			}
		}
	}

	if len(apiMsg.ReasoningRaw) > 0 {
		result.ReasoningContent = extractReasoningText(apiMsg.ReasoningRaw)
	}

	return result
}

func convertSchemaToolsToAPI(toolInfos []*schema.ToolInfo) []toolSpec {
	result := make([]toolSpec, 0, len(toolInfos))
	for _, info := range toolInfos {
		if info == nil {
			continue
		}

		result = append(result, toolSpec{
			Type: "function",
			Function: toolDefinition{
				Name:        info.Name,
				Description: info.Desc,
				Parameters:  buildToolParameters(info),
			},
		})
	}
	return result
}

func buildToolParameters(info *schema.ToolInfo) json.RawMessage {
	const fallback = `{"type":"object","properties":{}}`
	if info == nil || info.ParamsOneOf == nil {
		return json.RawMessage(fallback)
	}

	if jsonSchema, err := info.ToJSONSchema(); err == nil && jsonSchema != nil {
		if raw, err := json.Marshal(jsonSchema); err == nil {
			return raw
		}
	}

	return json.RawMessage(fallback)
}

func extractReasoningText(raw json.RawMessage) string {
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString
	}
	return string(raw)
}
