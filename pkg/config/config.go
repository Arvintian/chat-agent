package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Private global variable to store configuration
var globalConfig *Config

// Config represents the configuration for Eino CLI
type Config struct {
	Chats      map[string]Chat      `yaml:"chats,omitempty"`
	Providers  map[string]Provider  `yaml:"providers,omitempty"`
	Models     map[string]Model     `yaml:"models,omitempty"`
	MCPServers map[string]MCPServer `yaml:"mcpServers,omitempty"`
	Tools      map[string]Tool      `yaml:"tools,omitempty"`
}

// UnmarshalYAML implements custom YAML unmarshaling for backward compatibility.
// It normalizes snake_case keys to camelCase so both styles are accepted.
func (c *Config) UnmarshalYAML(value *yaml.Node) error {
	normalizeNodeKeys(value)
	type plain Config
	return value.Decode((*plain)(c))
}

type Chat struct {
	Desc              string        `yaml:"desc"`
	System            string        `yaml:"system"`
	Model             string        `yaml:"model"`
	MaxMessageRounds  int           `yaml:"maxMessageRounds"`
	FullMessageRounds int           `yaml:"fullMessageRounds,omitempty"`
	MaxIterations     int           `yaml:"maxIterations"`
	MaxRetries        int           `yaml:"maxRetries"`
	MCPServers        []string      `yaml:"mcpServers,omitempty"`
	Skill             *Skill        `yaml:"skill,omitempty"`
	Tools             []string      `yaml:"tools,omitempty"`
	Default           bool          `yaml:"default"`
	Hooks             *SessionHooks `yaml:"hooks,omitempty"`
	Persistence       bool          `yaml:"persistence"`
}

// SessionHooks represents session-related hooks configuration
type SessionHooks struct {
	Keep          *SessionHookConfig `yaml:"keep,omitempty"`
	GenModelInput *SessionHookConfig `yaml:"genModelInput,omitempty"`
}

// SessionHookConfig represents the configuration for a single hook
type SessionHookConfig struct {
	Enabled    bool              `yaml:"enabled"`
	Type       string            `yaml:"type,omitempty"`    // "script" or "http", default is "script"
	ScriptPath string            `yaml:"scriptPath"`        // used when type is "script"
	URL        string            `yaml:"url,omitempty"`     // used when type is "http"
	Method     string            `yaml:"method,omitempty"`  // HTTP method for http type, default is "POST"
	Headers    map[string]string `yaml:"headers,omitempty"` // HTTP headers for http type
	Args       []string          `yaml:"args,omitempty"`
	Timeout    int               `yaml:"timeout,omitempty"` // in seconds, default is 30
	Env        map[string]string `yaml:"env,omitempty"`     // environment variables for the hook script
}

type Skill struct {
	Dir               string   `yaml:"dir"`
	WorkDir           string   `yaml:"workDir"`
	Timeout           int      `yaml:"timeout"`
	AutoApproval      bool     `yaml:"autoApproval"`
	AutoApprovalTools []string `yaml:"autoApprovalTools"`
}

// Provider represents AI provider configuration
type Provider struct {
	Type    string `yaml:"type"`
	BaseURL string `yaml:"baseUrl,omitempty"`
	APIKey  string `yaml:"apiKey,omitempty"`
}

// Model represents AI model configuration
type Model struct {
	Provider        string         `yaml:"provider"`
	Model           string         `yaml:"model"`
	Thinking        bool           `yaml:"thinking"`
	ReasoningEffort *string        `yaml:"reasoningEffort"`
	MaxTokens       int            `yaml:"maxTokens,omitempty"`
	Temperature     float64        `yaml:"temperature,omitempty"`
	TopP            float64        `yaml:"topP,omitempty"`
	TopK            int            `yaml:"topK,omitempty"`
	ExtraBody       map[string]any `yaml:"extraBody"`
}

// MCPServer represents MCP server configuration
type MCPServer struct {
	Type string `yaml:"type"`
	// for stdio
	Cmd  string            `yaml:"cmd,omitempty"`
	Args []string          `yaml:"args,omitempty"`
	Env  map[string]string `yaml:"env,omitempty"`
	// for sse & streamable-http
	URL               string            `yaml:"url,omitempty"`
	Headers           map[string]string `yaml:"headers,omitempty"`
	AutoApproval      bool              `yaml:"autoApproval"`
	AutoApprovalTools []string          `yaml:"autoApprovalTools"`
	// Tool filtering: include only these tools (if non-empty, only these tools are kept)
	Include []string `yaml:"include,omitempty"`
	// Tool filtering: exclude these tools (if non-empty, these tools are removed)
	Exclude []string `yaml:"exclude,omitempty"`
	// NoConcurrent: if true, all tools from this server share a single mutex,
	// meaning no two tools from this server can run concurrently.
	NoConcurrent bool `yaml:"noConcurrent,omitempty"`
	// NoConcurrentTools: list of specific tool names that should NOT be called concurrently.
	// Each listed tool gets its own mutex, so different tools don't block each other.
	NoConcurrentTools []string `yaml:"noConcurrentTools,omitempty"`
}

type Tool struct {
	Category          string                 `yaml:"category"`
	Params            map[string]interface{} `yaml:"params"`
	AutoApproval      bool                   `yaml:"autoApproval"`
	AutoApprovalTools []string               `yaml:"autoApprovalTools"`
}

// LoadConfig loads configuration from file and saves to global variable
func LoadConfig(configPath string) (*Config, error) {
	// Check if configuration file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("configuration file does not exist: %s", configPath)
	}

	// Read configuration file
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read configuration file: %w", err)
	}

	// Parse YAML
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse configuration file: %w", err)
	}

	// Save to global variable
	globalConfig = &cfg

	return &cfg, nil
}

// GetConfig gets global configuration
func GetConfig() *Config {
	return globalConfig
}

// normalizeNodeKeys recursively normalizes mapping node keys from snake_case to camelCase.
// This provides backward compatibility: old configs with snake_case keys still work.
func normalizeNodeKeys(node *yaml.Node) {
	if node == nil {
		return
	}
	switch node.Kind {
	case yaml.DocumentNode:
		for _, child := range node.Content {
			normalizeNodeKeys(child)
		}
	case yaml.MappingNode:
		// Mapping nodes have Content as pairs: [key, value, key, value, ...]
		for i := 0; i < len(node.Content); i += 2 {
			keyNode := node.Content[i]
			valueNode := node.Content[i+1]
			if keyNode.Kind == yaml.ScalarNode && keyNode.Tag == "!!str" {
				keyNode.Value = snakeToCamel(keyNode.Value)
			}
			normalizeNodeKeys(valueNode)
		}
	case yaml.SequenceNode:
		for _, child := range node.Content {
			normalizeNodeKeys(child)
		}
	}
}

// snakeToCamel converts a snake_case string to camelCase.
func snakeToCamel(s string) string {
	parts := strings.Split(s, "_")
	if len(parts) <= 1 {
		return s
	}
	for i := 1; i < len(parts); i++ {
		if len(parts[i]) > 0 {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}
