package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

const projectsSchemaVersion = "work-log-project-view.v1"

// project 是项目登记表里的一项。
type project struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Aliases     []string `json:"aliases"`
	Description string   `json:"description,omitempty"`
	Status      string   `json:"status"`
	Color       string   `json:"color,omitempty"`
	ParentID    string   `json:"parent_id,omitempty"`
	// Kind 是项目类型：交付项目（场馆/展馆，含展项）/ 平台产品（自研，1 项目=1 路径，无展项）/ 通用沉淀。
	Kind string `json:"kind,omitempty"`
	// Source 标明项目来源：local（本地登记）或 workbench（从项目看板同步）。
	Source string `json:"source,omitempty"`
	// Paths is a read-only projection of Agent directory bindings.
	Paths []projectPath `json:"paths,omitempty"`
	// Work 是项目当前工作状态：优先级、目标、下一步、阻塞、是否完成。
	// 与 Status（active/archived 登记生命周期）分开，避免把“完成”和“归档”混为一谈。
	Work      projectWork `json:"work,omitempty"`
	CreatedAt string      `json:"created_at"`
	UpdatedAt string      `json:"updated_at"`
}

type projectsFile struct {
	SchemaVersion string    `json:"schema_version"`
	UpdatedAt     string    `json:"updated_at"`
	Projects      []project `json:"projects"`
}

func readProjects(root string) (projectsFile, error) {
	business, err := readWorkspaceBusiness(root)
	if err != nil {
		return projectsFile{}, err
	}
	snapshot, err := loadAgentWorkspace(context.Background())
	if err != nil {
		return projectsFile{}, err
	}
	return projectViewFromAgent(snapshot, *business), nil
}

func writeProjects(root string, pf projectsFile) error {
	business, err := readWorkspaceBusiness(root)
	if err != nil {
		return err
	}
	return writeWorkspaceBusiness(root, business, pf)
}

func normalizeProjectKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// projectIndex 建立「归一名称 / 别名 → 规范项目名」的索引。

// resolveProjectName 把原始项目名归一为规范名。

// projectIndexByName 按规范名（忽略大小写）找项目下标，找不到返回 -1。

func projectListCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	pf, err := readProjects(root)
	if err != nil {
		return nil, err
	}
	includeStats := true
	if in.IncludeStats != nil {
		includeStats = *in.IncludeStats
	}
	statusFilter := strings.TrimSpace(in.Status)

	recentDays := recentDaysFor(in)
	stats, err := collectProjectStats(root, pf)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	out := make([]map[string]any, 0, len(pf.Projects))
	for _, p := range pf.Projects {
		if statusFilter != "" && !strings.EqualFold(p.Status, statusFilter) {
			continue
		}
		stat := stats.Stats[p.ID]
		work := projectWorkFor(p, stat, now, recentDays)
		item := projectBoardItem(p, work, stat, now, recentDays)
		item["aliases"] = nonNilStrings(p.Aliases)
		if !includeStats {
			for _, key := range []string{
				"entry_count", "active_days", "first_date", "last_date", "last_title",
				"minutes_7d", "minutes_30d", "days_since_activity", "stale", "overdue",
				"plan_completion", "has_plan",
			} {
				delete(item, key)
			}
		}
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		left, right := mapInt(out[i]["entry_count"]), mapInt(out[j]["entry_count"])
		if left != right {
			return left > right
		}
		return strings.ToLower(mapStr(out[i], "name")) < strings.ToLower(mapStr(out[j], "name"))
	})

	return map[string]any{
		"log_root":      root,
		"total":         len(out),
		"projects":      out,
		"unmatched":     unmatchedList(stats.Unmatched),
		"pending_paths": collectPendingPaths(stats.Pending),
	}, nil
}

func mapStr(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func aliasMatches(aliases []string, raw string) bool {
	key := normalizeProjectKey(raw)
	if key == "" {
		return false
	}
	for _, a := range aliases {
		if normalizeProjectKey(a) == key {
			return true
		}
	}
	return false
}

func mapInt(value any) int {
	switch node := value.(type) {
	case int:
		return node
	case int64:
		return int(node)
	case float64:
		return int(node)
	default:
		return 0
	}
}

func mapString(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprintf("%v", value)
}

// A supplied ID never falls back to a name. Names and aliases must be unique.
func projectIndexForReference(pf projectsFile, id, name string) (int, error) {
	id, name = strings.TrimSpace(id), strings.TrimSpace(name)
	found := -1
	for i, p := range pf.Projects {
		if id != "" {
			if p.ID == id {
				return i, nil
			}
			continue
		}
		if name != "" && (strings.EqualFold(p.Name, name) || aliasMatches(p.Aliases, name)) {
			if found >= 0 {
				return -1, fmt.Errorf("项目名称存在歧义，请使用 Agent 项目 ID")
			}
			found = i
		}
	}
	if found < 0 {
		return -1, fmt.Errorf("Agent 项目树中未找到目标项目")
	}
	return found, nil
}
