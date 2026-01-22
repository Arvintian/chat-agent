package utils

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// BashSession represents a persistent bash session
type BashSession struct {
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	stdout       io.ReadCloser
	stderr       io.ReadCloser
	mutex        sync.Mutex
	sessionMutex sync.RWMutex
	running      bool
	workingDir   string
}

// BashManager manages bash sessions
type BashManager struct {
	session      *BashSession
	sessionMutex sync.Mutex
}

// NewBashManager creates a new bash manager
func NewBashManager() *BashManager {
	return &BashManager{}
}

// ExecuteCommand executes a bash command in the session
func (bm *BashManager) ExecuteCommand(command string, workdir string, timeout time.Duration) (string, error) {
	bm.sessionMutex.Lock()
	defer bm.sessionMutex.Unlock()

	// Create session if it doesn't exist
	if bm.session == nil || !bm.session.running {
		if err := bm.createSession(); err != nil {
			return "", fmt.Errorf("failed to create bash session: %w", err)
		}
	}

	output, err := bm.session.execute(command, workdir, timeout)
	if !bm.session.running {
		// If an error occurs, close and restart the session
		bm.session.close()
	}
	return output, err
}

// RestartSession restarts the bash session
func (bm *BashManager) RestartSession() error {
	bm.sessionMutex.Lock()
	defer bm.sessionMutex.Unlock()

	// Close existing session
	if bm.session != nil {
		bm.session.close()
	}

	// Create new session
	return bm.createSession()
}

// createSession creates a new bash session
func (bm *BashManager) createSession() error {
	session := &BashSession{}

	// Create the bash command
	session.cmd = exec.Command("bash")

	session.cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
		Pgid:    0,
	}

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

	// Start the bash process
	if err := session.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start bash: %w", err)
	}

	session.running = true
	bm.session = session
	return nil
}

// execute runs a command in the bash session
func (bs *BashSession) execute(command string, workdir string, timeout time.Duration) (string, error) {
	bs.mutex.Lock()
	defer bs.mutex.Unlock()

	if !bs.running {
		return "", fmt.Errorf("bash session is not running")
	}

	// Create a unique marker for command completion
	marker := fmt.Sprintf("__BASH_CMD_DONE_%d__", time.Now().UnixNano())

	// Construct command with marker and error capture
	fullCommand := fmt.Sprintf("(%s); echo '%s'$?; echo '%s' > /dev/stderr\n", command, marker, marker)

	if workdir != "" {
		fullCommand = fmt.Sprintf("cd %s && %s", workdir, fullCommand)
	}

	// Write command to bash
	if _, err := bs.stdin.Write([]byte(fullCommand)); err != nil {
		bs.running = false
		return "", fmt.Errorf("failed to write command: %w", err)
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Read output
	outputChan := make(chan string, 1)
	errorChan := make(chan error, 1)

	go func() {
		var output strings.Builder
		scanner := bufio.NewScanner(bs.stdout)

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
					errorChan <- fmt.Errorf("read bash output error")
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
		scanner := bufio.NewScanner(bs.stderr)

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
		bs.running = false
		return fmt.Sprintf("command timed out after %v", timeout), nil
	case err := <-errorChan:
		bs.running = false
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

// close closes the bash session
func (bs *BashSession) close() {
	bs.mutex.Lock()
	defer bs.mutex.Unlock()

	// Close pipes
	if bs.stdin != nil {
		bs.stdin.Close()
	}
	if bs.stdout != nil {
		bs.stdout.Close()
	}
	if bs.stderr != nil {
		bs.stderr.Close()
	}

	// Kill the process
	if bs.cmd != nil && bs.cmd.Process != nil {
		pgid, err := syscall.Getpgid(bs.cmd.Process.Pid)
		if err == nil {
			syscall.Kill(-pgid, syscall.SIGKILL)
		} else {
			bs.cmd.Process.Kill()
		}
		bs.cmd.Wait()
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
