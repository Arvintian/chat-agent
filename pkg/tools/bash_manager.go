package tools

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

type sessionType string

const (
	sessionTypeBash       sessionType = "bash"
	sessionTypePowerShell sessionType = "powershell"
)

type session struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	stderr  io.ReadCloser
	mutex   sync.Mutex
	running bool
	platform
}

type BashManager struct {
	session      *session
	sessionMutex sync.Mutex
}

type platform interface {
	createCommand() *exec.Cmd
	buildCommandString(command, workdir, marker string) string
	getMarker() string
	killProcess(cmd *exec.Cmd)
	sessionType() string
}

func NewBashManager() *BashManager {
	return &BashManager{}
}

func (bm *BashManager) ExecuteCommand(ctx context.Context, command string, workdir string, timeout time.Duration) (string, error) {
	bm.sessionMutex.Lock()
	defer bm.sessionMutex.Unlock()

	if bm.session == nil || !bm.session.running {
		if err := bm.createSession(); err != nil {
			return "", fmt.Errorf("failed to create %s session: %w", bm.session.sessionType(), err)
		}
	}

	output, err := bm.session.execute(ctx, command, workdir, timeout)
	if !bm.session.running {
		bm.session.close()
		bm.createSession()
	}
	return output, err
}

func (bm *BashManager) createSession() error {
	s := &session{}
	s.platform = getPlatform()
	s.cmd = s.platform.createCommand()

	var err error
	s.stdin, err = s.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	s.stdout, err = s.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	s.stderr, err = s.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := s.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start %s: %w", s.sessionType(), err)
	}

	s.running = true
	bm.session = s
	return nil
}

func (s *session) execute(ctx context.Context, command string, workdir string, timeout time.Duration) (string, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if !s.running {
		return "", fmt.Errorf("%s session is not running", s.sessionType())
	}

	marker := s.getMarker()
	fullCommand := s.buildCommandString(command, workdir, marker)

	if _, err := s.stdin.Write([]byte(fullCommand)); err != nil {
		s.running = false
		return "", fmt.Errorf("failed to write command: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	outputChan := make(chan string, 1)
	errorChan := make(chan error, 1)

	go func() {
		var output strings.Builder
		scanner := bufio.NewScanner(s.stdout)

		for scanner.Scan() {
			line := scanner.Text()

			if strings.HasPrefix(line, marker) {
				exitCode := strings.TrimPrefix(line, marker)
				if exitCode != "0" {
					output.WriteString(fmt.Sprintf("\n[Exit code: %s]", exitCode))
				}
				outputChan <- output.String()
				return
			}

			if strings.Contains(line, marker) {
				out := strings.Split(line, marker)
				if len(out) != 2 {
					errorChan <- fmt.Errorf("read %s output error", s.sessionType())
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

	stderrChan := make(chan string, 1)
	go func() {
		var stderr strings.Builder
		scanner := bufio.NewScanner(s.stderr)

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

	select {
	case <-ctx.Done():
		s.running = false
		return fmt.Sprintf("command timed out after %v or context canceled, process killed", timeout), nil
	case err := <-errorChan:
		s.running = false
		return "", fmt.Errorf("error reading output: %w", err)
	case output := <-outputChan:
		output = strings.TrimRight(output, "\n")
		stderrOutput := <-stderrChan
		stderrOutput = strings.TrimSpace(stderrOutput)
		if stderrOutput != "" {
			output = output + "\n\nSTDERR:\n" + stderrOutput
		}
		return output, nil
	}
}

func (s *session) close() {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.stdin != nil {
		s.stdin.Close()
	}
	if s.stdout != nil {
		s.stdout.Close()
	}
	if s.stderr != nil {
		s.stderr.Close()
	}

	s.killProcess(s.cmd)
}

func (bm *BashManager) Close() {
	bm.sessionMutex.Lock()
	defer bm.sessionMutex.Unlock()

	if bm.session != nil {
		bm.session.close()
		bm.session = nil
	}
}

func getPlatform() platform {
	switch runtime.GOOS {
	case "windows":
		return windowsPlatform{}
	default:
		return unixPlatform{}
	}
}

type unixPlatform struct{}

func (unixPlatform) createCommand() *exec.Cmd {
	cmd := exec.Command("bash")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
		Pgid:    0,
	}
	return cmd
}

func (unixPlatform) buildCommandString(command string, workdir string, marker string) string {
	fullCommand := fmt.Sprintf("(%s); echo '%s'$?; echo '%s' > /dev/stderr\n", command, marker, marker)
	if workdir != "" {
		fullCommand = fmt.Sprintf("cd %s && %s", workdir, fullCommand)
	}
	return fullCommand
}

func (unixPlatform) getMarker() string {
	return fmt.Sprintf("__BASH_CMD_DONE_%d__", time.Now().UnixNano())
}

func (unixPlatform) killProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err == nil {
		syscall.Kill(-pgid, syscall.SIGKILL)
	} else {
		cmd.Process.Kill()
	}
	cmd.Wait()
}

func (unixPlatform) sessionType() string {
	return string(sessionTypeBash)
}

type windowsPlatform struct{}

func (windowsPlatform) createCommand() *exec.Cmd {
	return exec.Command("powershell", "-NoLogo", "-NoProfile", "-Command", "-")
}

func (windowsPlatform) buildCommandString(command string, workdir string, marker string) string {
	if workdir != "" {
		return fmt.Sprintf("cd '%s'; %s; Write-Output '%s'$LASTEXITCODE; Write-Error '%s'", workdir, command, marker, marker)
	}
	return fmt.Sprintf("%s; Write-Output '%s'$LASTEXITCODE; Write-Error '%s'", command, marker, marker)
}

func (windowsPlatform) getMarker() string {
	return fmt.Sprintf("__POWERSHELL_CMD_DONE_%d__", time.Now().UnixNano())
}

func (windowsPlatform) killProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	cmd.Process.Kill()
	cmd.Wait()
}

func (windowsPlatform) sessionType() string {
	return string(sessionTypePowerShell)
}
