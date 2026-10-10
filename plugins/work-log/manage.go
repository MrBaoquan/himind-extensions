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

// ephemeralProjectPath 判断会话工作目录是否是「临时/缓存/草稿」路径，不应作为项目主路径
// 自动绑定：系统临时目录、Copilot 的工作树与聊天缓存、Codex 的任务草稿目录等。真正的
// 项目仓库（哪怕是缓存克隆，如 .copilot/repos/<repo>）不在其列。
func ephemeralProjectPath(p string) bool {
	key := normalizePathKey(p)
	if key == "" {
		return false
	}
	for _, t := range []string{os.TempDir(), os.Getenv("TEMP"), os.Getenv("TMP")} {
		tk := normalizePathKey(t)
		if tk != "" && (key == tk || strings.HasPrefix(key, tk+"/")) {
			return true
		}
	}
	scratch := []string{
		"appdata/local/temp/",
		"copilot-worktrees/",
		".copilot/chats/",
		".copilot/workspaces/",
		"documents/codex/",
	}
	for _, s := range scratch {
		if strings.Contains(key, s) {
			return true
		}
	}
	return false
}

// worktreeMainRoot 若路径位于链接工作树里（`.git` 是文件，内容为
// `gitdir: <主仓库>/.git/worktrees/<名>`），返回主仓库工作树根；否则返回空。
func worktreeMainRoot(dir string) string {
	for {
		gi := filepath.Join(dir, ".git")
		info, err := os.Stat(gi)
		if err == nil {
			if info.IsDir() {
				return ""
			}
			body, err := os.ReadFile(gi)
			if err != nil {
				return ""
			}
			line := strings.TrimSpace(string(body))
			line = strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
			if line == "" {
				return ""
			}
			norm := filepath.ToSlash(line)
			if idx := strings.Index(strings.ToLower(norm), "/.git/"); idx > 0 {
				return filepath.Clean(filepath.FromSlash(norm[:idx]))
			}
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// hasRepoMarker 判断路径本身是否像一个仓库/工程根（git/svn/hg 标记或工作日志自述清单）。
func hasRepoMarker(p string) bool {
	clean := filepath.Clean(p)
	for _, marker := range []string{".git", ".svn", ".hg"} {
		if _, err := os.Stat(filepath.Join(clean, marker)); err == nil {
			return true
		}
	}
	if _, err := os.Stat(filepath.Join(clean, ".himind", "project.json")); err == nil {
		return true
	}
	return false
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

	// 汇总 归一项目名 → 候选路径：优先非临时路径、其次出现次数多者、再取更短的规范根。
	// 链接工作树（如 Copilot copilot-worktrees）先解析到主仓库工作树，避免绑定 C 盘缓存。
	type cand struct {
		path  string
		count int
	}
	best := map[string]cand{}
	display := map[string]string{}
	skippedEphemeral := map[string]string{}
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
			if main := worktreeMainRoot(p); main != "" {
				p = main
			}
			label := strings.TrimSpace(s.Project)
			if label == "" {
				label = filepath.Base(filepath.Clean(p))
			}
			key := normalizeProjectKey(label)
			if key == "" {
				continue
			}
			if _, ok := display[key]; !ok {
				display[key] = label
			}
			if ephemeralProjectPath(p) {
				skippedEphemeral[key] = p
				continue
			}
			cur := best[key]
			switch {
			case cur.path == "" || cur.path == p:
				cur = cand{path: p, count: cur.count + 1}
			case preferCandidatePath(p, cur.path):
				cur = cand{path: p, count: 1}
			}
			best[key] = cur
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
	ignored := make([]map[string]any, 0, len(skippedEphemeral))
	for key, p := range skippedEphemeral {
		if _, bonded := best[key]; bonded {
			continue // 该项目另有可用路径，就不必再列忽略项。
		}
		ignored = append(ignored, map[string]any{"project": display[key], "path": p, "reason": "临时/缓存路径"})
	}
	if !dryRun && len(filled) > 0 {
		if err := writeProjects(root, pf); err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"log_root":          root,
		"material_dir":      dir,
		"found_material":    scanned > 0,
		"files":             scanned,
		"dry_run":           dryRun,
		"filled":            filled,
		"filled_count":      len(filled),
		"candidates":        candidates,
		"ignored_ephemeral": ignored,
		"ignored_count":     len(ignored),
	}, nil
}

// preferCandidatePath 在同一个项目的两个候选路径间择优：优先更像仓库/工程根的，其次取
// 更短的规范根（避免选到深层子目录或缓存工作树）。
func preferCandidatePath(cand, cur string) bool {
	if cur == "" {
		return true
	}
	if cRepo, curRepo := hasRepoMarker(cand), hasRepoMarker(cur); cRepo != curRepo {
		return cRepo
	}
	return len(cand) < len(cur)
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

// ---------- 重复条目整理 ----------

// entrySessionKey 归一化会话身份键：同一来源的同一会话视为同一条目，与日期无关。
func entrySessionKey(e entry) string {
	id := strings.TrimSpace(e.SessionID)
	if id == "" {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(e.Source)) + "|" + id
}

// entryWeight 粗略衡量条目内容充实度，用于在重复条目里挑选保留项。
func entryWeight(e entry) int {
	weight := 0
	for _, field := range []string{e.Title, e.Summary, e.Project, e.Feature, e.Exhibit, e.ProjectHint, e.RepoPath, e.Category, e.StartedAt, e.EndedAt} {
		if strings.TrimSpace(field) != "" {
			weight++
		}
	}
	weight += len(e.Outcomes) + len(e.Decisions) + len(e.Learnings) + len(e.Blockers) + len(e.Next) + len(e.Tags) + len(e.Refs)
	if e.Minutes > 0 {
		weight++
	}
	if len(e.Summary) > 40 {
		weight++
	}
	return weight
}

// unionStrings 合并字符串列表：保持首次出现顺序并去重。
func unionStrings(lists ...[]string) []string {
	out := make([]string, 0)
	seen := map[string]bool{}
	for _, list := range lists {
		for _, raw := range list {
			value := strings.TrimSpace(raw)
			if value == "" || seen[value] {
				continue
			}
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func earlierString(a, b string) bool {
	at, bt := strings.TrimSpace(a), strings.TrimSpace(b)
	if at == "" {
		return false
	}
	if bt == "" {
		return true
	}
	return at < bt
}

func laterString(a, b string) bool {
	at, bt := strings.TrimSpace(a), strings.TrimSpace(b)
	if at == "" {
		return false
	}
	if bt == "" {
		return true
	}
	return at > bt
}

// mergeDuplicates 把同一会话的重复条目合并为一条：内容取并集，时间取包络，工时取最大。
func mergeDuplicates(primary entry, others []entry) entry {
	merged := primary
	for _, other := range others {
		merged.Outcomes = unionStrings(merged.Outcomes, other.Outcomes)
		merged.Decisions = unionStrings(merged.Decisions, other.Decisions)
		merged.Learnings = unionStrings(merged.Learnings, other.Learnings)
		merged.Blockers = unionStrings(merged.Blockers, other.Blockers)
		merged.Next = unionStrings(merged.Next, other.Next)
		merged.Tags = unionStrings(merged.Tags, other.Tags)
		merged.Refs = append(merged.Refs, other.Refs...)
		if strings.TrimSpace(merged.Project) == "" {
			merged.Project = other.Project
		}
		if strings.TrimSpace(merged.Exhibit) == "" {
			merged.Exhibit = other.Exhibit
		}
		if strings.TrimSpace(merged.ProjectID) == "" {
			merged.ProjectID = other.ProjectID
		}
		if strings.TrimSpace(merged.ExhibitID) == "" {
			merged.ExhibitID = other.ExhibitID
		}
		if strings.TrimSpace(merged.RepoPath) == "" {
			merged.RepoPath = other.RepoPath
		}
		if strings.TrimSpace(merged.ProjectHint) == "" {
			merged.ProjectHint = other.ProjectHint
		}
		if strings.TrimSpace(merged.Category) == "" {
			merged.Category = other.Category
		}
		if strings.TrimSpace(merged.Title) == "" && strings.TrimSpace(other.Title) != "" {
			merged.Title = other.Title
		}
		if len(strings.TrimSpace(other.Summary)) > len(strings.TrimSpace(merged.Summary)) {
			merged.Summary = other.Summary
		}
		if merged.Minutes < other.Minutes {
			merged.Minutes = other.Minutes
			merged.MinutesSource = other.MinutesSource
		}
		if strings.TrimSpace(merged.Feature) == "" {
			merged.Feature = other.Feature
		}
		if earlierString(other.StartedAt, merged.StartedAt) {
			merged.StartedAt = other.StartedAt
		}
		if laterString(other.EndedAt, merged.EndedAt) {
			merged.EndedAt = other.EndedAt
		}
		if earlierString(other.CreatedAt, merged.CreatedAt) {
			merged.CreatedAt = other.CreatedAt
		}
		if laterString(other.UpdatedAt, merged.UpdatedAt) {
			merged.UpdatedAt = other.UpdatedAt
		}
	}
	return merged
}

// filterDates 按 [from,to] 区间筛选日期（空串表示不限）。
func filterDates(dates []string, from, to string) []string {
	out := make([]string, 0, len(dates))
	for _, d := range dates {
		if from != "" && d < from {
			continue
		}
		if to != "" && d > to {
			continue
		}
		out = append(out, d)
	}
	return out
}

// archiveDedupeCap 合并同一会话的重复条目。历史补录按不同日期基准写入时，同一会话
// 会散落在多天。默认 dry_run 预览；dry_run=false 时先备份受影响日文件再改写。
func archiveDedupeCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	dryRun := in.DryRun == nil || *in.DryRun
	unlock := lockRoot(root)
	defer unlock()

	dates, err := listDates(root)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.From) != "" || strings.TrimSpace(in.To) != "" {
		from, to := "", ""
		if strings.TrimSpace(in.From) != "" {
			from = normalizeDate(in.From)
		}
		if strings.TrimSpace(in.To) != "" {
			to = normalizeDate(in.To)
		}
		dates = filterDates(dates, from, to)
	}
	days := map[string]*dayFile{}
	order := make([]string, 0, len(dates))
	for _, d := range dates {
		day, err := readDay(root, d)
		if err != nil {
			continue
		}
		df := day
		days[d] = &df
		order = append(order, d)
	}

	type location struct {
		Date  string
		Entry entry
	}
	groups := map[string][]location{}
	for _, d := range order {
		for _, e := range days[d].Entries {
			key := entrySessionKey(e)
			if key == "" {
				continue
			}
			groups[key] = append(groups[key], location{Date: d, Entry: e})
		}
	}

	groupsOut := make([]map[string]any, 0)
	removals := map[string]map[string]bool{}
	additions := map[string][]entry{}
	affected := map[string]bool{}
	removedTotal := 0
	for key, locs := range groups {
		if len(locs) < 2 {
			continue
		}
		// 保留内容最充实的一条；同等充实度取更早的日期。
		primary := 0
		for i := 1; i < len(locs); i++ {
			wa, wb := entryWeight(locs[i].Entry), entryWeight(locs[primary].Entry)
			if wa > wb || (wa == wb && earlierString(locs[i].Date, locs[primary].Date)) {
				primary = i
			}
		}
		// 合并结果落到最早的日期（会话开始的那天）。
		target := locs[0].Date
		for _, l := range locs {
			if earlierString(l.Date, target) {
				target = l.Date
			}
		}
		others := make([]entry, 0, len(locs)-1)
		removed := make([]map[string]any, 0, len(locs))
		for i, l := range locs {
			if removals[l.Date] == nil {
				removals[l.Date] = map[string]bool{}
			}
			removals[l.Date][l.Entry.EntryID] = true
			affected[l.Date] = true
			removed = append(removed, map[string]any{"date": l.Date, "entry_id": l.Entry.EntryID, "title": l.Entry.Title})
			if i != primary {
				others = append(others, l.Entry)
			}
		}
		merged := mergeDuplicates(locs[primary].Entry, others)
		merged.Date = target
		additions[target] = append(additions[target], merged)
		affected[target] = true
		removedTotal += len(locs) - 1
		groupsOut = append(groupsOut, map[string]any{
			"key":     key,
			"kept":    map[string]any{"date": target, "entry_id": merged.EntryID, "title": merged.Title},
			"removed": removed,
		})
	}

	daysChanged := 0
	backupDir := ""
	if len(groupsOut) > 0 {
		daysChanged = len(affected)
	}
	if len(groupsOut) > 0 && !dryRun {
		backupDir = filepath.Join(root, "backups", "dedupe-"+time.Now().Format("20060102-150405"))
		for d := range affected {
			body, err := os.ReadFile(dayPath(root, d))
			if err != nil {
				continue
			}
			dest := filepath.Join(backupDir, "logs", yearOfDate(d), d+".json")
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(dest, body, 0o644); err != nil {
				return nil, err
			}
		}
		daysChanged = 0
		for d := range affected {
			day := days[d]
			kept := make([]entry, 0, len(day.Entries)+len(additions[d]))
			for _, e := range day.Entries {
				if removals[d][e.EntryID] {
					continue
				}
				kept = append(kept, e)
			}
			kept = append(kept, additions[d]...)
			sortEntries(kept)
			day.Entries = kept
			day.Date = d
			day.SchemaVersion = schemaVersion
			if err := writeDay(root, *day); err != nil {
				return nil, err
			}
			daysChanged++
		}
	}

	sort.Slice(groupsOut, func(i, j int) bool {
		return groupsOut[i]["key"].(string) < groupsOut[j]["key"].(string)
	})
	return map[string]any{
		"log_root":        root,
		"dry_run":         dryRun,
		"days_scanned":    len(order),
		"groups":          len(groupsOut),
		"entries_removed": removedTotal,
		"days_changed":    daysChanged,
		"backup_dir":      backupDir,
		"duplicates":      groupsOut,
	}, nil
}

func yearOfDate(date string) string {
	if len(date) >= 4 {
		return date[:4]
	}
	return date
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
