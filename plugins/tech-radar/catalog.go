package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	catalogVersion        = 1
	maxCategoryTopics     = 12
	maxCategoryLabelRunes = 40
	maxUserCategories     = 50
	maxCatalogTags        = 200
)

type catalogTag struct {
	Slug        string   `json:"slug"`
	Label       string   `json:"label"`
	Description string   `json:"description,omitempty"`
	Aliases     []string `json:"aliases,omitempty"`
	Source      string   `json:"source"`
}

type storedCategory struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Topics      []string `json:"topics"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

type userCatalog struct {
	Version    int              `json:"version"`
	Revision   int              `json:"revision"`
	Tags       []catalogTag     `json:"tags"`
	Categories []storedCategory `json:"categories"`
}

type resolvedCategory struct {
	ID          string
	Label       string
	Description string
	Topics      []string
	Source      string
	CreatedAt   string
	UpdatedAt   string
}

type resolvedCatalog struct {
	Revision   int
	Categories []resolvedCategory
	Tags       []catalogTag
}

type saveCategoryInput struct {
	ID               string       `json:"id"`
	Label            string       `json:"label"`
	Description      string       `json:"description"`
	Topics           []string     `json:"topics"`
	CustomTags       []catalogTag `json:"custom_tags"`
	ExpectedRevision int          `json:"expected_revision"`
}

type deleteCategoryInput struct {
	ID               string `json:"id"`
	ExpectedRevision int    `json:"expected_revision"`
}

func catalogPath() string {
	return filepath.Join(defaultDataRoot(), "catalog.json")
}

func readUserCatalog() (userCatalog, error) {
	path := catalogPath()
	payload, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return userCatalog{Version: catalogVersion, Revision: 1}, nil
	}
	if err != nil {
		return userCatalog{}, fmt.Errorf("读取分类目录失败：%w", err)
	}
	var catalog userCatalog
	if err := json.Unmarshal(payload, &catalog); err != nil {
		return userCatalog{}, fmt.Errorf("分类目录格式损坏：%w", err)
	}
	if catalog.Version == 0 {
		catalog.Version = catalogVersion
	}
	if catalog.Revision <= 0 {
		catalog.Revision = 1
	}
	catalog.Tags = normalizeCatalogTags(catalog.Tags)
	for index := range catalog.Categories {
		catalog.Categories[index].Topics = normalizeTopicSlugs(catalog.Categories[index].Topics)
	}
	return catalog, nil
}

func writeUserCatalog(catalog userCatalog) error {
	path := catalogPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建分类目录失败：%w", err)
	}
	payload, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化分类目录失败：%w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "catalog-*.tmp")
	if err != nil {
		return fmt.Errorf("创建分类目录临时文件失败：%w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := temp.Write(payload); err != nil {
		temp.Close()
		return fmt.Errorf("写入分类目录失败：%w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("同步分类目录失败：%w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("关闭分类目录临时文件失败：%w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("替换分类目录失败：%w", err)
	}
	return nil
}

func normalizeCatalogTags(tags []catalogTag) []catalogTag {
	result := make([]catalogTag, 0, len(tags))
	seen := map[string]struct{}{}
	for _, tag := range tags {
		slug := strings.ToLower(strings.TrimSpace(tag.Slug))
		if slug == "" || !customTopicPattern.MatchString(slug) {
			continue
		}
		if _, ok := seen[slug]; ok {
			continue
		}
		label := strings.TrimSpace(tag.Label)
		if label == "" {
			label = slug
		}
		source := strings.TrimSpace(tag.Source)
		if source != "system" {
			source = "custom"
		}
		seen[slug] = struct{}{}
		result = append(result, catalogTag{Slug: slug, Label: label, Source: source})
	}
	return result
}

func normalizeTopicSlugs(topics []string) []string {
	result := make([]string, 0, len(topics))
	for _, raw := range topics {
		topic := strings.ToLower(strings.TrimSpace(raw))
		if topic == "" {
			continue
		}
		result = appendUnique(result, topic)
	}
	return result
}

func builtinCategories() []resolvedCategory {
	result := make([]resolvedCategory, 0, len(themePresets))
	for _, preset := range themePresets {
		result = append(result, resolvedCategory{
			ID: preset.ID, Label: preset.Label, Topics: append([]string{}, preset.Topics...), Source: "system",
		})
	}
	return result
}

func builtinTags() []catalogTag {
	result := make([]catalogTag, 0, len(systemTagPresets)+len(themePresets)*2)
	seen := map[string]struct{}{}
	for _, tag := range systemTagPresets {
		slug := strings.ToLower(strings.TrimSpace(tag.Slug))
		if slug == "" {
			continue
		}
		if _, ok := seen[slug]; ok {
			continue
		}
		seen[slug] = struct{}{}
		tag.Slug = slug
		tag.Label = strings.TrimSpace(tag.Label)
		if tag.Label == "" {
			tag.Label = slug
		}
		tag.Source = "system"
		result = append(result, tag)
	}
	for _, preset := range themePresets {
		for _, topic := range preset.Topics {
			if _, ok := seen[topic]; ok {
				continue
			}
			seen[topic] = struct{}{}
			result = append(result, catalogTag{Slug: topic, Label: topic, Source: "system"})
		}
	}
	return result
}

func loadResolvedCatalog() (resolvedCatalog, error) {
	user, err := readUserCatalog()
	if err != nil {
		return resolvedCatalog{}, err
	}
	categories := builtinCategories()
	for _, item := range user.Categories {
		categories = append(categories, resolvedCategory{
			ID: item.ID, Label: item.Label, Description: item.Description,
			Topics: append([]string{}, item.Topics...), Source: "user",
			CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
		})
	}
	tags := builtinTags()
	for _, tag := range user.Tags {
		found := false
		for _, system := range tags {
			if system.Slug == tag.Slug {
				found = true
				break
			}
		}
		if !found {
			tags = append(tags, tag)
		}
	}
	sort.SliceStable(tags, func(left, right int) bool {
		if tags[left].Source != tags[right].Source {
			return tags[left].Source == "system"
		}
		return tags[left].Label < tags[right].Label
	})
	return resolvedCatalog{Revision: user.Revision, Categories: categories, Tags: tags}, nil
}

func catalogPayload() (map[string]any, error) {
	catalog, err := loadResolvedCatalog()
	if err != nil {
		return nil, err
	}
	themes := make([]map[string]any, 0, len(catalog.Categories))
	for _, item := range catalog.Categories {
		themes = append(themes, map[string]any{
			"id": item.ID, "label": item.Label, "description": item.Description,
			"topics": item.Topics, "source": item.Source, "editable": item.Source == "user",
			"query_count": len(item.Topics) * 2,
		})
	}
	tags := make([]map[string]any, 0, len(catalog.Tags))
	for _, tag := range catalog.Tags {
		tags = append(tags, map[string]any{
			"slug": tag.Slug, "label": tag.Label, "description": tag.Description,
			"aliases": tag.Aliases, "source": tag.Source,
		})
	}
	starterGroups := make([]map[string]any, 0, len(themePresets))
	for _, preset := range themePresets {
		starterGroups = append(starterGroups, map[string]any{
			"id": preset.ID, "name": preset.Label, "topics": preset.Topics,
			"query_count": len(preset.Topics) * 2,
		})
	}
	return map[string]any{
		"version": catalogVersion, "revision": catalog.Revision,
		"themes": themes, "tags": tags, "starter_groups": starterGroups,
		"max_queries": defaultMaxQueries, "max_group_topics": 12, "max_groups": 4,
		"query_interval_ms": unauthenticatedQueryIntervalMs, "custom_topic_pattern": customTopicPattern.String(),
	}, nil
}

func (catalog resolvedCatalog) category(id string) (resolvedCategory, bool) {
	key := strings.ToLower(strings.TrimSpace(id))
	for _, item := range catalog.Categories {
		if strings.EqualFold(item.ID, key) {
			return item, true
		}
	}
	return resolvedCategory{}, false
}

func (catalog resolvedCatalog) labels() map[string]string {
	result := make(map[string]string, len(catalog.Categories))
	for _, item := range catalog.Categories {
		result[item.ID] = item.Label
	}
	result["custom"] = "额外 topic"
	return result
}

func saveCategory(input saveCategoryInput) (map[string]any, error) {
	user, err := readUserCatalog()
	if err != nil {
		return nil, err
	}
	if input.ExpectedRevision > 0 && input.ExpectedRevision != user.Revision {
		return nil, fmt.Errorf("分类目录已被其他窗口修改（当前 revision=%d），请刷新后重试", user.Revision)
	}
	label := strings.TrimSpace(input.Label)
	if label == "" {
		return nil, errors.New("分类名称不能为空")
	}
	if len([]rune(label)) > maxCategoryLabelRunes {
		return nil, fmt.Errorf("分类名称不能超过 %d 个字", maxCategoryLabelRunes)
	}
	description := strings.TrimSpace(input.Description)
	if len([]rune(description)) > 200 {
		return nil, errors.New("分类说明不能超过 200 个字")
	}
	topics := normalizeTopicSlugs(input.Topics)
	if len(topics) == 0 {
		return nil, errors.New("分类至少需要一个标签主题")
	}
	if len(topics) > maxCategoryTopics {
		return nil, fmt.Errorf("单个分类最多 %d 个唯一 topic，当前为 %d 个", maxCategoryTopics, len(topics))
	}
	for _, topic := range topics {
		if !customTopicPattern.MatchString(topic) {
			return nil, fmt.Errorf("标签主题「%s」格式不合法：只允许小写字母、数字和连字符，长度 1-50", topic)
		}
	}
	for _, item := range builtinCategories() {
		if strings.EqualFold(item.Label, label) && !strings.EqualFold(item.ID, input.ID) {
			return nil, fmt.Errorf("分类名称「%s」与内置分类重复", label)
		}
	}
	for _, item := range user.Categories {
		if strings.EqualFold(item.Label, label) && !strings.EqualFold(item.ID, input.ID) {
			return nil, fmt.Errorf("分类名称「%s」已存在", label)
		}
	}

	customBySlug := make(map[string]catalogTag, len(input.CustomTags))
	for _, tag := range input.CustomTags {
		slug := strings.ToLower(strings.TrimSpace(tag.Slug))
		if !customTopicPattern.MatchString(slug) {
			return nil, fmt.Errorf("自定义标签「%s」格式不合法", tag.Slug)
		}
		label := strings.TrimSpace(tag.Label)
		if label == "" {
			label = slug
		}
		customBySlug[slug] = catalogTag{Slug: slug, Label: label, Source: "custom"}
	}
	knownTags := map[string]struct{}{}
	for _, tag := range append(builtinTags(), user.Tags...) {
		knownTags[tag.Slug] = struct{}{}
	}
	for _, topic := range topics {
		if _, ok := customBySlug[topic]; ok {
			continue
		}
		if _, ok := knownTags[topic]; !ok {
			return nil, fmt.Errorf("标签主题「%s」不在标签库中；请先添加自定义标签", topic)
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	index := -1
	if strings.TrimSpace(input.ID) != "" {
		for i := range user.Categories {
			if strings.EqualFold(user.Categories[i].ID, input.ID) {
				index = i
				break
			}
		}
		if index < 0 {
			return nil, errors.New("要修改的用户分类不存在；系统分类请先复制")
		}
	} else {
		if len(user.Categories) >= maxUserCategories {
			return nil, fmt.Errorf("最多创建 %d 个用户分类", maxUserCategories)
		}
		input.ID = newCategoryID()
	}
	for _, tag := range customBySlug {
		found := -1
		for i := range user.Tags {
			if user.Tags[i].Slug == tag.Slug {
				found = i
				break
			}
		}
		if found >= 0 {
			user.Tags[found].Label = tag.Label
			continue
		}
		if len(user.Tags) >= maxCatalogTags {
			return nil, fmt.Errorf("自定义标签最多 %d 个", maxCatalogTags)
		}
		user.Tags = append(user.Tags, tag)
	}
	user.Tags = normalizeCatalogTags(user.Tags)
	stored := storedCategory{
		ID: input.ID, Label: label, Description: description, Topics: topics,
		CreatedAt: now, UpdatedAt: now,
	}
	if index >= 0 {
		stored.CreatedAt = user.Categories[index].CreatedAt
		user.Categories[index] = stored
	} else {
		user.Categories = append(user.Categories, stored)
	}
	user.Version = catalogVersion
	user.Revision++
	if err := writeUserCatalog(user); err != nil {
		return nil, err
	}
	payload, err := catalogPayload()
	if err != nil {
		return nil, err
	}
	payload["saved_id"] = input.ID
	return payload, nil
}

func deleteCategory(input deleteCategoryInput) (map[string]any, error) {
	user, err := readUserCatalog()
	if err != nil {
		return nil, err
	}
	if input.ExpectedRevision > 0 && input.ExpectedRevision != user.Revision {
		return nil, fmt.Errorf("分类目录已被其他窗口修改（当前 revision=%d），请刷新后重试", user.Revision)
	}
	if _, ok := (resolvedCatalog{Categories: builtinCategories()}).category(input.ID); ok {
		return nil, errors.New("内置分类只读，不能删除；可以复制后修改")
	}
	index := -1
	for i := range user.Categories {
		if strings.EqualFold(user.Categories[i].ID, input.ID) {
			index = i
			break
		}
	}
	if index < 0 {
		return nil, errors.New("用户分类不存在")
	}
	user.Categories = append(user.Categories[:index], user.Categories[index+1:]...)
	user.Version = catalogVersion
	user.Revision++
	if err := writeUserCatalog(user); err != nil {
		return nil, err
	}
	return catalogPayload()
}

func newCategoryID() string {
	buffer := make([]byte, 6)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("usr-%d", time.Now().UnixNano())
	}
	return "usr-" + hex.EncodeToString(buffer)
}
