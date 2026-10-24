package design

import (
	"os"
	"testing"
)

func newTestCatalog(t *testing.T) *Catalog {
	t.Helper()
	return NewCatalog(os.DirFS("../.."))
}

func TestCatalogDescribe(t *testing.T) {
	c := newTestCatalog(t)
	result := c.Describe()
	if result["status"] != "ok" {
		t.Fatalf("unexpected status: %#v", result["status"])
	}
	if count, _ := result["stack_count"].(int); count != 22 {
		t.Fatalf("expected 22 stacks, got %#v", result["stack_count"])
	}
	domains, ok := result["domains"].([]map[string]any)
	if !ok || len(domains) < 10 {
		t.Fatalf("expected domain catalog, got %#v", result["domains"])
	}
}

func TestSearchScenarios(t *testing.T) {
	c := newTestCatalog(t)
	cases := []struct {
		name   string
		query  string
		domain string
		stack  string
	}{
		{name: "chinese dashboard", query: "企业深色数据密集仪表盘", domain: "style"},
		{name: "fintech product", query: "financial technology landing", domain: "product"},
		{name: "mobile healthcare", query: "mobile healthcare appointment", domain: "ux"},
		{name: "checkout ux", query: "ecommerce checkout accessibility error", domain: "ux"},
		{name: "realtime chart", query: "real-time monitoring", domain: "chart"},
		{name: "luxury typography", query: "luxury editorial", domain: "typography"},
		{name: "icon labels", query: "keyboard focus icon button", domain: "icons"},
		{name: "react performance", query: "rerender memo list", domain: "react"},
		{name: "nextjs stack", query: "suspense streaming bundle", stack: "nextjs"},
		{name: "wpf stack", query: "enterprise table virtualization", stack: "wpf"},
		{name: "fintech color", query: "trustworthy banking secure", domain: "color"},
		{name: "developer pricing", query: "developer tool pricing", domain: "landing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := c.Search(tc.query, tc.domain, tc.stack, 3)
			if err != nil {
				t.Fatal(err)
			}
			if result.Count == 0 || len(result.Matches) == 0 {
				t.Fatalf("expected matches, got %#v", result)
			}
			if result.Matches[0].Identity == "" {
				t.Fatalf("expected identity, got %#v", result.Matches[0])
			}
		})
	}
}

func TestSearchNoMatchIsExplicit(t *testing.T) {
	c := newTestCatalog(t)
	result, err := c.Search("zzzzzzqqqq", "ux", "", 3)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "no_match" || result.Count != 0 || len(result.Matches) != 0 {
		t.Fatalf("zero results must be explicit: %#v", result)
	}
}

func TestSearchValidation(t *testing.T) {
	c := newTestCatalog(t)
	if _, err := c.Search("", "ux", "", 3); err == nil {
		t.Fatal("empty query should fail")
	}
	if _, err := c.Search("focus", "ux", "react", 3); err == nil {
		t.Fatal("domain and stack together should fail")
	}
	if _, err := c.Search("focus", "unknown", "", 3); err == nil {
		t.Fatal("unknown domain should fail")
	}
}

func TestGenerateDesignSystem(t *testing.T) {
	c := newTestCatalog(t)
	result, err := c.Generate(GenerateInput{Query: "中文企业运维仪表盘", ProjectName: "运维台", Variance: 8, Motion: 6, Density: 8})
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "ok" {
		t.Fatalf("unexpected status: %#v", result["status"])
	}
	style := result["style"].(map[string]any)
	if style["identity"] == "" {
		t.Fatalf("missing style: %#v", style)
	}
	colors := result["colors"].(map[string]any)
	roles := colors["roles"].(map[string]string)
	if roles["primary"] == "" || roles["background"] == "" {
		t.Fatalf("missing color roles: %#v", roles)
	}
	if checks := colors["contrast_checks"].([]map[string]any); len(checks) == 0 {
		t.Fatal("expected contrast checks")
	}
	spacing := result["spacing"].(map[string]any)
	if spacing["label"] != "紧凑/仪表盘" {
		t.Fatalf("unexpected spacing: %#v", spacing)
	}
	cjk := result["cjk_fonts"].(map[string]any)
	if cjk["source"] != "himind-local-guidance" {
		t.Fatalf("missing cjk guidance: %#v", cjk)
	}
}

func TestContrastRatio(t *testing.T) {
	ratio, err := contrastRatio("#000000", "#FFFFFF")
	if err != nil {
		t.Fatal(err)
	}
	if ratio < 20.99 || ratio > 21.01 {
		t.Fatalf("unexpected contrast ratio: %f", ratio)
	}
}
