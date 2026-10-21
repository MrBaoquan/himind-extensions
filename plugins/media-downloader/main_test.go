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

func TestResolveDestinationAcceptsAbsoluteDirectory(t *testing.T) {
	root := t.TempDir()
	downloads := filepath.Join(root, "Downloads")
	target, err := resolveDestination(t.TempDir(), downloads)
	if err != nil {
		t.Fatal(err)
	}
	if target != downloads {
		t.Fatalf("unexpected absolute destination: %s", target)
	}
}

func TestResolveDestinationDefaultsToDownloadsDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	target, err := resolveDestination(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(target, filepath.Join(home, "Downloads")) {
		t.Fatalf("unexpected default destination: %s", target)
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

func TestListJobRecordsFiltersPaginatesAndSummarizes(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", root)
	records := []JobRecord{
		{ID: "job-a", Status: "completed", URL: "https://example.com/alpha", Title: "Alpha completed", WorkspaceRoot: root, DestDir: filepath.Join(root, "media"), CreatedAt: "2026-10-12T10:00:00Z", OutputBytes: 100},
		{ID: "job-b", Status: "completed", URL: "https://example.com/beta", Title: "Beta completed", WorkspaceRoot: root, DestDir: filepath.Join(root, "media"), CreatedAt: "2026-10-12T11:00:00Z", OutputBytes: 300},
		{ID: "job-c", Status: "failed", URL: "https://example.com/alpha-failed", Title: "Alpha failed", WorkspaceRoot: root, DestDir: filepath.Join(root, "media"), CreatedAt: "2026-10-12T12:00:00Z", Error: "network"},
	}
	for i := range records {
		if err := saveJob(&records[i]); err != nil {
			t.Fatal(err)
		}
	}

	result, err := listJobRecords(JobListInput{
		Limit:    1,
		Offset:   0,
		Query:    "alpha",
		Statuses: []string{"completed"},
		Sort:     "created_desc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 || result.Count != 1 || result.Items[0].ID != "job-a" {
		t.Fatalf("unexpected filtered page: %#v", result)
	}
	if result.Summary.Total != 3 || result.Summary.Completed != 2 || result.Summary.Failed != 1 || result.Summary.OutputBytes != 400 {
		t.Fatalf("unexpected summary: %#v", result.Summary)
	}

	page, err := listJobRecords(JobListInput{Limit: 2, Offset: 1, Sort: "created_asc"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 3 || page.Count != 2 || page.Items[0].ID != "job-b" || page.Items[1].ID != "job-c" {
		t.Fatalf("unexpected pagination: %#v", page)
	}
}

func TestRemoveJobRecordOnlyDeletesFinishedJobs(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", root)
	finished := JobRecord{ID: "job-done", Status: "completed", URL: "https://example.com/video", WorkspaceRoot: root, DestDir: filepath.Join(root, "media"), CreatedAt: "2026-10-12T00:00:00Z"}
	if err := saveJob(&finished); err != nil {
		t.Fatal(err)
	}
	if _, err := removeJobRecord(finished.ID); err != nil {
		t.Fatal(err)
	}
	dir, err := jobDir(finished.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("expected job directory to be deleted, got %v", err)
	}

	active := JobRecord{ID: "job-active", Status: "running", URL: "https://example.com/video", WorkspaceRoot: root, DestDir: filepath.Join(root, "media"), CreatedAt: "2026-10-12T00:00:00Z"}
	if err := saveJob(&active); err != nil {
		t.Fatal(err)
	}
	if _, err := removeJobRecord(active.ID); err == nil {
		t.Fatal("expected running job removal to be rejected")
	}
}

func TestCleanupJobRecordsDryRunAndBatch(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", root)
	records := []JobRecord{
		{ID: "job-done", Status: "completed", URL: "https://example.com/a", WorkspaceRoot: root, DestDir: filepath.Join(root, "media"), CreatedAt: "2026-10-10T00:00:00Z"},
		{ID: "job-failed", Status: "failed", URL: "https://example.com/b", WorkspaceRoot: root, DestDir: filepath.Join(root, "media"), CreatedAt: "2026-10-11T00:00:00Z"},
		{ID: "job-running", Status: "running", URL: "https://example.com/c", WorkspaceRoot: root, DestDir: filepath.Join(root, "media"), CreatedAt: "2026-10-12T00:00:00Z"},
	}
	for i := range records {
		if err := saveJob(&records[i]); err != nil {
			t.Fatal(err)
		}
	}

	dryRun, err := cleanupJobRecords(JobCleanupInput{Statuses: []string{"completed", "failed"}, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if dryRun.Matched != 2 || dryRun.Removed != 0 || dryRun.Remaining != 2 {
		t.Fatalf("unexpected dry run result: %#v", dryRun)
	}

	first, err := cleanupJobRecords(JobCleanupInput{Statuses: []string{"completed", "failed"}, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if first.Matched != 2 || first.Removed != 1 || first.Remaining != 1 {
		t.Fatalf("unexpected first cleanup result: %#v", first)
	}

	second, err := cleanupJobRecords(JobCleanupInput{Statuses: []string{"completed", "failed"}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if second.Matched != 1 || second.Removed != 1 || second.Remaining != 0 {
		t.Fatalf("unexpected second cleanup result: %#v", second)
	}

	if _, err := os.Stat(filepath.Join(root, "jobs", "job-running")); err != nil {
		t.Fatalf("running job should be kept: %v", err)
	}
}

func TestJobListCacheInvalidatesOnSave(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", root)
	first := JobRecord{ID: "job-cache-a", Status: "completed", URL: "https://example.com/a", WorkspaceRoot: root, DestDir: filepath.Join(root, "media"), CreatedAt: "2026-10-10T00:00:00Z"}
	if err := saveJob(&first); err != nil {
		t.Fatal(err)
	}
	result, err := listJobRecords(JobListInput{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 {
		t.Fatalf("unexpected initial total: %#v", result)
	}
	second := JobRecord{ID: "job-cache-b", Status: "completed", URL: "https://example.com/b", WorkspaceRoot: root, DestDir: filepath.Join(root, "media"), CreatedAt: "2026-10-11T00:00:00Z"}
	if err := saveJob(&second); err != nil {
		t.Fatal(err)
	}
	result, err = listJobRecords(JobListInput{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 2 {
		t.Fatalf("cache did not invalidate after save: %#v", result)
	}
}

func TestReadJobLogTailIsBounded(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", root)
	dir, err := jobDir("job-log")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "job.log")
	if err := os.WriteFile(logPath, []byte("first line\nsecond line\nthird line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	record := JobRecord{ID: "job-log", Status: "failed", URL: "https://example.com/video", WorkspaceRoot: root, DestDir: filepath.Join(root, "media"), CreatedAt: "2026-10-12T00:00:00Z", LogPath: logPath}
	if err := saveJob(&record); err != nil {
		t.Fatal(err)
	}
	result, err := readJobLogTail("job-log", 14)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Truncated {
		t.Fatal("expected tail to be marked truncated")
	}
	if strings.Contains(result.Tail, "first line") || !strings.Contains(result.Tail, "third line") {
		t.Fatalf("unexpected log tail: %q", result.Tail)
	}
}

func TestWorkerInputCarriesDisplayMetadata(t *testing.T) {
	record := JobRecord{
		ID:              "job-meta",
		URL:             "https://example.com/video",
		Title:           "示例视频",
		Thumbnail:       "https://example.com/cover.jpg",
		Extractor:       "demo",
		DurationSeconds: 42,
		WorkspaceRoot:   `C:\workspace`,
		DestDir:         `C:\workspace\media`,
		Quality:         "720p",
	}
	input := workerInput(record)
	if input.Title != record.Title || input.Thumbnail != record.Thumbnail || input.Extractor != record.Extractor || input.DurationSeconds != record.DurationSeconds {
		t.Fatalf("display metadata was not carried: %#v", input)
	}
}
