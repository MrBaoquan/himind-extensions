package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

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
	unlock, err := lockRoot(root)
	if err != nil {
		return nil, err
	}
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
	projectChanged := false
	projectProvided := false
	requestedID := ""
	idProvided := false
	manualMinutes := false
	sourceProvided := false
	for key, raw := range in.Patch {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "project":
			item.Project = asText(raw)
			projectChanged = true
			projectProvided = true
		case "project_id":
			value := asText(raw)
			if idProvided && requestedID != value {
				return nil, errors.New("项目 ID 与工作区项目 ID 不一致")
			}
			requestedID, idProvided = value, true
			projectChanged = true
		case "feature":
			item.Feature = asText(raw)
		case "exhibit":
			item.Exhibit = asText(raw)
		case "category":
			item.Category = asText(raw)
		case "title":
			item.Title = asText(raw)
		case "summary":
			item.Summary = asText(raw)
		case "minutes":
			item.Minutes = asInt(raw)
			manualMinutes = true
		case "minutes_source":
			item.MinutesSource = asText(raw)
			sourceProvided = true
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
	if manualMinutes && (!sourceProvided || item.MinutesSource == "manual") {
		item.MinutesSource, item.MinutesMethod, item.ActivityIntervals, item.OverlapAdjusted = "manual", "", nil, false
	}
	if projectChanged {
		if (idProvided && requestedID == "") || (!idProvided && strings.TrimSpace(item.Project) == "") {
			item.Project, item.ProjectID, item.ProjectHint = "", "", ""
		} else {
			snapshot, err := loadAgentWorkspace(context.Background())
			if err != nil {
				return nil, err
			}
			project, err := findAgentProject(snapshot, requestedID, item.Project)
			if err != nil {
				return nil, err
			}
			if projectProvided && requestedID != "" && strings.TrimSpace(item.Project) != "" && !strings.EqualFold(project.Name, strings.TrimSpace(item.Project)) && !aliasMatches(project.Aliases, item.Project) {
				return nil, errors.New("项目名称与 Agent 工作区项目 ID 不一致")
			}
			current, err := loadAgentWorkspace(context.Background())
			if err != nil {
				return nil, err
			}
			if current.Revision != snapshot.Revision {
				return nil, errors.New("Agent 工作区登记已变化，请重新修改")
			}
			item.Project, item.ProjectID, item.ProjectHint = project.Name, project.ID, ""
		}
	}
	item.UpdatedAt = time.Now().Format(time.RFC3339)
	day.Entries[at] = item
	if err := writeDay(root, day); err != nil {
		return nil, err
	}
	return map[string]any{
		"log_root": root,
		"date":     date,
		"entry":    day.Entries[at],
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

// sessionMaterialDir returns work-log's own material store.
func sessionMaterialDir(value string) (string, error) {
	if value != "" {
		return filepath.Abs(value)
	}
	if base := strings.TrimSpace(os.Getenv("HIMIND_PLUGIN_DATA_ROOT")); base != "" {
		return filepath.Join(base, "material"), nil
	}
	if local := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); local != "" {
		return filepath.Join(local, "HiMindAgent", "plugin-data", "com.himind.work-log", "material"), nil
	}
	return filepath.Join(os.TempDir(), "HiMindAgent", "com.himind.work-log", "material"), nil
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
	unlock, err := lockRoot(root)
	if err != nil {
		return nil, err
	}
	defer unlock()

	projectsChanged := false

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
	// 时间戳按真实时刻比较（跨时区也正确）；非时间戳（日期串等）仍走字符串比较。
	if order, ok := compareStamps(at, bt); ok {
		return order < 0
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
	if order, ok := compareStamps(at, bt); ok {
		return order > 0
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

// archiveDedupeCap 合并同一会话的重复条目。
//
// Agent 会话服务按消息「活动日」归集：同一条跨多日的长会话会在每一天各留一条记录，
// 这是**正常记录**，不应被合并。因此默认只合并「同一天、同一会话」的真正重复条目
// （例如 entry_id 规则变化前后各写过一次）。显式传 cross_day=true 时，才退回旧的
// 「跨天合并到最早日期」行为，用于清理历史误录。默认 dry_run 预览；dry_run=false 时
// 先备份受影响日文件再改写。
func archiveDedupeCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	dryRun := in.DryRun == nil || *in.DryRun
	crossDay := in.CrossDay != nil && *in.CrossDay
	unlock, err := lockRoot(root)
	if err != nil {
		return nil, err
	}
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
			// 默认只按「同一天 + 同一会话」去重，保留跨日归集出的多条正当记录；
			// cross_day=true 时才按会话跨天归并。
			if !crossDay {
				key = key + "|" + d
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
		sources := make([]map[string]any, 0, len(locs))
		for i, l := range locs {
			if removals[l.Date] == nil {
				removals[l.Date] = map[string]bool{}
			}
			removals[l.Date][l.Entry.EntryID] = true
			affected[l.Date] = true
			sources = append(sources, map[string]any{"date": l.Date, "entry_id": l.Entry.EntryID, "title": l.Entry.Title})
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
			"key":           key,
			"merge_into":    map[string]any{"date": target, "entry_id": merged.EntryID, "title": merged.Title},
			"removed_count": len(locs) - 1,
			"sources":       sources,
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
		"cross_day":       crossDay,
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
