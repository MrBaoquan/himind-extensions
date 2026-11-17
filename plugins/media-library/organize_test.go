package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOrganizePlanApplyAndReview(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	sourceRoot := t.TempDir()
	for _, name := range []string{"auto-1.png", "auto-2.png", "auto-3.png"} {
		writeTestPNG(t, filepath.Join(sourceRoot, name))
	}
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
	libraryRoot := filepath.Join(t.TempDir(), "自动精选")
	library, rpcError := call(t, "media-library.repository.create", map[string]any{
		"name": "自动整理库", "backend_type": "local", "root_path": libraryRoot,
		"access_mode": "contribute", "kind": "curated",
	})
	if rpcError != nil {
		t.Fatalf("create library: %s", rpcError.Message)
	}
	libraryID := decode(t, library)["id"].(string)

	planned, rpcError := call(t, "media-library.organize.plan", map[string]any{
		"library_id": libraryID,
		"source_id":  sourceID,
		"policy": map[string]any{
			"auto_ingest": true, "quality_floor": 1, "similarity": 12,
			"per_cluster": 1, "mode": "copy", "cleanup": false, "max_items": 20,
		},
	})
	if rpcError != nil {
		t.Fatalf("plan: %s", rpcError.Message)
	}
	plan := decode(t, planned)
	counts := plan["counts"].(map[string]any)
	if numberField(counts, "total") != 3 {
		t.Fatalf("expected three candidates: %#v", counts)
	}
	if numberField(counts, "auto") != 1 || numberField(counts, "review") != 2 {
		t.Fatalf("expected 1 auto and 2 review: %#v", counts)
	}
	runID := plan["run_id"].(string)

	applied, rpcError := call(t, "media-library.organize.apply", map[string]any{"run_id": runID})
	if rpcError != nil {
		t.Fatalf("apply: %s", rpcError.Message)
	}
	result := decode(t, applied)
	if numberField(result, "ingested") != 1 {
		t.Fatalf("expected one ingested item: %#v", result)
	}
	items, rpcError := call(t, "media-library.holdings.items", map[string]any{
		"library_id": libraryID, "limit": 20, "offset": 0,
	})
	if rpcError != nil {
		t.Fatalf("holdings: %s", rpcError.Message)
	}
	if numberField(decode(t, items), "total") != 1 {
		t.Fatalf("expected one library item: %#v", decode(t, items))
	}
	listed := decode(t, items)["items"].([]any)[0].(map[string]any)
	if _, statErr := os.Stat(listed["path"].(string)); statErr != nil {
		t.Fatalf("ingested file should exist: %v", statErr)
	}
	// 自动入库的条目不挂精选来源，后续才能被自动整理清理而不误伤人工精选。
	if origin := listed["origin_selection_id"]; origin != "" {
		t.Fatalf("auto item should not carry selection origin: %#v", origin)
	}

	review, rpcError := call(t, "media-library.organize.review.list", map[string]any{"run_id": runID, "limit": 20})
	if rpcError != nil {
		t.Fatalf("review list: %s", rpcError.Message)
	}
	reviewItems := decode(t, review)["items"].([]any)
	if len(reviewItems) != 2 {
		t.Fatalf("expected two review items: %#v", reviewItems)
	}
	firstID := reviewItems[0].(map[string]any)["asset_id"].(string)
	secondID := reviewItems[1].(map[string]any)["asset_id"].(string)
	decided, rpcError := call(t, "media-library.organize.review.decide", map[string]any{
		"run_id": runID,
		"items": []map[string]any{
			{"asset_id": firstID, "accept": true},
			{"asset_id": secondID, "accept": false},
		},
	})
	if rpcError != nil {
		t.Fatalf("review decide: %s", rpcError.Message)
	}
	if numberField(decode(t, decided), "accepted") != 1 || numberField(decode(t, decided), "rejected") != 1 {
		t.Fatalf("unexpected decide result: %#v", decode(t, decided))
	}
	itemsAfter, rpcError := call(t, "media-library.holdings.items", map[string]any{
		"library_id": libraryID, "limit": 20, "offset": 0,
	})
	if rpcError != nil {
		t.Fatalf("holdings after review: %s", rpcError.Message)
	}
	if numberField(decode(t, itemsAfter), "total") != 2 {
		t.Fatalf("expected two library items: %#v", decode(t, itemsAfter))
	}

	// 再跑一次：已在库里的内容不会再被规划；清理只针对源文件已消失的条目，不会把库翻动一遍。
	replanned, rpcError := call(t, "media-library.organize.plan", map[string]any{
		"library_id": libraryID, "source_id": sourceID,
		"policy": map[string]any{"quality_floor": 1, "per_cluster": 1, "mode": "copy", "cleanup": true},
	})
	if rpcError != nil {
		t.Fatalf("re-plan: %s", rpcError.Message)
	}
	secondPlan := decode(t, replanned)
	if numberField(secondPlan["counts"].(map[string]any), "total") != 1 {
		t.Fatalf("expected one remaining candidate: %#v", secondPlan["counts"])
	}
	secondApplied, rpcError := call(t, "media-library.organize.apply", map[string]any{"run_id": secondPlan["run_id"].(string)})
	if rpcError != nil {
		t.Fatalf("second apply: %s", rpcError.Message)
	}
	if numberField(decode(t, secondApplied), "removed") != 0 {
		t.Fatalf("cleanup must not remove live items: %#v", decode(t, secondApplied))
	}
	itemsFinal, rpcError := call(t, "media-library.holdings.items", map[string]any{
		"library_id": libraryID, "limit": 20, "offset": 0,
	})
	if rpcError != nil {
		t.Fatalf("holdings final: %s", rpcError.Message)
	}
	if numberField(decode(t, itemsFinal), "total") != 3 {
		t.Fatalf("expected three library items after second run: %#v", decode(t, itemsFinal))
	}
}
