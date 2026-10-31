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
	"time"

	_ "modernc.org/sqlite"
)

const libraryDBSchema = `
CREATE TABLE IF NOT EXISTS libraries (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  backend_type TEXT NOT NULL,
  root_uri TEXT NOT NULL,
  credential_ref TEXT NOT NULL DEFAULT '',
  access_mode TEXT NOT NULL DEFAULT 'contribute',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(backend_type, root_uri)
);
CREATE TABLE IF NOT EXISTS source_roots (
  id TEXT PRIMARY KEY,
  path TEXT NOT NULL UNIQUE,
  display_name TEXT NOT NULL,
  scan_generation TEXT NOT NULL DEFAULT '',
  scan_status TEXT NOT NULL DEFAULT 'pending',
  last_scan_at TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS scan_queue (
  source_id TEXT NOT NULL,
  path TEXT NOT NULL,
  depth INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(source_id, path)
);
CREATE TABLE IF NOT EXISTS candidates (
  id TEXT PRIMARY KEY,
  source_id TEXT NOT NULL,
  path TEXT NOT NULL UNIQUE,
  relative_path TEXT NOT NULL,
  name TEXT NOT NULL,
  kind TEXT NOT NULL,
  extension TEXT NOT NULL,
  bytes INTEGER NOT NULL,
  modified_at TEXT NOT NULL,
  seen_generation TEXT NOT NULL DEFAULT '',
  missing INTEGER NOT NULL DEFAULT 0,
  indexed_level INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS library_items (
  id TEXT PRIMARY KEY,
  library_id TEXT NOT NULL,
  sha256 TEXT NOT NULL,
  relative_path TEXT NOT NULL,
  local_path TEXT NOT NULL,
  kind TEXT NOT NULL,
  title TEXT NOT NULL,
  mime TEXT NOT NULL DEFAULT '',
  bytes INTEGER NOT NULL DEFAULT 0,
  author TEXT NOT NULL DEFAULT '',
  license_name TEXT NOT NULL DEFAULT '',
  license_url TEXT NOT NULL DEFAULT '',
  redistribution_allowed INTEGER NOT NULL DEFAULT 0,
  source_provider TEXT NOT NULL DEFAULT '',
  source_id TEXT NOT NULL DEFAULT '',
  source_url TEXT NOT NULL DEFAULT '',
  adopted_by TEXT NOT NULL DEFAULT '',
  adopted_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  metadata_json TEXT NOT NULL DEFAULT '{}',
  UNIQUE(library_id, sha256)
);
CREATE TABLE IF NOT EXISTS schema_migrations (
  name TEXT PRIMARY KEY,
  applied_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_candidates_recent ON candidates(missing, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_candidates_kind_recent ON candidates(missing, kind, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_library_recent ON library_items(adopted_at DESC);
CREATE INDEX IF NOT EXISTS idx_library_kind_recent ON library_items(kind, adopted_at DESC);
CREATE INDEX IF NOT EXISTS idx_library_scope ON library_items(library_id, kind, adopted_at DESC);
CREATE VIRTUAL TABLE IF NOT EXISTS search_fts USING fts5(
  entry_type UNINDEXED,
  entry_id UNINDEXED,
  library_id UNINDEXED,
  content
);
CREATE VIRTUAL TABLE IF NOT EXISTS search_fts_v2 USING fts5(
  entry_type UNINDEXED,
  entry_id UNINDEXED,
  library_id UNINDEXED,
  kind UNINDEXED,
  updated_at UNINDEXED,
  title,
  filename,
  path,
  tags,
  collection,
  license,
  author,
  tokenize='unicode61 remove_diacritics 2'
);
`

