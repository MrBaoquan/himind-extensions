package main

import (
	"path/filepath"
	"testing"
)

func TestCuratedLibraryEndToEnd(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	sourceRoot := t.TempDir()
	writeTestPNG(t, filepath.Join(sourceRoot, "curated-shot.png"))
	created, rpcError := call(t, "media-library.source.add", map[string]any{"path": sourceRoot})
	if rpcError != nil {
		t.Fatalf("add source: %s", rpcError.Message)
	}
	sourceID := decode(t, created)["id"].(string)
	if _, rpcError := call(t, "media-library.source.scan.batch", map[string]any{
		"source_id": sourceID, "restart": true, "max_entries": 100,
	}); rpcError != nil {
		t.Fatalf("scan: %s", rpcError.Message)
	}
	assetID := candidateIDByPath(t, filepath.Join(sourceRoot, "curated-shot.png"))
	if _, rpcError := call(t, "media-library.asset.enrich", map[string]any{
		"asset_ids": []string{assetID}, "limit": 1, "thumbnails": false,
	}); rpcError != nil {
		t.Fatalf("enrich: %s", rpcError.Message)
	}

	libraryRoot := filepath.Join(t.TempDir(), "精选")
	library, rpcError := call(t, "media-library.repository.create", map[string]any{
		"name": "测试精选库", "backend_type": "local", "root_path": libraryRoot,
		"access_mode": "contribute", "kind": "curated",
	})
	if rpcError != nil {
		t.Fatalf("create curated library: %s", rpcError.Message)
	}
	libraryID := decode(t, library)["id"].(string)
	if decode(t, library)["kind"] != "curated" {
		t.Fatalf("expected curated kind: %#v", decode(t, library))
	}

	selection, rpcError := call(t, "media-library.selection.create", map[string]any{
		"name": "测试精选", "asset_ids": []string{assetID}, "target_count": 1,
		"library_id": libraryID, "promote_mode": "copy",
	})
	if rpcError != nil {
		t.Fatalf("create selection: %s", rpcError.Message)
	}
	selectionID := decode(t, selection)["selection"].(map[string]any)["id"].(string)
	if decode(t, selection)["selection"].(map[string]any)["library_id"] != libraryID {
		t.Fatalf("selection should remember library: %#v", decode(t, selection)["selection"])
	}

	batch, rpcError := call(t, "media-library.selection.decide.batch", map[string]any{
		"selection_id": selectionID,
		"decisions":    []map[string]any{{"asset_id": assetID, "state": "included"}},
	})
	if rpcError != nil {
		t.Fatalf("batch decide: %s", rpcError.Message)
	}
	if decode(t, batch)["updated"].(float64) != 1 {
		t.Fatalf("expected one updated decision: %#v", decode(t, batch))
	}

	promoted, rpcError := call(t, "media-library.selection.promote", map[string]any{
		"selection_id": selectionID, "mode": "copy",
	})
	if rpcError != nil {
		t.Fatalf("promote: %s", rpcError.Message)
	}
	payload := decode(t, promoted)
	if payload["library_id"] != libraryID || payload["library_kind"] != "curated" {
		t.Fatalf("promote should return curated library: %#v", payload)
	}
	if len(payload["saved"].([]any)) != 1 {
		t.Fatalf("expected one saved file: %#v", payload["saved"])
	}
	if payload["registered"].(float64) != 1 || payload["library_items"].(float64) != 1 {
		t.Fatalf("expected one library item: %#v", payload)
	}

	// 精选库条目必须能被检索到，并带 curated 标记。
	results, rpcError := call(t, "media-library.asset.search", map[string]any{
		"query": "curated-shot", "scope": "library", "limit": 20,
	})
	if rpcError != nil {
		t.Fatalf("search: %s", rpcError.Message)
	}
	items := decode(t, results)["items"].([]any)
	if len(items) == 0 {
		t.Fatalf("expected curated item in library scope: %#v", decode(t, results))
	}
	first := items[0].(map[string]any)
	if first["library_kind"] != "curated" {
		t.Fatalf("expected curated badge on result: %#v", first)
	}
	if numberField(first, "width") == 0 || numberField(first, "height") == 0 {
		t.Fatalf("expected probe metadata backfilled: %#v", first)
	}

	// 「全部」里同一内容只保留一条，并标记本机也有。
	all, rpcError := call(t, "media-library.asset.search", map[string]any{
		"query": "curated-shot", "scope": "all", "limit": 20,
	})
	if rpcError != nil {
		t.Fatalf("search all: %s", rpcError.Message)
	}
	allItems := decode(t, all)["items"].([]any)

	if len(allItems) != 1 {
		t.Fatalf("expected deduplicated single item, got %d: %#v", len(allItems), allItems)
	}
	if allItems[0].(map[string]any)["also_local"] != true {
		t.Fatalf("expected also_local flag: %#v", allItems[0])
	}

	summary, rpcError := call(t, "media-library.selection.summary", map[string]any{})
	if rpcError != nil {
		t.Fatalf("summary: %s", rpcError.Message)
	}
	entry := decode(t, summary)["items"].([]any)[0].(map[string]any)
	if entry["status"] != "done" || entry["library_items"].(float64) != 1 {
		t.Fatalf("unexpected summary: %#v", entry)
	}

	// 库详情：能按库列出条目，并带回缩略图所需的 candidate_id 与探针元数据。
	libraryItems, rpcError := call(t, "media-library.holdings.items", map[string]any{
		"library_id": libraryID, "limit": 20, "offset": 0,
	})
	if rpcError != nil {
		t.Fatalf("library items: %s", rpcError.Message)
	}
	listed := decode(t, libraryItems)["items"].([]any)
	if len(listed) != 1 {
		t.Fatalf("expected one library item: %#v", listed)
	}
	listedItem := listed[0].(map[string]any)
	if listedItem["candidate_id"] != assetID {
		t.Fatalf("expected candidate id for thumbnails: %#v", listedItem)
	}
	if numberField(listedItem, "width") == 0 || numberField(listedItem, "duration_ms") < 0 {
		t.Fatalf("expected probe metadata in library items: %#v", listedItem)
	}
}

func numberField(payload map[string]any, key string) float64 {
	if value, ok := payload[key].(float64); ok {
		return value
	}
	return 0
}
