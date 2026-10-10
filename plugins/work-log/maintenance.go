package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// 本文件实现「P1 能力收敛」相关能力：
//   - work-log.entry.export      导出日志条目为可迁移的 JSON
//   - work-log.entry.import      从导出 JSON 合并/替换导入条目
//   - work-log.entry.lock        手工锁定/解锁条目（防自动重扫覆盖）
//   - work-log.entry.reconcile   读采集语料 diff 出「有会话但未入账/需更新」（重扫 dry-run，只读）
//   - work-log.archive.backup    把整个日志库打包为带清单的 zip
//   - work-log.archive.restore   从备份包一键恢复（恢复前自动留一份安全备份）

const entryExportSchema = "work-log-entry-export.v1"

// ---------- 条目导出 / 导入 ----------

func entryExportCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	from, to, err := resolveRange(in.From, in.To, in.Date)
	if err != nil {
		return nil, err
	}
	entries, err := collectEntries(root, from, to)
	if err != nil {
		return nil, err
	}
	doc := map[string]any{
		"schema_version": entryExportSchema,
		"exported_at":    time.Now().Format(time.RFC3339),
		"from":           from,
		"to":             to,
		"count":          len(entries),
		"entries":        entries,
	}
	payload, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, errors.New("日志导出序列化失败")
	}
	return map[string]any{
		"log_root": root,
		"from":     from,
		"to":       to,
		"count":    len(entries),
		"entries":  entries,
		"json":     string(payload),
	}, nil
}

