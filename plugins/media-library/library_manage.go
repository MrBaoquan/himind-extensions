package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// 库内容管理：软删除、恢复、重新扫描、与精选同步、事件日志。
// 默认只动索引，只有显式 delete_files 才删磁盘文件。

type libraryItemIDsInput struct {
	IDs         []string `json:"ids"`
	DeleteFiles bool     `json:"delete_files"`
}

type libraryTrashInput struct {
	LibraryID string `json:"library_id"`
	Limit     int    `json:"limit"`
}

func ensureLibraryManageSchema(db *sql.DB) error {
	var applied int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE name='v3_library_manage_v1'`).Scan(&applied); err != nil {
		return err
	}
	if applied > 0 {
		return backfillLibraryOriginSelection(db)
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, spec := range []struct{ table, column, stmt string }{
		{"libraries", "status", `ALTER TABLE libraries ADD COLUMN status TEXT NOT NULL DEFAULT 'active'`},
		{"libraries", "archived_at", `ALTER TABLE libraries ADD COLUMN archived_at TEXT NOT NULL DEFAULT ''`},
		{"library_items", "deleted_at", `ALTER TABLE library_items ADD COLUMN deleted_at TEXT NOT NULL DEFAULT ''`},
		{"library_items", "content_key", `ALTER TABLE library_items ADD COLUMN content_key TEXT NOT NULL DEFAULT ''`},
		{"library_items", "origin_selection_id", `ALTER TABLE library_items ADD COLUMN origin_selection_id TEXT NOT NULL DEFAULT ''`},
	} {
		var exists int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, spec.table, spec.column).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		if _, err := tx.Exec(spec.stmt); err != nil {
			return err
		}
	}
	stmts := []string{
		`CREATE INDEX IF NOT EXISTS idx_library_items_live ON library_items(library_id, deleted_at)`,
		`CREATE TABLE IF NOT EXISTS library_events (
  id TEXT PRIMARY KEY,
  library_id TEXT NOT NULL,
  item_id TEXT NOT NULL DEFAULT '',
  action TEXT NOT NULL,
  detail_json TEXT NOT NULL DEFAULT '{}',
  actor TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
)`,
		`CREATE INDEX IF NOT EXISTS idx_library_events_library ON library_events(library_id, created_at)`,
	}
	for _, stmt := range stmts {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations(name,applied_at) VALUES('v3_library_manage_v1',?)`, nowRFC3339()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return backfillLibraryOriginSelection(db)
}

// backfillLibraryOriginSelection 给老数据补上“这个精选库由哪条精选生成”。
func backfillLibraryOriginSelection(db *sql.DB) error {
	_, err := db.Exec(`UPDATE libraries SET origin_selection_id=(
		SELECT s.id FROM selections s WHERE s.library_id=libraries.id ORDER BY s.created_at DESC LIMIT 1)
		WHERE COALESCE(origin_selection_id,'')='' AND COALESCE(kind,'material')='curated'
		AND EXISTS (SELECT 1 FROM selections s WHERE s.library_id=libraries.id)`)
	return err
}

func logLibraryEvent(tx *sql.Tx, libraryID string, itemID string, action string, detail map[string]any) {
	payload, _ := json.Marshal(detail)
	_, _ = tx.Exec(`INSERT INTO library_events(id,library_id,item_id,action,detail_json,actor,created_at)
		VALUES(?,?,?,?,?,?,?)`, randomID("libev"), libraryID, itemID, action, string(payload), safeActor(""), nowRFC3339())
}

