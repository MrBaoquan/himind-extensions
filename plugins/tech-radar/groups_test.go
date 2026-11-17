package main

import (
	"strings"
	"testing"
)

func TestResolveCollectionDeduplicatesQueriesAndKeepsMembership(t *testing.T) {
	catalogTestRoot(t)
	catalog, err := loadResolvedCatalog()
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	plan, err := resolveCollection(input{InterestGroups: []interestGroup{
		{ID: "video", Name: "视频", Topics: []string{"text-to-video", "remotion"}},
		{ID: "animation", Name: "动画", Topics: []string{"remotion", "programmatic-video"}},
	}}, catalog)
	if err != nil {
		t.Fatalf("resolve collection: %v", err)
	}
	if len(plan.Groups) != 2 || len(plan.Topics) != 3 {
		t.Fatalf("unexpected collection plan: %#v", plan)
	}
	found := false
	for _, topic := range plan.Topics {
		if topic.Topic == "remotion" {
			found = true
			if strings.Join(topic.GroupIDs, ",") != "video,animation" {
				t.Fatalf("remotion membership lost: %#v", topic)
			}
		}
	}
	if !found {
		t.Fatal("expected remotion query")
	}
}

func TestResolveCollectionRejectsTooManyGroups(t *testing.T) {
	catalogTestRoot(t)
	catalog, err := loadResolvedCatalog()
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	groups := make([]interestGroup, 0, maxInterestGroups+1)
	for index := 0; index <= maxInterestGroups; index++ {
		groups = append(groups, interestGroup{
			ID: "g" + string(rune('a'+index)), Name: "组" + string(rune('A'+index)),
			Topics: []string{"remotion"},
		})
	}
	if _, err := resolveCollection(input{InterestGroups: groups}, catalog); err == nil {
		t.Fatal("expected group limit error")
	}
}

func TestLegacyTopicsBecomeOneInterestGroup(t *testing.T) {
	catalogTestRoot(t)
	catalog, err := loadResolvedCatalog()
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	plan, err := resolveCollection(input{ExtraTopics: []string{"remotion", "manim"}}, catalog)
	if err != nil {
		t.Fatalf("legacy topics: %v", err)
	}
	if len(plan.Groups) != 1 || plan.Groups[0].ID != "custom" || plan.Groups[0].Name != "关注标签" {
		t.Fatalf("unexpected legacy group: %#v", plan.Groups)
	}
	if len(plan.Topics) != 2 {
		t.Fatalf("unexpected legacy topics: %#v", plan.Topics)
	}
}

func TestSystemTagCatalogIsRichAndSearchableMetadataExists(t *testing.T) {
	catalogTestRoot(t)
	tags := builtinTags()
	if len(tags) < 40 {
		t.Fatalf("expected at least 40 system tags, got %d", len(tags))
	}
	labels := map[string]string{}
	for _, tag := range tags {
		labels[tag.Slug] = tag.Label
		if tag.Slug == "gaussian-splatting" && tag.Label == "" {
			t.Fatal("gaussian-splatting label is missing")
		}
	}
	for _, slug := range []string{"ai-image-generation", "text-to-speech", "remotion", "unity3d", "unreal-engine", "webxr", "3d-gaussian-splatting"} {
		if _, ok := labels[slug]; !ok {
			t.Fatalf("missing system tag %s", slug)
		}
	}
}
