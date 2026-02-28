package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Private global variable to store configuration
var globalConfig *Config

// Config represents the configuration for Eino CLI
type Config struct {
	Chats      map[string]Chat      `yaml:"chats,omitempty"`
	Providers  map[string]Provider  `yaml:"providers,omitempty"`
	Models     map[string]Model     `yaml:"models,omitempty"`
	MCPServers map[string]MCPServer `yaml:"mcp_servers,omitempty"`
	Tools      map[string]Tool      `yaml:"tools,omitempty"`
}

type Chat struct {
	Desc          string        `yaml:"desc"`
	System        string        `yaml:"system"`
	Model         string        `yaml:"model"`
	MaxMessages   int           `yaml:"maxMessages"`
	MaxIterations int           `yaml:"maxIterations"`
	MaxRetries    int           `yaml:"maxRetries"`
	MCPServers    []string      `yaml:"mcp_servers,omitempty"`
	Skill         *Skill        `yaml:"skill,omitempty"`
	Tools         []string      `yaml:"tools,omitempty"`
	Default       bool          `yaml:"default"`
	Hooks         *SessionHooks `yaml:"hooks,omitempty"`
}

// SessionHooks represents session-related hooks configuration
type SessionHooks struct {
	Keep          *SessionHookConfig `yaml:"keep,omitempty"`
	GenModelInput *SessionHookConfig `yaml:"genModelInput,omitempty"`
}

// SessionHookConfig represents the configuration for a single hook
type SessionHookConfig struct {
	Enabled    bool              `yaml:"enabled"`
	Type       string            `yaml:"type,omitempty"` // "script" or "http", default is "script"
	ScriptPath string            `yaml:"script_path"`    // used when type is "script"
	URL        string            `yaml:"url,omitempty"`  // used when type is "http"
	Method     string            `yaml:"method,omitempty"` // HTTP method for http type, default is "POST"
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
	BaseURL string `yaml:"base_url,omitempty"`
	APIKey  string `yaml:"api_key,omitempty"`
}

// Model represents AI model configuration
type Model struct {
	Provider    string  `yaml:"provider"`
	Model       string  `yaml:"model"`
	Thinking    bool    `yaml:"thinking"`
	MaxTokens   int     `yaml:"max_tokens,omitempty"`
	Temperature float64 `yaml:"temperature,omitempty"`
	TopP        float64 `yaml:"top_p,omitempty"`
	TopK        int     `yaml:"top_k,omitempty"`
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
