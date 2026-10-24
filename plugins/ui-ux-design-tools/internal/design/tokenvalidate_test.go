package design

import (
	"testing"
)

func TestValidateTokensGood(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "src/styles.css", ":root{--color-foreground:#111827;--color-background:#ffffff;--color-primary:#1e40af;--color-on-primary:#ffffff;}")
	c := newTestCatalog(t)
	result, err := c.ValidateTokens(TokenValidateInput{WorkspaceRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "ok" {
		t.Fatalf("unexpected status: %#v", result)
	}
	if result["token_count"].(int) < 4 {
		t.Fatalf("expected tokens: %#v", result["tokens"])
	}
	checks := result["contrast_checks"].([]map[string]any)
	if len(checks) == 0 {
		t.Fatal("expected contrast checks")
	}
	for _, check := range checks {
		if !check["passes_aa_normal"].(bool) {
			t.Fatalf("unexpected contrast failure: %#v", check)
		}
	}
}

func TestValidateTokensFindsIssues(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "styles.css", ":root{--color-foreground:#777777;--color-background:#ffffff;}\n.button{color:#ff00ff;background:#fff;}")
	c := newTestCatalog(t)
	result, err := c.ValidateTokens(TokenValidateInput{WorkspaceRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "issues" {
		t.Fatalf("expected issues status: %#v", result)
	}
	if len(result["hardcoded_colors"].([]map[string]any)) == 0 {
		t.Fatal("expected hardcoded colors")
	}
	if len(result["contrast_checks"].([]map[string]any)) == 0 {
		t.Fatal("expected contrast check")
	}
}

func TestValidateTokensJSON(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "tokens.json", `{"colors":{"foreground":"#111827","background":"#ffffff"}}`)
	c := newTestCatalog(t)
	result, err := c.ValidateTokens(TokenValidateInput{WorkspaceRoot: root, Paths: []string{"tokens.json"}})
	if err != nil {
		t.Fatal(err)
	}
	if result["token_count"].(int) != 2 {
		t.Fatalf("unexpected tokens: %#v", result["tokens"])
	}
	if len(result["contrast_checks"].([]map[string]any)) != 1 {
		t.Fatalf("expected one contrast check: %#v", result["contrast_checks"])
	}
}

func TestValidateTokensRejectsTraversal(t *testing.T) {
	c := newTestCatalog(t)
	if _, err := c.ValidateTokens(TokenValidateInput{WorkspaceRoot: t.TempDir(), Paths: []string{"../escape.css"}}); err == nil {
		t.Fatal("traversal path should fail")
	}
}

func TestValidateTokensNoFiles(t *testing.T) {
	c := newTestCatalog(t)
	result, err := c.ValidateTokens(TokenValidateInput{WorkspaceRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "no_files" {
		t.Fatalf("expected no_files: %#v", result)
	}
}
