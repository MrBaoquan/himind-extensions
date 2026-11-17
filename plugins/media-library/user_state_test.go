package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUserTagsAndRatingWithJSONLMode(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	root := t.TempDir()
	writeTestPNG(t, filepath.Join(root, "tagged.png"))
	created, rpcError := call(t, "media-library.source.add", map[string]any{
		"path": root, "display_name": "标记测试", "metadata_mode": "both",
	})
	if rpcError != nil {
		t.Fatalf("add source: %s", rpcError.Message)
	}
	sourceID := decode(t, created)["id"].(string)
	if _, rpcError := call(t, "media-library.source.scan.batch", map[string]any{
		"source_id": sourceID, "restart": true, "max_entries": 100,
	}); rpcError != nil {
		t.Fatalf("scan: %s", rpcError.Message)
	}
	id := candidateIDByPath(t, filepath.Join(root, "tagged.png"))

	tagged, rpcError := call(t, "media-library.asset.tags.set", map[string]any{
		"asset_id": id, "tags": []string{"夜景航拍", "精选"},
	})
	if rpcError != nil {
		t.Fatalf("tags: %s", rpcError.Message)
	}
	if len(decode(t, tagged)["tags"].([]any)) != 2 {
		t.Fatalf("tags not saved: %#v", decode(t, tagged))
	}
	if _, rpcError := call(t, "media-library.asset.rating.set", map[string]any{
		"asset_id": id, "rating": 4,
	}); rpcError != nil {
		t.Fatalf("rating: %s", rpcError.Message)
	}
	state, rpcError := call(t, "media-library.asset.user-state", map[string]any{"asset_id": id})
	if rpcError != nil {
		t.Fatalf("state: %s", rpcError.Message)
	}
	payload := decode(t, state)
	if payload["rating"].(float64) != 4 || payload["mode"] != "both" {
		t.Fatalf("unexpected state: %#v", payload)
	}
	if _, rpcError := call(t, "media-library.asset.note.set", map[string]any{
		"asset_id": id, "note": "航拍素材库 OCR 测试 2026",
	}); rpcError != nil {
		t.Fatalf("note: %s", rpcError.Message)
	}
	stateWithNote, rpcError := call(t, "media-library.asset.user-state", map[string]any{"asset_id": id})
	if rpcError != nil {
		t.Fatalf("state note: %s", rpcError.Message)
	}
	if decode(t, stateWithNote)["note"] != "航拍素材库 OCR 测试 2026" {
		t.Fatalf("note not saved: %#v", decode(t, stateWithNote))
	}

	// JSONL 追加写必须落在来源目录，便于随盘搬走。
	logPath := filepath.Join(root, ".himind-media", "user-data.jsonl")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("user-data.jsonl missing: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "tags.set") || !strings.Contains(text, "rating.set") {
		t.Fatalf("events missing: %s", text)
	}

	// 标签进入 FTS 后应能按标签命中。
	searched, rpcError := call(t, "media-library.asset.search", map[string]any{
		"query": "夜景航拍", "limit": 10,
	})
	if rpcError != nil {
		t.Fatalf("search: %s", rpcError.Message)
	}
	found := false
	for _, entry := range decode(t, searched)["items"].([]any) {
		if entry.(map[string]any)["id"] == id {
			found = true
		}
	}
	if !found {
		t.Fatalf("tag search did not find asset: %#v", decode(t, searched))
	}
}
