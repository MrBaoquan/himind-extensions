package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MrBaoquan/himind-extensions/sdk/jsonrpc"
)

func TestValidateMediaURLRejectsUnsafeTargets(t *testing.T) {
	for _, value := range []string{
		"file:///tmp/video.mp4",
		"http://127.0.0.1/video",
		"http://169.254.169.254/latest/meta-data",
		"http://localhost/video",
		"http://10.0.0.1/video",
		"ftp://example.com/video",
	} {
		if _, err := validateMediaURL(value); err == nil {
			t.Fatalf("expected %q to be rejected", value)
		}
	}
	if _, err := validateMediaURL("https://1.1.1.1/video"); err != nil {
		t.Fatalf("expected public IP URL to pass: %v", err)
	}
}

func TestResolveDestinationStaysInsideWorkspace(t *testing.T) {
	root := t.TempDir()
	target, err := resolveDestination(root, "media/downloads")
	if err != nil {
		t.Fatal(err)
	}
	if !withinRoot(root, target) {
		t.Fatalf("destination escaped workspace: %s", target)
	}
	for _, value := range []string{"../outside", `..\\outside`} {
		if _, err := resolveDestination(root, value); err == nil {
			t.Fatalf("expected %q to be rejected", value)
		}
	}
}

func TestBuildDownloadArgsUsesControlledOptions(t *testing.T) {
	input := JobStartInput{
		URL:       "https://example.com/video",
		Quality:   "720p",
		MaxBytes:  123456,
		Subtitles: []string{"zh-Hans", "en"},
	}
	args, err := buildDownloadArgs(input, `C:\\workspace\\media`, true)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"--no-config", "--no-playlist", "--format", "bestvideo[height<=720]+bestaudio/best[height<=720]", "--max-filesize 123456", "--write-subs", "--sub-langs zh-Hans,en"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args missing %q: %s", want, joined)
		}
	}
	if strings.Contains(joined, "--exec") || strings.Contains(joined, "--external-downloader") {
		t.Fatalf("unsafe option leaked into args: %s", joined)
	}
}

func TestBuildDownloadArgsRequiresFFmpegForAudio(t *testing.T) {
	_, err := buildDownloadArgs(JobStartInput{URL: "https://example.com/video", AudioOnly: true}, t.TempDir(), false)
	if err == nil || !strings.Contains(err.Error(), "ffmpeg") {
		t.Fatalf("expected ffmpeg error, got %v", err)
	}
}

func TestParseProgressLine(t *testing.T) {
	progress, downloaded, total, speed, eta, ok := parseProgressLine("download: 42.5%|1.2MiB/s|00:12|1024|4096")
	if !ok {
		t.Fatal("expected progress line to parse")
	}
	if progress != 42.5 || downloaded != 1024 || total != 4096 || speed != "1.2MiB/s" || eta != "00:12" {
		t.Fatalf("unexpected progress: %v %v %v %q %q", progress, downloaded, total, speed, eta)
	}
}

func TestMediaInfoFromMap(t *testing.T) {
	root := map[string]any{
		"id":       "abc",
		"title":    "demo",
		"uploader": "tester",
		"duration": 12.5,
		"formats": []any{
			map[string]any{"format_id": "137", "height": 1080, "ext": "mp4", "vcodec": "avc1", "acodec": "none"},
			map[string]any{"format_id": "140", "ext": "m4a", "vcodec": "none", "acodec": "mp4a"},
		},
		"subtitles": map[string]any{"en": []any{}, "zh-Hans": []any{}},
	}
	info := mediaInfoFromMap(root, false)
	if info.ID != "abc" || info.Title != "demo" || info.Uploader != "tester" {
		t.Fatalf("unexpected info: %#v", info)
	}
	if len(info.Formats) != 2 || len(info.Subtitles) != 2 {
		t.Fatalf("unexpected formats/subtitles: %#v", info)
	}
}

func TestHandleRuntimeStatusReturnsStructuredResult(t *testing.T) {
	result, rpcErr := handle(jsonrpc.Request{
		Method: "media.downloader.runtime.status",
		Params: json.RawMessage(`{}`),
	})
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	status, ok := result.(RuntimeStatus)
	if !ok {
		t.Fatalf("unexpected result type: %T", result)
	}
	if status.State == "" {
		t.Fatal("runtime state must not be empty")
	}
}

func TestRuntimeLockHasPinnedChecksum(t *testing.T) {
	lock, err := readRuntimeLock()
	if err != nil {
		t.Fatal(err)
	}
	if lock.Runtime != "yt-dlp" || lock.Version == "" || len(lock.SHA256) != 64 {
		t.Fatalf("runtime lock is incomplete: %#v", lock)
	}
}

func TestWriteAndReadJob(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", root)
	record := JobRecord{ID: "abc123", Status: "queued", URL: "https://example.com/video", WorkspaceRoot: root, DestDir: filepath.Join(root, "media"), CreatedAt: "2026-10-12T00:00:00Z"}
	if err := saveJob(&record); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadJob("abc123")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != record.ID || loaded.Status != "queued" {
		t.Fatalf("unexpected loaded job: %#v", loaded)
	}
	if _, err := os.Stat(filepath.Join(root, "jobs", "abc123", "job.json")); err != nil {
		t.Fatal(err)
	}
}