type libraryRecord struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	BackendType   string `json:"backend_type"`
	RootURI       string `json:"root_uri"`
	CredentialRef string `json:"credential_ref,omitempty"`
	AccessMode    string `json:"access_mode"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
	AssetCount    int    `json:"asset_count"`
	State         string `json:"state"`
}

type libraryManifest struct {
	SchemaVersion int      `json:"schema_version"`
	LibraryID     string   `json:"library_id"`
	Name          string   `json:"name"`
	BackendType   string   `json:"backend_type"`
	AccessMode    string   `json:"access_mode"`
	CreatedAt     string   `json:"created_at"`
	Features      []string `json:"features"`
}

type updateLibraryInput struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	AccessMode string `json:"access_mode"`
}

type removeLibraryInput struct {
	ID string `json:"id"`
}

type updateSourceInput struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

type removeSourceInput struct {
	ID string `json:"id"`
}

type createLibraryInput struct {
	Name          string `json:"name"`
	BackendType   string `json:"backend_type"`
	RootPath      string `json:"root_path"`
	CredentialRef string `json:"credential_ref"`
	AccessMode    string `json:"access_mode"`
}

type sourceRoot struct {
	ID          string `json:"id"`
	Path        string `json:"path"`
	DisplayName string `json:"display_name"`
	ScanStatus  string `json:"scan_status"`
	LastScanAt  string `json:"last_scan_at"`
	CreatedAt   string `json:"created_at"`
}

type sourceScanInput struct {
	SourceID   string `json:"source_id"`
	Restart    bool   `json:"restart"`
	MaxEntries int    `json:"max_entries"`
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func randomID(prefix string) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d", prefix, time.Now().UnixNano(), os.Getpid())))
	return prefix + "-" + hex.EncodeToString(digest[:16])
}

func stableID(prefix string, value string) string {
	digest := sha256.Sum256([]byte(prefix + "|" + value))
	return prefix + "-" + hex.EncodeToString(digest[:16])
}

func openLibraryDB() (*sql.DB, error) {
	root, err := pluginDataRoot()
	if err != nil {
		return nil, err
	}
	dbPath := filepath.Join(root, "library.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
		"PRAGMA journal_mode=WAL",
		"PRAGMA temp_store=MEMORY",
		"PRAGMA cache_size=-32768",
		"PRAGMA mmap_size=134217728",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, err
		}
	}
	if _, err := db.Exec(libraryDBSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化资源库索引失败: %w", err)
	}
	if err := ensureSearchIndexV2(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化新版检索索引失败: %w", err)
	}
	return db, nil
}

func normalizeLibraryRoot(path string, backendType string) (string, error) {
	value := strings.TrimSpace(path)
	if value == "" {
		return "", fmt.Errorf("请指定资源库根目录")
	}
	if backendType == "smb" {
		value = strings.ReplaceAll(value, "/", "\\")
		if !strings.HasPrefix(value, `\\`) {
			return "", fmt.Errorf("SMB 资源库必须使用 UNC 路径，例如 \\\\server\\share\\library")
		}
		return filepath.Clean(value), nil
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	return absolute, nil
}

func createLibrary(in createLibraryInput) (any, error) {
	backend := strings.ToLower(strings.TrimSpace(in.BackendType))
	if backend != "local" && backend != "smb" {
		return nil, fmt.Errorf("backend_type 必须是 local 或 smb")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("请填写资源库名称")
	}
	root, err := normalizeLibraryRoot(in.RootPath, backend)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0o755); err != nil {
		return nil, fmt.Errorf("创建资源库目录失败: %w", err)
	}
	metaDir := filepath.Join(root, ".himind-media")
	if err := os.MkdirAll(filepath.Join(metaDir, "catalog", "users"), 0o755); err != nil {
		return nil, fmt.Errorf("创建共享元数据目录失败: %w", err)
	}
	manifestPath := filepath.Join(metaDir, "library.json")
	now := nowRFC3339()
	manifest := libraryManifest{
		SchemaVersion: 1,
		LibraryID:     randomID("lib"),
		Name:          name,
		BackendType:   backend,
		AccessMode:    firstNonEmpty(strings.TrimSpace(in.AccessMode), "contribute"),
		CreatedAt:     now,
		Features:      []string{"assets", "adoption", "catalog-events", "local-index"},
	}
	if payload, err := os.ReadFile(manifestPath); err == nil {
		if err := json.Unmarshal(payload, &manifest); err != nil {
			return nil, fmt.Errorf("library.json 无法解析: %w", err)
		}
		if manifest.LibraryID == "" {
			return nil, fmt.Errorf("library.json 缺少 library_id")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	} else if err := writeJSONAtomic(manifestPath, manifest); err != nil {
		return nil, err
	}

	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var existingID string
	err = db.QueryRow(`SELECT id FROM libraries WHERE backend_type=? AND root_uri=?`, backend, root).Scan(&existingID)
	if err == nil {
		manifest.LibraryID = existingID
	} else if err != sql.ErrNoRows {
		return nil, err
	}
	_, err = db.Exec(`INSERT INTO libraries(id,name,backend_type,root_uri,credential_ref,access_mode,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(backend_type,root_uri) DO UPDATE SET
		  name=excluded.name, credential_ref=excluded.credential_ref,
		  access_mode=excluded.access_mode, updated_at=excluded.updated_at`,
		manifest.LibraryID, name, backend, root, strings.TrimSpace(in.CredentialRef), manifest.AccessMode, now, now)
	if err != nil {
		return nil, err
	}
	return getLibrary(manifest.LibraryID)
}

func getLibrary(id string) (*libraryRecord, error) {
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return getLibraryWithDB(db, id)
}

func getLibraryWithDB(db *sql.DB, id string) (*libraryRecord, error) {
	var item libraryRecord
	err := db.QueryRow(`SELECT l.id,l.name,l.backend_type,l.root_uri,l.credential_ref,l.access_mode,l.created_at,l.updated_at,
		(SELECT COUNT(*) FROM library_items i WHERE i.library_id=l.id)
		FROM libraries l WHERE l.id=?`, id).Scan(
		&item.ID, &item.Name, &item.BackendType, &item.RootURI, &item.CredentialRef, &item.AccessMode, &item.CreatedAt, &item.UpdatedAt, &item.AssetCount)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("资源库不存在: %s", id)
	}
	if err != nil {
		return nil, err
	}
	item.State = "offline"
	if info, statErr := os.Stat(item.RootURI); statErr == nil && info.IsDir() {
		item.State = "online"
	}
	return &item, nil
}

func listLibraries() (any, error) {
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT l.id,l.name,l.backend_type,l.root_uri,l.credential_ref,l.access_mode,l.created_at,l.updated_at,
		(SELECT COUNT(*) FROM library_items i WHERE i.library_id=l.id)
		FROM libraries l ORDER BY l.name,l.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []libraryRecord{}
	for rows.Next() {
		var item libraryRecord
		if err := rows.Scan(&item.ID, &item.Name, &item.BackendType, &item.RootURI, &item.CredentialRef, &item.AccessMode, &item.CreatedAt, &item.UpdatedAt, &item.AssetCount); err != nil {
			return nil, err
		}
		item.State = "offline"
		if info, statErr := os.Stat(item.RootURI); statErr == nil && info.IsDir() {
			item.State = "online"
		}
		items = append(items, item)
	}
	return map[string]any{"libraries": items, "total": len(items)}, rows.Err()
}

func updateLibrary(in updateLibraryInput) (any, error) {
	id := strings.TrimSpace(in.ID)
	name := strings.TrimSpace(in.Name)
	if id == "" {
		return nil, fmt.Errorf("请指定资源库")
	}
	if name == "" {
		return nil, fmt.Errorf("请填写资源库名称")
	}
	accessMode := strings.TrimSpace(in.AccessMode)
	if accessMode != "contribute" && accessMode != "read_only" {
		return nil, fmt.Errorf("访问模式必须是 contribute 或 read_only")
	}
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	library, err := getLibraryWithDB(db, id)
	if err != nil {
		return nil, err
	}
	now := nowRFC3339()
	if _, err := db.Exec(`UPDATE libraries SET name=?,access_mode=?,updated_at=? WHERE id=?`, name, accessMode, now, id); err != nil {
		return nil, err
	}
	manifestPath := filepath.Join(library.RootURI, ".himind-media", "library.json")
	if payload, readErr := os.ReadFile(manifestPath); readErr == nil {
		var manifest libraryManifest
		if err := json.Unmarshal(payload, &manifest); err != nil {
			return nil, fmt.Errorf("library.json 无法解析: %w", err)
		}
		manifest.Name = name
		manifest.AccessMode = accessMode
		if err := writeJSONAtomic(manifestPath, manifest); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(readErr) {
		return nil, readErr
	}
	return getLibraryWithDB(db, id)
}

func removeLibrary(in removeLibraryInput) (any, error) {
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return nil, fmt.Errorf("请指定资源库")
	}
	library, err := getLibrary(id)
	if err != nil {
		return nil, err
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
	if _, err := tx.Exec(`DELETE FROM search_fts WHERE entry_type='library' AND entry_id IN (SELECT id FROM library_items WHERE library_id=?)`, id); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM search_fts_v2 WHERE entry_type='library' AND entry_id IN (SELECT id FROM library_items WHERE library_id=?)`, id); err != nil {
		return nil, err
	}
	result, err := tx.Exec(`DELETE FROM library_items WHERE library_id=?`, id)
	if err != nil {
		return nil, err
	}
	removedItems, _ := result.RowsAffected()
	if _, err := tx.Exec(`DELETE FROM libraries WHERE id=?`, id); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "name": library.Name, "removed_items": removedItems, "disk_files_preserved": true}, nil
}

