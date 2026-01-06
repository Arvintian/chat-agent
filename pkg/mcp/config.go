package mcp

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"chat-agent/pkg/config"
)

// ValidateConfig validates MCP configuration
func ValidateConfig(cfg *config.Config) error {
	if cfg == nil {
		return NewMCPError("validate", "", "", ErrInvalidConfig)
	}

	// Validate MCP server configurations
	for serverName, serverConfig := range cfg.MCPServers {
		if err := validateServerConfig(serverName, serverConfig); err != nil {
			return err
		}
	}

	// Validate agent's MCP server references
	for agentName, agentConfig := range cfg.Chats {
		for _, serverName := range agentConfig.MCPServers {
			if _, exists := cfg.MCPServers[serverName]; !exists {
				return NewMCPError("validate", serverName, "",
					fmt.Errorf("chat %s references non-existent MCP server: %s", agentName, serverName))
			}
		}
	}

	return nil
}

// validateServerConfig validates a single MCP server configuration
func validateServerConfig(serverName string, serverConfig config.MCPServer) error {
	// Validate server name
	if strings.TrimSpace(serverName) == "" {
		return NewMCPError("validate", serverName, "",
			fmt.Errorf("MCP server name cannot be empty"))
	}

	// Validate command or URL
	if serverConfig.Cmd == "" && serverConfig.URL == "" {
		return NewMCPError("validate", serverName, "",
			fmt.Errorf("MCP server must specify cmd or url"))
	}

	// If both cmd and url are specified, return error
	if serverConfig.Cmd != "" && serverConfig.URL != "" {
		return NewMCPError("validate", serverName, "",
			fmt.Errorf("MCP server cannot specify both cmd and url"))
	}

	// Validate command path
	if serverConfig.Cmd != "" {
		if err := validateCommand(serverName, serverConfig.Cmd); err != nil {
			return err
		}
	}

	// Validate URL format
	if serverConfig.URL != "" {
		if err := validateURL(serverName, serverConfig.URL); err != nil {
			return err
		}
	}

	return nil
}

// validateCommand validates command path
func validateCommand(serverName, command string) error {
	// Parse command (may contain parameters)
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return NewMCPError("validate", serverName, "",
			fmt.Errorf("command cannot be empty"))
	}

	cmdPath := parts[0]

	// If relative path, check if exists
	if !filepath.IsAbs(cmdPath) {
		// Check if command exists in PATH
		if _, err := os.Stat(cmdPath); err != nil {
			// If not in current directory, try to find in PATH
			if _, pathErr := exec.LookPath(cmdPath); pathErr != nil {
				return NewMCPError("validate", serverName, "",
					fmt.Errorf("command does not exist: %s", cmdPath))
			}
		}
	} else {
		// Absolute path, directly check if file exists
		if _, err := os.Stat(cmdPath); err != nil {
			return NewMCPError("validate", serverName, "",
				fmt.Errorf("command file does not exist: %s", cmdPath))
		}
	}

	return nil
}

// validateURL validates URL format
func validateURL(serverName, url string) error {
	// Simple URL format validation
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "ws://") && !strings.HasPrefix(url, "wss://") {
		return NewMCPError("validate", serverName, "",
			fmt.Errorf("invalid URL format: %s, must start with http://, https://, ws:// or wss://", url))
	}

	return nil
}

// GetServerConfig gets configuration for specified server
func GetServerConfig(cfg *config.Config, serverName string) (*config.MCPServer, error) {
	if cfg == nil {
		return nil, NewMCPError("get_config", serverName, "", ErrInvalidConfig)
	}

	serverConfig, exists := cfg.MCPServers[serverName]
	if !exists {
		return nil, NewMCPError("get_config", serverName, "", ErrServerNotFound)
	}

	return &serverConfig, nil
}

// GetAgentMCPServers gets MCP server list for specified agent
func GetAgentMCPServers(cfg *config.Config, agentName string) ([]string, error) {
	if cfg == nil {
		return nil, NewMCPError("get_agent_servers", "", "", ErrInvalidConfig)
	}

	agentConfig, exists := cfg.Chats[agentName]
	if !exists {
		return nil, NewMCPError("get_agent_servers", "", "",
			fmt.Errorf("agent configuration does not exist: %s", agentName))
	}

	return agentConfig.MCPServers, nil
}
