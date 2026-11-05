//go:build windows

package ffmpeg

import (
	"fmt"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

const windowsCreateNewProcessGroup = 0x00000200

// configureHiddenProcess 让子进程不要弹出控制台窗口，并确保取消时能杀掉整棵进程树。
//
// Windows 上常见的 ffmpeg 是 shim：CommandContext 默认只终止 shim，真正的 ffmpeg
// 子进程会继续运行并占满 CPU。因此这里覆盖 Cancel，用 taskkill /T /F 清理进程树。
func configureHiddenProcess(command *exec.Cmd) {
	if command == nil {
		return
	}
	const createNoWindow = 0x08000000
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.HideWindow = true
	command.SysProcAttr.CreationFlags |= createNoWindow | windowsCreateNewProcessGroup
	command.WaitDelay = 5 * time.Second
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		kill := exec.Command("taskkill", "/PID", strconv.Itoa(command.Process.Pid), "/T", "/F")
		kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
		if err := kill.Run(); err != nil {
			return fmt.Errorf("终止 ffmpeg 进程树失败: %w", err)
		}
		return nil
	}
}
