package main

import (
	"errors"
	"path"
	"runtime"
	"sort"
	"strings"
	"time"
)

// 本文件实现「路径 → 项目/展项」的稳定映射：项目名是展示，路径才是标识。
// 采集条目的对象是会话工作目录对应的仓库根（repo_path）；写入时按最长前缀命中
// 已登记的路径映射，命中不了就留空进入「待认领」，从而不再因目录名/AI 改写而滋生假项目。

// pathSpec 是路径映射的输入形态（能力参数）。
type pathSpec struct {
	Path   string `json:"path"`
	Kind   string `json:"kind,omitempty"`
	Label  string `json:"label,omitempty"`
	Source string `json:"source,omitempty"`
}

// projectPath 是登记表里持久化的路径映射。
type projectPath struct {
	Path   string `json:"path"`
	Kind   string `json:"kind,omitempty"`   // repo | workdir | svn | release | ...
	Label  string `json:"label,omitempty"`  // 人类可读的说明
	Source string `json:"source,omitempty"` // local | workbench | declared
}

// ---------- 路径归一与匹配 ----------

// normalizePathKey 把路径归一到可比较的键：统一分隔符、去掉 Windows 长路径前缀、
// 清理 ./ 与 ..；Windows 上大小写不敏感。
func normalizePathKey(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	p = strings.ReplaceAll(p, "\\", "/")
	for {
		switch {
		case strings.HasPrefix(p, "//?/"):
			p = p[4:]
		case strings.HasPrefix(p, "//./"):
			p = p[4:]
		default:
			goto cleaned
		}
	}
cleaned:
	if p == "" {
		return ""
	}
	p = path.Clean(p)
	if p == "." {
		return ""
	}
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return p
}

// pathMatchScore 给「会话路径 dirKey」与「映射路径 mapKey」算命中分：
// 相等最高，子路径按映射路径长度评分（越长越具体、越优先）；不命中返回 -1。
func pathMatchScore(dirKey, mapKey string) int {
	if dirKey == "" || mapKey == "" {
		return -1
	}
	if dirKey == mapKey {
		return len(mapKey) + 1_000_000
	}
	prefix := strings.TrimSuffix(mapKey, "/") + "/"
	if strings.HasPrefix(dirKey, prefix) {
		return len(mapKey)
	}
	return -1
}

// pathHit 是一次路径命中的结果。
type pathHit struct {
	Project   string
	ProjectID string
	Exhibit   string
	ExhibitID string
	Score     int
}

// matchPathEntry 在登记表里为会话仓库根找最具体的路径映射；展项路径优先于项目路径。
func matchPathEntry(pf projectsFile, dir string) (pathHit, bool) {
	key := normalizePathKey(dir)
	if key == "" {
		return pathHit{}, false
	}
	var best pathHit
	found := false
	consider := func(project project, ex *exhibit) {
		var paths []projectPath
		if ex != nil {
			paths = ex.Paths
		} else {
			paths = project.Paths
		}
		for _, p := range paths {
			score := pathMatchScore(key, normalizePathKey(p.Path))
			if score < 0 {
				continue
			}
			// 更具体者胜；同分时展项优先。
			if found && score < best.Score {
				continue
			}
			if found && score == best.Score && !(ex != nil && best.Exhibit == "") {
				continue
			}
			best = pathHit{Project: project.Name, ProjectID: project.ID, Score: score}
			if ex != nil {
				best.Exhibit = ex.Name
				best.ExhibitID = ex.ID
			}
			found = true
		}
	}
	for _, p := range pf.Projects {
		consider(p, nil)
		for i := range p.Exhibits {
			consider(p, &p.Exhibits[i])
		}
	}
	return best, found
}

// ---------- 路径集合维护 ----------

