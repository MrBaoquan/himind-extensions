package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// 库类型：material = 素材库（默认），curated = 精选库（精选沉淀的产物）。
// 精选库与素材库共用 libraries 表，因此同样出现在资源库列表、可被搜索、有条目数。

const (
	libraryKindMaterial = "material"
	libraryKindCurated  = "curated"
)

func normalizeLibraryKind(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case libraryKindCurated:
		return libraryKindCurated
	case libraryKindMaterial, "":
		return libraryKindMaterial
	}
	return libraryKindMaterial
}

// curatedRegistration 是待登记进精选库的一条结果。
type curatedRegistration struct {
	AssetID     string
	SelectionID string
	Destination string
	Relative    string
	Kind        string
	Title       string
	Bytes       int64
	Width       int
	Height      int
	DurationMS  int64
	FPS         float64
	Codec       string
	BitDepth    int
	SourcePath  string
}

func hasColumn(db *sql.DB, table string, column string) bool {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull, primaryKey int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false
		}
		if strings.EqualFold(name, column) {
			return true
		}
	}
	return false
}

func ensureLibraryKind(db *sql.DB) error {
	var applied int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE name='v3_library_kind_v1'`).Scan(&applied); err != nil {
		return err
	}
	if applied > 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, spec := range []struct{ table, column, stmt string }{
		{"libraries", "kind", `ALTER TABLE libraries ADD COLUMN kind TEXT NOT NULL DEFAULT 'material'`},
		{"libraries", "origin_selection_id", `ALTER TABLE libraries ADD COLUMN origin_selection_id TEXT NOT NULL DEFAULT ''`},
		{"selections", "library_id", `ALTER TABLE selections ADD COLUMN library_id TEXT NOT NULL DEFAULT ''`},
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
	// 回填：已有精选集的目标目录就是它产出的精选库。
	rows, err := tx.Query(`SELECT id,name,target_path FROM selections WHERE target_path<>'' AND library_id=''`)
	if err != nil {
		return err
	}
	type backfill struct{ id, name, path string }
	pending := []backfill{}
	for rows.Next() {
		var item backfill
		if err := rows.Scan(&item.id, &item.name, &item.path); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, item)
	}
	rows.Close()
	for _, item := range pending {
		libraryID, err := ensureCuratedLibraryTx(tx, item.id, item.name, item.path)
		if err != nil {
			continue
		}
		if _, err := tx.Exec(`UPDATE selections SET library_id=? WHERE id=?`, libraryID, item.id); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations(name,applied_at) VALUES('v3_library_kind_v1',?)`, nowRFC3339()); err != nil {
		return err
	}
	return tx.Commit()
}

// ensureCuratedLibraryTx 取得（或登记）某个精选集对应的精选库。
// 库就是目录本身：root 相同即同一个精选库，重复沉淀不会分裂出多个库。
func ensureCuratedLibraryTx(tx *sql.Tx, selectionID string, selectionName string, root string) (string, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(root))
	if err != nil {
		return "", err
	}
	if absolute == "" {
		return "", fmt.Errorf("精选库目录不能为空")
	}
	if err := os.MkdirAll(absolute, 0o755); err != nil {
		return "", fmt.Errorf("创建精选库目录失败: %w", err)
	}
	var libraryID string
	err = tx.QueryRow(`SELECT id FROM libraries WHERE backend_type='local' AND root_uri=?`, absolute).Scan(&libraryID)
	if err == nil {
		if _, err := tx.Exec(`UPDATE libraries SET kind='curated', origin_selection_id=?, updated_at=? WHERE id=? AND (kind<>'curated' OR origin_selection_id='')`,
			selectionID, nowRFC3339(), libraryID); err != nil {
			return "", err
		}
		return libraryID, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	name := strings.TrimSpace(selectionName)
	if name == "" {
		name = filepath.Base(absolute)
	}
	if !strings.HasSuffix(name, "精选库") {
		name += " 精选库"
	}
	libraryID = stableID("lib", "curated|"+absolute)
	now := nowRFC3339()
	if _, err := tx.Exec(`INSERT INTO libraries(id,name,backend_type,root_uri,credential_ref,access_mode,created_at,updated_at,kind,origin_selection_id)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(backend_type,root_uri) DO UPDATE SET
		  kind='curated', origin_selection_id=excluded.origin_selection_id, updated_at=excluded.updated_at`,
		libraryID, name, "local", absolute, "", "contribute", now, now, libraryKindCurated, selectionID); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(absolute, ".himind-media"), 0o755); err != nil {
		return "", err
	}
	return libraryID, nil
}

