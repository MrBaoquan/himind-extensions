//go:build !windows

package ffmpeg

import "os/exec"

// configureHiddenProcess 在非 Windows 平台上是空操作（没有控制台窗口的概念）。
func configureHiddenProcess(command *exec.Cmd) {}
