package pluginproject

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// 视图落点要与 Agent 的安装校验对齐。脚手架曾写出 "tools"，产物在 Agent 侧
// 直接被拒（unsupported plugin view location），而仓库侧校验当时也拦不住。
func TestUIToolScaffoldUsesAgentAcceptedViewLocation(t *testing.T) {
	created, err := Create(Config{
		Name:         "ui-demo",
		DisplayName:  "界面示例",
		Description:  "示例插件。",
		Author:       "测试用户",
		Categories:   []string{"software-engineering"},
		ReleaseNotes: "首次创建。",
		Template:     "ui-tool",
		OutputDir:    t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(created.Root, "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Contributes.Views) != 1 {
		t.Fatalf("ui-tool must contribute exactly one view: %+v", manifest.Contributes.Views)
	}
	if manifest.Contributes.Views[0].Location != "plugin_navigation" {
		t.Fatalf("view location must be plugin_navigation, got %q", manifest.Contributes.Views[0].Location)
	}
}

// 名字里带连字符时，脚手架会把能力 ID 派生成多段前缀（media-toolkit → media.toolkit.run）。
// 这种前缀必须显式声明 capability_namespace，否则内核注册时可能与别的插件撞名而静默失效。
// 脚手架以前**根本不写这个字段** —— 它亲手产出不合规的清单，使用者照抄就跟着违规。
func TestScaffoldDeclaresNamespaceForHyphenatedName(t *testing.T) {
	created, err := Create(Config{
		Name:         "media-toolkit",
		DisplayName:  "媒体工具",
		Description:  "示例插件。",
		Author:       "测试用户",
		Categories:   []string{"video-post"},
		ReleaseNotes: "首次创建。",
		Template:     "readonly-tool",
		OutputDir:    t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	data, _ := os.ReadFile(filepath.Join(created.Root, "plugin.json"))
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Capabilities[0].ID != "media.toolkit.run" {
		t.Fatalf("能力 ID 派生改变: %q", manifest.Capabilities[0].ID)
	}
	if manifest.CapabilityNamespace != "media.toolkit" {
		t.Fatalf("多段前缀必须声明命名空间，实际 %q", manifest.CapabilityNamespace)
	}
}

func TestScaffoldNormalizesUnderscoresAndRejectsInvalidGeneratedCapabilityIDs(t *testing.T) {
	created, err := Create(Config{
		Name:         "media_toolkit",
		DisplayName:  "媒体工具",
		Description:  "示例插件。",
		Author:       "测试用户",
		Categories:   []string{"video-post"},
		ReleaseNotes: "首次创建。",
		Template:     "readonly-tool",
		OutputDir:    t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	data, _ := os.ReadFile(filepath.Join(created.Root, "plugin.json"))
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if got := manifest.Capabilities[0].ID; got != "media.toolkit.run" {
		t.Fatalf("underscore should become an ADR 0119 hierarchy separator, got %q", got)
	}
	if manifest.CapabilityNamespace != "media.toolkit" {
		t.Fatalf("multi-segment generated namespace should be declared, got %q", manifest.CapabilityNamespace)
	}

	for _, name := range []string{"3d-tool", "multi-word-plugin-name-test-extra"} {
		_, err := Create(Config{
			Name:         name,
			DisplayName:  "插件工具",
			Description:  "示例插件。",
			Author:       "测试用户",
			Categories:   []string{"video-post"},
			ReleaseNotes: "首次创建。",
			Template:     "readonly-tool",
			OutputDir:    t.TempDir(),
		})
		if err == nil {
			t.Errorf("scaffold must reject %q because its generated capability ID violates ADR 0119", name)
		}
	}
}

// 单段前缀（首段即插件 slug）无需声明 —— 写进去反而是噪音。
func TestScaffoldOmitsNamespaceWhenSlugIsEnough(t *testing.T) {
	created, err := Create(Config{
		Name:         "iostudio",
		DisplayName:  "示例工具",
		Description:  "示例插件。",
		Author:       "测试用户",
		Categories:   []string{"system-device"},
		ReleaseNotes: "首次创建。",
		Template:     "readonly-tool",
		OutputDir:    t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	data, _ := os.ReadFile(filepath.Join(created.Root, "plugin.json"))
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.CapabilityNamespace != "" {
		t.Fatalf("首段即 slug 时不应声明命名空间，实际 %q", manifest.CapabilityNamespace)
	}
}

// 脚手架必须在产出后自校验：它一旦产出不合规的工程，使用者照抄就跟着违规。
func TestScaffoldRejectsBannedDisplayName(t *testing.T) {
	_, err := Create(Config{
		Name:         "smart-gadget",
		DisplayName:  "媒体智能", // 规范禁用词
		Description:  "示例插件。",
		Author:       "测试用户",
		Categories:   []string{"video-post"},
		ReleaseNotes: "首次创建。",
		Template:     "readonly-tool",
		OutputDir:    t.TempDir(),
	})
	if err == nil {
		t.Fatal("显示名含禁用词时脚手架应拒绝产出")
	}
}
