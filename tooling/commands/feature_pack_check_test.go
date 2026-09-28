package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	catalogtool "github.com/MrBaoquan/himind-extensions/tooling/catalog"
)

// writeCatalog 把一份能力包写进临时仓的索引，供门禁测试使用。
func writeCatalog(t *testing.T, root string, packs []catalogtool.FeaturePack) {
	t.Helper()
	value := catalogtool.New("mrbaoquan/demo", "stable", "public")
	value.FeaturePacks = packs
	path := filepath.Join(root, filepath.FromSlash(catalogPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir catalog dir: %v", err)
	}
	if err := value.Save(path); err != nil {
		t.Fatalf("save catalog: %v", err)
	}
}

func TestValidateFeaturePacksRejectsForeignExtension(t *testing.T) {
	root := t.TempDir()
	pack := catalogtool.FeaturePack{
		ID:        "com.himind.feature.demo",
		Name:      "演示能力包",
		SkillIDs:  []string{"com.himind.skill.hosted"},
		PluginIDs: []string{"com.himind.plugin.elsewhere"},
	}
	writeCatalog(t, root, []catalogtool.FeaturePack{pack})
	kinds := map[string]string{"com.himind.skill.hosted": "skill"}
	err := validateFeaturePacks(root, []catalogtool.FeaturePack{pack}, kinds)
	if err == nil || !strings.Contains(err.Error(), "不在本仓") {
		t.Fatalf("expected foreign reference to be rejected, got %v", err)
	}
}

func TestValidateFeaturePacksRejectsCatalogDrift(t *testing.T) {
	root := t.TempDir()
	pack := catalogtool.FeaturePack{
		ID:       "com.himind.feature.demo",
		Name:     "演示能力包",
		SkillIDs: []string{"com.himind.skill.hosted"},
	}
	// 索引里带着官方仓的能力包：全量重建时不按本仓声明落盘就会是这种漂移。
	writeCatalog(t, root, []catalogtool.FeaturePack{pack, {
		ID:       "com.himind.feature.extension-authoring",
		Name:     "扩展创作",
		SkillIDs: []string{"com.himind.skill.hosted"},
	}})
	kinds := map[string]string{"com.himind.skill.hosted": "skill"}
	err := validateFeaturePacks(root, []catalogtool.FeaturePack{pack}, kinds)
	if err == nil || !strings.Contains(err.Error(), "不一致") {
		t.Fatalf("expected catalog drift to be rejected, got %v", err)
	}
}

func TestValidateFeaturePacksAcceptsHostedPack(t *testing.T) {
	root := t.TempDir()
	pack := catalogtool.FeaturePack{
		ID:        "com.himind.feature.demo",
		Name:      "演示能力包",
		PluginIDs: []string{"com.himind.plugin.hosted"},
		SkillIDs:  []string{"com.himind.skill.hosted"},
	}
	writeCatalog(t, root, []catalogtool.FeaturePack{pack})
	kinds := map[string]string{
		"com.himind.plugin.hosted": "plugin",
		"com.himind.skill.hosted":  "skill",
	}
	if err := validateFeaturePacks(root, []catalogtool.FeaturePack{pack}, kinds); err != nil {
		t.Fatalf("expected hosted pack to pass, got %v", err)
	}
}
