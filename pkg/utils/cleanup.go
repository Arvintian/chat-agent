package utils

import (
	"sync"
)

// CleanupFunc 清理函数类型
type CleanupFunc func()

// CleanupCtx 清理上下文结构
type CleanupRegistry struct {
	mu    sync.Mutex
	funcs []CleanupFunc
}

// NewCleanupCtx 创建清理上下文
func NewCleanupRegistry() *CleanupRegistry {
	return &CleanupRegistry{
		funcs: make([]CleanupFunc, 0),
	}
}

// Register 注册清理函数
func (c *CleanupRegistry) Register(f CleanupFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.funcs = append(c.funcs, f)
}

// Execute 执行所有清理函数（逆序）
func (c *CleanupRegistry) Execute() {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 逆序执行，后注册的先清理
	for i := len(c.funcs) - 1; i >= 0; i-- {
		c.funcs[i]()
	}
	c.funcs = nil
}
