package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var acquisitionDigestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func validateAcquisitionDigest(file acquisitionFile) error {
	if !acquisitionDigestPattern.MatchString(file.SHA256) || file.Bytes <= 0 {
		return fmt.Errorf("采集文件校验记录无效")
	}
	return nil
}

func acquisitionStillOwned(db *sql.DB, id, owner string) error {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM acquisition_jobs WHERE id=? AND lease_owner=? AND lease_until>? AND cancel_requested=0`, id, owner, time.Now().Unix()).Scan(&count)
	if err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("采集已取消或执行租约已失效")
	}
	return nil
}

// Copy into the final directory first, then publish without replacing any file.
// The private staging copy stays available until the entire task is committed.
func commitAcquisitionCopy(ctx context.Context, source, target, digest string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if existing, err := hashFile(target); err == nil {
		if existing == digest {
			return nil
		}
		return fmt.Errorf("目标已有不同内容的文件，请更换目录")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	temporary, err := os.CreateTemp(filepath.Dir(target), ".acquisition-*.tmp")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	_, copyErr := io.Copy(temporary, &contextReader{ctx: ctx, reader: input})
	syncErr := temporary.Sync()
	closeErr := temporary.Close()
	if copyErr != nil {
		return copyErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	actual, err := hashFile(name)
	if err != nil {
		return err
	}
	if actual != digest {
		return fmt.Errorf("目标文件校验失败")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err = publishAcquisitionFile(name, target); err != nil {
		if existing, readErr := hashFile(target); readErr == nil && existing == digest {
			return nil
		}
		return err
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

func saveAcquisitionFile(ctx context.Context, db *sql.DB, record acquisitionRecord, owner string, file acquisitionFile) (string, error) {
	if err := validateAcquisitionDigest(file); err != nil {
		return "", err
	}
	if err := acquisitionStillOwned(db, record.ID, owner); err != nil {
		return "", err
	}
	destination, err := filepath.Abs(record.Input.DestDir)
	if err != nil {
		return "", err
	}
	filename := safeFilename(filepath.Base(file.Path))
	if filename == "" {
		return "", fmt.Errorf("采集文件名无效")
	}
	target := filepath.Join(destination, filename)
	if existing, err := hashFile(target); err == nil && existing != file.SHA256 {
		ext := filepath.Ext(filename)
		target = filepath.Join(destination, strings.TrimSuffix(filename, ext)+"-"+file.SHA256[:12]+ext)
	}
	if err = commitAcquisitionCopy(ctx, file.Path, target, file.SHA256); err != nil {
		return "", err
	}
	if err = acquisitionStillOwned(db, record.ID, owner); err != nil {
		return "", err
	}
	return target, nil
}

func importAcquisitionFile(ctx context.Context, db *sql.DB, record acquisitionRecord, owner string, file acquisitionFile) (string, string, error) {
	if err := validateAcquisitionDigest(file); err != nil {
		return "", "", err
	}
	if err := acquisitionStillOwned(db, record.ID, owner); err != nil {
		return "", "", err
	}
	library, err := getLibraryWithDB(db, record.LibraryID)
	if err != nil {
		return "", "", err
	}
	if library.State != "online" || library.BackendType == "smb" || library.AccessMode == "read_only" || library.AccessMode == "readonly" {
		return "", "", fmt.Errorf("目标资源库当前不可写")
	}
	ext := strings.ToLower(filepath.Ext(file.Path))
	kind := mediaExtensions[ext]
	isSubtitle := isAcquisitionSubtitle(file.Path)
	if kind == "" && !isSubtitle {
		return "", "", fmt.Errorf("不支持入库的采集文件类型")
	}
	var existingID, existingPath string
	if kind != "" {
		err = db.QueryRow(`SELECT id,local_path FROM library_items WHERE library_id=? AND sha256=? AND deleted_at=''`, library.ID, file.SHA256).Scan(&existingID, &existingPath)
		if err != nil && err != sql.ErrNoRows {
			return "", "", err
		}
	}
	relative := filepath.ToSlash(filepath.Join("assets", file.SHA256[:2], file.SHA256+ext))
	target := filepath.Join(library.RootURI, filepath.FromSlash(relative))
	if existingID != "" {
		if actual, err := hashFile(existingPath); err == nil && actual == file.SHA256 {
			target = existingPath
		} else if isPathInside(library.RootURI, existingPath) {
			target = existingPath
		} else {
			return "", "", fmt.Errorf("已有素材的引用文件缺失，请先恢复原文件")
		}
	}
	if err = commitAcquisitionCopy(ctx, file.Path, target, file.SHA256); err != nil {
		return "", "", err
	}
	if isSubtitle {
		if err = commitAcquisitionAttachment(ctx, db, record, owner, file, target, relative); err != nil {
			return "", "", err
		}
		return "", target, nil
	}
	if relativePath, err := filepath.Rel(library.RootURI, target); err == nil {
		relative = filepath.ToSlash(relativePath)
	}
	provider, providerID, sourceURL, err := acquisitionSource(record)
	if err != nil {
		return "", "", err
	}
	id := existingID
	if id == "" {
		id = stableID("asset", library.ID+":"+file.SHA256)
	}
	title := firstNonEmpty(record.Title, filepath.Base(file.Path))
	now := nowRFC3339()
	event := catalogEvent{EventID: stableID("event", record.ID+":"+file.SHA256), Type: "asset.adopt", LibraryID: library.ID, AssetID: id, RelativePath: relative, Title: title, Kind: kind, SHA256: file.SHA256, SourceProvider: provider, SourceURL: sourceURL, Actor: "acquisition", CreatedAt: now, Deduplicated: existingID != ""}
	eventJSON, _ := json.Marshal(event)
	tx, err := db.Begin()
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	// Take the SQLite writer lock and fence the owner before committing assets.
	result, err := tx.Exec(`UPDATE acquisition_jobs SET updated_at=? WHERE id=? AND lease_owner=? AND lease_until>? AND cancel_requested=0`, now, record.ID, owner, time.Now().Unix())
	if err != nil {
		return "", "", err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return "", "", fmt.Errorf("采集已取消或执行租约已失效")
	}
	// Another worker may have committed the same content while this worker was
	// copying. Recheck after taking the writer lock, then attach this origin to
	// the winning item instead of failing the entire acquisition.
	var winnerID, winnerPath, deletedAt string
	err = tx.QueryRow(`SELECT id,local_path,deleted_at FROM library_items WHERE library_id=? AND sha256=?`, library.ID, file.SHA256).Scan(&winnerID, &winnerPath, &deletedAt)
	if err != nil && err != sql.ErrNoRows {
		return "", "", err
	}
	if deletedAt != "" {
		return "", "", fmt.Errorf("相同素材在回收站中，请先恢复后继续入库")
	}
	if winnerID != "" {
		if actual, readErr := hashFile(winnerPath); readErr != nil || actual != file.SHA256 {
			return "", "", fmt.Errorf("已有素材文件不可用，请恢复文件后重试")
		}
		id, target, existingID = winnerID, winnerPath, winnerID
		if rel, relErr := filepath.Rel(library.RootURI, target); relErr == nil {
			relative = filepath.ToSlash(rel)
		}
		event.AssetID, event.RelativePath, event.Deduplicated = id, relative, true
		eventJSON, _ = json.Marshal(event)
	}
	if existingID == "" {
		_, err = tx.Exec(`INSERT INTO library_items(id,library_id,sha256,relative_path,local_path,kind,title,mime,bytes,source_provider,source_id,source_url,adopted_by,adopted_at,updated_at,metadata_json)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,'{}')`, id, library.ID, file.SHA256, relative, target, kind, title, mime.TypeByExtension(ext), file.Bytes, provider, providerID, sourceURL, "acquisition", now, now)
		if err != nil {
			return "", "", err
		}
		if err = replaceSearchDocument(tx, searchDocument{EntryType: "library", EntryID: id, LibraryID: library.ID, Kind: kind, Title: title, Filename: filepath.Base(target), Path: relative, UpdatedAt: now}); err != nil {
			return "", "", err
		}
	}
	if _, err = tx.Exec(`INSERT OR IGNORE INTO asset_origins(library_item_id,provider,provider_id,source_url,acquisition_id,created_at) VALUES(?,?,?,?,?,?)`, id, provider, providerID, sourceURL, record.ID, now); err != nil {
		return "", "", err
	}
	if err = audioMetadata(tx, record, id, kind, file.DurationMS); err != nil {
		return "", "", err
	}
	if record.Input.Audio != nil {
		parent := record.Input.Audio.SourceAssetID
		if _, err = tx.Exec(`INSERT OR IGNORE INTO asset_origins(library_item_id,provider,provider_id,source_url,acquisition_id,created_at) SELECT ?,provider,provider_id,source_url,?,? FROM asset_origins WHERE library_item_id=?`, id, record.ID, now, parent); err != nil {
			return "", "", err
		}
		if _, err = tx.Exec(`UPDATE library_items SET author=COALESCE((SELECT author FROM library_items WHERE id=?),author),license_name=COALESCE((SELECT license_name FROM library_items WHERE id=?),license_name),license_url=COALESCE((SELECT license_url FROM library_items WHERE id=?),license_url) WHERE id=?`, parent, parent, parent, id); err != nil {
			return "", "", err
		}
	}
	if _, err = tx.Exec(`INSERT OR IGNORE INTO acquisition_outbox(event_id,library_id,payload_json,created_at) VALUES(?,?,?,?)`, event.EventID, library.ID, string(eventJSON), now); err != nil {
		return "", "", err
	}
	if ctx.Err() != nil {
		return "", "", ctx.Err()
	}
	var stillOwned int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM acquisition_jobs WHERE id=? AND lease_owner=? AND lease_until>? AND cancel_requested=0`, record.ID, owner, time.Now().Unix()).Scan(&stillOwned); err != nil {
		return "", "", err
	}
	if stillOwned != 1 {
		return "", "", fmt.Errorf("采集已取消或执行租约已失效")
	}
	if err = tx.Commit(); err != nil {
		return "", "", err
	}
	return id, target, nil
}

