package tools

import (
	"fmt"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	"github.com/mark3labs/mcp-filesystem-server/filesystemserver"
)

func getFileSystemTools(workDir string) ([]tool.BaseTool, error) {
	fss, err := filesystemserver.NewFilesystemServer([]string{workDir})
	if err != nil {
		return nil, err
	}
	tools := []tool.BaseTool{}
	for _, mcpTool := range fss.ListTools() {
		marshaledInputSchema, err := sonic.Marshal(mcpTool.Tool.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("conv mcp tool input schema fail(marshal): %w, tool name: %s", err, mcpTool.Tool.Name)
		}
		inputSchema := &jsonschema.Schema{}
		err = sonic.Unmarshal(marshaledInputSchema, inputSchema)
		if err != nil {
			return nil, fmt.Errorf("conv mcp tool input schema fail(unmarshal): %w, tool name: %s", err, mcpTool.Tool.Name)
		}
		tools = append(tools, &toolHelper{
			info: &schema.ToolInfo{
				Name:        mcpTool.Tool.Name,
				Desc:        mcpTool.Tool.Description,
				ParamsOneOf: schema.NewParamsOneOfByJSONSchema(inputSchema),
			},
			handler: mcpTool.Handler,
		})
	}
	return tools, nil
}
