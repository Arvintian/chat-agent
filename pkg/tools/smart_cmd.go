package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/Arvintian/chat-agent/pkg/mcp"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

func getSmartCommandTools(ctx context.Context, params map[string]interface{}) ([]tool.BaseTool, error) {
	cmdTool, err := getCommandTools(ctx, params)
	if err != nil {
		return nil, err
	}
	innerCmdTool := cmdTool[0].(*RunTerminalCommandTool)
	smartCmdTool := NewSmartCmdTool(innerCmdTool)
	return []tool.BaseTool{smartCmdTool}, nil
}

// SmartCmdTool wraps cmd tool with intelligent permission control
type SmartCmdTool struct {
	baseTool          *RunTerminalCommandTool
	dangerousPatterns []*regexp.Regexp
	dangerousCommands []string
}

func NewSmartCmdTool(baseTool *RunTerminalCommandTool) *SmartCmdTool {
	// Pre-compile dangerous patterns at initialization time
	dangerousPatterns := []*regexp.Regexp{
		// File system operations
		regexp.MustCompile(`^\s*(rm\s+.*-rf|rm\s+-rf\s+.*|rm\s+.*\s+-rf)`),
		regexp.MustCompile(`^\s*rm\s+.*/\s*$`),
		regexp.MustCompile(`^\s*rm\s+.*\.\./`),
		regexp.MustCompile(`^\s*rm\s+.*/etc/`),
		regexp.MustCompile(`^\s*rm\s+.*/usr/`),
		regexp.MustCompile(`^\s*rm\s+.*/var/`),
		regexp.MustCompile(`^\s*rm\s+.*/lib/`),
		regexp.MustCompile(`^\s*rm\s+.*/bin/`),
		regexp.MustCompile(`^\s*rm\s+.*/sbin/`),
		regexp.MustCompile(`^\s*rm\s+.*/boot/`),
		regexp.MustCompile(`^\s*rm\s+.*/root/`),
		regexp.MustCompile(`^\s*rm\s+.*/home/`),

		// System operations
		regexp.MustCompile(`^\s*(shutdown|halt|poweroff|reboot)`),
		regexp.MustCompile(`^\s*dd\s+.*`),
		regexp.MustCompile(`^\s*mkfs\s+.*`),
		regexp.MustCompile(`^\s*fdisk\s+.*`),
		regexp.MustCompile(`^\s*parted\s+.*`),

		// Network operations
		regexp.MustCompile(`^\s*iptables\s+.*`),
		regexp.MustCompile(`^\s*ufw\s+.*`),
		regexp.MustCompile(`^\s*nft\s+.*`),

		// Process operations
		regexp.MustCompile(`^\s*kill\s+.*-9`),
		regexp.MustCompile(`^\s*killall\s+.*`),
		regexp.MustCompile(`^\s*pkill\s+.*`),

		// User/group operations
		regexp.MustCompile(`^\s*user(del|add|mod)\s+.*`),
		regexp.MustCompile(`^\s*group(del|add|mod)\s+.*`),
		regexp.MustCompile(`^\s*passwd\s+.*`),

		// Permission operations
		regexp.MustCompile(`^\s*chmod\s+.*777`),
		regexp.MustCompile(`^\s*chmod\s+.*000`),
		regexp.MustCompile(`^\s*chown\s+.*root:`),
		regexp.MustCompile(`^\s*chown\s+.*:root`),

		// Package management (dangerous operations)
		regexp.MustCompile(`^\s*(apt|yum|dnf|pacman|apk|zypper|emerge|port)\s+(remove|purge|autoremove|erase|uninstall|clean|-Rns|-rns)\s+.*`),
		regexp.MustCompile(`^\s*(apt|yum|dnf|pacman|apk|zypper|emerge|port)\s+.*--force`),

		// Shell operations
		regexp.MustCompile(`^\s*(bash|sh|zsh|dash|ksh|tcsh|csh|fish)\s+-c\s+.*(rm|dd|mkfs|fdisk|wipe|shred|kill).*`),

		// Data destruction
		regexp.MustCompile(`^\s*wipe\s+.*`),
		regexp.MustCompile(`^\s*shred\s+.*`),

		// Cryptocurrency mining (often malicious)
		regexp.MustCompile(`^\s*(xmrig|ccminer|minerd|cpuminer|nicehash|ethminer|gminer).*`),

		// Reverse shells and network connections
		regexp.MustCompile(`^\s*(nc|netcat|socat|telnet|ncat)\s+.*`),
		regexp.MustCompile(`^\s*curl\s+.*\|\s*(bash|sh|zsh|dash|ksh|tcsh|csh|fish)`),
		regexp.MustCompile(`^\s*wget\s+.*\|\s*(bash|sh|zsh|dash|ksh|tcsh|csh|fish)`),

		// Database operations
		regexp.MustCompile(`^\s*(mysql|psql|mongosh|sqlite3|mongo|redis-cli|sqlcmd)\s+.*(drop|delete|truncate|erase|remove|purge).*`),
	}

	// Pre-defined dangerous commands
	dangerousCommands := []string{
		"rm -rf",
		"rm -r -f",
		"rm -f -r",
		":(){ :|:& };:", // fork bomb
		"chmod 777 /",
		"chmod 000 /",
		"nohup",
	}

	return &SmartCmdTool{
		baseTool:          baseTool,
		dangerousPatterns: dangerousPatterns,
		dangerousCommands: dangerousCommands,
	}
}

