package design

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var safeSlugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type PersistInput struct {
	WorkspaceRoot string `json:"workspace_root"`
	ProjectSlug   string `json:"project_slug"`
	PageSlug      string `json:"page_slug"`
	Content       string `json:"content"`
	Overwrite     bool   `json:"overwrite"`
}

func (c *Catalog) PersistDesignSystem(in PersistInput) (map[string]any, error) {
	root, err := resolveWorkspaceRoot(in.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	projectSlug := strings.TrimSpace(in.ProjectSlug)
	pageSlug := strings.TrimSpace(in.PageSlug)
	if !safeSlugPattern.MatchString(projectSlug) || len(projectSlug) > 64 {
		return nil, errInvalid("project_slug 必须是 1 到 64 位的 lowercase-hyphen-case")
	}
	if pageSlug != "" && (!safeSlugPattern.MatchString(pageSlug) || len(pageSlug) > 64) {
		return nil, errInvalid("page_slug 必须是 1 到 64 位的 lowercase-hyphen-case")
	}
	content := in.Content
	if strings.TrimSpace(content) == "" {
		return nil, errInvalid("content 不能为空")
	}
	if len([]byte(content)) > 512<<10 {
		return nil, errInvalid("content 不能超过 512 KiB")
	}
	relative := filepath.ToSlash(filepath.Join("design-system", projectSlug, "MASTER.md"))
	if pageSlug != "" {
		relative = filepath.ToSlash(filepath.Join("design-system", projectSlug, "pages", pageSlug+".md"))
	}
	target, err := safeWorkspaceTarget(root, relative)
	if err != nil {
		return nil, err
	}
	if info, statErr := os.Lstat(target); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, errInvalid("目标文件不能是符号链接")
		}
		if info.IsDir() {
			return nil, errInvalid("目标路径是目录")
		}
		if !in.Overwrite {
			return map[string]any{
				"status":         "exists",
				"path":           relative,
				"overwrite":      false,
				"message":        "目标文件已存在；如需替换请显式设置 overwrite=true。",
				"existing_bytes": info.Size(),
			}, nil
		}
	} else if !os.IsNotExist(statErr) {
		return nil, fmt.Errorf("检查目标文件失败：%w", statErr)
	}
	if err := ensureNoSymlinkParents(root, filepath.Dir(target)); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, fmt.Errorf("创建设计系统目录失败：%w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(target), ".uiux-design-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("创建设计系统临时文件失败：%w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if _, err := temp.WriteString(content); err != nil {
		_ = temp.Close()
		return nil, fmt.Errorf("写入设计系统失败：%w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return nil, fmt.Errorf("同步设计系统失败：%w", err)
	}
	if err := temp.Close(); err != nil {
		return nil, fmt.Errorf("关闭设计系统临时文件失败：%w", err)
	}
	if err := os.Rename(tempName, target); err != nil {
		if !in.Overwrite {
			return nil, fmt.Errorf("保存设计系统失败：%w", err)
		}
		if removeErr := os.Remove(target); removeErr != nil && !os.IsNotExist(removeErr) {
			return nil, fmt.Errorf("替换已有设计系统失败：%w", removeErr)
		}
		if err := os.Rename(tempName, target); err != nil {
			return nil, fmt.Errorf("替换设计系统失败：%w", err)
		}
	}
	digest := sha256.Sum256([]byte(content))
	return map[string]any{
		"status":    "written",
		"path":      relative,
		"bytes":     len([]byte(content)),
		"sha256":    hex.EncodeToString(digest[:]),
		"overwrite": in.Overwrite,
	}, nil
}

func safeWorkspaceTarget(root, relative string) (string, error) {
	if filepath.IsAbs(relative) || strings.Contains(relative, ":") {
		return "", errInvalid("目标路径必须是相对路径")
	}
	cleaned := filepath.Clean(filepath.FromSlash(relative))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", errInvalid("目标路径越出 workspace_root")
	}
	full := filepath.Join(root, cleaned)
	if !withinRoot(root, full) {
		return "", errInvalid("目标路径越出 workspace_root")
	}
	return full, nil
}

func ensureNoSymlinkParents(root, parent string) error {
	relative, err := filepath.Rel(root, parent)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errInvalid("目标目录越出 workspace_root")
	}
	current := root
	for _, segment := range strings.Split(relative, string(filepath.Separator)) {
		if segment == "" || segment == "." {
			continue
		}
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("检查目标目录失败：%w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errInvalid("目标路径不能经过符号链接")
		}
		if !info.IsDir() {
			return errInvalid("目标路径父级不是目录")
		}
	}
	return nil
}
