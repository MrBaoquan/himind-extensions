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

const projectsSchemaVersion = "work-log-projects.v2"

// project 是项目登记表里的一项。
type project struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Aliases     []string `json:"aliases"`
	Description string   `json:"description,omitempty"`
	Status      string   `json:"status"`
	Color       string   `json:"color,omitempty"`
	// Kind 是项目类型：交付项目（场馆/展馆，含展项）/ 平台产品（自研，1 项目=1 路径，无展项）/ 通用沉淀。
	Kind string `json:"kind,omitempty"`
	// Source 标明项目来源：local（本地登记）或 workbench（从项目看板同步）。
	Source string `json:"source,omitempty"`
	// ExternalID 是工作台里的项目 ID（命中同步时填充），用于稳定关联。
	ExternalID string `json:"external_id,omitempty"`
	// Exhibits 是该项目下的展项（可选维度）。展项也支持别名。
	Exhibits []exhibit `json:"exhibits,omitempty"`
	// Paths 是该项目的本机目录/仓库映射（路径是稳定标识，名称只是展示）。
	Paths []projectPath `json:"paths,omitempty"`
	// Work 是项目当前工作状态：优先级、目标、下一步、阻塞、是否完成。
	// 与 Status（active/archived 登记生命周期）分开，避免把“完成”和“归档”混为一谈。
	Work      projectWork `json:"work,omitempty"`
	CreatedAt string      `json:"created_at"`
	UpdatedAt string      `json:"updated_at"`
}

// exhibit 是项目下的展项（工作台维度）。
type exhibit struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Aliases    []string `json:"aliases,omitempty"`
	ExternalID string   `json:"external_id,omitempty"`
	// Paths 是展项对应的本机目录/仓库映射（路径优先于名称，用于稳定归类）。
	Paths []projectPath `json:"paths,omitempty"`
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
	if err := migrateProjectsFile(&pf); err != nil {
		return projectsFile{}, err
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
		return fmt.Errorf("项目登记写入失败：%w", err)
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
		Work:      projectWork{Priority: priorityP2, NextSteps: []nextStep{}, Blockers: []string{}},
		CreatedAt: now,
		UpdatedAt: now,
	})
	index[key] = strings.TrimSpace(name)
	return true
}

