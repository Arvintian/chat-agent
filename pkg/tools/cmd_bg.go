package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type RunBackgroundCommandTool struct {
	TaskManager *BackgroundTaskManager
}

type RunBackgroundCommandArgs struct {
	Action string `json:"action"`
	TaskID string `json:"task_id,omitempty"`
}

func (t *RunBackgroundCommandTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "cmd_bg",
		Desc: `Manage background tasks.`,
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"action": {
				Type: schema.String,
				Desc: `Action to perform: list, show, output, remove.
- list: List all background tasks
- show: Show details of a task
- output: Get output of a task
- remove: Remove/kill a task`,
				Required: true,
			},
			"task_id": {
				Type:     schema.String,
				Desc:     "Task ID (required for show, output, remove actions)",
				Required: false,
			},
		}),
	}, nil
}

func (t *RunBackgroundCommandTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	var args RunBackgroundCommandArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	if args.Action == "" {
		return "", fmt.Errorf("action is required")
	}

	switch args.Action {
	case "list", "ls":
		tasks := t.TaskManager.ListTasks()
		if len(tasks) == 0 {
			return "No background tasks", nil
		}

		var result strings.Builder
		result.WriteString("Background Tasks:\n")
		result.WriteString(strings.Repeat("-", 100))
		result.WriteString("\n")
		result.WriteString(fmt.Sprintf("%-6s %-10s %-20s %-15s %-30s\n", "ID", "Status", "Duration", "Exit Code", "Command"))
		result.WriteString(strings.Repeat("-", 100))
		result.WriteString("\n")

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

			result.WriteString(fmt.Sprintf("%-6s %-10s %-20s %-15s %-30s\n", task.ID, status, duration, exitCode, command))
		}

		return result.String(), nil

	case "show":
		if args.TaskID == "" {
			return "", fmt.Errorf("task_id is required for show action")
		}
		return t.formatTaskDetails(args.TaskID)

	case "output", "logs":
		if args.TaskID == "" {
			return "", fmt.Errorf("task_id is required for output action")
		}
		return t.formatTaskOutput(args.TaskID)

	case "remove", "rm", "kill", "stop":
		if args.TaskID == "" {
			return "", fmt.Errorf("task_id is required for remove action")
		}
		task, ok := t.TaskManager.GetTask(args.TaskID)
		if !ok {
			return "", fmt.Errorf("task not found: %s", args.TaskID)
		}
		if err := t.TaskManager.RemoveTask(args.TaskID); err != nil {
			return "", fmt.Errorf("failed to remove task: %w", err)
		}
		if task.Status == TaskStatusRunning {
			return fmt.Sprintf("Task %s killed and removed", args.TaskID), nil
		}
		return fmt.Sprintf("Task %s removed", args.TaskID), nil

	default:
		return "", fmt.Errorf("unknown action: %s\nAvailable actions: list, show, output, remove", args.Action)
	}
}

var _ tool.InvokableTool = (*RunBackgroundCommandTool)(nil)

func (t *RunBackgroundCommandTool) formatTaskDetails(taskID string) (string, error) {
	task, ok := t.TaskManager.GetTask(taskID)
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

func (t *RunBackgroundCommandTool) formatTaskOutput(taskID string) (string, error) {
	task, ok := t.TaskManager.GetTask(taskID)
	if !ok {
		return "", fmt.Errorf("task not found: %s", taskID)
	}

	output := task.GetOutputString()
	if output == "" {
		output = "(no output yet)"
	}

	return fmt.Sprintf("Task %s Output:\n%s\n", taskID, output), nil
}
