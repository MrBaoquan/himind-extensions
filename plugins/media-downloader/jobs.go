package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func startJob(ctx context.Context, input JobStartInput) (map[string]any, error) {
	if _, err := validateMediaURL(input.URL); err != nil {
		return nil, err
	}
	workspace, err := resolveWorkspaceRoot(input.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	destination, err := resolveDestination(workspace, input.DestDir)
	if err != nil {
		return nil, err
	}
	location, err := locateYtDlp()
	if err != nil {
		return nil, err
	}
	if err := quickRuntimeProbe(ctx, location.Path); err != nil {
		return nil, fmt.Errorf("yt-dlp 运行时不可用: %w", err)
	}
	ffmpegPath, _ := ffmpegStatus(ctx)
	ffmpegAvailable := ffmpegPath != ""
	if input.AudioOnly && !ffmpegAvailable {
		return nil, errors.New("音频提取需要 ffmpeg，请先安装 ffmpeg")
	}
	jobID, err := newJobID()
	if err != nil {
		return nil, err
	}
	record := JobRecord{
		ID:            jobID,
		Status:        "queued",
		URL:           strings.TrimSpace(input.URL),
		WorkspaceRoot: workspace,
		DestDir:       destination,
		FormatID:      strings.TrimSpace(input.FormatID),
		Quality:       firstNonEmpty(strings.TrimSpace(input.Quality), "best"),
		AudioOnly:     input.AudioOnly,
		Subtitles:     append([]string(nil), input.Subtitles...),
		Playlist:      input.Playlist,
		MaxItems:      clampMaxItems(input.MaxItems),
		MaxBytes:      clampMaxBytes(input.MaxBytes),
		Overwrite:     input.Overwrite,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		YtdlpVersion:  location.Version,
		FFmpegPath:    ffmpegPath,
	}
	if err := saveJob(&record); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return nil, fmt.Errorf("创建下载目录失败: %w", err)
	}
	if err := startWorkerProcess(jobID); err != nil {
		_ = mutateJob(jobID, func(item *JobRecord) {
			item.Status = "failed"
			item.Error = err.Error()
			item.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		})
		return nil, err
	}
	return map[string]any{"job_id": jobID, "status": "queued", "dest_dir": destination}, nil
}

func startWorkerProcess(jobID string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	command := exec.Command(executable, "--worker", jobID)
	command.Dir = filepath.Dir(executable)
	command.Env = os.Environ()
	configureHiddenProcess(command)
	if err := command.Start(); err != nil {
		return fmt.Errorf("启动下载任务失败: %w", err)
	}
	return command.Process.Release()
}

func cancelJob(jobID string) (map[string]any, error) {
	record, err := loadJob(jobID)
	if err != nil {
		return nil, err
	}
	switch record.Status {
	case "completed", "failed", "canceled":
		return map[string]any{"job_id": jobID, "status": record.Status, "message": "任务已结束"}, nil
	}
	path, err := jobCancelPath(jobID)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644); err != nil {
		return nil, err
	}
	return map[string]any{"job_id": jobID, "status": "canceling", "message": "已请求取消，下载器会尽快停止"}, nil
}

func getJobStatus(jobID string) (JobRecord, error) {
	if !safeJobID(jobID) {
		return JobRecord{}, errors.New("job_id 不合法")
	}
	return loadJob(jobID)
}

func listJobs(input JobListInput) (map[string]any, error) {
	records, err := listJobRecords(input.Limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{"items": records, "count": len(records)}, nil
}