func (t *SmartCmdTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	baseInfo, err := t.baseTool.Info(ctx)
	if err != nil {
		return nil, err
	}
	return baseInfo, nil
}

func (t *SmartCmdTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	var args RunTerminalCommandArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	if args.Command == "" {
		return "", fmt.Errorf("command is required")
	}

	// Check if command is dangerous
	if t.isDangerousCommand(args.Command) {
		// This is a dangerous command, require approval
		return t.requireApproval(ctx, argumentsInJSON, opts...)
	}

	// Safe command, execute directly
	return t.baseTool.InvokableRun(ctx, argumentsInJSON, opts...)
}

func (t *SmartCmdTool) isDangerousCommand(command string) bool {
	// Convert to lowercase for case-insensitive matching
	cmdLower := strings.ToLower(strings.TrimSpace(command))

	// Check against pre-compiled dangerous patterns
	for _, pattern := range t.dangerousPatterns {
		if pattern.MatchString(cmdLower) {
			return true
		}
	}

	// Check for specific dangerous commands with arguments
	for _, dangerousCmd := range t.dangerousCommands {
		if strings.Contains(cmdLower, dangerousCmd) {
			return true
		}
	}

	return false
}

func (t *SmartCmdTool) requireApproval(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	toolInfo, err := t.Info(ctx)
	if err != nil {
		return "", err
	}

	wasInterrupted, _, storedArguments := compose.GetInterruptState[string](ctx)
	if !wasInterrupted {
		// First time, interrupt for approval
		return "", compose.StatefulInterrupt(ctx, &mcp.ApprovalInfo{
			ToolName:        toolInfo.Name,
			ArgumentsInJSON: argumentsInJSON,
			ToolCallID:      compose.GetToolCallID(ctx),
		}, argumentsInJSON)
	}

	isResumeTarget, hasData, data := compose.GetResumeContext[*mcp.ApprovalResult](ctx)
	if !isResumeTarget {
		// Was interrupted but not resumed, re-interrupt
		return "", compose.StatefulInterrupt(ctx, &mcp.ApprovalInfo{
			ToolName:        toolInfo.Name,
			ArgumentsInJSON: storedArguments,
			ToolCallID:      compose.GetToolCallID(ctx),
		}, storedArguments)
	}

	if !hasData {
		return "", fmt.Errorf("tool '%s' resumed with no data", toolInfo.Name)
	}

	if data.Approved {
		// User approved, execute the command
		return t.baseTool.InvokableRun(ctx, storedArguments, opts...)
	}

	if data.DisapproveReason != nil {
		return fmt.Sprintf("tool '%s' disapproved, reason: %s", toolInfo.Name, *data.DisapproveReason), nil
	}

	return fmt.Sprintf("tool '%s' disapproved", toolInfo.Name), nil
}

// Ensure SmartCmdTool implements tool.InvokableTool
var _ tool.InvokableTool = (*SmartCmdTool)(nil)
