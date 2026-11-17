//go:build windows

package mediadownload

import "golang.org/x/sys/windows/registry"

func systemRuntimeProxy() (string, string) {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil {
		return "", ""
	}
	defer key.Close()
	enabled, _, _ := key.GetIntegerValue("ProxyEnable")
	if enabled != 1 {
		return "", ""
	}
	server, _, _ := key.GetStringValue("ProxyServer")
	bypass, _, _ := key.GetStringValue("ProxyOverride")
	return server, bypass
}
