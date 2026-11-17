package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyImportIsIdempotentAndNeverAdoptsRunningOwnerOrTouchesFiles(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	file := filepath.Join(t.TempDir(), "existing.mp4")
	if err := os.WriteFile(file, []byte("original-media"), 0600); err != nil {
		t.Fatal(err)
	}
	record := legacyDownload{JobID: "legacy-1", Status: "completed", Title: "旧记录", URL: "https://www.youtube.com/watch?v=fixture", Outputs: []string{file}, Owner: "untrusted"}
	active := record
	active.JobID = "active-1"
	active.Status = "running"
	one, err := importLegacyDownloads([]legacyDownload{record, active})
	if err != nil {
		t.Fatal(err)
	}
	if one.(map[string]any)["imported"] != 1 || one.(map[string]any)["skipped"] != 1 {
		t.Fatal(one)
	}
	two, err := importLegacyDownloads([]legacyDownload{record})
	if err != nil {
		t.Fatal(err)
	}
	if two.(map[string]any)["imported"] != 0 {
		t.Fatal("duplicate history", two)
	}
	listed, err := listLegacyDownloads()
	if err != nil {
		t.Fatal(err)
	}
	items := listed.(map[string]any)["items"].([]legacyDownload)
	if len(items) != 1 || items[0].Owner != "com.himind.media-downloader" || items[0].Outputs[0] != file {
		t.Fatalf("ownership/path changed: %+v", items)
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "original-media" {
		t.Fatal("source changed", err)
	}
	db, err := openLibraryDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM acquisition_jobs`).Scan(&count); err != nil || count != 0 {
		t.Fatal("import created worker task", count, err)
	}
}

func TestLegacyImportRejectsInvalidBatchAtomicallyAndSkipsSignedURLs(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	good := legacyDownload{JobID: "good", Status: "completed", URL: "https://www.douyin.com/video/123456"}
	bad := good
	bad.JobID = "bad"
	bad.Outputs = []string{"relative.mp4"}
	if _, err := importLegacyDownloads([]legacyDownload{good, bad}); err == nil {
		t.Fatal("unsafe batch accepted")
	}
	listed, err := listLegacyDownloads()
	if err != nil {
		t.Fatal(err)
	}
	if listed.(map[string]any)["count"] != 0 {
		t.Fatal("partial migration committed")
	}
	good.URL = "https://example.com/video?signature=fixture-secret"
	result, err := importLegacyDownloads([]legacyDownload{good})
	if err != nil || result.(map[string]any)["skipped"] != 1 {
		t.Fatal("signed source retained", result, err)
	}
}
