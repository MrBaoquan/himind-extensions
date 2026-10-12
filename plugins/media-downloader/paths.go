package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var jobsMu sync.Mutex

func pluginDataRoot() (string, error) {
	if value := strings.TrimSpace(os.Getenv("HIMIND_PLUGIN_DATA_ROOT")); value != "" {
		if err := os.MkdirAll(value, 0o755); err != nil {
			return "", fmt.Errorf("创建插件数据目录失败: %w", err)
		}
		return value, nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("无法确定插件数据目录: %w", err)
	}
	root := filepath.Join(cache, "himind-media-downloader")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("创建插件数据目录失败: %w", err)
	}
	return root, nil
}

func runtimesRoot() (string, error) {
	root, err := pluginDataRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "runtimes"), nil
}

func jobsRoot() (string, error) {
	root, err := pluginDataRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "jobs"), nil
}

func jobDir(jobID string) (string, error) {
	if !safeJobID(jobID) {
		return "", errors.New("任务 ID 不合法")
	}
	root, err := jobsRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, jobID), nil
}

func jobRecordPath(jobID string) (string, error) {
	dir, err := jobDir(jobID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "job.json"), nil
}

func jobLogPath(jobID string) (string, error) {
	dir, err := jobDir(jobID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "job.log"), nil
}

func jobCancelPath(jobID string) (string, error) {
	dir, err := jobDir(jobID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cancel.requested"), nil
}

func safeJobID(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return false
	}
	return true
}

func newJobID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func resolveWorkspaceRoot(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", errors.New("workspace_root 不能为空")
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("工作区路径无效: %w", err)
	}
	real, err := filepath.EvalSymlinks(absolute)
	if err == nil {
		absolute = real
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("工作区不存在: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("workspace_root 必须是目录")
	}
	return filepath.Clean(absolute), nil
}

func resolveDestination(workspaceRoot, raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		value = "media"
	}
	if filepath.IsAbs(value) {
		return "", errors.New("dest_dir 必须是工作区内的相对路径")
	}
	clean := filepath.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("dest_dir 不能越过工作区")
	}
	target := filepath.Join(workspaceRoot, clean)
	target, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(workspaceRoot, target)
	if err != nil {
		return "", err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("dest_dir 不能越过工作区")
	}
	return target, nil
}

func withinRoot(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func writeJSON(path string, value any) error {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	temp := path + ".tmp"
	if err := os.WriteFile(temp, payload, 0o644); err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		// Windows 不允许 Rename 覆盖已有文件；单写者场景下退回到直接写。
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			_ = os.Remove(temp)
			return err
		}
		if err := os.Rename(temp, path); err != nil {
			_ = os.Remove(temp)
			return err
		}
	}
	return nil
}

func readJSON(path string, target any) error {
	payload, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(payload, target)
}

func sha256File(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func mutateJob(jobID string, mutate func(*JobRecord)) error {
	jobsMu.Lock()
	defer jobsMu.Unlock()
	path, err := jobRecordPath(jobID)
	if err != nil {
		return err
	}
	var record JobRecord
	if err := readJSON(path, &record); err != nil {
		return err
	}
	mutate(&record)
	return writeJSON(path, &record)
}

func saveJob(record *JobRecord) error {
	jobsMu.Lock()
	defer jobsMu.Unlock()
	path, err := jobRecordPath(record.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeJSON(path, record)
}

func loadJob(jobID string) (JobRecord, error) {
	jobsMu.Lock()
	defer jobsMu.Unlock()
	path, err := jobRecordPath(jobID)
	if err != nil {
		return JobRecord{}, err
	}
	var record JobRecord
	if err := readJSON(path, &record); err != nil {
		return JobRecord{}, err
	}
	return record, nil
}

func listJobRecords(limit int) ([]JobRecord, error) {
	root, err := jobsRoot()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	records := make([]JobRecord, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !safeJobID(entry.Name()) {
			continue
		}
		record, err := loadJob(entry.Name())
		if err == nil {
			records = append(records, record)
		}
	}
	for i := 0; i < len(records); i++ {
		for j := i + 1; j < len(records); j++ {
			if records[j].CreatedAt > records[i].CreatedAt {
				records[i], records[j] = records[j], records[i]
			}
		}
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if len(records) > limit {
		records = records[:limit]
	}
	return records, nil
}
