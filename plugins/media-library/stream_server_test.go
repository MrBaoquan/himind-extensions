package main

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAssetPreviewStreamsWithRange(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	root := t.TempDir()
	videoPath := filepath.Join(root, "clip.mp4")
	if !writeTestMP4(t, videoPath) {
		t.Skip("ffmpeg 不可用")
	}
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
	id := candidateIDByPath(t, videoPath)
	preview, rpcError := call(t, "media-library.asset.preview", map[string]any{
		"asset_id": id, "path": videoPath, "kind": "video",
	})
	if rpcError != nil {
		t.Fatalf("preview: %s", rpcError.Message)
	}
	streamURL, _ := decode(t, preview)["stream_url"].(string)
	if streamURL == "" {
		t.Fatalf("expected stream_url, got %#v", decode(t, preview))
	}
	parsed, err := url.Parse(streamURL)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodGet, parsed.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Range", "bytes=0-99")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusPartialContent {
		t.Fatalf("expected 206, got %d", response.StatusCode)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != 100 {
		t.Fatalf("expected 100 bytes, got %d", len(body))
	}
	if response.Header.Get("Accept-Ranges") != "bytes" {
		t.Fatalf("expected Accept-Ranges: bytes, got %q", response.Header.Get("Accept-Ranges"))
	}

	// 错误 token 必须被拒绝。
	bad := *parsed
	query := bad.Query()
	query.Set("token", strings.Repeat("0", len(query.Get("token"))))
	bad.RawQuery = query.Encode()
	response2, err := http.Get(bad.String())
	if err != nil {
		t.Fatal(err)
	}
	response2.Body.Close()
	if response2.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for bad token, got %d", response2.StatusCode)
	}
}

func TestStreamableAssetPathRejectsUnknownID(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	if _, err := streamableAssetPath("candidate-not-exist"); err == nil {
		t.Fatal("expected unknown asset to fail")
	}
	if _, err := os.Stat(filepath.Join(os.TempDir(), "nope")); err == nil {
		t.Fatal("sanity")
	}
}
