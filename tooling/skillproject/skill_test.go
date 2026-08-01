package skillproject

import (
	"archive/zip"
	"os"
	"path/filepath"
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
	if len(manifest.SupportedClients) != 3 ||
		manifest.SupportedClients[1] != "github-copilot" ||
		manifest.SupportedClients[2] != "workbuddy" {
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
