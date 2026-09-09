package hook

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Arvintian/chat-agent/pkg/config"
)

// writeTestScript creates an executable script that prints OK
func writeTestScript(t *testing.T, dir, name string) string {
	t.Helper()
	var content string
	if runtime.GOOS == "windows" {
		content = "echo OK\r\n"
	} else {
		content = "#!/bin/sh\necho OK\n"
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatalf("failed to write test script: %v", err)
	}
	return path
}

// A script under a non-existent hooks base dir must succeed: the base dir
// is created automatically before exec (previously failed with
// "chdir ...: no such file or directory").
func TestExecuteScriptHookAutoCreatesBaseDir(t *testing.T) {
	tmp := t.TempDir()
	// base dir intentionally does not exist yet
	baseDir := filepath.Join(tmp, "hooks")
	scriptDir := t.TempDir()
	scriptPath := writeTestScript(t, scriptDir, "keep.sh")

	cfg := &config.SessionHookConfig{
		Enabled:    true,
		Type:       "script",
		ScriptPath: scriptPath,
		Timeout:    10,
	}
	hm := &HookManager{sessionKeep: cfg, baseDir: baseDir}

	err := hm.OnSessionKeep(context.Background(), "sess-1", "chat", nil)
	if err != nil {
		t.Fatalf("OnSessionKeep failed: %v", err)
	}
	if _, err := os.Stat(baseDir); err != nil {
		t.Fatalf("base dir was not created: %v", err)
	}
}

// A relative script path is resolved under the (auto-created) base dir.
func TestExecuteScriptHookRelativePathUnderBaseDir(t *testing.T) {
	tmp := t.TempDir()
	baseDir := filepath.Join(tmp, "hooks")
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeTestScript(t, baseDir, "rel.sh")

	cfg := &config.SessionHookConfig{
		Enabled:    true,
		Type:       "script",
		ScriptPath: "rel.sh",
		Timeout:    10,
	}
	hm := &HookManager{genModelInput: cfg, baseDir: baseDir}

	messages, err := hm.OnGenModelInput(context.Background(), "sess-1", "chat", nil)
	if err != nil {
		t.Fatalf("OnGenModelInput failed: %v", err)
	}
	if messages != nil {
		t.Fatalf("expected nil messages for non-JSON output, got %v", messages)
	}
}