// removeLibraryItems 软删除条目：检索立即消失，文件默认保留。
func removeLibraryItems(in libraryItemIDsInput) (any, error) {
	ids := uniqueStrings(in.IDs)
	if len(ids) == 0 {
		return nil, fmt.Errorf("请选择要移出的条目")
	}
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := nowRFC3339()
	removed := 0
	filesDeleted := 0
	failures := []map[string]any{}
	for _, id := range ids {
		var libraryID, localPath, relative string
		var bytes int64
		err := tx.QueryRow(`SELECT library_id,COALESCE(local_path,''),COALESCE(relative_path,''),COALESCE(bytes,0)
			FROM library_items WHERE id=? AND deleted_at=''`, id).Scan(&libraryID, &localPath, &relative, &bytes)
		if err == sql.ErrNoRows {
			failures = append(failures, map[string]any{"id": id, "error": "条目不存在或已移出"})
			continue
		}
		if err != nil {
			return nil, err
		}
		if in.DeleteFiles && localPath != "" {
			if ok, err := removeFileInsideLibrary(tx, libraryID, localPath); err != nil {
				failures = append(failures, map[string]any{"id": id, "error": err.Error()})
			} else if ok {
				filesDeleted++
			}
		}
		if _, err := tx.Exec(`UPDATE library_items SET deleted_at=?,content_key=? WHERE id=?`, now, contentKeyForRelative(relative, bytes), id); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`DELETE FROM search_fts WHERE entry_type='library' AND entry_id=?`, id); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`DELETE FROM search_fts_v2 WHERE entry_type='library' AND entry_id=?`, id); err != nil {
			return nil, err
		}
		// 精选记录同步作废，避免“库已移出、精选还显示已加入”。
		_, _ = tx.Exec(`DELETE FROM curated_entries WHERE location_path=?`, localPath)
		logLibraryEvent(tx, libraryID, id, "item.remove", map[string]any{"file": localPath, "delete_files": in.DeleteFiles})
		removed++
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return map[string]any{"removed": removed, "files_deleted": filesDeleted, "failed": failures}, nil
}

func removeFileInsideLibrary(tx *sql.Tx, libraryID string, localPath string) (bool, error) {
	var root string
	if err := tx.QueryRow(`SELECT root_uri FROM libraries WHERE id=?`, libraryID).Scan(&root); err != nil {
		return false, err
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return false, err
	}
	target, err := filepath.Abs(localPath)
	if err != nil {
		return false, err
	}
	relative, err := filepath.Rel(root, target)
	if err != nil || strings.HasPrefix(relative, "..") {
		return false, fmt.Errorf("文件不在库目录内，已跳过删除")
	}
	info, err := os.Stat(target)
	if err != nil {
		return false, nil
	}
	if info.IsDir() {
		return false, fmt.Errorf("目标是目录，已跳过删除")
	}
	if err := os.Remove(target); err != nil {
		return false, err
	}
	return true, nil
}

func restoreLibraryItems(in libraryItemIDsInput) (any, error) {
	ids := uniqueStrings(in.IDs)
	if len(ids) == 0 {
		return nil, fmt.Errorf("请选择要恢复的条目")
	}
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	restored := 0
	for _, id := range ids {
		var libraryID, title, kind, relative, local, metaJSON string
		var bytes int64
		err := tx.QueryRow(`SELECT library_id,COALESCE(title,''),COALESCE(kind,''),COALESCE(relative_path,''),
			COALESCE(local_path,''),COALESCE(bytes,0),COALESCE(metadata_json,'{}') FROM library_items WHERE id=? AND deleted_at<>''`, id).
			Scan(&libraryID, &title, &kind, &relative, &local, &bytes, &metaJSON)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`UPDATE library_items SET deleted_at='' WHERE id=?`, id); err != nil {
			return nil, err
		}
		// 恢复后把精选里的“已加入”记录补回去，避免库里有、精选显示未加入。
		relinkCuratedEntry(tx, libraryID, local, metaJSON)
		if err := replaceSearchDocument(tx, searchDocument{
			EntryType: "library", EntryID: id, LibraryID: libraryID, Kind: kind, UpdatedAt: nowRFC3339(),
			Title: title, Filename: filepath.Base(local), Path: relative,
		}); err != nil {
			return nil, err
		}
		logLibraryEvent(tx, libraryID, id, "item.restore", map[string]any{"file": local})
		restored++
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return map[string]any{"restored": restored}, nil
}

