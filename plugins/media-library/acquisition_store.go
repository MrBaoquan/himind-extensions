package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/MrBaoquan/himind-extensions/internal/mediadownload"
)

type acquisitionStartInput struct {
	mediadownload.JobStartInput
	Purpose           string                   `json:"purpose,omitempty"`
	Music             *mediadownload.MusicInfo `json:"music,omitempty"`
	Audio             *audioProcess            `json:"audio_process,omitempty"`
	Mode              string                   `json:"mode"`
	LibraryID         string                   `json:"library_id,omitempty"`
	IdempotencyKey    string                   `json:"idempotency_key"`
	RemoteID          string                   `json:"remote_id,omitempty"`
	AccountGeneration string                   `json:"-"`
}

type acquisitionFile struct {
	DurationMS    int64  `json:"duration_ms,omitempty"`
	Path          string `json:"path"`
	SHA256        string `json:"sha256,omitempty"`
	Bytes         int64  `json:"bytes"`
	LibraryItemID string `json:"library_item_id,omitempty"`
	OriginalName  string `json:"original_name,omitempty"`
	Role          string `json:"role,omitempty"`
	ParentItemID  string `json:"parent_item_id,omitempty"`
	Language      string `json:"language,omitempty"`
}

type acquisitionRecord struct {
	Purpose           string                `json:"purpose,omitempty"`
	TaskKind          string                `json:"task_kind,omitempty"`
	ID                string                `json:"job_id"`
	Status            string                `json:"status"`
	Phase             string                `json:"phase"`
	Title             string                `json:"title"`
	URL               string                `json:"url"`
	Mode              string                `json:"mode"`
	LibraryID         string                `json:"library_id,omitempty"`
	Progress          float64               `json:"progress"`
	Attempt           int                   `json:"attempt"`
	CancelRequested   bool                  `json:"cancel_requested"`
	ErrorCode         string                `json:"error_code,omitempty"`
	Message           string                `json:"message,omitempty"`
	CreatedAt         string                `json:"created_at"`
	UpdatedAt         string                `json:"updated_at"`
	Files             []acquisitionFile     `json:"files"`
	Input             acquisitionStartInput `json:"-"`
	LeaseOwner        string                `json:"-"`
	LeaseUntil        int64                 `json:"-"`
	AccountGeneration string                `json:"account_generation,omitempty"`
}

func ensureAcquisitionSchema(db *sql.DB) error {
	var applied int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE name='acquisition_v1'`).Scan(&applied); err != nil {
		return err
	}
	if applied > 0 {
		if err := ensureAcquisitionSessionSchema(db); err != nil {
			return err
		}
		return ensureAcquisitionAttachmentSchema(db)
	}
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS remote_resources (
 id TEXT PRIMARY KEY, provider TEXT NOT NULL, provider_id TEXT NOT NULL,
 source_url TEXT NOT NULL, metadata_json TEXT NOT NULL DEFAULT '{}', updated_at TEXT NOT NULL,
 UNIQUE(provider,provider_id)
);
CREATE TABLE IF NOT EXISTS remote_bookmarks (
 remote_id TEXT PRIMARY KEY REFERENCES remote_resources(id), note TEXT NOT NULL DEFAULT '',
 tags_json TEXT NOT NULL DEFAULT '[]', created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS acquisition_jobs (
 id TEXT PRIMARY KEY, idempotency_key TEXT NOT NULL UNIQUE, intent_hash TEXT NOT NULL,
 input_json TEXT NOT NULL, status TEXT NOT NULL, phase TEXT NOT NULL DEFAULT 'queued',
 attempt INTEGER NOT NULL DEFAULT 0, progress REAL NOT NULL DEFAULT 0,
 cancel_requested INTEGER NOT NULL DEFAULT 0, lease_owner TEXT NOT NULL DEFAULT '',
 lease_until INTEGER NOT NULL DEFAULT 0, error_code TEXT NOT NULL DEFAULT '',
 message TEXT NOT NULL DEFAULT '', files_json TEXT NOT NULL DEFAULT '[]',
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_acquisition_recent ON acquisition_jobs(updated_at DESC,id);
CREATE TABLE IF NOT EXISTS asset_origins (
 library_item_id TEXT NOT NULL, provider TEXT NOT NULL, provider_id TEXT NOT NULL,
 source_url TEXT NOT NULL, acquisition_id TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL,
 PRIMARY KEY(library_item_id,provider,provider_id)
);
CREATE TABLE IF NOT EXISTS acquisition_outbox (
 event_id TEXT PRIMARY KEY, library_id TEXT NOT NULL, payload_json TEXT NOT NULL,
 delivered INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL
);
INSERT OR IGNORE INTO schema_migrations(name,applied_at) VALUES('acquisition_v1',strftime('%Y-%m-%dT%H:%M:%SZ','now'));
`)
	if err != nil {
		return err
	}
	if err = ensureAcquisitionSessionSchema(db); err != nil {
		return err
	}
	return ensureAcquisitionAttachmentSchema(db)
}

