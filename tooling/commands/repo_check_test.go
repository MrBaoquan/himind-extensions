package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureCatalogCompleteRejectsStaleExtensionDirectories(t *testing.T) {
	root := t.TempDir()
	stale := filepath.Join(root, "plugins", "stale-with-build-output", "bin")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	err := ensureCatalogComplete(root, "plugins", "plugin.json", map[string]string{})
	if err == nil || !strings.Contains(err.Error(), "missing plugin.json") {
		t.Fatalf("stale build directory must fail with a manifest hint, got %v", err)
	}
	if err := os.RemoveAll(filepath.Join(root, "plugins", "stale-with-build-output")); err != nil {
		t.Fatal(err)
	}

	// 下划线目录是仓库共享资源，不是扩展工程。
	if err := os.MkdirAll(filepath.Join(root, "plugins", "_shared"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ensureCatalogComplete(root, "plugins", "plugin.json", map[string]string{}); err != nil {
		t.Fatalf("shared underscore directory must be ignored: %v", err)
	}
}

func TestEnsureCatalogCompleteStillRequiresExtensionsJSONEntry(t *testing.T) {
	root := t.TempDir()
	valid := filepath.Join(root, "plugins", "valid")
	if err := os.MkdirAll(valid, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(valid, "plugin.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := ensureCatalogComplete(root, "plugins", "plugin.json", map[string]string{})
	if err == nil || !strings.Contains(err.Error(), "missing from extensions.json") {
		t.Fatalf("manifest without extensions.json entry must fail, got %v", err)
	}
}
