package main

import (
	"database/sql"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

func writeNoisePNG(t *testing.T, path string, seed int64) {
	t.Helper()
	random := rand.New(rand.NewSource(seed))
	img := image.NewRGBA(image.Rect(0, 0, 96, 72))
	for y := 0; y < 72; y++ {
		for x := 0; x < 96; x++ {
			img.Set(x, y, color.RGBA{
				R: uint8(random.Intn(256)),
				G: uint8(random.Intn(256)),
				B: uint8(random.Intn(256)),
				A: 255,
			})
		}
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, img); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func selectionItemIDs(t *testing.T, selectionID string) []string {
	t.Helper()
	db, err := openLibraryDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT asset_id FROM selection_items WHERE selection_id=? ORDER BY asset_id`, selectionID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

func TestSelectionAutoAndPromote(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	sourceRoot := t.TempDir()
	for index := 0; index < 5; index++ {
		writeNoisePNG(t, filepath.Join(sourceRoot, string(rune('a'+index))+".png"), int64(index+1))
	}
	created, rpcError := call(t, "media-library.source.add", map[string]any{"path": sourceRoot, "display_name": "精选测试"})
	if rpcError != nil {
		t.Fatalf("add source: %s", rpcError.Message)
	}
	sourceID := decode(t, created)["id"].(string)
	if _, rpcError := call(t, "media-library.source.scan.batch", map[string]any{
		"source_id": sourceID, "restart": true, "max_entries": 100,
	}); rpcError != nil {
		t.Fatalf("scan: %s", rpcError.Message)
	}
	db, err := openLibraryDB()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT id FROM candidates WHERE source_id=? ORDER BY path`, sourceID)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			db.Close()
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	db.Close()
	if len(ids) != 5 {
		t.Fatalf("expected 5 candidates, got %d", len(ids))
	}

	targetRoot := filepath.Join(t.TempDir(), "精选")
	createdSelection, rpcError := call(t, "media-library.selection.create", map[string]any{
		"name": "测试精选", "asset_ids": ids, "target_count": 2,
		"target_path": targetRoot, "promote_mode": "hardlink",
	})
	if rpcError != nil {
		t.Fatalf("create selection: %s", rpcError.Message)
	}
	selectionID := decode(t, createdSelection)["selection"].(map[string]any)["id"].(string)
	autoResult, rpcError := call(t, "media-library.selection.auto", map[string]any{"selection_id": selectionID})
	if rpcError != nil {
		t.Fatalf("auto: %s", rpcError.Message)
	}
	autoPayload := decode(t, autoResult)
	items := autoPayload["items"].([]any)
	included := 0
	includedID := ""
	for _, entry := range items {
		item := entry.(map[string]any)
		if item["state"] == "included" {
			included++
			if includedID == "" {
				includedID = item["asset_id"].(string)
			}
		}
	}
	if included != 2 {
		t.Fatalf("expected 2 included, got %d", included)
	}
	if includedID == "" {
		t.Fatal("expected included asset")
	}

	decided, rpcError := call(t, "media-library.selection.decide", map[string]any{
		"selection_id": selectionID, "asset_id": includedID, "state": "excluded",
	})
	if rpcError != nil {
		t.Fatalf("decide: %s", rpcError.Message)
	}
	remaining := 0
	for _, entry := range decode(t, decided)["items"].([]any) {
		if entry.(map[string]any)["state"] == "included" {
			remaining++
		}
	}
	if remaining != 1 {
		t.Fatalf("expected 1 included after decision, got %d", remaining)
	}
	// 放回入选，验证沉淀。
	if _, rpcError := call(t, "media-library.selection.decide", map[string]any{
		"selection_id": selectionID, "asset_id": includedID, "state": "included",
	}); rpcError != nil {
		t.Fatalf("decide back: %s", rpcError.Message)
	}

	promoted, rpcError := call(t, "media-library.selection.promote", map[string]any{"selection_id": selectionID})
	if rpcError != nil {
		t.Fatalf("promote: %s", rpcError.Message)
	}
	promotePayload := decode(t, promoted)
	saved := promotePayload["saved"].([]any)
	if len(saved) != 2 {
		t.Fatalf("expected 2 saved, got %d: %#v", len(saved), promotePayload)
	}
	for _, entry := range saved {
		path := entry.(map[string]any)["path"].(string)
		if info, err := os.Stat(path); err != nil || info.Size() == 0 {
			t.Fatalf("promoted file missing: %s", path)
		}
	}
	if _, err := os.Stat(filepath.Join(targetRoot, ".himind-media", "curation.jsonl")); err != nil {
		t.Fatalf("curation log missing: %v", err)
	}

	// 再次沉淀必须幂等。
	again, rpcError := call(t, "media-library.selection.promote", map[string]any{"selection_id": selectionID})
	if rpcError != nil {
		t.Fatalf("promote again: %s", rpcError.Message)
	}
	againPayload := decode(t, again)
	if len(againPayload["saved"].([]any)) != 0 || len(againPayload["skipped"].([]any)) != 2 {
		t.Fatalf("expected idempotent promote, got %#v", againPayload)
	}

	// 数据库里必须有精选条目与位置记录。
	check, err := openLibraryDB()
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	var curated, locations int
	if err := check.QueryRow(`SELECT COUNT(*) FROM curated_entries WHERE selection_id=?`, selectionID).Scan(&curated); err != nil {
		t.Fatal(err)
	}
	if err := check.QueryRow(`SELECT COUNT(*) FROM asset_locations WHERE role='curated'`).Scan(&locations); err != nil {
		t.Fatal(err)
	}
	if curated != 2 || locations < 2 {
		t.Fatalf("curated=%d locations=%d", curated, locations)
	}
	var _ = sql.ErrNoRows
}
