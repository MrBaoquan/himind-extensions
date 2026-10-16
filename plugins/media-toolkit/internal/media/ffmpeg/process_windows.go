//go:build windows

package ffmpeg

import (
	"os/exec"
	"syscall"
)

// configureHiddenProcess 让子进程不要弹出控制台窗口。
//
// 插件是以无窗口进程被 Agent 拉起的，任何未隐藏的控制台窗口都会在用户桌面上闪一下，
// 批量处理时尤其明显。CREATE_NO_WINDOW 是唯一可靠的做法（HideWindow 只对新建窗口生效）。
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
