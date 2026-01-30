//go:build windows

package tools

import (
	"context"
	"os"
	"os/exec"
)

type windowsTask struct{}

func (windowsTask) createCommand(ctx context.Context, command string) *exec.Cmd {
	return exec.CommandContext(ctx, "powershell", "-Command", command)
}

func (windowsTask) setSysProcAttr(cmd *exec.Cmd) {
}

func (windowsTask) killProcess(process *os.Process) error {
	return process.Kill()
}

func getTaskPlatform() taskPlatform {
	return windowsTask{}
}

func killTaskProcess(process *os.Process) error {
	return getTaskPlatform().killProcess(process)
}
