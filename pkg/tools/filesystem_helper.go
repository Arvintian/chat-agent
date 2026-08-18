package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

var fileLocks sync.Map // map[string]*sync.Mutex

// fileWriteToolPaths maps tool names to the argument keys whose values are file paths to lock on.
var fileWriteToolPaths = map[string][]string{
	"modify_file": {"path"},
	"write_file":  {"path"},
	"delete_file": {"path"},
	"move_file":   {"source", "destination"},
	"copy_file":   {"source", "destination"},
}

type lockedToolHelper struct {
	info     *schema.ToolInfo
	handler  mcpserver.ToolHandlerFunc
	pathKeys []string
}

func (m *lockedToolHelper) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return m.info, nil
}

func (m *lockedToolHelper) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return fmt.Sprintf("failed to parse arguments: %v", err), nil
	}

	// Extract paths and sort for consistent lock ordering
	paths := make([]string, 0, len(m.pathKeys))
	for _, key := range m.pathKeys {
		if p, ok := args[key].(string); ok && p != "" {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)

	// Acquire locks
	var unlockFns []func()
	for _, p := range paths {
		mu, _ := fileLocks.LoadOrStore(p, &sync.Mutex{})
		mu.(*sync.Mutex).Lock()
		unlockFns = append(unlockFns, func() { mu.(*sync.Mutex).Unlock() })
	}
	defer func() {
		for i := len(unlockFns) - 1; i >= 0; i-- {
			unlockFns[i]()
		}
	}()

	result, err := m.handler(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      m.info.Name,
			Arguments: args,
		},
	})
	if err != nil {
		return fmt.Sprintf("failed to call tool: %v", err), nil
	}
	marshaledResult, err := sonic.MarshalString(result)
	if err != nil {
		return "", fmt.Errorf("failed to marshal tool result: %w", err)
	}
	if result.IsError {
		return marshaledResult, err
	}
	return marshaledResult, nil
}
