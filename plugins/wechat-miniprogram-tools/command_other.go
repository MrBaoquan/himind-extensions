//go:build !windows

package main

import "os/exec"

func configureHiddenCommand(_ *exec.Cmd) {}
