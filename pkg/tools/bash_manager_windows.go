//go:build windows
// +build windows

package tools

import (
	"context"
	"fmt"
	"time"
)

// BashManager is a stub implementation for Windows
type BashManager struct{}

// NewBashManager creates a new BashManager stub for Windows
func NewBashManager() *BashManager {
	return &BashManager{}
}

// ExecuteCommand executes a bash command (stub for Windows)
func (bm *BashManager) ExecuteCommand(ctx context.Context, command string, workdir string, timeout time.Duration) (string, error) {
	return "", fmt.Errorf("bash command execution not supported on Windows")
}

// Close closes the bash session (stub for Windows)
func (bm *BashManager) Close() {
	// Nothing to close for Windows stub
}
