package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func acquisitionTestLibrary(t *testing.T) (*sql.DB, *libraryRecord) {
	t.Helper()
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	value, err := createLibrary(createLibraryInput{Name: "采集测试库", BackendType: "local", RootPath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	library := value.(*libraryRecord)
	db, err := openLibraryDB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, library
}

func acquisitionTestTask(t *testing.T, db *sql.DB, libraryID, key, url string) (acquisitionRecord, acquisitionFile) {
	t.Helper()
	input := acquisitionFixture()
	input.Mode = "import"
	input.LibraryID = libraryID
	input.IdempotencyKey = key
	input.URL = url
	record, _, err := createAcquisition(db, input)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := pluginDataRoot()
	path := filepath.Join(root, "acquisition-staging", record.ID, "shared.mp4")
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("fixture media payload"), 0600); err != nil {
		t.Fatal(err)
	}
	digest, err := hashFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return record, acquisitionFile{Path: path, SHA256: digest, Bytes: 21}
}

func TestAcquisitionImportRetryUsesDownloadedFilesAndRestoresMissingAsset(t *testing.T) {
	db, library := acquisitionTestLibrary(t)
	record, file := acquisitionTestTask(t, db, library.ID, "resume-request-1", "https://www.youtube.com/watch?v=resume")
	files, _ := json.Marshal([]acquisitionFile{file})
	if _, err := db.Exec(`UPDATE acquisition_jobs SET status='pending_import',phase='importing',files_json=? WHERE id=?`, string(files), record.ID); err != nil {
		t.Fatal(err)
	}
	// A persisted file manifest must skip network/runtime resolution entirely.
	if _, err := retryAcquisition(db, record.ID); err != nil {
		t.Fatal(err)
	}
	if err := runAcquisitionWorker(record.ID); err != nil {
		t.Fatal(err)
	}
	completed, err := loadAcquisition(db, record.ID)
	if err != nil || completed.Status != "completed" || len(completed.Files) != 1 || completed.Files[0].LibraryItemID == "" {
		t.Fatalf("resumed: %+v %v", completed, err)
	}
	if _, err = os.Stat(file.Path); !os.IsNotExist(err) {
		t.Fatal("completed staging copy retained")
	}
	itemID, target := completed.Files[0].LibraryItemID, completed.Files[0].Path
	if err = os.Remove(target); err != nil {
		t.Fatal(err)
	}
	repair, repairFile := acquisitionTestTask(t, db, library.ID, "repair-request-2", "https://www.douyin.com/video/123456")
	if claimed, err := claimAcquisition(db, repair.ID, "repair", time.Now()); err != nil || !claimed {
		t.Fatal("claim", err)
	}
	restoredID, restoredPath, err := importAcquisitionFile(context.Background(), db, repair, "repair", repairFile)
	if err != nil || restoredID != itemID || restoredPath != target {
		t.Fatalf("missing file recovery: %s %s %v", restoredID, restoredPath, err)
	}
	if digest, err := hashFile(target); err != nil || digest != file.SHA256 {
		t.Fatal("asset not restored", err)
	}
	var assets, origins int
	_ = db.QueryRow(`SELECT COUNT(*) FROM library_items`).Scan(&assets)
	_ = db.QueryRow(`SELECT COUNT(*) FROM asset_origins WHERE library_item_id=?`, itemID).Scan(&origins)
	if assets != 1 || origins != 2 {
		t.Fatalf("dedup lost sources: assets=%d origins=%d", assets, origins)
	}
}

func TestAcquisitionConcurrentImportsDeduplicateAndOutboxReplays(t *testing.T) {
	db, library := acquisitionTestLibrary(t)
	db2, err := openLibraryDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	a, fa := acquisitionTestTask(t, db, library.ID, "concurrent-request-a", "https://www.youtube.com/watch?v=concurrent")
	b, fb := acquisitionTestTask(t, db, library.ID, "concurrent-request-b", "https://www.douyin.com/video/999123")
	for _, record := range []acquisitionRecord{a, b} {
		if ok, err := claimAcquisition(db, record.ID, record.ID, time.Now()); err != nil || !ok {
			t.Fatal("claim", err)
		}
	}
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	for _, work := range []struct {
		db     *sql.DB
		record acquisitionRecord
		file   acquisitionFile
	}{{db, a, fa}, {db2, b, fb}} {
		wg.Add(1)
		go func(work struct {
			db     *sql.DB
			record acquisitionRecord
			file   acquisitionFile
		}) {
			defer wg.Done()
			_, _, err := importAcquisitionFile(context.Background(), work.db, work.record, work.record.ID, work.file)
			errors <- err
		}(work)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var assets, origins, pending int
	_ = db.QueryRow(`SELECT COUNT(*) FROM library_items`).Scan(&assets)
	_ = db.QueryRow(`SELECT COUNT(*) FROM asset_origins`).Scan(&origins)
	_ = db.QueryRow(`SELECT COUNT(*) FROM acquisition_outbox WHERE delivered=0`).Scan(&pending)
	if assets != 1 || origins != 2 || pending != 2 {
		t.Fatalf("assets=%d origins=%d pending=%d", assets, origins, pending)
	}
	if err = flushAcquisitionOutbox(db); err != nil {
		t.Fatal(err)
	}
	// Simulate crash after publishing the event but before committing delivered.
	if _, err = db.Exec(`UPDATE acquisition_outbox SET delivered=0`); err != nil {
		t.Fatal(err)
	}
	if err = flushAcquisitionOutbox(db); err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob(filepath.Join(library.RootURI, ".himind-media", "catalog", "events", "*.json"))
	if err != nil || len(paths) != 2 {
		t.Fatalf("catalog replay duplicated: %v %v", paths, err)
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil || !json.Valid(data) {
			t.Fatal("partial event published", err)
		}
	}
}

func TestAcquisitionRejectsCancelledAndExpiredOwnerBeforeImport(t *testing.T) {
	db, library := acquisitionTestLibrary(t)
	for _, scenario := range []string{"cancelled", "expired"} {
		record, file := acquisitionTestTask(t, db, library.ID, "fence-request-"+scenario, "https://www.youtube.com/watch?v="+scenario)
		if ok, err := claimAcquisition(db, record.ID, "owner", time.Now()); err != nil || !ok {
			t.Fatal("claim", err)
		}
		if scenario == "cancelled" {
			if _, err := cancelAcquisition(db, record.ID); err != nil {
				t.Fatal(err)
			}
		} else {
			if _, err := db.Exec(`UPDATE acquisition_jobs SET lease_until=? WHERE id=?`, time.Now().Add(-time.Second).Unix(), record.ID); err != nil {
				t.Fatal(err)
			}
		}
		if _, _, err := importAcquisitionFile(context.Background(), db, record, "owner", file); err == nil {
			t.Fatal("stale/cancelled worker committed")
		}
	}
	var count int
	_ = db.QueryRow(`SELECT COUNT(*) FROM library_items`).Scan(&count)
	if count != 0 {
		t.Fatal("fenced job created formal assets")
	}
}

func TestAcquisitionCopyNeverOverwritesAndCatalogConflictIsPreserved(t *testing.T) {
	root := t.TempDir()
	source, target := filepath.Join(root, "source.mp4"), filepath.Join(root, "target.mp4")
	_ = os.WriteFile(source, []byte("new"), 0600)
	_ = os.WriteFile(target, []byte("existing"), 0600)
	digest, _ := hashFile(source)
	if err := commitAcquisitionCopy(context.Background(), source, target, digest); err == nil {
		t.Fatal("overwrote different target")
	}
	if data, _ := os.ReadFile(target); string(data) != "existing" {
		t.Fatal("target changed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := commitAcquisitionCopy(ctx, source, filepath.Join(root, "cancelled.mp4"), digest); err == nil {
		t.Fatal("cancelled copy succeeded")
	}
	event := filepath.Join(root, "events", "event.json")
	if err := publishAcquisitionEvent(event, `{"event_id":"a"}`); err != nil {
		t.Fatal(err)
	}
	if err := publishAcquisitionEvent(event, `{"event_id":"b"}`); err == nil {
		t.Fatal("catalog event overwritten")
	}
	if data, _ := os.ReadFile(event); string(data) != "{\"event_id\":\"a\"}\n" {
		t.Fatal("catalog conflict modified original")
	}
}

func TestAcquisitionRecoversAbandonedLaunchAndExpiredCancellation(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	db, err := openLibraryDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	queued, _, err := createAcquisition(db, acquisitionFixture())
	if err != nil {
		t.Fatal(err)
	}
	input := acquisitionFixture()
	input.IdempotencyKey = "cancel-request-2"
	cancelled, _, err := createAcquisition(db, input)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err = db.Exec(`UPDATE acquisition_jobs SET updated_at=? WHERE id=?`, now.Add(-2*time.Minute).UTC().Format(time.RFC3339), queued.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE acquisition_jobs SET status='canceling',cancel_requested=1,lease_owner='dead',lease_until=? WHERE id=?`, now.Add(-time.Second).Unix(), cancelled.ID); err != nil {
		t.Fatal(err)
	}
	if err = recoverAcquisitions(db, now); err != nil {
		t.Fatal(err)
	}
	q, _ := loadAcquisition(db, queued.ID)
	c, _ := loadAcquisition(db, cancelled.ID)
	if q.Status != "interrupted" || c.Status != "cancelled" || c.LeaseOwner != "" {
		t.Fatalf("launch recovery: %+v %+v", q, c)
	}
}
