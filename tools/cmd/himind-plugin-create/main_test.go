package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MrBaoquan/himind-extensions/tooling/pluginproject"
)

func TestCreatePluginGeneratesRunnableContract(t *testing.T) {
	for _, template := range []string{"readonly-tool", "job-worker", "ui-tool"} {
		t.Run(template, func(t *testing.T) {
			output := t.TempDir()
			if _, err := pluginproject.Create(pluginproject.Config{Name: "example-tool", DisplayName: "示例工具", Description: "用于验证插件脚手架。", Author: "测试用户", Categories: []string{"software-engineering"}, ReleaseNotes: "新增示例插件。", Template: template, OutputDir: output}); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(output, "example-tool")
			data, err := os.ReadFile(filepath.Join(root, "plugin.json"))
			if err != nil {
				t.Fatal(err)
			}
			var item pluginproject.Manifest
			if err := json.Unmarshal(data, &item); err != nil {
				t.Fatal(err)
			}
			properties, _ := item.Capabilities[0].InputSchema["properties"].(map[string]any)
			if _, ok := properties["value"]; !ok {
				t.Fatal("generated schema must declare the value input used by the handler")
			}
			if item.Name != "示例工具" || item.Contributes.Commands[0].Title != "运行示例工具" {
				t.Fatalf("generated manifest must use the Chinese display name: %#v", item)
			}
			if _, err := os.Stat(filepath.Join(root, "internal", "himindjsonrpc", "jsonrpc.go")); err != nil {
				t.Fatal("generated project must contain a self-contained JSON-RPC runtime")
			}
			if _, err := os.Stat(filepath.Join(root, "main_test.go")); err != nil {
				t.Fatal("generated project must include a unit test")
			}
			if template == "ui-tool" {
				if len(item.Contributes.Views) != 1 {
					t.Fatalf("ui-tool must declare one view: %#v", item.Contributes.Views)
				}
				if _, err := os.Stat(filepath.Join(root, "ui", "index.html")); err != nil {
					t.Fatal("ui-tool must include ui/index.html")
				}
			}
		})
	}
}

func TestCreatePluginRequiresChineseDisplayName(t *testing.T) {
	if _, err := pluginproject.Create(pluginproject.Config{Name: "example-tool", Author: "测试用户", Categories: []string{"software-engineering"}, Description: "验证展示名称。", ReleaseNotes: "新增验证。", Template: "readonly-tool", OutputDir: t.TempDir()}); err == nil {
		t.Fatal("expected an explicit display name to be required")
	}
}
