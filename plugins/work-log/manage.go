package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// 本文件实现「登记管理增强」与「存储稳健性」相关能力：
//   - work-log.entry.patch   单条条目的字段级局部更新（只改传入的键，不注入默认值）
//   - work-log.project.rename       项目改名（保持稳定 ID，回绑历史条目）
//   - work-log.project.autofill     从 AI 会话素材自动补全项目路径（可预览）
//   - work-log.project.export / import  登记表导出与导入（备份 / 迁移 / 跨机复制）
//   - work-log.archive.migrate          把历史文件补齐到当前 schema（一次性维护）
// 以及对应的迁移函数 migrateProjectsFile / migrateDayFile。

// ---------- 条目局部更新 ----------

// entryPatchCap 对单条条目做字段级更新：只有 patch 里出现的键会被改写，
// 其余字段（含 source/category 默认值）保持不变，便于在视图里就地修正功能/项目。
func entryPatchCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	date := strings.TrimSpace(in.Date)
	if date == "" {
		return nil, errors.New("需要 date（条目所属日期）")
	}
	id := strings.TrimSpace(in.EntryID)
	if id == "" {
		return nil, errors.New("需要 entry_id")
	}
	if len(in.Patch) == 0 {
		return nil, errors.New("patch 不能为空：请至少提供一个要修改的字段")
	}
	unlock := lockRoot(root)
	defer unlock()
	day, err := readDay(root, date)
	if err != nil {
		return nil, err
	}
	at := -1
	for i := range day.Entries {
		if day.Entries[i].EntryID == id {
			at = i
			break
		}
	}
	if at < 0 {
		return nil, errors.New("未找到该条目：" + id)
	}
	item := day.Entries[at]
	relink := false
	for key, raw := range in.Patch {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "project":
			item.Project = asText(raw)
			relink = true
		case "feature":
			item.Feature = asText(raw)
		case "exhibit":
			item.Exhibit = asText(raw)
			relink = true
		case "category":
			item.Category = asText(raw)
		case "title":
			item.Title = asText(raw)
		case "summary":
			item.Summary = asText(raw)
		case "minutes":
			item.Minutes = asInt(raw)
		case "minutes_source":
			item.MinutesSource = asText(raw)
		case "repo_path":
			item.RepoPath = asText(raw)
		case "tags":
			item.Tags = nonNilStrings(asTextList(raw))
		default:
			// 未知键直接忽略，避免拼写错误导致误改。
			continue
		}
	}
	if item.Title == "" {
		return nil, errors.New("标题不能为空")
	}
	item.UpdatedAt = time.Now().Format(time.RFC3339)
	day.Entries[at] = item
	if relink {
		if err := rebindEntryProject(root, &day.Entries[at]); err != nil {
			return nil, err
		}
	} else if pf, err := readProjects(root); err == nil {
		enrichEntryLinks(pf, day.Entries[at:at+1])
	}
	if err := writeDay(root, day); err != nil {
		return nil, err
	}
	return map[string]any{
		"log_root": root,
		"date":     date,
		"entry":    day.Entries[at],
	}, nil
}

// rebindEntryProject 显式设置项目名后重新绑定项目/展项 ID：命中登记表就归一，
// 没命中就把用户显式输入的名字登记为新项目（这是人工确认的动作，不是"猜"）。
func rebindEntryProject(root string, item *entry) error {
	name := strings.TrimSpace(item.Project)
	if name == "" {
		item.ProjectID = ""
		item.ExhibitID = ""
		return nil
	}
	pf, err := readProjects(root)
	if err != nil {
		return err
	}
	index := projectIndex(pf)
	if canonical, ok := resolveProjectName(index, name); ok {
		item.Project = canonical
	} else if registerProject(&pf, index, name) {
		if err := writeProjects(root, pf); err != nil {
			return err
		}
		if pf, err = readProjects(root); err != nil {
			return err
		}
	}
	for _, p := range pf.Projects {
		if !strings.EqualFold(p.Name, item.Project) {
			continue
		}
		item.ProjectID = p.ID
		ex := strings.TrimSpace(item.Exhibit)
		if ex == "" {
			item.ExhibitID = ""
			return nil
		}
		for _, e := range p.Exhibits {
			if strings.EqualFold(e.Name, ex) || aliasMatches(e.Aliases, ex) {
				item.Exhibit = e.Name
				item.ExhibitID = e.ID
				return nil
			}
		}
		item.ExhibitID = ""
		return nil
	}
	item.ProjectID = ""
	return nil
}

