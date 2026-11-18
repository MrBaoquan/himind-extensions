// jobs.go —— 矩阵批量生成作业。
//
// 一次矩阵 = 提示词 × 风格包 × 尺寸，每格再乘变体数。矩阵是「慢、量大、要能
// 关掉窗口继续跑」的形态，所以走异步作业：start 立即返回 job_id，进度与逐项
// 结果由 status 查询，cancel 只影响尚未开始与正在执行的项。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	jobStateQueued      = "queued"
	jobStateRunning     = "running"
	jobStateSucceeded   = "succeeded"
	jobStatePartial     = "partial"
	jobStateFailed      = "failed"
	jobStateCanceled    = "canceled"
	jobStateInterrupted = "interrupted"

	itemStatePending   = "pending"
	itemStateRunning   = "running"
	itemStateSucceeded = "succeeded"
	itemStateFailed    = "failed"
	itemStateCanceled  = "canceled"

	maxMatrixItems = 36
)

type jobItem struct {
	Index   int             `json:"index"`
	StyleID string          `json:"style_id"`
	Size    string          `json:"size"`
	Prompt  string          `json:"prompt"`
	State   string          `json:"state"`
	Files   []generatedFile `json:"files,omitempty"`
	Error   string          `json:"error,omitempty"`
}

type jobRecord struct {
	ID            string     `json:"id"`
	State         string     `json:"state"`
	WorkspaceRoot string     `json:"workspace_root"`
	OutputDir     string     `json:"output_dir"`
	BasePrompt    string     `json:"base_prompt"`
	StyleIDs      []string   `json:"style_ids"`
	Sizes         []string   `json:"sizes"`
	Variants      int        `json:"variants"`
	Total         int        `json:"total"`
	Completed     int        `json:"completed"`
	Failed        int        `json:"failed"`
	Items         []*jobItem `json:"items"`
	Error         string     `json:"error,omitempty"`
	CreatedAt     string     `json:"created_at"`
	UpdatedAt     string     `json:"updated_at"`
}

// batchPlan 是已展开、已校验的执行计划：作业只负责跑，不再改语义。
type batchPlan struct {
	WorkspaceRoot string
	OutputDir     string
	BasePrompt    string
	StyleIDs      []string
	Sizes         []string
	Variants      int
	ServiceID     string
	Model         string
	Params        map[string]any
	FilenameBase  string
	Items         []*jobItem
}

type jobManager struct {
	mu      sync.Mutex
	jobs    map[string]*jobRecord
	cancels map[string]context.CancelFunc
}

func newJobManager() *jobManager {
	manager := &jobManager{jobs: map[string]*jobRecord{}, cancels: map[string]context.CancelFunc{}}
	manager.load()
	return manager
}

// load 把上一次进程留下的作业读回来，并把仍然标记为运行中的作业改判为中断。
//
// Agent 退出时插件进程也随之结束，运行中的 goroutine 不会幸存；如果照原样
// 返回 running，界面会永远停在某一个百分比上。
func (m *jobManager) load() {
	directory, err := dataSub("jobs")
	if err != nil {
		return
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			continue
		}
		var job jobRecord
		if err := json.Unmarshal(content, &job); err != nil || job.ID == "" {
			continue
		}
		if job.State == jobStateQueued || job.State == jobStateRunning {
			job.State = jobStateInterrupted
			job.UpdatedAt = nowText()
			job.Error = "作业在 Agent 退出时中断；已完成的项仍保留在结果里"
			for _, item := range job.Items {
				if item.State == itemStatePending || item.State == itemStateRunning {
					item.State = itemStateCanceled
				}
			}
			m.persist(&job)
		}
		m.jobs[job.ID] = &job
	}
}

func (m *jobManager) persist(job *jobRecord) {
	directory, err := dataSub("jobs")
	if err != nil {
		return
	}
	payload, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return
	}
	path := filepath.Join(directory, job.ID+".json")
	if err := os.WriteFile(path+".tmp", payload, 0o644); err != nil {
		return
	}
	_ = os.Rename(path+".tmp", path)
}

