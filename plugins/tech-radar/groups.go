package main

import (
	"fmt"
	"strings"
)

const maxInterestGroups = 4
const maxInterestGroupTopics = 12

type interestGroup struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Topics []string `json:"topics"`
}

type snapshotInterestGroup struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Topics     []string `json:"topics"`
	QueryCount int      `json:"query_count"`
}

type resolvedTopicQuery struct {
	Topic    string
	GroupIDs []string
}

type collectionPlan struct {
	Groups        []interestGroup
	Topics        []resolvedTopicQuery
	GroupLabels   map[string]string
	CustomTopics  []string
	ResolvedCount int
}

func resolveCollection(in input, catalog resolvedCatalog) (collectionPlan, error) {
	groups := append([]interestGroup(nil), in.InterestGroups...)
	explicitGroups := len(groups) > 0
	if !explicitGroups {
		groups = legacyInterestGroups(in, catalog)
	}
	if len(groups) == 0 {
		return collectionPlan{}, fmt.Errorf("至少需要一个采集组")
	}
	if explicitGroups && len(groups) > maxInterestGroups {
		return collectionPlan{}, fmt.Errorf("一个计划最多支持 %d 个采集组", maxInterestGroups)
	}

	normalized := make([]interestGroup, 0, len(groups))
	groupIDs := map[string]struct{}{}
	for index, group := range groups {
		name := strings.TrimSpace(group.Name)
		if name == "" {
			name = fmt.Sprintf("采集组 %d", index+1)
		}
		id := strings.TrimSpace(group.ID)
		if id == "" {
			id = fmt.Sprintf("group-%d", index+1)
		}
		base := id
		for suffix := 2; ; suffix++ {
			if _, exists := groupIDs[id]; !exists {
				break
			}
			id = fmt.Sprintf("%s-%d", base, suffix)
		}
		groupIDs[id] = struct{}{}
		topics := normalizeTopicSlugs(group.Topics)
		if len(topics) == 0 {
			return collectionPlan{}, fmt.Errorf("采集组「%s」至少需要一个标签", name)
		}
		if len(topics) > maxInterestGroupTopics {
			return collectionPlan{}, fmt.Errorf("采集组「%s」最多支持 %d 个唯一 topic", name, maxInterestGroupTopics)
		}
		for _, topic := range topics {
			if !customTopicPattern.MatchString(topic) {
				return collectionPlan{}, fmt.Errorf("采集组「%s」中的 topic「%s」格式不合法", name, topic)
			}
		}
		normalized = append(normalized, interestGroup{ID: id, Name: name, Topics: topics})
	}

	labels := make(map[string]string, len(normalized))
	topicOrder := make([]string, 0)
	membership := map[string][]string{}
	custom := make([]string, 0)
	systemTags := map[string]struct{}{}
	for _, tag := range catalog.Tags {
		systemTags[tag.Slug] = struct{}{}
	}
	for _, group := range normalized {
		labels[group.ID] = group.Name
		for _, topic := range group.Topics {
			if _, exists := membership[topic]; !exists {
				topicOrder = append(topicOrder, topic)
			}
			membership[topic] = appendUnique(membership[topic], group.ID)
			if _, system := systemTags[topic]; !system {
				custom = appendUnique(custom, topic)
			}
		}
	}
	topics := make([]resolvedTopicQuery, 0, len(topicOrder))
	for _, topic := range topicOrder {
		topics = append(topics, resolvedTopicQuery{Topic: topic, GroupIDs: append([]string{}, membership[topic]...)})
	}
	return collectionPlan{
		Groups: normalized, Topics: topics, GroupLabels: labels,
		CustomTopics: custom, ResolvedCount: len(topics),
	}, nil
}

func legacyInterestGroups(in input, catalog resolvedCatalog) []interestGroup {
	categoryIDs := in.CategoryIDs
	if len(categoryIDs) == 0 {
		categoryIDs = in.Domains
	}
	if len(categoryIDs) == 0 && len(in.Topics) == 0 && len(in.ExtraTopics) == 0 {
		categoryIDs = defaultDomains
	}
	groups := make([]interestGroup, 0, len(categoryIDs)+1)
	for _, raw := range categoryIDs {
		key := strings.ToLower(strings.TrimSpace(raw))
		if key == "" {
			continue
		}
		category, ok := catalog.category(key)
		if !ok {
			continue
		}
		groups = append(groups, interestGroup{ID: category.ID, Name: category.Label, Topics: category.Topics})
	}
	customTopics := normalizeTopicSlugs(append(append([]string{}, in.Topics...), in.ExtraTopics...))
	if len(customTopics) > 0 {
		groups = append(groups, interestGroup{ID: "custom", Name: "关注标签", Topics: customTopics})
	}
	return groups
}

func (plan collectionPlan) SnapshotGroups() []snapshotInterestGroup {
	result := make([]snapshotInterestGroup, 0, len(plan.Groups))
	for _, group := range plan.Groups {
		result = append(result, snapshotInterestGroup{
			ID: group.ID, Name: group.Name, Topics: append([]string{}, group.Topics...),
			QueryCount: len(group.Topics) * 2,
		})
	}
	return result
}

func (plan collectionPlan) GroupIDs() []string {
	result := make([]string, 0, len(plan.Groups))
	for _, group := range plan.Groups {
		result = append(result, group.ID)
	}
	return result
}
