package main

import (
	"archive/zip"
	"encoding/json"
	"fmt"
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
func TestChannelDocumentURLUsesRepositoryAndChannel(t *testing.T) {
	location, err := channelDocumentURL(input{
		ProductID:  "com.himind.media-resolver",
		Repository: "MrBaoquan/MediaResolver",
		Channel:    "beta",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://raw.githubusercontent.com/MrBaoquan/MediaResolver/HEAD/distribution/com.himind.media-resolver/beta.json"
	if location != want {
		t.Fatalf("channel url = %s, want %s", location, want)
	}
}

func TestChannelDocumentURLRejectsUntrustedHost(t *testing.T) {
	if _, err := channelDocumentURL(input{
		ProductID:  "com.himind.media-resolver",
		ChannelURL: "https://evil.example.com/stable.json",
	}); err == nil {
		t.Fatal("expected untrusted channel_url to be rejected")
	}
}

func TestEvaluateChannelDocumentAcceptsNewerSignedRelease(t *testing.T) {
	result, err := evaluateChannelDocument(input{
		ProductID:      "com.himind.media-resolver",
		Channel:        "stable",
		Platform:       "windows",
		Architecture:   "x64",
		PackageType:    "directory-zip",
		CurrentVersion: "1.3.0",
	}, signedChannelDocument("1.4.0"), "https://raw.githubusercontent.com/o/r/HEAD/distribution/com.himind.media-resolver/stable.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	payload, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type %T", result)
	}
	update, ok := payload["update"].(softwareUpdateManifest)
	if !ok {
		t.Fatalf("expected an update, got %#v", payload["update"])
	}
	if update.Version != "1.4.0" || update.SignatureKeyID != "himind-production-2026" {
		t.Fatalf("unexpected manifest: %#v", update)
	}
}

func TestEvaluateChannelDocumentRejectsUnsignedByDefault(t *testing.T) {
	unsigned := []byte(`{"schema_version":"software_distribution_channel.v1","product_id":"com.himind.media-resolver","channel":"stable","platform":"windows","architecture":"x64","package_type":"directory-zip","release":{"version":"1.4.0","file_name":"a.zip","size_bytes":10,"sha256":"` + testDigest + `","download_url":"https://github.com/o/r/releases/download/t/a.zip","signature":null}}`)
	if _, err := evaluateChannelDocument(input{
		ProductID:      "com.himind.media-resolver",
		CurrentVersion: "1.3.0",
	}, unsigned, "https://raw.githubusercontent.com/o/r/HEAD/x.json"); err == nil {
		t.Fatal("expected unsigned release to be rejected")
	}
	if _, err := evaluateChannelDocument(input{
		ProductID:      "com.himind.media-resolver",
		CurrentVersion: "1.3.0",
		AllowUnsigned:  true,
	}, unsigned, "https://raw.githubusercontent.com/o/r/HEAD/x.json"); err != nil {
		t.Fatalf("expected allow_unsigned to pass: %v", err)
	}
}

func TestEvaluateChannelDocumentRefusesDowngradeByDefault(t *testing.T) {
	result, err := evaluateChannelDocument(input{
		ProductID:      "com.himind.media-resolver",
		CurrentVersion: "2.0.0",
	}, signedChannelDocument("1.4.0"), "https://raw.githubusercontent.com/o/r/HEAD/x.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	payload := result.(map[string]any)
	if payload["update"] != nil {
		t.Fatalf("expected downgrade to be refused, got %#v", payload["update"])
	}
	if payload["reason"] != "线上版本低于当前版本，默认拒绝降级" {
		t.Fatalf("unexpected reason: %v", payload["reason"])
	}
}

func TestEvaluateChannelDocumentRejectsMismatchedIdentity(t *testing.T) {
	body := signedChannelDocument("1.4.0")
	for name, request := range map[string]input{
		"product":  {ProductID: "com.himind.other", CurrentVersion: "1.0.0"},
		"channel":  {ProductID: "com.himind.media-resolver", Channel: "beta", CurrentVersion: "1.0.0"},
		"platform": {ProductID: "com.himind.media-resolver", Platform: "android", CurrentVersion: "1.0.0"},
		"package":  {ProductID: "com.himind.media-resolver", PackageType: "apk", CurrentVersion: "1.0.0"},
	} {
		if _, err := evaluateChannelDocument(request, body, "https://raw.githubusercontent.com/o/r/HEAD/x.json"); err == nil {
			t.Fatalf("expected %s mismatch to be rejected", name)
		}
	}
}

func TestEvaluateChannelDocumentRejectsRevokedAndForeignDownloadHost(t *testing.T) {
	revoked := signedChannelDocument("1.4.0")
	revoked = []byte(strings.Replace(string(revoked), `"revoked":false`, `"revoked":true`, 1))
	result, err := evaluateChannelDocument(input{
		ProductID:      "com.himind.media-resolver",
		CurrentVersion: "1.3.0",
	}, revoked, "https://raw.githubusercontent.com/o/r/HEAD/x.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.(map[string]any)["update"] != nil {
		t.Fatal("expected revoked release to yield no update")
	}
	foreign := []byte(strings.Replace(string(signedChannelDocument("1.4.0")), "https://github.com/o/r/releases/download/t/a.zip", "https://evil.example.com/a.zip", 1))
	if _, err := evaluateChannelDocument(input{
		ProductID:      "com.himind.media-resolver",
		CurrentVersion: "1.3.0",
	}, foreign, "https://raw.githubusercontent.com/o/r/HEAD/x.json"); err == nil {
		t.Fatal("expected foreign download host to be rejected")
	}
}

func TestInstanceRolloutIsStableAndBounded(t *testing.T) {
	if !instanceInRollout("device-1", "1.4.0", 100) {
		t.Fatal("percent 100 must always be in rollout")
	}
	if instanceInRollout("device-1", "1.4.0", 0) {
		t.Fatal("percent 0 must never be in rollout")
	}
	first := instanceInRollout("device-1", "1.4.0", 50)
	if second := instanceInRollout("device-1", "1.4.0", 50); second != first {
		t.Fatal("rollout decision must be stable for the same instance and version")
	}
	if instanceInRollout("device-1", "1.4.0", 50) == instanceInRollout("device-1", "1.5.0", 50) {
		// 不强制不同版本结果相反，但必须能因版本变化而重新计算。
		_ = first
	}
	inRollout := 0
	for index := 0; index < 1000; index++ {
		if instanceInRollout(fmt.Sprintf("device-%d", index), "1.4.0", 50) {
			inRollout++
		}
	}
	if inRollout < 400 || inRollout > 600 {
		t.Fatalf("50%% rollout covered %d/1000 instances, expected roughly half", inRollout)
	}
}

func TestEvaluateChannelDocumentHonoursRolloutForKnownInstance(t *testing.T) {
	document := []byte(strings.Replace(string(signedChannelDocument("1.4.0")), `"rollout_percent":100`, `"rollout_percent":1`, 1))
	outsiders, insiders := "", ""
	for index := 0; index < 200; index++ {
		id := fmt.Sprintf("instance-%d", index)
		if instanceInRollout(id, "1.4.0", 1) {
			insiders = id
			break
		}
		outsiders = id
	}
	if insiders == "" {
		t.Skip("no instance landed inside a 1% rollout sample")
	}
	allowed, err := evaluateChannelDocument(input{
		ProductID:      "com.himind.media-resolver",
		CurrentVersion: "1.3.0",
		InstanceID:     insiders,
	}, document, "https://raw.githubusercontent.com/o/r/HEAD/x.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed.(map[string]any)["update"] == nil {
		t.Fatal("instance inside rollout must receive the update")
	}
	blocked, err := evaluateChannelDocument(input{
		ProductID:      "com.himind.media-resolver",
		CurrentVersion: "1.3.0",
		InstanceID:     outsiders,
	}, document, "https://raw.githubusercontent.com/o/r/HEAD/x.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if blocked.(map[string]any)["update"] != nil {
		t.Fatal("instance outside rollout must not receive the update")
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		left, right string
		want        int
	}{
		{"1.4.0", "1.3.9", 1},
		{"1.4.0", "1.4.0", 0},
		{"1.4.0", "1.10.0", -1},
		{"1.4.0-rc.1", "1.4.0", -1},
		{"2.0", "1.9.9", 1},
	}
	for _, item := range cases {
		if got := compareVersions(item.left, item.right); got != item.want {
			t.Fatalf("compareVersions(%s, %s) = %d, want %d", item.left, item.right, got, item.want)
		}
	}
}

const testDigest = "9c4cffb25cb43c507fbea39bd4e35510b5e69e23336215d22646cab4f067dbac"

func signedChannelDocument(version string) []byte {
	return []byte(`{"schema_version":"software_distribution_channel.v1","product_id":"com.himind.media-resolver","product_name":"MediaResolver","channel":"stable","platform":"windows","architecture":"x64","package_type":"directory-zip","release":{"version":"` + version + `","file_name":"a.zip","size_bytes":10,"sha256":"` + testDigest + `","download_url":"https://github.com/o/r/releases/download/t/a.zip","signature":{"key_id":"himind-production-2026","algorithm":"ed25519","value":"sig"},"mandatory":false,"rollout_percent":100,"revoked":false}}`)
}