// start 创建作业并立即返回；实际执行在后台 goroutine 里按顺序推进。
func (m *jobManager) start(invoker CapabilityInvoker, plan batchPlan) (*jobRecord, error) {
	if invoker == nil {
		return nil, fmt.Errorf("当前没有可用的能力代理，无法开始生成")
	}
	job := &jobRecord{
		ID:            newID("job"),
		State:         jobStateQueued,
		WorkspaceRoot: plan.WorkspaceRoot,
		OutputDir:     plan.OutputDir,
		BasePrompt:    plan.BasePrompt,
		StyleIDs:      plan.StyleIDs,
		Sizes:         plan.Sizes,
		Variants:      plan.Variants,
		Total:         len(plan.Items),
		Items:         plan.Items,
		CreatedAt:     nowText(),
		UpdatedAt:     nowText(),
	}
	contextValue, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.jobs[job.ID] = job
	m.cancels[job.ID] = cancel
	m.persist(job)
	m.mu.Unlock()

	snapshot := *job
	go m.run(contextValue, invoker, job.ID, plan)
	return &snapshot, nil
}

func (m *jobManager) run(ctx context.Context, invoker CapabilityInvoker, jobID string, plan batchPlan) {
	defer func() {
		m.mu.Lock()
		if cancel, ok := m.cancels[jobID]; ok {
			cancel()
			delete(m.cancels, jobID)
		}
		m.mu.Unlock()
	}()
	m.mutate(jobID, func(job *jobRecord) { job.State = jobStateRunning })

	for index, item := range plan.Items {
		if ctx.Err() != nil {
			m.mutate(jobID, func(job *jobRecord) {
				job.State = jobStateCanceled
				if index < len(job.Items) {
					for _, pending := range job.Items[index:] {
						if pending.State == itemStatePending {
							pending.State = itemStateCanceled
						}
					}
				}
			})
			return
		}
		m.mutate(jobID, func(job *jobRecord) {
			job.Items[index].State = itemStateRunning
		})
		input := map[string]any{
			"prompt":     item.Prompt,
			"size":       item.Size,
			"n":          plan.Variants,
			"output_dir": plan.OutputDir,
			"inline":     false,
			"filename":   filenameFor(plan.FilenameBase, item.StyleID, item.Size),
		}
		if plan.ServiceID != "" {
			input["service"] = plan.ServiceID
		}
		if plan.Model != "" {
			input["model"] = plan.Model
		}
		if len(plan.Params) > 0 {
			input["parameters"] = plan.Params
		}
		result, err := invoker.Invoke(capImageGenerate, input)
		if err != nil {
			m.mutate(jobID, func(job *jobRecord) {
				job.Items[index].State = itemStateFailed
				job.Items[index].Error = err.Error()
				job.Failed++
				job.Completed++
			})
			continue
		}
		files := filesFromResult(result)
		if len(files) == 0 {
			m.mutate(jobID, func(job *jobRecord) {
				job.Items[index].State = itemStateFailed
				job.Items[index].Error = "生图能力没有返回任何文件"
				job.Failed++
				job.Completed++
			})
			continue
		}
		records := make([]Lineage, 0, len(files))
		for _, file := range files {
			records = append(records, Lineage{
				LineageID:     newID("lineage"),
				MediaID:       file.MediaID,
				Path:          file.Path,
				SHA256:        file.SHA256,
				MimeType:      file.MimeType,
				Width:         file.Width,
				Height:        file.Height,
				SizeBytes:     file.SizeBytes,
				Capability:    capImageGenerate,
				Prompt:        item.Prompt,
				BasePrompt:    plan.BasePrompt,
				StyleID:       item.StyleID,
				ServiceID:     stringValue(result["service_id"]),
				Model:         stringValue(result["model"]),
				JobID:         jobID,
				RequestID:     stringValue(result["request_id"]),
				WorkspaceRoot: plan.WorkspaceRoot,
				CreatedAt:     nowText(),
			})
		}
		_ = appendLineage(records)
		m.mutate(jobID, func(job *jobRecord) {
			job.Items[index].State = itemStateSucceeded
			job.Items[index].Files = files
			job.Completed++
		})
	}
	m.mutate(jobID, func(job *jobRecord) {
		switch {
		case job.Failed == 0:
			job.State = jobStateSucceeded
		case job.Failed >= job.Total:
			job.State = jobStateFailed
			job.Error = "所有项都失败了，请检查服务与提示词"
		default:
			job.State = jobStatePartial
		}
	})
}

