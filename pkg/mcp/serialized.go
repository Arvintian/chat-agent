package mcp

import (
	"context"
	"sync"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// serializedTool wraps an InvokableTool and ensures that calls are serialized
// using a mutex. The mutex can be per-tool (for NoConcurrentTools) or shared
// across all tools of a server (for server-level NoConcurrent).
type serializedTool struct {
	tool.InvokableTool
	mu *sync.Mutex
}

func (s *serializedTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return s.InvokableTool.Info(ctx)
}

func (s *serializedTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.InvokableTool.InvokableRun(ctx, argumentsInJSON, opts...)
}

// newSerializedTool creates a serialized tool wrapper that uses a per-tool mutex
// to ensure only one call to this specific tool executes at a time.
func newSerializedTool(t tool.InvokableTool) tool.InvokableTool {
	return &serializedTool{
		InvokableTool: t,
		mu:            &sync.Mutex{},
	}
}

// newSerializedToolWithMutex creates a serialized tool wrapper that uses the
// provided mutex. This is used for server-level NoConcurrent where all tools
// from the same server share a single mutex.
func newSerializedToolWithMutex(t tool.InvokableTool, mu *sync.Mutex) tool.InvokableTool {
	return &serializedTool{
		InvokableTool: t,
		mu:            mu,
	}
}

// renamedTool wraps an InvokableTool and overrides the tool name returned by
// Info(). InvokableRun delegates to the underlying tool, so the original
// tool name flows through to the MCP server unchanged. This is used to
// present lowercase tool names to the LLM agent while keeping internal MCP
// communication intact.
type renamedTool struct {
	base tool.InvokableTool // the underlying tool (e.g. toolHelper)
	name string             // new name exposed via Info()
}

func (r *renamedTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	info, err := r.base.Info(ctx)
	if err != nil {
		return nil, err
	}
	copied := *info
	copied.Name = r.name
	return &copied, nil
}

func (r *renamedTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	return r.base.InvokableRun(ctx, argumentsInJSON, opts...)
}

func newRenamedTool(base tool.InvokableTool, name string) tool.InvokableTool {
	return &renamedTool{
		base: base,
		name: name,
	}
}
