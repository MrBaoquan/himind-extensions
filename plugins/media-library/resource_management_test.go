package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func containsAll(value string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}

func TestLibraryCanBeUpdatedAndRemovedWithoutDeletingFiles(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	root := t.TempDir()
	created, rpcError := call(t, "media-library.repository.create", map[string]any{
		"name": "待编辑资源库", "backend_type": "local", "root_path": root, "access_mode": "contribute",
	})
	if rpcError != nil {
		t.Fatalf("create library: %s", rpcError.Message)
	}
	libraryID := decode(t, created)["id"].(string)
	assetPath := filepath.Join(root, "assets", "managed.png")
	if err := os.MkdirAll(filepath.Dir(assetPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(assetPath, []byte("managed"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := openLibraryDB()
	if err != nil {
		t.Fatal(err)
	}
	tx, _ := db.Begin()
	if _, err := tx.Exec(`INSERT INTO library_items(id,library_id,sha256,relative_path,local_path,kind,title,adopted_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		"managed-item", libraryID, "managed-sha", "assets/managed.png", assetPath, "image", "managed.png", "2026-10-28T00:00:00Z", "2026-10-28T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := replaceSearchDocument(tx, searchDocument{EntryType: "library", EntryID: "managed-item", LibraryID: libraryID, Kind: "image", UpdatedAt: "2026-10-28T00:00:00Z", Title: "managed.png", Filename: "managed.png", Path: "assets/managed.png"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	db.Close()

	updated, rpcError := call(t, "media-library.repository.update", map[string]any{"id": libraryID, "name": "已编辑资源库", "access_mode": "read_only"})
	if rpcError != nil {
		t.Fatalf("update library: %s", rpcError.Message)
	}
	payload := decode(t, updated)
	if payload["name"] != "已编辑资源库" || payload["access_mode"] != "read_only" {
		t.Fatalf("library update not applied: %#v", payload)
	}
	manifest, err := os.ReadFile(filepath.Join(root, ".himind-media", "library.json"))
	if err != nil || !containsAll(string(manifest), "已编辑资源库", "read_only") {
		t.Fatalf("library manifest not updated: %s %v", manifest, err)
	}
	removed, rpcError := call(t, "media-library.repository.remove", map[string]any{"id": libraryID})
	if rpcError != nil {
		t.Fatalf("remove library: %s", rpcError.Message)
	}
	if decode(t, removed)["disk_files_preserved"] != true {
		t.Fatal("remove library must preserve disk files")
	}
	if _, err := os.Stat(assetPath); err != nil {
		t.Fatalf("disk asset deleted: %v", err)
	}
	db, _ = openLibraryDB()
	defer db.Close()
	var libraries, items, fts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM libraries WHERE id=?`, libraryID).Scan(&libraries); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM library_items WHERE library_id=?`, libraryID).Scan(&items); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM search_fts_v2 WHERE entry_type='library' AND library_id=?`, libraryID).Scan(&fts); err != nil {
		t.Fatal(err)
	}
	if libraries != 0 || items != 0 || fts != 0 {
		t.Fatalf("library index not removed: libraries=%d items=%d fts=%d", libraries, items, fts)
	}
}

func TestSourceCanBeRenamedAndRemovedWithoutDeletingFiles(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	sourceDir := t.TempDir()
	mediaPath := filepath.Join(sourceDir, "city.png")
	if err := os.WriteFile(mediaPath, []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}
	added, rpcError := call(t, "media-library.source.add", map[string]any{"path": sourceDir})
	if rpcError != nil {
		t.Fatalf("add source: %s", rpcError.Message)
	}
	sourceID := decode(t, added)["id"].(string)
	if _, rpcError := call(t, "media-library.source.scan.batch", map[string]any{"source_id": sourceID, "restart": true, "max_entries": 20}); rpcError != nil {
		t.Fatalf("scan source: %s", rpcError.Message)
	}
	updated, rpcError := call(t, "media-library.source.update", map[string]any{"id": sourceID, "display_name": "已重命名来源"})
	if rpcError != nil {
		t.Fatalf("update source: %s", rpcError.Message)
	}
	if decode(t, updated)["display_name"] != "已重命名来源" {
		t.Fatalf("source rename not applied: %#v", decode(t, updated))
	}
	removed, rpcError := call(t, "media-library.source.remove", map[string]any{"id": sourceID})
	if rpcError != nil {
		t.Fatalf("remove source: %s", rpcError.Message)
	}
	if decode(t, removed)["disk_files_preserved"] != true {
		t.Fatal("remove source must preserve disk files")
	}
	if _, err := os.Stat(mediaPath); err != nil {
		t.Fatalf("source file deleted: %v", err)
	}
	db, _ := openLibraryDB()
	defer db.Close()
	var sources, candidates, fts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM source_roots WHERE id=?`, sourceID).Scan(&sources); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM candidates WHERE source_id=?`, sourceID).Scan(&candidates); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM search_fts_v2 WHERE entry_type='candidate' AND entry_id IN (SELECT id FROM candidates WHERE source_id=?)`, sourceID).Scan(&fts); err != nil {
		t.Fatal(err)
	}
	if sources != 0 || candidates != 0 || fts != 0 {
		t.Fatalf("source index not removed: sources=%d candidates=%d fts=%d", sources, candidates, fts)
	}
}
