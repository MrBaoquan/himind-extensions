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