// curatedLibraryForSelection 在事务外取精选库，必要时登记。
func curatedLibraryForSelection(selectionID string, selectionName string, root string) (string, error) {
	db, err := openLibraryDB()
	if err != nil {
		return "", err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	libraryID, err := ensureCuratedLibraryTx(tx, selectionID, selectionName, root)
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(`UPDATE selections SET library_id=? WHERE id=?`, libraryID, selectionID); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return libraryID, nil
}

// upsertCuratedItem 把沉淀后的文件登记成资源库条目，并写入检索索引。
// 精选库按路径镜像文件，sha256 列用「相对路径 + 大小」的稳定摘要占位，
// 避免为几十 GB 的素材做全量哈希。
func upsertCuratedItem(tx *sql.Tx, libraryID string, reg curatedRegistration) (string, error) {
	relative := strings.ReplaceAll(strings.TrimSpace(reg.Relative), "\\", "/")
	digest := sha256.Sum256([]byte("curated|" + relative + "|" + fmt.Sprintf("%d", reg.Bytes)))
	identity := "curated:" + hex.EncodeToString(digest[:16])
	itemID := stableID("item", libraryID+"|"+relative)
	now := nowRFC3339()
	metadata, _ := json.Marshal(map[string]any{
		"selection_asset_id": reg.AssetID,
		"source_path":        reg.SourcePath,
		"width":              reg.Width,
		"height":             reg.Height,
		"duration_ms":        reg.DurationMS,
		"fps":                reg.FPS,
		"codec":              reg.Codec,
		"bit_depth":          reg.BitDepth,
	})
	if _, err := tx.Exec(`INSERT INTO library_items(id,library_id,sha256,relative_path,local_path,kind,title,mime,bytes,adopted_by,adopted_at,updated_at,metadata_json,content_key,origin_selection_id)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
		  local_path=excluded.local_path, kind=excluded.kind, title=excluded.title,
		  bytes=excluded.bytes, updated_at=excluded.updated_at, metadata_json=excluded.metadata_json,
		  content_key=excluded.content_key, origin_selection_id=excluded.origin_selection_id,
		  deleted_at=''`,
		itemID, libraryID, identity, relative, reg.Destination, reg.Kind, reg.Title, "", reg.Bytes,
		"curation", now, now, string(metadata), contentKeyForRelative(relative, reg.Bytes), reg.SelectionID); err != nil {
		return "", err
	}
	if err := replaceSearchDocument(tx, searchDocument{
		EntryType: "library", EntryID: itemID, LibraryID: libraryID, Kind: reg.Kind, UpdatedAt: now,
		Title: reg.Title, Filename: filepath.Base(reg.Destination), Path: relative,
	}); err != nil {
		return "", err
	}
	return itemID, nil
}

// annotateLibraryKinds 给检索结果补上库类型与对应本机素材，前端据此显示「精选」徽标并复用缩略图。
func annotateLibraryKinds(db *sql.DB, items []resourceSearchItem) {
	ids := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		if item.EntryType == "library" && item.LibraryID != "" && !seen[item.LibraryID] {
			seen[item.LibraryID] = true
			ids = append(ids, item.LibraryID)
		}
	}
	kinds := map[string]string{}
	if len(ids) > 0 {
		placeholders, args := placeholders(ids)
		if rows, err := db.Query(`SELECT id,COALESCE(kind,'material') FROM libraries WHERE id IN (`+placeholders+`)`, args...); err == nil {
			for rows.Next() {
				var id, kind string
				if err := rows.Scan(&id, &kind); err == nil {
					kinds[id] = kind
				}
			}
			rows.Close()
		}
	}
	candidates := map[string]string{}
	metadataByID := map[string]map[string]any{}
	if len(items) > 0 {
		itemIDs := []string{}
		for _, item := range items {
			if item.EntryType == "library" && item.ID != "" {
				itemIDs = append(itemIDs, item.ID)
			}
		}
		if len(itemIDs) > 0 {
			placeholders, args := placeholders(itemIDs)
			if rows, err := db.Query(`SELECT id,metadata_json FROM library_items WHERE id IN (`+placeholders+`)`, args...); err == nil {
				for rows.Next() {
					var id, metadata string
					if err := rows.Scan(&id, &metadata); err != nil {
						continue
					}
					payload := map[string]any{}
					if json.Unmarshal([]byte(metadata), &payload) == nil {
						if assetID, ok := payload["selection_asset_id"].(string); ok && assetID != "" {
							candidates[id] = assetID
						}
						metadataByID[id] = payload
					}
				}
				rows.Close()
			}
		}
		// 旧数据没有 metadata 时，用沉淀记录里的目标路径反查。
		missingPaths := []string{}
		for _, item := range items {
			if item.EntryType == "library" && item.Path != "" && candidates[item.ID] == "" {
				missingPaths = append(missingPaths, item.Path)
			}
		}
		if len(missingPaths) > 0 {
			placeholders, args := placeholders(missingPaths)
			if rows, err := db.Query(`SELECT location_path,asset_id FROM curated_entries WHERE location_path IN (`+placeholders+`)`, args...); err == nil {
				byPath := map[string]string{}
				for rows.Next() {
					var location, assetID string
					if err := rows.Scan(&location, &assetID); err == nil {
						byPath[location] = assetID
					}
				}
				rows.Close()
				for _, item := range items {
					if item.EntryType == "library" && candidates[item.ID] == "" {
						if assetID, ok := byPath[item.Path]; ok {
							candidates[item.ID] = assetID
						}
					}
				}
			}
		}
	}
	for index := range items {
		if kind, ok := kinds[items[index].LibraryID]; ok {
			items[index].LibraryKind = kind
		}
		if items[index].EntryType == "library" {
			if candidateID, ok := candidates[items[index].ID]; ok {
				items[index].CandidateID = candidateID
			}
			// 精选库条目本身不存分辨率/时长，从沉淀时写入的 metadata_json 回填。
			if payload, ok := metadataByID[items[index].ID]; ok {
				items[index].Width = intValue(payload["width"])
				items[index].Height = intValue(payload["height"])
				items[index].DurationMS = int64(intValue(payload["duration_ms"]))
				items[index].FPS = floatValue(payload["fps"])
				items[index].Codec = stringValue(payload["codec"])
				items[index].BitDepth = intValue(payload["bit_depth"])
			}
		}
	}
}

