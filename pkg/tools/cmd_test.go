package tools

import (
	"context"
	"encoding/json"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

// printEnvCmd returns a shell command that prints the given environment
// variable value, compatible with both Unix (sh) and Windows (PowerShell).
func printEnvCmd(name string) string {
	if runtime.GOOS == "windows" {
		return "echo %" + name + "%"
	}
	return "printenv " + name
}

func envMapFromList(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return m
}

func TestResolveEnvInheritsProcessEnv(t *testing.T) {
	t.Setenv("CHAT_AGENT_TEST_INHERIT", "process-value")

	tt := &RunTerminalCommandTool{}
	got := envMapFromList(tt.resolveEnv())
	if got["CHAT_AGENT_TEST_INHERIT"] != "process-value" {
		t.Fatalf("expected process env CHAT_AGENT_TEST_INHERIT=process-value, got %q", got["CHAT_AGENT_TEST_INHERIT"])
	}
	// Sanity: common process env vars should be present
	if got["PATH"] == "" {
		t.Fatal("expected PATH to be inherited from process env")
	}
}

func TestResolveEnvAddsToolEnv(t *testing.T) {
	tt := &RunTerminalCommandTool{
		Env: map[string]string{
			"CHAT_AGENT_TEST_NEW": "tool-value",
		},
	}
	got := envMapFromList(tt.resolveEnv())
	if got["CHAT_AGENT_TEST_NEW"] != "tool-value" {
		t.Fatalf("expected tool env CHAT_AGENT_TEST_NEW=tool-value, got %q", got["CHAT_AGENT_TEST_NEW"])
	}
}

func TestResolveEnvToolOverridesProcess(t *testing.T) {
	t.Setenv("CHAT_AGENT_TEST_OVERRIDE", "process-value")
	tt := &RunTerminalCommandTool{
		Env: map[string]string{
			"CHAT_AGENT_TEST_OVERRIDE": "tool-value",
		},
	}
	got := envMapFromList(tt.resolveEnv())
	if got["CHAT_AGENT_TEST_OVERRIDE"] != "tool-value" {
		t.Fatalf("expected tool env to override process env, got %q", got["CHAT_AGENT_TEST_OVERRIDE"])
	}
}

func TestResolveEnvIsSorted(t *testing.T) {
	tt := &RunTerminalCommandTool{
		Env: map[string]string{"ZZZ": "1", "AAA": "2"},
	}
	env := tt.resolveEnv()
	if !sort.StringsAreSorted(env) {
		t.Fatalf("expected env list to be sorted, got %v", env)
	}
}

func TestGetCommandToolsEnvFromParams(t *testing.T) {
	params := map[string]interface{}{
		"workDir": "/tmp",
		"timeout": 10,
		"env": map[string]interface{}{
			"http_proxy":  "http://127.0.0.1:7890",
			"https_proxy": "http://127.0.0.1:7890",
		},
	}
	tools, err := getCommandTools(context.Background(), params)
	if err != nil {
		t.Fatalf("getCommandTools error: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools (cmd, cmd_bg), got %d", len(tools))
	}
	cmdTool, ok := tools[0].(*RunTerminalCommandTool)
	if !ok {
		t.Fatalf("expected first tool to be *RunTerminalCommandTool, got %T", tools[0])
	}
	if cmdTool.WorkingDir != "/tmp" {
		t.Fatalf("expected WorkingDir /tmp, got %q", cmdTool.WorkingDir)
	}
	if cmdTool.Timeout != 10*time.Second {
		t.Fatalf("expected timeout 10s, got %v", cmdTool.Timeout)
	}
	if cmdTool.Env["http_proxy"] != "http://127.0.0.1:7890" || cmdTool.Env["https_proxy"] != "http://127.0.0.1:7890" {
		t.Fatalf("expected env config to be passed through, got %v", cmdTool.Env)
	}
}

func TestGetCommandToolsDefaultTimeout(t *testing.T) {
	tools, err := getCommandTools(context.Background(), map[string]interface{}{"workDir": "/tmp"})
	if err != nil {
		t.Fatalf("getCommandTools error: %v", err)
	}
	cmdTool := tools[0].(*RunTerminalCommandTool)
	if cmdTool.Timeout != time.Duration(DEFAULT_CMD_TIMEOUT)*time.Second {
		t.Fatalf("expected default timeout %v, got %v", time.Duration(DEFAULT_CMD_TIMEOUT)*time.Second, cmdTool.Timeout)
	}
	if cmdTool.Env != nil {
		t.Fatalf("expected nil env when not configured, got %v", cmdTool.Env)
	}
}

// TestCmdToolInfoHasNoEnvParam verifies the env feature is config-only:
// it must not appear in the tool parameters exposed to the model.
func TestCmdToolInfoHasNoEnvParam(t *testing.T) {
	tt := &RunTerminalCommandTool{Timeout: time.Second}
	info, err := tt.Info(context.Background())
	if err != nil {
		t.Fatalf("Info error: %v", err)
	}
	js, err := info.ParamsOneOf.ToJSONSchema()
	if err != nil {
		t.Fatalf("ToJSONSchema error: %v", err)
	}
	if js.Properties == nil || js.Properties.Len() == 0 {
		t.Fatal("expected properties in tool json schema")
	}
	if _, ok := js.Properties.Get("env"); ok {
		t.Fatal("env must not be exposed as a tool call parameter")
	}
}

func TestCmdToolEnvAppliedToForegroundCommand(t *testing.T) {
	tt := &RunTerminalCommandTool{
		Timeout: 10 * time.Second,
		Env: map[string]string{
			"CHAT_AGENT_TEST_PROXY": "http://127.0.0.1:7890",
		},
		TaskManager: NewBackgroundTaskManager(),
	}
	out, err := tt.InvokableRun(context.Background(), `{"command":`+mustJSON(printEnvCmd("CHAT_AGENT_TEST_PROXY"))+`}`)
	if err != nil {
		t.Fatalf("InvokableRun error: %v", err)
	}
	if !strings.Contains(out, "http://127.0.0.1:7890") {
		t.Fatalf("expected env value in command output, got: %s", out)
	}
}

func TestCmdToolEnvOverridesProcessForCommand(t *testing.T) {
	t.Setenv("CHAT_AGENT_TEST_OVERRIDE", "process-value")
	tt := &RunTerminalCommandTool{
		Timeout: 10 * time.Second,
		Env: map[string]string{
			"CHAT_AGENT_TEST_OVERRIDE": "tool-value",
		},
		TaskManager: NewBackgroundTaskManager(),
	}
	out, err := tt.InvokableRun(context.Background(), `{"command":`+mustJSON(printEnvCmd("CHAT_AGENT_TEST_OVERRIDE"))+`}`)
	if err != nil {
		t.Fatalf("InvokableRun error: %v", err)
	}
	if !strings.Contains(out, "tool-value") {
		t.Fatalf("expected tool env to override process env in command, got: %s", out)
	}
	if strings.Contains(out, "process-value") {
		t.Fatalf("process env value leaked into command env: %s", out)
	}
}

func TestCmdToolBackgroundTaskEnvApplied(t *testing.T) {
	tt := &RunTerminalCommandTool{
		Timeout: 10 * time.Second,
		Env: map[string]string{
			"CHAT_AGENT_TEST_BG_PROXY": "http://127.0.0.1:7890",
		},
		TaskManager: NewBackgroundTaskManager(),
	}
	out, err := tt.InvokableRun(context.Background(), `{"command":`+mustJSON(printEnvCmd("CHAT_AGENT_TEST_BG_PROXY"))+`, "background": true}`)
	if err != nil {
		t.Fatalf("InvokableRun error: %v", err)
	}
	taskID := parseTaskID(t, out)

	task, ok := waitForTaskDone(tt.TaskManager, taskID, 10*time.Second)
	if !ok {
		t.Fatal("background task did not finish in time")
	}
	if task.Status != TaskStatusSuccess {
		t.Fatalf("expected task success, got %s (output: %s)", task.Status, task.GetOutputString())
	}
	if !strings.Contains(task.Output.String(), "http://127.0.0.1:7890") {
		t.Fatalf("expected env value in background task output, got: %s", task.GetOutputString())
	}
}

func TestStartTaskNilEnvInheritsProcessEnv(t *testing.T) {
	t.Setenv("CHAT_AGENT_TEST_INHERIT_BG", "inherited")
	tm := NewBackgroundTaskManager()
	task, err := tm.StartTask(printEnvCmd("CHAT_AGENT_TEST_INHERIT_BG"), "", nil)
	if err != nil {
		t.Fatalf("StartTask error: %v", err)
	}
	done, ok := waitForTaskDone(tm, task.ID, 10*time.Second)
	if !ok {
		t.Fatal("background task did not finish in time")
	}
	if done.Status != TaskStatusSuccess {
		t.Fatalf("expected task success, got %s", done.Status)
	}
	if !strings.Contains(done.Output.String(), "inherited") {
		t.Fatalf("expected process env inherited when env is nil, got: %s", done.GetOutputString())
	}
}

func mustJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// parseTaskID extracts the task ID from the runInBackground result message.
func parseTaskID(t *testing.T, out string) string {
	t.Helper()
	const prefix = "Background task started with ID: "
	idx := strings.Index(out, prefix)
	if idx < 0 {
		t.Fatalf("cannot find task ID in output: %s", out)
	}
	rest := out[idx+len(prefix):]
	if i := strings.IndexAny(rest, "\n "); i >= 0 {
		return rest[:i]
	}
	return rest
}

// waitForTaskDone polls the task until it is no longer running or the deadline passes.
func waitForTaskDone(tm *BackgroundTaskManager, taskID string, timeout time.Duration) (*BackgroundTask, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		task, ok := tm.GetTask(taskID)
		if ok && task.Status != TaskStatusRunning {
			return task, true
		}
		time.Sleep(50 * time.Millisecond)
	}
	task, ok := tm.GetTask(taskID)
	return task, ok
}
