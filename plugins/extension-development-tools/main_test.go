package main

import (
	"path/filepath"
	"testing"

	"github.com/MrBaoquan/himind-extensions/sdk/jsonrpc"
	"github.com/MrBaoquan/himind-extensions/tooling/pluginproject"
)

func invoke(t *testing.T, method string, params any) any {
	t.Helper()
	request, err := jsonrpc.NewRequest(1, method, params)
	if err != nil {
		t.Fatal(err)
	}
	result, rpcError := handle(request)
	if rpcError != nil {
		t.Fatalf("%s failed: %s", method, rpcError.Message)
	}
	return result
}

func TestSkillWorkflowInBlankWorkspace(t *testing.T) {
	workspace := t.TempDir()
	outputDir := filepath.Join(workspace, "skills")
	result := invoke(t, "extension.skill.scaffold", map[string]any{
		"workspace_root": workspace,
		"output_dir":     outputDir,
		"slug":           "blank-skill",
		"name":           "空白目录技能",
		"description":    "验证技能开发流程。",
		"author":         "测试用户",
		"categories":     []string{"software-engineering"},
		"release_notes":  "新增空白目录技能开发流程。",
	})
	root := result.(map[string]any)["root"].(string)
	invoke(t, "extension.skill.validate", map[string]any{
		"workspace_root": workspace,
		"path":           root,
	})
	packagePath := filepath.Join(workspace, "dist", "blank-skill.hmskill")
	invoke(t, "extension.skill.package", map[string]any{
		"workspace_root": workspace,
		"path":           root,
		"output":         packagePath,
	})
	invoke(t, "extension.skill.validate", map[string]any{
		"workspace_root": workspace,
		"path":           packagePath,
	})
}

func TestPluginWorkflowInBlankWorkspace(t *testing.T) {
	workspace := t.TempDir()
	outputDir := filepath.Join(workspace, "plugins")
	result := invoke(t, "extension.plugin.scaffold", map[string]any{
		"workspace_root": workspace,
		"output_dir":     outputDir,
		"name":           "blank-plugin",
		"display_name":   "空白目录插件",
		"description":    "验证插件开发流程。",
		"author":         "测试用户",
		"categories":     []string{"software-engineering"},
		"release_notes":  "新增空白目录插件开发流程。",
		"template":       "readonly-tool",
	})
	root := result.(pluginproject.Result).Root
	invoke(t, "extension.plugin.validate", map[string]any{
		"workspace_root": workspace,
		"path":           root,
	})
	invoke(t, "extension.plugin.build", map[string]any{
		"workspace_root": workspace,
		"path":           root,
	})
	packagePath := filepath.Join(workspace, "dist", "blank-plugin.hmpkg")
	invoke(t, "extension.plugin.package", map[string]any{
		"workspace_root": workspace,
		"path":           root,
		"output":         packagePath,
	})
	invoke(t, "extension.plugin.validate", map[string]any{
		"workspace_root": workspace,
		"path":           packagePath,
	})
}

func TestSkillPreflightDoesNotRequireGo(t *testing.T) {
	result := invoke(t, "extension.environment.preflight", map[string]any{"kind": "skill"}).(map[string]any)
	if result["ready"] != true {
		t.Fatalf("skill preflight should be ready without external toolchains: %#v", result)
	}
}

func TestRejectsPathsOutsideWorkspace(t *testing.T) {
	workspace := t.TempDir()
	request, err := jsonrpc.NewRequest(1, "extension.skill.validate", map[string]any{
		"workspace_root": workspace,
		"path":           filepath.Join(workspace, "..", "outside"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, rpcError := handle(request); rpcError == nil {
		t.Fatal("expected path outside workspace to be rejected")
	}
}
