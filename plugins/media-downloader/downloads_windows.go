//go:build windows

package main

import "golang.org/x/sys/windows"

func knownDownloadsDir() string {
	path, err := windows.KnownFolderPath(windows.FOLDERID_Downloads, windows.KF_FLAG_DEFAULT)
	if err != nil {
		return ""
	}
	return path
}
