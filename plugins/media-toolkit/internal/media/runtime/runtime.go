// Package runtime 定位 ffmpeg / ffprobe 的托管运行时。
//
// 解析顺序固定为：插件包内置 -> 插件私有数据目录 -> 系统 PATH。
// 插件包与数据目录中的运行时都使用同一套平台目录约定：
//
//	<root>/runtime/windows-x64/{ffmpeg.exe,ffprobe.exe,manifest.json}
//
// manifest.json 是可选的。开发期允许直接放入未经清单登记的本机构建；
// 一旦提供 manifest，就必须通过平台、文件摘要校验，避免把损坏或被替换的
// 二进制误当成已验证运行时。PATH 回退只作为开发便利，不参与 manifest 校验。
package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"himind-plugin/media-toolkit/internal/media/plugindata"
)

const ManifestName = "manifest.json"

const (
	SourcePluginBundle = "plugin_bundle"
	SourcePluginData   = "plugin_data"
	SourcePath         = "path"
)

// ErrNotAvailable 表示三级位置都没有找到成对的 ffmpeg / ffprobe。
var ErrNotAvailable = errors.New("未找到成对的 ffmpeg / ffprobe 运行时")

// Tools 是定位结果，不含执行逻辑。
type Tools struct {
	FFmpeg          string   `json:"ffmpeg_path"`
	FFprobe         string   `json:"ffprobe_path"`
	Source          string   `json:"source"`
	RuntimeRoot     string   `json:"runtime_root,omitempty"`
	ManifestVersion string   `json:"manifest_version,omitempty"`
	Warnings        []string `json:"warnings,omitempty"`
}

// Locator 可注入路径与查找函数，便于测试且不依赖真实安装。
type Locator struct {
	PluginRoots []string
	DataRoots   []string
	Platform    string
	LookPath    func(string) (string, error)
}

// DefaultLocator 使用插件进程可推断出的根目录、插件数据目录和系统 PATH。
func DefaultLocator() Locator {
	return Locator{
		PluginRoots: defaultPluginRoots(),
		DataRoots:   defaultDataRoots(),
		Platform:    PlatformKey(runtime.GOOS, runtime.GOARCH),
		LookPath:    exec.LookPath,
	}
}

// Locate 按固定顺序返回第一组完整且通过校验的运行时。
func (locator Locator) Locate() (Tools, error) {
	lookPath := locator.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	platform := strings.TrimSpace(locator.Platform)
	if platform == "" {
		platform = PlatformKey(runtime.GOOS, runtime.GOARCH)
	}

	var attempts []string
	seen := make(map[string]bool)
	for _, candidate := range []struct {
		roots  []string
		source string
	}{
		{locator.PluginRoots, SourcePluginBundle},
		{locator.DataRoots, SourcePluginData},
	} {
		for _, root := range candidate.roots {
			root = strings.TrimSpace(root)
			if root == "" {
				continue
			}
			for _, dir := range runtimeDirs(root, platform) {
				key := filepath.Clean(dir)
				if seen[key] {
					continue
				}
				seen[key] = true
				tools, found, err := inspectDirectory(dir, platform, candidate.source)
				if found {
					return tools, nil
				}
				if err != nil {
					attempts = append(attempts, err.Error())
				}
			}
		}
	}

	if tools, err := inspectPATH(platform, lookPath); err == nil {
		return tools, nil
	} else {
		attempts = append(attempts, err.Error())
	}

	if len(attempts) == 0 {
		return Tools{}, ErrNotAvailable
	}
	return Tools{}, fmt.Errorf("%w: %s", ErrNotAvailable, strings.Join(attempts, " | "))
}

// PlatformKey 将 Go 的 GOOS/GOARCH 转成运行时目录名。
func PlatformKey(goos, goarch string) string {
	arch := strings.ToLower(strings.TrimSpace(goarch))
	switch arch {
	case "amd64":
		arch = "x64"
	case "386":
		arch = "x86"
	case "arm64":
		arch = "arm64"
	}
	return strings.ToLower(strings.TrimSpace(goos)) + "-" + arch
}

func defaultPluginRoots() []string {
	roots := make([]string, 0, 4)
	if value := strings.TrimSpace(os.Getenv("HIMIND_PLUGIN_ROOT")); value != "" {
		roots = append(roots, value)
	}
	if executable, err := os.Executable(); err == nil {
		if resolved, resolveErr := filepath.EvalSymlinks(executable); resolveErr == nil {
			executable = resolved
		}
		executableDir := filepath.Dir(executable)
		roots = append(roots, executableDir, filepath.Dir(executableDir))
	}
	if cwd, err := os.Getwd(); err == nil {
		if _, statErr := os.Stat(filepath.Join(cwd, "plugin.json")); statErr == nil {
			roots = append(roots, cwd)
		}
	}
	return uniqueStrings(roots)
}

func defaultDataRoots() []string {
	root, err := plugindata.Root()
	if err != nil {
		return nil
	}
	return []string{root}
}