// applyProjectRegistry 用登记表归一一批条目的项目名，并直接为未命中的项目建项目。
//
// 归类优先级：仓库路径（展项优先）> 名称/别名 > 未命中即建。未命中时不再留下「待认领」
// 环节，而是直接用条目自带项目名 / 采集建议 project_hint / 仓库目录名建项目，并把仓库
// 路径登记为该项目的映射：之后同类会话自动命中，用户可在登记管理里改名或合并。
//   - 命中路径/名称：填规范名与稳定 ID。
//   - 未命中：建项目（名称取 raw > project_hint > 目录名），登记仓库路径映射与展项。
//   - 既无名称也无路径：保持空（无从命名）。
//
// 返回是否改动了登记表（改了就落盘 projects.json）。
func applyProjectRegistry(root string, entries []entry, resolve bool) (bool, error) {
	if !resolve {
		return false, nil
	}
	pf, err := readProjects(root)
	if err != nil {
		return false, err
	}
	index := projectIndex(pf)
	changed := false
	now := time.Now().Format(time.RFC3339)
	for i := range entries {
		item := &entries[i]
		raw := strings.TrimSpace(item.Project)
		dir := strings.TrimSpace(item.RepoPath)
		if dir != "" {
			if hit, ok := matchPathEntry(pf, dir); ok {
				item.Project = hit.Project
				item.ProjectID = hit.ProjectID
				if hit.Exhibit != "" {
					item.Exhibit = hit.Exhibit
					item.ExhibitID = hit.ExhibitID
				} else {
					applyExhibitLink(pf, item)
				}
				item.ProjectHint = ""
				continue
			}
		}
		if raw != "" {
			if canonical, ok := resolveProjectName(index, raw); ok {
				item.Project = canonical
				applyExhibitLink(pf, item)
				item.ProjectHint = ""
				continue
			}
		}
		// 未命中：直接建项目（不再有待认领环节）。名称优先取条目自带项目名，
		// 其次取采集建议 project_hint，最后退回仓库目录名（稳定事实）。
		name := firstNonEmpty(raw, strings.TrimSpace(item.ProjectHint), repoBaseName(dir))
		if name == "" {
			item.ProjectID = ""
			item.ExhibitID = ""
			item.ProjectHint = ""
			continue
		}
		at := projectIndexByName(pf, name)
		if at < 0 {
			if registerProject(&pf, index, name) {
				changed = true
			}
			at = projectIndexByName(pf, name)
		}
		if at < 0 {
			item.ProjectID = ""
			item.ExhibitID = ""
			continue
		}
		target := &pf.Projects[at]
		item.Project = target.Name
		item.ProjectID = target.ID
		if dir != "" && !projectHasPath(*target, dir) {
			target.Paths = mergePaths(target.Paths, sanitizePaths([]pathSpec{{Path: dir, Kind: "repo", Source: "local"}}))
			target.UpdatedAt = now
			changed = true
		}
		if ex := strings.TrimSpace(item.Exhibit); ex != "" {
			if !hasExhibitNamed(*target, ex) {
				target.Exhibits = mergeExhibits(target.Exhibits, []exhibit{{
					ID:      "ex-" + shortHash(normalizeProjectKey(ex)),
					Name:    ex,
					Aliases: []string{},
				}})
				target.UpdatedAt = now
				changed = true
			}
			item.Exhibit = ex
			item.ExhibitID = "ex-" + shortHash(normalizeProjectKey(ex))
		} else {
			item.ExhibitID = ""
		}
		item.ProjectHint = ""
	}
	if changed {
		if err := writeProjects(root, pf); err != nil {
			return false, err
		}
	}
	return changed, nil
}

// projectIndexByName 按规范名（忽略大小写）找项目下标，找不到返回 -1。
func projectIndexByName(pf projectsFile, name string) int {
	key := normalizeProjectKey(name)
	if key == "" {
		return -1
	}
	for i := range pf.Projects {
		if normalizeProjectKey(pf.Projects[i].Name) == key {
			return i
		}
	}
	return -1
}

// projectHasPath 判断项目是否已登记该路径（归一后比较）。
func projectHasPath(p project, dir string) bool {
	key := normalizePathKey(dir)
	if key == "" {
		return false
	}
	for _, item := range p.Paths {
		if normalizePathKey(item.Path) == key {
			return true
		}
	}
	return false
}

// repoBaseName 取仓库/目录路径的最后一段，作为无从命名时的兜底项目名（保留原大小写）。
func repoBaseName(p string) string {
	s := strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(p), "\\", "/"))
	s = strings.TrimRight(s, "/")
	if s == "" {
		return ""
	}
	if idx := strings.LastIndex(s, "/"); idx >= 0 {
		s = s[idx+1:]
	}
	return strings.TrimSpace(s)
}