func intValue(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case int64:
		return int(typed)
	}
	return 0
}

func floatValue(value any) float64 {
	if number, ok := value.(float64); ok {
		return number
	}
	return 0
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

// contentKeyFor 用「文件名 + 字节数」识别同一份内容：精选库条目是硬链接/副本，
// 路径与候选不同但这两个值一致，可据此在「全部」里只留一条。
func contentKeyFor(item resourceSearchItem) string {
	path := item.RelativePath
	if strings.TrimSpace(path) == "" {
		path = item.Path
	}
	name := strings.ToLower(filepath.Base(strings.ReplaceAll(path, "\\", "/")))
	if name == "" || name == "." {
		name = strings.ToLower(item.Title)
	}
	return fmt.Sprintf("%s|%d", name, item.Bytes)
}

// dedupeByContent 合并同一内容的候选与库条目：保留库条目（更“贵重”），
// 并标记 also_local 让界面提示「本机也有」。
func dedupeByContent(items []resourceSearchItem) []resourceSearchItem {
	seen := make(map[string]int, len(items))
	merged := make([]resourceSearchItem, 0, len(items))
	for _, item := range items {
		if item.Bytes == 0 && strings.TrimSpace(item.Path) == "" {
			merged = append(merged, item)
			continue
		}
		key := contentKeyFor(item)
		index, ok := seen[key]
		if !ok {
			seen[key] = len(merged)
			item.ContentKey = key
			merged = append(merged, item)
			continue
		}
		existing := &merged[index]
		switch {
		case existing.EntryType != "library" && item.EntryType == "library":
			item.AlsoLocal = true
			item.ContentKey = key
			*existing = item
		case existing.EntryType == "library" && item.EntryType != "library":
			existing.AlsoLocal = true
		}
	}
	return merged
}
func lastSelectionRun(db *sql.DB, selectionID string) map[string]any {
	var algorithm, resultJSON, createdAt string
	err := db.QueryRow(`SELECT algorithm,result_json,created_at FROM selection_runs
		WHERE selection_id=? ORDER BY created_at DESC LIMIT 1`, selectionID).
		Scan(&algorithm, &resultJSON, &createdAt)
	if err != nil {
		return map[string]any{}
	}
	result := map[string]any{}
	_ = json.Unmarshal([]byte(resultJSON), &result)
	return map[string]any{
		"algorithm": algorithm, "created_at": createdAt,
		"total": result["total"], "included": result["included"], "clusters": result["clusters"],
	}
}

func listCuratedSelections() (any, error) {
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT s.id,s.name,s.status,s.target_count,s.library_id,
		(SELECT COUNT(*) FROM selection_items i WHERE i.selection_id=s.id),
		(SELECT COUNT(*) FROM selection_items i WHERE i.selection_id=s.id AND i.state='included'),
		COALESCE(l.kind,''),COALESCE(l.root_uri,''),
		(SELECT COUNT(*) FROM library_items li WHERE li.library_id=s.library_id)
		FROM selections s LEFT JOIN libraries l ON l.id=s.library_id
		ORDER BY s.updated_at DESC`)
	if err != nil {
		return nil, err
	}
	type selectionRow struct {
		id, name, status, libraryID, libraryKind, rootPath string
		targetCount, total, included, libraryItems         int
	}
	collected := []selectionRow{}
	for rows.Next() {
		var row selectionRow
		if err := rows.Scan(&row.id, &row.name, &row.status, &row.targetCount, &row.libraryID, &row.total, &row.included,
			&row.libraryKind, &row.rootPath, &row.libraryItems); err != nil {
			rows.Close()
			return nil, err
		}
		collected = append(collected, row)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// 连接池只有 1 条连接：必须先把上面的结果集读完关闭，再逐条查最近一次预选，
	// 否则嵌套查询会把自己锁死。
	items := make([]map[string]any, 0, len(collected))
	for _, row := range collected {
		items = append(items, map[string]any{
			"id": row.id, "name": row.name, "status": row.status, "target_count": row.targetCount,
			"library_id": row.libraryID, "library_kind": row.libraryKind, "library_root": row.rootPath,
			"library_items": row.libraryItems, "total": row.total, "included": row.included,
			"last_run": lastSelectionRun(db, row.id),
		})
	}
	return map[string]any{"items": items, "total": len(items)}, nil
}
