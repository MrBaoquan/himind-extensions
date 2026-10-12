package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MrBaoquan/himind-extensions/sdk/jsonrpc"
)

func decode(t *testing.T, value any) map[string]any {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return decoded
}

func call(t *testing.T, method string, params any) (any, *jsonrpc.Error) {
	t.Helper()
	request, err := jsonrpc.NewRequest(1, method, params)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	return handle(request)
}

func TestProviderListReportsConfiguration(t *testing.T) {
	t.Setenv("HIMIND_MEDIA_LIBRARY_FAKE", "1")
	t.Setenv("HIMIND_MEDIA_LIBRARY_PEXELS_KEY", "")
	result, rpcError := call(t, "media-library.provider.list", map[string]any{})
	if rpcError != nil {
		t.Fatalf("unexpected error: %s", rpcError.Message)
	}
	providers := decode(t, result)["providers"].([]any)
	if len(providers) != 4 {
		t.Fatalf("expected 4 providers, got %d", len(providers))
	}
	configured := map[string]bool{}
	for _, entry := range providers {
		item := entry.(map[string]any)
		configured[item["id"].(string)] = item["configured"].(bool)
	}
	if !configured["fake"] {
		t.Fatalf("fake provider should be configured when the flag is on")
	}
	if configured["pexels"] {
		t.Fatalf("pexels should stay unconfigured without a key")
	}
}

func TestSearchKeepsWorkingWhenOneSourceIsMissing(t *testing.T) {
	t.Setenv("HIMIND_MEDIA_LIBRARY_FAKE", "1")
	t.Setenv("HIMIND_MEDIA_LIBRARY_PEXELS_KEY", "")
	result, rpcError := call(t, "media-library.search", map[string]any{"query": "夜景", "kinds": []string{"image"}})
	if rpcError != nil {
		t.Fatalf("unexpected error: %s", rpcError.Message)
	}
	payload := decode(t, result)
	if payload["total"].(float64) == 0 {
		t.Fatalf("expected at least one item from the offline provider")
	}
	statuses := map[string]string{}
	for _, entry := range payload["providers"].([]any) {
		item := entry.(map[string]any)
		statuses[item["id"].(string)] = item["status"].(string)
	}
	if statuses["fake"] != "ok" {
		t.Fatalf("fake provider should succeed, got %q", statuses["fake"])
	}
	if statuses["pexels"] != "skipped" {
		t.Fatalf("pexels should be skipped without a key, got %q", statuses["pexels"])
	}
}

func TestSearchRejectsUnknownKind(t *testing.T) {
	if _, rpcError := call(t, "media-library.search", map[string]any{"query": "x", "kinds": []string{"model"}}); rpcError == nil {
		t.Fatalf("expected an error for an unsupported kind")
	}
}

func TestFetchAssetsWritesPayloadAndSourceRecord(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(strings.Repeat("img", 1024)))
	}))
	defer server.Close()

	workspace := t.TempDir()
	dataRoot := t.TempDir()
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", dataRoot)

	result, rpcError := call(t, "media-library.asset.fetch", map[string]any{
		"workspace_root": workspace,
		"dest_dir":       "assets",
		"items": []map[string]any{{
			"provider":     "pexels",
			"id":           "42",
			"kind":         "image",
			"title":        "样例",
			"license":      "Pexels License",
			"source_url":   "https://example.invalid/photo/42",
			"download_url": server.URL + "/photo.jpg",
		}},
	})
	if rpcError != nil {
		t.Fatalf("unexpected error: %s", rpcError.Message)
	}
	payload := decode(t, result)
	saved := payload["saved"].([]any)
	if len(saved) != 1 {
		t.Fatalf("expected one saved asset, got %d", len(saved))
	}
	record := saved[0].(map[string]any)
	path := record["path"].(string)
	if !strings.HasPrefix(path, filepath.Join(workspace, "assets")) {
		t.Fatalf("asset should land inside the workspace, got %s", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("payload missing: %v", err)
	}
	sidecar, err := os.ReadFile(path + ".source.json")
	if err != nil {
		t.Fatalf("source record missing: %v", err)
	}
	if !strings.Contains(string(sidecar), "Pexels License") {
		t.Fatalf("source record lost the license: %s", string(sidecar))
	}

	// 同一份内容再次取用应当判定为跳过，避免重复落盘。
	again, rpcError := call(t, "media-library.asset.fetch", map[string]any{
		"workspace_root": workspace,
		"dest_dir":       "assets",
		"items": []map[string]any{{
			"provider":     "pexels",
			"id":           "42",
			"kind":         "image",
			"download_url": server.URL + "/photo.jpg",
		}},
	})
	if rpcError != nil {
		t.Fatalf("unexpected error on repeat: %s", rpcError.Message)
	}
	if len(decode(t, again)["skipped"].([]any)) != 1 {
		t.Fatalf("expected the repeat fetch to be skipped")
	}
}

