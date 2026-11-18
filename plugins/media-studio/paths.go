// paths.go —— 插件私有数据目录、工作区内路径与 ID 生成。
package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// pluginFolderName 只在 Agent 未注入 HIMIND_PLUGIN_DATA_ROOT 时兜底使用。
const pluginFolderName = "himind-media-studio"

// dataRoot 返回插件私有数据根目录（必要时创建）。
//
// 由 Agent 按插件 ID 推导后注入，插件无法把自己指向别的扩展目录；
// 独立运行（无 Agent）时退回用户缓存目录，便于本地调试。
func dataRoot() (string, error) {
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

// dataSub 返回数据根下的子目录（必要时创建）。
func dataSub(name string) (string, error) {
	root, err := dataRoot()
	if err != nil {
		return "", err
	}
	target := filepath.Join(root, name)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", fmt.Errorf("创建目录失败 %s: %w", name, err)
	}
	return target, nil
}

// cleanWorkspace 校验调用方给出的工作区：必须是已存在的绝对目录。
func cleanWorkspace(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", fmt.Errorf("workspace_root 不能为空")
	}
	if !filepath.IsAbs(value) {
		return "", fmt.Errorf("workspace_root 必须是绝对路径: %s", value)
	}
	info, err := os.Stat(value)
	if err != nil {
		return "", fmt.Errorf("workspace_root 不可用: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workspace_root 不是目录: %s", value)
	}
	return filepath.Clean(value), nil
}

// resolveInside 把相对路径解析到工作区内，并拒绝任何越界结果。
func resolveInside(root, raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", fmt.Errorf("路径不能为空")
	}
	target := value
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	target = filepath.Clean(target)
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return "", fmt.Errorf("无法解析路径 %s: %w", value, err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("路径必须位于工作区内: %s", value)
	}
	return target, nil
}

// fileInside 判断一个已存在的文件是否在工作区内（用于校验媒体库等外部来源的素材）。
func fileInside(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func randomHex(bytes int) string {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		// rand 失败时退回时间戳，宁可 ID 弱一点也不要让能力直接失败。
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(buffer)
}

func newID(prefix string) string {
	return prefix + "_" + randomHex(8)
}

func nowText() string {
	return time.Now().UTC().Format(time.RFC3339)
}
