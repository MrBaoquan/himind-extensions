//go:build !windows

package mediadownload

func systemRuntimeProxy() (string, string) { return "", "" }
