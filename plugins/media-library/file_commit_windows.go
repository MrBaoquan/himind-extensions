//go:build windows

package main

import "golang.org/x/sys/windows"

func publishAcquisitionFile(source, target string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	// No MOVEFILE_REPLACE_EXISTING: user files must never be overwritten.
	return windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH)
}
