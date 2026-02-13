package openrouter

import "encoding/json"

type chatCompletionRequest struct {
	Model            string           `json:"model"`
	Messages         []chatMessage    `json:"messages"`
	Tools            []toolSpec       `json:"tools,omitempty"`
	ToolChoice       string           `json:"tool_choice,omitempty"`
	Reasoning        *reasoningConfig `json:"reasoning,omitempty"`
	IncludeReasoning bool             `json:"include_reasoning,omitempty"`
	Stream           bool             `json:"stream"`
	MaxTokens        *int             `json:"max_tokens,omitempty"`
	Seed             *int             `json:"seed,omitempty"`
	Stop             []string         `json:"stop,omitempty"`
	TopP             *float32         `json:"top_p,omitempty"`
	Temperature      *float32         `json:"temperature,omitempty"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

type chatCompletionResponseChunk struct {
	Choices []struct {
		Delta chatMessage `json:"delta"`
	} `json:"choices"`
}

type chatMessage struct {
	Role             string            `json:"role"`
	Content          string            `json:"content,omitempty"`
	Name             string            `json:"name,omitempty"`
	ToolCallID       string            `json:"tool_call_id,omitempty"`
	ToolCalls        []chatToolCall    `json:"tool_calls,omitempty"`
	Annotations      []annotation      `json:"annotations,omitempty"`
	ReasoningRaw     json.RawMessage   `json:"reasoning,omitempty"`
	ReasoningDetails []json.RawMessage `json:"reasoning_details,omitempty"`
}

type chatToolCall struct {
	Index    *int             `json:"index,omitempty"`
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function chatFunctionCall `json:"function"`
}

type chatFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type toolSpec struct {
	Type     string         `json:"type"`
	Function toolDefinition `json:"function"`
}

type toolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type reasoningConfig struct {
	Effort    string `json:"effort,omitempty"`
	MaxTokens int    `json:"max_tokens,omitempty"`
	Exclude   bool   `json:"exclude,omitempty"`
	Enabled   *bool  `json:"enabled,omitempty"`
}

type annotation struct {
	Type        string       `json:"type"`
	URLCitation *urlCitation `json:"url_citation,omitempty"`
}

type urlCitation struct {
	URL     string `json:"url"`
	Title   string `json:"title"`
	Content string `json:"content"`
}
