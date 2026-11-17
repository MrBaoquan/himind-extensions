//go:build !windows

package mediadownload

func knownDownloadsDir() string {
	return ""
}
