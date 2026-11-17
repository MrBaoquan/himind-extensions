package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/MrBaoquan/himind-extensions/internal/mediadownload"
)

var acquisitionIDPattern = regexp.MustCompile(`^acq-[a-f0-9]{32}$`)

func startAcquisition(in acquisitionStartInput) (any, error) {
	provider, _, _, err := canonicalRemoteReference(in.URL)
	if err != nil {
		return nil, err
	}
	if err := mediadownload.ValidateURL(in.URL); err != nil {
		return nil, err
	}
	if err := configureDownloadKernel(); err != nil {
		return nil, err
	}
	if in.Playlist {
		return nil, fmt.Errorf("播放列表请暂用媒体下载工具；媒体库当前采集单个作品")
	}
	if provider == "douyin" {
		status, err := mediadownload.ConnectionStatus()
		if err != nil {
			return nil, err
		}
		if status.State != "connected" || status.Generation == "" {
			return nil, fmt.Errorf("请先在设置中连接抖音")
		}
		in.AccountGeneration = status.Generation
	}
	destination, err := mediadownload.Destination(in.WorkspaceRoot, in.DestDir)
	if err != nil {
		return nil, err
	}
	in.DestDir = destination
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	// Only metadata resolved by the platform adapter can become music identity.
	in.Music = nil
	if provider == "douyin" && in.AudioOnly {
		in.Music, err = musicForVideo(db, in.URL)
		if err != nil {
			return nil, err
		}
	}
	record, created, err := createAcquisition(db, in)
	if err != nil {
		return nil, err
	}
	if created {
		root, err := pluginDataRoot()
		if err != nil {
			return nil, err
		}
		if err = mediadownload.StartLibraryWorker(record.ID, root); err != nil {
			_, _ = db.Exec(`UPDATE acquisition_jobs SET status='failed',error_code='WORKER_START_FAILED',message=?,updated_at=? WHERE id=? AND status='queued'`, err.Error(), nowRFC3339(), record.ID)
			return nil, err
		}
	}
	return record, nil
}

func acquisitionOperation(action, id string, limit int, useCurrentAccount bool) (any, error) {
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if action == "list" {
		return listAcquisitions(db, limit)
	}
	if !acquisitionIDPattern.MatchString(id) {
		return nil, fmt.Errorf("采集任务编号无效")
	}
	if err = recoverAcquisitions(db, time.Now()); err != nil {
		return nil, err
	}
	switch action {
	case "status":
		return loadAcquisition(db, id)
	case "cancel":
		signalAudioCancel(db, id)
		return cancelAcquisition(db, id)
	case "retry":
		current, err := loadAcquisition(db, id)
		if err != nil {
			return nil, err
		}
		if current.Status == "completed" && current.ErrorCode == "CATALOG_PENDING" {
			if err = flushAcquisitionOutbox(db); err != nil {
				return nil, fmt.Errorf("目录记录仍未同步，请恢复资源库后重试")
			}
			_, err = db.Exec(`UPDATE acquisition_jobs SET error_code='',message='' WHERE id=? AND status='completed'`, id)
			if err != nil {
				return nil, err
			}
			return loadAcquisition(db, id)
		}
		generation := ""
		if useCurrentAccount {
			provider, _, _, err := canonicalRemoteReference(current.URL)
			if err != nil {
				return nil, err
			}
			if provider != "douyin" {
				return nil, fmt.Errorf("当前平台不需要重新绑定账号")
			}
			if err = configureDownloadKernel(); err != nil {
				return nil, err
			}
			status, err := mediadownload.ConnectionStatus()
			if err != nil {
				return nil, err
			}
			if status.State != "connected" || status.Generation == "" {
				return nil, fmt.Errorf("请先连接抖音")
			}
			generation = status.Generation
		}
		record, err := retryAcquisitionAccount(db, id, generation)
		if err != nil {
			return nil, err
		}
		root, err := pluginDataRoot()
		if err != nil {
			return nil, err
		}
		if record.Input.Audio != nil && len(record.Files) == 0 {
			if err = submitAudioRender(db, record, filepath.Join(root, "acquisition-staging", id)); err != nil {
				_, _ = db.Exec(`UPDATE acquisition_jobs SET status='failed',error_code='AUDIO_START_FAILED',message='音频处理未启动，请稍后重试' WHERE id=? AND lease_until=0`, id)
				return nil, err
			}
		}
		if err = mediadownload.StartLibraryWorker(id, root); err != nil {
			_, _ = db.Exec(`UPDATE acquisition_jobs SET status='failed',error_code='WORKER_START_FAILED',message=? WHERE id=? AND status='queued'`, err.Error(), id)
			return nil, err
		}
		return record, nil
	case "remove":
		result, err := db.Exec(`DELETE FROM acquisition_jobs WHERE id=? AND lease_until<=? AND status IN ('completed','failed','cancelled','interrupted','pending_import','waiting_for_target','waiting_for_login')`, id, time.Now().Unix())
		if err != nil {
			return nil, err
		}
		count, _ := result.RowsAffected()
		if count != 1 {
			return nil, fmt.Errorf("请先结束采集任务，再移除记录")
		}
		return map[string]any{"removed": true, "files_preserved": true}, nil
	case "log":
		record, err := loadAcquisition(db, id)
		if err != nil {
			return nil, err
		}
		return map[string]any{"job_id": id, "phase": record.Phase, "status": record.Status, "attempt": record.Attempt, "error_code": record.ErrorCode, "message": record.Message}, nil
	}
	return nil, fmt.Errorf("不支持的采集操作")
}