func addSourceRoot(path string, displayName string) (any, error) {
	value := strings.TrimSpace(path)
	if value == "" {
		return nil, fmt.Errorf("请选择要索引的本地文件夹")
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("文件夹不可用: %s", value)
	}
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var existing sourceRoot
	err = db.QueryRow(`SELECT id,path,display_name,scan_status,last_scan_at,created_at FROM source_roots WHERE path=?`, absolute).Scan(
		&existing.ID, &existing.Path, &existing.DisplayName, &existing.ScanStatus, &existing.LastScanAt, &existing.CreatedAt)
	if err == nil {
		return existing, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	name := strings.TrimSpace(displayName)
	if name == "" {
		name = filepath.Base(absolute)
	}
	item := sourceRoot{ID: randomID("src"), Path: absolute, DisplayName: name, ScanStatus: "pending", CreatedAt: nowRFC3339()}
	_, err = db.Exec(`INSERT INTO source_roots(id,path,display_name,scan_status,created_at) VALUES(?,?,?,?,?)`,
		item.ID, item.Path, item.DisplayName, item.ScanStatus, item.CreatedAt)
	if err != nil {
		return nil, err
	}
	return item, nil
}

func updateSourceRoot(in updateSourceInput) (any, error) {
	id := strings.TrimSpace(in.ID)
	name := strings.TrimSpace(in.DisplayName)
	if id == "" {
		return nil, fmt.Errorf("请指定本机来源")
	}
	if name == "" {
		return nil, fmt.Errorf("请填写来源名称")
	}
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	result, err := db.Exec(`UPDATE source_roots SET display_name=? WHERE id=?`, name, id)
	if err != nil {
		return nil, err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return nil, fmt.Errorf("本机来源不存在")
	}
	var item sourceRoot
	if err := db.QueryRow(`SELECT id,path,display_name,scan_status,last_scan_at,created_at FROM source_roots WHERE id=?`, id).Scan(&item.ID, &item.Path, &item.DisplayName, &item.ScanStatus, &item.LastScanAt, &item.CreatedAt); err != nil {
		return nil, err
	}
	return item, nil
}

func removeSourceRoot(in removeSourceInput) (any, error) {
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return nil, fmt.Errorf("请指定本机来源")
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
	if _, err := tx.Exec(`DELETE FROM search_fts WHERE entry_type='candidate' AND entry_id IN (SELECT id FROM candidates WHERE source_id=?)`, id); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM search_fts_v2 WHERE entry_type='candidate' AND entry_id IN (SELECT id FROM candidates WHERE source_id=?)`, id); err != nil {
		return nil, err
	}
	result, err := tx.Exec(`DELETE FROM candidates WHERE source_id=?`, id)
	if err != nil {
		return nil, err
	}
	removedItems, _ := result.RowsAffected()
	if _, err := tx.Exec(`DELETE FROM scan_queue WHERE source_id=?`, id); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM source_roots WHERE id=?`, id); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "removed_candidates": removedItems, "disk_files_preserved": true}, nil
}

