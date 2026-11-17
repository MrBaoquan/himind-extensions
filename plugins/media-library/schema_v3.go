package main

import (
	"database/sql"
	"fmt"
	"strings"
)

// ensureV3Schema 把 2.x 的候选索引扩展为 v3 资产索引。
//
// 这里只做加法：保留 candidates 表名与现有检索 SQL，补充元数据列、缩略图缓存、
// 卷/位置/精选表。这样 2.x 的扫描、检索、采纳路径仍然可用，同时为后续
// “全类型元数据 + 精选沉淀”提供结构。
func ensureV3Schema(db *sql.DB) error {
	var applied int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE name='v3_assets_v1'`).Scan(&applied); err != nil {
		return err
	}
	if applied > 0 {
		return nil
	}

	candidateColumns := map[string]string{
		"volume_id":   "TEXT NOT NULL DEFAULT ''",
		"file_id":     "TEXT NOT NULL DEFAULT ''",
		"folder":      "TEXT NOT NULL DEFAULT ''",
		"width":       "INTEGER NOT NULL DEFAULT 0",
		"height":      "INTEGER NOT NULL DEFAULT 0",
		"duration_ms": "INTEGER NOT NULL DEFAULT 0",
		"fps":         "REAL NOT NULL DEFAULT 0",
		"codec":       "TEXT NOT NULL DEFAULT ''",
		"bit_depth":   "INTEGER NOT NULL DEFAULT 0",
		"hdr":         "TEXT NOT NULL DEFAULT ''",
		"rotation":    "INTEGER NOT NULL DEFAULT 0",
		"sample_rate": "INTEGER NOT NULL DEFAULT 0",
		"channels":    "INTEGER NOT NULL DEFAULT 0",
		"bitrate":     "INTEGER NOT NULL DEFAULT 0",
		"captured_at": "TEXT NOT NULL DEFAULT ''",
		"probe_state": "TEXT NOT NULL DEFAULT 'pending'",
		"thumb_state": "TEXT NOT NULL DEFAULT 'pending'",
		"meta_json":   "TEXT NOT NULL DEFAULT '{}'",
	}
	if err := ensureColumns(db, "candidates", candidateColumns); err != nil {
		return err
	}
	if err := ensureColumns(db, "source_roots", map[string]string{
		"metadata_mode": "TEXT NOT NULL DEFAULT 'db'",
	}); err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
CREATE TABLE IF NOT EXISTS thumb_cache (
  asset_id TEXT NOT NULL,
  size TEXT NOT NULL,
  path TEXT NOT NULL,
  bytes INTEGER NOT NULL DEFAULT 0,
  source_mtime TEXT NOT NULL DEFAULT '',
  last_used_at TEXT NOT NULL,
  PRIMARY KEY(asset_id, size)
);
CREATE TABLE IF NOT EXISTS volumes (
  id TEXT PRIMARY KEY,
  serial TEXT NOT NULL DEFAULT '',
  label TEXT NOT NULL DEFAULT '',
  filesystem TEXT NOT NULL DEFAULT '',
  last_seen_at TEXT NOT NULL,
  online INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE IF NOT EXISTS asset_locations (
  asset_id TEXT NOT NULL,
  path TEXT NOT NULL,
  source_id TEXT NOT NULL DEFAULT '',
  role TEXT NOT NULL DEFAULT 'original',
  identity TEXT NOT NULL DEFAULT 'file_id',
  PRIMARY KEY(asset_id, path)
);
CREATE TABLE IF NOT EXISTS selections (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  scope_json TEXT NOT NULL DEFAULT '{}',
  target_kind TEXT NOT NULL DEFAULT 'folder',
  target_path TEXT NOT NULL DEFAULT '',
  promote_mode TEXT NOT NULL DEFAULT 'hardlink',
  target_count INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'draft',
  criteria_json TEXT NOT NULL DEFAULT '{}',
  created_by TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS selection_items (
  selection_id TEXT NOT NULL,
  asset_id TEXT NOT NULL,
  state TEXT NOT NULL DEFAULT 'candidate',
  rank INTEGER,
  score REAL,
  score_json TEXT NOT NULL DEFAULT '{}',
  reason TEXT NOT NULL DEFAULT '',
  decided_by TEXT NOT NULL DEFAULT '',
  decided_at TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(selection_id, asset_id)
);
CREATE TABLE IF NOT EXISTS selection_runs (
  id TEXT PRIMARY KEY,
  selection_id TEXT NOT NULL,
  algorithm TEXT NOT NULL,
  version TEXT NOT NULL,
  params_json TEXT NOT NULL DEFAULT '{}',
  result_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS curated_entries (
  selection_id TEXT NOT NULL,
  asset_id TEXT NOT NULL,
  location_path TEXT NOT NULL,
  mode TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY(selection_id, asset_id)
);
CREATE INDEX IF NOT EXISTS idx_candidates_probe ON candidates(missing, probe_state, modified_at DESC);
CREATE INDEX IF NOT EXISTS idx_candidates_thumb ON candidates(missing, thumb_state, modified_at DESC);
CREATE INDEX IF NOT EXISTS idx_candidates_folder ON candidates(source_id, folder);
CREATE INDEX IF NOT EXISTS idx_candidates_meta ON candidates(kind, width, height);
CREATE INDEX IF NOT EXISTS idx_locations_asset ON asset_locations(asset_id);
`); err != nil {
		return err
	}
	// 老数据只有 relative_path；按“去掉文件名”回填 folder，供文件夹 facet 使用。
	if _, err := tx.Exec(`
UPDATE candidates
SET folder = CASE
  WHEN instr(relative_path, '/') > 0
  THEN substr(relative_path, 1, length(relative_path) - length(name) - 1)
  ELSE ''
END
WHERE folder = '' AND relative_path <> name`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations(name,applied_at) VALUES('v3_assets_v1',?)`, nowRFC3339()); err != nil {
		return err
	}
	return tx.Commit()
}

func ensureColumns(db *sql.DB, table string, wanted map[string]string) error {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	existing := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, kind string
		var notNull, primaryKey int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		existing[strings.ToLower(name)] = true
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for name, ddl := range wanted {
		if existing[strings.ToLower(name)] {
			continue
		}
		if _, err := db.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, name, ddl)); err != nil {
			return fmt.Errorf("新增字段 %s.%s 失败: %w", table, name, err)
		}
	}
	return nil
}
