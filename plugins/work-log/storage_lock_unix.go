//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"syscall"
)

func lockRootFile(root string) (func(), error) {
	file, err := os.OpenFile(filepath.Join(root, ".work-log.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		file.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}
