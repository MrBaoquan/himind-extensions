package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 用户数据：标签、评分、备注。默认只写本机 DB；来源设置为 jsonl/both 时，
// 额外追加到 <来源>/.himind-media/user-data.jsonl，便于随素材盘一起搬走。
// JSONL 是追加式事件，DB 是物化视图；读取以 DB 为准，重建可在后续版本补。

type tagsInput struct {
	AssetID string   `json:"asset_id"`
	Tags    []string `json:"tags"`
}

type ratingInput struct {
	AssetID string `json:"asset_id"`
	Rating  int    `json:"rating"`
}

type userStateInput struct {
	AssetID string `json:"asset_id"`
}

type noteInput struct {
	AssetID string `json:"asset_id"`
	Note    string `json:"note"`
}

type userStateRecord struct {
	AssetID string   `json:"asset_id"`
	Tags    []string `json:"tags"`
	Rating  int      `json:"rating"`
	Note    string   `json:"note"`
	Mode    string   `json:"mode"`
	Updated string   `json:"updated_at"`
}

func ensureUserStateSchema(db *sql.DB) error {
	var applied int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE name='v3_user_state_v1'`).Scan(&applied); err != nil {
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
	if _, err := tx.Exec(`
CREATE TABLE IF NOT EXISTS user_asset_state (
  asset_id TEXT PRIMARY KEY,
  asset_key TEXT NOT NULL DEFAULT '',
  tags_json TEXT NOT NULL DEFAULT '[]',
  rating INTEGER NOT NULL DEFAULT 0,
  note TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_user_state_updated ON user_asset_state(updated_at DESC);
`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations(name,applied_at) VALUES('v3_user_state_v1',?)`, nowRFC3339()); err != nil {
		return err
	}
	return tx.Commit()
}

func getAssetUserState(in userStateInput) (any, error) {
	assetID := strings.TrimSpace(in.AssetID)
	if assetID == "" {
		return nil, fmt.Errorf("请指定资产")
	}
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return loadUserState(db, assetID)
}

func loadUserState(db *sql.DB, assetID string) (userStateRecord, error) {
	record := userStateRecord{AssetID: assetID, Tags: []string{}}
	var tagsJSON string
	err := db.QueryRow(`SELECT COALESCE(tags_json,'[]'),COALESCE(rating,0),COALESCE(note,''),updated_at
		FROM user_asset_state WHERE asset_id=?`, assetID).Scan(&tagsJSON, &record.Rating, &record.Note, &record.Updated)
	if err == sql.ErrNoRows {
		record.Mode = "db"
	} else if err != nil {
		return record, err
	} else {
		_ = json.Unmarshal([]byte(tagsJSON), &record.Tags)
	}
	var mode string
	_ = db.QueryRow(`SELECT COALESCE(s.metadata_mode,'db') FROM candidates c
		LEFT JOIN source_roots s ON s.id=c.source_id WHERE c.id=?`, assetID).Scan(&mode)
	record.Mode = firstNonEmpty(mode, "db")
	return record, nil
}

