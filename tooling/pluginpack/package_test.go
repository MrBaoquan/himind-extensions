package pluginpack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackageFilesSkipsGeneratedRuntimeTrees(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{
		"main.go",
		"renderer/remotion/index.jsx",
		"renderer/remotion/node_modules/react/index.js",
		"renderer/remotion/package-lock.json",
		"dist/ignored.js",
	} {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(path), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files, err := packageFiles(root, filepath.Join(root, "out.hmpkg"))
	if err != nil {
		t.Fatal(err)
	}
	joined := "\n" + strings.Join(files, "\n") + "\n"
	for _, forbidden := range []string{"node_modules", "package-lock.json", "dist/ignored.js"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("generated path was packaged: %s (%v)", forbidden, files)
		}
	}
	if !strings.Contains(joined, "main.go") || !strings.Contains(joined, "renderer/remotion/index.jsx") {
		t.Fatalf("source files were dropped: %v", files)
	}
}
