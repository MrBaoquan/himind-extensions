// Package mediadownload implements controlled media resolution, authenticated
// platform sessions and transfers. Product entry points own their data identity.
package mediadownload

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

// Config is immutable after process startup. It never accepts credentials.
type Config struct {
	PluginID       string
	DataRoot       string
	StrictDataRoot bool
}

var productConfig = Config{PluginID: "com.himind.media-downloader"}
var productConfigMu sync.RWMutex

func currentProductConfig() Config {
	productConfigMu.RLock()
	defer productConfigMu.RUnlock()
	return productConfig
}

// Configure must be called once by the executable, before serving requests.
func Configure(config Config) error {
	if config.PluginID != "com.himind.media-library" && config.PluginID != "com.himind.media-downloader" {
		return fmt.Errorf("下载内核的插件身份无效")
	}
	if config.DataRoot != "" && !filepath.IsAbs(config.DataRoot) {
		return fmt.Errorf("下载内核数据目录必须是绝对路径")
	}
	if config.StrictDataRoot && strings.TrimSpace(config.DataRoot) == "" {
		return fmt.Errorf("下载内核需要固定数据目录")
	}
	productConfigMu.Lock()
	productConfig = config
	productConfigMu.Unlock()
	return nil
}

func Runtime(ctx context.Context) RuntimeStatus { return buildRuntimeStatus(ctx) }
func Install(ctx context.Context, force bool) (RuntimeInstallResult, error) {
	return installRuntime(ctx, force)
}

func Resolve(ctx context.Context, input InfoInput) (MediaInfo, error) {
	location, err := locateYtDlp()
	if err != nil {
		return MediaInfo{}, err
	}
	return probeMedia(ctx, location.Path, input)
}

func ConnectionStatus() (SiteStatus, error) { return siteStatus() }
func Connect() (SiteStatus, error)          { return connectSite() }
func Disconnect() (SiteStatus, error)       { return disconnectSite() }

// ValidateURL applies the same public HTTP(S) target policy to both products.
func ValidateURL(raw string) error {
	_, err := validateMediaURL(raw)
	return err
}