func sanitizePaths(specs []pathSpec) []projectPath {
	out := make([]projectPath, 0, len(specs))
	seen := map[string]bool{}
	for _, s := range specs {
		key := normalizePathKey(s.Path)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, projectPath{
			Path:   strings.TrimSpace(s.Path),
			Kind:   strings.TrimSpace(s.Kind),
			Label:  strings.TrimSpace(s.Label),
			Source: firstNonEmpty(strings.TrimSpace(s.Source), "local"),
		})
	}
	return out
}

// mergePaths 把 incoming 并入 current（按归一键去重，保留先出现的展示形态）。
func mergePaths(current, incoming []projectPath) []projectPath {
	out := make([]projectPath, 0, len(current)+len(incoming))
	seen := map[string]bool{}
	for _, p := range append(append([]projectPath{}, current...), incoming...) {
		key := normalizePathKey(p.Path)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, p)
	}
	return out
}

func removePaths(current []projectPath, remove []string) []projectPath {
	if len(remove) == 0 {
		return current
	}
	drop := map[string]bool{}
	for _, r := range remove {
		if key := normalizePathKey(r); key != "" {
			drop[key] = true
		}
	}
	out := make([]projectPath, 0, len(current))
	for _, p := range current {
		if drop[normalizePathKey(p.Path)] {
			continue
		}
		out = append(out, p)
	}
	return out
}

func pathsToMaps(paths []projectPath) []map[string]any {
	out := make([]map[string]any, 0, len(paths))
	for _, p := range paths {
		out = append(out, map[string]any{
			"path":   p.Path,
			"kind":   p.Kind,
			"label":  p.Label,
			"source": p.Source,
		})
	}
	return out
}

// findProjectIndex 按 ID 或名称/别名找项目下标，找不到返回 -1。
func findProjectIndex(pf projectsFile, id, name string) int {
	at := -1
	if id != "" {
		for i := range pf.Projects {
			if pf.Projects[i].ID == id {
				at = i
				break
			}
		}
	}
	if at >= 0 {
		return at
	}
	resolved := name
	if canonical, ok := resolveProjectName(projectIndex(pf), name); ok {
		resolved = canonical
	}
	if strings.TrimSpace(resolved) == "" {
		return -1
	}
	for i := range pf.Projects {
		if strings.EqualFold(pf.Projects[i].Name, resolved) {
			return i
		}
		for _, a := range pf.Projects[i].Aliases {
			if strings.EqualFold(a, resolved) {
				return i
			}
		}
	}
	return -1
}

func findExhibitIndex(p project, id, name string) int {
	if id != "" {
		for i := range p.Exhibits {
			if p.Exhibits[i].ID == id {
				return i
			}
		}
	}
	if name != "" {
		for i := range p.Exhibits {
			if strings.EqualFold(p.Exhibits[i].Name, name) || aliasMatches(p.Exhibits[i].Aliases, name) {
				return i
			}
		}
	}
	return -1
}

// ---------- 能力：路径映射管理 ----------

// projectPathSetCap 管理项目/展项的路径映射。给定 Paths 时整组替换；否则用 AddPaths/RemovePaths 增量修改。
func projectPathSetCap(in input) (any, error) {
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
	at := findProjectIndex(pf, in.ProjectID, firstNonEmpty(in.ProjectName, in.Project))
	if at < 0 {
		return nil, errors.New("未找到目标项目：请提供有效的 project_id 或 project_name")
	}
	now := time.Now().Format(time.RFC3339)
	exAt := findExhibitIndex(pf.Projects[at], in.ExhibitID, in.ExhibitName)
	if strings.TrimSpace(in.ExhibitID+in.ExhibitName) != "" && exAt < 0 {
		return nil, errors.New("未找到目标展项：请提供有效的 exhibit_id 或 exhibit_name")
	}

	apply := func(current []projectPath) []projectPath {
		next := current
		if in.Paths != nil {
			next = sanitizePaths(in.Paths)
		} else {
			next = mergePaths(next, sanitizePaths(in.AddPaths))
			next = removePaths(next, in.RemovePaths)
		}
		return next
	}

	if exAt >= 0 {
		pf.Projects[at].Exhibits[exAt].Paths = apply(pf.Projects[at].Exhibits[exAt].Paths)
	} else {
		pf.Projects[at].Paths = apply(pf.Projects[at].Paths)
	}
	pf.Projects[at].UpdatedAt = now
	if err := writeProjects(root, pf); err != nil {
		return nil, err
	}
	result := map[string]any{
		"log_root": root,
		"project":  pf.Projects[at],
	}
	if exAt >= 0 {
		result["exhibit"] = pf.Projects[at].Exhibits[exAt]
	}
	return result, nil
}

