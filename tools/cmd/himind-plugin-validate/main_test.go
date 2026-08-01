package validate

import (
	"archive/zip"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestParseManifestAcceptsCurrentPluginShape(t *testing.T) {
	data := []byte(`{
		"id":"com.himind.example.tool",
		"name":"Example Tool",
		"author":"Test User",
		"categories":["software-engineering"],
		"description":"Example plugin.",
		"release_notes":"Initial release.",
		"version":"1.0.0",
		"entry":"example.exe",
		"runtime":"process-jsonrpc-stdio",
		"min_agent_version":"0.2.0",
		"governance":"optional",
		"capabilities":[{"id":"example.run","input_schema":{"type":"object"},"risk_level":"read_only"}],
		"permissions":[]
	}`)
	item, err := ParseManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(item.Platforms) != 1 || item.Platforms[0] != "windows-x64" {
		t.Fatalf("unexpected default platforms: %#v", item.Platforms)
	}
}

func TestParseManifestRejectsUnsafeEntry(t *testing.T) {
	data := []byte(`{
		"id":"com.himind.example.tool",
		"name":"Example Tool",
		"version":"1.0.0",
		"entry":"../example.exe",
		"runtime":"process-jsonrpc-stdio",
		"governance":"optional"
	}`)
	if _, err := ParseManifest(data); err == nil {
		t.Fatal("expected unsafe entry to be rejected")
	}
}

func TestValidateArchiveRejectsTraversal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unsafe.hmpkg")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	entry, err := writer.Create("../outside.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("unsafe")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ValidateArchive(path); err == nil {
		t.Fatal("expected traversal archive to be rejected")
	}
}

func TestValidateArchiveAcceptsCompleteChecksumsAndRejectsTampering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "valid.hmpkg")
	manifest := []byte(`{"id":"com.himind.example.tool","name":"Example","author":"Test User","categories":["software-engineering"],"description":"Example plugin.","release_notes":"Initial release.","version":"1.0.0","entry":"bin/tool.exe","runtime":"process-jsonrpc-stdio","platforms":["windows-x64"],"governance":"optional"}`)
	entry := []byte("binary")
	writeChecksumArchive(t, path, manifest, entry, entry)
	if err := ValidateArchive(path); err != nil {
		t.Fatal(err)
	}
	writeChecksumArchive(t, path, manifest, entry, []byte("tampered"))
	if err := ValidateArchive(path); err == nil {
		t.Fatal("expected tampered archive to be rejected")
	}
}

func writeChecksumArchive(t *testing.T, path string, manifest, checksumEntry, archiveEntry []byte) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for name, content := range map[string][]byte{"plugin.json": manifest, "bin/tool.exe": archiveEntry} {
		item, createErr := writer.Create(name)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, writeErr := item.Write(content); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	manifestHash := sha256.Sum256(manifest)
	entryHash := sha256.Sum256(checksumEntry)
	checksums, err := writer.Create("checksums.sha256")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fmt.Fprintf(checksums, "%x  plugin.json\n%x  bin/tool.exe\n", manifestHash, entryHash)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
