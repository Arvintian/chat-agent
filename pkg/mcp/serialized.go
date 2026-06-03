package mcp

import (
	"context"
	"sync"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// serializedTool wraps an InvokableTool and ensures that calls to the same tool
// are serialized using a per-tool mutex. This prevents concurrent calls to
// specific tools that don't support concurrency.
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
