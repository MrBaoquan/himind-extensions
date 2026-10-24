package design

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type ProjectInspectInput struct {
	WorkspaceRoot string `json:"workspace_root"`
	MaxDepth      int    `json:"max_depth"`
	MaxFiles      int    `json:"max_files"`
}

var skippedDirectories = map[string]bool{
	".git": true, ".hg": true, ".svn": true, "node_modules": true,
	"dist": true, "build": true, "target": true, ".next": true,
	".nuxt": true, ".cache": true, "bin": true, "obj": true,
}

func (c *Catalog) InspectProject(in ProjectInspectInput) (map[string]any, error) {
	root, err := resolveWorkspaceRoot(in.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	maxDepth := in.MaxDepth
	if maxDepth == 0 {
		maxDepth = 4
	}
	if maxDepth < 1 || maxDepth > 8 {
		return nil, errInvalid("max_depth 必须在 1 到 8 之间")
	}
	maxFiles := in.MaxFiles
	if maxFiles == 0 {
		maxFiles = 3000
	}
	if maxFiles < 1 || maxFiles > 10000 {
		return nil, errInvalid("max_files 必须在 1 到 10000 之间")
	}

	files := []string{}
	truncated := false
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		relative = filepath.ToSlash(relative)
		depth := strings.Count(relative, "/") + 1
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if skippedDirectories[strings.ToLower(entry.Name())] || depth > maxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if depth > maxDepth {
			return nil
		}
		if len(files) >= maxFiles {
			truncated = true
			return filepath.SkipAll
		}
		files = append(files, relative)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("扫描工作区失败：%w", err)
	}
	sort.Strings(files)

	detected := []map[string]any{}
	evidence := []string{}
	extensionCounts := map[string]int{}
	for _, file := range files {
		extensionCounts[strings.ToLower(filepath.Ext(file))]++
	}
	add := func(name, confidence, evidencePath string) {
		detected = append(detected, map[string]any{"name": name, "confidence": confidence, "evidence": evidencePath})
	}
	hasFile := func(matcher func(string) bool) (string, bool) {
		for _, file := range files {
			if matcher(file) {
				return file, true
			}
		}
		return "", false
	}

	var packageDoc struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
		Scripts         map[string]string `json:"scripts"`
	}
	if packagePath, ok := hasFile(func(file string) bool { return file == "package.json" || strings.HasSuffix(file, "/package.json") }); ok {
		data, readErr := readSafeFile(root, filepath.Join(root, filepath.FromSlash(packagePath)), 2<<20)
		if readErr == nil && json.Unmarshal(data, &packageDoc) == nil {
			deps := map[string]string{}
			for name, version := range packageDoc.Dependencies {
				deps[name] = version
			}
			for name, version := range packageDoc.DevDependencies {
				deps[name] = version
			}
			stackChecks := []struct{ dep, label string }{
				{"next", "Next.js"}, {"nuxt", "Nuxt"}, {"@angular/core", "Angular"},
				{"svelte", "Svelte"}, {"vue", "Vue"}, {"react-native", "React Native"},
				{"expo", "Expo"}, {"react", "React"}, {"tailwindcss", "Tailwind CSS"},
				{"typescript", "TypeScript"}, {"vite", "Vite"},
			}
			for _, check := range stackChecks {
				if version, present := deps[check.dep]; present {
					add(check.label, "high", packagePath+" ("+check.dep+" "+version+")")
				}
			}
			evidence = append(evidence, packagePath)
		}
	}
	if file, ok := hasFile(func(file string) bool { return file == "components.json" }); ok {
		add("shadcn/ui", "high", file)
		evidence = append(evidence, file)
	}
	if file, ok := hasFile(func(file string) bool { return file == "pubspec.yaml" }); ok {
		add("Flutter / Dart", "high", file)
		evidence = append(evidence, file)
	}
	if file, ok := hasFile(func(file string) bool {
		return strings.HasSuffix(strings.ToLower(file), ".csproj") || strings.HasSuffix(strings.ToLower(file), ".sln")
	}); ok {
		add(".NET / Windows 桌面", "medium", file)
		evidence = append(evidence, file)
	}
	if extensionCounts[".xaml"] > 0 || extensionCounts[".axaml"] > 0 {
		file, _ := hasFile(func(file string) bool {
			lower := strings.ToLower(file)
			return strings.HasSuffix(lower, ".xaml") || strings.HasSuffix(lower, ".axaml")
		})
		add("XAML UI", "high", file)
		evidence = append(evidence, file)
		if extensionCounts[".axaml"] > 0 {
			add("Avalonia", "high", file)
		}
	}
	if extensionCounts[".swift"] > 0 {
		file, _ := hasFile(func(file string) bool { return strings.HasSuffix(strings.ToLower(file), ".swift") })
		add("SwiftUI / Swift", "medium", file)
		evidence = append(evidence, file)
	}
	if extensionCounts[".dart"] > 0 {
		file, _ := hasFile(func(file string) bool { return strings.HasSuffix(strings.ToLower(file), ".dart") })
		add("Flutter / Dart", "medium", file)
		evidence = append(evidence, file)
	}

	designFiles := []string{}
	tokenFiles := []string{}
	for _, file := range files {
		lower := strings.ToLower(file)
		base := strings.ToLower(filepath.Base(file))
		if base == "design.md" || strings.Contains(lower, "design-system/") || strings.Contains(lower, "design-guidelines") {
			designFiles = append(designFiles, file)
		}
		isXaml := strings.HasSuffix(base, ".xaml") || strings.HasSuffix(base, ".axaml")
		hasXamlTokenPattern := isXaml && (base == "app.axaml" || strings.Contains(base, "token") ||
			strings.Contains(base, "theme") || strings.Contains(base, "resource") || strings.Contains(lower, "/themes/"))
		if strings.Contains(base, "token") || base == "theme.json" || base == "globals.css" || base == "styles.css" || strings.HasSuffix(base, ".tokens.json") || hasXamlTokenPattern {
			tokenFiles = append(tokenFiles, file)
		}
	}
	unique := func(values []string) []string {
		seen := map[string]bool{}
		out := []string{}
		for _, value := range values {
			if value != "" && !seen[value] {
				seen[value] = true
				out = append(out, value)
			}
		}
		sort.Strings(out)
		return out
	}
	evidence = unique(evidence)
	designFiles = unique(designFiles)
	tokenFiles = unique(tokenFiles)

	warnings := []string{}
	if !hasAnyStack(detected) {
		warnings = append(warnings, "未从常见工程文件识别出技术栈，请提供项目入口或 package.json。")
	}
	if len(designFiles) == 0 && len(tokenFiles) == 0 {
		warnings = append(warnings, "未发现设计系统或令牌文件，生成设计系统前应先确认品牌约束。")
	}
	return map[string]any{
		"status":         "ok",
		"workspace_name": filepath.Base(root),
		"detected":       detected,
		"design_system": map[string]any{
			"design_files":      designFiles,
			"token_files":       tokenFiles,
			"has_design_system": len(designFiles) > 0,
			"has_design_tokens": len(tokenFiles) > 0,
		},
		"evidence":         evidence,
		"extension_counts": extensionCounts,
		"scan": map[string]any{
			"files_scanned": len(files),
			"max_depth":     maxDepth,
			"max_files":     maxFiles,
			"truncated":     truncated,
		},
		"warnings": warnings,
	}, nil
}

func hasAnyStack(items []map[string]any) bool {
	for _, item := range items {
		if name, ok := item["name"].(string); ok && name != "" {
			return true
		}
	}
	return false
}

func resolveWorkspaceRoot(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errInvalid("workspace_root 不能为空")
	}
	if !filepath.IsAbs(raw) {
		return "", errInvalid("workspace_root 必须是绝对路径")
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", errInvalid("workspace_root 无效")
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", errInvalid("workspace_root 不存在或不可访问")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", errInvalid("workspace_root 必须是目录")
	}
	return filepath.Clean(resolved), nil
}

func withinRoot(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func readSafeFile(root, fullPath string, maxBytes int64) ([]byte, error) {
	resolved, err := filepath.EvalSymlinks(fullPath)
	if err != nil {
		return nil, err
	}
	if !withinRoot(root, resolved) {
		return nil, errInvalid("文件不在 workspace_root 内")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errInvalid("目标不是普通文件")
	}
	if info.Size() > maxBytes {
		return nil, errInvalid("文件超过读取上限")
	}
	return os.ReadFile(resolved)
}
