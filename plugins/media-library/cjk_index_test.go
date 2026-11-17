package main

import (
	"path/filepath"
	"testing"
)

func TestShortChineseSearchUsesCJKIndex(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	root := t.TempDir()
	imagePath := filepath.Join(root, "海边日落.png")
	writeTestPNG(t, imagePath)
	created, rpcError := call(t, "media-library.source.add", map[string]any{"path": root})
	if rpcError != nil {
		t.Fatalf("add source: %s", rpcError.Message)
	}
	sourceID := decode(t, created)["id"].(string)
	if _, rpcError := call(t, "media-library.source.scan.batch", map[string]any{
		"source_id": sourceID, "restart": true, "max_entries": 100,
	}); rpcError != nil {
		t.Fatalf("scan: %s", rpcError.Message)
	}
	id := candidateIDByPath(t, imagePath)
	for _, query := range []string{"日落", "边日", "海边"} {
		result, rpcError := call(t, "media-library.asset.search", map[string]any{"query": query, "limit": 10})
		if rpcError != nil {
			t.Fatalf("search %s: %s", query, rpcError.Message)
		}
		found := false
		for _, entry := range decode(t, result)["items"].([]any) {
			if entry.(map[string]any)["id"] == id {
				found = true
			}
		}
		if !found {
			t.Fatalf("short Chinese query %q did not find asset: %#v", query, decode(t, result))
		}
	}
}
