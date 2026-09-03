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
	writeZIP(t, artifact, map[string]string{
		"MediaResolver.exe":                       "media-resolver",
		"distribution.sample.json":                `{"productId":"com.himind.media-resolver"}`,
		"updater/HiMind.Distribution.Updater.exe": "updater",
	})
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

func TestArtifactInspectRejectsIncompleteMediaResolverPackage(t *testing.T) {
	root := t.TempDir()
	artifact := filepath.Join(root, "MediaResolver-v1.0.0-win-x64-portable.zip")
	writeZIP(t, artifact, map[string]string{"MediaResolver.exe": "media-resolver"})
	params, _ := json.Marshal(input{WorkspaceRoot: root, ArtifactPath: artifact, ProductID: "com.himind.media-resolver", Version: "1.0.0", Channel: "stable", Platform: "windows", Architecture: "x64", PackageType: "directory-zip"})
	_, rpcError := handle(jsonrpc.Request{Method: "software.distribution.artifact.inspect", Params: params})
	if rpcError == nil || !strings.Contains(rpcError.Message, "distribution.sample.json") {
		t.Fatalf("unexpected error: %#v", rpcError)
	}
}

func TestProjectInspectDistinguishesWPFAndFindsClientState(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "MediaResolver")
	if err := os.MkdirAll(filepath.Join(project, "updater"), 0o755); err != nil {
		t.Fatal(err)
	}
	projectFile := `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><UseWPF>true</UseWPF><TargetFramework>net8.0-windows</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="HiMind.Distribution" Version="1.0.0" /><PackageReference Include="HiMind.Distribution.Windows" Version="1.0.0" /></ItemGroup></Project>`
	if err := os.WriteFile(filepath.Join(project, "MediaResolver.csproj"), []byte(projectFile), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "distribution.sample.json"), []byte(`{"productId":"com.himind.media-resolver","resolve":"/api/software-distribution/v1/updates/resolve"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "updater", "HiMind.Distribution.Updater.exe"), []byte("updater"), 0o644); err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(input{WorkspaceRoot: root, ProjectPath: project, ProductID: "com.himind.media-resolver"})
	value, rpcError := handle(jsonrpc.Request{Method: "software.distribution.project.inspect", Params: params})
	if rpcError != nil {
		t.Fatal(rpcError)
	}
	result := value.(map[string]any)
	if result["project_type"] != "wpf" {
		t.Fatalf("project type = %#v", result["project_type"])
	}
	status := result["client_status"].(map[string]any)
	for _, key := range []string{"protocol_package_installed", "windows_adapter_installed", "configuration_detected", "updater_detected"} {
		if status[key] != true {
			t.Fatalf("%s = %#v", key, status[key])
		}
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

func TestArtifactInspectAcceptsExplicitExternalWorkspace(t *testing.T) {
	workspace := t.TempDir()
	artifact := filepath.Join(workspace, "release.zip")
	writeZIP(t, artifact, map[string]string{"app.exe": "release"})
	params, _ := json.Marshal(input{
		WorkspaceRoot: workspace,
		ArtifactPath:  artifact,
		ProductID:     "com.example.app",
		Version:       "1.0.0",
		Channel:       "stable",
		Platform:      "windows",
		Architecture:  "x64",
		PackageType:   "directory-zip",
	})
	value, rpcError := handle(jsonrpc.Request{Method: "software.distribution.artifact.inspect", Params: params})
	if rpcError != nil {
		t.Fatal(rpcError)
	}
	result := value.(map[string]any)
	expectedArtifact, err := filepath.EvalSymlinks(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if result["ready"] != true || result["artifact_path"] != expectedArtifact {
		t.Fatalf("unexpected external workspace result: %#v", result)
	}
}

func TestResolveUsesConfiguredDashboardAndNoCredentialInput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/software-distribution/v1/updates/resolve" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"update":{"productId":"com.himind.media-resolver","version":"1.1.0","releaseName":"MediaResolver 1.1.0","channel":"stable","artifactUrl":"https://dashboard.example/api/software-distribution/v1/artifacts/a1/download?ticket=short-lived","fileName":"MediaResolver-v1.1.0-win-x64-portable.zip","packageType":"directory-zip","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","size":1024,"signature":"signature","signatureKeyId":"release-key-1","signatureAlgorithm":"rsa-pss-sha256"}}`))
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

func TestResolveRejectsMismatchedManifest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"update":{"productId":"com.himind.other","version":"1.1.0"}}`))
	}))
	defer server.Close()
	t.Setenv("HIMIND_DASHBOARD_URL", server.URL)
	params, _ := json.Marshal(input{ProductID: "com.himind.media-resolver", CurrentVersion: "1.0.0", Channel: "stable", Platform: "windows", Architecture: "x64"})
	_, rpcError := handle(jsonrpc.Request{Method: "software.distribution.release.resolve", Params: params})
	if rpcError == nil || !strings.Contains(rpcError.Message, "productId") {
		t.Fatalf("unexpected error: %#v", rpcError)
	}
}

func writeZIP(t *testing.T, target string, entries map[string]string) {
	t.Helper()
	file, err := os.Create(target)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for name, content := range entries {
		entry, createErr := writer.Create(name)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, writeErr := entry.Write([]byte(content)); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