// ---------- 登记表导出 / 导入 ----------

// projectExportCap 导出完整登记表（含别名、路径、展项），供备份或跨机迁移。
func projectExportCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	pf, err := readProjects(root)
	if err != nil {
		return nil, err
	}
	pf.SchemaVersion = projectsSchemaVersion
	payload, err := json.MarshalIndent(pf, "", "  ")
	if err != nil {
		return nil, errors.New("登记表序列化失败")
	}
	return map[string]any{
		"log_root":       root,
		"schema_version": projectsSchemaVersion,
		"count":          len(pf.Projects),
		"projects":       pf.Projects,
		"json":           string(payload),
	}, nil
}

// projectImportCap 导入登记表：merge（默认，按名称/外部 ID 合并并补齐路径/别名/展项）
// 或 replace（整体替换）。导入来源是 work-log.project.export 产出的 JSON 文本。
func projectImportCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(in.ProjectsJSON)
	if raw == "" {
		return nil, errors.New("需要 projects_json（登记表 JSON 文本）")
	}
	var incoming projectsFile
	if err := json.Unmarshal([]byte(raw), &incoming); err != nil {
		// 也接受直接给一个项目数组。
		var list []project
		if err2 := json.Unmarshal([]byte(raw), &list); err2 != nil {
			return nil, errors.New("导入内容不是合法的登记表 JSON")
		}
		incoming.Projects = list
	}
	unlock := lockRoot(root)
	defer unlock()
	pf, err := readProjects(root)
	if err != nil {
		return nil, err
	}
	mode := strings.ToLower(strings.TrimSpace(in.Mode))
	if mode == "replace" {
		pf.Projects = nonNilProjects(incoming.Projects)
		if err := migrateProjectsFile(&pf); err != nil {
			return nil, err
		}
		if err := writeProjects(root, pf); err != nil {
			return nil, err
		}
		return map[string]any{"log_root": root, "mode": "replace", "imported": len(pf.Projects)}, nil
	}
	now := time.Now().Format(time.RFC3339)
	created, updated := 0, 0
	for _, src := range incoming.Projects {
		name := strings.TrimSpace(src.Name)
		if name == "" {
			continue
		}
		at := matchProjectIndex(pf, name, src.ExternalID)
		if at < 0 {
			src.CreatedAt = firstNonEmpty(src.CreatedAt, now)
			src.UpdatedAt = now
			pf.Projects = append(pf.Projects, src)
			created++
			continue
		}
		p := pf.Projects[at]
		p.Aliases = mergeAliases(p.Aliases, src.Aliases)
		p.Paths = mergePaths(p.Paths, src.Paths)
		p.Exhibits = mergeExhibits(p.Exhibits, src.Exhibits)
		if p.ExternalID == "" {
			p.ExternalID = src.ExternalID
		}
		if p.Source == "" {
			p.Source = src.Source
		}
		if p.Description == "" {
			p.Description = src.Description
		}
		p.UpdatedAt = now
		pf.Projects[at] = p
		updated++
	}
	if err := migrateProjectsFile(&pf); err != nil {
		return nil, err
	}
	if err := writeProjects(root, pf); err != nil {
		return nil, err
	}
	return map[string]any{
		"log_root": root,
		"mode":     "merge",
		"created":  created,
		"updated":  updated,
	}, nil
}

