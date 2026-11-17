package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func catalogTestRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", root)
	return root
}

func TestSaveUserCategoryPersistsTagsAndRevision(t *testing.T) {
	root := catalogTestRoot(t)
	first, err := catalogPayload()
	if err != nil {
		t.Fatalf("initial catalog: %v", err)
	}
	revision := first["revision"].(int)
	result, err := saveCategory(saveCategoryInput{
		Label:            "我的视频创作",
		Description:      "覆盖生成与代码动画",
		Topics:           []string{"text-to-video", "motion-graphics", "motion-graphics"},
		CustomTags:       []catalogTag{{Slug: "motion-graphics", Label: "动态图形", Source: "custom"}},
		ExpectedRevision: revision,
	})
	if err != nil {
		t.Fatalf("save category: %v", err)
	}
	if result["revision"].(int) != revision+1 {
		t.Fatalf("unexpected revision: %#v", result["revision"])
	}
	if _, err := os.Stat(filepath.Join(root, "catalog.json")); err != nil {
		t.Fatalf("catalog file missing: %v", err)
	}
	resolved, err := loadResolvedCatalog()
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	category, ok := resolved.category("usr-")
	if ok {
		t.Fatalf("partial id unexpectedly matched: %#v", category)
	}
	var user resolvedCategory
	for _, item := range resolved.Categories {
		if item.Source == "user" {
			user = item
			break
		}
	}
	if user.ID == "" || user.Label != "我的视频创作" || len(user.Topics) != 2 {
		t.Fatalf("unexpected user category: %#v", user)
	}
	foundTag := false
	for _, tag := range resolved.Tags {
		if tag.Slug == "motion-graphics" && tag.Label == "动态图形" {
			foundTag = true
		}
	}
	if !foundTag {
		t.Fatal("custom tag label was not persisted")
	}
}

func TestCategoryValidationAndRevisionConflict(t *testing.T) {
	catalogTestRoot(t)
	revision := int((mustCatalogRevision(t)))
	for _, input := range []saveCategoryInput{
		{Label: "", Topics: []string{"remotion"}, ExpectedRevision: revision},
		{Label: "空分类", Topics: nil, ExpectedRevision: revision},
		{Label: "非法标签", Topics: []string{"AI Agent"}, ExpectedRevision: revision},
		{Label: "未知标签", Topics: []string{"not-in-library"}, ExpectedRevision: revision},
	} {
		if _, err := saveCategory(input); err == nil {
			t.Fatalf("expected validation error for %#v", input)
		}
	}
	saved, err := saveCategory(saveCategoryInput{
		Label: "可用分类", Topics: []string{"remotion"}, ExpectedRevision: revision,
	})
	if err != nil {
		t.Fatalf("save valid category: %v", err)
	}
	if _, err := saveCategory(saveCategoryInput{
		Label: "过期写入", Topics: []string{"llm"}, ExpectedRevision: revision,
	}); err == nil || !strings.Contains(err.Error(), "revision") {
		t.Fatalf("expected stale revision error, got %v", err)
	}
	if saved["revision"].(int) != revision+1 {
		t.Fatalf("unexpected revision after save: %#v", saved["revision"])
	}
}

func TestDeleteCategoryProtectsBuiltinsAndUsers(t *testing.T) {
	catalogTestRoot(t)
	if _, err := deleteCategory(deleteCategoryInput{ID: "ai-video"}); err == nil {
		t.Fatal("expected builtin delete to be rejected")
	}
	revision := int(mustCatalogRevision(t))
	saved, err := saveCategory(saveCategoryInput{
		Label: "待删除分类", Topics: []string{"llm"}, ExpectedRevision: revision,
	})
	if err != nil {
		t.Fatalf("save category: %v", err)
	}
	id := saved["saved_id"].(string)
	if _, err := deleteCategory(deleteCategoryInput{ID: id, ExpectedRevision: saved["revision"].(int)}); err != nil {
		t.Fatalf("delete user category: %v", err)
	}
	resolved, err := loadResolvedCatalog()
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	for _, item := range resolved.Categories {
		if item.ID == id {
			t.Fatalf("deleted category still visible: %#v", item)
		}
	}
}

func TestQueryGroupsResolvesUserCategoryAndLegacyDomains(t *testing.T) {
	catalogTestRoot(t)
	revision := int(mustCatalogRevision(t))
	saved, err := saveCategory(saveCategoryInput{
		Label:            "我的视频分类",
		Topics:           []string{"text-to-video", "remotion"},
		CustomTags:       []catalogTag{{Slug: "remotion", Label: "Remotion", Source: "custom"}},
		ExpectedRevision: revision,
	})
	if err != nil {
		t.Fatalf("save category: %v", err)
	}
	id := saved["saved_id"].(string)
	groups, names, err := queryGroups(input{
		CategoryIDs: []string{id},
		ExtraTopics: []string{"motion-graphics"},
	})
	if err != nil {
		t.Fatalf("resolve category: %v", err)
	}
	if strings.Join(names, ",") != id+",custom" || len(groups) != 3 {
		t.Fatalf("unexpected groups/names: %v %#v", names, groups)
	}
	legacy, names, err := queryGroups(input{Domains: []string{"ai-video"}})
	if err != nil {
		t.Fatalf("legacy domains: %v", err)
	}
	if strings.Join(names, ",") != "ai-video" || len(legacy) != 2 {
		t.Fatalf("legacy domains were not compatible: %v %#v", names, legacy)
	}
}

func TestQueryGroupsDeduplicatesTopicsAcrossCategories(t *testing.T) {
	catalogTestRoot(t)
	saved, err := saveCategory(saveCategoryInput{
		Label:            "重叠分类",
		Topics:           []string{"text-to-video", "motion-graphics"},
		CustomTags:       []catalogTag{{Slug: "motion-graphics", Label: "动态图形", Source: "custom"}},
		ExpectedRevision: int(mustCatalogRevision(t)),
	})
	if err != nil {
		t.Fatalf("save category: %v", err)
	}
	id := saved["saved_id"].(string)
	groups, names, err := queryGroups(input{
		CategoryIDs: []string{"ai-video", id, "ai-video"},
		ExtraTopics: []string{"video-generation"},
	})
	if err != nil {
		t.Fatalf("resolve overlapping categories: %v", err)
	}
	want := []string{"text-to-video", "video-generation", "motion-graphics"}
	if strings.Join(names, ",") != "ai-video,"+id {
		t.Fatalf("duplicate category names were not removed: %v", names)
	}
	if len(groups) != len(want) {
		t.Fatalf("expected %d unique topics, got %d: %#v", len(want), len(groups), groups)
	}
	for index, topic := range want {
		if groups[index].topic != topic {
			t.Fatalf("topic %d: expected %s, got %#v", index, topic, groups[index])
		}
	}
}
func TestCategoryLimitAndTopicLimit(t *testing.T) {
	catalogTestRoot(t)
	tooMany := make([]string, 0, maxCategoryTopics+1)
	for index := 0; index <= maxCategoryTopics; index++ {
		tooMany = append(tooMany, "topic-"+string(rune('a'+index)))
	}
	if _, err := saveCategory(saveCategoryInput{Label: "超限分类", Topics: tooMany}); err == nil {
		t.Fatal("expected topic limit error")
	}
}

func mustCatalogRevision(t *testing.T) int {
	t.Helper()
	catalog, err := catalogPayload()
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	return catalog["revision"].(int)
}
