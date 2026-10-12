//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

func configureHiddenProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateProcessTree(pid int) error {
	process, err := os.FindProcess(-pid)
	if err != nil {
		return err
	}
	return process.Kill()
}
