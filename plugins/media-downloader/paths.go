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
	"sort"
	"strings"
	"sync"
	"time"
)

var jobsMu sync.Mutex

var jobsCacheMu sync.Mutex
var jobsCacheRecords []JobRecord
var jobsCacheAt time.Time
var jobsCacheRoot string
var jobsCacheGeneration uint64

const jobsCacheTTL = 1500 * time.Millisecond

var validJobStatuses = map[string]bool{
	"queued":    true,
	"running":   true,
	"canceling": true,
	"completed": true,
	"failed":    true,
	"canceled":  true,
}

func userCachePluginDataRoot() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("无法确定用户缓存目录: %w", err)
	}
	root := filepath.Join(cache, "himind-media-downloader")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("创建插件缓存目录失败: %w", err)
	}
	return root, nil
}

func pluginDataRoot() (string, error) {
	if value := strings.TrimSpace(os.Getenv("HIMIND_PLUGIN_DATA_ROOT")); value != "" {
		if err := os.MkdirAll(value, 0o755); err != nil {
			if info, statErr := os.Stat(value); statErr != nil || !info.IsDir() {
				return "", fmt.Errorf("创建插件数据目录失败: %w", err)
			}
		}
		return value, nil
	}
	return userCachePluginDataRoot()
}

func canWriteDirectory(root string) bool {
	probe, err := os.CreateTemp(root, ".write-probe-*")
	if err != nil {
		return false
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return true
}

func pluginDataRootCandidates() ([]string, error) {
	configured, err := pluginDataRoot()
	if err != nil {
		return nil, err
	}
	candidates := []string{configured}
	if fallback, err := userCachePluginDataRoot(); err == nil && !strings.EqualFold(configured, fallback) {
		candidates = append(candidates, fallback)
	}
	if cwd, err := os.Getwd(); err == nil && strings.TrimSpace(cwd) != "" {
		candidates = append(candidates, filepath.Join(cwd, ".himind-data"))
	}
	return candidates, nil
}

// Agent 的插件进程运行在受限令牌下，plugin-data/用户缓存/系统下载目录都可能
// 不可写；最后一个稳定落点是插件执行目录。任务数据必须可写，否则任务在创建
// 元数据时就会失败。
func writablePluginDataRoot() (string, error) {
	candidates, err := pluginDataRootCandidates()
	if err != nil {
		return "", err
	}
	for _, candidate := range candidates {
		if err := os.MkdirAll(candidate, 0o755); err != nil {
			continue
		}
		if canWriteDirectory(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("插件数据目录不可写（已尝试: %s）", strings.Join(candidates, "; "))
}

func runtimesRoot() (string, error) {
	root, err := writablePluginDataRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "runtimes"), nil
}

func runtimesRoots() ([]string, error) {
	candidates, err := pluginDataRootCandidates()
	if err != nil {
		return nil, err
	}
	roots := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		roots = append(roots, filepath.Join(candidate, "runtimes"))
	}
	return roots, nil
}

func jobsRoot() (string, error) {
	root, err := writablePluginDataRoot()
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

func defaultDownloadsDir() string {
	if path := knownDownloadsDir(); strings.TrimSpace(path) != "" {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, "Downloads")
}

func isFilesystemRoot(path string) bool {
	volume := filepath.VolumeName(path)
	if volume == "" {
		return filepath.Clean(path) == string(filepath.Separator)
	}
	return strings.EqualFold(filepath.Clean(path), volume+string(filepath.Separator))
}

func writableDownloadFallback(workspaceRoot string) (string, error) {
	candidates := make([]string, 0, 2)
	if strings.TrimSpace(workspaceRoot) != "" {
		candidates = append(candidates, filepath.Join(workspaceRoot, "media"))
	}
	if cwd, err := os.Getwd(); err == nil && strings.TrimSpace(cwd) != "" {
		candidates = append(candidates, filepath.Join(cwd, ".himind-downloads"))
	}
	for _, candidate := range candidates {
		if err := os.MkdirAll(candidate, 0o755); err == nil && canWriteDirectory(candidate) {
			return candidate, nil
		}
	}
	return "", errors.New("没有可写的下载目录")
}

func ensureWritableDestination(target, workspaceRoot string) (string, error) {
	if err := os.MkdirAll(target, 0o755); err == nil && canWriteDirectory(target) {
		return target, nil
	}
	if fallback, err := writableDownloadFallback(workspaceRoot); err == nil {
		return fallback, nil
	}
	return "", fmt.Errorf("保存目录不可写: %s", target)
}

func resolveDestination(workspaceRoot, raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		value = defaultDownloadsDir()
		if value == "" {
			return "", errors.New("无法确定系统下载目录，请手动选择保存目录")
		}
	}
	if filepath.IsAbs(value) {
		target, err := filepath.Abs(filepath.Clean(value))
		if err != nil {
			return "", fmt.Errorf("保存目录无效: %w", err)
		}
		if isFilesystemRoot(target) {
			return "", errors.New("保存目录不能是磁盘根目录")
		}
		if info, err := os.Stat(target); err == nil && !info.IsDir() {
			return "", errors.New("保存目录必须是文件夹")
		}
		return ensureWritableDestination(target, workspaceRoot)
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
	return ensureWritableDestination(target, workspaceRoot)
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

func invalidateJobsCache() {
	jobsCacheMu.Lock()
	jobsCacheGeneration++
	jobsCacheAt = time.Time{}
	jobsCacheMu.Unlock()
}

func cloneJobRecords(records []JobRecord) []JobRecord {
	out := make([]JobRecord, len(records))
	copy(out, records)
	return out
}

func mutateJob(jobID string, mutate func(*JobRecord)) error {
	jobsMu.Lock()
	path, err := jobRecordPath(jobID)
	if err != nil {
		jobsMu.Unlock()
		return err
	}
	var record JobRecord
	if err := readJSON(path, &record); err != nil {
		jobsMu.Unlock()
		return err
	}
	mutate(&record)
	err = writeJSON(path, &record)
	jobsMu.Unlock()
	if err == nil {
		invalidateJobsCache()
	}
	return err
}

func saveJob(record *JobRecord) error {
	jobsMu.Lock()
	path, err := jobRecordPath(record.ID)
	if err != nil {
		jobsMu.Unlock()
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		jobsMu.Unlock()
		return err
	}
	err = writeJSON(path, record)
	jobsMu.Unlock()
	if err == nil {
		invalidateJobsCache()
	}
	return err
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

func loadAllJobRecords() ([]JobRecord, error) {
	root, err := jobsRoot()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}

	jobsCacheMu.Lock()
	if jobsCacheRoot == root && !jobsCacheAt.IsZero() && time.Since(jobsCacheAt) < jobsCacheTTL {
		cached := cloneJobRecords(jobsCacheRecords)
		jobsCacheMu.Unlock()
		return cached, nil
	}
	generation := jobsCacheGeneration
	jobsCacheMu.Unlock()

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
	sort.SliceStable(records, func(i, j int) bool {
		return records[i].CreatedAt > records[j].CreatedAt
	})

	jobsCacheMu.Lock()
	if jobsCacheRoot == root && generation == jobsCacheGeneration {
		jobsCacheRecords = cloneJobRecords(records)
		jobsCacheAt = time.Now()
	}
	jobsCacheMu.Unlock()
	return records, nil
}

func summarizeJobs(records []JobRecord) JobSummary {
	summary := JobSummary{Total: len(records)}
	for _, record := range records {
		switch record.Status {
		case "queued":
			summary.Queued++
		case "running":
			summary.Running++
		case "canceling":
			summary.Canceling++
		case "completed":
			summary.Completed++
		case "failed":
			summary.Failed++
		case "canceled":
			summary.Canceled++
		}
		summary.DownloadedBytes += record.DownloadedBytes
		if record.OutputBytes > 0 {
			summary.OutputBytes += record.OutputBytes
		} else if record.Status == "completed" && record.TotalBytes > 0 {
			summary.OutputBytes += record.TotalBytes
		}
	}
	summary.Active = summary.Queued + summary.Running + summary.Canceling
	return summary
}

func parseJobTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

func filterJobRecords(records []JobRecord, input JobListInput) []JobRecord {
	statuses := make(map[string]bool, len(input.Statuses))
	for _, status := range input.Statuses {
		status = strings.ToLower(strings.TrimSpace(status))
		if validJobStatuses[status] {
			statuses[status] = true
		}
	}
	query := strings.ToLower(strings.TrimSpace(input.Query))
	since, hasSince := parseJobTime(input.Since)
	until, hasUntil := parseJobTime(input.Until)
	filtered := make([]JobRecord, 0, len(records))
	for _, record := range records {
		if len(statuses) > 0 && !statuses[record.Status] {
			continue
		}
		created, hasCreated := parseJobTime(record.CreatedAt)
		if hasSince && hasCreated && created.Before(since) {
			continue
		}
		if hasUntil && hasCreated && created.After(until) {
			continue
		}
		if query != "" {
			haystack := strings.ToLower(strings.Join([]string{
				record.ID,
				record.Title,
				record.URL,
				record.DestDir,
				record.Extractor,
				record.Error,
			}, "\n"))
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		filtered = append(filtered, record)
	}
	return filtered
}

func sortJobRecords(records []JobRecord, order string) {
	switch strings.TrimSpace(order) {
	case "created_asc":
		sort.SliceStable(records, func(i, j int) bool {
			return records[i].CreatedAt < records[j].CreatedAt
		})
	case "size_desc":
		sort.SliceStable(records, func(i, j int) bool {
			left := records[i].OutputBytes
			if left == 0 {
				left = records[i].TotalBytes
			}
			right := records[j].OutputBytes
			if right == 0 {
				right = records[j].TotalBytes
			}
			return left > right
		})
	case "progress_desc":
		sort.SliceStable(records, func(i, j int) bool {
			return records[i].Progress > records[j].Progress
		})
	case "title_asc":
		sort.SliceStable(records, func(i, j int) bool {
			left := strings.ToLower(firstNonEmpty(records[i].Title, records[i].URL))
			right := strings.ToLower(firstNonEmpty(records[j].Title, records[j].URL))
			return left < right
		})
	default:
		sort.SliceStable(records, func(i, j int) bool {
			return records[i].CreatedAt > records[j].CreatedAt
		})
	}
}

func listJobRecords(input JobListInput) (JobListResult, error) {
	records, err := loadAllJobRecords()
	if err != nil {
		return JobListResult{}, err
	}
	summary := summarizeJobs(records)
	filtered := filterJobRecords(records, input)
	sortJobRecords(filtered, input.Sort)

	limit := input.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset := input.Offset
	if offset < 0 {
		offset = 0
	}
	start := offset
	if start > len(filtered) {
		start = len(filtered)
	}
	end := start + limit
	if end > len(filtered) {
		end = len(filtered)
	}
	page := make([]JobRecord, 0, end-start)
	page = append(page, filtered[start:end]...)
	return JobListResult{
		Items:   page,
		Total:   len(filtered),
		Count:   len(page),
		Offset:  offset,
		Limit:   limit,
		Summary: summary,
	}, nil
}

func removeJobRecord(jobID string) (JobRecord, error) {
	record, err := loadJob(jobID)
	if err != nil {
		return JobRecord{}, err
	}
	switch record.Status {
	case "completed", "failed", "canceled":
	default:
		return JobRecord{}, errors.New("只能删除已结束的任务记录，请先取消进行中的任务")
	}
	dir, err := jobDir(jobID)
	if err != nil {
		return JobRecord{}, err
	}
	jobsMu.Lock()
	err = os.RemoveAll(dir)
	jobsMu.Unlock()
	if err != nil {
		return JobRecord{}, fmt.Errorf("删除任务记录失败: %w", err)
	}
	invalidateJobsCache()
	return record, nil
}

func cleanupJobRecords(input JobCleanupInput) (JobCleanupResult, error) {
	statuses := make(map[string]bool, len(input.Statuses))
	for _, status := range input.Statuses {
		status = strings.ToLower(strings.TrimSpace(status))
		if validJobStatuses[status] {
			statuses[status] = true
		}
	}
	if len(statuses) == 0 {
		statuses["completed"] = true
		statuses["failed"] = true
		statuses["canceled"] = true
	}
	days := input.OlderThanDays
	if days < 0 {
		days = 0
	}
	if days > 3650 {
		days = 3650
	}
	var cutoff time.Time
	if days > 0 {
		cutoff = time.Now().UTC().AddDate(0, 0, -days)
	}

	records, err := loadAllJobRecords()
	if err != nil {
		return JobCleanupResult{}, err
	}
	candidates := make([]JobRecord, 0)
	for _, record := range records {
		if !statuses[record.Status] {
			continue
		}
		if days > 0 {
			created, ok := parseJobTime(record.CreatedAt)
			if !ok || created.After(cutoff) {
				continue
			}
		}
		candidates = append(candidates, record)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].CreatedAt < candidates[j].CreatedAt
	})

	result := JobCleanupResult{Matched: len(candidates), DryRun: input.DryRun}
	if input.DryRun {
		result.Remaining = len(candidates)
		return result, nil
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 200
	}
	if limit > 500 {
		limit = 500
	}
	if limit > len(candidates) {
		limit = len(candidates)
	}
	for _, record := range candidates[:limit] {
		if _, err := removeJobRecord(record.ID); err != nil {
			result.Failed++
			continue
		}
		result.Removed++
	}
	result.Remaining = result.Matched - result.Removed
	return result, nil
}

func readJobLogTail(jobID string, maxBytes int) (JobLogResult, error) {
	record, err := loadJob(jobID)
	if err != nil {
		return JobLogResult{}, err
	}
	path := record.LogPath
	if path == "" {
		path, err = jobLogPath(jobID)
		if err != nil {
			return JobLogResult{}, err
		}
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return JobLogResult{JobID: jobID, Path: path, Tail: "", Truncated: false, Size: 0}, nil
		}
		return JobLogResult{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return JobLogResult{}, err
	}
	if maxBytes <= 0 {
		maxBytes = 64 * 1024
	}
	if maxBytes > 256*1024 {
		maxBytes = 256 * 1024
	}
	start := info.Size() - int64(maxBytes)
	if start < 0 {
		start = 0
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return JobLogResult{}, err
	}
	payload, err := io.ReadAll(io.LimitReader(file, int64(maxBytes)))
	if err != nil {
		return JobLogResult{}, err
	}
	truncated := start > 0
	if truncated {
		if index := strings.IndexByte(string(payload), '\n'); index >= 0 {
			payload = payload[index+1:]
		}
	}
	return JobLogResult{
		JobID:     jobID,
		Path:      path,
		Tail:      string(payload),
		Truncated: truncated,
		Size:      info.Size(),
	}, nil
}
