package main

import (
	"path/filepath"
	"testing"
)

func TestAIBudgetTracksTokensAndCaps(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	root := t.TempDir()
	writeTestPNG(t, filepath.Join(root, "budget.png"))
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
	id := candidateIDByPath(t, filepath.Join(root, "budget.png"))

	fresh, rpcError := call(t, "media-library.ai.budget", map[string]any{"planned_calls": 10})
	if rpcError != nil {
		t.Fatalf("budget: %s", rpcError.Message)
	}
	before := decode(t, fresh)
	if before["allowed"] != true {
		t.Fatalf("expected budget allowed: %#v", before)
	}
	if used := before["used_tokens"].(map[string]any); used["today"].(float64) != 0 {
		t.Fatalf("expected no tokens yet: %#v", used)
	}
	if budget := before["next_job_budget_tokens"].(float64); budget <= 0 {
		t.Fatalf("expected positive job budget: %#v", before)
	}

	if _, rpcError := call(t, "media-library.asset.ai-cache.set", map[string]any{
		"asset_id": id, "profile": "fast", "model": "custom:test", "prompt_version": "tags-v2",
		"result": map[string]any{"tags": []string{"预算标签"}}, "tokens": 400000,
	}); rpcError != nil {
		t.Fatalf("set cache: %s", rpcError.Message)
	}
	after, rpcError := call(t, "media-library.ai.budget", map[string]any{"planned_calls": 10})
	if rpcError != nil {
		t.Fatalf("budget after: %s", rpcError.Message)
	}
	spent := decode(t, after)
	used := spent["used_tokens"].(map[string]any)
	if used["today"].(float64) != 400000 || used["month"].(float64) != 400000 {
		t.Fatalf("expected tracked tokens: %#v", used)
	}
	calls := spent["calls"].(map[string]any)
	if calls["today"].(float64) != 1 {
		t.Fatalf("expected one recorded call: %#v", calls)
	}

	// 重复写入同一结果不应再次计入今日花费。
	if _, rpcError := call(t, "media-library.asset.ai-cache.set", map[string]any{
		"asset_id": id, "profile": "fast", "model": "custom:test", "prompt_version": "tags-v2",
		"result": map[string]any{"tags": []string{"预算标签"}}, "tokens": 400000,
	}); rpcError != nil {
		t.Fatalf("set cache again: %s", rpcError.Message)
	}
	again, rpcError := call(t, "media-library.ai.budget", map[string]any{"planned_calls": 10})
	if rpcError != nil {
		t.Fatalf("budget again: %s", rpcError.Message)
	}
	if decode(t, again)["calls"].(map[string]any)["today"].(float64) != 1 {
		t.Fatalf("expected idempotent accounting: %#v", decode(t, again))
	}
}
