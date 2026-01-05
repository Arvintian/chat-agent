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
}

type Chat struct {
	System      string   `yaml:"system"`
	Model       string   `yaml:"model"`
	MaxMessages int      `yaml:"maxMessages"`
	MCPServers  []string `yaml:"mcp_servers,omitempty"`
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
	URL     string            `yaml:"url,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty"`
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
