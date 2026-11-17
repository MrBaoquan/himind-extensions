package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// revealPath 在系统文件管理器里定位文件或打开目录，用于「打开精选库」「定位素材」。
type revealInput struct {
	Path    string `json:"path"`
	AssetID string `json:"asset_id"`
}

func revealPath(in revealInput) (any, error) {
	target := strings.TrimSpace(in.Path)
	if target == "" && strings.TrimSpace(in.AssetID) != "" {
		db, err := openLibraryDB()
		if err != nil {
			return nil, err
		}
		defer db.Close()
		if err := db.QueryRow(`SELECT path FROM candidates WHERE id=?`, strings.TrimSpace(in.AssetID)).Scan(&target); err != nil {
			return nil, fmt.Errorf("素材不存在")
		}
	}
	if target == "" {
		return nil, fmt.Errorf("请指定要打开的路径")
	}
	if !filepath.IsAbs(target) {
		return nil, fmt.Errorf("路径必须是绝对路径")
	}
	info, err := os.Stat(target)
	if err != nil {
		return nil, fmt.Errorf("路径不可用: %w", err)
	}
	if runtime.GOOS != "windows" {
		return nil, fmt.Errorf("当前平台暂不支持打开文件管理器")
	}
	clean := filepath.Clean(target)
	var command *exec.Cmd
	if info.IsDir() {
		command = exec.Command("explorer.exe", clean)
	} else {
		command = exec.Command("explorer.exe", "/select,"+clean)
	}
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("打开文件管理器失败: %w", err)
	}
	kind := "file"
	if info.IsDir() {
		kind = "directory"
	}
	return map[string]any{"opened": true, "path": clean, "kind": kind}, nil
}
