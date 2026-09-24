package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/MrBaoquan/himind-extensions/sdk/jsonrpc"
	"github.com/MrBaoquan/himind-extensions/tooling/pluginproject"
	"github.com/MrBaoquan/himind-extensions/tooling/workflowproject"
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

func TestWorkflowInBlankWorkspace(t *testing.T) {
	workspace := t.TempDir()
	outputDir := filepath.Join(workspace, "workflows")
	result := invoke(t, "extension.workflow.scaffold", map[string]any{
		"workspace_root":    workspace,
		"output_dir":        outputDir,
		"slug":              "blank-workflow",
		"id":                "com.example.workflow.blank",
		"name":              "空白目录工作流",
		"description":       "验证工作流开发流程。",
		"author":            "测试用户",
		"version":           "0.1.0",
		"min_agent_version": "0.3.47",
		"release_notes":     "新增空白目录工作流开发流程。",
		"template":          "segmented",
	}).(workflowproject.Result)
	root := result.Root
	invoke(t, "extension.workflow.validate", map[string]any{
		"workspace_root": workspace,
		"path":           root,
	})
	invoke(t, "extension.workflow.build", map[string]any{
		"workspace_root": workspace,
		"path":           root,
	})
	packagePath := filepath.Join(workspace, "dist", "blank-workflow.hmwf")
	invoke(t, "extension.workflow.package", map[string]any{
		"workspace_root": workspace,
		"path":           root,
		"output":         packagePath,
	})
	invoke(t, "extension.workflow.validate", map[string]any{
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

func TestWorkflowPreflightDoesNotRequireGo(t *testing.T) {
	result := invoke(t, "extension.environment.preflight", map[string]any{"kind": "workflow"}).(map[string]any)
	if result["ready"] != true {
		t.Fatalf("workflow preflight should be ready without external toolchains: %#v", result)
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

func failureMessage(t *testing.T, method string, params any) string {
	t.Helper()
	request, err := jsonrpc.NewRequest(1, method, params)
	if err != nil {
		t.Fatal(err)
	}
	_, rpcError := handle(request)
	if rpcError == nil {
		t.Fatalf("%s should fail", method)
	}
	return rpcError.Message
}

// TestScaffoldRejectsOverlongMetadata 锁定元数据长度约束：名称、说明和更新说明超限时脚手架必须拒绝。
func TestScaffoldRejectsOverlongMetadata(t *testing.T) {
	workspace := t.TempDir()
	outputDir := filepath.Join(workspace, "out")
	longName := strings.Repeat("超长名称", 6)
	longDescription := strings.Repeat("用途说明", 31)
	longNotes := strings.Repeat("更新说明", 31)
	brief := "验证元数据长度约束。"
	skillParams := func(name, description, notes string) map[string]any {
		return map[string]any{
			"workspace_root": workspace,
			"output_dir":     outputDir,
			"slug":           "constraint-check",
			"name":           name,
			"description":    description,
			"author":         "测试用户",
			"categories":     []string{"software-engineering"},
			"release_notes":  notes,
		}
	}
	pluginParams := func(displayName, notes string) map[string]any {
		return map[string]any{
			"workspace_root": workspace,
			"output_dir":     outputDir,
			"name":           "constraint-check",
			"display_name":   displayName,
			"description":    brief,
			"author":         "测试用户",
			"categories":     []string{"software-engineering"},
			"release_notes":  notes,
			"template":       "readonly-tool",
		}
	}
	workflowParams := func(name string) map[string]any {
		return map[string]any{
			"workspace_root": workspace,
			"output_dir":     outputDir,
			"slug":           "constraint-check",
			"id":             "com.example.workflow.constraint-check",
			"name":           name,
			"description":    brief,
			"author":         "测试用户",
			"release_notes":  brief,
			"template":       "strict",
		}
	}
	cases := []struct {
		method string
		params map[string]any
		expect string
	}{
		{"extension.skill.scaffold", skillParams(longName, brief, brief), "name is too long"},
		{"extension.skill.scaffold", skillParams("长度约束校验", longDescription, brief), "description is too long"},
		{"extension.skill.scaffold", skillParams("长度约束校验", brief, longNotes), "release_notes is too long"},
		{"extension.plugin.scaffold", pluginParams(longName, brief), "name is too long"},
		{"extension.plugin.scaffold", pluginParams("长度约束校验", longNotes), "release_notes is too long"},
		{"extension.workflow.scaffold", workflowParams(longName), "name is too long"},
	}
	for _, item := range cases {
		if message := failureMessage(t, item.method, item.params); !strings.Contains(message, item.expect) {
			t.Fatalf("%s should reject %s, got %q", item.method, item.expect, message)
		}
	}
}