func TestFetchAssetsRejectsEscapeFromWorkspace(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	for _, dest := range []string{"../outside", "C:\\Windows"} {
		_, rpcError := call(t, "media-library.asset.fetch", map[string]any{
			"workspace_root": workspace,
			"dest_dir":       dest,
			"items": []map[string]any{{
				"provider":     "pexels",
				"id":           "1",
				"download_url": "https://example.invalid/a.png",
			}},
		})
		if rpcError == nil {
			t.Fatalf("expected dest_dir %q to be rejected", dest)
		}
	}
}

func TestLibraryIndexAndSearch(t *testing.T) {
	media := t.TempDir()
	if err := os.MkdirAll(filepath.Join(media, "shots"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(media, "shots", "hero-image.png"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(media, "shots", "voice-over.mp3"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(media, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())

	result, rpcError := call(t, "media-library.index.create", map[string]any{"roots": []string{media}})
	if rpcError != nil {
		t.Fatalf("unexpected error: %s", rpcError.Message)
	}
	indexed := decode(t, result)
	if indexed["total"].(float64) != 2 {
		t.Fatalf("expected 2 media files, got %v", indexed["total"])
	}

	found, rpcError := call(t, "media-library.index.search", map[string]any{"query": "hero"})
	if rpcError != nil {
		t.Fatalf("unexpected error: %s", rpcError.Message)
	}
	items := decode(t, found)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 match, got %d", len(items))
	}
	if items[0].(map[string]any)["kind"].(string) != "image" {
		t.Fatalf("expected the match to be classified as an image")
	}
}

func TestSearchLibraryWithoutIndexExplainsNextStep(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	result, rpcError := call(t, "media-library.index.search", map[string]any{})
	if rpcError != nil {
		t.Fatalf("unexpected error: %s", rpcError.Message)
	}
	payload := decode(t, result)
	if payload["indexed"].(bool) {
		t.Fatalf("expected indexed=false before any index is built")
	}
	if !strings.Contains(payload["message"].(string), "library.index") {
		t.Fatalf("the empty state should point at the indexing capability")
	}
}

func TestResolveWithinRejectsParentTraversal(t *testing.T) {
	root := t.TempDir()
	if _, err := resolveWithin(root, filepath.Join(root, "..", "elsewhere")); err == nil {
		t.Fatalf("expected traversal to be rejected")
	}
	if _, err := resolveWithin(root, filepath.Join(root, "nested", "file.png")); err != nil {
		t.Fatalf("expected an in-tree path to be accepted: %v", err)
	}
}

func TestConfigureProviderStoresKeyWithoutEchoing(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	t.Setenv("HIMIND_MEDIA_LIBRARY_PEXELS_KEY", "")

	if _, rpcError := call(t, "media-library.provider.configure", map[string]any{"provider": "pexels", "key": "short"}); rpcError == nil {
		t.Fatalf("short keys should be rejected")
	}
	if _, rpcError := call(t, "media-library.provider.configure", map[string]any{"provider": "unknown", "key": "0123456789abcdef"}); rpcError == nil {
		t.Fatalf("unknown providers should be rejected")
	}

	const secret = "0123456789abcdef"
	result, rpcError := call(t, "media-library.provider.configure", map[string]any{"provider": "pexels", "key": secret})
	if rpcError != nil {
		t.Fatalf("unexpected error: %s", rpcError.Message)
	}
	payload := decode(t, result)
	if payload["configured"] != true {
		t.Fatalf("expected configured=true after saving a key")
	}
	if strings.Contains(mustJSON(t, payload), secret) {
		t.Fatalf("the capability must not echo the key back")
	}

	list, rpcError := call(t, "media-library.provider.list", map[string]any{})
	if rpcError != nil {
		t.Fatalf("unexpected error: %s", rpcError.Message)
	}
	configured := map[string]bool{}
	for _, entry := range decode(t, list)["providers"].([]any) {
		item := entry.(map[string]any)
		configured[item["id"].(string)] = item["configured"].(bool)
	}
	if !configured["pexels"] {
		t.Fatalf("saving a key should make the source available")
	}

	if _, rpcError := call(t, "media-library.provider.configure", map[string]any{"provider": "pexels", "key": ""}); rpcError != nil {
		t.Fatalf("clearing should succeed: %s", rpcError.Message)
	}
	cleared, _ := call(t, "media-library.provider.list", map[string]any{})
	for _, entry := range decode(t, cleared)["providers"].([]any) {
		item := entry.(map[string]any)
		if item["id"].(string) == "pexels" && item["configured"].(bool) {
			t.Fatalf("clearing the key should put the source back to unconfigured")
		}
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(payload)
}