// applyExhibitLink 给单条条目补上稳定的项目/展项 ID，并把展项名归一。
func applyExhibitLink(pf projectsFile, item *entry) {
	project := strings.TrimSpace(item.Project)
	if project == "" {
		return
	}
	for _, p := range pf.Projects {
		if !strings.EqualFold(p.Name, project) {
			continue
		}
		item.Project = p.Name
		item.ProjectID = p.ID
		ex := strings.TrimSpace(item.Exhibit)
		if ex == "" {
			return
		}
		for _, e := range p.Exhibits {
			if strings.EqualFold(e.Name, ex) || aliasMatches(e.Aliases, ex) {
				item.Exhibit = e.Name
				item.ExhibitID = e.ID
				return
			}
		}
		item.ExhibitID = ""
		return
	}
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

	recentDays := recentDaysFor(in)
	stats, err := collectProjectStats(root, index)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	out := make([]map[string]any, 0, len(pf.Projects))
	for _, p := range pf.Projects {
		if statusFilter != "" && !strings.EqualFold(p.Status, statusFilter) {
			continue
		}
		stat := stats.Stats[p.Name]
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
			Kind:        strings.TrimSpace(in.Kind),
			Work:        projectWork{State: workStatePlanned, Priority: priorityP2, NextSteps: []nextStep{}, Blockers: []string{}},
			Paths:       sanitizePaths(in.Paths),
			CreatedAt:   now,
			UpdatedAt:   now,
		})
		index = len(pf.Projects) - 1
	} else {
		item := pf.Projects[index]
		if item.Work.NextSteps == nil {
			item.Work.NextSteps = []nextStep{}
		}
		if item.Work.Blockers == nil {
			item.Work.Blockers = []string{}
		}
		item.Name = name
		if len(aliases) > 0 {
			item.Aliases = mergeAliases(item.Aliases, aliases)
		}
		if in.Paths != nil {
			item.Paths = sanitizePaths(in.Paths)
		} else {
			item.Paths = mergePaths(item.Paths, sanitizePaths(in.AddPaths))
			item.Paths = removePaths(item.Paths, in.RemovePaths)
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
		if kind := strings.TrimSpace(in.Kind); kind != "" {
			item.Kind = kind
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
	// 支持单个与批量：project_ids 优先，兼容 project_id / project_name。
	wantedIDs := map[string]bool{}
	for _, v := range in.ProjectIDs {
		if s := strings.TrimSpace(v); s != "" {
			wantedIDs[s] = true
		}
	}
	if id := strings.TrimSpace(in.ProjectID); id != "" {
		wantedIDs[id] = true
	}
	wantedNames := map[string]bool{}
	if name := strings.TrimSpace(in.ProjectName); name != "" {
		wantedNames[normalizeProjectKey(name)] = true
	}
	if len(wantedIDs) == 0 && len(wantedNames) == 0 {
		return nil, errors.New("需要 project_id(s) 或 project_name")
	}
	kept := make([]project, 0, len(pf.Projects))
	removed := make([]project, 0, 1)
	for _, p := range pf.Projects {
		if wantedIDs[p.ID] || wantedNames[normalizeProjectKey(p.Name)] {
			removed = append(removed, p)
			continue
		}
		kept = append(kept, p)
	}
	if len(removed) == 0 {
		return map[string]any{"log_root": root, "removed": false, "count": 0}, nil
	}
	pf.Projects = kept
	if err := writeProjects(root, pf); err != nil {
		return nil, err
	}
	result := map[string]any{
		"log_root": root,
		"removed":  true,
		"count":    len(removed),
		"projects": removed,
	}
	if len(removed) == 1 {
		result["project"] = removed[0]
	}
	return result, nil
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
			ExternalID: firstNonEmpty(mapStr(m, "id"), mapStr(m, "pid"), mapStr(m, "exhibit_id")),
			Paths:      exhibitPathsFromWorkbench(m),
		})
	}
	return out
}