func setAcquisitionPhase(db *sql.DB, id, owner, status string, progress float64, files []acquisitionFile) error {
	fileJSON, err := json.Marshal(files)
	if err != nil {
		return err
	}
	result, err := db.Exec(`UPDATE acquisition_jobs SET status=?,phase=?,progress=?,files_json=?,updated_at=?
 WHERE id=? AND lease_owner=? AND lease_until>? AND cancel_requested=0`, status, status, progress, string(fileJSON), nowRFC3339(), id, owner, time.Now().Unix())
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return fmt.Errorf("采集已取消或执行租约已失效")
	}
	return nil
}

func stopAcquisition(db *sql.DB, id, owner, status, code, message string) {
	_, _ = db.Exec(`UPDATE acquisition_jobs SET status=CASE WHEN cancel_requested=1 THEN 'cancelled' ELSE ? END,error_code=?,message=?,lease_owner='',lease_until=0,updated_at=? WHERE id=? AND lease_owner=? AND lease_until>? AND status!='completed'`, status, code, message, nowRFC3339(), id, owner, time.Now().Unix())
}

func runAcquisitionWorker(id string) error {
	if !acquisitionIDPattern.MatchString(id) {
		return fmt.Errorf("采集任务编号无效")
	}
	if err := configureDownloadKernel(); err != nil {
		return err
	}
	db, err := openLibraryDB()
	if err != nil {
		return err
	}
	defer db.Close()
	owner := randomID("worker")
	claimed, err := claimAcquisition(db, id, owner, time.Now())
	if err != nil {
		return err
	}
	if !claimed {
		return nil
	}
	defer stopAcquisition(db, id, owner, "interrupted", "INTERRUPTED", "采集已中断，可以继续")
	record, err := loadAcquisition(db, id)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	heartbeatDone := make(chan struct{})
	defer func() { cancel(); <-heartbeatDone }()
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ok, err := heartbeatAcquisition(db, id, owner, time.Now())
				if err != nil || !ok {
					cancel()
					return
				}
			}
		}
	}()
	files := record.Files
	root, err := pluginDataRoot()
	if err != nil {
		stopAcquisition(db, id, owner, "failed", "DATA_ROOT_UNAVAILABLE", "采集数据目录不可用")
		return err
	}
	staging := filepath.Join(root, "acquisition-staging", id)
	if err = os.MkdirAll(staging, 0700); err != nil {
		stopAcquisition(db, id, owner, "failed", "STAGING_UNAVAILABLE", "采集暂存目录不可写")
		return err
	}
	if staging, err = filepath.EvalSymlinks(staging); err != nil {
		return err
	}
	if len(files) == 0 && record.Input.Audio != nil {
		if err = setAcquisitionPhase(db, id, owner, "verifying", 0, files); err != nil {
			return err
		}
		files, err = waitAudioRender(ctx, db, record, owner, staging)
		if err != nil {
			stopAcquisition(db, id, owner, "failed", "AUDIO_PROCESS_FAILED", err.Error())
			return err
		}
		if err = setAcquisitionPhase(db, id, owner, "verifying", 100, files); err != nil {
			return err
		}
	}
	if len(files) == 0 {
		provider, _, _, err := canonicalRemoteReference(record.URL)
		if err != nil {
			return err
		}
		if provider == "douyin" && record.AccountGeneration == "" {
			stopAcquisition(db, id, owner, "waiting_for_login", "SESSION_CHANGED", "请确认使用当前抖音账号后继续")
			return fmt.Errorf("采集任务缺少原账号绑定")
		}
		if err = setAcquisitionPhase(db, id, owner, "resolving", 0, files); err != nil {
			stopAcquisition(db, id, owner, "interrupted", "LEASE_LOST", err.Error())
			return err
		}
		if err = setAcquisitionPhase(db, id, owner, "downloading", 0, files); err != nil {
			return err
		}
		last := time.Time{}
		paths, downloadErr := mediadownload.ExecuteTransfer(ctx, record.Input.JobStartInput, staging, record.AccountGeneration, func(progress mediadownload.TransferProgress) {
			if time.Since(last) < time.Second {
				return
			}
			last = time.Now()
			percent := progress.Percent
			if math.IsNaN(percent) || math.IsInf(percent, 0) || percent < 0 || percent > 100 {
				percent = 0
			}
			_, _ = db.Exec(`UPDATE acquisition_jobs SET progress=?,updated_at=? WHERE id=? AND lease_owner=? AND cancel_requested=0 AND lease_until>?`, percent, nowRFC3339(), id, owner, time.Now().Unix())
		})
		if downloadErr != nil {
			status := "failed"
			code, message := mediadownload.DownloadFailure(downloadErr)
			if strings.Contains(downloadErr.Error(), "site_connection_") {
				status, code, message = "waiting_for_login", "AUTH_REQUIRED", "请连接平台账号后继续"
			}
			if strings.Contains(downloadErr.Error(), "site_connection_changed") {
				status, code, message = "waiting_for_login", "SESSION_CHANGED", "平台账号已切换，请确认使用当前账号后重试"
			}
			if strings.Contains(downloadErr.Error(), "未找到 yt-dlp") {
				code, message = "RUNTIME_MISSING", "下载组件未安装，请在设置中安装后重试"
			}
			if ctx.Err() != nil {
				status, code, message = "interrupted", "INTERRUPTED", "采集已中断，可以继续"
			}
			stopAcquisition(db, id, owner, status, code, message)
			return downloadErr
		}
		for _, path := range paths {
			files = append(files, acquisitionFile{Path: path})
		}
		if err = setAcquisitionPhase(db, id, owner, "verifying", 100, files); err != nil {
			stopAcquisition(db, id, owner, "interrupted", "LEASE_LOST", err.Error())
			return err
		}
	}
	for index := range files {
		if ctx.Err() != nil {
			stopAcquisition(db, id, owner, "interrupted", "INTERRUPTED", "采集已中断，可以继续")
			return ctx.Err()
		}
		path := files[index].Path
		real, evalErr := filepath.EvalSymlinks(path)
		if evalErr != nil || !isPathInside(staging, real) {
			// Files already submitted by an earlier attempt can be outside staging.
			if files[index].SHA256 == "" {
				stopAcquisition(db, id, owner, "failed", "OUTPUT_INVALID", "采集输出不可用")
				return fmt.Errorf("采集输出不可用")
			}
		}
		info, statErr := os.Stat(path)
		if statErr != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
			stopAcquisition(db, id, owner, "pending_import", "OUTPUT_UNAVAILABLE", "已下载文件暂不可用，请恢复文件后继续")
			return fmt.Errorf("采集文件不可用")
		}
		digest, hashErr := hashFile(path)
		if hashErr != nil {
			stopAcquisition(db, id, owner, "pending_import", "OUTPUT_UNAVAILABLE", "无法读取已下载文件")
			return hashErr
		}
		if files[index].SHA256 != "" && files[index].SHA256 != digest {
			stopAcquisition(db, id, owner, "failed", "OUTPUT_CHANGED", "已下载文件发生变化，请重新采集")
			return fmt.Errorf("采集文件校验不一致")
		}
		files[index].SHA256, files[index].Bytes = digest, info.Size()
		if files[index].OriginalName == "" {
			files[index].OriginalName = filepath.Base(path)
		}
		if isAcquisitionSubtitle(path) {
			files[index].Role = "subtitle"
		} else {
			files[index].Role = mediaExtensions[strings.ToLower(filepath.Ext(path))]
		}
	}
	sort.SliceStable(files, func(i, j int) bool { return files[i].Role != "subtitle" && files[j].Role == "subtitle" })
	phase := "verifying"
	if record.Mode == "import" {
		phase = "importing"
	}
	if err = setAcquisitionPhase(db, id, owner, phase, 100, files); err != nil {
		stopAcquisition(db, id, owner, "interrupted", "LEASE_LOST", err.Error())
		return err
	}
	stagedPaths := make([]string, len(files))
	for index := range files {
		stagedPaths[index] = files[index].Path
	}
	for index := range files {
		if record.Mode == "import" {
			if files[index].Role == "subtitle" {
				parent, language := acquisitionSubtitleParent(files[index].OriginalName, files)
				if parent == "" {
					stopAcquisition(db, id, owner, "pending_import", "ATTACHMENT_PARENT_MISSING", "字幕已下载，未找到对应素材；请核对采集文件后继续")
					return fmt.Errorf("字幕对应素材不可用")
				}
				files[index].ParentItemID, files[index].Language = parent, language
			}
			itemID, finalPath, importErr := importAcquisitionFile(ctx, db, record, owner, files[index])
			if importErr != nil {
				stopAcquisition(db, id, owner, "pending_import", "IMPORT_FAILED", "文件已下载，入库未完成；可以仅重试入库")
				return importErr
			}
			files[index].LibraryItemID = itemID
			files[index].Path = finalPath
		} else {
			finalPath, saveErr := saveAcquisitionFile(ctx, db, record, owner, files[index])
			if saveErr != nil {
				stopAcquisition(db, id, owner, "waiting_for_target", "TARGET_UNAVAILABLE", "文件已下载，目标目录不可用；恢复目录后继续")
				return saveErr
			}
			files[index].Path = finalPath
		}
		if err = setAcquisitionPhase(db, id, owner, phase, 100, files); err != nil {
			stopAcquisition(db, id, owner, "interrupted", "LEASE_LOST", err.Error())
			return err
		}
	}
	fileJSON, err := json.Marshal(files)
	if err != nil {
		return err
	}
	result, err := db.Exec(`UPDATE acquisition_jobs SET status='completed',phase='completed',progress=100,files_json=?,error_code='',message='',lease_owner='',lease_until=0,updated_at=? WHERE id=? AND lease_owner=? AND lease_until>? AND cancel_requested=0`, string(fileJSON), nowRFC3339(), id, owner, time.Now().Unix())
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return fmt.Errorf("采集已取消或执行租约已失效")
	}
	// A catalog synchronization failure never causes a second network download.
	if err = flushAcquisitionOutbox(db); err != nil {
		_, _ = db.Exec(`UPDATE acquisition_jobs SET error_code='CATALOG_PENDING',message='素材已入库，目录记录待同步' WHERE id=? AND status='completed'`, id)
	}
	cleanupAcquisitionStaging(staging, stagedPaths)
	return nil
}

// Remove only known files from this completed task. Unknown files and outputs
// outside the task directory are retained; no recursive removal is used.
func cleanupAcquisitionStaging(staging string, paths []string) {
	realRoot, err := filepath.EvalSymlinks(staging)
	if err != nil {
		return
	}
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		real, err := filepath.EvalSymlinks(path)
		if err == nil && isPathInside(realRoot, real) {
			_ = os.Remove(path)
		}
	}
	_ = os.Remove(staging)
}

func isPathInside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
