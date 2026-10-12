//go:build windows

package main

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

const (
	createNewProcessGroup = 0x00000200
	createNoWindow        = 0x08000000
)

func configureHiddenProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNewProcessGroup | createNoWindow,
	}
}

func terminateProcessTree(pid int) error {
	killer := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))
	configureHiddenProcess(killer)
	_ = killer.Run()
	if process, err := os.FindProcess(pid); err == nil {
		_ = process.Kill()
	}
	return nil
}
