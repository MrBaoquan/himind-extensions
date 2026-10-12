package expertproject

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validManifest = `{
  "schema_version": "expert.v1",
  "id": "com.himind.expert.software-engineer",
  "name": "软件工程师",
  "author": "HiMind",
  "categories": ["software-engineering"],
  "version": "1.0.0",
  "release_notes": "首个版本。",
  "instructions": "先理解现状，再修改、验证并交付可复现结果。",
  "contents": ["EXPERT.md"]
}`

func writeExpert(t *testing.T, manifest string, withDocument bool) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "expert.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if withDocument {
		if err := os.WriteFile(filepath.Join(root, "EXPERT.md"), []byte("正文"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func replaceOnce(source, old, replacement string) string {
	if !strings.Contains(source, old) {
		panic("测试夹具缺少待替换片段: " + old)
	}
	return strings.Replace(source, old, replacement, 1)
}

func TestValidateAcceptsConformingExpert(t *testing.T) {
	if err := Validate(writeExpert(t, validManifest, true)); err != nil {
		t.Fatalf("合规专家应通过: %v", err)
	}
}

func TestValidateRejectsDrift(t *testing.T) {
	cases := []struct {
		name         string
		manifest     string
		withDocument bool
	}{
		{
			name:         "schema 版本不支持",
			manifest:     replaceOnce(validManifest, "expert.v1", "expert.v2"),
			withDocument: true,
		},
		{
			name:         "缺少正文文件",
			manifest:     validManifest,
			withDocument: false,
		},
		{
			name:         "contents 多声明",
			manifest:     replaceOnce(validManifest, `["EXPERT.md"]`, `["EXPERT.md","NOTES.md"]`),
			withDocument: true,
		},
		{
			name:         "instructions 为空",
			manifest:     replaceOnce(validManifest, "先理解现状，再修改、验证并交付可复现结果。", ""),
			withDocument: true,
		},
		{
			name:         "id 含空格",
			manifest:     replaceOnce(validManifest, "com.himind.expert.software-engineer", "com.himind.expert.software engineer"),
			withDocument: true,
		},
		{
			name:         "version 含非法字符",
			manifest:     replaceOnce(validManifest, `"version": "1.0.0"`, `"version": "1.0.0 beta"`),
			withDocument: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := Validate(writeExpert(t, tc.manifest, tc.withDocument)); err == nil {
				t.Fatal("应判定为不合规")
			}
		})
	}
}
