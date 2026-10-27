package skillproject

import (
	"archive/zip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCreateAndPackageSkillInBlankDirectory(t *testing.T) {
	workspace := t.TempDir()
	root, err := Create(CreateConfig{
		Slug:         "blank-skill",
		Name:         "空白目录技能",
		Description:  "在空白目录验证技能开发工具。",
		Author:       "测试用户",
		Categories:   []string{"software-engineering"},
		ReleaseNotes: "新增 Skill 创建与打包流程。",
		OutputDir:    workspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, _, err := readDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	expectedClients := []string{"himind-ai", "codex", "github-copilot", "workbuddy"}
	if !slices.Equal(manifest.SupportedClients, expectedClients) {
		t.Fatalf("unexpected clients: %#v", manifest.SupportedClients)
	}
	if _, err := os.Stat(filepath.Join(root, "agents", "openai.yaml")); err != nil {
		t.Fatal("scaffold must include agents/openai.yaml", err)
	}
	packagePath := filepath.Join(workspace, "dist", "blank-skill.hmskill")
	if err := Package(root, packagePath); err != nil {
		t.Fatal(err)
	}
	if err := Validate(packagePath); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsMissingDeclaredFile(t *testing.T) {
	workspace := t.TempDir()
	root, err := Create(CreateConfig{
		Slug: "missing-file", Name: "缺失文件", Description: "验证缺失文件。", Author: "测试用户", Categories: []string{"testing-quality"}, ReleaseNotes: "新增缺失文件校验。", OutputDir: workspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "agents", "openai.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := Validate(root); err == nil {
		t.Fatal("expected missing declared file to fail validation")
	}
}

func TestValidateRejectsArchiveTraversal(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "unsafe.hmskill")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	entry, err := writer.Create("../escape.txt")
	if err == nil {
		_, err = entry.Write([]byte("unsafe"))
	}
	if closeErr := writer.Close(); err == nil {
		err = closeErr
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(archivePath); err == nil {
		t.Fatal("expected traversal path to fail validation")
	}
}

// 正文引用了但清单没声明的能力 —— 实测踩过：我自己的技能引用了
// iostudio.motion.validate 却漏声明，校验/打包/候选测试全绿。
func TestValidateRejectsUndeclaredCapabilityReference(t *testing.T) {
	files := map[string][]byte{
		"skill.json": []byte(`{
			"id":"com.himind.skill.demo","name":"示例助手","author":"测试用户",
			"categories":["software-engineering"],"version":"1.0.0","scope":"organization",
			"description":"示例。","release_notes":"首次。","min_agent_version":"0.3.47",
			"distribution_targets":["workbench"],
			"supported_clients":["agent-skills"],
			"capabilities":[{"id":"media.toolkit.probe","provider":"com.himind.media-toolkit"}],
			"plugin_dependencies":[{"plugin_id":"com.himind.media-toolkit","required":true,"min_version":"1.0.0"}],
			"contents":["skill.json","SKILL.md"]
		}`),
		"SKILL.md": []byte("# 示例助手\n\n先跑 `media.toolkit.probe`，再用 `media.toolkit.scene.score` 看图。\n"),
	}
	manifest, err := parseManifest(files["skill.json"])
	if err != nil {
		t.Fatal(err)
	}
	if err := validateCapabilityReferences(manifest, files); err == nil {
		t.Fatal("引用了未声明的 media.toolkit.scene.score 应被拒绝")
	}
	// 声明之后应当通过
	manifest.Capabilities = append(manifest.Capabilities, CapabilityDependency{
		ID: "media.toolkit.scene.score", Provider: "com.himind.media-toolkit"})
	if err := validateCapabilityReferences(manifest, files); err != nil {
		t.Fatalf("补齐声明后应通过: %v", err)
	}
}

// 能力 ID 形态与 provider：技能侧此前完全没有对应检查。
func TestValidateCapabilityShape(t *testing.T) {
	bad := []CapabilityDependency{
		{ID: "", Provider: "com.himind.x"},
		{ID: "Media.Toolkit.Probe", Provider: "com.himind.x"}, // 大写
		{ID: "media_toolkit_probe", Provider: "com.himind.x"}, // 下划线
		{ID: "probe", Provider: "com.himind.x"},               // 只有一段
		{ID: "media.toolkit.probe"},                           // 缺 provider
	}
	for _, capability := range bad {
		if err := validateCapabilityShape(Manifest{Capabilities: []CapabilityDependency{capability}}); err == nil {
			t.Errorf("%+v 应被拒绝", capability)
		}
	}
	good := CapabilityDependency{ID: "media.toolkit.frames.contact-sheet", Provider: "com.himind.media-toolkit"}
	if err := validateCapabilityShape(Manifest{Capabilities: []CapabilityDependency{good}}); err != nil {
		t.Errorf("合规能力应通过: %v", err)
	}
}

func TestParseManifestRejectsPluginDependencyWithLegacyIDField(t *testing.T) {
	_, err := parseManifest([]byte(`{
		"id":"com.himind.skill.demo","name":"示例助手","author":"测试用户",
		"categories":["software-engineering"],"version":"1.0.0","scope":"organization",
		"description":"示例。","release_notes":"首次。","min_agent_version":"0.3.47",
		"distribution_targets":["workbench"],
		"supported_clients":["agent-skills"],
		"capabilities":[],
		"plugin_dependencies":[{"id":"com.himind.media-toolkit","required":true,"min_version":"1.0.0"}],
		"contents":["skill.json","SKILL.md"]
	}`))
	if err == nil || !strings.Contains(err.Error(), "plugin_id is required") {
		t.Fatalf("legacy id field must be rejected, got: %v", err)
	}
}
