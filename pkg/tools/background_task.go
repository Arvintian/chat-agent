package tools

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type TaskStatus string

const (
	TaskStatusRunning TaskStatus = "running"
	TaskStatusSuccess TaskStatus = "success"
	TaskStatusFailed  TaskStatus = "failed"
	TaskStatusKilled  TaskStatus = "killed"
)

type BackgroundTask struct {
	ID         string
	Command    string
	WorkingDir string
	StartTime  time.Time
	EndTime    *time.Time
	Status     TaskStatus
	Output     strings.Builder
	Stderr     strings.Builder
	ExitCode   *int
	Process    *exec.Cmd
	CancelFunc context.CancelFunc
	mu         sync.Mutex
}

type BackgroundTaskManager struct {
	tasks    map[string]*BackgroundTask
	taskID   atomic.Uint64
	mu       sync.RWMutex
	outputMu sync.Mutex
}

var (
	globalTaskManager *BackgroundTaskManager
	taskManagerOnce   sync.Once
)

func GetTaskManager() *BackgroundTaskManager {
	taskManagerOnce.Do(func() {
		globalTaskManager = &BackgroundTaskManager{
			tasks: make(map[string]*BackgroundTask),
		}
	})
	return globalTaskManager
}

func (tm *BackgroundTaskManager) generateID() string {
	id := tm.taskID.Add(1)
	return fmt.Sprintf("%d", id)
}

func (tm *BackgroundTaskManager) StartTask(command, workdir string) (*BackgroundTask, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	taskID := tm.generateID()

	task := &BackgroundTask{
		ID:         taskID,
		Command:    command,
		WorkingDir: workdir,
		StartTime:  time.Now(),
		Status:     TaskStatusRunning,
		CancelFunc: cancel,
	}

	p := getTaskPlatform()
	cmd := p.createCommand(ctx, command)
	p.setSysProcAttr(cmd)

	if workdir != "" {
		cmd.Dir = workdir
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		stdout.Close()
		cancel()
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		stdout.Close()
		stderr.Close()
		cancel()
		return nil, fmt.Errorf("failed to start command: %w", err)
	}

	task.Process = cmd
	tm.tasks[taskID] = task

	go tm.monitorTask(ctx, task, stdout, stderr, cmd)

	return task, nil
}

func (tm *BackgroundTaskManager) monitorTask(ctx context.Context, task *BackgroundTask, stdout, stderr io.ReadCloser, cmd *exec.Cmd) {
	defer stdout.Close()
	defer stderr.Close()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			tm.outputMu.Lock()
			task.Output.WriteString(scanner.Text() + "\n")
			tm.outputMu.Unlock()
		}
	}()

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			tm.outputMu.Lock()
			task.Stderr.WriteString(scanner.Text() + "\n")
			tm.outputMu.Unlock()
		}
	}()

	wg.Wait()

	err := cmd.Wait()

	task.mu.Lock()
	defer task.mu.Unlock()

	task.EndTime = new(time.Time)
	*task.EndTime = time.Now()

	if ctx.Err() == context.Canceled {
		task.Status = TaskStatusKilled
	} else if err != nil {
		task.Status = TaskStatusFailed
		if exitErr, ok := err.(*exec.ExitError); ok {
			code := exitErr.ExitCode()
			task.ExitCode = &code
		}
	} else {
		task.Status = TaskStatusSuccess
		successCode := 0
		task.ExitCode = &successCode
	}
}

func (tm *BackgroundTaskManager) ListTasks() []*BackgroundTask {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	tasks := make([]*BackgroundTask, 0, len(tm.tasks))
	for _, task := range tm.tasks {
		tasks = append(tasks, task)
	}
	return tasks
}

func (tm *BackgroundTaskManager) GetTask(id string) (*BackgroundTask, bool) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	task, ok := tm.tasks[id]
	return task, ok
}

func (tm *BackgroundTaskManager) killTaskInternal(id string) error {
	task, ok := tm.tasks[id]
	if !ok {
		return fmt.Errorf("task not found: %s", id)
	}

	task.mu.Lock()
	if task.Status != TaskStatusRunning {
		task.mu.Unlock()
		return fmt.Errorf("task is not running: %s", id)
	}
	task.mu.Unlock()

	task.CancelFunc()

	if task.Process != nil && task.Process.Process != nil {
		killTaskProcess(task.Process.Process)
	}

	return nil
}

func (tm *BackgroundTaskManager) removeTaskInternal(id string) error {
	task, ok := tm.tasks[id]
	if !ok {
		return fmt.Errorf("task not found: %s", id)
	}

	task.mu.Lock()
	if task.Status == TaskStatusRunning {
		task.mu.Unlock()
		return fmt.Errorf("cannot remove running task: %s", id)
	}
	task.mu.Unlock()

	delete(tm.tasks, id)
	return nil
}

func (tm *BackgroundTaskManager) KillTask(id string) error {
	return tm.killTaskInternal(id)
}

func (tm *BackgroundTaskManager) RemoveTask(id string) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	task, ok := tm.tasks[id]
	if !ok {
		return fmt.Errorf("task not found: %s", id)
	}

	if task.Status == TaskStatusRunning {
		tm.mu.Unlock()
		if err := tm.killTaskInternal(id); err != nil {
			return err
		}
		tm.mu.Lock()
		task, ok = tm.tasks[id]
		if !ok {
			return nil
		}
	}

	delete(tm.tasks, id)
	return nil
}

func (tm *BackgroundTaskManager) GetTaskOutput(id string, follow bool) (<-chan string, error) {
	task, ok := tm.GetTask(id)
	if !ok {
		return nil, fmt.Errorf("task not found: %s", id)
	}

	ch := make(chan string, 100)

	go func() {
		defer close(ch)

		stdoutPos := 0
		stderrPos := 0
		task.mu.Lock()
		status := task.Status
		task.mu.Unlock()

		for {
			task.mu.Lock()
			stdoutLen := task.Output.Len()
			stderrLen := task.Stderr.Len()
			task.mu.Unlock()

			if stdoutLen > stdoutPos {
				task.mu.Lock()
				stdoutContent := task.Output.String()[stdoutPos:]
				task.mu.Unlock()
				select {
				case ch <- stdoutContent:
					stdoutPos = stdoutLen
				default:
				}
			}

			if stderrLen > stderrPos {
				task.mu.Lock()
				stderrContent := task.Stderr.String()[stderrPos:]
				task.mu.Unlock()
				select {
				case ch <- "STDERR: " + stderrContent:
					stderrPos = stderrLen
				default:
				}
			}

			task.mu.Lock()
			status = task.Status
			task.mu.Unlock()

			if status != TaskStatusRunning || !follow {
				break
			}

			time.Sleep(100 * time.Millisecond)
		}
	}()

	return ch, nil
}

func (t *BackgroundTask) GetDuration() string {
	t.mu.Lock()
	defer t.mu.Unlock()

	end := t.EndTime
	if end == nil {
		return time.Since(t.StartTime).String()
	}
	return end.Sub(t.StartTime).String()
}

func (t *BackgroundTask) GetOutputString() string {
	t.mu.Lock()
	defer t.mu.Unlock()

	output := t.Output.String()
	stderr := t.Stderr.String()

	if stderr != "" {
		return output + "\nSTDERR:\n" + stderr
	}
	return output
}

type taskPlatform interface {
	createCommand(ctx context.Context, command string) *exec.Cmd
	setSysProcAttr(cmd *exec.Cmd)
	killProcess(process *os.Process) error
}
