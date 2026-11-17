package main

import (
	"path/filepath"
	"testing"
)

func TestAIJobRefRoundTrip(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	root := t.TempDir()
	writeTestPNG(t, filepath.Join(root, "job.png"))
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
	if _, rpcError := call(t, "media-library.ai.job.set", map[string]any{
		"selection_id": "sel-test", "job_id": "vision-1-abcd1234",
		"profile": "fast", "service": "custom:test",
	}); rpcError != nil {
		t.Fatalf("set ref: %s", rpcError.Message)
	}
	found, rpcError := call(t, "media-library.ai.job.get", map[string]any{"selection_id": "sel-test"})
	if rpcError != nil {
		t.Fatalf("get ref: %s", rpcError.Message)
	}
	payload := decode(t, found)
	if payload["found"] != true || payload["job_id"] != "vision-1-abcd1234" || payload["profile"] != "fast" {
		t.Fatalf("unexpected ref: %#v", payload)
	}
	list, rpcError := call(t, "media-library.ai.job.list", map[string]any{})
	if rpcError != nil {
		t.Fatalf("list refs: %s", rpcError.Message)
	}
	if items := decode(t, list)["items"].([]any); len(items) != 1 {
		t.Fatalf("expected one ref: %#v", items)
	}
	if _, rpcError := call(t, "media-library.ai.job.clear", map[string]any{"selection_id": "sel-test"}); rpcError != nil {
		t.Fatalf("clear ref: %s", rpcError.Message)
	}
	cleared, rpcError := call(t, "media-library.ai.job.get", map[string]any{"selection_id": "sel-test"})
	if rpcError != nil {
		t.Fatalf("get cleared ref: %s", rpcError.Message)
	}
	if decode(t, cleared)["found"] != false {
		t.Fatalf("expected ref cleared: %#v", decode(t, cleared))
	}
}