// projectMapPathCap 只读预览：给一个路径，返回会命中哪个项目/展项。
func projectMapPathCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	pf, err := readProjects(root)
	if err != nil {
		return nil, err
	}
	raw := firstNonEmpty(in.RepoPath, in.Path)
	hit, ok := matchPathEntry(pf, raw)
	matchType := "none"
	if ok {
		if hit.Exhibit != "" {
			matchType = "exhibit_path"
		} else {
			matchType = "project_path"
		}
	}
	candidates := make([]map[string]any, 0, len(pf.Projects))
	for _, p := range pf.Projects {
		candidates = append(candidates, map[string]any{"id": p.ID, "name": p.Name})
	}
	return map[string]any{
		"log_root":   root,
		"path":       raw,
		"path_key":   normalizePathKey(raw),
		"matched":    ok,
		"match_type": matchType,
		"project":    hit.Project,
		"project_id": hit.ProjectID,
		"exhibit":    hit.Exhibit,
		"exhibit_id": hit.ExhibitID,
		"candidates": candidates,
	}, nil
}

// projectClaimCap 认领一个仓库路径：写入到目标项目/展项，并回填历史条目（按路径前缀）。
func projectClaimCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	repoPath := firstNonEmpty(in.RepoPath, in.Path)
	if normalizePathKey(repoPath) == "" {
		return nil, errors.New("需要 repo_path（要认领的仓库/目录路径）")
	}
	unlock := lockRoot(root)
	defer unlock()
	pf, err := readProjects(root)
	if err != nil {
		return nil, err
	}
	now := time.Now().Format(time.RFC3339)

	at := findProjectIndex(pf, in.ProjectID, firstNonEmpty(in.ProjectName, in.Project))
	if at < 0 {
		create := in.CreateTarget == nil || *in.CreateTarget
		name := strings.TrimSpace(firstNonEmpty(in.ProjectName, in.Project))
		if !create || name == "" {
			return nil, errors.New("未找到目标项目：请提供有效的 project_id/project_name，或允许创建")
		}
		pf.Projects = append(pf.Projects, project{
			ID:        makeProjectID(name),
			Name:      name,
			Aliases:   []string{},
			Status:    "active",
			CreatedAt: now,
			UpdatedAt: now,
		})
		at = len(pf.Projects) - 1
	}

	target := pf.Projects[at]
	exAt := findExhibitIndex(target, in.ExhibitID, in.ExhibitName)
	if strings.TrimSpace(in.ExhibitID+in.ExhibitName) != "" && exAt < 0 {
		create := in.CreateTarget == nil || *in.CreateTarget
		name := strings.TrimSpace(in.ExhibitName)
		if !create || name == "" {
			return nil, errors.New("未找到目标展项：请提供有效的 exhibit_id/exhibit_name，或允许创建")
		}
		target.Exhibits = append(target.Exhibits, exhibit{ID: "ex-" + shortHash(normalizeProjectKey(name)), Name: name})
		exAt = len(target.Exhibits) - 1
	}

	spec := sanitizePaths([]pathSpec{{Path: repoPath, Kind: "repo", Source: "local"}})
	if exAt >= 0 {
		target.Exhibits[exAt].Paths = mergePaths(target.Exhibits[exAt].Paths, spec)
	} else {
		target.Paths = mergePaths(target.Paths, spec)
	}
	target.UpdatedAt = now
	pf.Projects[at] = target
	if err := writeProjects(root, pf); err != nil {
		return nil, err
	}

	// 回填历史条目：仓库根命中已认领路径（相等或子路径）。
	claimKey := normalizePathKey(repoPath)
	updated := 0
	touched := make([]string, 0)
	dates, err := listDates(root)
	if err != nil {
		return nil, err
	}
	var exhibitName, exhibitID string
	if exAt >= 0 {
		exhibitName = target.Exhibits[exAt].Name
		exhibitID = target.Exhibits[exAt].ID
	}
	for _, date := range dates {
		day, err := readDay(root, date)
		if err != nil {
			return nil, err
		}
		changed := false
		for i := range day.Entries {
			item := &day.Entries[i]
			if pathMatchScore(normalizePathKey(item.RepoPath), claimKey) < 0 {
				continue
			}
			item.Project = target.Name
			item.ProjectID = target.ID
			if exhibitName != "" {
				item.Exhibit = exhibitName
				item.ExhibitID = exhibitID
			}
			item.ProjectHint = ""
			item.UpdatedAt = now
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
	result := map[string]any{
		"log_root": root,
		"project":  target.Name,
		"updated":  updated,
		"dates":    touched,
	}
	if exAt >= 0 {
		result["exhibit"] = exhibitName
	}
	return result, nil
}

// projectMergeCap 合并项目：把一个或多个来源项目的别名/路径/展项并入目标项目，
// 删除来源登记，并改写历史条目的归类。支持 from_project（单个）与 from_projects（批量）。
func projectMergeCap(in input) (any, error) {
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
	toAt := findProjectIndex(pf, in.ProjectID, firstNonEmpty(in.ToProject, in.ProjectName))
	if toAt < 0 {
		return nil, errors.New("未找到目标项目（to_project）")
	}
	// 收集来源：from_projects 批量优先，兼容 from_project / project。
	rawSources := make([]string, 0, len(in.FromProjects)+1)
	for _, s := range in.FromProjects {
		if t := strings.TrimSpace(s); t != "" {
			rawSources = append(rawSources, t)
		}
	}
	if single := firstNonEmpty(in.FromProject, in.Project); single != "" {
		rawSources = append(rawSources, single)
	}
	if len(rawSources) == 0 {
		return nil, errors.New("需要 from_project 或 from_projects（来源项目）")
	}
	fromSet := map[int]bool{}
	for _, s := range rawSources {
		if at := findProjectIndex(pf, "", s); at >= 0 {
			fromSet[at] = true
		}
	}
	delete(fromSet, toAt) // 目标不能作为来源。
	if len(fromSet) == 0 {
		return map[string]any{"log_root": root, "merged": false, "reason": "未找到来源项目"}, nil
	}
	sources := make([]project, 0, len(fromSet))
	for at := range fromSet {
		sources = append(sources, pf.Projects[at])
	}
	sort.SliceStable(sources, func(i, j int) bool { return sources[i].Name < sources[j].Name })

	target := pf.Projects[toAt]
	now := time.Now().Format(time.RFC3339)
	for _, src := range sources {
		target.Aliases = mergeAliases(target.Aliases, append([]string{src.Name}, src.Aliases...))
		target.Aliases = filterOut(target.Aliases, target.Name)
		target.Paths = mergePaths(target.Paths, src.Paths)
		target.Exhibits = mergeExhibits(target.Exhibits, src.Exhibits)
		if target.ExternalID == "" {
			target.ExternalID = src.ExternalID
		}
	}
	target.UpdatedAt = now
	pf.Projects[toAt] = target

	// 删除所有来源项目。
	kept := make([]project, 0, len(pf.Projects)-len(fromSet))
	for i, p := range pf.Projects {
		if fromSet[i] {
			continue
		}
		kept = append(kept, p)
	}
	pf.Projects = kept
	if err := writeProjects(root, pf); err != nil {
		return nil, err
	}

	// 改写历史条目：来源项目名/ID/别名 → 目标项目。
	sourceIDs := map[string]bool{}
	sourceNames := map[string]bool{}
	sourceAliases := map[string]bool{}
	for _, src := range sources {
		if src.ID != "" {
			sourceIDs[src.ID] = true
		}
		sourceNames[normalizeProjectKey(src.Name)] = true
		for _, a := range src.Aliases {
			sourceAliases[normalizeProjectKey(a)] = true
		}
	}
	updated := 0
	touched := make([]string, 0)
	dates, err := listDates(root)
	if err != nil {
		return nil, err
	}
	for _, date := range dates {
		day, err := readDay(root, date)
		if err != nil {
			return nil, err
		}
		changed := false
		for i := range day.Entries {
			item := &day.Entries[i]
			key := normalizeProjectKey(item.Project)
			match := (item.ProjectID != "" && sourceIDs[item.ProjectID]) ||
				sourceNames[key] || sourceAliases[key]
			if !match {
				continue
			}
			item.Project = target.Name
			item.ProjectID = target.ID
			item.UpdatedAt = now
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
	names := make([]string, 0, len(sources))
	for _, src := range sources {
		names = append(names, src.Name)
	}
	return map[string]any{
		"log_root": root,
		"merged":   true,
		"from":     strings.Join(names, ", "),
		"to":       target.Name,
		"updated":  updated,
		"dates":    touched,
	}, nil
}

func filterOut(values []string, drop string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if strings.EqualFold(strings.TrimSpace(v), strings.TrimSpace(drop)) {
			continue
		}
		out = append(out, v)
	}
	return out
}

// pendingPathStat 汇总一个「待认领」路径的条目情况。
type pendingPathStat struct {
	count int
	days  map[string]struct{}
	first string
	last  string
	hints map[string]int
}

// collectPendingPaths 汇总未归类（有 repo_path 但项目为空）的条目，按路径聚合。
func collectPendingPaths(entries []entry) []map[string]any {
	stats := map[string]*pendingPathStat{}
	display := map[string]string{}
	for _, item := range entries {
		key := normalizePathKey(item.RepoPath)
		if key == "" || strings.TrimSpace(item.Project) != "" {
			continue
		}
		stat := stats[key]
		if stat == nil {
			stat = &pendingPathStat{days: map[string]struct{}{}, hints: map[string]int{}}
			stats[key] = stat
			display[key] = strings.TrimSpace(item.RepoPath)
		}
		stat.count++
		stat.days[item.Date] = struct{}{}
		if stat.first == "" || item.Date < stat.first {
			stat.first = item.Date
		}
		if item.Date > stat.last {
			stat.last = item.Date
		}
		if hint := strings.TrimSpace(item.ProjectHint); hint != "" {
			stat.hints[hint]++
		}
	}
	out := make([]map[string]any, 0, len(stats))
	for key, stat := range stats {
		hint := ""
		best := 0
		for h, c := range stat.hints {
			if c > best || (c == best && h < hint) {
				hint, best = h, c
			}
		}
		out = append(out, map[string]any{
			"path":         display[key],
			"path_key":     key,
			"entry_count":  stat.count,
			"active_days":  len(stat.days),
			"first_date":   stat.first,
			"last_date":    stat.last,
			"project_hint": hint,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if mapInt(out[i]["entry_count"]) != mapInt(out[j]["entry_count"]) {
			return mapInt(out[i]["entry_count"]) > mapInt(out[j]["entry_count"])
		}
		return mapString(out[i]["path"]) < mapString(out[j]["path"])
	})
	return out
}
