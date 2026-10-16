// Package job 管理长任务（转写动辄几分钟到几十分钟）。
//
// 为什么必须有它：转写 226 秒音频要 95 秒、一小时的片子要二三十分钟。
// 调用方（AI 或界面）不可能一直阻塞等待，也无法中途查看进度或取消。
// 作业模型把"提交"与"观察"分开。
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

	"himind-plugin/media-ai/internal/media/plugindata"
)

// 作业状态。
const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

// Record 一条作业记录。
type Record struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Status   string `json:"status"`
	Input    string `json:"input,omitempty"`
	Language string `json:"language,omitempty"`
	Model    string `json:"model,omitempty"`
	// Stage 当前阶段（抽音频 / 转写），让长任务的不同阶段可见。
	Stage string `json:"stage,omitempty"`
	// Result 完成后的转写结果（段落与文本）。
	Result *json.RawMessage `json:"result,omitempty"`
	Error  string           `json:"error,omitempty"`
	// LogPath 过程日志（whisper 的 stderr 原样落盘，便于排查）。
	LogPath   string `json:"log_path,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	ElapsedMs int64  `json:"elapsed_ms"`
}

// Store 作业存储。并发安全；记录落盘，进程重启后仍可查。
type Store struct {
	mu      sync.Mutex
	root    string
	jobs    map[string]*Record
	cancels map[string]context.CancelFunc
}

// NewStore 创建作业存储。
func NewStore() (*Store, error) {
	root, err := plugindata.Sub("jobs")
	if err != nil {
		return nil, err
	}
	store := &Store{root: root, jobs: map[string]*Record{}, cancels: map[string]context.CancelFunc{}}
	store.loadExisting()
	return store, nil
}

// Submit 登记一条作业并异步执行。run 收到 ctx 后自行完成工作并返回结果。
func (s *Store) Submit(kind, input, language, model string, run func(context.Context, *Reporter) (any, error)) (*Record, error) {
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
		ID: id, Kind: kind, Status: StatusQueued, Input: input,
		Language: language, Model: model, Stage: StatusQueued,
		LogPath: logPath, CreatedAt: now, UpdatedAt: now,
	}

	s.mu.Lock()
	s.jobs[id] = record
	s.mu.Unlock()
	s.persist(record)

	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancels[id] = cancel
	s.mu.Unlock()

	go s.run(ctx, record, run)
	return s.Get(id)
}

// Reporter 交给作业函数，用于汇报阶段与日志。
type Reporter struct {
	store *Store
	id    string
	file  *os.File
	mu    sync.Mutex
}

// Stage 汇报当前阶段。
func (r *Reporter) Stage(stage string) {
	r.store.update(r.id, func(item *Record) { item.Stage = stage })
}

// Log 追加一行过程日志。
func (r *Reporter) Log(line string) {
	if r.file == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	fmt.Fprintln(r.file, line)
}

func (s *Store) run(ctx context.Context, record *Record, run func(context.Context, *Reporter) (any, error)) {
	started := time.Now()
	s.update(record.ID, func(item *Record) {
		item.Status = StatusRunning
		item.Stage = StatusRunning
	})

	reporter := &Reporter{store: s, id: record.ID}
	if file, err := os.Create(record.LogPath); err == nil {
		reporter.file = file
		defer file.Close()
	}

	payload, err := run(ctx, reporter)

	s.update(record.ID, func(item *Record) {
		item.ElapsedMs = time.Since(started).Milliseconds()
		switch {
		case err == nil:
			item.Status = StatusCompleted
			item.Stage = StatusCompleted
			if payload != nil {
				if encoded, marshalErr := json.Marshal(payload); marshalErr == nil {
					raw := json.RawMessage(encoded)
					item.Result = &raw
				}
			}
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
		records = append(records, *record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].CreatedAt > records[j].CreatedAt })
	if limit > 0 && len(records) > limit {
		records = records[:limit]
	}
	return records
}

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
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
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
