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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"himind-plugin/media-toolkit/internal/media/ffmpeg"
	"himind-plugin/media-toolkit/internal/media/plugindata"
)

// 作业状态。
const (
	StatusQueued      = "queued"
	StatusRunning     = "running"
	StatusCompleted   = "completed"
	StatusFailed      = "failed"
	StatusCancelled   = "cancelled"
	StatusInterrupted = "interrupted"
)

// InlineWorkerEnv 仅供测试使用；生产作业必须由独立 worker 进程执行。
const InlineWorkerEnv = "HIMIND_MEDIA_TOOLKIT_INLINE_JOBS"

// Record 一条作业记录。字段刻意扁平，便于直接作为能力返回值。
type Record struct {
	ID         string  `json:"id"`
	Kind       string  `json:"kind"`
	Status     string  `json:"status"`
	Progress   float64 `json:"progress"`
	Input      string  `json:"input,omitempty"`
	Output     string  `json:"output,omitempty"`
	TempOutput string  `json:"temp_output,omitempty"`
	WorkerPID  int     `json:"worker_pid,omitempty"`
	Heartbeat  string  `json:"heartbeat_at,omitempty"`
	// Recipe 摘要（人类可读），用于回看"这条是怎么编出来的"。
	Recipe    string   `json:"recipe,omitempty"`
	Args      []string `json:"args,omitempty"`
	RunArgs   []string `json:"-"`
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
	inline  bool
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
		inline:  strings.TrimSpace(os.Getenv(InlineWorkerEnv)) == "1",
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
	tempOutput, err := createTempOutput(output)
	if err != nil {
		return nil, err
	}
	runArgs := append([]string(nil), args...)
	if len(runArgs) == 0 {
		_ = os.Remove(tempOutput)
		return nil, fmt.Errorf("ffmpeg 参数为空")
	}
	// recipe 编译结果始终以输出路径结尾；先写同目录临时文件，成功后再原子替换，
	// 避免失败/取消留下半截产物，或把上一版可用产物提前删掉。
	runArgs[len(runArgs)-1] = tempOutput

	now := time.Now().UTC().Format(time.RFC3339)
	record := &Record{
		ID:         id,
		Kind:       kind,
		Status:     StatusQueued,
		Progress:   0,
		Input:      input,
		Output:     output,
		TempOutput: tempOutput,
		Recipe:     description,
		Args:       append([]string(nil), args...),
		RunArgs:    runArgs,
		TotalMs:    totalMs,
		LogPath:    logPath,
		Heartbeat:  now,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	s.mu.Lock()
	s.jobs[id] = record
	s.mu.Unlock()
	s.persist(record)

	if s.inline {
		ctx, cancel := context.WithCancel(context.Background())
		s.mu.Lock()
		s.cancels[id] = cancel
		s.mu.Unlock()
		go s.run(ctx, record)
		return record, nil
	}

	workerPID, err := spawnWorker(filepath.Join(s.root, id))
	if err != nil {
		s.update(id, func(item *Record) {
			item.Status = StatusFailed
			item.Error = "启动后台 worker 失败: " + err.Error()
			item.TempOutput = ""
			item.Heartbeat = ""
		})
		_ = os.Remove(tempOutput)
		return nil, err
	}
	s.mu.Lock()
	record.WorkerPID = workerPID
	s.mu.Unlock()
	s.persist(record)
	return record, nil
}

func (s *Store) run(ctx context.Context, record *Record) {
	started := time.Now()
	s.update(record.ID, func(item *Record) {
		item.Status = StatusRunning
		item.WorkerPID = os.Getpid()
		item.Heartbeat = time.Now().UTC().Format(time.RFC3339)
	})
	runCtx, stopWatch := s.watchJob(ctx, record.ID)
	defer stopWatch()

	logFile, logErr := os.Create(record.LogPath)
	var logWriter *os.File
	if logErr == nil {
		logWriter = logFile
		defer logFile.Close()
	}

	runArgs := record.RunArgs
	if len(runArgs) == 0 {
		runArgs = append([]string(nil), record.Args...)
		if record.TempOutput != "" && len(runArgs) > 0 {
			runArgs[len(runArgs)-1] = record.TempOutput
		}
	}
	_, err := s.tools.Run(runCtx, ffmpeg.RunOptions{
		Args:      runArgs,
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

	var finalizeErr error
	if err == nil {
		finalizeErr = finalizeTempOutput(record.TempOutput, record.Output)
	}
	if err != nil || finalizeErr != nil {
		_ = os.Remove(record.TempOutput)
	}
	s.update(record.ID, func(item *Record) {
		item.ElapsedMs = time.Since(started).Milliseconds()
		item.TempOutput = ""
		switch {
		case err == nil && finalizeErr == nil:
			item.Status = StatusCompleted
			item.Progress = 100
		case runCtx.Err() != nil:
			item.Status = StatusCancelled
			item.Error = "已被取消"
		case err != nil:
			item.Status = StatusFailed
			item.Error = err.Error()
		default:
			item.Status = StatusFailed
			item.Error = "写入最终产物失败: " + finalizeErr.Error()
		}
	})
	_ = os.Remove(cancelPath(s.root, record.ID))

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
	if record.Status != StatusQueued && record.Status != StatusRunning {
		return fmt.Errorf("作业已结束，无法取消: %s (%s)", id, record.Status)
	}
	marker := cancelPath(s.root, id)
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		return fmt.Errorf("创建取消标记目录失败: %w", err)
	}
	if err := os.WriteFile(marker, []byte("cancel\n"), 0o644); err != nil {
		return fmt.Errorf("写入取消标记失败: %w", err)
	}
	if ok {
		cancel()
	}
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
		// 独立 worker 会持续写心跳。父插件进程退出不再等于作业中断；
		// 只有心跳超过宽限期仍未更新，才把作业标记为 interrupted。
		if record.Status == StatusRunning || record.Status == StatusQueued {
			if heartbeatFresh(record.Heartbeat, 20*time.Second) {
				s.jobs[record.ID] = &record
				continue
			}
			record.Status = StatusInterrupted
			record.Error = "后台 worker 已停止，作业中断"
			_ = os.Remove(record.TempOutput)
			_ = os.Remove(cancelPath(s.root, record.ID))
			record.TempOutput = ""
			record.Heartbeat = ""
			s.persist(&record)
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

func createTempOutput(output string) (string, error) {
	if strings.TrimSpace(output) == "" {
		return "", fmt.Errorf("输出路径为空")
	}
	dir := filepath.Dir(output)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建输出目录失败: %w", err)
	}
	file, err := os.CreateTemp(dir, "."+filepath.Base(output)+".part-*"+filepath.Ext(output))
	if err != nil {
		return "", fmt.Errorf("创建临时输出失败: %w", err)
	}
	path := file.Name()
	if closeErr := file.Close(); closeErr != nil {
		_ = os.Remove(path)
		return "", closeErr
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	return path, nil
}

func finalizeTempOutput(temp, output string) error {
	info, err := os.Stat(temp)
	if err != nil || info.Size() == 0 {
		return fmt.Errorf("临时产物缺失或为空")
	}
	if err := os.Remove(output); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(temp, output)
}

// RunWorker 由独立进程执行一条已落盘作业。
func RunWorker(jobDir string) error {
	jobDir = filepath.Clean(strings.TrimSpace(jobDir))
	payload, err := os.ReadFile(filepath.Join(jobDir, "job.json"))
	if err != nil {
		return fmt.Errorf("读取作业失败: %w", err)
	}
	var record Record
	if err := json.Unmarshal(payload, &record); err != nil {
		return fmt.Errorf("解析作业失败: %w", err)
	}
	if record.Status == StatusCompleted || record.Status == StatusFailed || record.Status == StatusCancelled || record.Status == StatusInterrupted {
		return nil
	}
	tools, err := ffmpeg.Locate()
	if err != nil {
		return err
	}
	root := filepath.Dir(jobDir)
	store := &Store{
		tools: tools, root: root,
		jobs:    map[string]*Record{record.ID: &record},
		cancels: make(map[string]context.CancelFunc),
	}
	ctx, cancel := context.WithCancel(context.Background())
	store.cancels[record.ID] = cancel
	store.run(ctx, &record)
	return nil
}

func (s *Store) watchJob(parent context.Context, id string) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			s.update(id, func(item *Record) {
				item.WorkerPID = os.Getpid()
				item.Heartbeat = time.Now().UTC().Format(time.RFC3339)
			})
			if _, err := os.Stat(cancelPath(s.root, id)); err == nil {
				cancel()
				return
			}
			select {
			case <-parent.Done():
				cancel()
				return
			case <-done:
				return
			case <-ticker.C:
			}
		}
	}()
	return ctx, func() {
		cancel()
		close(done)
	}
}

func cancelPath(root, id string) string {
	return filepath.Join(root, id, "cancel.requested")
}

func heartbeatFresh(value string, limit time.Duration) bool {
	heartbeat, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return false
	}
	age := time.Since(heartbeat)
	return age >= -5*time.Second && age <= limit
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
