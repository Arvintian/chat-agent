package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/tool"
)

func TestConcurrentModifyFile(t *testing.T) {
	// Setup: create temp dir with a file
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.txt")
	initialContent := "line1\nline2\nline3\nline4\n"
	if err := os.WriteFile(filePath, []byte(initialContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Get filesystem tools
	toolCtx := context.Background()
	toolsList, err := GetBuiltinTools(toolCtx, "filesystem", map[string]interface{}{
		"workDir": tmpDir,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Find modify_file tool
	var modifyTool tool.InvokableTool
	for _, tl := range toolsList {
		info, _ := tl.Info(toolCtx)
		if info != nil && info.Name == "modify_file" {
			iv, ok := tl.(tool.InvokableTool)
			if !ok {
				t.Fatal("modify_file does not implement InvokableTool")
			}
			modifyTool = iv
			break
		}
	}
	if modifyTool == nil {
		t.Fatal("modify_file tool not found")
	}

	// Concurrently modify the same file
	const numGoroutines = 10
	var wg sync.WaitGroup
	errCh := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			args := fmt.Sprintf(`{"path":%q,"find":"line%d","replace":"modified_line%d"}`, filePath, id%4+1, id)
			_, err := modifyTool.InvokableRun(toolCtx, args)
			if err != nil {
				errCh <- fmt.Errorf("goroutine %d: %w", id, err)
			}
		}(i)
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Error(err)
	}

	// Verify file is still valid (no corruption)
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	result := string(content)
	if strings.Contains(result, "\x00") {
		t.Error("file contains null bytes (corrupted)")
	}
	t.Logf("final content:\n%s", result)
	t.Log("concurrent modify_file completed without conflict")
}