func flushAcquisitionOutbox(db *sql.DB) error {
	rows, err := db.Query(`SELECT event_id,library_id,payload_json FROM acquisition_outbox WHERE delivered=0 ORDER BY created_at`)
	if err != nil {
		return err
	}
	type pending struct{ id, library, payload string }
	var events []pending
	for rows.Next() {
		var event pending
		if err = rows.Scan(&event.id, &event.library, &event.payload); err != nil {
			rows.Close()
			return err
		}
		events = append(events, event)
	}
	readErr := rows.Err()
	rows.Close()
	if readErr != nil {
		return readErr
	}
	for _, event := range events {
		library, err := getLibraryWithDB(db, event.library)
		if err != nil {
			return err
		}
		var decoded catalogEvent
		if err = json.Unmarshal([]byte(event.payload), &decoded); err != nil {
			return err
		}
		// Acquisitions export separate immutable events. Existing user JSONL stays
		// untouched; both formats use the same catalogEvent payload and event_id.
		directory := filepath.Join(library.RootURI, ".himind-media", "catalog", "events")
		path := filepath.Join(directory, event.id+".json")
		if err = publishAcquisitionEvent(path, event.payload); err != nil {
			return err
		}
		if _, err = db.Exec(`UPDATE acquisition_outbox SET delivered=1 WHERE event_id=?`, event.id); err != nil {
			return err
		}
	}
	return nil
}

func publishAcquisitionEvent(path, payload string) error {
	match := func() bool {
		data, err := os.ReadFile(path)
		return err == nil && strings.TrimSpace(string(data)) == payload
	}
	if _, err := os.Lstat(path); err == nil {
		if match() {
			return nil
		}
		return fmt.Errorf("来源记录冲突，需核对后继续")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".catalog-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := io.WriteString(file, payload+"\n")
	syncErr, closeErr := file.Sync(), file.Close()
	if writeErr != nil {
		return writeErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err = publishAcquisitionFile(file.Name(), path); err != nil {
		if match() {
			return nil
		}
		return err
	}
	return nil
}
