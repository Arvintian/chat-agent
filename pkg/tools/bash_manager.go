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
