// Package plugindata 解析插件的私有数据目录（沿用工作区既有约定）。
package plugindata

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const pluginFolderName = "himind-media-ai"

// Root 返回插件数据根目录（必要时创建）。
func Root() (string, error) {
	if value := strings.TrimSpace(os.Getenv("HIMIND_PLUGIN_DATA_ROOT")); value != "" {
		if err := os.MkdirAll(value, 0o755); err != nil {
			return "", fmt.Errorf("创建插件数据目录失败: %w", err)
		}
		return value, nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("无法确定插件数据目录: %w", err)
	}
	root := filepath.Join(cache, pluginFolderName)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("创建插件数据目录失败: %w", err)
	}
	return root, nil
}

// Sub 返回数据根下的子目录（必要时创建）。
func Sub(name string) (string, error) {
	root, err := Root()
	if err != nil {
		return "", err
	}
	target := filepath.Join(root, name)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", fmt.Errorf("创建目录失败 %s: %w", name, err)
	}
	return target, nil
}
