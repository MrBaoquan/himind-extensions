package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
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
	workspaceInput := strings.TrimSpace(input.WorkspaceRoot)
	if workspaceInput == "" {
		home, err := os.UserHomeDir()
		if err != nil || strings.TrimSpace(home) == "" {
			return nil, errors.New("无法确定用户主目录，请绑定工作区或选择保存目录")
		}
		workspaceInput = home
	}
	workspace, err := resolveWorkspaceRoot(workspaceInput)
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
		ID:              jobID,
		Status:          "queued",
		URL:             strings.TrimSpace(input.URL),
		Title:           limitRunes(strings.TrimSpace(input.Title), 300),
		Thumbnail:       safePreviewURL(input.Thumbnail),
		Extractor:       limitRunes(strings.TrimSpace(input.Extractor), 100),
		DurationSeconds: input.DurationSeconds,
		WorkspaceRoot:   workspace,
		DestDir:         destination,
		FormatID:        strings.TrimSpace(input.FormatID),
		Quality:         firstNonEmpty(strings.TrimSpace(input.Quality), "best"),
		AudioOnly:       input.AudioOnly,
		Subtitles:       append([]string(nil), input.Subtitles...),
		Playlist:        input.Playlist,
		MaxItems:        clampMaxItems(input.MaxItems),
		MaxBytes:        clampMaxBytes(input.MaxBytes),
		Overwrite:       input.Overwrite,
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
		YtdlpVersion:    location.Version,
		FFmpegPath:      ffmpegPath,
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

func retryJob(ctx context.Context, jobID string) (map[string]any, error) {
	record, err := loadJob(jobID)
	if err != nil {
		return nil, err
	}
	switch record.Status {
	case "queued", "running", "canceling":
		return nil, errors.New("任务仍在进行中，请先等待结束或取消")
	}
	input := workerInput(record)
	input.DestDir = record.DestDir
	// 重试时覆盖同名产物，避免旧文件被当作“已完成”而跳过下载。
	input.Overwrite = true
	result, err := startJob(ctx, input)
	if err != nil {
		return nil, err
	}
	result["source_job_id"] = jobID
	return result, nil
}

func startWorkerProcess(jobID string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	command := exec.Command(executable, "--worker", jobID)
	// 父进程与 worker 必须使用同一个工作目录；受限环境下任务数据会回退到
	// 工作目录下的 .himind-data，若这里切到 bin/ 会看不到同一个任务。
	if cwd, err := os.Getwd(); err == nil && strings.TrimSpace(cwd) != "" {
		command.Dir = cwd
	} else {
		command.Dir = filepath.Dir(executable)
	}
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

func listJobs(input JobListInput) (JobListResult, error) {
	return listJobRecords(input)
}

func removeJob(jobID string) (map[string]any, error) {
	record, err := removeJobRecord(jobID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"job_id":  jobID,
		"status":  record.Status,
		"message": "任务记录已删除，已下载的媒体文件不受影响",
	}, nil
}

func getJobLog(input JobLogInput) (JobLogResult, error) {
	return readJobLogTail(input.JobID, input.MaxBytes)
}

func limitRunes(value string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}

func safePreviewURL(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return ""
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ""
	}
	return limitRunes(value, 2000)
}
