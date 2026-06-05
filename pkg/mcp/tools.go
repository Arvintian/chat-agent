package mcp

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/Arvintian/chat-agent/pkg/eino-ext/components/tool/mcp"
	"github.com/cloudwego/eino/components/tool"
	mcpProtocol "github.com/mark3labs/mcp-go/mcp"
)

// toolFiltered checks whether a tool should be filtered based on include/exclude lists.
// Returns true if the tool should be kept, false if it should be filtered out.
func toolFiltered(toolName string, include, exclude []string) bool {
	// If include list is non-empty, only keep tools in the include list
	if len(include) > 0 {
		if !slices.Contains(include, toolName) {
			return false
		}
	}
	// If exclude list is non-empty, remove tools in the exclude list
	if len(exclude) > 0 {
		if slices.Contains(exclude, toolName) {
			return false
		}
	}
	return true
}

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

				toolName := info.Name

				// Optionally lowercase tool name for matching and registration.
				// When enabled, we wrap the tool so that the LLM agent sees a
				// lowercase Function.Name via Info(), while internal MCP
				// communication continues to use the original tool name.
				if serverConfig.LowercaseTools {
					toolName = strings.ToLower(toolName)
					invokableTool = newRenamedTool(invokableTool, toolName)
				}

				// Apply server-level include/exclude filtering
				if !toolFiltered(toolName, serverConfig.Include, serverConfig.Exclude) {
					continue
				}

				// Determine the final invokable tool (wrapping as needed)
				var finalTool tool.InvokableTool

				// Server-level NoConcurrent: all tools from this server share one mutex.
				// Tool-level NoConcurrentTools: each listed tool gets its own mutex.
				// Server-level takes precedence.
				if serverConfig.NoConcurrent {
					if _, ok := c.serverMutexes[serverName]; !ok {
						c.serverMutexes[serverName] = &sync.Mutex{}
					}
					finalTool = newSerializedToolWithMutex(invokableTool, c.serverMutexes[serverName])
				} else if slices.Contains(serverConfig.NoConcurrentTools, toolName) {
					finalTool = newSerializedTool(invokableTool)
				} else {
					finalTool = invokableTool
				}

				// Use serverName_toolName as tool name to avoid conflicts
				fullName := fmt.Sprintf("%s_%s", serverName, toolName)
				if serverConfig.AutoApproval || slices.Contains(serverConfig.AutoApprovalTools, toolName) {
					c.tools[fullName] = finalTool
				} else {
					c.tools[fullName] = InvokableApprovableTool{InvokableTool: finalTool}
				}
			}
		}
	}
	return nil
}
