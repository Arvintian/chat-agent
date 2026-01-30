//go:build !windows

package tools

import (
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

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

func getPlatform() platform {
	return unixPlatform{}
}