// entryImportCap 从 work-log.entry.export 产出的 JSON 导入条目。
// mode=merge（默认）按 entry_id 幂等合并；replace 表示「以导入内容为准」重建这些日期
// （仍保留被手工锁定的旧条目，避免一键导入抹掉锁定记录）。
func entryImportCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(in.EntriesJSON)
	if raw == "" {
		return nil, errors.New("需要 entries_json（日志条目 JSON 文本）")
	}
	var doc struct {
		Entries []entry `json:"entries"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil || len(doc.Entries) == 0 {
		var list []entry
		if err2 := json.Unmarshal([]byte(raw), &list); err2 != nil {
			return nil, errors.New("导入内容不是合法的日志条目 JSON")
		}
		doc.Entries = list
	}
	if len(doc.Entries) == 0 {
		return nil, errors.New("导入内容没有日志条目")
	}

	byDate := map[string][]entry{}
	for _, item := range doc.Entries {
		date := normalizeDate(strings.TrimSpace(item.Date))
		if !validDate(date) {
			continue
		}
		byDate[date] = append(byDate[date], item)
	}
	if len(byDate) == 0 {
		return nil, errors.New("导入内容缺少合法日期")
	}
	dates := make([]string, 0, len(byDate))
	for date := range byDate {
		dates = append(dates, date)
	}
	sort.Strings(dates)

	replace := strings.EqualFold(strings.TrimSpace(in.Mode), "replace")
	unlock := lockRoot(root)
	defer unlock()

	now := time.Now().Format(time.RFC3339)
	added, updated, lockedSkipped := 0, 0, 0
	for _, date := range dates {
		day, err := readDay(root, date)
		if err != nil {
			return nil, err
		}
		incoming := make([]entry, 0, len(byDate[date]))
		for _, item := range byDate[date] {
			incoming = append(incoming, normalizeEntry(item, date, now))
		}
		stats := mergeIntoDay(&day, incoming, replace)
		day.Date = date
		day.SchemaVersion = schemaVersion
		resolve := in.ResolveProjects == nil || *in.ResolveProjects
		if _, err := applyProjectRegistry(root, day.Entries, resolve); err != nil {
			return nil, err
		}
		if resolve {
			if pf, err := readProjects(root); err == nil {
				enrichEntryLinks(pf, day.Entries)
			}
		}
		if err := writeDay(root, day); err != nil {
			return nil, err
		}
		added += stats.added
		updated += stats.updated
		lockedSkipped += stats.lockedSkipped
	}
	mode := "merge"
	if replace {
		mode = "replace"
	}
	return map[string]any{
		"log_root":       root,
		"mode":           mode,
		"days":           len(dates),
		"added":          added,
		"updated":        updated,
		"locked_skipped": lockedSkipped,
	}, nil
}

type mergeStats struct {
	added         int
	updated       int
	lockedSkipped int
}

// mergeIntoDay 按 entry_id 幂等合并一组条目到某天；replace=true 时以 incoming 为准重建，
// 但保留被手工锁定的旧条目（解锁请用 work-log.entry.lock）。
func mergeIntoDay(day *dayFile, incoming []entry, replace bool) mergeStats {
	var stats mergeStats
	if replace {
		kept := make([]entry, 0, len(day.Entries))
		keptIDs := map[string]bool{}
		for _, item := range day.Entries {
			if item.Locked {
				kept = append(kept, item)
				keptIDs[item.EntryID] = true
			}
		}
		day.Entries = kept
		for _, item := range incoming {
			if keptIDs[item.EntryID] {
				stats.lockedSkipped++
				continue
			}
			day.Entries = append(day.Entries, item)
			stats.added++
		}
		sortEntries(day.Entries)
		return stats
	}
	for _, item := range incoming {
		existing := -1
		for i := range day.Entries {
			if day.Entries[i].EntryID == item.EntryID {
				existing = i
				break
			}
		}
		if existing >= 0 {
			if day.Entries[existing].Locked {
				stats.lockedSkipped++
				continue
			}
			item.CreatedAt = day.Entries[existing].CreatedAt
			day.Entries[existing] = mergeEntry(day.Entries[existing], item)
			stats.updated++
			continue
		}
		day.Entries = append(day.Entries, item)
		stats.added++
	}
	sortEntries(day.Entries)
	return stats
}

// ---------- 条目手工锁定 ----------

func entryLockCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	date := normalizeDate(strings.TrimSpace(in.Date))
	if !validDate(date) {
		return nil, errors.New("date 必须是 YYYY-MM-DD 格式")
	}
	ids := map[string]bool{}
	if id := strings.TrimSpace(in.EntryID); id != "" {
		ids[id] = true
	}
	for _, id := range in.EntryIDs {
		if id = strings.TrimSpace(id); id != "" {
			ids[id] = true
		}
	}
	if len(ids) == 0 {
		return nil, errors.New("需要 entry_id 或 entry_ids")
	}
	locked := in.Locked != nil && *in.Locked
	unlock := lockRoot(root)
	defer unlock()
	day, err := readDay(root, date)
	if err != nil {
		return nil, err
	}
	now := time.Now().Format(time.RFC3339)
	updated := make([]string, 0, len(ids))
	missing := make([]string, 0)
	found := map[string]bool{}
	for i := range day.Entries {
		if !ids[day.Entries[i].EntryID] {
			continue
		}
		found[day.Entries[i].EntryID] = true
		if day.Entries[i].Locked != locked {
			day.Entries[i].Locked = locked
			day.Entries[i].UpdatedAt = now
		}
		updated = append(updated, day.Entries[i].EntryID)
	}
	for id := range ids {
		if !found[id] {
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	if len(updated) > 0 {
		if err := writeDay(root, day); err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"log_root":  root,
		"date":      date,
		"locked":    locked,
		"updated":   len(updated),
		"entry_ids": updated,
		"missing":   missing,
	}, nil
}

// ---------- 重扫对账（dry-run 预览） ----------

// digestSession 是采集语料（session-digest-*.json）里与会话身份相关的最小字段集。
type digestSession struct {
	Source    string `json:"source"`
	SessionID string `json:"session_id"`
	Date      string `json:"date"`
	RepoPath  string `json:"repo_path"`
	Project   string `json:"project"`
	Title     string `json:"title"`
	Minutes   int    `json:"minutes"`
	StartedAt string `json:"started_at"`
}

type digestFile struct {
	Sessions []digestSession `json:"sessions"`
}

type reconcileRef struct {
	Date      string `json:"date"`
	Source    string `json:"source"`
	SessionID string `json:"session_id"`
	RepoPath  string `json:"repo_path,omitempty"`
	Project   string `json:"project,omitempty"`
	Title     string `json:"title,omitempty"`
	Minutes   int    `json:"minutes,omitempty"`
	Locked    bool   `json:"locked,omitempty"`
}

type reconcileBucket struct {
	Material int `json:"material"`
	Matched  int `json:"matched"`
	Missing  int `json:"missing"`
	Stale    int `json:"stale"`
	Locked   int `json:"locked"`
}

const reconcileSampleCap = 500

// entryReconcileCap 读取采集器产出的会话语料，与日志按 (source, session_id) 对账，
// 列出「语料里有、日志里没有」的未入账会话与「日志里有、语料里没有」的过时条目，
// 供重扫前预览。纯只读，不写盘。
func entryReconcileCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	from, to, err := resolveRange(in.From, in.To, in.Date)
	if err != nil {
		return nil, err
	}
	dir, err := sessionMaterialDir(in.MaterialRoot)
	if err != nil {
		return nil, err
	}
	entries, err := collectEntries(root, from, to)
	if err != nil {
		return nil, err
	}

	logIndex := map[string]reconcileRef{}
	logByDate := map[string]*reconcileBucket{}
	for _, item := range entries {
		date := strings.TrimSpace(item.Date)
		if date == "" {
			continue
		}
		if logByDate[date] == nil {
			logByDate[date] = &reconcileBucket{}
		}
		if item.Locked {
			logByDate[date].Locked++
		}
		sid := strings.TrimSpace(item.SessionID)
		if sid == "" {
			continue
		}
		src := strings.TrimSpace(item.Source)
		logIndex[sessionKey(src, sid)] = reconcileRef{
			Date: date, Source: src, SessionID: sid,
			Project: item.Project, Title: item.Title, Minutes: item.Minutes,
			Locked: item.Locked,
		}
	}

	materialIndex := map[string]reconcileRef{}
	materialByDate := map[string]int{}
	found := true
	files, readErr := os.ReadDir(dir)
	if readErr != nil {
		found = false
	}
	for _, f := range files {
		if f.IsDir() || !strings.HasPrefix(f.Name(), "session-digest-") || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			continue
		}
		var doc digestFile
		if err := json.Unmarshal(body, &doc); err != nil {
			continue
		}
		for _, s := range doc.Sessions {
			sid := strings.TrimSpace(s.SessionID)
			if sid == "" {
				continue
			}
			date := normalizeDate(strings.TrimSpace(s.Date))
			if !validDate(date) || date < from || date > to {
				continue
			}
			key := sessionKey(strings.TrimSpace(s.Source), sid)
			if _, seen := materialIndex[key]; !seen {
				materialByDate[date]++
			}
			materialIndex[key] = reconcileRef{
				Date: date, Source: strings.TrimSpace(s.Source), SessionID: sid,
				RepoPath: s.RepoPath, Project: s.Project, Title: s.Title, Minutes: s.Minutes,
			}
		}
	}

	missing := make([]reconcileRef, 0)
	for key, ref := range materialIndex {
		if _, ok := logIndex[key]; !ok {
			missing = append(missing, ref)
		}
	}
	stale := make([]reconcileRef, 0)
	for key, ref := range logIndex {
		if _, ok := materialIndex[key]; !ok {
			stale = append(stale, ref)
		}
	}
	sortRefs(missing)
	sortRefs(stale)

	byDate := map[string]reconcileBucket{}
	for date, count := range materialByDate {
		b := byDate[date]
		b.Material = count
		byDate[date] = b
	}
	for _, ref := range missing {
		b := byDate[ref.Date]
		b.Missing++
		byDate[ref.Date] = b
	}
	for _, ref := range stale {
		b := byDate[ref.Date]
		b.Stale++
		byDate[ref.Date] = b
	}
	for date, bucket := range logByDate {
		b := byDate[date]
		b.Locked = bucket.Locked
		byDate[date] = b
	}
	matched := 0
	for key := range materialIndex {
		if _, ok := logIndex[key]; ok {
			matched++
			date := materialIndex[key].Date
			b := byDate[date]
			b.Matched++
			byDate[date] = b
		}
	}

	return map[string]any{
		"log_root":          root,
		"material_dir":      dir,
		"found_material":    found,
		"from":              from,
		"to":                to,
		"material_sessions": len(materialIndex),
		"log_sessions":      len(logIndex),
		"matched":           matched,
		"missing_count":     len(missing),
		"stale_count":       len(stale),
		"missing":           capRefs(missing, reconcileSampleCap),
		"stale":             capRefs(stale, reconcileSampleCap),
		"by_date":           byDate,
	}, nil
}

func sessionKey(source, sessionID string) string {
	return strings.ToLower(strings.TrimSpace(source)) + "|" + strings.TrimSpace(sessionID)
}

func sortRefs(items []reconcileRef) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Date != items[j].Date {
			return items[i].Date < items[j].Date
		}
		if items[i].Source != items[j].Source {
			return items[i].Source < items[j].Source
		}
		return items[i].SessionID < items[j].SessionID
	})
}

func capRefs(items []reconcileRef, limit int) []reconcileRef {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

// ---------- 整库备份 / 一键恢复 ----------

// backupIncludeRoots 是备份纳入的目录（相对日志根）；单文件见 backupIncludeFiles。
var backupIncludeRoots = []string{"logs", "reports", "config"}

var backupIncludeFiles = []string{"projects.json"}

func archiveBackupCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	unlock := lockRoot(root)
	defer unlock()
	path, files, size, err := writeBackupArchive(root)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"log_root": root,
		"path":     path,
		"files":    files,
		"bytes":    size,
	}, nil
}

// writeBackupArchive 把日志库打包为 zip（含 manifest.json 清单），返回包路径、文件数、字节数。
func writeBackupArchive(root string) (string, int, int, error) {
	dir := filepath.Join(root, "backups")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, 0, errors.New("备份目录创建失败")
	}
	full := uniqueBackupPath(dir)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	manifest := map[string]any{
		"schema_version": "work-log-backup.v1",
		"created_at":     time.Now().Format(time.RFC3339),
		"app":            "com.himind.work-log",
	}
	if body, err := json.MarshalIndent(manifest, "", "  "); err == nil {
		if w, err := zw.Create("manifest.json"); err == nil {
			_, _ = w.Write(body)
		}
	}

	files := 0
	for _, rel := range backupIncludeFiles {
		target := filepath.Join(root, rel)
		if info, err := os.Stat(target); err != nil || info.IsDir() {
			continue
		}
		if err := addFileToZip(zw, root, target); err != nil {
			continue
		}
		files++
	}
	for _, rel := range backupIncludeRoots {
		base := filepath.Join(root, rel)
		if info, err := os.Stat(base); err != nil || !info.IsDir() {
			continue
		}
		_ = filepath.WalkDir(base, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil || d.IsDir() {
				return nil
			}
			name := strings.ToLower(d.Name())
			if strings.HasSuffix(name, ".bak") || strings.HasSuffix(name, ".tmp") {
				return nil
			}
			if err := addFileToZip(zw, root, path); err == nil {
				files++
			}
			return nil
		})
	}
	if err := zw.Close(); err != nil {
		return "", 0, 0, errors.New("备份打包失败")
	}
	if err := os.WriteFile(full, buf.Bytes(), 0o644); err != nil {
		return "", 0, 0, errors.New("备份写入失败")
	}
	return full, files, buf.Len(), nil
}

// uniqueBackupPath 生成一个不与现有备份冲突的路径：同一秒内多次备份时追加序号，
// 避免「恢复前的安全备份」覆盖掉用户正要恢复的那一份。
func uniqueBackupPath(dir string) string {
	base := "work-log-backup-" + time.Now().Format("20060102-150405")
	full := filepath.Join(dir, base+".zip")
	for i := 2; ; i++ {
		if _, err := os.Stat(full); errors.Is(err, os.ErrNotExist) {
			return full
		}
		full = filepath.Join(dir, fmt.Sprintf("%s-%d.zip", base, i))
	}
}

func addFileToZip(zw *zip.Writer, root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	w, err := zw.Create(filepath.ToSlash(rel))
	if err != nil {
		return err
	}
	_, err = w.Write(body)
	return err
}

func archiveRestoreCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	target := strings.TrimSpace(in.BackupPath)
	if target == "" {
		latest, err := latestBackup(root)
		if err != nil {
			return nil, err
		}
		target = latest
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return nil, errors.New("备份路径无效")
	}
	if info, err := os.Stat(abs); err != nil || info.IsDir() {
		return nil, errors.New("找不到备份文件：" + abs)
	}

	unlock := lockRoot(root)
	defer unlock()
	// 恢复前先留一份当前状态的安全备份，避免误恢复无法回退。
	safety, _, _, err := writeBackupArchive(root)
	if err != nil {
		return nil, errors.New("恢复前自动备份失败：" + err.Error())
	}

	reader, err := zip.OpenReader(abs)
	if err != nil {
		return nil, errors.New("备份不是合法的 zip 包")
	}
	defer reader.Close()

	restored := 0
	for _, f := range reader.File {
		rel := filepath.ToSlash(f.Name)
		if f.FileInfo().IsDir() || !isRestorablePath(rel) {
			continue
		}
		clean := filepath.Clean(filepath.FromSlash(rel))
		if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
			continue // 防 zip-slip
		}
		dest := filepath.Join(root, clean)
		if !strings.HasPrefix(dest, root) {
			continue
		}
		src, err := f.Open()
		if err != nil {
			continue
		}
		body, err := io.ReadAll(src)
		src.Close()
		if err != nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return nil, errors.New("恢复失败：无法创建目录")
		}
		if err := os.WriteFile(dest, body, 0o644); err != nil {
			return nil, errors.New("恢复失败：无法写入 " + rel)
		}
		restored++
	}
	return map[string]any{
		"log_root":      root,
		"restored_from": abs,
		"files":         restored,
		"safety_backup": safety,
	}, nil
}

func isRestorablePath(rel string) bool {
	lower := strings.ToLower(rel)
	if lower == "projects.json" {
		return true
	}
	for _, prefix := range []string{"logs/", "reports/", "config/"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func latestBackup(root string) (string, error) {
	dir := filepath.Join(root, "backups")
	items, err := os.ReadDir(dir)
	if err != nil {
		return "", errors.New("没有可用备份：请先运行 work-log.archive.backup")
	}
	names := make([]string, 0)
	for _, it := range items {
		if it.IsDir() || !strings.HasSuffix(strings.ToLower(it.Name()), ".zip") {
			continue
		}
		names = append(names, it.Name())
	}
	if len(names) == 0 {
		return "", errors.New("没有可用备份：请先运行 work-log.archive.backup")
	}
	sort.Strings(names)
	return filepath.Join(dir, names[len(names)-1]), nil
}
