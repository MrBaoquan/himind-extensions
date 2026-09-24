//go:build windows

package main

import (
	"os/exec"
	"testing"
)

func TestConfigureHiddenCommandSetsNoWindowFlags(t *testing.T) {
	command := exec.Command("cmd.exe")
	configureHiddenCommand(command)
	if command.SysProcAttr == nil || !command.SysProcAttr.HideWindow {
		t.Fatal("HideWindow was not enabled")
	}
	if command.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Fatal("CREATE_NO_WINDOW was not enabled")
	}
}
