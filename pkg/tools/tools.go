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

var ExemptAutoApprovalTools = []string{"cmd_bg", "smart_cmd"}

func GetBuiltinTools(ctx context.Context, category string, params map[string]interface{}) ([]tool.BaseTool, error) {
	switch category {
	case "filesystem":
		return getFileSystemTools(ctx, params)
	case "cmd":
		return getCommandTools(ctx, params)
	case "smart_cmd":
		return getSmartCommandTools(ctx, params)
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
		return fmt.Sprintf("failed to parse arguments: %v", err), nil
	}
	result, err := m.handler(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      m.info.Name,
			Arguments: args,
		},
	})
	if err != nil {
		return fmt.Sprintf("failed to call mcp tool: %v", err), nil
	}
	marshaledResult, err := sonic.MarshalString(result)
	if err != nil {
		return "", fmt.Errorf("failed to marshal mcp tool result: %w", err)
	}
	if result.IsError {
		return marshaledResult, err
	}

	return marshaledResult, nil
}
