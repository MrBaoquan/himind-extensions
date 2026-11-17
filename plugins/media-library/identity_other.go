//go:build !windows

package main

type fileIdentity struct {
	VolumeID string
	FileID   string
}

func identifyFile(path string) fileIdentity {
	return fileIdentity{}
}