func mergeExhibits(current, incoming []exhibit) []exhibit {
	if len(incoming) == 0 {
		return current
	}
	seen := map[string]int{}
	out := make([]exhibit, 0, len(current)+len(incoming))
	for _, ex := range append(append([]exhibit{}, current...), incoming...) {
		key := normalizeProjectKey(ex.Name)
		if key == "" {
			continue
		}
		if at, ok := seen[key]; ok {
			// 同名展项：合并路径与别名，补齐外部 ID。
			out[at].Paths = mergePaths(out[at].Paths, ex.Paths)
			out[at].Aliases = mergeAliases(out[at].Aliases, ex.Aliases)
			if out[at].ExternalID == "" {
				out[at].ExternalID = ex.ExternalID
			}
			continue
		}
		seen[key] = len(out)
		out = append(out, ex)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// exhibitPathsFromWorkbench 从工作台展项对象里解析本机路径（source_path / release_path）。
func exhibitPathsFromWorkbench(m map[string]any) []projectPath {
	var specs []pathSpec
	if sp := firstNonEmpty(mapStr(m, "source_path"), mapStr(m, "sourcePath")); sp != "" {
		specs = append(specs, pathSpec{Path: sp, Kind: "repo", Source: "workbench"})
	}
	if rp := firstNonEmpty(mapStr(m, "release_path"), mapStr(m, "releasePath")); rp != "" {
		specs = append(specs, pathSpec{Path: rp, Kind: "release", Source: "workbench"})
	}
	return sanitizePaths(specs)
}

func exhibitsToMaps(items []exhibit) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, ex := range items {
		out = append(out, map[string]any{
			"id":          ex.ID,
			"name":        ex.Name,
			"aliases":     nonNilStrings(ex.Aliases),
			"external_id": ex.ExternalID,
			"paths":       pathsToMaps(ex.Paths),
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

// projectSyncCap 把工作台项目（含展项）并入本地登记表；已存在则补齐 source/external_id/展项/路径。
func projectSyncCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	items := in.Projects
	if len(items) == 0 {
		items = workbenchProjectsFromStep(in, in.ProjectsStep)
	}
	exhibitItems := in.Exhibits
	if len(exhibitItems) == 0 {
		exhibitItems = workbenchExhibitsFromStep(in, in.ExhibitsStep)
	}
	unlock := lockRoot(root)
	defer unlock()
	pf, err := readProjects(root)
	if err != nil {
		return nil, err
	}
	now := time.Now().Format(time.RFC3339)
	created, updated, skipped := 0, 0, 0
	for _, raw := range items {
		name := firstNonEmpty(mapStr(raw, "project_name"), mapStr(raw, "name"))
		if name == "" {
			continue
		}
		externalID := firstNonEmpty(mapStr(raw, "id"), mapStr(raw, "project_id"))
		paths := projectPathsFromWorkbench(raw)

		at := matchProjectIndex(pf, name, externalID)
		if at < 0 {
			// 本地没有实际关联（条目/路径）的项目不自动登记：工作台是权威来源，但不做批量导入。
			skipped++
			continue
		}
		p := pf.Projects[at]
		if externalID != "" {
			p.ExternalID = externalID
		}
		p.Source = "workbench"
		// 仅增补本地已实际使用的展项；未在本地出现的展项不批量引入。
		p.Exhibits = mergeExhibits(p.Exhibits, knownExhibitsOnly(p, exhibitsFromWorkbench(raw)))
		p.Paths = mergePaths(p.Paths, paths)
		p.UpdatedAt = now
		pf.Projects[at] = p
		updated++
	}

	// 扁平展项列表（来自 workbench.business.exhibit.list）：按项目归位，携带 source_path/release_path。
	exUpdated := 0
	for _, raw := range exhibitItems {
		name := firstNonEmpty(mapStr(raw, "exhibit_name"), mapStr(raw, "name"))
		if name == "" {
			continue
		}
		pid := firstNonEmpty(mapStr(raw, "pid"), mapStr(raw, "id"), mapStr(raw, "exhibit_id"))
		projectName := firstNonEmpty(mapStr(raw, "project_name"), mapStr(raw, "project"))
		projectExt := mapStr(raw, "project_id")
		ex := exhibit{
			ID:         "ex-" + shortHash(normalizeProjectKey(name)),
			Name:       name,
			ExternalID: pid,
			Paths:      exhibitPathsFromWorkbench(raw),
		}
		at := -1
		if projectExt != "" {
			at = matchProjectIndex(pf, "", projectExt)
		}
		if at < 0 && projectName != "" {
			at = matchProjectIndex(pf, projectName, "")
		}
		if at < 0 {
			// 本地没有该项目：不引入。
			skipped++
			continue
		}
		p := pf.Projects[at]
		if !hasExhibitNamed(p, name) {
			// 本地尚未实际使用的展项不批量引入（等条目/路径真正用到时再本地登记）。
			skipped++
			continue
		}
		p.Exhibits = mergeExhibits(p.Exhibits, []exhibit{ex})
		p.UpdatedAt = now
		pf.Projects[at] = p
		exUpdated++
	}

	// 有展项的项目自动归为「交付项目」（仅在未显式设置 kind 时）。
	for i := range pf.Projects {
		if pf.Projects[i].Kind == "" && len(pf.Projects[i].Exhibits) > 0 {
			pf.Projects[i].Kind = "交付项目"
		}
	}

	if created > 0 || updated > 0 || exUpdated > 0 {
		if err := writeProjects(root, pf); err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"log_root":      root,
		"total":         len(items),
		"created":       created,
		"updated":       updated,
		"exhibits":      exUpdated,
		"exhibit_total": len(exhibitItems),
		"skipped":       skipped,
		"note":          "仅增补本地已有关联的项目/展项，不对工作台做批量导入",
	}, nil
}

// hasExhibitNamed 判断项目下是否已有同名（或别名）展项。
func hasExhibitNamed(p project, name string) bool {
	key := normalizeProjectKey(name)
	if key == "" {
		return false
	}
	for _, e := range p.Exhibits {
		if normalizeProjectKey(e.Name) == key || aliasMatches(e.Aliases, name) {
			return true
		}
	}
	return false
}

// knownExhibitsOnly 过滤掉项目本地尚未登记的展项，只保留可以增补元数据的已有关联展项。
func knownExhibitsOnly(p project, incoming []exhibit) []exhibit {
	if len(incoming) == 0 {
		return nil
	}
	out := make([]exhibit, 0, len(incoming))
	for _, ex := range incoming {
		if hasExhibitNamed(p, ex.Name) {
			out = append(out, ex)
		}
	}
	return out
}

// registerExhibit 在项目（按名匹配）下按名本地登记一个展项（条目实际用到时调用）。
// 项目不存在或展项已存在时返回 false。
func registerExhibit(pf *projectsFile, projectName, exhibitName string) bool {
	name := strings.TrimSpace(exhibitName)
	key := normalizeProjectKey(projectName)
	if name == "" || key == "" {
		return false
	}
	for i := range pf.Projects {
		if normalizeProjectKey(pf.Projects[i].Name) != key {
			continue
		}
		if hasExhibitNamed(pf.Projects[i], name) {
			return false
		}
		pf.Projects[i].Exhibits = mergeExhibits(pf.Projects[i].Exhibits, []exhibit{{
			ID:      "ex-" + shortHash(normalizeProjectKey(name)),
			Name:    name,
			Aliases: []string{},
		}})
		pf.Projects[i].UpdatedAt = time.Now().Format(time.RFC3339)
		return true
	}
	return false
}

// projectPathsFromWorkbench 从工作台项目对象里解析路径映射（paths 数组或 source_path）。
func projectPathsFromWorkbench(m map[string]any) []projectPath {
	var specs []pathSpec
	if arr, ok := m["paths"].([]any); ok {
		for _, node := range arr {
			switch v := node.(type) {
			case map[string]any:
				specs = append(specs, pathSpec{
					Path:   mapStr(v, "path"),
					Kind:   mapStr(v, "kind"),
					Label:  mapStr(v, "label"),
					Source: firstNonEmpty(mapStr(v, "source"), "workbench"),
				})
			case string:
				specs = append(specs, pathSpec{Path: v, Source: "workbench"})
			}
		}
	}
	if sp := firstNonEmpty(mapStr(m, "source_path"), mapStr(m, "sourcePath")); sp != "" {
		specs = append(specs, pathSpec{Path: sp, Kind: "repo", Source: "workbench"})
	}
	return sanitizePaths(specs)
}

// workbenchExhibitsFromStep 读取上游步骤输出里的展项数组（兼容 {items:[...]} 与直接数组）。
func workbenchExhibitsFromStep(in input, stepID string) []map[string]any {
	stepID = strings.TrimSpace(stepID)
	if stepID == "" {
		return nil
	}
	out := upstreamStepOutput(in, stepID)
	if out == nil {
		return nil
	}
	for _, key := range []string{"items", "exhibits", "list", "data"} {
		if arr, ok := out[key].([]any); ok {
			return asMapList(arr)
		}
	}
	return nil
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
