package design

import (
	"strings"
	"testing"
)

func TestCJKGuidanceSelection(t *testing.T) {
	c := newTestCatalog(t)
	technical := c.CJKGuidance("中文企业运维仪表盘")["selected"].(CJKFontPair)
	if technical.ID != "technical-ui" {
		t.Fatalf("expected technical-ui, got %#v", technical)
	}
	editorial := c.CJKGuidance("中文文化杂志编辑页")["selected"].(CJKFontPair)
	if editorial.ID != "editorial" {
		t.Fatalf("expected editorial, got %#v", editorial)
	}
}

func TestExpandQueryIsDeterministic(t *testing.T) {
	first := ExpandQuery("中文数据表格与状态反馈")
	second := ExpandQuery("中文数据表格与状态反馈")
	if first != second || !strings.Contains(first, "cjk") || !strings.Contains(first, "data table") {
		t.Fatalf("unexpected expansion: %q / %q", first, second)
	}
}

func TestExpandIndustrialDesktopQuery(t *testing.T) {
	expanded := ExpandQuery("Avalonia 工业设备调试工作台，高信息密度，键盘优先")
	for _, term := range []string{"industrial equipment", "developer tool", "desktop", "data dense"} {
		if !strings.Contains(expanded, term) {
			t.Fatalf("missing %q in expansion: %q", term, expanded)
		}
	}
}
