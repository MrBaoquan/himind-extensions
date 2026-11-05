//go:build !windows

package ffmpeg

import (
	"os/exec"
	"syscall"
	"time"
)

// configureHiddenProcess 在非 Windows 平台不隐藏窗口；设置独立进程组并在取消时杀组，
// 避免 ffmpeg 的包装进程或子进程残留。
func configureHiddenProcess(command *exec.Cmd) {
	if command == nil {
		return
	}
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.Setpgid = true
	command.WaitDelay = 5 * time.Second
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
}
