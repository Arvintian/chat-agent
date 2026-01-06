package mcp

import (
	"errors"
	"fmt"
)

// MCP related error definitions
var (
	// ErrMCPNotInitialized MCP manager not initialized
	ErrMCPNotInitialized = errors.New("MCP manager not initialized")

	// ErrServerNotFound MCP server not found
	ErrServerNotFound = errors.New("MCP server not found")

	// ErrToolNotFound MCP tool not found
	ErrToolNotFound = errors.New("MCP tool not found")

	// ErrInvalidConfig Invalid MCP configuration
	ErrInvalidConfig = errors.New("Invalid MCP configuration")

	// ErrConnectionFailed MCP connection failed
	ErrConnectionFailed = errors.New("MCP connection failed")
)

// MCPError MCP error wrapper
type MCPError struct {
	Op     string // Operation name
	Server string // Server name
	Tool   string // Tool name
	Err    error  // Original error
}

// Error implements error interface
func (e *MCPError) Error() string {
	if e.Server != "" && e.Tool != "" {
		return fmt.Sprintf("MCP error [%s] server:%s tool:%s - %v", e.Op, e.Server, e.Tool, e.Err)
	} else if e.Server != "" {
		return fmt.Sprintf("MCP error [%s] server:%s - %v", e.Op, e.Server, e.Err)
	} else {
		return fmt.Sprintf("MCP error [%s] - %v", e.Op, e.Err)
	}
}

// Unwrap returns original error
func (e *MCPError) Unwrap() error {
	return e.Err
}

// NewMCPError creates new MCP error
func NewMCPError(op, server, tool string, err error) *MCPError {
	return &MCPError{
		Op:     op,
		Server: server,
		Tool:   tool,
		Err:    err,
	}
}

// IsConnectionError checks if it's a connection error
func IsConnectionError(err error) bool {
	if mcpErr, ok := err.(*MCPError); ok {
		return errors.Is(mcpErr.Err, ErrConnectionFailed)
	}
	return errors.Is(err, ErrConnectionFailed)
}

// IsConfigError checks if it's a configuration error
func IsConfigError(err error) bool {
	if mcpErr, ok := err.(*MCPError); ok {
		return errors.Is(mcpErr.Err, ErrInvalidConfig)
	}
	return errors.Is(err, ErrInvalidConfig)
}
