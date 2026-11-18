package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBuiltinStylesAreConsistent(t *testing.T) {
	seen := map[string]bool{}
	for _, style := range builtinStyles() {
		if !styleIDPattern(style.ID) {
			t.Fatalf("内置风格包 ID 不合规: %q", style.ID)
		}
		if seen[style.ID] {
			t.Fatalf("内置风格包 ID 重复: %s", style.ID)
		}
		seen[style.ID] = true
		if style.Label == "" || style.PromptSuffix == "" {
			t.Fatalf("内置风格包 %s 缺少 label 或 prompt_suffix", style.ID)
		}
		if len(style.Sizes) == 0 {
			t.Fatalf("内置风格包 %s 缺少默认尺寸", style.ID)
		}
		for _, size := range style.Sizes {
			if normalized := normalizeSizes([]string{size}); len(normalized) != 1 {
				t.Fatalf("内置风格包 %s 的尺寸非法: %s", style.ID, size)
			}
		}
	}
}

func TestNormalizeStyleRejectsInvalidInput(t *testing.T) {
	cases := []Style{
		{ID: "Bad_ID", Label: "x", PromptSuffix: "y"},
		{ID: "-bad", Label: "x", PromptSuffix: "y"},
		{ID: "good", Label: "", PromptSuffix: "y"},
		{ID: "good", Label: "x", PromptSuffix: ""},
	}
	for _, input := range cases {
		if _, err := normalizeStyle(input); err == nil {
			t.Fatalf("应当拒绝非法风格包: %+v", input)
		}
	}
}

func TestStyleOverrideMergesWithBuiltin(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	styles, err := allStyles()
	if err != nil {
		t.Fatalf("读取风格包失败: %v", err)
	}
	original := len(styles)

	override, err := normalizeStyle(Style{
		ID:           "clean-product",
		Label:        "干净产品图（自定义）",
		PromptSuffix: "纯白背景、柔和阴影",
		Sizes:        []string{"1024x1024", "not-a-size"},
	})
	if err != nil {
		t.Fatalf("归一化失败: %v", err)
	}
	if len(override.Sizes) != 1 || override.Sizes[0] != "1024x1024" {
		t.Fatalf("非法尺寸应被过滤: %+v", override.Sizes)
	}
	if err := saveCustomStyles([]Style{override}); err != nil {
		t.Fatalf("保存风格包失败: %v", err)
	}

	merged, err := allStyles()
	if err != nil {
		t.Fatalf("合并风格包失败: %v", err)
	}
	if len(merged) != original {
		t.Fatalf("覆盖同名风格包不应改变总数：期望 %d，实际 %d", original, len(merged))
	}
	found, ok, err := findStyle("clean-product")
	if err != nil || !ok {
		t.Fatalf("查找风格包失败: ok=%v err=%v", ok, err)
	}
	if found.Label != "干净产品图（自定义）" || found.Builtin {
		t.Fatalf("自定义包应覆盖内置包且不带内置标记: %+v", found)
	}
}

func TestComposePromptOrdersBaseStyleAndAvoid(t *testing.T) {
	style := Style{PromptSuffix: "水墨晕染", Avoid: "写实照片"}
	got := composePrompt("  一座桥  ", style)
	want := "一座桥。风格要求：水墨晕染。不要出现：写实照片"
	if got != want {
		t.Fatalf("提示词拼装顺序不对:\n期望 %q\n实际 %q", want, got)
	}
	if composePrompt("", Style{}) != "" {
		t.Fatal("空描述与空风格应拼出空提示词，交给上层拒绝")
	}
}

func TestStyleStoreFileIsReadableJSON(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", root)
	if err := saveCustomStyles([]Style{{ID: "a", Label: "A", PromptSuffix: "x"}}); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(root, styleStoreFile))
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(content, &payload); err != nil {
		t.Fatalf("风格包文件不是合法 JSON: %v", err)
	}
	if payload["schema_version"] != float64(1) {
		t.Fatalf("缺少 schema_version: %+v", payload)
	}
}
