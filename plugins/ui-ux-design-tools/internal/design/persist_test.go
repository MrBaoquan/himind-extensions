package design

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPersistDesignSystem(t *testing.T) {
	root := t.TempDir()
	c := newTestCatalog(t)
	first, err := c.PersistDesignSystem(PersistInput{WorkspaceRoot: root, ProjectSlug: "ops-console", Content: "# MASTER\n"})
	if err != nil {
		t.Fatal(err)
	}
	if first["status"] != "written" {
		t.Fatalf("unexpected status: %#v", first)
	}
	path := filepath.Join(root, "design-system", "ops-console", "MASTER.md")
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "# MASTER\n" {
		t.Fatalf("unexpected file: %q, %v", data, err)
	}
	second, err := c.PersistDesignSystem(PersistInput{WorkspaceRoot: root, ProjectSlug: "ops-console", Content: "# CHANGED\n"})
	if err != nil {
		t.Fatal(err)
	}
	if second["status"] != "exists" {
		t.Fatalf("expected exists, got %#v", second)
	}
	data, _ = os.ReadFile(path)
	if string(data) != "# MASTER\n" {
		t.Fatalf("existing file was overwritten: %q", data)
	}
	third, err := c.PersistDesignSystem(PersistInput{WorkspaceRoot: root, ProjectSlug: "ops-console", PageSlug: "overview", Content: "# Page\n", Overwrite: true})
	if err != nil {
		t.Fatal(err)
	}
	if third["status"] != "written" || third["path"] != "design-system/ops-console/pages/overview.md" {
		t.Fatalf("unexpected page write: %#v", third)
	}
}

func TestPersistRejectsTraversal(t *testing.T) {
	c := newTestCatalog(t)
	if _, err := c.PersistDesignSystem(PersistInput{WorkspaceRoot: t.TempDir(), ProjectSlug: "../escape", Content: "# x"}); err == nil {
		t.Fatal("traversal slug should fail")
	}
	if _, err := c.PersistDesignSystem(PersistInput{WorkspaceRoot: t.TempDir(), ProjectSlug: "safe", PageSlug: "../escape", Content: "# x"}); err == nil {
		t.Fatal("traversal page should fail")
	}
	if _, err := c.PersistDesignSystem(PersistInput{WorkspaceRoot: t.TempDir(), ProjectSlug: "safe", Content: strings.Repeat("x", 513<<10)}); err == nil {
		t.Fatal("oversized content should fail")
	}
}

func TestPersistRejectsSymlinkDirectory(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "design-system")); err != nil {
		t.Skip("symlink creation not permitted")
	}
	c := newTestCatalog(t)
	if _, err := c.PersistDesignSystem(PersistInput{WorkspaceRoot: root, ProjectSlug: "safe", Content: "# x"}); err == nil {
		t.Fatal("symlink parent should fail")
	}
}
