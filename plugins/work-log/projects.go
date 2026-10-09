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
	// Source 标明项目来源：local（本地登记）或 workbench（从项目看板同步）。
	Source string `json:"source,omitempty"`
	// ExternalID 是工作台里的项目 ID（命中同步时填充），用于稳定关联。
	ExternalID string `json:"external_id,omitempty"`
	// Exhibits 是该项目下的展项（可选维度）。展项也支持别名。
	Exhibits  []exhibit `json:"exhibits,omitempty"`
	CreatedAt string    `json:"created_at"`
	UpdatedAt string    `json:"updated_at"`
}

// exhibit 是项目下的展项（工作台维度）。
type exhibit struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Aliases    []string `json:"aliases,omitempty"`
	ExternalID string   `json:"external_id,omitempty"`
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
	if err := writeFileAtomic(projectsPath(root), append(payload, '\n')); err != nil {
		return errors.New("项目登记写入失败")
	}
	return nil
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
			"source":      p.Source,
			"external_id": p.ExternalID,
			"exhibits":    exhibitsToMaps(p.Exhibits),
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
	unlock := lockRoot(root)
	defer unlock()
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
	unlock := lockRoot(root)
	defer unlock()
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
	unlock := lockRoot(root)
	defer unlock()
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

// ---------- 工作台项目同步 ----------

// workbenchProjectsFromStep 读取上游步骤输出里的项目数组（兼容 {items:[...]} 与直接数组）。
func workbenchProjectsFromStep(in input, stepID string) []map[string]any {
	stepID = strings.TrimSpace(stepID)
	if stepID == "" {
		return nil
	}
	out := upstreamStepOutput(in, stepID)
	if out == nil {
		return nil
	}
	for _, key := range []string{"items", "projects", "list", "data"} {
		if arr, ok := out[key].([]any); ok {
			return asMapList(arr)
		}
	}
	return nil
}

func asMapList(arr []any) []map[string]any {
	out := make([]map[string]any, 0, len(arr))
	for _, node := range arr {
		if m, ok := node.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
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

// exhibitsFromWorkbench 从工作台项目对象里解析展项数组。
func exhibitsFromWorkbench(item map[string]any) []exhibit {
	raw, _ := item["exhibits"].([]any)
	if len(raw) == 0 {
		return nil
	}
	out := make([]exhibit, 0, len(raw))
	for _, node := range raw {
		m, ok := node.(map[string]any)
		if !ok {
			continue
		}
		name := firstNonEmpty(mapStr(m, "name"), mapStr(m, "exhibit_name"), mapStr(m, "title"))
		if name == "" {
			continue
		}
		out = append(out, exhibit{
			ID:         "ex-" + shortHash(normalizeProjectKey(name)),
			Name:       name,
			ExternalID: firstNonEmpty(mapStr(m, "id"), mapStr(m, "exhibit_id")),
		})
	}
	return out
}

func mergeExhibits(current, incoming []exhibit) []exhibit {
	if len(incoming) == 0 {
		return current
	}
	seen := map[string]bool{}
	out := make([]exhibit, 0, len(current)+len(incoming))
	for _, ex := range append(append([]exhibit{}, current...), incoming...) {
		key := normalizeProjectKey(ex.Name)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, ex)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func exhibitsToMaps(items []exhibit) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, ex := range items {
		out = append(out, map[string]any{
			"id":          ex.ID,
			"name":        ex.Name,
			"aliases":     nonNilStrings(ex.Aliases),
			"external_id": ex.ExternalID,
		})
	}
	return out
}

// matchProjectIndex 按 external_id、规范名或别名找已登记项目的下标，找不到返回 -1。
func matchProjectIndex(pf projectsFile, name, externalID string) int {
	if externalID != "" {
		for i := range pf.Projects {
			if pf.Projects[i].ExternalID == externalID {
				return i
			}
		}
	}
	key := normalizeProjectKey(name)
	if canonical, ok := projectIndex(pf)[key]; ok {
		key = normalizeProjectKey(canonical)
	}
	if key == "" {
		return -1
	}
	for i := range pf.Projects {
		if normalizeProjectKey(pf.Projects[i].Name) == key {
			return i
		}
		for _, alias := range pf.Projects[i].Aliases {
			if normalizeProjectKey(alias) == key {
				return i
			}
		}
	}
	return -1
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

// enrichEntryLinks 用登记表给条目补上稳定的项目/展项 ID，并把展项名归一。
func enrichEntryLinks(pf projectsFile, entries []entry) {
	index := projectIndex(pf)
	for i := range entries {
		raw := strings.TrimSpace(entries[i].Project)
		if raw == "" {
			continue
		}
		canonical, ok := resolveProjectName(index, raw)
		if !ok {
			continue
		}
		entries[i].Project = canonical
		for _, p := range pf.Projects {
			if !strings.EqualFold(p.Name, canonical) {
				continue
			}
			entries[i].ProjectID = p.ID
			if ex := strings.TrimSpace(entries[i].Exhibit); ex != "" {
				for _, item := range p.Exhibits {
					if strings.EqualFold(item.Name, ex) || aliasMatches(item.Aliases, ex) {
						entries[i].Exhibit = item.Name
						entries[i].ExhibitID = item.ID
						break
					}
				}
			}
			break
		}
	}
}

// projectSyncCap 把工作台项目（含展项）并入本地登记表；已存在则补齐 source/external_id/展项。
func projectSyncCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	items := in.Projects
	if len(items) == 0 {
		items = workbenchProjectsFromStep(in, in.ProjectsStep)
	}
	unlock := lockRoot(root)
	defer unlock()
	pf, err := readProjects(root)
	if err != nil {
		return nil, err
	}
	now := time.Now().Format(time.RFC3339)
	created, updated := 0, 0
	for _, raw := range items {
		name := firstNonEmpty(mapStr(raw, "project_name"), mapStr(raw, "name"))
		if name == "" {
			continue
		}
		externalID := firstNonEmpty(mapStr(raw, "id"), mapStr(raw, "project_id"))
		exhibits := exhibitsFromWorkbench(raw)

		at := matchProjectIndex(pf, name, externalID)
		if at < 0 {
			pf.Projects = append(pf.Projects, project{
				ID:         makeProjectID(name),
				Name:       name,
				Aliases:    []string{},
				Status:     "active",
				Source:     "workbench",
				ExternalID: externalID,
				Exhibits:   exhibits,
				CreatedAt:  now,
				UpdatedAt:  now,
			})
			created++
			continue
		}
		p := pf.Projects[at]
		if externalID != "" {
			p.ExternalID = externalID
		}
		p.Source = "workbench"
		p.Exhibits = mergeExhibits(p.Exhibits, exhibits)
		p.UpdatedAt = now
		pf.Projects[at] = p
		updated++
	}
	if created > 0 || updated > 0 {
		if err := writeProjects(root, pf); err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"log_root": root,
		"total":    len(items),
		"created":  created,
		"updated":  updated,
	}, nil
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
