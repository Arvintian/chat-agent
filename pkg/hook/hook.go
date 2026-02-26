package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/Arvintian/chat-agent/pkg/config"
	"github.com/Arvintian/chat-agent/pkg/logger"
	"github.com/cloudwego/eino/schema"
)

// getLogCategory returns the log category for hook operations
func getLogCategory() string {
	return "hook"
}

// logInfo logs an info message
func logInfo(format string, v ...any) {
	logger.Info(getLogCategory(), fmt.Sprintf(format, v...))
}

// logWarn logs a warning message
func logWarn(format string, v ...any) {
	logger.Warn(getLogCategory(), fmt.Sprintf(format, v...))
}

// logError logs an error message
func logError(format string, v ...any) {
	logger.Error(getLogCategory(), fmt.Sprintf(format, v...))
}

// SessionHookData represents the data passed to session hooks via stdin
type SessionHookData struct {
	SessionID   string            `json:"session_id"`
	SessionName string            `json:"session_name"`
	Messages    []*schema.Message `json:"messages"`
	Timestamp   string            `json:"timestamp"`
}

// GenModelInputResult represents the result returned by genmodelinput hook
type GenModelInputResult struct {
	Messages []*schema.Message `json:"messages"`
}

// HookManager manages session hooks
type HookManager struct {
	sessionKeep   *config.SessionHookConfig
	genModelInput *config.SessionHookConfig
	baseDir       string
}

func NewHookManager(hooksConfig *config.SessionHooks) *HookManager {
	// Default base directory is $HOME/.chat-agent/hooks
	homeDir, _ := os.UserHomeDir()
	baseDir := filepath.Join(homeDir, ".chat-agent", "hooks")

	return &HookManager{
		sessionKeep:   hooksConfig.Keep,
		genModelInput: hooksConfig.GenModelInput,
		baseDir:       baseDir,
	}
}

// executeHookScript executes a hook script with the given configuration and session data
// It returns the raw output and any error from execution
func (hm *HookManager) executeHookScript(ctx context.Context, cfg *config.SessionHookConfig, sessionID string, sessionName string, messages []*schema.Message, logPrefix string) ([]byte, error) {
	if cfg == nil || !cfg.Enabled {
		return nil, nil
	}

	scriptPath := cfg.ScriptPath

	// Expand ~ to home directory and make path absolute
	if filepath.HasPrefix(scriptPath, "~") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			logWarn("Failed to get home directory: %v", err)
		} else {
			scriptPath = filepath.Join(homeDir, scriptPath[1:])
		}
	}

	// If path is relative, make it relative to base directory
	if !filepath.IsAbs(scriptPath) {
		scriptPath = filepath.Join(hm.baseDir, scriptPath)
	}

	// Check if script exists
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("hook script does not exist: %s", scriptPath)
	}

	// Make script executable if it's not already
	if err := os.Chmod(scriptPath, 0755); err != nil {
		logWarn("Failed to make script executable: %v", err)
	}

	// Prepare command
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 // default timeout
	}

	cmd := exec.CommandContext(ctx, scriptPath, cfg.Args...)

	// Set environment variables
	envVars := append(os.Environ(),
		fmt.Sprintf("SESSION_HOOK=true"),
		fmt.Sprintf("HOOK_TIMEOUT=%d", timeout),
	)

	// Add custom environment variables from config
	if cfg.Env != nil {
		for key, value := range cfg.Env {
			envVars = append(envVars, fmt.Sprintf("%s=%s", key, value))
		}
	}

	cmd.Env = envVars

	// Set working directory to base dir
	cmd.Dir = hm.baseDir

	// Prepare JSON data to pass via stdin
	hookData := SessionHookData{
		SessionID:   sessionID,
		SessionName: sessionName,
		Messages:    messages,
		Timestamp:   time.Now().Format(time.RFC3339),
	}

	jsonData, err := json.MarshalIndent(hookData, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal session data: %w", err)
	}

	logInfo("%s: executing hook: %s", logPrefix, filepath.Base(scriptPath))

	startTime := time.Now()

	// Pass JSON data via stdin
	cmd.Stdin = bytes.NewReader(jsonData)

	// Capture stdout and stderr separately
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err = cmd.Run()
	duration := time.Since(startTime)

	if err != nil {
		logError("%s: hook failed after %v: %v\nstderr: %s", logPrefix, duration, err, stderrBuf.String())
		return nil, fmt.Errorf("hook execution failed: %w", err)
	}

	// Log stderr if there is any output
	if stderrBuf.Len() > 0 {
		logWarn("%s: hook produced stderr output: %s", logPrefix, stderrBuf.String())
	}

	output := stdoutBuf.Bytes()

	logInfo("%s: hook completed successfully in %v", logPrefix, duration)
	return output, nil
}

// OnSessionClear executes the session clear hook if enabled
func (hm *HookManager) OnSessionKeep(ctx context.Context, sessionID string, sessionName string, messages []*schema.Message) error {
	output, err := hm.executeHookScript(ctx, hm.sessionKeep, sessionID, sessionName, messages, "Session keep hook")
	if err != nil {
		return err
	}
	// For session clear, we don't expect any specific output format
	_ = output
	return nil
}

// OnGenModelInput executes the genmodelinput hook if enabled
// It passes session data via stdin and expects JSON output with []message
func (hm *HookManager) OnGenModelInput(ctx context.Context, sessionID string, sessionName string, messages []*schema.Message) ([]*schema.Message, error) {
	output, err := hm.executeHookScript(ctx, hm.genModelInput, sessionID, sessionName, messages, "GenModelInput hook")
	if err != nil {
		return messages, err
	}

	if output == nil {
		return messages, nil
	}

	// Parse the JSON output to get the result
	var result GenModelInputResult
	if err := json.Unmarshal(output, &result); err != nil {
		logWarn("Failed to parse genmodelinput hook output as JSON: %v", err)
		return messages, nil // Return original messages on parse error
	}

	logInfo("Genmodelinput hook processed %d messages", len(result.Messages))
	return result.Messages, nil
}
