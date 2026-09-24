package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MrBaoquan/himind-extensions/tooling/distribution"
)

type fixture struct {
	root       string
	source     string
	artifact   string
	manifest   string
	catalog    string
	artifactAt string
	manifestAt string
}

func newFixture(t *testing.T, kind, id, version, channel string) fixture {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, kind)
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	name, err := catalogManifestFile(kind)
	if err != nil {
		t.Fatal(err)
	}
	artifactName, err := distribution.ArtifactName(kind, id, version)
	if err != nil {
		t.Fatal(err)
	}
	tag, err := distribution.ReleaseTag(kind, id, version)
	if err != nil {
		t.Fatal(err)
	}
	sourceManifest := map[string]interface{}{
		"id": id, "name": "示例扩展", "author": "Tester", "version": version,
		"description": "用于测试索引记录生成。", "release_notes": "首次发布。",
		"categories": []string{"software-engineering"}, "min_agent_version": "0.3.0",
	}
	writeJSON(t, filepath.Join(source, name), sourceManifest)
	artifact := filepath.Join(root, artifactName)
	payload := []byte("signed artifact " + id)
	if err := os.WriteFile(artifact, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	release := distribution.ReleaseManifest{
		SchemaVersion: distribution.ReleaseManifestSchema,
		Repository:    "MrBaoquan/himind-extensions", Tag: tag, Kind: kind,
		ID: id, Version: version, Channel: channel, SourceCommit: "commit-sha",
		MinAgentVersion: "0.3.0",
		Artifact: distribution.ReleaseArtifact{
			Name: artifactName, SizeBytes: int64(len(payload)), SHA256: hex.EncodeToString(digest[:]),
		},
		Signature: &distribution.ReleaseSignature{
			FileName: artifactName, FileSize: int64(len(payload)), SHA256: hex.EncodeToString(digest[:]),
			Signature: "c2ln", SignatureKeyID: "key-1", SignatureAlgorithm: "rsa-pss-sha256",
		},
	}
	releasePath := filepath.Join(root, distribution.ManifestName(id, version))
	if err := distribution.WriteReleaseManifest(releasePath, release); err != nil {
		t.Fatal(err)
	}
	catalogPath := filepath.Join(root, "catalog.json")
	writeJSON(t, catalogPath, map[string]interface{}{
		"schema_version": 1, "distribution_id": "mrbaoquan/himind-extensions",
		"channel": "stable", "catalog_id": "public", "generation": "old",
		"plugins": []interface{}{}, "skills": []interface{}{}, "workflows": []interface{}{},
	})
	return fixture{root: root, source: source, artifact: artifact, manifest: releasePath, catalog: catalogPath, artifactAt: artifact, manifestAt: releasePath}
}

func TestUpsertWritesCanonicalEntry(t *testing.T) {
	item := newFixture(t, "plugin", "com.example.beta", "1.0.0", "stable")
	if err := upsert("plugin", item.source, item.manifest, item.artifact, "", item.catalog, "2026-09-17T00:00:00Z", "tree-sha", "MrBaoquan/himind-extensions"); err != nil {
		t.Fatal(err)
	}
	value := readCatalog(t, item.catalog)
	if len(value.Plugins) != 1 {
		t.Fatalf("plugins = %d", len(value.Plugins))
	}
	entry := value.Plugins[0]
	if entry["release_tag"] != "plugin/com.example.beta@1.0.0" {
		t.Fatalf("release_tag = %#v", entry["release_tag"])
	}
	if entry["file_name"] != "com.example.beta-1.0.0.hmpkg" {
		t.Fatalf("file_name = %#v", entry["file_name"])
	}
	want := "https://github.com/MrBaoquan/himind-extensions/releases/download/plugin%2Fcom.example.beta@1.0.0/com.example.beta-1.0.0.hmpkg"
	if entry["download_url"] != want {
		t.Fatalf("download_url = %#v", entry["download_url"])
	}
	if entry["source_tree"] != "tree-sha" || entry["signature_key_id"] != "key-1" {
		t.Fatalf("entry = %#v", entry)
	}
	if value.Generation == "old" || value.Generation == "" {
		t.Fatalf("generation = %#v", value.Generation)
	}
}

func TestUpsertRejectsVersionMismatch(t *testing.T) {
	item := newFixture(t, "plugin", "com.example.beta", "1.0.0", "stable")
	// 源码清单停留在别的版本时，不能生成索引记录：索引会指向半个版本。
	writeJSON(t, filepath.Join(item.source, "plugin.json"), map[string]interface{}{
		"id": "com.example.beta", "name": "示例扩展", "author": "Tester",
		"version": "1.1.0", "description": "d", "release_notes": "r",
		"categories": []string{"software-engineering"},
	})
	if err := upsert("plugin", item.source, item.manifest, item.artifact, "", item.catalog, "2026-09-17T00:00:00Z", "", ""); err == nil {
		t.Fatal("版本不一致时必须拒绝写入索引")
	}
}

func TestUpsertRejectsEditedArtifact(t *testing.T) {
	item := newFixture(t, "plugin", "com.example.beta", "1.0.0", "stable")
	if err := os.WriteFile(item.artifact, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := upsert("plugin", item.source, item.manifest, item.artifact, "", item.catalog, "2026-09-17T00:00:00Z", "", ""); err == nil {
		t.Fatal("制品被改动后必须拒绝写入索引")
	}
}

func TestWorkflowUpsertRequiresAndStoresExtensionLock(t *testing.T) {
	item := newFixture(t, "workflow", "com.example.workflow", "1.0.0", "stable")
	if err := upsert("workflow", item.source, item.manifest, item.artifact, "", item.catalog, "2026-09-17T00:00:00Z", "", ""); err == nil {
		t.Fatal("工作流发布缺少扩展锁时必须失败")
	}
	lock := filepath.Join(item.root, "com.example.workflow-1.0.0.extension-lock.json")
	writeJSON(t, lock, map[string]interface{}{"schema_version": "extension_lock.v1"})
	if err := upsert("workflow", item.source, item.manifest, item.artifact, lock, item.catalog, "2026-09-17T00:00:00Z", "", ""); err != nil {
		t.Fatal(err)
	}
	value := readCatalog(t, item.catalog)
	if len(value.Workflows) != 1 {
		t.Fatalf("workflows = %d", len(value.Workflows))
	}
	if lock, ok := value.Workflows[0]["extension_lock"].(map[string]interface{}); !ok || lock["schema_version"] != "extension_lock.v1" {
		t.Fatalf("extension_lock = %#v", value.Workflows[0]["extension_lock"])
	}
}

func catalogManifestFile(kind string) (string, error) {
	switch kind {
	case "plugin":
		return "plugin.json", nil
	case "skill":
		return "skill.json", nil
	default:
		return "workflow.json", nil
	}
}

type catalogFile struct {
	Generation string                   `json:"generation"`
	Plugins    []map[string]interface{} `json:"plugins"`
	Skills     []map[string]interface{} `json:"skills"`
	Workflows  []map[string]interface{} `json:"workflows"`
}

func readCatalog(t *testing.T, path string) catalogFile {
	t.Helper()
	var value catalogFile
	if err := json.Unmarshal([]byte(readFile(t, path)), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func writeJSON(t *testing.T, path string, value interface{}) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
