package releaseplan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildDerivesIconSidecarName(t *testing.T) {
	root := t.TempDir()
	manifest := `{
  "id": "com.example.icon",
  "name": "Icon demo",
  "author": "Tester",
  "version": "1.2.3",
  "release_notes": "first",
  "description": "demo",
  "icon": "assets/icon.webp"
}`
	if err := os.WriteFile(filepath.Join(root, "plugin.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := Build("plugin", root, "", "stable", "")
	if err != nil {
		t.Fatal(err)
	}
	if plan.IconName != "com.example.icon-1.2.3-icon.webp" {
		t.Fatalf("icon_name = %q", plan.IconName)
	}
}

func TestBuildRejectsIconOutsidePackage(t *testing.T) {
	root := t.TempDir()
	manifest := `{
  "id": "com.example.icon",
  "name": "Icon demo",
  "author": "Tester",
  "version": "1.2.3",
  "release_notes": "first",
  "description": "demo",
  "icon": "../icon.png"
}`
	if err := os.WriteFile(filepath.Join(root, "plugin.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Build("plugin", root, "", "stable", ""); err == nil || !strings.Contains(err.Error(), "包内相对路径") {
		t.Fatalf("expected package-relative icon error, got %v", err)
	}
}