func setAssetTags(in tagsInput) (any, error) {
	assetID := strings.TrimSpace(in.AssetID)
	if assetID == "" {
		return nil, fmt.Errorf("请指定资产")
	}
	tags := normalizeUserTags(in.Tags)
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var assetKey, name, kind, relativePath, updatedAt string
	if err := db.QueryRow(`SELECT COALESCE(volume_id,'')||'/'||COALESCE(file_id,''),name,kind,relative_path,updated_at
		FROM candidates WHERE id=?`, assetID).Scan(&assetKey, &name, &kind, &relativePath, &updatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("资产不存在")
		}
		return nil, err
	}
	encoded, _ := json.Marshal(tags)
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO user_asset_state(asset_id,asset_key,tags_json,updated_at) VALUES(?,?,?,?)
		ON CONFLICT(asset_id) DO UPDATE SET asset_key=excluded.asset_key,tags_json=excluded.tags_json,updated_at=excluded.updated_at`,
		assetID, assetKey, string(encoded), nowRFC3339()); err != nil {
		return nil, err
	}
	if err := replaceSearchDocument(tx, searchDocument{
		EntryType: "candidate", EntryID: assetID, Kind: kind, UpdatedAt: updatedAt,
		Title: name, Filename: name, Path: relativePath, Tags: strings.Join(tags, " "),
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	_ = appendUserEvent(db, assetID, "tags.set", tags, nil, nil)
	return loadUserState(db, assetID)
}

func setAssetNote(in noteInput) (any, error) {
	assetID := strings.TrimSpace(in.AssetID)
	if assetID == "" {
		return nil, fmt.Errorf("请指定资产")
	}
	note := strings.TrimSpace(in.Note)
	if len([]rune(note)) > 10000 {
		return nil, fmt.Errorf("OCR 文本超过 10000 字")
	}
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var name, kind, relativePath, updatedAt, tagsJSON string
	if err := db.QueryRow(`SELECT name,kind,relative_path,updated_at,COALESCE((SELECT tags_json FROM user_asset_state WHERE asset_id=c.id),'[]')
		FROM candidates c WHERE id=?`, assetID).Scan(&name, &kind, &relativePath, &updatedAt, &tagsJSON); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("资产不存在")
		}
		return nil, err
	}
	var tags []string
	_ = json.Unmarshal([]byte(tagsJSON), &tags)
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO user_asset_state(asset_id,note,updated_at) VALUES(?,?,?)
		ON CONFLICT(asset_id) DO UPDATE SET note=excluded.note,updated_at=excluded.updated_at`,
		assetID, note, nowRFC3339()); err != nil {
		return nil, err
	}
	searchable := strings.TrimSpace(strings.Join(tags, " ") + " " + note)
	if err := replaceSearchDocument(tx, searchDocument{
		EntryType: "candidate", EntryID: assetID, Kind: kind, UpdatedAt: updatedAt,
		Title: name, Filename: name, Path: relativePath, Tags: searchable,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	_ = appendUserEvent(db, assetID, "note.set", nil, nil, &note)
	return loadUserState(db, assetID)
}

func setAssetRating(in ratingInput) (any, error) {
	assetID := strings.TrimSpace(in.AssetID)
	if assetID == "" {
		return nil, fmt.Errorf("请指定资产")
	}
	if in.Rating < 0 || in.Rating > 5 {
		return nil, fmt.Errorf("评分必须在 0 到 5 之间")
	}
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var assetKey string
	if err := db.QueryRow(`SELECT COALESCE(volume_id,'')||'/'||COALESCE(file_id,'') FROM candidates WHERE id=?`, assetID).Scan(&assetKey); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("资产不存在")
		}
		return nil, err
	}
	if _, err := db.Exec(`INSERT INTO user_asset_state(asset_id,asset_key,rating,updated_at) VALUES(?,?,?,?)
		ON CONFLICT(asset_id) DO UPDATE SET rating=excluded.rating,updated_at=excluded.updated_at`,
		assetID, assetKey, in.Rating, nowRFC3339()); err != nil {
		return nil, err
	}
	_ = appendUserEvent(db, assetID, "rating.set", nil, &in.Rating, nil)
	return loadUserState(db, assetID)
}

func normalizeUserTags(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		for _, part := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == '，' || r == ';' || r == '；' || r == '、' }) {
			part = strings.TrimSpace(part)
			if part == "" || seen[part] || len([]rune(part)) > 60 {
				continue
			}
			seen[part] = true
			out = append(out, part)
			if len(out) >= 20 {
				return out
			}
		}
	}
	return out
}

func appendUserEvent(db *sql.DB, assetID string, op string, tags []string, rating *int, note *string) error {
	var sourcePath, mode string
	if err := db.QueryRow(`SELECT s.path,COALESCE(s.metadata_mode,'db') FROM candidates c
		JOIN source_roots s ON s.id=c.source_id WHERE c.id=?`, assetID).Scan(&sourcePath, &mode); err != nil {
		return err
	}
	if mode != "jsonl" && mode != "both" {
		return nil
	}
	directory := filepath.Join(sourcePath, ".himind-media")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	event := map[string]any{
		"event_id":   randomID("event"),
		"asset_id":   assetID,
		"op":         op,
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"actor":      safeActor(""),
	}
	if tags != nil {
		event["tags"] = tags
	}
	if rating != nil {
		event["rating"] = *rating
	}
	if note != nil {
		event["note"] = *note
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(directory, "user-data.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(append(payload, '\n'))
	return err
}
