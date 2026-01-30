//go:build !windows

package tools

import (
	"context"
	"os"
	"os/exec"
	"syscall"
)

type unixTask struct{}

func (t *unixTask) createCommand(ctx context.Context, command string) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", "-c", command)
}

func (t *unixTask) setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func (t *unixTask) killProcess(process *os.Process) error {
	pgid, err := syscall.Getpgid(process.Pid)
	if err == nil {
		return syscall.Kill(-pgid, syscall.SIGKILL)
	}
	return process.Kill()
}

func (t *unixTask) setExitGroup(cmd *exec.Cmd) error {
	return nil
}

func getTaskPlatform() taskPlatform {
	return &unixTask{}
}
