//go:build !windows

package tools

import (
	"context"
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

func (t *unixTask) killProcess(cmd *exec.Cmd) error {
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

func getTaskPlatform() taskPlatform {
	return &unixTask{}
}
