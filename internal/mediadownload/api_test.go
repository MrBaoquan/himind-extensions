package mediadownload

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProductIdentityCannotFallBackToLegacyData(t *testing.T) {
	original := currentProductConfig()
	t.Cleanup(func() { _ = Configure(original) })
	root := filepath.Join(t.TempDir(), "media-library")
	if err := Configure(Config{PluginID: "com.himind.media-library", DataRoot: root, StrictDataRoot: true}); err != nil {
		t.Fatal(err)
	}
	candidates, err := pluginDataRootCandidates()
	if err != nil || len(candidates) != 1 || candidates[0] != root {
		t.Fatalf("unexpected data roots: %v, %v", candidates, err)
	}
	if err := os.WriteFile(filepath.Join(root, "blocked"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Configure(Config{PluginID: "com.himind.media-library", DataRoot: filepath.Join(root, "blocked", "child"), StrictDataRoot: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := writablePluginDataRoot(); err == nil {
		t.Fatal("a blocked explicit root must fail instead of falling back")
	}
}
