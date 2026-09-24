package workflowproject

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// 依赖列表允许两种写法：字符串是「本分发内的必需依赖」，对象可以补 required 与
// min_version。两种写法都要能解析，且只声明 ID 的写法不被扩写成对象。
func TestDependenciesAcceptStringAndObjectForms(t *testing.T) {
	raw := `{
		"skills": ["develop-himind-skills", {"skill_id": "wechatide-skill", "required": false, "min_version": "0.4.0"}],
		"plugins": [{"plugin_id": "com.himind.wechat-miniprogram-tools"}],
		"connectors": ["wechat-miniprogram"],
		"runtimes": ["himind.builtin"]
	}`
	var dependencies Dependencies
	if err := json.Unmarshal([]byte(raw), &dependencies); err != nil {
		t.Fatalf("unmarshal dependencies: %v", err)
	}
	expected := []DependencyRef{
		{ID: "develop-himind-skills", Required: true},
		{ID: "wechatide-skill", Required: false, MinVersion: "0.4.0"},
	}
	if len(dependencies.Skills) != len(expected) {
		t.Fatalf("skills: got %d entries, want %d", len(dependencies.Skills), len(expected))
	}
	for index, want := range expected {
		if dependencies.Skills[index] != want {
			t.Fatalf("skill %d: got %+v, want %+v", index, dependencies.Skills[index], want)
		}
	}
	// 对象里只写 ID 时按必需依赖处理，不写版本下限。
	if dependencies.Plugins[0] != (DependencyRef{ID: "com.himind.wechat-miniprogram-tools", Required: true}) {
		t.Fatalf("plugin dependency: %+v", dependencies.Plugins[0])
	}

	encoded, err := json.Marshal(dependencies)
	if err != nil {
		t.Fatalf("marshal dependencies: %v", err)
	}
	text := string(encoded)
	if !containsText(text, `"skills":["develop-himind-skills",{"id":"wechatide-skill","required":false,"min_version":"0.4.0"}]`) {
		t.Fatalf("skills should keep the simple form as a string: %s", text)
	}
	if !containsText(text, `"plugins":["com.himind.wechat-miniprogram-tools"]`) {
		t.Fatalf("plugin without extra fields should stay a string: %s", text)
	}
}

// 依赖 ID 会被拼进 tag 与制品名，非法字符必须在清单校验阶段拦下。
func TestValidateRejectsInvalidDependencyRefs(t *testing.T) {
	_, result := createWorkflow(t, "strict")
	manifestPath := result.Root + "/workflow.json"
	original, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name      string
		skills    string
		expectSub string
	}{
		{"empty id", `[{"skill_id": ""}]`, "must declare an id"},
		{"duplicate id", `["a-skill", "a-skill"]`, "duplicate workflow skill dependency"},
		{"invalid id", `["../escape"]`, "invalid workflow skill dependency id"},
		{"invalid min_version", `[{"skill_id": "a-skill", "min_version": "1.0"}]`, "invalid workflow skill dependency min_version"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			restore := replaceDependencies(string(original), testCase.skills)
			if err := os.WriteFile(manifestPath, []byte(restore), 0o644); err != nil {
				t.Fatal(err)
			}
			err := Validate(result.Root)
			if err == nil || !containsText(err.Error(), testCase.expectSub) {
				t.Fatalf("want error containing %q, got %v", testCase.expectSub, err)
			}
		})
	}
	if err := os.WriteFile(manifestPath, original, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Validate(result.Root); err != nil {
		t.Fatalf("validate restored workflow: %v", err)
	}
}

func containsText(value, expected string) bool {
	return strings.Contains(value, expected)
}

// replaceDependencies 只替换清单里的 skills 数组，其余字段保持原样。
func replaceDependencies(manifest, skills string) string {
	const marker = `"skills": [`
	start := strings.Index(manifest, marker)
	if start < 0 {
		return manifest
	}
	end := strings.Index(manifest[start:], "]")
	if end < 0 {
		return manifest
	}
	return manifest[:start+len(marker)] + skills[1:len(skills)-1] + manifest[start+end:]
}
