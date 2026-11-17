package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

func isAcquisitionSubtitle(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".srt", ".vtt", ".ass", ".lrc":
		return true
	}
	return false
}

func acquisitionSubtitleParent(name string, files []acquisitionFile) (string, string) {
	best, parent, language := 0, "", ""
	for _, file := range files {
		if file.LibraryItemID == "" || file.Role == "subtitle" {
			continue
		}
		stem := strings.TrimSuffix(file.OriginalName, filepath.Ext(file.OriginalName))
		if len(stem) > best && strings.HasPrefix(name, stem+".") {
			best, parent = len(stem), file.LibraryItemID
			language = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSuffix(name, filepath.Ext(name)), stem), ".")
		}
	}
	return parent, language
}

func commitAcquisitionAttachment(ctx context.Context, db *sql.DB, record acquisitionRecord, owner string, file acquisitionFile, target, relative string) error {
	if file.ParentItemID == "" {
		return fmt.Errorf("字幕缺少对应素材")
	}
	provider, _, sourceURL, err := canonicalRemoteReference(record.URL)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`UPDATE acquisition_jobs SET updated_at=? WHERE id=? AND lease_owner=? AND lease_until>? AND cancel_requested=0`, nowRFC3339(), record.ID, owner, time.Now().Unix())
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return fmt.Errorf("采集已取消或执行租约已失效")
	}
	var parent int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM library_items WHERE id=? AND library_id=? AND deleted_at=''`, file.ParentItemID, record.LibraryID).Scan(&parent); err != nil {
		return err
	}
	if parent != 1 {
		return fmt.Errorf("字幕对应素材不可用")
	}
	now := nowRFC3339()
	if _, err = tx.Exec(`INSERT INTO asset_attachments(library_item_id,sha256,local_path,relative_path,role,language,original_name,bytes,source_url,acquisition_id,created_at)
 VALUES(?,?,?,?,'subtitle',?,?,?,?,?,?) ON CONFLICT(library_item_id,sha256) DO NOTHING`, file.ParentItemID, file.SHA256, target, relative, file.Language, file.OriginalName, file.Bytes, sourceURL, record.ID, now); err != nil {
		return err
	}
	event := catalogEvent{EventID: stableID("event", record.ID+":attachment:"+file.ParentItemID+":"+file.SHA256), Type: "asset.attachment", LibraryID: record.LibraryID, AssetID: file.ParentItemID, ParentAssetID: file.ParentItemID, RelativePath: relative, Title: file.OriginalName, SHA256: file.SHA256, SourceURL: sourceURL, SourceProvider: provider, Actor: "acquisition", CreatedAt: now, Role: "subtitle", Language: file.Language}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT OR IGNORE INTO acquisition_outbox(event_id,library_id,payload_json,created_at) VALUES(?,?,?,?)`, event.EventID, record.LibraryID, string(payload), now); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return tx.Commit()
}

func acquisitionProvenance(itemID string) (any, error) {
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var provider, id, sourceURL string
	if err = db.QueryRow(`SELECT source_provider,source_id,source_url FROM library_items WHERE id=?`, itemID).Scan(&provider, &id, &sourceURL); err != nil {
		return nil, fmt.Errorf("素材不存在")
	}
	rows, err := db.Query(`SELECT provider,provider_id,source_url,acquisition_id,created_at FROM asset_origins WHERE library_item_id=? ORDER BY created_at,provider,provider_id`, itemID)
	if err != nil {
		return nil, err
	}
	origins := []map[string]any{}
	known := map[string]bool{}
	for rows.Next() {
		var p, i, u, a, c string
		if err = rows.Scan(&p, &i, &u, &a, &c); err != nil {
			rows.Close()
			return nil, err
		}
		known[p+":"+i] = true
		origins = append(origins, map[string]any{"provider": p, "provider_id": i, "source_url": u, "acquisition_id": a, "created_at": c})
	}
	readErr := rows.Err()
	rows.Close()
	if readErr != nil {
		return nil, readErr
	}
	if sourceURL != "" && !known[provider+":"+id] {
		origins = append(origins, map[string]any{"provider": provider, "provider_id": id, "source_url": sourceURL})
	}
	rows, err = db.Query(`SELECT sha256,local_path,role,language,original_name,bytes FROM asset_attachments WHERE library_item_id=? ORDER BY original_name,sha256`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	attachments := []map[string]any{}
	for rows.Next() {
		var hash, path, role, language, name string
		var size int64
		if err = rows.Scan(&hash, &path, &role, &language, &name, &size); err != nil {
			return nil, err
		}
		attachments = append(attachments, map[string]any{"sha256": hash, "path": path, "role": role, "language": language, "name": name, "bytes": size})
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	derivations, err := audioProvenance(db, itemID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"item_id": itemID, "origins": origins, "attachments": attachments, "audio_derivations": derivations}, nil
}
