package ffmpeg

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	executable, err := os.Executable()
	if err != nil {
		os.Exit(1)
	}
	name := strings.ToLower(filepath.Base(executable))
	if name == "ffmpeg.exe" || name == "ffmpeg" {
		os.Exit(fakeFFmpeg(os.Args[1:]))
	}
	if name == "ffprobe.exe" || name == "ffprobe" {
		os.Exit(fakeFFprobe(os.Args[1:]))
	}
	os.Exit(m.Run())
}

func fakeFFmpeg(args []string) int {
	for _, arg := range args {
		if arg == "-version" {
			fmt.Println("ffmpeg version fake-runtime")
			return 0
		}
	}
	if len(args) == 0 {
		return 2
	}
	output := args[len(args)-1]
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return 1
	}
	if err := os.WriteFile(output, []byte("fake-media-output"), 0o644); err != nil {
		return 1
	}
	return 0
}

func fakeFFprobe(args []string) int {
	fmt.Println(`{"streams":[],"format":{}}`)
	return 0
}

func copyExecutable(t *testing.T, source, target string) {
	t.Helper()
	input, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := os.Create(target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
}

func fileDigest(t *testing.T, path string) (string, int64) {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), int64(len(payload))
}

func TestLocateAndRunFromPluginRuntimeWithoutPATH(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	pluginRoot := t.TempDir()
	runtimeDir := filepath.Join(pluginRoot, "runtime", "windows-x64")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ffmpegPath := filepath.Join(runtimeDir, "ffmpeg.exe")
	ffprobePath := filepath.Join(runtimeDir, "ffprobe.exe")
	copyExecutable(t, executable, ffmpegPath)
	copyExecutable(t, executable, ffprobePath)
	ffmpegHash, ffmpegSize := fileDigest(t, ffmpegPath)
	ffprobeHash, ffprobeSize := fileDigest(t, ffprobePath)
	manifest := map[string]any{
		"version": "fake-runtime", "platform": "windows-x64",
		"files": map[string]any{
			"ffmpeg.exe":  map[string]any{"sha256": ffmpegHash, "size": ffmpegSize},
			"ffprobe.exe": map[string]any{"sha256": ffprobeHash, "size": ffprobeSize},
		},
	}
	payload, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(runtimeDir, "manifest.json"), payload, 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HIMIND_PLUGIN_ROOT", pluginRoot)
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	t.Setenv("PATH", "")
	tools, err := Locate()
	if err != nil {
		t.Fatal(err)
	}
	if tools.Source != "plugin_bundle" || tools.ManifestVersion != "fake-runtime" {
		t.Fatalf("没有从插件内置运行时定位: %+v", tools)
	}
	output := filepath.Join(t.TempDir(), "result.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := tools.Run(ctx, RunOptions{Args: []string{
		"-f", "lavfi", "-i", "color=c=red:s=32x32", output,
	}}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(output); err != nil || info.Size() == 0 {
		t.Fatalf("无 PATH 时没有完成转换: %v", err)
	}
}
