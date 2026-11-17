package main

import (
	"path/filepath"
	"testing"
)

func TestAITagCacheRoundTrip(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	root := t.TempDir()
	writeTestPNG(t, filepath.Join(root, "cache.png"))
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
	id := candidateIDByPath(t, filepath.Join(root, "cache.png"))
	if _, rpcError := call(t, "media-library.asset.ai-cache.set", map[string]any{
		"asset_id": id, "profile": "fast", "model": "custom:test", "prompt_version": "tags-v2",
		"result": map[string]any{"tags": []string{"缓存标签"}}, "tokens": 123,
	}); rpcError != nil {
		t.Fatalf("set cache: %s", rpcError.Message)
	}
	hit, rpcError := call(t, "media-library.asset.ai-cache.get", map[string]any{
		"asset_id": id, "profile": "fast", "model": "custom:test", "prompt_version": "tags-v2",
	})
	if rpcError != nil {
		t.Fatalf("get cache: %s", rpcError.Message)
	}
	payload := decode(t, hit)
	if payload["hit"] != true || int(payload["tokens"].(float64)) != 123 {
		t.Fatalf("unexpected cache hit: %#v", payload)
	}
	tags := payload["result"].(map[string]any)["tags"].([]any)
	if len(tags) != 1 || tags[0] != "缓存标签" {
		t.Fatalf("unexpected cached tags: %#v", tags)
	}
	miss, rpcError := call(t, "media-library.asset.ai-cache.get", map[string]any{
		"asset_id": id, "profile": "full", "model": "custom:test", "prompt_version": "full-v2",
	})
	if rpcError != nil {
		t.Fatalf("get miss: %s", rpcError.Message)
	}
	if decode(t, miss)["hit"] != false {
		t.Fatalf("expected cache miss")
	}
}