func ensureAcquisitionAttachmentSchema(db *sql.DB) error {
	var applied int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE name='acquisition_attachments_v1'`).Scan(&applied); err != nil {
		return err
	}
	if applied > 0 {
		return nil
	}
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS asset_attachments(
 library_item_id TEXT NOT NULL REFERENCES library_items(id),sha256 TEXT NOT NULL,local_path TEXT NOT NULL,relative_path TEXT NOT NULL,
 role TEXT NOT NULL,language TEXT NOT NULL DEFAULT '',original_name TEXT NOT NULL,bytes INTEGER NOT NULL,
 source_url TEXT NOT NULL,acquisition_id TEXT NOT NULL,created_at TEXT NOT NULL,PRIMARY KEY(library_item_id,sha256));
 INSERT OR IGNORE INTO schema_migrations(name,applied_at) VALUES('acquisition_attachments_v1',strftime('%Y-%m-%dT%H:%M:%SZ','now'));`)
	return err
}

func ensureAcquisitionSessionSchema(db *sql.DB) error {
	var applied int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE name='acquisition_session_v1'`).Scan(&applied); err != nil {
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
	var exists int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('acquisition_jobs') WHERE name='account_generation'`).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		if _, err = tx.Exec(`ALTER TABLE acquisition_jobs ADD COLUMN account_generation TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`INSERT OR IGNORE INTO schema_migrations(name,applied_at) VALUES('acquisition_session_v1',?)`, nowRFC3339()); err != nil {
		return err
	}
	return tx.Commit()
}

// Stable source references deliberately exclude tracking and signed media URLs.
func canonicalRemoteReference(raw string) (provider, id, canonical string, err error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") || len(raw) > 2000 {
		return "", "", "", fmt.Errorf("请使用完整的媒体网页链接")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	u.Fragment = ""
	q := u.Query()
	for key := range q {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "utm_") || lower == "fbclid" {
			q.Del(key)
		}
		if lower == "token" || lower == "access_token" || lower == "api_key" || lower == "signature" || lower == "x-amz-signature" || lower == "x-goog-signature" {
			return "", "", "", fmt.Errorf("请使用作品网页链接，不能保存临时鉴权地址")
		}
	}
	switch {
	case host == "douyin.com" || strings.HasSuffix(host, ".douyin.com"):
		provider = "douyin"
		id = q.Get("modal_id")
		if id == "" && strings.HasPrefix(u.Path, "/video/") {
			id = strings.Trim(strings.TrimPrefix(u.Path, "/video/"), "/")
		}
		if id != "" {
			for _, c := range id {
				if c < '0' || c > '9' {
					return "", "", "", fmt.Errorf("抖音作品编号无效")
				}
			}
			return provider, id, "https://www.douyin.com/video/" + id, nil
		}
	case host == "youtube.com" || strings.HasSuffix(host, ".youtube.com") || host == "youtu.be":
		provider = "youtube"
		id = q.Get("v")
		if host == "youtu.be" {
			id = strings.Trim(u.Path, "/")
		}
		if strings.HasPrefix(u.Path, "/shorts/") {
			id = strings.TrimPrefix(u.Path, "/shorts/")
		}
		if id != "" && q.Get("list") == "" {
			return provider, id, "https://www.youtube.com/watch?v=" + url.QueryEscape(id), nil
		}
	case host == "bilibili.com" || strings.HasSuffix(host, ".bilibili.com"):
		provider = "bilibili"
		if strings.HasPrefix(u.Path, "/video/") {
			id = strings.Trim(strings.TrimPrefix(u.Path, "/video/"), "/")
			u.RawQuery = ""
			if page := q.Get("p"); page != "" {
				u.RawQuery = "p=" + url.QueryEscape(page)
				id += ":p:" + page
			}
			return provider, id, u.String(), nil
		}
	}
	if provider == "" {
		provider = host
	}
	u.RawQuery = q.Encode()
	canonical = u.String()
	return provider, stableID("url", canonical), canonical, nil
}

func createAcquisition(db *sql.DB, in acquisitionStartInput) (acquisitionRecord, bool, error) {
	if len(in.IdempotencyKey) < 8 || len(in.IdempotencyKey) > 128 {
		return acquisitionRecord{}, false, fmt.Errorf("采集请求标识无效，请重试")
	}
	if in.Audio == nil {
		_, _, canonical, err := canonicalRemoteReference(in.URL)
		if err != nil {
			return acquisitionRecord{}, false, err
		}
		in.URL = canonical
	} else if err := validateAudioRange(in.Audio.StartMS, in.Audio.EndMS, in.Audio.Format); err != nil {
		return acquisitionRecord{}, false, err
	}
	if in.Purpose != "" && (in.Purpose != "bgm" || (!in.AudioOnly && in.Audio == nil)) {
		return acquisitionRecord{}, false, fmt.Errorf("BGM用途仅适用于音频采集")
	}
	if err := validateMusic(in.Music); err != nil {
		return acquisitionRecord{}, false, err
	}
	// A thumbnail returned by an extractor may be a signed CDN URL. It is a
	// transient display hint, never part of the durable acquisition intent.
	in.Thumbnail = ""
	if in.Mode == "" {
		in.Mode = "save"
	}
	if in.Mode != "save" && in.Mode != "import" {
		return acquisitionRecord{}, false, fmt.Errorf("保存模式必须是仅保存文件或下载并入库")
	}
	if in.Overwrite {
		return acquisitionRecord{}, false, fmt.Errorf("采集不会覆盖现有文件，请选择其他目录")
	}
	if in.Mode == "import" {
		library, err := getLibraryWithDB(db, in.LibraryID)
		if err != nil {
			return acquisitionRecord{}, false, err
		}
		if library.AccessMode == "read_only" || library.AccessMode == "readonly" {
			return acquisitionRecord{}, false, fmt.Errorf("目标资源库只读")
		}
		if library.BackendType == "smb" {
			return acquisitionRecord{}, false, fmt.Errorf("平台作品未明确允许再分发，请先保存到本机资源库")
		}
	} else {
		in.LibraryID = ""
	}
	payload, err := json.Marshal(in)
	if err != nil {
		return acquisitionRecord{}, false, err
	}
	digest := sha256.Sum256(payload)
	intentHash := hex.EncodeToString(digest[:])
	id, now := randomID("acq"), nowRFC3339()
	result, err := db.Exec(`INSERT INTO acquisition_jobs(id,idempotency_key,intent_hash,input_json,status,created_at,updated_at,account_generation)
 VALUES(?,?,?,?,'queued',?,?,?) ON CONFLICT(idempotency_key) DO NOTHING`, id, in.IdempotencyKey, intentHash, string(payload), now, now, in.AccountGeneration)
	if err != nil {
		return acquisitionRecord{}, false, err
	}
	count, _ := result.RowsAffected()
	var storedHash string
	if err := db.QueryRow(`SELECT id,intent_hash FROM acquisition_jobs WHERE idempotency_key=?`, in.IdempotencyKey).Scan(&id, &storedHash); err != nil {
		return acquisitionRecord{}, false, err
	}
	if storedHash != intentHash {
		return acquisitionRecord{}, false, fmt.Errorf("同一采集请求不能更改目标或格式，请重新发起")
	}
	record, err := loadAcquisition(db, id)
	return record, count > 0, err
}

func loadAcquisition(db *sql.DB, id string) (acquisitionRecord, error) {
	var record acquisitionRecord
	var inputJSON, filesJSON string
	err := db.QueryRow(`SELECT id,status,phase,attempt,progress,cancel_requested,lease_owner,lease_until,error_code,message,input_json,files_json,created_at,updated_at,account_generation
 FROM acquisition_jobs WHERE id=?`, id).Scan(&record.ID, &record.Status, &record.Phase, &record.Attempt, &record.Progress, &record.CancelRequested, &record.LeaseOwner, &record.LeaseUntil, &record.ErrorCode, &record.Message, &inputJSON, &filesJSON, &record.CreatedAt, &record.UpdatedAt, &record.AccountGeneration)
	if err == sql.ErrNoRows {
		return record, fmt.Errorf("采集任务不存在")
	}
	if err != nil {
		return record, err
	}
	if err := json.Unmarshal([]byte(inputJSON), &record.Input); err != nil {
		return record, fmt.Errorf("采集任务参数无法恢复")
	}
	if err := json.Unmarshal([]byte(filesJSON), &record.Files); err != nil {
		return record, fmt.Errorf("采集文件记录无法恢复")
	}
	if record.Files == nil {
		record.Files = []acquisitionFile{}
	}
	record.Title, record.URL, record.Mode, record.LibraryID = record.Input.Title, record.Input.URL, record.Input.Mode, record.Input.LibraryID
	record.Purpose = record.Input.Purpose
	if record.Input.Audio != nil {
		record.TaskKind = "audio"
	}
	return record, nil
}

func claimAcquisition(db *sql.DB, id, owner string, now time.Time) (bool, error) {
	if owner == "" {
		return false, fmt.Errorf("采集执行标识为空")
	}
	result, err := db.Exec(`UPDATE acquisition_jobs SET status='resolving',lease_owner=?,lease_until=?,attempt=attempt+1,updated_at=?
 WHERE id=? AND cancel_requested=0 AND lease_until<=? AND status IN ('queued','interrupted','resolving','downloading','verifying','importing')`, owner, now.Add(30*time.Second).Unix(), now.UTC().Format(time.RFC3339), id, now.Unix())
	if err != nil {
		return false, err
	}
	count, _ := result.RowsAffected()
	return count == 1, nil
}

func heartbeatAcquisition(db *sql.DB, id, owner string, now time.Time) (bool, error) {
	result, err := db.Exec(`UPDATE acquisition_jobs SET lease_until=?,updated_at=? WHERE id=? AND lease_owner=? AND cancel_requested=0 AND lease_until>? AND status IN ('resolving','downloading','verifying','importing')`, now.Add(30*time.Second).Unix(), now.UTC().Format(time.RFC3339), id, owner, now.Unix())
	if err != nil {
		return false, err
	}
	count, _ := result.RowsAffected()
	return count == 1, nil
}

func cancelAcquisition(db *sql.DB, id string) (acquisitionRecord, error) {
	_, err := db.Exec(`UPDATE acquisition_jobs SET cancel_requested=1,status=CASE WHEN lease_until>? THEN 'canceling' ELSE 'cancelled' END,updated_at=?
 WHERE id=? AND status NOT IN ('completed','cancelled')`, time.Now().Unix(), nowRFC3339(), id)
	if err != nil {
		return acquisitionRecord{}, err
	}
	return loadAcquisition(db, id)
}

func retryAcquisition(db *sql.DB, id string) (acquisitionRecord, error) {
	return retryAcquisitionAccount(db, id, "")
}

func retryAcquisitionAccount(db *sql.DB, id, generation string) (acquisitionRecord, error) {
	result, err := db.Exec(`UPDATE acquisition_jobs SET status='queued',cancel_requested=0,lease_owner='',lease_until=0,error_code='',message='',updated_at=?,account_generation=CASE WHEN ?!='' THEN ? ELSE account_generation END
 WHERE id=? AND lease_until<=? AND status IN ('failed','interrupted','cancelled','pending_import','waiting_for_login','waiting_for_target')`, nowRFC3339(), generation, generation, id, time.Now().Unix())
	if err != nil {
		return acquisitionRecord{}, err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return acquisitionRecord{}, fmt.Errorf("任务正在进行或已完成，不能重试")
	}
	return loadAcquisition(db, id)
}

func listAcquisitions(db *sql.DB, limit int) (any, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	// Recover cancellation whose owning worker has disappeared; never touch files.
	if err := recoverAcquisitions(db, time.Now()); err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT id FROM acquisition_jobs ORDER BY updated_at DESC,id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	readErr := rows.Err()
	rows.Close()
	if readErr != nil {
		return nil, readErr
	}
	items := []acquisitionRecord{}
	for _, id := range ids {
		record, err := loadAcquisition(db, id)
		if err != nil {
			return nil, err
		}
		items = append(items, record)
	}
	var active, total int
	if err := db.QueryRow(`SELECT COUNT(*),COALESCE(SUM(CASE WHEN status IN ('queued','resolving','downloading','verifying','importing','canceling') THEN 1 ELSE 0 END),0) FROM acquisition_jobs`).Scan(&total, &active); err != nil {
		return nil, err
	}
	return map[string]any{"items": items, "active": active, "total": total}, nil
}

func recoverAcquisitions(db *sql.DB, now time.Time) error {
	if _, err := db.Exec(`UPDATE acquisition_jobs SET status='cancelled',lease_owner='',lease_until=0 WHERE status='canceling' AND lease_until<=?`, now.Unix()); err != nil {
		return err
	}
	_, err := db.Exec(`UPDATE acquisition_jobs SET status='interrupted',lease_owner='',lease_until=0,error_code='INTERRUPTED',message='采集已中断，可以继续'
 WHERE (status IN ('resolving','downloading','verifying','importing') AND lease_until<=?)
 OR (status='queued' AND lease_until<=? AND CAST(strftime('%s',updated_at) AS INTEGER)<=?)`, now.Unix(), now.Unix(), now.Add(-time.Minute).Unix())
	return err
}