func nonNilProjects(items []project) []project {
	out := make([]project, 0, len(items))
	for _, p := range items {
		if strings.TrimSpace(p.Name) == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// ---------- 项目改名 ----------

// projectRenameCap 修改项目的展示名：保持稳定 ID 不变，并把历史条目里按旧名/旧别名
// 归类的条目回绑到新名，避免改名后老条目变成未归类。新名若已被其它项目（含别名）
// 占用则报错，提示改用「并入」而不是静默覆盖。
func projectRenameCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	newName := strings.TrimSpace(in.NewName)
	if newName == "" {
		return nil, errors.New("需要 new_name（新的项目名）")
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
	oldName := pf.Projects[at].Name
	if normalizeProjectKey(oldName) == normalizeProjectKey(newName) {
		return map[string]any{
			"log_root": root,
			"renamed":  false,
			"reason":   "名称未变化",
			"project":  pf.Projects[at],
		}, nil
	}
	if canonical, ok := resolveProjectName(projectIndex(pf), newName); ok && !strings.EqualFold(canonical, oldName) {
		return nil, errors.New("名称已被「" + canonical + "」占用，请改用「并入」合并到该项目")
	}
	now := time.Now().Format(time.RFC3339)
	oldAliases := append([]string{}, pf.Projects[at].Aliases...)
	pf.Projects[at].Name = newName
	pf.Projects[at].UpdatedAt = now
	targetID := pf.Projects[at].ID
	if err := writeProjects(root, pf); err != nil {
		return nil, err
	}

	// 回绑历史条目：按 ID / 旧名 / 旧别名命中即改写到新名。
	oldKey := normalizeProjectKey(oldName)
	aliasKeys := map[string]bool{}
	for _, a := range oldAliases {
		if k := normalizeProjectKey(a); k != "" {
			aliasKeys[k] = true
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
			match := (targetID != "" && item.ProjectID == targetID) ||
				normalizeProjectKey(item.Project) == oldKey ||
				aliasKeys[normalizeProjectKey(item.Project)]
			if !match {
				continue
			}
			item.Project = newName
			item.ProjectID = targetID
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
	return map[string]any{
		"log_root": root,
		"renamed":  true,
		"from":     oldName,
		"to":       newName,
		"project":  pf.Projects[at],
		"updated":  updated,
		"dates":    touched,
	}, nil
}

// ---------- 会话记录自动填充路径 ----------

// materialSession 是会话素材里与会话路径相关的最小字段集。
type materialSession struct {
	Project   string `json:"project"`
	Directory string `json:"directory"`
	RepoPath  string `json:"repo_path"`
}

type materialFile struct {
	Sessions []materialSession `json:"sessions"`
}

// sessionMaterialDir 解析「AI 会话素材」目录：显式入参 > 采集器私有数据目录 material/。
func sessionMaterialDir(value string) (string, error) {
	if v := strings.TrimSpace(value); v != "" {
		abs, err := filepath.Abs(v)
		if err != nil {
			return "", errors.New("material_root 无效")
		}
		return abs, nil
	}
	base := strings.TrimSpace(os.Getenv("HIMIND_PLUGIN_DATA_ROOT"))
	if base == "" {
		if local := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); local != "" {
			base = filepath.Join(local, "HiMindAgent", "plugin-data")
		} else {
			base = filepath.Join(os.TempDir(), "HiMindAgent", "plugin-data")
		}
		return filepath.Join(base, "com.himind.work-log-collector", "material"), nil
	}
	// HIMIND_PLUGIN_DATA_ROOT 指向本插件的私有数据目录（…/plugin-data/com.himind.work-log），
	// 采集器是其同级插件。素材目录在 plugin-data 下的同级目录里；若某些宿主把它设成了
	// 共享的 plugin-data 根，则退回同名嵌套目录。两种布局都探测，取其存在者。
	candidates := []string{
		filepath.Join(filepath.Dir(base), "com.himind.work-log-collector", "material"),
		filepath.Join(base, "com.himind.work-log-collector", "material"),
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c, nil
		}
	}
	return candidates[0], nil
}

// projectAutofillCap 从 AI 工具会话记录（采集器落盘的会话素材）里「搜一轮」，为还没有
// 绑定路径的登记项目自动补上仓库路径：会话里出现过的「项目名 → 工作目录/仓库根」，
// 按名称（含别名）或路径末级目录名匹配到登记项目，命中即写入（source=session）。
// dry_run 时只预览；只补空，绝不覆盖已有路径。
func projectAutofillCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	dir, err := sessionMaterialDir(in.MaterialRoot)
	if err != nil {
		return nil, err
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		return map[string]any{
			"log_root":       root,
			"material_dir":   dir,
			"found_material": false,
			"filled":         []any{},
			"filled_count":   0,
			"candidates":     []any{},
			"message":        "未找到会话素材目录，请先运行一次采集：" + dir,
		}, nil
	}

	// 汇总 归一项目名 → 候选路径（出现次数最多者胜，次数相同取更长的路径）。
	type cand struct {
		path  string
		count int
	}
	best := map[string]cand{}
	display := map[string]string{}
	scanned := 0
	for _, f := range files {
		if f.IsDir() || !strings.HasPrefix(f.Name(), "session-digest-") || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			continue
		}
		var mf materialFile
		if json.Unmarshal(body, &mf) != nil {
			continue
		}
		scanned++
		for _, s := range mf.Sessions {
			p := firstNonEmpty(s.RepoPath, s.Directory)
			if normalizePathKey(p) == "" {
				continue
			}
			label := strings.TrimSpace(s.Project)
			if label == "" {
				label = filepath.Base(filepath.Clean(p))
			}
			key := normalizeProjectKey(label)
			if key == "" {
				continue
			}
			cur := best[key]
			switch {
			case cur.path == "" || cur.path == p:
				cur = cand{path: p, count: cur.count + 1}
			case len(p) > len(cur.path):
				cur = cand{path: p, count: 1}
			}
			best[key] = cur
			if _, ok := display[key]; !ok {
				display[key] = label
			}
		}
	}

	unlock := lockRoot(root)
	defer unlock()
	pf, err := readProjects(root)
	if err != nil {
		return nil, err
	}
	dryRun := in.DryRun != nil && *in.DryRun
	now := time.Now().Format(time.RFC3339)
	index := projectIndex(pf)
	nameAt := map[string]int{}
	for i := range pf.Projects {
		nameAt[pf.Projects[i].Name] = i
	}
	filled := make([]map[string]any, 0)
	matched := map[string]bool{}
	for key, c := range best {
		label := display[key]
		canonical, ok := resolveProjectName(index, label)
		if !ok {
			canonical, ok = resolveProjectName(index, filepath.Base(filepath.Clean(c.path)))
		}
		if !ok {
			continue
		}
		at, ok := nameAt[canonical]
		if !ok || len(pf.Projects[at].Paths) > 0 {
			continue // 只补空，不覆盖已有路径。
		}
		matched[key] = true
		filled = append(filled, map[string]any{
			"project_id": pf.Projects[at].ID,
			"project":    pf.Projects[at].Name,
			"path":       c.path,
			"from":       label,
		})
		if !dryRun {
			pf.Projects[at].Paths = mergePaths(pf.Projects[at].Paths, sanitizePaths([]pathSpec{{Path: c.path, Kind: "repo", Source: "session"}}))
			pf.Projects[at].UpdatedAt = now
		}
	}

	candidates := make([]map[string]any, 0, len(best))
	for key, c := range best {
		candidates = append(candidates, map[string]any{
			"project": display[key],
			"path":    c.path,
			"count":   c.count,
			"matched": matched[key],
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return asInt(candidates[i]["count"]) > asInt(candidates[j]["count"])
	})
	if !dryRun && len(filled) > 0 {
		if err := writeProjects(root, pf); err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"log_root":       root,
		"material_dir":   dir,
		"found_material": scanned > 0,
		"files":          scanned,
		"dry_run":        dryRun,
		"filled":         filled,
		"filled_count":   len(filled),
		"candidates":     candidates,
	}, nil
}

// ---------- schema 迁移 ----------

// migrateProjectsFile 把登记表补齐到当前 schema：戳版本、补 ID、补状态。
// 项目 ID 由名称确定性推导，因此读时迁移是幂等的、可安全地只改内存。
func migrateProjectsFile(pf *projectsFile) error {
	pf.SchemaVersion = projectsSchemaVersion
	if pf.Projects == nil {
		pf.Projects = []project{}
	}
	for i := range pf.Projects {
		p := &pf.Projects[i]
		if strings.TrimSpace(p.Name) == "" {
			continue
		}
		if strings.TrimSpace(p.ID) == "" {
			p.ID = makeProjectID(p.Name)
		}
		if strings.TrimSpace(p.Status) == "" {
			p.Status = "active"
		}
		if p.Aliases == nil {
			p.Aliases = []string{}
		}
		for j := range p.Exhibits {
			ex := &p.Exhibits[j]
			if strings.TrimSpace(ex.Name) != "" && strings.TrimSpace(ex.ID) == "" {
				ex.ID = "ex-" + shortHash(normalizeProjectKey(ex.Name))
			}
		}
	}
	return nil
}

// migrateDayFile 把某天的日志补齐到当前 schema。只做确定性、幂等的修正
// （版本戳、条目日期），不生成新身份，避免破坏去重键。
func migrateDayFile(day *dayFile) {
	day.SchemaVersion = schemaVersion
	if day.Entries == nil {
		day.Entries = []entry{}
	}
	for i := range day.Entries {
		if strings.TrimSpace(day.Entries[i].Date) == "" {
			day.Entries[i].Date = day.Date
		}
	}
	day.Count = len(day.Entries)
}

// archiveMigrateCap 一次性维护：把登记表与所有日文件补齐到当前 schema，可 dry_run 预览。
func archiveMigrateCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	dryRun := in.DryRun != nil && *in.DryRun
	unlock := lockRoot(root)
	defer unlock()

	// 直接读原始文件，避免与读时迁移混淆（读时迁移不落盘，会掩盖真实改动）。
	projectsChanged := false
	if body, err := os.ReadFile(projectsPath(root)); err == nil && len(body) > 0 {
		var raw projectsFile
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, errors.New("项目登记不是合法 JSON")
		}
		before, _ := json.Marshal(raw)
		if err := migrateProjectsFile(&raw); err != nil {
			return nil, err
		}
		after, _ := json.Marshal(raw)
		projectsChanged = string(before) != string(after)
		if projectsChanged && !dryRun {
			if err := writeProjects(root, raw); err != nil {
				return nil, err
			}
		}
	}

	dates, err := listDates(root)
	if err != nil {
		return nil, err
	}
	daysChanged := 0
	for _, date := range dates {
		body, err := os.ReadFile(dayPath(root, date))
		if err != nil {
			continue
		}
		var raw dayFile
		if err := json.Unmarshal(body, &raw); err != nil {
			continue
		}
		raw.Date = date
		before, _ := json.Marshal(raw)
		migrateDayFile(&raw)
		after, _ := json.Marshal(raw)
		if string(before) == string(after) {
			continue
		}
		daysChanged++
		if !dryRun {
			if err := writeDay(root, raw); err != nil {
				return nil, err
			}
		}
	}
	return map[string]any{
		"log_root":         root,
		"dry_run":          dryRun,
		"projects_changed": projectsChanged,
		"days_scanned":     len(dates),
		"days_changed":     daysChanged,
	}, nil
}

// ---------- 小工具 ----------

func asText(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	default:
		return strings.TrimSpace(jsonScalar(v))
	}
}

func jsonScalar(value any) string {
	body, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return strings.Trim(string(body), `"`)
}

func asInt(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n)
		}
	case string:
		var n int
		for _, ch := range strings.TrimSpace(v) {
			if ch < '0' || ch > '9' {
				return n
			}
			n = n*10 + int(ch-'0')
		}
		return n
	}
	return 0
}

func asTextList(value any) []string {
	switch v := value.(type) {
	case nil:
		return nil
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, node := range v {
			if s := asText(node); s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		parts := strings.Split(v, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if s := strings.TrimSpace(p); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
