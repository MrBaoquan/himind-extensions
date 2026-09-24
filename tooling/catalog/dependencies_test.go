package catalog

import (
	"strings"
	"testing"
)

func workflowManifestWithDependencies(skills, plugins interface{}) Manifest {
	return Manifest{
		ID: "com.example.workflow.sample", Name: "Sample Workflow", Version: "1.0.0",
		Dependencies: map[string]interface{}{"skills": skills, "plugins": plugins},
	}
}

// 工作流依赖的两种写法都要归一成同一条声明。
func TestDeclaredDependenciesAcceptStringAndObjectForms(t *testing.T) {
	manifest := workflowManifestWithDependencies(
		[]interface{}{
			"develop-himind-skills",
			map[string]interface{}{"skill_id": "wechatide-skill", "required": false, "min_version": "0.4.0"},
		},
		[]interface{}{"com.himind.wechat-miniprogram-tools"},
	)
	declared, err := DeclaredDependencies("workflow", manifest)
	if err != nil {
		t.Fatalf("declared dependencies: %v", err)
	}
	expected := []DeclaredDependency{
		{Kind: "skill", ID: "develop-himind-skills", Required: true},
		{Kind: "skill", ID: "wechatide-skill", Required: false, MinVersion: "0.4.0"},
		{Kind: "plugin", ID: "com.himind.wechat-miniprogram-tools", Required: true},
	}
	if len(declared) != len(expected) {
		t.Fatalf("got %d dependencies, want %d: %+v", len(declared), len(expected), declared)
	}
	for index, want := range expected {
		if declared[index] != want {
			t.Fatalf("dependency %d: got %+v, want %+v", index, declared[index], want)
		}
	}
}

func TestDeclaredDependenciesRejectInvalidShape(t *testing.T) {
	cases := []struct {
		name      string
		skills    interface{}
		expectSub string
	}{
		{"not an array", "wechatide-skill", "必须是数组"},
		{"object without id", []interface{}{map[string]interface{}{"required": false}}, "缺少 skill_id"},
		{"non-boolean required", []interface{}{map[string]interface{}{"skill_id": "a", "required": "no"}}, "required 不是布尔值"},
		{"unsupported entry", []interface{}{42}, "必须是字符串或对象"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			manifest := workflowManifestWithDependencies(testCase.skills, nil)
			_, err := DeclaredDependencies("workflow", manifest)
			if err == nil || !strings.Contains(err.Error(), testCase.expectSub) {
				t.Fatalf("want error containing %q, got %v", testCase.expectSub, err)
			}
		})
	}
}

// 跨分发依赖（这里解析不到规范发布）允许发布，pin 保留最低版本但不定位制品；
// 本分发内的必需依赖仍然阻断，避免发出一个装不上的版本。
func TestPinDependenciesKeepsExternalDependencyUnpinned(t *testing.T) {
	manifest := workflowManifestWithDependencies(
		[]interface{}{
			map[string]interface{}{"skill_id": "wechatide-skill", "required": false, "min_version": "0.4.0"},
		},
		[]interface{}{"com.himind.wechat-miniprogram-tools"},
	)
	declared, err := DeclaredDependencies("workflow", manifest)
	if err != nil {
		t.Fatalf("declared dependencies: %v", err)
	}
	_, err = PinDependencies(declared, New("", "", ""), "MrBaoquan/himind-extensions")
	if err == nil || !strings.Contains(err.Error(), "com.himind.wechat-miniprogram-tools") {
		t.Fatalf("required dependency should block publish, got %v", err)
	}

	optional, err := PinDependencies(declared[:1], New("", "", ""), "MrBaoquan/himind-extensions")
	if err != nil {
		t.Fatalf("optional dependency should not block publish: %v", err)
	}
	if len(optional) != 1 || optional[0].Pinned || optional[0].MinVersion != "0.4.0" || optional[0].Version != "0.4.0" {
		t.Fatalf("unexpected pin: %+v", optional)
	}
}
