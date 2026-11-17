package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MrBaoquan/himind-extensions/internal/mediadownload"
)

func acquisitionFixture() acquisitionStartInput {
	return acquisitionStartInput{JobStartInput: mediadownload.JobStartInput{URL: "https://www.youtube.com/watch?v=fixture", Title: "fixture"}, Mode: "save", IdempotencyKey: "fixture-request-1"}
}

func TestAcquisitionIdempotencyAndTargetConflict(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	db, err := openLibraryDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	input := acquisitionFixture()
	first, created, err := createAcquisition(db, input)
	if err != nil || !created {
		t.Fatalf("create: %v %v", created, err)
	}
	second, created, err := createAcquisition(db, input)
	if err != nil || created || second.ID != first.ID {
		t.Fatalf("idempotency: %+v %v %v", second, created, err)
	}
	input.DestDir = "different-target"
	if _, _, err = createAcquisition(db, input); err == nil {
		t.Fatal("changed intent reused request key")
	}
}

func TestAcquisitionLeaseFencesStaleWorkerAcrossConnections(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	db, err := openLibraryDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db2, err := openLibraryDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	record, _, err := createAcquisition(db, acquisitionFixture())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	if ok, err := claimAcquisition(db, record.ID, "worker-a", now); err != nil || !ok {
		t.Fatalf("first lease: %v %v", ok, err)
	}
	if ok, err := claimAcquisition(db2, record.ID, "worker-b", now); err != nil || ok {
		t.Fatalf("duplicate lease: %v %v", ok, err)
	}
	if ok, err := claimAcquisition(db2, record.ID, "worker-b", now.Add(31*time.Second)); err != nil || !ok {
		t.Fatalf("expired lease: %v %v", ok, err)
	}
	if ok, err := heartbeatAcquisition(db, record.ID, "worker-a", now.Add(32*time.Second)); err != nil || ok {
		t.Fatalf("stale owner renewed lease: %v %v", ok, err)
	}
}

func TestAcquisitionRetryKeepsVerifiedFilesAndCancelProtectsCompleted(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	db, err := openLibraryDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	record, _, err := createAcquisition(db, acquisitionFixture())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "kept.mp4")
	if err = os.WriteFile(path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	files, _ := json.Marshal([]acquisitionFile{{Path: path, Bytes: 7, SHA256: "fixture"}})
	if _, err = db.Exec(`UPDATE acquisition_jobs SET status='pending_import',phase='importing',files_json=? WHERE id=?`, string(files), record.ID); err != nil {
		t.Fatal(err)
	}
	retried, err := retryAcquisition(db, record.ID)
	if err != nil || len(retried.Files) != 1 || retried.Phase != "importing" {
		t.Fatalf("retry lost files: %+v %v", retried, err)
	}
	if _, err = db.Exec(`UPDATE acquisition_jobs SET status='completed' WHERE id=?`, record.ID); err != nil {
		t.Fatal(err)
	}
	cancelled, err := cancelAcquisition(db, record.ID)
	if err != nil || cancelled.Status != "completed" {
		t.Fatalf("late cancel: %+v %v", cancelled, err)
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("completed file was removed")
	}
}

func TestBookmarkCanonicalIdentityPersistsWithoutDownload(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	first, err := saveBookmark(remoteResource{SourceURL: "https://www.douyin.com/video/123456?utm_source=test", Title: "原始标题", Note: "待剪辑"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := saveBookmark(remoteResource{SourceURL: "https://www.douyin.com/?modal_id=123456", Title: "已更新", Tags: []string{"航拍"}})
	if err != nil {
		t.Fatal(err)
	}
	if first.(remoteResource).ID != second.(remoteResource).ID {
		t.Fatal("same work produced duplicate identity")
	}
	listed, err := listBookmarks("航拍")
	if err != nil {
		t.Fatal(err)
	}
	items := listed.(map[string]any)["items"].([]remoteResource)
	if len(items) != 1 || items[0].Title != "已更新" {
		t.Fatalf("persistence: %+v", items)
	}
	db, err := openLibraryDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM acquisition_jobs`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("bookmark started acquisition: %d %v", count, err)
	}
	if _, err = saveBookmark(remoteResource{SourceURL: "https://example.com/video?signature=fixture-secret"}); err == nil || strings.Contains(err.Error(), "fixture-secret") {
		t.Fatal("signed source must be rejected without exposing its value")
	}
}

func TestAcquisitionSessionBindingIsDurableAndExplicitlyRebound(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	db, err := openLibraryDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	in := acquisitionFixture()
	in.AccountGeneration = "original-session"
	in.Thumbnail = "https://cdn.example.com/a?signature=fixture-secret"
	record, _, err := createAcquisition(db, in)
	if err != nil {
		t.Fatal(err)
	}
	if record.AccountGeneration != "original-session" || record.Input.Thumbnail != "" {
		t.Fatal("session/thumbnail boundary incorrect")
	}
	var inputJSON string
	_ = db.QueryRow(`SELECT input_json FROM acquisition_jobs WHERE id=?`, record.ID).Scan(&inputJSON)
	if strings.Contains(inputJSON, "fixture-secret") || strings.Contains(inputJSON, "original-session") {
		t.Fatal("ephemeral metadata entered persisted intent")
	}
	_, _ = db.Exec(`UPDATE acquisition_jobs SET status='waiting_for_login' WHERE id=?`, record.ID)
	retried, err := retryAcquisition(db, record.ID)
	if err != nil || retried.AccountGeneration != "original-session" {
		t.Fatal("implicit rebind", err)
	}
	_, _ = db.Exec(`UPDATE acquisition_jobs SET status='waiting_for_login' WHERE id=?`, record.ID)
	retried, err = retryAcquisitionAccount(db, record.ID, "confirmed-current-session")
	if err != nil || retried.AccountGeneration != "confirmed-current-session" {
		t.Fatal("explicit rebind failed", err)
	}
}

func TestBookmarksDoNotPersistSignedPreviewURLs(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	_, err := saveBookmark(remoteResource{SourceURL: "https://www.douyin.com/video/123456", PreviewURL: "https://cdn.example.com/cover?signature=fixture-secret"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := listBookmarks("")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "signature") || strings.Contains(string(encoded), "fixture-secret") {
		t.Fatal("signed thumbnail persisted")
	}
}
