package main

import (
	"database/sql"
	"fmt"
	"strings"
)

// AI 作业引用：记住「哪个精选集正在跑哪个 Agent 后台作业」，
// 关窗/重开视图乃至 Agent 重启后都能接着看结果。
// 相似组成员关系不落库，重连时按精选集当前条目重新推导。

type aiJobRefInput struct {
	SelectionID string `json:"selection_id"`
	JobID       string `json:"job_id"`
	Profile     string `json:"profile"`
	Service     string `json:"service"`
}

func ensureAIJobRefTable(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS ai_job_state (
  selection_id TEXT PRIMARY KEY,
  job_id TEXT NOT NULL,
  profile TEXT NOT NULL DEFAULT 'fast',
  service TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);`)
	return err
}

func setAIJobRef(in aiJobRefInput) (any, error) {
	selectionID := strings.TrimSpace(in.SelectionID)
	jobID := strings.TrimSpace(in.JobID)
	if selectionID == "" || jobID == "" {
		return nil, fmt.Errorf("请指定精选集和作业")
	}
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err := ensureAIJobRefTable(db); err != nil {
		return nil, err
	}
	if _, err := db.Exec(`INSERT INTO ai_job_state(selection_id,job_id,profile,service,created_at)
		VALUES(?,?,?,?,?)
		ON CONFLICT(selection_id) DO UPDATE SET job_id=excluded.job_id,profile=excluded.profile,
			service=excluded.service,created_at=excluded.created_at`,
		selectionID, jobID, strings.TrimSpace(in.Profile), strings.TrimSpace(in.Service),
		nowRFC3339()); err != nil {
		return nil, err
	}
	return map[string]any{"saved": true}, nil
}

func getAIJobRef(in struct {
	SelectionID string `json:"selection_id"`
}) (any, error) {
	selectionID := strings.TrimSpace(in.SelectionID)
	if selectionID == "" {
		return nil, fmt.Errorf("请指定精选集")
	}
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err := ensureAIJobRefTable(db); err != nil {
		return nil, err
	}
	var jobID, profile, service, createdAt string
	err = db.QueryRow(`SELECT job_id,profile,service,created_at FROM ai_job_state WHERE selection_id=?`, selectionID).
		Scan(&jobID, &profile, &service, &createdAt)
	if err == sql.ErrNoRows {
		return map[string]any{"found": false}, nil
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"found": true, "selection_id": selectionID, "job_id": jobID,
		"profile": profile, "service": service, "created_at": createdAt,
	}, nil
}

func clearAIJobRef(in struct {
	SelectionID string `json:"selection_id"`
}) (any, error) {
	selectionID := strings.TrimSpace(in.SelectionID)
	if selectionID == "" {
		return nil, fmt.Errorf("请指定精选集")
	}
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err := ensureAIJobRefTable(db); err != nil {
		return nil, err
	}
	if _, err := db.Exec(`DELETE FROM ai_job_state WHERE selection_id=?`, selectionID); err != nil {
		return nil, err
	}
	return map[string]any{"cleared": true}, nil
}

func listAIJobRefs() (any, error) {
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err := ensureAIJobRefTable(db); err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT selection_id,job_id,profile,service,created_at FROM ai_job_state ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var selectionID, jobID, profile, service, createdAt string
		if err := rows.Scan(&selectionID, &jobID, &profile, &service, &createdAt); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{
			"selection_id": selectionID, "job_id": jobID, "profile": profile,
			"service": service, "created_at": createdAt,
		})
	}
	return map[string]any{"items": items}, rows.Err()
}