func scanSourceBatch(in sourceScanInput) (any, error) {
	sourceID := strings.TrimSpace(in.SourceID)
	if sourceID == "" {
		return nil, fmt.Errorf("请指定索引来源")
	}
	maxEntries := in.MaxEntries
	if maxEntries <= 0 {
		maxEntries = 200
	}
	if maxEntries > 2000 {
		maxEntries = 2000
	}
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var root, status, generation string
	if err := db.QueryRow(`SELECT path,scan_status,scan_generation FROM source_roots WHERE id=?`, sourceID).Scan(&root, &status, &generation); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("索引来源不存在: %s", sourceID)
		}
		return nil, err
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if in.Restart || generation == "" || status == "complete" {
		generation = nowRFC3339()
		if _, err := tx.Exec(`DELETE FROM scan_queue WHERE source_id=?`, sourceID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO scan_queue(source_id,path,depth) VALUES(?,?,0)`, sourceID, root); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`UPDATE source_roots SET scan_generation=?,scan_status='scanning' WHERE id=?`, generation, sourceID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`UPDATE candidates SET seen_generation='' WHERE source_id=?`, sourceID); err != nil {
			return nil, err
		}
	}
	processed := 0
	indexedFiles := 0
	done := false
	for processed < maxEntries {
		var dir string
		err := tx.QueryRow(`SELECT path FROM scan_queue WHERE source_id=? ORDER BY depth,path LIMIT 1`, sourceID).Scan(&dir)
		if err == sql.ErrNoRows {
			done = true
			break
		}
		if err != nil {
			return nil, err
		}
		entries, readErr := os.ReadDir(dir)
		if readErr == nil {
			for _, entry := range entries {
				if processed >= maxEntries {
					break
				}
				if entry.Type()&os.ModeSymlink != 0 {
					continue
				}
				path := filepath.Join(dir, entry.Name())
				if entry.IsDir() {
					switch strings.ToLower(entry.Name()) {
					case ".git", "node_modules", ".himind", ".himind-media":
						continue
					}
					if _, err := tx.Exec(`INSERT OR IGNORE INTO scan_queue(source_id,path,depth)
						SELECT ?,?,COALESCE((SELECT depth FROM scan_queue WHERE source_id=? AND path=?),0)+1`,
						sourceID, path, sourceID, dir); err != nil {
						return nil, err
					}
					processed++
					continue
				}
				kind, ok := mediaExtensions[strings.ToLower(filepath.Ext(path))]
				if !ok {
					continue
				}
				info, infoErr := entry.Info()
				if infoErr != nil {
					continue
				}
				relative, _ := filepath.Rel(root, path)
				id := stableID("candidate", path)
				now := nowRFC3339()
				_, err := tx.Exec(`INSERT INTO candidates(id,source_id,path,relative_path,name,kind,extension,bytes,modified_at,seen_generation,created_at,updated_at)
					VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
					ON CONFLICT(path) DO UPDATE SET source_id=excluded.source_id, relative_path=excluded.relative_path,
					name=excluded.name, kind=excluded.kind, extension=excluded.extension, bytes=excluded.bytes,
					modified_at=excluded.modified_at, seen_generation=excluded.seen_generation, missing=0, updated_at=excluded.updated_at`,
					id, sourceID, path, filepath.ToSlash(relative), entry.Name(), kind, strings.ToLower(filepath.Ext(path)),
					info.Size(), info.ModTime().UTC().Format(time.RFC3339), generation, now, now)
				if err != nil {
					return nil, err
				}
				if err := replaceSearchDocument(tx, searchDocument{
					EntryType: "candidate", EntryID: id, Kind: kind, UpdatedAt: now,
					Title: entry.Name(), Filename: entry.Name(), Path: filepath.ToSlash(relative),
				}); err != nil {
					return nil, err
				}
				indexedFiles++
				processed++
			}
		}
		if _, err := tx.Exec(`DELETE FROM scan_queue WHERE source_id=? AND path=?`, sourceID, dir); err != nil {
			return nil, err
		}
	}
	var remaining int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM scan_queue WHERE source_id=?`, sourceID).Scan(&remaining); err != nil {
		return nil, err
	}
	if remaining == 0 {
		done = true
		if _, err := tx.Exec(`UPDATE candidates SET missing=1 WHERE source_id=? AND seen_generation<>?`, sourceID, generation); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`UPDATE source_roots SET scan_status='complete',last_scan_at=? WHERE id=?`, nowRFC3339(), sourceID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return map[string]any{
		"source_id":     sourceID,
		"generation":    generation,
		"processed":     processed,
		"indexed_files": indexedFiles,
		"remaining":     remaining,
		"done":          done,
	}, nil
}

type searchDocument struct {
	EntryType  string
	EntryID    string
	LibraryID  string
	Kind       string
	UpdatedAt  string
	Title      string
	Filename   string
	Path       string
	Tags       string
	Collection string
	License    string
	Author     string
}

func replaceSearchDocument(tx *sql.Tx, doc searchDocument) error {
	content := strings.Join([]string{doc.Title, doc.Filename, doc.Path, doc.Tags, doc.Collection, doc.License, doc.Author}, " ")
	if _, err := tx.Exec(`DELETE FROM search_fts WHERE entry_type=? AND entry_id=?`, doc.EntryType, doc.EntryID); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO search_fts(entry_type,entry_id,library_id,content) VALUES(?,?,?,?)`, doc.EntryType, doc.EntryID, doc.LibraryID, content); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM search_fts_v2 WHERE entry_type=? AND entry_id=?`, doc.EntryType, doc.EntryID); err != nil {
		return err
	}
	_, err := tx.Exec(`INSERT INTO search_fts_v2(entry_type,entry_id,library_id,kind,updated_at,title,filename,path,tags,collection,license,author) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		doc.EntryType, doc.EntryID, doc.LibraryID, doc.Kind, doc.UpdatedAt, doc.Title, doc.Filename, doc.Path, doc.Tags, doc.Collection, doc.License, doc.Author)
	return err
}

func ensureSearchIndexV2(db *sql.DB) error {
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE name='search_fts_v2_v1'`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO search_fts_v2(entry_type,entry_id,library_id,kind,updated_at,title,filename,path,tags,collection,license,author)
		SELECT 'candidate',id,'',kind,updated_at,name,name,relative_path,'','','',''
		FROM candidates`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO search_fts_v2(entry_type,entry_id,library_id,kind,updated_at,title,filename,path,tags,collection,license,author)
		SELECT 'library',id,library_id,kind,updated_at,title,title,relative_path,
		CASE WHEN json_valid(metadata_json) THEN COALESCE((SELECT group_concat(value,' ') FROM json_each(json_extract(metadata_json,'$.tags'))),'') ELSE '' END,
		CASE WHEN json_valid(metadata_json) THEN COALESCE(json_extract(metadata_json,'$.collection'),'') ELSE '' END,
		license_name,author
		FROM library_items`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO search_fts_v2(search_fts_v2) VALUES('optimize')`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations(name,applied_at) VALUES('search_fts_v2_v1',?)`, nowRFC3339()); err != nil {
		return err
	}
	return tx.Commit()
}

func writeJSONAtomic(path string, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, append(encoded, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func listSourceRoots() (any, error) {
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT id,path,display_name,scan_status,last_scan_at,created_at FROM source_roots ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []sourceRoot{}
	for rows.Next() {
		var item sourceRoot
		if err := rows.Scan(&item.ID, &item.Path, &item.DisplayName, &item.ScanStatus, &item.LastScanAt, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return map[string]any{"sources": items, "total": len(items)}, rows.Err()
}
