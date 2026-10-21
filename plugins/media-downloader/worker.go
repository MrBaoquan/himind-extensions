package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type fileFingerprint struct {
	Size    int64
	ModTime time.Time
}

func runWorker(jobID string) error {
	record, err := loadJob(jobID)
	if err != nil {
		return err
	}
	if record.Status == "completed" || record.Status == "canceled" || record.Status == "failed" {
		return nil
	}
	location, err := locateYtDlp()
	if err != nil {
		return failJob(jobID, err)
	}
	version, err := runtimeVersion(context.Background(), location.Path)
	if err != nil {
		return failJob(jobID, err)
	}
	ffmpegPath, _ := ffmpegStatus(context.Background())
	before, _ := snapshotFiles(record.DestDir)
	startedAt := time.Now().UTC()
	_ = mutateJob(jobID, func(item *JobRecord) {
		item.Status = "running"
		item.PID = os.Getpid()
		item.StartedAt = startedAt.Format(time.RFC3339)
		item.YtdlpVersion = version
		item.FFmpegPath = ffmpegPath
	})

	logPath, err := jobLogPath(jobID)
	if err != nil {
		return failJob(jobID, err)
	}
	logFile, err := os.Create(logPath)
	if err != nil {
		return failJob(jobID, err)
	}
	defer logFile.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelPath, _ := jobCancelPath(jobID)
	go watchCancellation(ctx, cancel, cancelPath)

	args, err := buildDownloadArgs(workerInput(record), record.DestDir, ffmpegPath != "")
	if err != nil {
		return failJob(jobID, err)
	}
	command := exec.CommandContext(ctx, location.Path, args...)
	command.Dir = record.WorkspaceRoot
	command.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8")
	configureHiddenProcess(command)
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		return terminateProcessTree(command.Process.Pid)
	}
	command.WaitDelay = 5 * time.Second
	progress := &jobProgressWriter{jobID: jobID}
	command.Stdout = io.MultiWriter(logFile, progress)
	command.Stderr = logFile
	runErr := command.Run()
	if cancelRequested(cancelPath) {
		_ = mutateJob(jobID, func(item *JobRecord) {
			item.Status = "canceled"
			item.Error = ""
			item.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		})
		return nil
	}
	if runErr != nil {
		detail := strings.TrimSpace(runErr.Error())
		if data, readErr := os.ReadFile(logPath); readErr == nil {
			text := strings.TrimSpace(string(data))
			if len(text) > 2000 {
				text = text[len(text)-2000:]
			}
			if text != "" {
				detail = text
			}
		}
		return failJob(jobID, fmt.Errorf("下载失败: %s", detail))
	}
	after, _ := snapshotFiles(record.DestDir)
	outputs := changedFiles(before, after, record.DestDir)
	outputs = filterMediaOutputs(outputs)
	if len(outputs) == 0 {
		return failJob(jobID, fmt.Errorf("下载器已结束，但没有检测到新的媒体文件"))
	}
	var outputBytes int64
	for _, output := range outputs {
		if info, statErr := os.Stat(output); statErr == nil {
			outputBytes += info.Size()
		}
		if err := writeSourceSidecar(record, output, version, ffmpegPath); err != nil {
			return failJob(jobID, err)
		}
	}
	_ = mutateJob(jobID, func(item *JobRecord) {
		item.Status = "completed"
		item.Progress = 100
		item.Outputs = outputs
		item.OutputBytes = outputBytes
		item.Error = ""
		item.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	})
	return nil
}

func workerInput(record JobRecord) JobStartInput {
	return JobStartInput{
		WorkspaceRoot:   record.WorkspaceRoot,
		URL:             record.URL,
		DestDir:         record.DestDir,
		FormatID:        record.FormatID,
		Quality:         record.Quality,
		AudioOnly:       record.AudioOnly,
		Subtitles:       record.Subtitles,
		Playlist:        record.Playlist,
		MaxItems:        record.MaxItems,
		MaxBytes:        record.MaxBytes,
		Overwrite:       record.Overwrite,
		Title:           record.Title,
		Thumbnail:       record.Thumbnail,
		Extractor:       record.Extractor,
		DurationSeconds: record.DurationSeconds,
	}
}

func failJob(jobID string, err error) error {
	_ = mutateJob(jobID, func(item *JobRecord) {
		item.Status = "failed"
		item.Error = err.Error()
		item.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	})
	return err
}

func watchCancellation(ctx context.Context, cancel context.CancelFunc, cancelPath string) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if cancelRequested(cancelPath) {
				cancel()
				return
			}
		}
	}
}

func cancelRequested(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

type jobProgressWriter struct {
	jobID string
	mu    sync.Mutex
	buf   string
	last  time.Time
}

func (w *jobProgressWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf += string(p)
	for {
		index := strings.IndexByte(w.buf, '\n')
		if index < 0 {
			break
		}
		line := w.buf[:index]
		w.buf = w.buf[index+1:]
		if progress, downloaded, total, speed, eta, ok := parseProgressLine(line); ok {
			now := time.Now()
			if now.Sub(w.last) >= time.Second {
				w.last = now
				_ = mutateJob(w.jobID, func(item *JobRecord) {
					item.Progress = progress
					item.DownloadedBytes = downloaded
					item.TotalBytes = total
					item.Speed = speed
					item.ETA = eta
				})
			}
		}
	}
	return len(p), nil
}

func snapshotFiles(root string) (map[string]fileFingerprint, error) {
	result := map[string]fileFingerprint{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		result[path] = fileFingerprint{Size: info.Size(), ModTime: info.ModTime()}
		return nil
	})
	if os.IsNotExist(err) {
		return result, nil
	}
	return result, err
}

func changedFiles(before, after map[string]fileFingerprint, destinationRoot string) []string {
	paths := make([]string, 0)
	for path, fingerprint := range after {
		old, existed := before[path]
		if !existed || old.Size != fingerprint.Size || !old.ModTime.Equal(fingerprint.ModTime) {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	result := paths[:0]
	for _, path := range paths {
		absolute, err := filepath.Abs(path)
		if err != nil || !withinRoot(destinationRoot, absolute) {
			continue
		}
		result = append(result, absolute)
	}
	return result
}

func filterMediaOutputs(paths []string) []string {
	result := paths[:0]
	for _, path := range paths {
		lower := strings.ToLower(path)
		if strings.HasSuffix(lower, ".part") || strings.HasSuffix(lower, ".ytdl") || strings.HasSuffix(lower, ".temp") || strings.HasSuffix(lower, ".tmp") || strings.HasSuffix(lower, ".info.json") || strings.HasSuffix(lower, ".source.json") {
			continue
		}
		result = append(result, path)
	}
	return result
}

func writeSourceSidecar(record JobRecord, outputPath, version, ffmpegPath string) error {
	digest, size, err := sha256File(outputPath)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"source_url":     record.URL,
		"job_id":         record.ID,
		"downloaded_at":  time.Now().UTC().Format(time.RFC3339),
		"yt_dlp_version": version,
		"ffmpeg_path":    ffmpegPath,
		"format_id":      record.FormatID,
		"quality":        record.Quality,
		"audio_only":     record.AudioOnly,
		"sha256":         digest,
		"bytes":          size,
		"workspace_root": record.WorkspaceRoot,
		"dest_dir":       record.DestDir,
	}
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	sidecar := outputPath + ".source.json"
	return os.WriteFile(sidecar, append(encoded, '\n'), 0o644)
}

func sha256Bytes(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
