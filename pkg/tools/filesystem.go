package tools

import (
	"context"
	"fmt"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	"github.com/mark3labs/mcp-filesystem-server/filesystemserver"
)

func getFileSystemTools(ctx context.Context, params map[string]interface{}) ([]tool.BaseTool, error) {
	workDir, ok := params["workDir"]
	if !ok {
		return nil, fmt.Errorf("workDir params empty")
	}
	dir, ok := workDir.(string)
	if !ok {
		return nil, fmt.Errorf("workDir params error")
	}

	// Parse exclude list
	var excludeList []string
	if exclude, exists := params["exclude"]; exists {
		switch v := exclude.(type) {
		case []string:
			excludeList = v
		case []interface{}:
			for _, item := range v {
				if s, ok := item.(string); ok {
					excludeList = append(excludeList, s)
				}
			}
		}
	}
	// Create exclude map for fast lookup
	excludeMap := make(map[string]bool)
	for _, name := range excludeList {
		excludeMap[name] = true
	}

	fss, err := filesystemserver.NewFilesystemServer([]string{dir})
	if err != nil {
		return nil, err
	}
	tools := []tool.BaseTool{}
	for _, mcpTool := range fss.ListTools() {
		// Skip excluded tools
		if excludeMap[mcpTool.Tool.Name] {
			continue
		}
		marshaledInputSchema, err := sonic.Marshal(mcpTool.Tool.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("conv mcp tool input schema fail(marshal): %w, tool name: %s", err, mcpTool.Tool.Name)
		}
		inputSchema := &jsonschema.Schema{}
		err = sonic.Unmarshal(marshaledInputSchema, inputSchema)
		if err != nil {
			return nil, fmt.Errorf("conv mcp tool input schema fail(unmarshal): %w, tool name: %s", err, mcpTool.Tool.Name)
		}
		params := schema.NewParamsOneOfByJSONSchema(inputSchema)
		if inputSchema.Properties == nil {
			params = nil
		}
		tools = append(tools, &toolHelper{
			info: &schema.ToolInfo{
				Name:        mcpTool.Tool.Name,
				Desc:        mcpTool.Tool.Description,
				ParamsOneOf: params,
			},
			handler: mcpTool.Handler,
		})
	}
	return tools, nil
}
