//go:build !windows

package iostudio

import "os/exec"

// configureHiddenProcess 在非 Windows 上是空操作。
func configureHiddenProcess(command *exec.Cmd) {}