func listLibraryTrash(in libraryTrashInput) (any, error) {
	libraryID := strings.TrimSpace(in.LibraryID)
	if libraryID == "" {
		return nil, fmt.Errorf("请指定资源库")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT id,COALESCE(title,''),COALESCE(kind,''),COALESCE(relative_path,''),
		COALESCE(local_path,''),COALESCE(bytes,0),COALESCE(deleted_at,'')
		FROM library_items WHERE library_id=? AND deleted_at<>'' ORDER BY deleted_at DESC LIMIT ?`, libraryID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, title, kind, relative, local, deletedAt string
		var bytes int64
		if err := rows.Scan(&id, &title, &kind, &relative, &local, &bytes, &deletedAt); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{
			"id": id, "title": title, "kind": kind, "relative_path": relative,
			"path": local, "bytes": bytes, "deleted_at": deletedAt,
		})
	}
	return map[string]any{"items": items, "total": len(items)}, rows.Err()
}

func contentKeyForRelative(relative string, bytes int64) string {
	name := strings.ToLower(filepath.Base(strings.ReplaceAll(relative, "\\\\", "/")))
	return fmt.Sprintf("%s|%d", name, bytes)
}

// relinkCuratedEntry 让“恢复条目”同时恢复精选里的已加入标记。
func relinkCuratedEntry(tx *sql.Tx, libraryID, localPath, metaJSON string) {
	payload := map[string]any{}
	_ = json.Unmarshal([]byte(metaJSON), &payload)
	assetID := stringValue(payload["selection_asset_id"])
	if assetID == "" || localPath == "" {
		return
	}
	var origin string
	_ = tx.QueryRow(`SELECT COALESCE(origin_selection_id,'') FROM libraries WHERE id=?`, libraryID).Scan(&origin)
	if origin == "" {
		_ = tx.QueryRow(`SELECT id FROM selections WHERE library_id=? ORDER BY created_at DESC LIMIT 1`, libraryID).Scan(&origin)
	}
	if origin == "" {
		return
	}
	var included int
	_ = tx.QueryRow(`SELECT COUNT(*) FROM selection_items WHERE selection_id=? AND asset_id=? AND state='included'`, origin, assetID).Scan(&included)
	if included == 0 {
		return
	}
	_, _ = tx.Exec(`INSERT INTO curated_entries(selection_id,asset_id,location_path,mode,created_at) VALUES(?,?,?,'copy',?)
		ON CONFLICT(selection_id,asset_id) DO UPDATE SET location_path=excluded.location_path`, origin, assetID, localPath, nowRFC3339())
}

type libraryRescanInput struct {
	LibraryID string `json:"library_id"`
}

// rescanLibrary 只报告差异，不自动改数据。
func rescanLibrary(in libraryRescanInput) (any, error) {
	libraryID := strings.TrimSpace(in.LibraryID)
	if libraryID == "" {
		return nil, fmt.Errorf("请指定资源库")
	}
	library, err := getLibrary(libraryID)
	if err != nil {
		return nil, err
	}
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT id,COALESCE(relative_path,''),COALESCE(local_path,''),COALESCE(bytes,0)
		FROM library_items WHERE library_id=? AND deleted_at=''`, libraryID)
	if err != nil {
		return nil, err
	}
	type row struct {
		id, relative, local string
		bytes               int64
	}
	known := map[string]row{}
	for rows.Next() {
		var item row
		if err := rows.Scan(&item.id, &item.relative, &item.local, &item.bytes); err != nil {
			rows.Close()
			return nil, err
		}
		known[strings.ReplaceAll(item.relative, "\\", "/")] = item
	}
	rows.Close()
	missing := []map[string]any{}
	changed := []map[string]any{}
	for relative, item := range known {
		info, statErr := os.Stat(item.local)
		if statErr != nil || info.IsDir() {
			missing = append(missing, map[string]any{"id": item.id, "relative_path": relative, "path": item.local})
			continue
		}
		if info.Size() != item.bytes {
			changed = append(changed, map[string]any{"id": item.id, "relative_path": relative, "db_bytes": item.bytes, "disk_bytes": info.Size()})
		}
	}
	orphan := []string{}
	root := filepath.Clean(library.RootURI)
	visited := 0
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		visited++
		if visited > 20000 {
			return filepath.SkipAll
		}
		name := entry.Name()
		if entry.IsDir() {
			if name == ".himind-media" || name == "assets" {
				return filepath.SkipDir
			}
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		normalized := strings.ReplaceAll(relative, "\\", "/")
		if _, ok := known[normalized]; !ok {
			orphan = append(orphan, normalized)
		}
		return nil
	})
	sort.Strings(orphan)
	return map[string]any{
		"library_id": libraryID,
		"missing":    missing,
		"changed":    changed,
		"orphan":     orphan,
		"counts": map[string]any{
			"items": len(known), "missing": len(missing), "changed": len(changed), "orphan": len(orphan),
		},
	}, nil
}

type librarySyncInput struct {
	SelectionID string `json:"selection_id"`
	Token       string `json:"token"`
	Apply       bool   `json:"apply"`
}

