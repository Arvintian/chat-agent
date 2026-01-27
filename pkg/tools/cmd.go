package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/Arvintian/chat-agent/pkg/utils"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

const DEFAULT_CMD_TIMEOUT = 5

func getCommandTools(ctx context.Context, params map[string]interface{}) ([]tool.BaseTool, error) {
	var cfg RunTerminalCommandTool
	bts, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(bts), &cfg); err != nil {
		return nil, err
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DEFAULT_CMD_TIMEOUT
	}
	bash := NewBashManager()
	if v, ok := ctx.Value("cleanup").(*utils.CleanupRegistry); ok {
		v.Register(func() {
			bash.Close()
		})
	}
	cmdTool := RunTerminalCommandTool{
		WorkingDir:  cfg.WorkingDir,
		Timeout:     time.Duration(cfg.Timeout) * time.Second,
		BashMannger: bash,
	}
	return []tool.BaseTool{&cmdTool}, nil
}

type RunTerminalCommandTool struct {
	BashMannger     *BashManager
	WorkingDir      string        `json:"workDir"`
	Timeout         time.Duration `json:"timeout"`
	AllowedCommands []string
}

type RunTerminalCommandArgs struct {
	Command    string `json:"command"`
	WorkingDir string `json:"working_dir,omitempty"`
}

func (t *RunTerminalCommandTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "cmd",
		Desc: fmt.Sprintf(`Execute a terminal command, wait exit and return the output.
Long-running tasks cannot be executed; they will timeout after %v and be killed.
Windows systems will use PowerShell for execution, while other platforms will use shell, current system is %s.`, t.Timeout, runtime.GOOS),
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"command": {
				Type:     schema.String,
				Desc:     "The command to execute (e.g., 'git status', 'ls -la')",
				Required: true,
			},
			"working_dir": {
				Type:     schema.String,
				Desc:     "Optional working directory for the command. Defaults to current directory.",
				Required: false,
			},
		}),
	}, nil
}

// InvokableRun executes the tool and returns the command output.
func (t *RunTerminalCommandTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	var args RunTerminalCommandArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	if args.Command == "" {
		return "", fmt.Errorf("command is required")
	}

	// Check allowed commands if configured
	if len(t.AllowedCommands) > 0 {
		allowed := false
		for _, prefix := range t.AllowedCommands {
			if strings.HasPrefix(args.Command, prefix) {
				allowed = true
				break
			}
		}
		if !allowed {
			return "", fmt.Errorf("command not allowed: %s", args.Command)
		}
	}

	// Determine working directory
	workingDir := t.WorkingDir
	if args.WorkingDir != "" {
		workingDir = args.WorkingDir
	}

	// Create command with timeout context
	timeoutCtx, cancel := context.WithTimeout(ctx, t.Timeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(timeoutCtx, "powershell", "-Command", args.Command)
	} else {
		if t.BashMannger != nil {
			return t.BashMannger.ExecuteCommand(timeoutCtx, args.Command, workingDir, t.Timeout)
		}
		// fallback with exec
		cmd = exec.CommandContext(timeoutCtx, "sh", "-c", args.Command)
	}

	if workingDir != "" {
		cmd.Dir = workingDir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	// Build result
	var result strings.Builder
	if stdout.Len() > 0 {
		result.WriteString("STDOUT:\n")
		result.WriteString(stdout.String())
	}
	if stderr.Len() > 0 {
		if result.Len() > 0 {
			result.WriteString("\n")
		}
		result.WriteString("STDERR:\n")
		result.WriteString(stderr.String())
	}

	if err != nil {
		if result.Len() > 0 {
			result.WriteString("\n")
		}
		result.WriteString(fmt.Sprintf("EXIT ERROR: %v", err))
	}

	if result.Len() == 0 {
		return "(command completed with no output)", nil
	}

	return result.String(), nil
}

// Ensure RunTerminalCommandTool implements tool.InvokableTool
var _ tool.InvokableTool = (*RunTerminalCommandTool)(nil)
