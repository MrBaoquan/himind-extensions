package main

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

func validateMediaURL(raw string) (*url.URL, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, errors.New("url 不能为空")
	}
	if len(value) > 2000 {
		return nil, errors.New("url 过长")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return nil, fmt.Errorf("url 无效: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("url 只支持 http 或 https")
	}
	if parsed.Hostname() == "" {
		return nil, errors.New("url 缺少主机名")
	}
	if parsed.User != nil {
		return nil, errors.New("url 不允许携带用户名或密码")
	}
	if allowPrivateURLs() {
		return parsed, nil
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return nil, errors.New("url 不允许指向本机地址")
	}
	if ip := net.ParseIP(host); ip != nil {
		if blockedIP(ip) {
			return nil, errors.New("url 不允许指向本机、私网或链路本地地址")
		}
		return parsed, nil
	}
	addresses, err := net.LookupIP(host)
	if err != nil {
		return nil, fmt.Errorf("无法解析 url 主机名: %w", err)
	}
	for _, ip := range addresses {
		if blockedIP(ip) {
			return nil, errors.New("url 解析到了本机、私网或链路本地地址")
		}
	}
	return parsed, nil
}

func allowPrivateURLs() bool {
	value := strings.TrimSpace(os.Getenv("HIMIND_MEDIA_DOWNLOADER_ALLOW_PRIVATE"))
	return value == "1" || strings.EqualFold(value, "true")
}

func blockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		// 云元数据地址和部分保留网段。
		if ip4[0] == 169 && ip4[1] == 254 {
			return true
		}
		if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
			return true
		}
		if ip4[0] == 192 && ip4[1] == 0 && ip4[2] == 0 {
			return true
		}
	}
	return false
}
