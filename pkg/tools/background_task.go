package tools

import (
	"bufio"
	"context"
	"fmt"
	"io"
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

type taskPlatform interface {
	createCommand(ctx context.Context, command string) *exec.Cmd
	setSysProcAttr(cmd *exec.Cmd)
	killProcess(cmd *exec.Cmd) error
}

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
	platform   taskPlatform
}

type BackgroundTaskManager struct {
	tasks  map[string]*BackgroundTask
	taskID atomic.Uint64
	mu     sync.RWMutex
}

func NewBackgroundTaskManager() *BackgroundTaskManager {
	return &BackgroundTaskManager{
		tasks: make(map[string]*BackgroundTask),
	}
}

func (tm *BackgroundTaskManager) generateID() string {
	id := tm.taskID.Add(1)
	return fmt.Sprintf("%d", id)
}

// StartTask starts a background task. env is the full environment for the
// child process; if nil, the process environment is inherited.
func (tm *BackgroundTaskManager) StartTask(command, workdir string, env []string) (*BackgroundTask, error) {
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
	task.platform = p

	if len(env) > 0 {
		cmd.Env = env
	}
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
			line := scanner.Text() + "\n"
			task.mu.Lock()
			task.Output.WriteString(line)
			task.mu.Unlock()
		}
	}()

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text() + "\n"
			task.mu.Lock()
			task.Stderr.WriteString(line)
			task.mu.Unlock()
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

// RemoveTask removes a task from the manager, killing it first if it is
// still running. Lock order is always tm.mu -> task.mu, so it cannot
// deadlock.
func (tm *BackgroundTaskManager) RemoveTask(id string) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	task, ok := tm.tasks[id]
	if !ok {
		return fmt.Errorf("task not found: %s", id)
	}

	if task.getStatus() == TaskStatusRunning {
		task.CancelFunc()

		task.mu.Lock()
		cmd := task.Process
		platform := task.platform
		task.mu.Unlock()

		if cmd != nil && cmd.Process != nil {
			platform.killProcess(cmd)
		}
	}

	delete(tm.tasks, id)
	return nil
}

// getStatus returns the task status under the task lock.
func (t *BackgroundTask) getStatus() TaskStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.Status
}

// getEndTime returns the end time under the task lock (nil if still running).
func (t *BackgroundTask) getEndTime() *time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.EndTime
}

// getExitCode returns the exit code under the task lock (nil if not finished).
func (t *BackgroundTask) getExitCode() *int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ExitCode
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
