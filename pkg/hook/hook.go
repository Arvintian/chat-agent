package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

// executeHookScript executes a hook script or HTTP request with the given configuration and session data
// It returns the raw output and any error from execution
func (hm *HookManager) executeHook(ctx context.Context, cfg *config.SessionHookConfig, sessionID string, sessionName string, messages []*schema.Message, logPrefix string) ([]byte, error) {
	if cfg == nil || !cfg.Enabled {
		return nil, nil
	}

	// Determine hook type: "script" or "http", default is "script"
	hookType := cfg.Type
	if hookType == "" {
		hookType = "script"
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 // default timeout
	}

	switch hookType {
	case "script":
		return hm.executeScriptHook(ctx, cfg, sessionID, sessionName, messages, logPrefix, timeout)
	case "http":
		return hm.executeHTTPHook(ctx, cfg, sessionID, sessionName, messages, logPrefix, timeout)
	default:
		return nil, fmt.Errorf("unknown hook type: %s, supported types: script, http", hookType)
	}
}

// executeScriptHook executes a local script hook
func (hm *HookManager) executeScriptHook(ctx context.Context, cfg *config.SessionHookConfig, sessionID string, sessionName string, messages []*schema.Message, logPrefix string, timeout int) ([]byte, error) {
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

// executeHTTPHook executes an HTTP request hook
func (hm *HookManager) executeHTTPHook(ctx context.Context, cfg *config.SessionHookConfig, sessionID string, sessionName string, messages []*schema.Message, logPrefix string, timeout int) ([]byte, error) {
	url := cfg.URL
	if url == "" {
		return nil, fmt.Errorf("HTTP URL is required for http type hook")
	}

	method := cfg.Method
	if method == "" {
		method = "POST"
	}

	// Prepare JSON data to send as request body
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

	logInfo("%s: executing HTTP hook: %s %s", logPrefix, method, url)

	startTime := time.Now()

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: time.Duration(timeout) * time.Second,
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")

	// Add custom headers from config
	if cfg.Headers != nil {
		for key, value := range cfg.Headers {
			req.Header.Set(key, value)
		}
	}

	// Execute request
	resp, err := client.Do(req)
	duration := time.Since(startTime)

	if err != nil {
		logError("%s: HTTP hook failed after %v: %v", logPrefix, duration, err)
		return nil, fmt.Errorf("HTTP hook execution failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logError("%s: failed to read HTTP response body: %v", logPrefix, err)
		return nil, fmt.Errorf("failed to read HTTP response: %w", err)
	}

	// Log status
	logInfo("%s: HTTP hook completed with status %d in %v", logPrefix, resp.StatusCode, duration)

	// Check for non-success status codes
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		logError("%s: HTTP hook returned non-success status: %d, body: %s", logPrefix, resp.StatusCode, string(body))
		return nil, fmt.Errorf("HTTP hook returned status %d", resp.StatusCode)
	}

	return body, nil
}

// OnSessionClear executes the session clear hook if enabled
func (hm *HookManager) OnSessionKeep(ctx context.Context, sessionID string, sessionName string, messages []*schema.Message) error {
	output, err := hm.executeHook(ctx, hm.sessionKeep, sessionID, sessionName, messages, "Session keep hook")
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
	output, err := hm.executeHook(ctx, hm.genModelInput, sessionID, sessionName, messages, "GenModelInput hook")
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
