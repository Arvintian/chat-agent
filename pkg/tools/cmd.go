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
	Background bool   `json:"background,omitempty"`
}

func (t *RunTerminalCommandTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	bgHelp := `Background Commands:
  bg start <command>       Start a command in background
  bg list                  List all background tasks
  bg show <id>             Show details of a background task
  bg output <id>           Get output of a background task
  bg remove <id>           Remove/kill a background task`

	return &schema.ToolInfo{
		Name: "cmd",
		Desc: fmt.Sprintf(`Execute a terminal command, wait exit and return the output.
Long-running tasks cannot be executed; they will timeout after %v and be killed.
Uses persistent shell sessions (bash on Unix, PowerShell on Windows), current system is %s.
Background task management is available. Use "bg" commands:
%s
`, t.Timeout, runtime.GOOS, bgHelp),
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"command": {
				Type:     schema.String,
				Desc:     "The command to execute (e.g., 'git status', 'ls -la'). Use 'bg list' for background task management.",
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

	if strings.HasPrefix(args.Command, "bg ") {
		return t.handleBackgroundCommand(strings.TrimSpace(strings.TrimPrefix(args.Command, "bg ")))
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

	// Use bash manager for persistent sessions on all platforms
	if t.BashMannger != nil {
		return t.BashMannger.ExecuteCommand(timeoutCtx, args.Command, workingDir, t.Timeout)
	}

	// Fallback with exec for platforms without bash manager support
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(timeoutCtx, "powershell", "-Command", args.Command)
	} else {
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

func (t *RunTerminalCommandTool) handleBackgroundCommand(bgCommand string) (string, error) {
	parts := strings.Fields(bgCommand)
	if len(parts) == 0 {
		return t.formatTaskList()
	}

	command := parts[0]
	args := strings.Join(parts[1:], " ")

	switch command {
	case "start":
		if args == "" {
			return "", fmt.Errorf("usage: bg start <command>")
		}
		task, err := GetTaskManager().StartTask(args, t.WorkingDir)
		if err != nil {
			return "", fmt.Errorf("failed to start background task: %w", err)
		}
		return fmt.Sprintf("Background task started with ID: %s\nUse 'bg output %s' to check output, 'bg show %s' for details", task.ID, task.ID, task.ID), nil

	case "list", "ls":
		return t.formatTaskList()

	case "show":
		if args == "" {
			return "", fmt.Errorf("usage: bg show <task_id>")
		}
		return t.formatTaskDetails(args)

	case "output", "logs":
		if args == "" {
			return "", fmt.Errorf("usage: bg output <task_id>")
		}
		return t.formatTaskOutput(args)

	case "remove", "rm", "kill", "stop":
		if args == "" {
			return "", fmt.Errorf("usage: bg remove <task_id>")
		}
		task, ok := GetTaskManager().GetTask(args)
		if !ok {
			return "", fmt.Errorf("task not found: %s", args)
		}
		if err := GetTaskManager().RemoveTask(args); err != nil {
			return "", fmt.Errorf("failed to remove task: %w", err)
		}
		if task.Status == TaskStatusRunning {
			return fmt.Sprintf("Task %s killed and removed", args), nil
		}
		return fmt.Sprintf("Task %s removed", args), nil

	default:
		return "", fmt.Errorf("unknown bg command: %s\nAvailable commands: start, list, show, output, remove", command)
	}
}

func (t *RunTerminalCommandTool) runInBackground(command, workdir string) (string, error) {
	task, err := GetTaskManager().StartTask(command, workdir)
	if err != nil {
		return "", fmt.Errorf("failed to start background task: %w", err)
	}
	return fmt.Sprintf("Background task started with ID: %s\nCommand: %s\nUse 'bg output %s' to check output", task.ID, command, task.ID), nil
}

func (t *RunTerminalCommandTool) formatTaskList() (string, error) {
	tasks := GetTaskManager().ListTasks()
	if len(tasks) == 0 {
		return "No background tasks", nil
	}

	var sb strings.Builder
	sb.WriteString("Background Tasks:\n")
	sb.WriteString(strings.Repeat("-", 100))
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("%-6s %-10s %-20s %-15s %-30s\n", "ID", "Status", "Duration", "Exit Code", "Command"))
	sb.WriteString(strings.Repeat("-", 100))
	sb.WriteString("\n")

	for _, task := range tasks {
		status := string(task.Status)
		duration := task.GetDuration()
		command := task.Command
		if len(command) > 30 {
			command = command[:27] + "..."
		}

		exitCode := "N/A"
		if task.ExitCode != nil {
			exitCode = fmt.Sprintf("%d", *task.ExitCode)
		}

		sb.WriteString(fmt.Sprintf("%-6s %-10s %-20s %-15s %-30s\n", task.ID, status, duration, exitCode, command))
	}

	return sb.String(), nil
}

func (t *RunTerminalCommandTool) formatTaskDetails(taskID string) (string, error) {
	task, ok := GetTaskManager().GetTask(taskID)
	if !ok {
		return "", fmt.Errorf("task not found: %s", taskID)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Task ID: %s\n", task.ID))
	sb.WriteString(fmt.Sprintf("Status: %s\n", task.Status))
	sb.WriteString(fmt.Sprintf("Command: %s\n", task.Command))
	sb.WriteString(fmt.Sprintf("Working Directory: %s\n", task.WorkingDir))
	sb.WriteString(fmt.Sprintf("Start Time: %s\n", task.StartTime.Format("2006-01-02 15:04:05")))
	if task.EndTime != nil {
		sb.WriteString(fmt.Sprintf("End Time: %s\n", task.EndTime.Format("2006-01-02 15:04:05")))
		sb.WriteString(fmt.Sprintf("Duration: %s\n", task.GetDuration()))
	} else {
		sb.WriteString(fmt.Sprintf("Running for: %s\n", task.GetDuration()))
	}
	if task.ExitCode != nil {
		sb.WriteString(fmt.Sprintf("Exit Code: %d\n", *task.ExitCode))
	}

	return sb.String(), nil
}

func (t *RunTerminalCommandTool) formatTaskOutput(taskID string) (string, error) {
	task, ok := GetTaskManager().GetTask(taskID)
	if !ok {
		return "", fmt.Errorf("task not found: %s", taskID)
	}

	output := task.GetOutputString()
	if output == "" {
		output = "(no output yet)"
	}

	return fmt.Sprintf("Task %s Output:\n%s\n", taskID, output), nil
}
