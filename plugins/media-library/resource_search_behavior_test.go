package main

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

func TestSearchPrefersTitleAndTagMatches(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	db, err := openLibraryDB()
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	insert := func(id, title, path, tags, collection, updated string) {
		t.Helper()
		if _, err := tx.Exec(`INSERT INTO library_items(id,library_id,sha256,relative_path,local_path,kind,title,metadata_json,adopted_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			id, "rank-lib", id, path, filepath.Join(t.TempDir(), title), "image", title, fmt.Sprintf(`{"tags":[%q],"collection":%q}`, tags, collection), updated, updated); err != nil {
			t.Fatal(err)
		}
		if err := replaceSearchDocument(tx, searchDocument{EntryType: "library", EntryID: id, LibraryID: "rank-lib", Kind: "image", UpdatedAt: updated, Title: title, Filename: filepath.Base(path), Path: path, Tags: tags, Collection: collection}); err != nil {
			t.Fatal(err)
		}
	}
	insert("title-match", "城市夜景", "assets/aa/other.png", "其他", "杂项", "2026-10-28T00:00:03Z")
	insert("tag-match", "其他素材", "assets/aa/tag.png", "城市夜景", "杂项", "2026-10-28T00:00:02Z")
	insert("path-match", "另一个素材", "assets/city/城市夜景.png", "其他", "杂项", "2026-10-28T00:00:04Z")
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	db.Close()

	value, err := searchResources(resourceSearchInput{Query: "城市夜景", Scope: "library", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	items := value.(map[string]any)["items"].([]resourceSearchItem)
	if len(items) < 3 || items[0].Title != "城市夜景" {
		t.Fatalf("expected title match first, got %#v", items)
	}
}

func TestSearchFiltersBeforeLimitAndCursorPagination(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	db, err := openLibraryDB()
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 620; i++ {
		id := fmt.Sprintf("image-%04d", i)
		title := fmt.Sprintf("shared image %04d", i)
		if _, err := tx.Exec(`INSERT INTO library_items(id,library_id,sha256,relative_path,local_path,kind,title,adopted_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
			id, "filter-lib", id, fmt.Sprintf("assets/aa/%s.png", id), "missing", "image", title, fmt.Sprintf("2026-10-28T00:%02d:%02dZ", i/60%60, i%60), fmt.Sprintf("2026-10-28T00:%02d:%02dZ", i/60%60, i%60)); err != nil {
			t.Fatal(err)
		}
		if err := replaceSearchDocument(tx, searchDocument{EntryType: "library", EntryID: id, LibraryID: "filter-lib", Kind: "image", UpdatedAt: fmt.Sprintf("2026-10-28T00:%02d:%02dZ", i/60%60, i%60), Title: title, Filename: id + ".png", Path: fmt.Sprintf("assets/aa/%s.png", id)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO library_items(id,library_id,sha256,relative_path,local_path,kind,title,adopted_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		"audio-target", "filter-lib", "audio-target", "assets/bb/audio-target.mp3", "missing", "audio", "shared target", "2026-10-27T00:00:00Z", "2026-10-27T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := replaceSearchDocument(tx, searchDocument{EntryType: "library", EntryID: "audio-target", LibraryID: "filter-lib", Kind: "audio", UpdatedAt: "2026-10-27T00:00:00Z", Title: "shared target", Filename: "audio-target.mp3", Path: "assets/bb/audio-target.mp3"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	db.Close()

	filtered, err := searchResources(resourceSearchInput{Query: "shared", Scope: "library", LibraryID: "filter-lib", Kinds: []string{"audio"}, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	filteredItems := filtered.(map[string]any)["items"].([]resourceSearchItem)
	if len(filteredItems) != 1 || filteredItems[0].ID != "audio-target" {
		t.Fatalf("filter should apply before limit, got %#v", filteredItems)
	}

	first, err := searchResources(resourceSearchInput{Query: "shared", Scope: "library", LibraryID: "filter-lib", Kinds: []string{"image"}, Limit: 25})
	if err != nil {
		t.Fatal(err)
	}
	firstPayload := first.(map[string]any)
	firstItems := firstPayload["items"].([]resourceSearchItem)
	cursor, _ := firstPayload["next_cursor"].(string)
	if len(firstItems) != 25 || cursor == "" {
		t.Fatalf("expected first page and cursor, got %d cursor=%q", len(firstItems), cursor)
	}
	second, err := searchResources(resourceSearchInput{Query: "shared", Scope: "library", LibraryID: "filter-lib", Kinds: []string{"image"}, Limit: 25, Cursor: cursor})
	if err != nil {
		t.Fatal(err)
	}
	secondItems := second.(map[string]any)["items"].([]resourceSearchItem)
	if len(secondItems) != 25 {
		t.Fatalf("expected second page, got %d", len(secondItems))
	}
	seen := map[string]bool{}
	for _, item := range append(firstItems, secondItems...) {
		if seen[item.ID] {
			t.Fatalf("duplicate item across pages: %s", item.ID)
		}
		seen[item.ID] = true
	}
}

func TestShortChineseSearchFindsSubstring(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	db, err := openLibraryDB()
	if err != nil {
		t.Fatal(err)
	}
	tx, _ := db.Begin()
	if _, err := tx.Exec(`INSERT INTO library_items(id,library_id,sha256,relative_path,local_path,kind,title,adopted_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		"cn-item", "cn-lib", "cn-sha", "assets/aa/city.png", "missing", "image", "城市夜景", "2026-10-28T00:00:00Z", "2026-10-28T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := replaceSearchDocument(tx, searchDocument{EntryType: "library", EntryID: "cn-item", LibraryID: "cn-lib", Kind: "image", UpdatedAt: "2026-10-28T00:00:00Z", Title: "城市夜景", Filename: "city.png", Path: "assets/aa/city.png"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	db.Close()
	value, err := searchResources(resourceSearchInput{Query: "夜景", Scope: "library", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	items := value.(map[string]any)["items"].([]resourceSearchItem)
	if len(items) != 1 || items[0].ID != "cn-item" {
		t.Fatalf("short Chinese substring search failed: %#v", items)
	}
}

func TestSearchIndexMigrationBackfillsV2(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", root)
	dbPath := filepath.Join(root, "library.db")
	raw, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(libraryDBSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`DROP TABLE search_fts_v2`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`DELETE FROM schema_migrations WHERE name='search_fts_v2_v1'`); err != nil {
		t.Fatal(err)
	}
	now := "2026-10-28T00:00:00Z"
	if _, err := raw.Exec(`INSERT INTO candidates(id,source_id,path,relative_path,name,kind,extension,bytes,modified_at,seen_generation,missing,indexed_level,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		"legacy-candidate", "source", `C:\legacy\城市.png`, "城市/城市.png", "城市.png", "image", ".png", 10, now, "g", 0, 1, now, now); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	db, err := openLibraryDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM search_fts_v2 WHERE entry_type='candidate' AND entry_id='legacy-candidate'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected legacy candidate backfill, got %d", count)
	}
}
