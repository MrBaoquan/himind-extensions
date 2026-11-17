package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Imported history is a reference, never an acquisition or a transferred lease.
// The original downloader remains the owner of its tasks and source records.
type legacyDownload struct {
	JobID      string   `json:"job_id"`
	Status     string   `json:"status"`
	Title      string   `json:"title"`
	URL        string   `json:"url"`
	Outputs    []string `json:"outputs"`
	FinishedAt string   `json:"finished_at"`
	Owner      string   `json:"owner"`
}

var legacyJobPattern = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

func legacyHistoryDB() (*sql.DB, error) {
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS legacy_downloads(job_id TEXT PRIMARY KEY,metadata_json TEXT NOT NULL,imported_at TEXT NOT NULL)`); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func importLegacyDownloads(items []legacyDownload) (any, error) {
	if len(items) > 200 {
		return nil, fmt.Errorf("每次最多导入 200 条旧记录")
	}
	prepared := []legacyDownload{}
	skipped := 0
	for _, item := range items {
		if item.Status != "completed" && item.Status != "failed" && item.Status != "canceled" {
			skipped++
			continue
		}
		if !legacyJobPattern.MatchString(item.JobID) || len(item.Outputs) > 200 {
			return nil, fmt.Errorf("旧记录编号或输出数量无效")
		}
		_, _, canonical, err := canonicalRemoteReference(item.URL)
		if err != nil {
			skipped++
			continue
		}
		item.URL, item.Title, item.Owner = canonical, clipDiscoveryText(item.Title, 300), "com.himind.media-downloader"
		if item.FinishedAt != "" {
			if _, err := time.Parse(time.RFC3339Nano, item.FinishedAt); err != nil {
				return nil, fmt.Errorf("旧记录结束时间无效")
			}
		}
		paths := []string{}
		seen := map[string]bool{}
		for _, path := range item.Outputs {
			path = strings.TrimSpace(path)
			if !filepath.IsAbs(path) || len(path) > 2000 || strings.ContainsAny(path, "\r\n\x00") {
				return nil, fmt.Errorf("旧记录输出路径无效")
			}
			path = filepath.Clean(path)
			if !seen[path] {
				paths = append(paths, path)
				seen[path] = true
			}
		}
		item.Outputs = paths
		prepared = append(prepared, item)
	}
	db, err := legacyHistoryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	added := 0
	for _, item := range prepared {
		data, _ := json.Marshal(item)
		result, err := tx.Exec(`INSERT INTO legacy_downloads(job_id,metadata_json,imported_at) VALUES(?,?,?) ON CONFLICT(job_id) DO NOTHING`, item.JobID, string(data), nowRFC3339())
		if err != nil {
			return nil, err
		}
		count, _ := result.RowsAffected()
		added += int(count)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return map[string]any{"imported": added, "existing": len(prepared) - added, "skipped": skipped, "files_unchanged": true, "original_owner_preserved": true}, nil
}

func listLegacyDownloads() (any, error) {
	db, err := legacyHistoryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT metadata_json FROM legacy_downloads ORDER BY imported_at DESC,job_id DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []legacyDownload{}
	for rows.Next() {
		var data string
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		var item legacyDownload
		if err = json.Unmarshal([]byte(data), &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return map[string]any{"items": items, "count": len(items)}, nil
}
