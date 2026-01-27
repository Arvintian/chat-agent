//go:build windows
// +build windows

package tools

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// PowerShellSession represents a persistent PowerShell session
type PowerShellSession struct {
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	stdout       io.ReadCloser
	stderr       io.ReadCloser
	mutex        sync.Mutex
	sessionMutex sync.RWMutex
	running      bool
	workingDir   string
}

// BashManager manages PowerShell sessions on Windows
type BashManager struct {
	session      *PowerShellSession
	sessionMutex sync.Mutex
}

// NewBashManager creates a new BashManager for Windows
func NewBashManager() *BashManager {
	return &BashManager{}
}

// ExecuteCommand executes a PowerShell command in the session
func (bm *BashManager) ExecuteCommand(ctx context.Context, command string, workdir string, timeout time.Duration) (string, error) {
	bm.sessionMutex.Lock()
	defer bm.sessionMutex.Unlock()

	// Create session if it doesn't exist
	if bm.session == nil || !bm.session.running {
		if err := bm.createSession(); err != nil {
			return "", fmt.Errorf("failed to create PowerShell session: %w", err)
		}
	}

	output, err := bm.session.execute(ctx, command, workdir, timeout)
	if !bm.session.running {
		// If an error occurs, close and restart the session
		bm.session.close()
		bm.createSession()
	}
	return output, err
}

// createSession creates a new PowerShell session
func (bm *BashManager) createSession() error {
	session := &PowerShellSession{}

	// Create the PowerShell command with proper settings
	session.cmd = exec.Command("powershell", "-NoLogo", "-NoProfile", "-Command", "-")

	// Get stdin/stdout/stderr pipes
	var err error
	session.stdin, err = session.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	session.stdout, err = session.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	session.stderr, err = session.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Start the PowerShell process
	if err := session.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start PowerShell: %w", err)
	}

	session.running = true
	bm.session = session
	return nil
}

// execute runs a command in the PowerShell session
func (ps *PowerShellSession) execute(ctx context.Context, command string, workdir string, timeout time.Duration) (string, error) {
	ps.mutex.Lock()
	defer ps.mutex.Unlock()

	if !ps.running {
		return "", fmt.Errorf("PowerShell session is not running")
	}

	// Create a unique marker for command completion
	marker := fmt.Sprintf("__POWERSHELL_CMD_DONE_%d__", time.Now().UnixNano())

	// Construct command with marker and error capture
	// PowerShell uses $LASTEXITCODE for exit code
	var fullCommand string
	if workdir != "" {
		fullCommand = fmt.Sprintf("cd '%s'; %s; Write-Output '%s'$LASTEXITCODE; Write-Error '%s'", workdir, command, marker, marker)
	} else {
		fullCommand = fmt.Sprintf("%s; Write-Output '%s'$LASTEXITCODE; Write-Error '%s'", command, marker, marker)
	}

	// Write command to PowerShell
	if _, err := ps.stdin.Write([]byte(fullCommand + "\n")); err != nil {
		ps.running = false
		return "", fmt.Errorf("failed to write command: %w", err)
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Read output
	outputChan := make(chan string, 1)
	errorChan := make(chan error, 1)

	go func() {
		var output strings.Builder
		scanner := bufio.NewScanner(ps.stdout)

		for scanner.Scan() {
			line := scanner.Text()

			// Check if this is our completion marker
			if strings.HasPrefix(line, marker) {
				// Extract exit code
				exitCode := strings.TrimPrefix(line, marker)
				if exitCode != "0" {
					output.WriteString(fmt.Sprintf("\n[Exit code: %s]", exitCode))
				}
				outputChan <- output.String()
				return
			}

			// Cmd output not end with newline
			if strings.Contains(line, marker) {
				out := strings.Split(line, marker)
				if len(out) != 2 {
					errorChan <- fmt.Errorf("read PowerShell output error")
				}
				cmdOut, exitCode := out[0], out[1]
				output.WriteString(cmdOut)
				output.WriteString("\n")
				if exitCode != "0" {
					output.WriteString(fmt.Sprintf("\n[Exit code: %s]", exitCode))
				}
				outputChan <- output.String()
				return
			}

			output.WriteString(line)
			output.WriteString("\n")
		}

		if err := scanner.Err(); err != nil {
			errorChan <- err
		}
	}()

	// Also capture stderr
	stderrChan := make(chan string, 1)
	go func() {
		var stderr strings.Builder
		scanner := bufio.NewScanner(ps.stderr)

		// Read stderr with a short buffer
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, marker) {
				if idx := strings.Index(line, marker); idx != -1 {
					if idx > 0 {
						stderr.WriteString(line[:idx])
					}
				}
				break
			}
			stderr.WriteString(line)
			stderr.WriteString("\n")
		}
		stderrChan <- stderr.String()
	}()

	// Wait for completion or timeout
	select {
	case <-ctx.Done():
		ps.running = false
		return fmt.Sprintf("command timed out after %v or context canceled, process killed", timeout), nil
	case err := <-errorChan:
		ps.running = false
		return "", fmt.Errorf("error reading output: %w", err)
	case output := <-outputChan:
		// Trim trailing newline
		output = strings.TrimRight(output, "\n")

		// Check if there's stderr output
		stderrOutput := <-stderrChan
		stderrOutput = strings.TrimSpace(stderrOutput)
		if stderrOutput != "" {
			output = output + "\n\nSTDERR:\n" + stderrOutput
		}

		return output, nil
	}
}

// close closes the PowerShell session
func (ps *PowerShellSession) close() {
	ps.mutex.Lock()
	defer ps.mutex.Unlock()

	// Close pipes
	if ps.stdin != nil {
		ps.stdin.Close()
	}
	if ps.stdout != nil {
		ps.stdout.Close()
	}
	if ps.stderr != nil {
		ps.stderr.Close()
	}

	// Kill the process
	if ps.cmd != nil && ps.cmd.Process != nil {
		ps.cmd.Process.Kill()
		ps.cmd.Wait()
	}
}

// Close closes the bash manager and all sessions
func (bm *BashManager) Close() {
	bm.sessionMutex.Lock()
	defer bm.sessionMutex.Unlock()

	if bm.session != nil {
		bm.session.close()
		bm.session = nil
	}
}
