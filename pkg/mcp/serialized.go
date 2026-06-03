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
