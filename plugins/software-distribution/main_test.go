package main

import (
	"archive/zip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MrBaoquan/himind-extensions/sdk/jsonrpc"
)

func TestArtifactInspectComputesMediaResolverMetadata(t *testing.T) {
	root := t.TempDir()
	artifact := filepath.Join(root, "MediaResolver-v1.0.0-win-x64-portable.zip")
	file, err := os.Create(artifact)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	entry, _ := writer.Create("MediaResolver.exe")
	_, _ = entry.Write([]byte("media-resolver"))
	_ = writer.Close()
	_ = file.Close()
	params, _ := json.Marshal(input{WorkspaceRoot: root, ArtifactPath: artifact, ProductID: "com.himind.media-resolver", Version: "1.0.0", Channel: "stable", Platform: "windows", Architecture: "x64", PackageType: "directory-zip"})
	value, rpcError := handle(jsonrpc.Request{Method: "software.distribution.artifact.inspect", Params: params})
	if rpcError != nil {
		t.Fatal(rpcError)
	}
	result := value.(map[string]any)
	if result["ready"] != true || result["sha256"] == "" || result["file_name"] != filepath.Base(artifact) {
		t.Fatalf("unexpected inspect result: %#v", result)
	}
}

func TestArtifactInspectRejectsWorkspaceEscape(t *testing.T) {
	root := t.TempDir()
	params, _ := json.Marshal(input{WorkspaceRoot: root, ArtifactPath: filepath.Join(root, "..", "outside.zip"), ProductID: "com.himind.media-resolver", Version: "1.0.0", Platform: "windows", Architecture: "x64", PackageType: "directory-zip"})
	_, rpcError := handle(jsonrpc.Request{Method: "software.distribution.artifact.inspect", Params: params})
	if rpcError == nil || !strings.Contains(rpcError.Message, "workspace_root") {
		t.Fatalf("unexpected error: %#v", rpcError)
	}
}

func TestResolveUsesConfiguredDashboardAndNoCredentialInput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/software-distribution/v1/updates/resolve" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"update":{"productId":"com.himind.media-resolver","version":"1.1.0"}}`))
	}))
	defer server.Close()
	t.Setenv("HIMIND_DASHBOARD_URL", server.URL)
	params, _ := json.Marshal(input{ProductID: "com.himind.media-resolver", CurrentVersion: "1.0.0", Channel: "stable", Platform: "windows", Architecture: "x64"})
	value, rpcError := handle(jsonrpc.Request{Method: "software.distribution.release.resolve", Params: params})
	if rpcError != nil {
		t.Fatal(rpcError)
	}
	if value.(map[string]any)["update"] == nil {
		t.Fatal("expected update")
	}
}
