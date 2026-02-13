package openrouter

import (
	"net/http"
	"time"

	"github.com/cloudwego/eino/schema"
)

// ChatModel implements eino's ToolCallingChatModel interface for the OpenRouter API.
type ChatModel struct {
	headers    http.Header
	client     *http.Client
	boundTools []*schema.ToolInfo
	reasoning  *reasoningConfig
	config     *Config
}

// Config contains the necessary parameters for constructing an OpenRouter chat model.
type Config struct {
	APIKey      string
	Model       string
	BaseURL     string
	AppName     string
	Referer     string
	HTTPClient  *http.Client
	Timeout     time.Duration
	Reasoning   *ReasoningConfig
	MaxTokens   *int
	Seed        *int
	Stop        []string
	TopP        *float32
	Temperature *float32
}

const (
	EffortOfNone   = "none"
	EffortOfLow    = "low"
	EffortOfMedium = "medium"
	EffortOfHigh   = "high"
)

// ReasoningConfig mirrors OpenRouter's reasoning parameter.
type ReasoningConfig struct {
	Effort    string
	MaxTokens int
	Exclude   bool
	Enabled   *bool
}