// mutate 在锁内改动作业并落盘，其余读路径只通过 snapshot 拿副本。
func (m *jobManager) mutate(id string, change func(*jobRecord)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	if !ok {
		return
	}
	change(job)
	job.UpdatedAt = nowText()
	m.persist(job)
}

func (m *jobManager) snapshot(id string) (jobRecord, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	if !ok {
		return jobRecord{}, false
	}
	copyOfJob := *job
	copyOfJob.Items = make([]*jobItem, len(job.Items))
	for index, item := range job.Items {
		if item == nil {
			continue
		}
		cloned := *item
		copyOfJob.Items[index] = &cloned
	}
	return copyOfJob, true
}

func (m *jobManager) cancel(id string) (jobRecord, bool) {
	m.mu.Lock()
	cancel, running := m.cancels[id]
	m.mu.Unlock()
	if running && cancel != nil {
		cancel()
	}
	m.mutate(id, func(job *jobRecord) {
		if job.State == jobStateSucceeded || job.State == jobStateFailed || job.State == jobStateCanceled || job.State == jobStateInterrupted {
			return
		}
		if !running {
			// 没有在跑的 goroutine（例如重启后遗留的作业），直接改判。
			job.State = jobStateCanceled
			for _, item := range job.Items {
				if item.State == itemStatePending || item.State == itemStateRunning {
					item.State = itemStateCanceled
				}
			}
		}
	})
	return m.snapshot(id)
}

func (m *jobManager) list(limit int) []jobRecord {
	m.mu.Lock()
	ids := make([]string, 0, len(m.jobs))
	for id := range m.jobs {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	result := make([]jobRecord, 0, limit)
	for _, id := range ids {
		if len(result) >= limit {
			break
		}
		if snapshot, ok := m.snapshot(id); ok {
			// 列表只需要摘要，不返回逐项产物。
			snapshot.Items = nil
			result = append(result, snapshot)
		}
	}
	return result
}

func (m *jobManager) runningCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, job := range m.jobs {
		if job.State == jobStateQueued || job.State == jobStateRunning {
			count++
		}
	}
	return count
}

// filenameFor 生成安全且可读的文件名前缀：单元格信息写进名字，便于脱离插件也能辨认。
func filenameFor(base, styleID, size string) string {
	parts := []string{base, styleID, size}
	for index, part := range parts {
		parts[index] = sanitizeFilename(part)
	}
	joined := strings.Join(parts, "-")
	if len([]rune(joined)) > 80 {
		joined = string([]rune(joined)[:80])
	}
	return joined
}

func sanitizeFilename(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "studio"
	}
	var builder strings.Builder
	for _, char := range trimmed {
		switch char {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', ' ', '\t', '\n', '\r':
			builder.WriteRune('-')
		default:
			builder.WriteRune(char)
		}
	}
	result := strings.Trim(builder.String(), "-")
	if result == "" {
		return "studio"
	}
	return result
}

// defaultOutputDir 让产物默认落在工作区的 .himind/media 下，与内核生图的默认一致。
func defaultOutputDir(workspaceRoot string) string {
	if strings.TrimSpace(workspaceRoot) == "" {
		return ""
	}
	return filepath.Join(workspaceRoot, ".himind", "media")
}

func timestampSuffix() string {
	return time.Now().UTC().Format("20060102T150405")
}
