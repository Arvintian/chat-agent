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
	if v, ok := ctx.Value("cleanup").(*utils.CleanupRegistry); ok {
		v.Register(func() {
			tm := GetTaskManager()
			for _, task := range tm.ListTasks() {
				if task.Status == TaskStatusRunning {
					tm.KillTask(task.ID)
				}
			}
		})
	}
	cmdTool := RunTerminalCommandTool{
		WorkingDir: cfg.WorkingDir,
		Timeout:    time.Duration(cfg.Timeout) * time.Second,
	}
	return []tool.BaseTool{&cmdTool}, nil
}

type RunTerminalCommandTool struct {
	WorkingDir      string        `json:"workDir"`
	Timeout         time.Duration `json:"timeout"`
	AllowedCommands []string
}

type RunTerminalCommandArgs struct {
	Command    string `json:"command"`
	WorkingDir string `json:"working_dir,omitempty"`
	Background bool   `json:"background,omitempty"`
}

func (t *RunTerminalCommandTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "cmd",
		Desc: fmt.Sprintf(`Execute a terminal command, wait exit and return the output, bash on Unix, PowerShell on Windows, current system is %s.
Long-running tasks cannot be executed; they will timeout after %v and be killed. Use background=true to run commands in the background, then use the "cmd_bg" tool to manage background tasks (list, show, output, remove).
`, runtime.GOOS, t.Timeout),
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"command": {
				Type:     schema.String,
				Desc:     "The command to execute (e.g., 'git status', 'ls -la').",
				Required: true,
			},
			"working_dir": {
				Type:     schema.String,
				Desc:     "Optional working directory for the command. Defaults to current directory.",
				Required: false,
			},
			"background": {
				Type:     schema.Boolean,
				Desc:     "Set to true to run the command in the background. Returns immediately with task ID.",
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

	if args.Background {
		return t.runInBackground(args.Command, workingDir)
	}

	// Create command with timeout context
	timeoutCtx, cancel := context.WithTimeout(ctx, t.Timeout)
	defer cancel()

	// Fallback with exec for platforms without bash manager support
	var cmd *exec.Cmd
	platform := getTaskPlatform()
	cmd = platform.createCommand(ctx, args.Command)
	platform.setSysProcAttr(cmd)
	if workingDir != "" {
		cmd.Dir = workingDir
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// cmd run and wait
	err := cmd.Start()
	if err != nil {
		return "", err
	}
	if err := platform.setExitGroup(cmd); err != nil {
		return "", err
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	select {
	case err = <-done:
	case <-timeoutCtx.Done():
		platform.killProcess(cmd.Process)
		err = <-done
		err = fmt.Errorf("command timed out or context canceled, process killed. %v", err)
	}

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

func (t *RunTerminalCommandTool) runInBackground(command, workdir string) (string, error) {
	task, err := GetTaskManager().StartTask(command, workdir)
	if err != nil {
		return "", fmt.Errorf("failed to start background task: %w", err)
	}
	return fmt.Sprintf("Background task started with ID: %s\nCommand: %s\nUse 'cmd_bg' with action='output' and task_id='%s' to check output", task.ID, command, task.ID), nil
}

// Ensure RunTerminalCommandTool implements tool.InvokableTool
var _ tool.InvokableTool = (*RunTerminalCommandTool)(nil)
