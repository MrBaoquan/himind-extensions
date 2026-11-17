//go:build windows

package main

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// fileIdentity 返回卷序列号与文件 ID。两者都取不到时返回零值，调用方回退到路径指纹。
type fileIdentity struct {
	VolumeID string
	FileID   string
}

func identifyFile(path string) fileIdentity {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return fileIdentity{}
	}
	handle, err := windows.CreateFile(
		pointer,
		windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return fileIdentity{}
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return fileIdentity{}
	}
	return fileIdentity{
		VolumeID: fmt.Sprintf("vol:%08X", info.VolumeSerialNumber),
		FileID:   fmt.Sprintf("%08X%08X", info.FileIndexHigh, info.FileIndexLow),
	}
}
