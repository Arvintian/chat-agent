package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

type GetToolsFunc func(params map[string]interface{}) ([]tool.BaseTool, error)

func GetBuiltinTools(category string, params map[string]interface{}) ([]tool.BaseTool, error) {
	switch category {
	case "filesystem":
		return getFileSystemTools(params)
	case "cmd":
		return getCommandTools(params)
	}
	return nil, fmt.Errorf("not found %s tools", category)
}

type toolHelper struct {
	info    *schema.ToolInfo
	handler mcpserver.ToolHandlerFunc
}

func (m *toolHelper) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return m.info, nil
}

func (m *toolHelper) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	var args interface{}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", err
	}
	result, err := m.handler(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      m.info.Name,
			Arguments: args,
		},
	})
	if err != nil {
		return "", err
	}
	marshaledResult, err := sonic.MarshalString(result)
	if err != nil {
		return "", fmt.Errorf("failed to marshal mcp tool result: %w", err)
	}
	if result.IsError {
		return "", fmt.Errorf("failed to call mcp tool, mcp server return error: %s", marshaledResult)
	}

	return marshaledResult, nil
}
