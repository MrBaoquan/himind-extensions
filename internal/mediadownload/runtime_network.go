package mediadownload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
)

func runtimeHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: 12 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.ResponseHeaderTimeout = 20 * time.Second
	// Go only reads proxy environment variables. Desktop Windows users normally
	// configure the per-user system proxy instead, including local proxy apps.
	if !hasProxyEnvironment() {
		server, bypass := systemRuntimeProxy()
		transport.Proxy = func(request *http.Request) (*url.URL, error) {
			return configuredRuntimeProxy(server, bypass, request)
		}
	}
	return &http.Client{Transport: transport, Timeout: 75 * time.Second}
}

func hasProxyEnvironment() bool {
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "NO_PROXY", "no_proxy"} {
		if strings.TrimSpace(os.Getenv(key)) != "" {
			return true
		}
	}
	return false
}

func configuredRuntimeProxy(server, bypass string, request *http.Request) (*url.URL, error) {
	host := strings.ToLower(request.URL.Hostname())
	if host == "localhost" || net.ParseIP(host).IsLoopback() {
		return nil, nil
	}
	for _, rule := range strings.Split(bypass, ";") {
		rule = strings.ToLower(strings.TrimSpace(rule))
		if rule == "" {
			continue
		}
		if rule == "<local>" && !strings.Contains(host, ".") && net.ParseIP(host) == nil {
			return nil, nil
		}
		if match, _ := path.Match(rule, host); match {
			return nil, nil
		}
		if match, _ := path.Match(rule, strings.ToLower(request.URL.Host)); match {
			return nil, nil
		}
	}
	selected := strings.TrimSpace(server)
	if strings.Contains(selected, "=") {
		selected = ""
		for _, item := range strings.Split(server, ";") {
			parts := strings.SplitN(strings.TrimSpace(item), "=", 2)
			if len(parts) == 2 && strings.EqualFold(parts[0], request.URL.Scheme) {
				selected = strings.TrimSpace(parts[1])
				break
			}
		}
	}
	if selected == "" {
		return nil, nil
	}
	if !strings.Contains(selected, "://") {
		selected = "http://" + selected
	}
	proxy, err := url.Parse(selected)
	if err != nil || proxy.Hostname() == "" || (proxy.Scheme != "http" && proxy.Scheme != "https" && proxy.Scheme != "socks5" && proxy.Scheme != "socks5h") {
		return nil, errors.New("系统代理地址无效，请检查代理设置")
	}
	return proxy, nil
}

type runtimeHTTPError struct{ status int }

func (err runtimeHTTPError) Error() string { return fmt.Sprintf("HTTP %d", err.status) }

func retryRuntimeDownload(ctx context.Context, attempts int, delay time.Duration, download func() error) error {
	var last error
	for attempt := 0; attempt < attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		last = download()
		if last == nil || !retryableRuntimeDownload(last) {
			return last
		}
		if attempt+1 < attempts {
			timer := time.NewTimer(delay * time.Duration(attempt+1))
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return last
}

func retryableRuntimeDownload(err error) bool {
	var status runtimeHTTPError
	if errors.As(err, &status) {
		return status.status == 408 || status.status == 429 || status.status >= 500
	}
	var networkError net.Error
	return errors.As(err, &networkError) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}
