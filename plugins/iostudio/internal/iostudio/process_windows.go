//go:build windows

package iostudio

import (
	"os/exec"
	"syscall"
)

// configureHiddenProcess 让子进程不弹控制台窗口。
// IOStudio 是 GUI 子系统程序，未隐藏时会闪一下黑框；批量校验时尤其明显。
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
