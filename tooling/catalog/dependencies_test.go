package catalog

import (
	"path/filepath"
	"strings"
	"testing"
)

const miniprogramToolsDownloadURL = "https://github.com/MrBaoquan/himind-extensions/releases/download/" +
	"plugin%2Fcom.himind.wechat-miniprogram-tools@0.3.13/com.himind.wechat-miniprogram-tools-0.3.13.hmpkg"

func pluginEntry(id, version, downloadURL string) map[string]interface{} {
	return map[string]interface{}{
		"plugin_id": id,
		"version":   version,
		"release_tag": "plugin/" + id + "@" + version,
		"sha256":      strings.Repeat("a", 64),
		"download_url": downloadURL,
	}
}

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

// 依赖由别的分发仓发布时，索引里解析到的 pin 必须指向依赖自己的发货仓：
// 写成本仓会让安装器去本仓找一个不存在的 Release。
func TestPinDependenciesPointsAtDependencyDistribution(t *testing.T) {
	official := New("mrbaoquan/himind-extensions", "stable", "public")
	official.FeaturePacks = []FeaturePack{}
	official.SetEntries("plugin", []map[string]interface{}{
		pluginEntry("com.himind.wechat-miniprogram-tools", "0.3.13", miniprogramToolsDownloadURL),
	})
	declared := []DeclaredDependency{{
		Kind: "plugin", ID: "com.himind.wechat-miniprogram-tools",
		Required: true, MinVersion: "0.3.13",
	}}
	pins, err := PinDependencies(declared, official, "MrBaoquan/himind-ext-projects")
	if err != nil {
		t.Fatalf("cross-distribution dependency should resolve: %v", err)
	}
	if len(pins) != 1 {
		t.Fatalf("got %d pins, want 1", len(pins))
	}
	pin := pins[0]
	if !pin.Pinned || pin.Version != "0.3.13" {
		t.Fatalf("unexpected pin: %+v", pin)
	}
	if pin.Source.Repository != "MrBaoquan/himind-extensions" {
		t.Fatalf("pin should point at the dependency distribution, got %q", pin.Source.Repository)
	}
	if pin.Source.Reference != "plugin/com.himind.wechat-miniprogram-tools@0.3.13" ||
		pin.Source.ArtifactURL != miniprogramToolsDownloadURL {
		t.Fatalf("unexpected pin source: %+v", pin.Source)
	}
}

// 合并索引：本仓索引里没有的依赖，从依赖仓索引里解析；路径不存在按空索引处理。
func TestLoadMergedResolvesDependencyFromAnotherDistribution(t *testing.T) {
	directory := t.TempDir()
	officialPath := filepath.Join(directory, "official.json")
	official := New("mrbaoquan/himind-extensions", "stable", "public")
	official.FeaturePacks = []FeaturePack{}
	official.SetEntries("plugin", []map[string]interface{}{
		pluginEntry("com.himind.wechat-miniprogram-tools", "0.3.13", miniprogramToolsDownloadURL),
	})
	if err := official.Save(officialPath); err != nil {
		t.Fatalf("save official catalog: %v", err)
	}
	ownPath := filepath.Join(directory, "own.json")
	own := New("mrbaoquan/himind-ext-projects", "stable", "public")
	own.FeaturePacks = []FeaturePack{}
	if err := own.Save(ownPath); err != nil {
		t.Fatalf("save own catalog: %v", err)
	}

	index, err := LoadMerged(ownPath, filepath.Join(directory, "missing.json"), officialPath)
	if err != nil {
		t.Fatalf("load merged: %v", err)
	}
	declared := []DeclaredDependency{{
		Kind: "plugin", ID: "com.himind.wechat-miniprogram-tools", Required: true,
	}}
	pins, err := PinDependencies(declared, index, "MrBaoquan/himind-ext-projects")
	if err != nil {
		t.Fatalf("merged index should resolve the dependency: %v", err)
	}
	if pins[0].Source.Repository != "MrBaoquan/himind-extensions" {
		t.Fatalf("unexpected pin source: %+v", pins[0].Source)
	}
}
