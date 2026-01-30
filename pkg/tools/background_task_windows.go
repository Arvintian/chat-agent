//go:build windows

package tools

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsTask struct {
	exitGroup *ProcessExitGroup
}

func (t *windowsTask) createCommand(ctx context.Context, command string) *exec.Cmd {
	return exec.CommandContext(ctx, "powershell", "-Command", command)
}

func (t *windowsTask) setSysProcAttr(cmd *exec.Cmd) {
}

func (t *windowsTask) setExitGroup(cmd *exec.Cmd) error {
	g, err := NewProcessExitGroup()
	if err != nil {
		return err
	}
	g.AddProcess(cmd.Process)
	t.exitGroup = &g
	return nil
}

func (t *windowsTask) killProcess(process *os.Process) error {
	if t.exitGroup != nil {
		defer t.exitGroup.Dispose()
	}
	cmd := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(process.Pid))
	return cmd.Run()
}

func getTaskPlatform() taskPlatform {
	return &windowsTask{}
}

type process struct {
	Pid    int
	Handle uintptr
}

type ProcessExitGroup windows.Handle

func NewProcessExitGroup() (ProcessExitGroup, error) {
	handle, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}

	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(
		handle,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info))); err != nil {
		return 0, err
	}

	return ProcessExitGroup(handle), nil
}

func (g ProcessExitGroup) Dispose() error {
	return windows.CloseHandle(windows.Handle(g))
}

func (g ProcessExitGroup) AddProcess(p *os.Process) error {
	return windows.AssignProcessToJobObject(
		windows.Handle(g),
		windows.Handle((*process)(unsafe.Pointer(p)).Handle))
}