type syncPlan struct {
	selectionID string
	libraryID   string
	addKeys     []string
	remove      []map[string]any
	missing     []map[string]any
	token       string
}

func buildSyncPlan(db *sql.DB, selectionID string) (*syncPlan, error) {
	var libraryID string
	if err := db.QueryRow(`SELECT COALESCE(library_id,'') FROM selections WHERE id=?`, selectionID).Scan(&libraryID); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("精选不存在")
		}
		return nil, err
	}
	if libraryID == "" {
		return nil, fmt.Errorf("这条精选还没有目标精选库")
	}
	rows, err := db.Query(`SELECT c.path,COALESCE(c.bytes,0) FROM selection_items i
		JOIN candidates c ON c.id=i.asset_id WHERE i.selection_id=? AND i.state='included'`, selectionID)
	if err != nil {
		return nil, err
	}
	included := map[string]bool{}
	for rows.Next() {
		var path string
		var bytes int64
		if err := rows.Scan(&path, &bytes); err != nil {
			rows.Close()
			return nil, err
		}
		key := contentKeyForRelative(path, bytes)
		included[key] = true
	}
	rows.Close()
	itemRows, err := db.Query(`SELECT id,COALESCE(relative_path,''),COALESCE(local_path,''),COALESCE(bytes,0)
		FROM library_items WHERE library_id=? AND deleted_at=''`, libraryID)
	if err != nil {
		return nil, err
	}
	plan := &syncPlan{selectionID: selectionID, libraryID: libraryID}
	libraryKeys := map[string]bool{}
	for itemRows.Next() {
		var id, relative, local string
		var bytes int64
		if err := itemRows.Scan(&id, &relative, &local, &bytes); err != nil {
			itemRows.Close()
			return nil, err
		}
		key := contentKeyForRelative(relative, bytes)
		libraryKeys[key] = true
		if !included[key] {
			plan.remove = append(plan.remove, map[string]any{"id": id, "relative_path": relative, "path": local, "key": key})
		}
		if _, statErr := os.Stat(local); statErr != nil {
			plan.missing = append(plan.missing, map[string]any{"id": id, "relative_path": relative, "path": local})
		}
	}
	itemRows.Close()
	for key := range included {
		if !libraryKeys[key] {
			plan.addKeys = append(plan.addKeys, key)
		}
	}
	sort.Strings(plan.addKeys)
	sort.Slice(plan.remove, func(left, right int) bool {
		return plan.remove[left]["relative_path"].(string) < plan.remove[right]["relative_path"].(string)
	})
	hash := sha256.Sum256([]byte(strings.Join(plan.addKeys, ",") + "|" + joinRemoveKeys(plan.remove)))
	plan.token = hex.EncodeToString(hash[:12])
	return plan, nil
}

func joinRemoveKeys(items []map[string]any) string {
	keys := make([]string, 0, len(items))
	for _, item := range items {
		keys = append(keys, item["key"].(string))
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func librarySync(in librarySyncInput) (any, error) {
	selectionID := strings.TrimSpace(in.SelectionID)
	if selectionID == "" {
		return nil, fmt.Errorf("请指定精选")
	}
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	plan, err := buildSyncPlan(db, selectionID)
	if err != nil {
		return nil, err
	}
	preview := map[string]any{
		"library_id": plan.libraryID,
		"token":      plan.token,
		"add_count":  len(plan.addKeys),
		"remove":     plan.remove,
		"missing":    plan.missing,
		"counts": map[string]any{
			"add": len(plan.addKeys), "remove": len(plan.remove), "missing": len(plan.missing),
		},
	}
	if !in.Apply {
		return preview, nil
	}
	if strings.TrimSpace(in.Token) != plan.token {
		return nil, fmt.Errorf("库内容已变化，请重新预览后再执行")
	}
	ids := make([]string, 0, len(plan.remove))
	for _, item := range plan.remove {
		ids = append(ids, item["id"].(string))
	}
	if len(ids) == 0 {
		return map[string]any{"applied": 0, "token": plan.token}, nil
	}
	result, err := removeLibraryItems(libraryItemIDsInput{IDs: ids})
	if err != nil {
		return nil, err
	}
	payload := result.(map[string]any)
	payload["token"] = plan.token
	return payload, nil
}

var _ = time.Now
