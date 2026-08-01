package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestPackagePluginCanReplaceOutputInsideProject(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"com.himind.example.tool","name":"Example","author":"Test User","categories":["software-engineering"],"description":"Example plugin.","release_notes":"Initial release.","version":"1.0.0","entry":"bin/tool.exe","runtime":"process-jsonrpc-stdio","platforms":["windows-x64"],"governance":"optional"}`
	if err := os.WriteFile(filepath.Join(root, "plugin.json"), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin/tool.exe"), []byte("binary"), 0755); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "tool.hmpkg")
	if err := packagePlugin(root, output); err != nil {
		t.Fatal(err)
	}
	if err := packagePlugin(root, output); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.OpenReader(output)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	for _, file := range archive.File {
		if file.Name == "tool.hmpkg" {
			t.Fatal("package must not contain its previous output")
		}
	}
}
