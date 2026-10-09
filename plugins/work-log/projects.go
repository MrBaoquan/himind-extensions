package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// 本文件实现「项目」作为一等归类维度：项目是一份可管理的登记表（projects.json），
// 条目写入时按登记表归一（支持别名合并），查询时可按项目聚合与筛选。
//
// 存储位置：<日志根>/projects.json，与日志数据同属一个写入者（本插件），
// 采集器只读取、不写入。

const projectsSchemaVersion = "work-log-projects.v1"

// project 是项目登记表里的一项。
type project struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Aliases     []string `json:"aliases"`
	Description string   `json:"description,omitempty"`
	Status      string   `json:"status"`
	Color       string   `json:"color,omitempty"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

type projectsFile struct {
	SchemaVersion string    `json:"schema_version"`
	UpdatedAt     string    `json:"updated_at"`
	Projects      []project `json:"projects"`
}

func projectsPath(root string) string {
	return filepath.Join(root, "projects.json")
}

func readProjects(root string) (projectsFile, error) {
	body, err := os.ReadFile(projectsPath(root))
	if errors.Is(err, os.ErrNotExist) {
		return projectsFile{SchemaVersion: projectsSchemaVersion, Projects: []project{}}, nil
	}
	if err != nil {
		return projectsFile{}, errors.New("项目登记读取失败")
	}
	var pf projectsFile
	if err := json.Unmarshal(body, &pf); err != nil {
		return projectsFile{}, errors.New("项目登记不是合法 JSON")
	}
	pf.SchemaVersion = projectsSchemaVersion
	if pf.Projects == nil {
		pf.Projects = []project{}
	}
	return pf, nil
}

func writeProjects(root string, pf projectsFile) error {
	pf.SchemaVersion = projectsSchemaVersion
	pf.UpdatedAt = time.Now().Format(time.RFC3339)
	if pf.Projects == nil {
		pf.Projects = []project{}
	}
	payload, err := json.MarshalIndent(pf, "", "  ")
	if err != nil {
		return errors.New("项目登记序列化失败")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return errors.New("项目登记目录创建失败")
	}
	return os.WriteFile(projectsPath(root), append(payload, '\n'), 0o644)
}

func normalizeProjectKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// makeProjectID 由项目名生成稳定 ID，中文名也可用（取归一名的短哈希）。
func makeProjectID(name string) string {
	return "pj-" + shortHash(normalizeProjectKey(name))
}

// projectIndex 建立「归一名称 / 别名 → 规范项目名」的索引。
func projectIndex(pf projectsFile) map[string]string {
	index := map[string]string{}
	for _, p := range pf.Projects {
		canonical := p.Name
		if key := normalizeProjectKey(p.Name); key != "" {
			index[key] = canonical
		}
		for _, alias := range p.Aliases {
			key := normalizeProjectKey(alias)
			if key == "" {
				continue
			}
			if _, exists := index[key]; !exists {
				index[key] = canonical
			}
		}
	}
	return index
}

// resolveProjectName 把原始项目名归一为规范名。
func resolveProjectName(index map[string]string, raw string) (string, bool) {
	key := normalizeProjectKey(raw)
	if key == "" {
		return "", false
	}
	if canonical, ok := index[key]; ok {
		return canonical, true
	}
	return "", false
}

func mergeAliases(current, incoming []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(current)+len(incoming))
	for _, value := range append(append([]string{}, current...), incoming...) {
		key := normalizeProjectKey(value)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, strings.TrimSpace(value))
	}
	sort.Strings(out)
	return out
}

// registerProject 在登记表里登记一个新项目（若同名/同别名已存在则跳过）。
func registerProject(pf *projectsFile, index map[string]string, name string) bool {
	key := normalizeProjectKey(name)
	if key == "" {
		return false
	}
	if _, exists := index[key]; exists {
		return false
	}
	now := time.Now().Format(time.RFC3339)
	pf.Projects = append(pf.Projects, project{
		ID:        makeProjectID(name),
		Name:      strings.TrimSpace(name),
		Aliases:   []string{},
		Status:    "active",
		CreatedAt: now,
		UpdatedAt: now,
	})
	index[key] = strings.TrimSpace(name)
	return true
}

// applyProjectRegistry 用登记表归一一批条目的项目名；登记表缺失时按配置补登。
// 返回是否改动了登记表。resolve/register 两个开关都关时直接跳过。
func applyProjectRegistry(root string, entries []entry, resolve, register bool) (bool, error) {
	if !resolve && !register {
		return false, nil
	}
	pf, err := readProjects(root)
	if err != nil {
		return false, err
	}
	index := projectIndex(pf)
	changed := false
	for i := range entries {
		raw := strings.TrimSpace(entries[i].Project)
		if raw == "" {
			continue
		}
		if canonical, ok := resolveProjectName(index, raw); ok {
			entries[i].Project = canonical
			continue
		}
		if register && registerProject(&pf, index, raw) {
			changed = true
		}
	}
	if changed {
		if err := writeProjects(root, pf); err != nil {
			return false, err
		}
	}
	return changed, nil
}

// ---------- 能力实现 ----------

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
	index := projectIndex(pf)

	type projectStat struct {
		count int
		days  map[string]struct{}
		first string
		last  string
	}
	stats := map[string]*projectStat{}
	unmatched := map[string]int{}
	if includeStats {
		entries, err := collectEntries(root, "0000-01-01", "9999-12-31")
		if err != nil {
			return nil, err
		}
		for _, item := range entries {
			raw := strings.TrimSpace(item.Project)
			if raw == "" {
				continue
			}
			canonical, ok := resolveProjectName(index, raw)
			if !ok {
				unmatched[raw]++
				canonical = raw
			}
			stat := stats[canonical]
			if stat == nil {
				stat = &projectStat{days: map[string]struct{}{}}
				stats[canonical] = stat
			}
			stat.count++
			stat.days[item.Date] = struct{}{}
			if stat.first == "" || item.Date < stat.first {
				stat.first = item.Date
			}
			if item.Date > stat.last {
				stat.last = item.Date
			}
		}
	}

	out := make([]map[string]any, 0, len(pf.Projects))
	for _, p := range pf.Projects {
		if statusFilter != "" && !strings.EqualFold(p.Status, statusFilter) {
			continue
		}
		item := map[string]any{
			"id":          p.ID,
			"name":        p.Name,
			"aliases":     nonNilStrings(p.Aliases),
			"description": p.Description,
			"status":      p.Status,
			"color":       p.Color,
			"created_at":  p.CreatedAt,
			"updated_at":  p.UpdatedAt,
		}
		if includeStats {
			if stat := stats[p.Name]; stat != nil {
				item["entry_count"] = stat.count
				item["active_days"] = len(stat.days)
				item["first_date"] = stat.first
				item["last_date"] = stat.last
			} else {
				item["entry_count"] = 0
			}
		}
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		left, right := mapInt(out[i]["entry_count"]), mapInt(out[j]["entry_count"])
		if left != right {
			return left > right
		}
		return strings.ToLower(mapString(out[i]["name"])) < strings.ToLower(mapString(out[j]["name"]))
	})

	unmatchedList := make([]map[string]any, 0, len(unmatched))
	for name, count := range unmatched {
		unmatchedList = append(unmatchedList, map[string]any{"name": name, "entry_count": count})
	}
	sort.SliceStable(unmatchedList, func(i, j int) bool {
		if mapInt(unmatchedList[i]["entry_count"]) != mapInt(unmatchedList[j]["entry_count"]) {
			return mapInt(unmatchedList[i]["entry_count"]) > mapInt(unmatchedList[j]["entry_count"])
		}
		return mapString(unmatchedList[i]["name"]) < mapString(unmatchedList[j]["name"])
	})

	return map[string]any{
		"log_root":  root,
		"total":     len(out),
		"projects":  out,
		"unmatched": unmatchedList,
	}, nil
}

func projectUpsertCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.ProjectName)
	if name == "" {
		return nil, errors.New("项目名称（project_name）不能为空")
	}
	pf, err := readProjects(root)
	if err != nil {
		return nil, err
	}
	now := time.Now().Format(time.RFC3339)
	index := -1
	if id := strings.TrimSpace(in.ProjectID); id != "" {
		for i := range pf.Projects {
			if pf.Projects[i].ID == id {
				index = i
				break
			}
		}
	}
	if index < 0 {
		canonical, ok := resolveProjectName(projectIndex(pf), name)
		if ok {
			for i := range pf.Projects {
				if strings.EqualFold(pf.Projects[i].Name, canonical) {
					index = i
					break
				}
			}
		}
	}
	aliases := nonNilStrings(trimList(in.Aliases))
	status := strings.TrimSpace(in.Status)
	created := false
	if index < 0 {
		created = true
		if status == "" {
			status = "active"
		}
		pf.Projects = append(pf.Projects, project{
			ID:          makeProjectID(name),
			Name:        name,
			Aliases:     aliases,
			Description: strings.TrimSpace(in.Description),
			Status:      status,
			Color:       strings.TrimSpace(in.Color),
			CreatedAt:   now,
			UpdatedAt:   now,
		})
		index = len(pf.Projects) - 1
	} else {
		item := pf.Projects[index]
		item.Name = name
		if len(aliases) > 0 {
			item.Aliases = mergeAliases(item.Aliases, aliases)
		}
		if description := strings.TrimSpace(in.Description); description != "" {
			item.Description = description
		}
		if status != "" {
			item.Status = status
		}
		if color := strings.TrimSpace(in.Color); color != "" {
			item.Color = color
		}
		item.UpdatedAt = now
		pf.Projects[index] = item
	}
	if err := writeProjects(root, pf); err != nil {
		return nil, err
	}
	return map[string]any{
		"log_root": root,
		"created":  created,
		"project":  pf.Projects[index],
	}, nil
}

func projectDeleteCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	pf, err := readProjects(root)
	if err != nil {
		return nil, err
	}
	id := strings.TrimSpace(in.ProjectID)
	name := strings.TrimSpace(in.ProjectName)
	if id == "" && name == "" {
		return nil, errors.New("需要 project_id 或 project_name")
	}
	kept := make([]project, 0, len(pf.Projects))
	var removed *project
	for _, p := range pf.Projects {
		match := (id != "" && p.ID == id) || (id == "" && name != "" && strings.EqualFold(p.Name, name))
		if match && removed == nil {
			copy := p
			removed = &copy
			continue
		}
		kept = append(kept, p)
	}
	if removed == nil {
		return map[string]any{"log_root": root, "removed": false}, nil
	}
	pf.Projects = kept
	if err := writeProjects(root, pf); err != nil {
		return nil, err
	}
	return map[string]any{"log_root": root, "removed": true, "project": removed}, nil
}

func projectResolveCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	pf, err := readProjects(root)
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(in.ProjectName)
	if raw == "" {
		raw = strings.TrimSpace(in.Project)
	}
	canonical, ok := resolveProjectName(projectIndex(pf), raw)
	name := raw
	if ok {
		name = canonical
	}
	return map[string]any{
		"log_root": root,
		"raw":      raw,
		"name":     name,
		"matched":  ok,
	}, nil
}

// projectAssignCap 把一批已有条目重新归到目标项目（清理历史归类）。
func projectAssignCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	target := strings.TrimSpace(in.ProjectName)
	if target == "" {
		target = strings.TrimSpace(in.Project)
	}
	if target == "" {
		return nil, errors.New("需要目标项目（project 或 project_name）")
	}
	pf, err := readProjects(root)
	if err != nil {
		return nil, err
	}
	if canonical, ok := resolveProjectName(projectIndex(pf), target); ok {
		target = canonical
	}
	from, to, err := resolveRange(in.From, in.To, in.Date)
	if err != nil {
		return nil, err
	}
	dates, err := listDates(root)
	if err != nil {
		return nil, err
	}
	updated := 0
	touched := make([]string, 0)
	for _, date := range dates {
		if date < from || date > to {
			continue
		}
		day, err := readDay(root, date)
		if err != nil {
			return nil, err
		}
		changed := false
		for i := range day.Entries {
			item := day.Entries[i]
			if strings.TrimSpace(item.Project) == target {
				continue
			}
			if !assignSelectorMatches(item, in) {
				continue
			}
			day.Entries[i].Project = target
			day.Entries[i].UpdatedAt = time.Now().Format(time.RFC3339)
			updated++
			changed = true
		}
		if changed {
			if err := writeDay(root, day); err != nil {
				return nil, err
			}
			touched = append(touched, date)
		}
	}
	return map[string]any{
		"log_root": root,
		"project":  target,
		"updated":  updated,
		"dates":    touched,
	}, nil
}

// assignSelectorMatches 判断条目是否命中重归类选择器。空选择器表示不过滤。
func assignSelectorMatches(item entry, in input) bool {
	if id := strings.TrimSpace(in.SessionID); id != "" && item.SessionID != id {
		return false
	}
	if s := strings.TrimSpace(in.Source); s != "" && !strings.EqualFold(item.Source, s) {
		return false
	}
	if c := strings.TrimSpace(in.Category); c != "" && !strings.EqualFold(item.Category, c) {
		return false
	}
	if p := strings.TrimSpace(in.CurrentProject); p != "" && !strings.Contains(strings.ToLower(item.Project), strings.ToLower(p)) {
		return false
	}
	if t := strings.TrimSpace(in.Tag); t != "" {
		found := false
		for _, tag := range item.Tags {
			if strings.EqualFold(tag, t) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
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
