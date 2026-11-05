package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRuntime(t *testing.T, root, platform, ffmpegContent, ffprobeContent string, manifestMode string) string {
	t.Helper()
	dir := filepath.Join(root, "runtime", platform)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ffmpegPath := filepath.Join(dir, executableName("ffmpeg", platform))
	ffprobePath := filepath.Join(dir, executableName("ffprobe", platform))
	if err := os.WriteFile(ffmpegPath, []byte(ffmpegContent), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ffprobePath, []byte(ffprobeContent), 0o755); err != nil {
		t.Fatal(err)
	}
	if manifestMode == "none" {
		return dir
	}
	value := manifest{
		Version:  "test-1",
		Platform: platform,
		Files: map[string]manifestFile{
			executableName("ffmpeg", platform):  {SHA256: digestOf(ffmpegContent), Size: int64(len(ffmpegContent))},
			executableName("ffprobe", platform): {SHA256: digestOf(ffprobeContent), Size: int64(len(ffprobeContent))},
		},
	}
	if manifestMode == "bad-hash" {
		value.Files[executableName("ffmpeg", platform)] = manifestFile{SHA256: strings.Repeat("0", 64)}
	}
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestName), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func digestOf(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func TestLocatePrefersPluginRuntimeBeforeDataAndPath(t *testing.T) {
	bundleRoot := t.TempDir()
	dataRoot := t.TempDir()
	bundleDir := writeRuntime(t, bundleRoot, "windows-x64", "bundle-ffmpeg", "bundle-ffprobe", "valid")
	writeRuntime(t, dataRoot, "windows-x64", "data-ffmpeg", "data-ffprobe", "valid")

	tools, err := (Locator{
		PluginRoots: []string{bundleRoot},
		DataRoots:   []string{dataRoot},
		Platform:    "windows-x64",
		LookPath: func(string) (string, error) {
			t.Fatal("已有插件内置运行时时不应访问 PATH")
			return "", nil
		},
	}).Locate()
	if err != nil {
		t.Fatal(err)
	}
	if tools.Source != SourcePluginBundle {
		t.Fatalf("运行时应优先来自插件包，实际 %s", tools.Source)
	}
	if filepath.Clean(tools.RuntimeRoot) != filepath.Clean(bundleDir) {
		t.Fatalf("错误的内置运行时目录: %s", tools.RuntimeRoot)
	}
	if tools.ManifestVersion != "test-1" {
		t.Fatalf("未保留 manifest 版本: %#v", tools)
	}
}

func TestLocateFallsBackToPluginDataBeforePath(t *testing.T) {
	dataRoot := t.TempDir()
	dataDir := writeRuntime(t, dataRoot, "windows-x64", "data-ffmpeg", "data-ffprobe", "none")

	tools, err := (Locator{
		PluginRoots: []string{t.TempDir()},
		DataRoots:   []string{dataRoot},
		Platform:    "windows-x64",
		LookPath: func(string) (string, error) {
			t.Fatal("已有插件数据目录运行时时不应访问 PATH")
			return "", nil
		},
	}).Locate()
	if err != nil {
		t.Fatal(err)
	}
	if tools.Source != SourcePluginData {
		t.Fatalf("运行时应回退到插件数据目录，实际 %s", tools.Source)
	}
	if filepath.Clean(tools.RuntimeRoot) != filepath.Clean(dataDir) {
		t.Fatalf("错误的插件数据运行时目录: %s", tools.RuntimeRoot)
	}
	if len(tools.Warnings) == 0 || !strings.Contains(tools.Warnings[0], "manifest.json") {
		t.Fatalf("未登记 manifest 的开发运行时必须给出风险提示: %#v", tools.Warnings)
	}
}

func TestLocateFallsBackToPathLast(t *testing.T) {
	looked := make([]string, 0, 2)
	tools, err := (Locator{
		PluginRoots: []string{t.TempDir()},
		DataRoots:   []string{t.TempDir()},
		Platform:    "windows-x64",
		LookPath: func(name string) (string, error) {
			looked = append(looked, name)
			return filepath.Join("system", name), nil
		},
	}).Locate()
	if err != nil {
		t.Fatal(err)
	}
	if tools.Source != SourcePath {
		t.Fatalf("运行时应最终回退到 PATH，实际 %s", tools.Source)
	}
	if len(looked) != 2 || looked[0] != "ffmpeg.exe" || looked[1] != "ffprobe.exe" {
		t.Fatalf("PATH 查找顺序错误: %#v", looked)
	}
}

func TestLocateRejectsManifestHashMismatchAndContinues(t *testing.T) {
	bundleRoot := t.TempDir()
	dataRoot := t.TempDir()
	writeRuntime(t, bundleRoot, "windows-x64", "tampered-ffmpeg", "tampered-ffprobe", "bad-hash")
	writeRuntime(t, dataRoot, "windows-x64", "valid-ffmpeg", "valid-ffprobe", "valid")

	tools, err := (Locator{
		PluginRoots: []string{bundleRoot},
		DataRoots:   []string{dataRoot},
		Platform:    "windows-x64",
		LookPath: func(string) (string, error) {
			t.Fatal("修复数据目录运行时应优先于 PATH")
			return "", nil
		},
	}).Locate()
	if err != nil {
		t.Fatal(err)
	}
	if tools.Source != SourcePluginData {
		t.Fatalf("损坏的内置运行时不应被采用，实际 %s", tools.Source)
	}
}

func TestLocateReportsIncompleteRuntimeInsteadOfMixingBinaries(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "runtime", "windows-x64")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ffmpeg.exe"), []byte("only-ffmpeg"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := (Locator{
		PluginRoots: []string{root},
		DataRoots:   []string{t.TempDir()},
		Platform:    "windows-x64",
		LookPath: func(string) (string, error) {
			return "", os.ErrNotExist
		},
	}).Locate()
	if err == nil || !strings.Contains(err.Error(), "缺少 ffprobe") || !strings.Contains(err.Error(), "PATH 中未找到 ffmpeg") {
		t.Fatalf("应报告不完整运行时并继续查找，实际: %v", err)
	}
}

func TestPlatformKey(t *testing.T) {
	cases := map[string]string{
		"windows/amd64": "windows-x64",
		"windows/arm64": "windows-arm64",
		"linux/amd64":   "linux-x64",
		"darwin/arm64":  "darwin-arm64",
	}
	for input, expected := range cases {
		parts := strings.Split(input, "/")
		if got := PlatformKey(parts[0], parts[1]); got != expected {
			t.Fatalf("PlatformKey(%q)=%q，期望 %q", input, got, expected)
		}
	}
}
