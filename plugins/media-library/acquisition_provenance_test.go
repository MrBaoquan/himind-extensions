package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAcquisitionSubtitlePersistsParentAndCatalogReadsBothFormats(t *testing.T) {
	db, library := acquisitionTestLibrary(t)
	record, media := acquisitionTestTask(t, db, library.ID, "subtitle-request-1", "https://www.youtube.com/watch?v=subtitles")
	subtitlePath := filepath.Join(filepath.Dir(media.Path), "shared.en.vtt")
	payload := []byte("WEBVTT\n\n00:00.000 --> 00:01.000\nfixture\n")
	if err := os.WriteFile(subtitlePath, payload, 0600); err != nil {
		t.Fatal(err)
	}
	files, _ := json.Marshal([]acquisitionFile{{Path: subtitlePath}, media})
	_, err := db.Exec(`UPDATE acquisition_jobs SET status='pending_import',files_json=? WHERE id=?`, string(files), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = retryAcquisition(db, record.ID); err != nil {
		t.Fatal(err)
	}
	if err = runAcquisitionWorker(record.ID); err != nil {
		t.Fatal(err)
	}
	completed, err := loadAcquisition(db, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(completed.Files) != 2 || completed.Files[1].ParentItemID != completed.Files[0].LibraryItemID || completed.Files[1].Language != "en" {
		t.Fatalf("subtitle association: %+v", completed.Files)
	}
	result, err := acquisitionProvenance(completed.Files[0].LibraryItemID)
	if err != nil {
		t.Fatal(err)
	}
	provenance := result.(map[string]any)
	if len(provenance["origins"].([]map[string]any)) != 1 || len(provenance["attachments"].([]map[string]any)) != 1 {
		t.Fatal("provenance incomplete", result)
	}
	paths, err := filepath.Glob(filepath.Join(library.RootURI, ".himind-media", "catalog", "events", "*.json"))
	if err != nil || len(paths) != 2 {
		t.Fatal("missing catalog events", err)
	}
	duplicate, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(library.RootURI, ".himind-media", "catalog", "users", "legacy.jsonl"), duplicate, 0600); err != nil {
		t.Fatal(err)
	}
	count := 0
	if err = walkLibraryCatalog(context.Background(), library, func(catalogEvent) error { count++; return nil }); err != nil || count != 2 {
		t.Fatalf("combined catalog: count=%d err=%v", count, err)
	}
	var conflict catalogEvent
	if err = json.Unmarshal(duplicate, &conflict); err != nil {
		t.Fatal(err)
	}
	conflict.Title = "conflicting title"
	changed, _ := json.Marshal(conflict)
	if err = os.WriteFile(filepath.Join(library.RootURI, ".himind-media", "catalog", "users", "conflict.jsonl"), changed, 0600); err != nil {
		t.Fatal(err)
	}
	if err = walkLibraryCatalog(context.Background(), library, func(catalogEvent) error { return nil }); err == nil {
		t.Fatal("conflicting catalog event hidden")
	}
}
