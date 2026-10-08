package chatbot

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Arvintian/chat-agent/pkg/config"
	"github.com/Arvintian/chat-agent/pkg/hook"
)

func TestRenderCronPromptBuiltins(t *testing.T) {
	out, err := renderCronPrompt("date={{.Date}} user={{.User}}", nil)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if strings.Count(out, "=") != 2 || strings.HasPrefix(out, "date= ") {
		t.Fatalf("unexpected output %q", out)
	}
	if !strings.Contains(out, "date=20") {
		t.Fatalf("expected rendered date, got %q", out)
	}
}

func TestRenderCronPromptHookData(t *testing.T) {
	out, err := renderCronPrompt("do {{.task}} for {{.items}}", map[string]any{
		"task":  "cleanup",
		"items": []any{"a", "b"},
	})
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}
	want := "do cleanup for [a b]"
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

// Hook data overrides the built-in variables of the same name.
func TestRenderCronPromptHookDataOverridesBuiltins(t *testing.T) {
	out, err := renderCronPrompt("{{.Date}}", map[string]any{"Date": "overridden"})
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if out != "overridden" {
		t.Fatalf("got %q, want %q", out, "overridden")
	}
}

func TestRenderCronPromptEnvFunc(t *testing.T) {
	out, err := renderCronPrompt("home={{env \"HOME\"}}", nil)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if !strings.Contains(out, "home=/") {
		t.Fatalf("expected rendered HOME, got %q", out)
	}
}

func TestRenderCronPromptInvalidTemplate(t *testing.T) {
	if _, err := renderCronPrompt("{{unknownFunc}}", nil); err == nil {
		t.Fatal("expected parse error for unknown function")
	}
	if _, err := renderCronPrompt("{{oops", nil); err == nil {
		t.Fatal("expected parse error for malformed template")
	}
}

func TestRenderCronPromptEmpty(t *testing.T) {
	out, err := renderCronPrompt("", map[string]any{"task": "x"})
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if out != "" {
		t.Fatalf("got %q, want empty", out)
	}
}

// writeTestHookScript creates an executable script that prints the given
// payload to stdout.
func writeTestHookScript(t *testing.T, payload string) string {
	t.Helper()
	var content string
	if runtime.GOOS == "windows" {
		content = "echo " + payload + "\r\n"
	} else {
		content = "#!/bin/sh\necho '" + payload + "'\n"
	}
	path := filepath.Join(t.TempDir(), "hook.sh")
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatalf("failed to write test script: %v", err)
	}
	return path
}

func newCronTestSession() *ChatSession {
	return &ChatSession{
		ID:          "sess",
		Name:        "chat",
		hookManager: hook.NewHookManager(nil),
	}
}

func TestRunCronHookJSONOutput(t *testing.T) {
	s := newCronTestSession()
	script := writeTestHookScript(t, `{"task":"hello","count":2}`)

	data, err := s.runCronHook(context.Background(), &config.SessionHookConfig{
		Enabled:    true,
		Type:       "script",
		ScriptPath: script,
		Timeout:    10,
	})
	if err != nil {
		t.Fatalf("runCronHook failed: %v", err)
	}
	if data["task"] != "hello" || data["count"] != float64(2) {
		t.Fatalf("unexpected data: %v", data)
	}
}

func TestRunCronHookRejectsNonJSON(t *testing.T) {
	s := newCronTestSession()
	script := writeTestHookScript(t, `this is not json`)

	_, err := s.runCronHook(context.Background(), &config.SessionHookConfig{
		Enabled:    true,
		Type:       "script",
		ScriptPath: script,
		Timeout:    10,
	})
	if err == nil {
		t.Fatal("expected error for non-JSON hook output")
	}
	if !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunCronHookEmptyOutput(t *testing.T) {
	s := newCronTestSession()
	// A script that prints nothing (no echo)
	path := filepath.Join(t.TempDir(), "empty.sh")
	content := "#!/bin/sh\n"
	if runtime.GOOS == "windows" {
		content = ""
	}
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatalf("failed to write test script: %v", err)
	}

	data, err := s.runCronHook(context.Background(), &config.SessionHookConfig{
		Enabled:    true,
		Type:       "script",
		ScriptPath: path,
		Timeout:    10,
	})
	if err != nil {
		t.Fatalf("runCronHook failed: %v", err)
	}
	if data != nil {
		t.Fatalf("expected nil data for empty output, got %v", data)
	}
}

func TestRunCronHookMissingScript(t *testing.T) {
	s := newCronTestSession()
	_, err := s.runCronHook(context.Background(), &config.SessionHookConfig{
		Enabled:    true,
		Type:       "script",
		ScriptPath: filepath.Join(t.TempDir(), "does-not-exist.sh"),
		Timeout:    10,
	})
	if err == nil {
		t.Fatal("expected error for missing script")
	}
}

func TestRunCronHookNilHook(t *testing.T) {
	s := newCronTestSession()
	data, err := s.runCronHook(context.Background(), nil)
	if err != nil {
		t.Fatalf("runCronHook failed: %v", err)
	}
	if data != nil {
		t.Fatalf("expected nil data for nil hook, got %v", data)
	}
}

// NewHookManager must tolerate a nil config (cron tasks may not have any
// session-level hooks configured).
func TestNewHookManagerNilConfig(t *testing.T) {
	hm := hook.NewHookManager(nil)
	if hm == nil {
		t.Fatal("NewHookManager(nil) returned nil")
	}
	out, err := hm.ExecuteHook(context.Background(), nil, "s", "c", nil, "test")
	if err != nil {
		t.Fatalf("ExecuteHook with nil cfg failed: %v", err)
	}
	if out != nil {
		t.Fatalf("expected nil output for nil cfg, got %v", out)
	}
}