func runtimeDirs(root, platform string) []string {
	return []string{
		filepath.Join(root, "runtime", platform),
		filepath.Join(root, "runtime"),
	}
}

func inspectDirectory(dir, platform, source string) (Tools, bool, error) {
	ffmpegPath := filepath.Join(dir, executableName("ffmpeg", platform))
	ffprobePath := filepath.Join(dir, executableName("ffprobe", platform))
	ffmpegOK := isRegularFile(ffmpegPath)
	ffprobeOK := isRegularFile(ffprobePath)
	if !ffmpegOK && !ffprobeOK {
		return Tools{}, false, nil
	}
	if !ffmpegOK || !ffprobeOK {
		return Tools{}, false, fmt.Errorf("%s 运行时不完整: %s", dir, missingToolName(ffmpegOK, ffprobeOK))
	}

	tools := Tools{
		FFmpeg:      ffmpegPath,
		FFprobe:     ffprobePath,
		Source:      source,
		RuntimeRoot: dir,
	}
	manifestPath := filepath.Join(dir, ManifestName)
	manifest, exists, err := readManifest(manifestPath)
	if err != nil {
		return Tools{}, false, fmt.Errorf("%s: %w", dir, err)
	}
	if !exists {
		tools.Warnings = append(tools.Warnings, "运行时未提供 manifest.json，已按本地开发构建使用但无法证明来源与完整性")
		return tools, true, nil
	}
	if manifest.Platform != "" && manifest.Platform != platform {
		return Tools{}, false, fmt.Errorf("%s: manifest 平台为 %s，期望 %s", dir, manifest.Platform, platform)
	}
	if err := verifyManifest(dir, manifest, platform); err != nil {
		return Tools{}, false, fmt.Errorf("%s: manifest 校验失败: %w", dir, err)
	}
	tools.ManifestVersion = strings.TrimSpace(manifest.Version)
	return tools, true, nil
}

func inspectPATH(platform string, lookPath func(string) (string, error)) (Tools, error) {
	ffmpegPath, ffmpegErr := lookPath(executableName("ffmpeg", platform))
	if ffmpegErr != nil {
		return Tools{}, fmt.Errorf("PATH 中未找到 ffmpeg")
	}
	ffprobePath, ffprobeErr := lookPath(executableName("ffprobe", platform))
	if ffprobeErr != nil {
		return Tools{}, fmt.Errorf("PATH 中找到了 ffmpeg，但未找到 ffprobe")
	}
	return Tools{
		FFmpeg:  ffmpegPath,
		FFprobe: ffprobePath,
		Source:  SourcePath,
		Warnings: []string{
			"正在使用系统 PATH 运行时；分发版应优先内置或托管运行时以保证可重复性",
		},
	}, nil
}

type manifest struct {
	Version  string                  `json:"version"`
	Platform string                  `json:"platform"`
	Files    map[string]manifestFile `json:"files"`
}

type manifestFile struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size,omitempty"`
}

func readManifest(path string) (manifest, bool, error) {
	payload, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return manifest{}, false, nil
	}
	if err != nil {
		return manifest{}, false, fmt.Errorf("读取 manifest.json 失败: %w", err)
	}
	var value manifest
	if err := json.Unmarshal(payload, &value); err != nil {
		return manifest{}, false, fmt.Errorf("manifest.json 不是合法 JSON: %w", err)
	}
	return value, true, nil
}

func verifyManifest(dir string, value manifest, platform string) error {
	for _, name := range []string{executableName("ffmpeg", platform), executableName("ffprobe", platform)} {
		entry, ok := value.Files[name]
		if !ok {
			return fmt.Errorf("缺少 %s 的摘要", name)
		}
		digest, size, err := sha256File(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("计算 %s 摘要失败: %w", name, err)
		}
		expected := strings.ToLower(strings.TrimSpace(entry.SHA256))
		if expected == "" {
			return fmt.Errorf("%s 的 sha256 为空", name)
		}
		if digest != expected {
			return fmt.Errorf("%s 摘要不匹配", name)
		}
		if entry.Size > 0 && entry.Size != size {
			return fmt.Errorf("%s 大小不匹配", name)
		}
	}
	return nil
}

func sha256File(path string) (string, int64, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return "", 0, err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), int64(len(payload)), nil
}

func executableName(base, platform string) string {
	if strings.HasPrefix(platform, "windows-") {
		return base + ".exe"
	}
	return base
}

func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func missingToolName(ffmpegOK, ffprobeOK bool) string {
	if !ffmpegOK {
		return "缺少 ffmpeg"
	}
	if !ffprobeOK {
		return "缺少 ffprobe"
	}
	return "缺少运行时文件"
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		clean := filepath.Clean(strings.TrimSpace(value))
		if clean == "." || seen[clean] {
			continue
		}
		seen[clean] = true
		result = append(result, clean)
	}
	return result
}
