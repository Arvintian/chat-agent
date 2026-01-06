package mcp

import (
	"context"
	"fmt"
	"sync"

	"github.com/Arvintian/chat-agent/pkg/config"

	"github.com/cloudwego/eino/components/tool"
	"github.com/mark3labs/mcp-go/client"
)

// Client MCP client structure
type Client struct {
	mu      sync.RWMutex
	clients map[string]*client.Client
	tools   map[string]tool.BaseTool
	config  *config.Config
}

// NewClient creates a new MCP client
func NewClient(cfg *config.Config) *Client {
	return &Client{
		clients: make(map[string]*client.Client),
		tools:   make(map[string]tool.BaseTool),
		config:  cfg,
	}
}

// Initialize initializes the MCP client
func (c *Client) Initialize(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Validate configuration
	if err := ValidateConfig(c.config); err != nil {
		return NewMCPError("initialize", "", "", fmt.Errorf("configuration validation failed: %w", err))
	}

	// Create clients for each configured MCP server
	for serverName, serverConfig := range c.config.MCPServers {
		client, err := c.createMCPClient(ctx, serverName, serverConfig)
		if err != nil {
			return NewMCPError("initialize", serverName, "", fmt.Errorf("failed to create MCP client: %w", err))
		}
		c.clients[serverName] = client
	}

	// Discover and register all tools
	if err := c.discoverTools(ctx); err != nil {
		return NewMCPError("initialize", "", "", fmt.Errorf("failed to discover MCP tools: %w", err))
	}

	return nil
}

// GetTools gets all available MCP tools
func (c *Client) GetTools() map[string]tool.BaseTool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	tools := make(map[string]tool.BaseTool)
	for name, tool := range c.tools {
		tools[name] = tool
	}
	return tools
}

func (c *Client) GetToolList() []tool.BaseTool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	tools := make([]tool.BaseTool, 0)
	for _, tool := range c.tools {
		tools = append(tools, tool)
	}
	return tools
}

// GetToolsForServers gets tools from specified MCP servers
func (c *Client) GetToolsForServers(serverNames []string) map[string]tool.BaseTool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	tools := make(map[string]tool.BaseTool)
	for _, serverName := range serverNames {
		for toolName, tool := range c.tools {
			// Tool name format: serverName_toolName
			if len(toolName) > len(serverName)+1 && toolName[:len(serverName)+1] == serverName+"_" {
				tools[toolName] = tool
			}
		}
	}
	return tools
}

func (c *Client) GetToolListForServers(serverNames []string) []tool.BaseTool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	tools := make([]tool.BaseTool, 0)
	for _, serverName := range serverNames {
		for toolName, tool := range c.tools {
			// Tool name format: serverName_toolName
			if len(toolName) > len(serverName)+1 && toolName[:len(serverName)+1] == serverName+"_" {
				tools = append(tools, tool)
			}
		}
	}
	return tools
}

// Close closes all MCP client connections
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var errs []error
	for name, client := range c.clients {
		if err := client.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close MCP client %s: %w", name, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors occurred while closing MCP clients: %v", errs)
	}

	return nil
}
