package mcp

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/Arvintian/chat-agent/pkg/config"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
)

// createMCPClient creates MCP client based on configuration
func (c *Client) createMCPClient(ctx context.Context, serverName string, serverConfig config.MCPServer) (*client.Client, error) {
	switch serverConfig.Type {
	case "stdio", "STDIO":
		return c.createStdioClient(ctx, serverConfig)
	case "sse", "SSE":
		return c.createSSEClient(ctx, serverConfig)
	case "streamable-http", "STREAMABLE-HTTP", "http", "HTTP":
		return c.createStreamableHTTPClient(ctx, serverConfig)
	default:
		return nil, fmt.Errorf("unsupported MCP server type: %s", serverConfig.Type)
	}
}

// createStdioClient creates STDIO type MCP client
func (c *Client) createStdioClient(ctx context.Context, serverConfig config.MCPServer) (*client.Client, error) {
	if serverConfig.Cmd == "" {
		return nil, fmt.Errorf("STDIO type MCP server must specify cmd")
	}

	// Prepare environment variables
	var env []string
	if len(serverConfig.Env) > 0 {
		for key, value := range serverConfig.Env {
			env = append(env, fmt.Sprintf("%s=%s", key, value))
		}
	}

	// Create STDIO client
	mcpClient, err := client.NewStdioMCPClient(serverConfig.Cmd, env, serverConfig.Args...)
	if err != nil {
		return nil, fmt.Errorf("failed to create STDIO MCP client: %w", err)
	}

	return mcpClient, nil
}

// createStreamableHTTPClient creates StreamableHTTP type MCP client
func (c *Client) createStreamableHTTPClient(ctx context.Context, serverConfig config.MCPServer) (*client.Client, error) {
	if serverConfig.URL == "" {
		return nil, fmt.Errorf("StreamableHTTP type MCP server must specify URL")
	}

	// Prepare client options
	var options []transport.StreamableHTTPCOption

	// Add request headers
	if len(serverConfig.Headers) > 0 {
		options = append(options, transport.WithHTTPHeaders(serverConfig.Headers))
	}

	// Set default timeout
	options = append(options, transport.WithHTTPTimeout(30*time.Second))

	// Create StreamableHTTP client
	mcpClient, err := client.NewStreamableHttpClient(serverConfig.URL, options...)
	if err != nil {
		return nil, fmt.Errorf("failed to create StreamableHTTP MCP client: %w", err)
	}

	if err := mcpClient.Start(ctx); err != nil {
		return nil, err
	}

	return mcpClient, nil
}

// createSSEClient creates SSE type MCP client
func (c *Client) createSSEClient(ctx context.Context, serverConfig config.MCPServer) (*client.Client, error) {
	if serverConfig.URL == "" {
		return nil, fmt.Errorf("SSE type MCP server must specify URL")
	}

	// Validate URL format
	if _, err := url.Parse(serverConfig.URL); err != nil {
		return nil, fmt.Errorf("invalid SSE server URL: %w", err)
	}

	// Prepare client options
	var options []transport.ClientOption
	if len(serverConfig.Headers) > 0 {
		options = append(options, transport.WithHeaders(serverConfig.Headers))
	}

	// Create SSE client
	mcpClient, err := client.NewSSEMCPClient(serverConfig.URL, options...)
	if err != nil {
		return nil, fmt.Errorf("failed to create SSE MCP client: %w", err)
	}

	if err := mcpClient.Start(ctx); err != nil {
		return nil, err
	}

	return mcpClient, nil
}
