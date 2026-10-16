// Package job 管理长任务（转码这类动辄几分钟的工作）。
//
// 为什么必须有它而不是"同步跑完再返回"：转码几小时的任务，调用方（AI 或界面）
// 不可能一直阻塞等待，也无法在中途询问进度或取消。作业模型把"提交"与"观察"分开，
// 并且把进度做成结构化数据——而不是让人去解析 ffmpeg 的人类可读输出。
package job

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"himind-plugin/media-toolkit/internal/media/ffmpeg"
	"himind-plugin/media-toolkit/internal/media/plugindata"
)

// 作业状态。
const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

// Record 一条作业记录。字段刻意扁平，便于直接作为能力返回值。
type Record struct {
	ID       string  `json:"id"`
	Kind     string  `json:"kind"`
	Status   string  `json:"status"`
	Progress float64 `json:"progress"`
	Input    string  `json:"input,omitempty"`
	Output   string  `json:"output,omitempty"`
	// Recipe 摘要（人类可读），用于回看"这条是怎么编出来的"。
	Recipe    string   `json:"recipe,omitempty"`
	Args      []string `json:"args,omitempty"`
	TotalMs   int64    `json:"total_ms,omitempty"`
	OutTimeMs int64    `json:"out_time_ms,omitempty"`
	Speed     string   `json:"speed,omitempty"`
	Error     string   `json:"error,omitempty"`
	LogPath   string   `json:"log_path,omitempty"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
	ElapsedMs int64    `json:"elapsed_ms"`
}

// Store 作业存储。并发安全；记录落盘，进程重启后仍可查。
type Store struct {
	mu      sync.Mutex
	tools   ffmpeg.Tools
	root    string
	jobs    map[string]*Record
	cancels map[string]context.CancelFunc
}

// NewStore 创建作业存储。
func NewStore(tools ffmpeg.Tools) (*Store, error) {
	root, err := plugindata.Sub("jobs")
	if err != nil {
		return nil, err
	}
	store := &Store{
		tools:   tools,
		root:    root,
		jobs:    make(map[string]*Record),
		cancels: make(map[string]context.CancelFunc),
	}
	store.loadExisting()
	return store, nil
}

// Submit 登记一条作业并异步执行。返回登记后的记录。
//
// 先把记录置为 queued 再返回，保证调用方立刻拿得到 ID ——
// 若等真正跑起来才返回，长任务的提交接口会阻塞在启动阶段。
func (s *Store) Submit(kind, input, output, description string, args []string, totalMs int64) (*Record, error) {
	if s.tools.FFmpeg == "" {
		return nil, ffmpeg.ErrNotAvailable
	}
	id, err := newID()
	if err != nil {
		return nil, err
	}
	logPath, err := s.logPath(id)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	record := &Record{
		ID:        id,
		Kind:      kind,
		Status:    StatusQueued,
		Progress:  0,
		Input:     input,
		Output:    output,
		Recipe:    description,
		Args:      args,
		TotalMs:   totalMs,
		LogPath:   logPath,
		CreatedAt: now,
		UpdatedAt: now,
	}

	s.mu.Lock()
	s.jobs[id] = record
	s.mu.Unlock()
	s.persist(record)

	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancels[id] = cancel
	s.mu.Unlock()

	go s.run(ctx, record)
	return record, nil
}

func (s *Store) run(ctx context.Context, record *Record) {
	started := time.Now()
	s.update(record.ID, func(item *Record) {
		item.Status = StatusRunning
	})

	logFile, logErr := os.Create(record.LogPath)
	var logWriter *os.File
	if logErr == nil {
		logWriter = logFile
		defer logFile.Close()
	}

	_, err := s.tools.Run(ctx, ffmpeg.RunOptions{
		Args:      record.Args,
		LogWriter: logWriter,
		OnProgress: func(progress ffmpeg.Progress) {
			s.update(record.ID, func(item *Record) {
				item.OutTimeMs = progress.OutTimeMs
				item.Speed = progress.Speed
				if item.TotalMs > 0 && progress.OutTimeMs > 0 {
					ratio := float64(progress.OutTimeMs) / float64(item.TotalMs)
					if ratio > 1 {
						ratio = 1
					}
					item.Progress = float64(int(ratio*1000)) / 10
				}
			})
		},
	})

	s.update(record.ID, func(item *Record) {
		item.ElapsedMs = time.Since(started).Milliseconds()
		switch {
		case err == nil:
			item.Status = StatusCompleted
			item.Progress = 100
		case ctx.Err() != nil:
			item.Status = StatusCancelled
			item.Error = "已被取消"
		default:
			item.Status = StatusFailed
			item.Error = err.Error()
		}
	})

	s.mu.Lock()
	if cancel, ok := s.cancels[record.ID]; ok {
		cancel()
		delete(s.cancels, record.ID)
	}
	s.mu.Unlock()
}

// Get 取一条作业记录。
func (s *Store) Get(id string) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.jobs[id]
	if !ok {
		return nil, fmt.Errorf("未找到作业: %s", id)
	}
	copied := *record
	copied.Args = append([]string(nil), record.Args...)
	return &copied, nil
}

// Cancel 取消一条作业。
func (s *Store) Cancel(id string) error {
	s.mu.Lock()
	cancel, ok := s.cancels[id]
	record, exists := s.jobs[id]
	s.mu.Unlock()

	if !exists {
		return fmt.Errorf("未找到作业: %s", id)
	}
	if !ok {
		return fmt.Errorf("作业已结束，无法取消: %s (%s)", id, record.Status)
	}
	cancel()
	return nil
}

// List 列出作业，最新的在前。
func (s *Store) List(limit int) []Record {
	s.mu.Lock()
	defer s.mu.Unlock()

	records := make([]Record, 0, len(s.jobs))
	for _, record := range s.jobs {
		copied := *record
		copied.Args = append([]string(nil), record.Args...)
		records = append(records, copied)
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].CreatedAt > records[j].CreatedAt
	})
	if limit > 0 && len(records) > limit {
		records = records[:limit]
	}
	return records
}

// ── 内部 ──────────────────────────────────────────────

func (s *Store) update(id string, mutate func(*Record)) {
	s.mu.Lock()
	record, ok := s.jobs[id]
	if !ok {
		s.mu.Unlock()
		return
	}
	mutate(record)
	record.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	copied := *record
	copied.Args = append([]string(nil), record.Args...)
	s.mu.Unlock()
	s.persist(&copied)
}

func (s *Store) persist(record *Record) {
	dir := filepath.Join(s.root, record.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	payload, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return
	}
	// 先写临时文件再改名：避免读到写了一半的记录。
	target := filepath.Join(dir, "job.json")
	temp := target + ".tmp"
	if err := os.WriteFile(temp, payload, 0o644); err != nil {
		return
	}
	_ = os.Rename(temp, target)
}

func (s *Store) loadExisting() {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !safeID(entry.Name()) {
			continue
		}
		payload, readErr := os.ReadFile(filepath.Join(s.root, entry.Name(), "job.json"))
		if readErr != nil {
			continue
		}
		var record Record
		if json.Unmarshal(payload, &record) != nil || record.ID == "" {
			continue
		}
		// 进程重启后，运行中的作业实际已经死了：标成失败而不是永远停在 running，
		// 否则调用方会一直等一个永远不会完成的进度。
		if record.Status == StatusRunning || record.Status == StatusQueued {
			record.Status = StatusFailed
			record.Error = "插件进程重启，作业中断"
		}
		s.jobs[record.ID] = &record
	}
}

func (s *Store) logPath(id string) (string, error) {
	root, err := plugindata.Sub("logs")
	if err != nil {
		return "", err
	}
	return filepath.Join(root, id+".log"), nil
}

func newID() (string, error) {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("生成作业 ID 失败: %w", err)
	}
	return hex.EncodeToString(buffer), nil
}

// safeID 只接受十六进制串，杜绝用作业 ID 做路径拼接时的目录穿越。
func safeID(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, char := range value {
		isDigit := char >= '0' && char <= '9'
		isLowerHex := char >= 'a' && char <= 'f'
		if !isDigit && !isLowerHex {
			return false
		}
	}
	return true
}

// NormalizeLimit 把 limit 收敛到合理范围。
func NormalizeLimit(value, fallback, maximum int) int {
	if value <= 0 {
		return fallback
	}
	if value > maximum {
		return maximum
	}
	return value
}
