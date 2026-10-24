package design

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInspectProject(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "package.json", `{"dependencies":{"next":"16.0.0","react":"19.0.0"},"devDependencies":{"tailwindcss":"4.0.0","typescript":"5.0.0"}}`)
	writeFixture(t, root, "components.json", `{"style":"new-york"}`)
	writeFixture(t, root, "design-system/demo/MASTER.md", "# Demo")
	writeFixture(t, root, "src/styles/tokens.css", ":root { --color-background: #fff; }")
	writeFixture(t, root, "src/app/page.tsx", "export default function Page(){return null}")
	c := newTestCatalog(t)
	result, err := c.InspectProject(ProjectInspectInput{WorkspaceRoot: root, MaxDepth: 5, MaxFiles: 500})
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "ok" {
		t.Fatalf("unexpected status: %#v", result["status"])
	}
	detected := result["detected"].([]map[string]any)
	names := map[string]bool{}
	for _, item := range detected {
		names[item["name"].(string)] = true
	}
	for _, name := range []string{"Next.js", "React", "Tailwind CSS", "TypeScript", "shadcn/ui"} {
		if !names[name] {
			t.Fatalf("missing detected stack %s: %#v", name, detected)
		}
	}
	ds := result["design_system"].(map[string]any)
	if !ds["has_design_system"].(bool) {
		t.Fatalf("expected design system detection: %#v", ds)
	}
	if result["scan"].(map[string]any)["files_scanned"].(int) < 4 {
		t.Fatalf("expected scanned files: %#v", result["scan"])
	}
}

func TestInspectProjectValidation(t *testing.T) {
	c := newTestCatalog(t)
	if _, err := c.InspectProject(ProjectInspectInput{WorkspaceRoot: "relative/path"}); err == nil {
		t.Fatal("relative workspace_root should fail")
	}
	if _, err := c.InspectProject(ProjectInspectInput{WorkspaceRoot: t.TempDir(), MaxDepth: 20}); err == nil {
		t.Fatal("invalid max_depth should fail")
	}
}

func writeFixture(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
