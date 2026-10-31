package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestBrokerCredentialIsAcquiredPerInvocation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer broker-token" {
			t.Fatalf("unexpected authorization header")
		}
		var body map[string]string
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatalf("decode broker request: %v", err)
		}
		if body["credential_id"] != "pexels.api" || body["purpose"] != "media-library.search" {
			t.Fatalf("unexpected broker request: %#v", body)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"credential_id":"pexels.api","fields":{"api_key":"brokered-secret"}}`))
	}))
	defer server.Close()
	t.Setenv(brokerURLEnd, server.URL)
	t.Setenv(brokerTokenEnv, "broker-token")

	fields, err := acquireCredential("pexels.api", "media-library.search")
	if err != nil {
		t.Fatalf("unexpected broker error: %v", err)
	}
	if fields["api_key"] != "brokered-secret" {
		t.Fatalf("unexpected credential fields: %#v", fields)
	}
}

func TestMissingBrokerCredentialDegradesSafely(t *testing.T) {
	t.Setenv(brokerURLEnd, "")
	t.Setenv(brokerTokenEnv, "")
	_, err := acquireCredential("pexels.api", "media-library.search")
	if err == nil {
		t.Fatal("expected missing broker to fail")
	}
	state, message := credentialState(err)
	if state != "unconfigured" {
		t.Fatalf("expected unconfigured state, got %q", state)
	}
	if strings.Contains(message, "brokered-secret") || strings.Contains(message, "provider-keys") {
		t.Fatalf("credential failure leaked private data: %s", message)
	}
}

func TestBrokerCredentialFailuresAreClassified(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   string
	}{
		{http.StatusConflict, "unconfigured"},
		{http.StatusForbidden, "permission_denied"},
		{http.StatusUnauthorized, "session_expired"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(tc.status)
				_, _ = writer.Write([]byte(`{"error":"credential_unavailable","message":"safe broker message"}`))
			}))
			defer server.Close()
			t.Setenv(brokerURLEnd, server.URL)
			t.Setenv(brokerTokenEnv, "token")
			_, err := acquireCredential("pexels.api", "media-library.search")
			if err == nil {
				t.Fatal("expected broker failure")
			}
			state, message := credentialState(err)
			if state != tc.want {
				t.Fatalf("expected state %q, got %q", tc.want, state)
			}
			if strings.Contains(message, "secret") {
				t.Fatalf("failure message leaked a secret: %s", message)
			}
		})
	}
}
func TestProviderListUsesAgentBrokerWithoutExposingSecret(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(request.Body).Decode(&body)
		_, _ = writer.Write([]byte(`{"fields":{"api_key":"broker-secret-value"}}`))
	}))
	defer server.Close()
	t.Setenv(brokerURLEnd, server.URL)
	t.Setenv(brokerTokenEnv, "token")
	result, rpcError := call(t, "media-library.provider.list", map[string]any{})
	if rpcError != nil {
		t.Fatalf("unexpected error: %s", rpcError.Message)
	}
	if strings.Contains(mustJSON(t, result), "broker-secret-value") {
		t.Fatal("provider list must not expose credential values")
	}
}

func TestLocalLibrarySourceScanAdoptAndSearch(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	libraryRoot := t.TempDir()
	sourceDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceDir, "hero-image.png"), []byte(strings.Repeat("i", 2048)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "voice-over.mp3"), []byte(strings.Repeat("a", 1024)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "notes.txt"), []byte("not media"), 0o644); err != nil {
		t.Fatal(err)
	}

	created, rpcError := call(t, "media-library.repository.create", map[string]any{
		"name": "测试资源库", "backend_type": "local", "root_path": libraryRoot, "access_mode": "contribute",
	})
	if rpcError != nil {
		t.Fatalf("create library: %s", rpcError.Message)
	}
	library := decode(t, created)
	libraryID := library["id"].(string)
	if _, err := os.Stat(filepath.Join(libraryRoot, ".himind-media", "library.json")); err != nil {
		t.Fatalf("library manifest missing: %v", err)
	}

	added, rpcError := call(t, "media-library.source.add", map[string]any{"path": sourceDir})
	if rpcError != nil {
		t.Fatalf("add source: %s", rpcError.Message)
	}
	sourceID := decode(t, added)["id"].(string)
	restart := true
	for {
		scanned, rpcError := call(t, "media-library.source.scan.batch", map[string]any{
			"source_id": sourceID, "restart": restart, "max_entries": 1,
		})
		if rpcError != nil {
			t.Fatalf("scan source: %s", rpcError.Message)
		}
		payload := decode(t, scanned)
		restart = false
		if payload["done"].(bool) {
			break
		}
	}

	found, rpcError := call(t, "media-library.asset.search", map[string]any{"query": "hero", "scope": "sources"})
	if rpcError != nil {
		t.Fatalf("search sources: %s", rpcError.Message)
	}
	searchPayload := decode(t, found)
	items := searchPayload["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected one candidate, got %d", len(items))
	}
	candidate := items[0].(map[string]any)
	candidateID := candidate["id"].(string)

	adopted, rpcError := call(t, "media-library.asset.adopt", map[string]any{
		"library_id": libraryID,
		"mode":       "copy",
		"actor":      "tester",
		"items": []map[string]any{{
			"candidate_id": candidateID,
			"license_name": "Test License",
			"title":        "主视觉图",
			"filename":     "城市主视觉",
			"collection":   "品牌资产/城市",
			"tags":         []string{"hero", "test", "夜景"},
		}},
	})
	if rpcError != nil {
		t.Fatalf("adopt: %s", rpcError.Message)
	}
	adoptedPayload := decode(t, adopted)
	saved := adoptedPayload["saved"].([]any)
	if len(saved) != 1 {
		t.Fatalf("expected one adopted asset, got %d", len(saved))
	}
	asset := saved[0].(map[string]any)
	if !strings.HasPrefix(asset["local_path"].(string), filepath.Join(libraryRoot, "assets")) {
		t.Fatalf("asset escaped library: %s", asset["local_path"])
	}
	eventPath := filepath.Join(libraryRoot, ".himind-media", "catalog", "users", "tester.jsonl")
	if _, err := os.Stat(eventPath); err != nil {
		t.Fatalf("catalog event missing: %v", err)
	}
	eventBytes, err := os.ReadFile(eventPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(eventBytes), "品牌资产/城市") || !strings.Contains(string(eventBytes), "夜景") {
		t.Fatalf("catalog event lost collection or tags: %s", eventBytes)
	}

	again, rpcError := call(t, "media-library.asset.adopt", map[string]any{
		"library_id": libraryID,
		"mode":       "copy",
		"actor":      "tester",
		"items": []map[string]any{{
			"candidate_id": candidateID,
		}},
	})
	if rpcError != nil {
		t.Fatalf("dedup adopt: %s", rpcError.Message)
	}
	if !decode(t, again)["saved"].([]any)[0].(map[string]any)["deduplicated"].(bool) {
		t.Fatal("expected duplicate adoption to be deduplicated")
	}

	librarySearch, rpcError := call(t, "media-library.asset.search", map[string]any{
		"query": "品牌资产", "scope": "library", "library_id": libraryID,
	})
	if rpcError != nil {
		t.Fatalf("search library: %s", rpcError.Message)
	}
	if !decode(t, found)["fts_available"].(bool) {
		t.Fatal("expected FTS index to be available")
	}
	libraryItems := decode(t, librarySearch)["items"].([]any)
	if len(libraryItems) != 1 {
		t.Fatal("expected adopted asset in library search")
	}
	libraryItem := libraryItems[0].(map[string]any)
	if libraryItem["collection"].(string) != "品牌资产/城市" {
		t.Fatalf("collection not searchable: %#v", libraryItem)
	}
	tags := libraryItem["tags"].([]any)
	if len(tags) != 3 || tags[2].(string) != "夜景" {
		t.Fatalf("tags not returned by search: %#v", libraryItem)
	}
}

func TestOnlineAssetAdoptionRecordsLicenseAndDeduplicates(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "image/png")
		_, _ = writer.Write([]byte(strings.Repeat("p", 4096)))
	}))
	defer server.Close()
	libraryRoot := t.TempDir()
	created, rpcError := call(t, "media-library.repository.create", map[string]any{
		"name": "在线采纳库", "backend_type": "local", "root_path": libraryRoot,
	})
	if rpcError != nil {
		t.Fatalf("create library: %s", rpcError.Message)
	}
	libraryID := decode(t, created)["id"].(string)
	item := map[string]any{
		"provider": "pexels", "provider_id": "42", "kind": "image", "title": "online.png",
		"author": "Photographer", "license_name": "Pexels License",
		"source_url": "https://example.invalid/photo/42", "download_url": server.URL + "/photo.png",
	}
	adopted, rpcError := call(t, "media-library.asset.adopt", map[string]any{
		"library_id": libraryID, "mode": "copy", "actor": "tester", "items": []map[string]any{item},
	})
	if rpcError != nil {
		t.Fatalf("adopt online asset: %s", rpcError.Message)
	}
	if len(decode(t, adopted)["saved"].([]any)) != 1 {
		t.Fatal("expected online asset to be adopted")
	}
	events, err := os.ReadFile(filepath.Join(libraryRoot, ".himind-media", "catalog", "users", "tester.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(events), "Pexels License") {
		t.Fatalf("catalog event lost license: %s", events)
	}
	if !strings.Contains(string(events), "Pexels License") {
		t.Fatalf("catalog event lost license: %s", events)
	}
}

func TestPreviewReturnsImageDataForAuthorizedLibraryPath(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	libraryRoot := t.TempDir()
	created, rpcError := call(t, "media-library.repository.create", map[string]any{
		"name": "预览库", "backend_type": "local", "root_path": libraryRoot,
	})
	if rpcError != nil {
		t.Fatalf("create library: %s", rpcError.Message)
	}
	assetDir := filepath.Join(libraryRoot, "assets", "cc")
	if err := os.MkdirAll(assetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	assetPath := filepath.Join(assetDir, "preview.png")
	if err := os.WriteFile(assetPath, []byte("image-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, rpcError := call(t, "media-library.asset.adopt", map[string]any{
		"library_id": decode(t, created)["id"].(string),
		"mode":       "reference",
		"items":      []map[string]any{{"local_path": assetPath, "kind": "image", "title": "preview.png"}},
	}); rpcError != nil {
		t.Fatalf("adopt preview asset: %s", rpcError.Message)
	}
	result, rpcError := call(t, "media-library.asset.preview", map[string]any{"path": assetPath, "kind": "image"})
	if rpcError != nil {
		t.Fatalf("preview: %s", rpcError.Message)
	}
	if !strings.HasPrefix(decode(t, result)["data_url"].(string), "data:image/png;base64,") {
		t.Fatal("expected image data URL")
	}
	if decode(t, created)["id"] == "" {
		t.Fatal("library id missing")
	}
}
func TestOnlineAdoptionUsesDownloadURLFilename(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "audio/mpeg")
		_, _ = writer.Write([]byte(strings.Repeat("m", 4096)))
	}))
	defer server.Close()
	libraryRoot := t.TempDir()
	created, rpcError := call(t, "media-library.repository.create", map[string]any{
		"name": "音频资源库", "backend_type": "local", "root_path": libraryRoot,
	})
	if rpcError != nil {
		t.Fatalf("create library: %s", rpcError.Message)
	}
	result, rpcError := call(t, "media-library.asset.adopt", map[string]any{
		"library_id": decode(t, created)["id"].(string),
		"mode":       "copy",
		"items": []map[string]any{{
			"provider": "freesound", "provider_id": "420826", "kind": "audio",
			"title": "城市交通氛围", "filename": "城市交通氛围", "collection": "验收/城市音频",
			"tags":         []string{"城市交通", "验收"},
			"download_url": server.URL + "/previews/city.mp3",
			"license_name": "CC BY 4.0", "redistribution_allowed": true,
		}},
	})
	if rpcError != nil {
		t.Fatalf("adopt: %s", rpcError.Message)
	}
	saved := decode(t, result)["saved"].([]any)[0].(map[string]any)
	if !strings.HasSuffix(saved["local_path"].(string), ".mp3") {
		t.Fatalf("expected download URL extension to win, got %s", saved["local_path"])
	}
	if !strings.HasSuffix(saved["relative_path"].(string), "城市交通氛围.mp3") {
		t.Fatalf("expected renamed relative path with .mp3, got %s", saved["relative_path"])
	}
	if saved["title"].(string) != "城市交通氛围" || saved["collection"].(string) != "验收/城市音频" {
		t.Fatalf("online rename metadata not returned: %#v", saved)
	}
}
func TestPreviewReturnsVideoAndAudioDataURLs(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	libraryRoot := t.TempDir()
	created, rpcError := call(t, "media-library.repository.create", map[string]any{
		"name": "媒体预览库", "backend_type": "local", "root_path": libraryRoot,
	})
	if rpcError != nil {
		t.Fatalf("create library: %s", rpcError.Message)
	}
	libraryID := decode(t, created)["id"].(string)
	for _, item := range []struct {
		name string
		kind string
		body string
	}{
		{"preview.mp4", "video", strings.Repeat("v", 2048)},
		{"preview.wav", "audio", strings.Repeat("a", 2048)},
	} {
		path := filepath.Join(libraryRoot, item.name)
		if err := os.WriteFile(path, []byte(item.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, rpcError := call(t, "media-library.asset.adopt", map[string]any{
			"library_id": libraryID, "mode": "reference",
			"items": []map[string]any{{"local_path": path, "kind": item.kind, "title": item.name}},
		}); rpcError != nil {
			t.Fatalf("adopt %s: %s", item.kind, rpcError.Message)
		}
		result, rpcError := call(t, "media-library.asset.preview", map[string]any{"path": path, "kind": item.kind})
		if rpcError != nil {
			t.Fatalf("preview %s: %s", item.kind, rpcError.Message)
		}
		prefix := "data:" + item.kind + "/"
		if !strings.HasPrefix(decode(t, result)["data_url"].(string), prefix) {
			t.Fatalf("expected %s data URL for %s", prefix, item.kind)
		}
	}
}

func TestConcurrentLibraryAndSourceLists(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	libraryRoot := t.TempDir()
	if _, rpcError := call(t, "media-library.repository.create", map[string]any{
		"name": "并发资源库", "backend_type": "local", "root_path": libraryRoot,
	}); rpcError != nil {
		t.Fatalf("create library: %s", rpcError.Message)
	}
	var wait sync.WaitGroup
	errs := make(chan string, 16)
	for i := 0; i < 8; i++ {
		wait.Add(2)
		go func() {
			defer wait.Done()
			if _, rpcError := call(t, "media-library.repository.list", map[string]any{}); rpcError != nil {
				errs <- rpcError.Message
			}
		}()
		go func() {
			defer wait.Done()
			if _, rpcError := call(t, "media-library.source.list", map[string]any{}); rpcError != nil {
				errs <- rpcError.Message
			}
		}()
	}
	wait.Wait()
	close(errs)
	for message := range errs {
		if strings.Contains(message, "SQLITE_BUSY") || strings.Contains(strings.ToLower(message), "database is locked") {
			t.Fatalf("concurrent list hit SQLite lock: %s", message)
		}
	}
}
func TestSMBLibraryRulesAndNormalization(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	normalized, err := normalizeLibraryRoot("\\\\nas\\media\\library", "smb")
	if err != nil {
		t.Fatalf("expected UNC path to be accepted: %v", err)
	}
	if !strings.HasPrefix(normalized, `\\`) {
		t.Fatalf("expected UNC normalized path, got %s", normalized)
	}
	if _, err := normalizeLibraryRoot(t.TempDir(), "smb"); err == nil {
		t.Fatal("expected local path to be rejected for SMB backend")
	}

	db, err := openLibraryDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	library := &libraryRecord{ID: "lib-test", RootURI: t.TempDir(), BackendType: "smb", AccessMode: "contribute"}
	_, err = adoptOne(db, library, "copy", "tester", adoptItem{
		Kind:                  "image",
		Title:                 "redistribution denied",
		DownloadURL:           "https://example.invalid/image.png",
		RedistributionAllowed: false,
	})
	if err == nil || !strings.Contains(err.Error(), "未明确允许再分发") {
		t.Fatalf("expected redistribution guard, got %v", err)
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
