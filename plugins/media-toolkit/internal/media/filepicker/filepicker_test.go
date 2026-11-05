package filepicker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"himind-plugin/media-toolkit/internal/media/workspace"
)

func TestImportPathsKeepsWorkspaceFileRelative(t *testing.T) {
	rootDir := t.TempDir()
	input := filepath.Join(rootDir, "assets", "photo.png")
	if err := os.MkdirAll(filepath.Dir(input), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := workspace.Open(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ImportPaths(context.Background(), root, []string{input}, Options{Mode: "image", Multiple: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 {
		t.Fatalf("want one file, got %d", len(result.Files))
	}
	file := result.Files[0]
	if file.Path != "assets/photo.png" || file.Kind != "image" || file.Imported {
		t.Fatalf("unexpected file result: %#v", file)
	}
}

func TestImportPathsCopiesExternalFileIntoInbox(t *testing.T) {
	rootDir := t.TempDir()
	externalDir := t.TempDir()
	input := filepath.Join(externalDir, "clip.mp4")
	if err := os.WriteFile(input, []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := workspace.Open(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ImportPaths(context.Background(), root, []string{input}, Options{Mode: "video"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 {
		t.Fatalf("want one file, got %d", len(result.Files))
	}
	file := result.Files[0]
	if !file.Imported || file.Kind != "video" {
		t.Fatalf("unexpected imported file: %#v", file)
	}
	if !strings.HasPrefix(file.Path, ".himind/media-toolkit/inbox/") {
		t.Fatalf("external file should be copied into inbox: %s", file.Path)
	}
	resolved, err := root.Resolve(file.Path)
	if err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(resolved); err != nil || string(content) != "video" {
		t.Fatalf("copied content mismatch: %q err=%v", content, err)
	}
}

func TestImportPathsRejectsWrongKind(t *testing.T) {
	rootDir := t.TempDir()
	input := filepath.Join(rootDir, "photo.png")
	if err := os.WriteFile(input, []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := workspace.Open(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ImportPaths(context.Background(), root, []string{input}, Options{Mode: "video"})
	if err == nil || !strings.Contains(err.Error(), "不适合当前任务") {
		t.Fatalf("expected media-kind rejection, got %v", err)
	}
}

func TestImportPathsRejectsMultipleForSingleSelection(t *testing.T) {
	rootDir := t.TempDir()
	root, err := workspace.Open(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	one := filepath.Join(rootDir, "one.png")
	two := filepath.Join(rootDir, "two.png")
	if err := os.WriteFile(one, []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(two, []byte("2"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = ImportPaths(context.Background(), root, []string{one, two}, Options{Mode: "image"})
	if err == nil || !strings.Contains(err.Error(), "只能选择一个") {
		t.Fatalf("expected single-selection rejection, got %v", err)
	}
}
