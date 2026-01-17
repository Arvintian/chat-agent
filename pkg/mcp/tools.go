package mcp

import (
	"context"
	"fmt"
	"slices"

	"github.com/cloudwego/eino-ext/components/tool/mcp"
	"github.com/cloudwego/eino/components/tool"
	mcpProtocol "github.com/mark3labs/mcp-go/mcp"
)

// discoverTools discovers tools from MCP servers
func (c *Client) discoverTools(ctx context.Context) error {
	for serverName, mcpClient := range c.clients {
		serverConfig := c.config.MCPServers[serverName]
		// Check if client is nil
		if mcpClient == nil {
			return fmt.Errorf("MCP client for server %s is not initialized", serverName)
		}

		// Initialize MCP client connection
		initRequest := mcpProtocol.InitializeRequest{
			Params: mcpProtocol.InitializeParams{
				ProtocolVersion: "2024-11-05",
				ClientInfo: mcpProtocol.Implementation{
					Name:    "chat-agent",
					Version: "1.0.0",
				},
			},
		}

		_, err := mcpClient.Initialize(ctx, initRequest)
		if err != nil {
			return fmt.Errorf("failed to initialize MCP client for server %s: %w", serverName, err)
		}

		// Use eino-ext's mcp package to get tools
		mcpTools, err := mcp.GetTools(ctx, &mcp.Config{Cli: mcpClient})
		if err != nil {
			return fmt.Errorf("failed to get tools from server %s: %w", serverName, err)
		}

		// Add tools to the tool mapping
		for _, mcpTool := range mcpTools {
			// Try to convert BaseTool to InvokableTool
			if invokableTool, ok := mcpTool.(tool.InvokableTool); ok {
				// Get tool info to obtain tool name
				info, err := mcpTool.Info(ctx)
				if err != nil {
					return fmt.Errorf("failed to get tool info: %w", err)
				}
				// Use serverName_toolName as tool name to avoid conflicts
				toolName := fmt.Sprintf("%s_%s", serverName, info.Name)
				if serverConfig.AutoApproval || slices.Contains(serverConfig.AutoApprovalTools, info.Name) {
					c.tools[toolName] = invokableTool
				} else {
					c.tools[toolName] = InvokableApprovableTool{InvokableTool: invokableTool}
				}
			}
		}
	}
	return nil
}
