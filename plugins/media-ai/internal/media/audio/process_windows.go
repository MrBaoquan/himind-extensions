//go:build windows

package audio

import (
	"os/exec"
	"syscall"
)

// configureHiddenProcess 让子进程不弹控制台窗口。
func configureHiddenProcess(command *exec.Cmd) {
	if command == nil {
		return
	}
	const createNoWindow = 0x08000000
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.HideWindow = true
	command.SysProcAttr.CreationFlags |= createNoWindow
}
