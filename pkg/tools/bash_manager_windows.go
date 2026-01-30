//go:build windows

package tools

import (
	"fmt"
	"os/exec"
	"time"
)

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

func getPlatform() platform {
	return windowsPlatform{}
}
