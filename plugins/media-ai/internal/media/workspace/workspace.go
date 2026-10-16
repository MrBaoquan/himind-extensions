// Package workspace 把"读写都必须留在调用方指定的工作区内"这条约束收在一处。
// 插件规范禁止越出工作区读写路径；集中实现让"越界"只有一处可能出错。
package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Root 一个已解析的工作区根目录。
type Root struct {
	abs string
}

// Open 解析并校验工作区根目录。
func Open(raw string) (Root, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return Root{}, errors.New("workspace_root 不能为空")
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return Root{}, fmt.Errorf("workspace_root 无效: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return Root{}, fmt.Errorf("workspace_root 不存在: %w", err)
	}
	if !info.IsDir() {
		return Root{}, errors.New("workspace_root 不是目录")
	}
	return Root{abs: abs}, nil
}

// Abs 工作区根的绝对路径。
func (r Root) Abs() string { return r.abs }

// Resolve 把相对路径解析到工作区内；越界一律拒绝。
//
// 先解析再比较，而不是拼字符串比前缀：`..`、符号链接、大小写差异都会让前缀比较给出错误结论。
func (r Root) Resolve(relative string) (string, error) {
	value := strings.TrimSpace(relative)
	if value == "" {
		return "", errors.New("路径不能为空")
	}
	if filepath.IsAbs(value) {
		return r.EnsureInside(value)
	}
	return r.EnsureInside(filepath.Join(r.abs, value))
}

// EnsureInside 校验一个绝对路径确实在工作区内。
func (r Root) EnsureInside(abs string) (string, error) {
	resolved, err := filepath.Abs(abs)
	if err != nil {
		return "", fmt.Errorf("路径无效: %w", err)
	}
	// 输出文件通常还不存在，因此比较"最近的已存在祖先"+剩余部分。
	existing := resolved
	var tail []string
	for {
		if _, statErr := os.Lstat(existing); statErr == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		tail = append([]string{filepath.Base(existing)}, tail...)
		existing = parent
	}
	realExisting, err := filepath.EvalSymlinks(existing)
	if err != nil {
		realExisting = existing
	}
	final := realExisting
	if len(tail) > 0 {
		final = filepath.Join(append([]string{realExisting}, tail...)...)
	}
	root := r.abs
	if realRoot, err := filepath.EvalSymlinks(r.abs); err == nil {
		root = realRoot
	}
	if !isWithin(root, final) {
		return "", fmt.Errorf("路径越出工作区: %s", abs)
	}
	return resolved, nil
}

func isWithin(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".."
}

// EnsureParentDir 创建输出文件的父目录。
func EnsureParentDir(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0o755)
}
