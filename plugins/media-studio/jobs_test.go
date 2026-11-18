package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeInvoker 记录调用并按预设返回产物或错误，用来把「编排」与「真的花钱生图」解耦。
type fakeInvoker struct {
	mu           sync.Mutex
	calls        []map[string]any
	capabilities []string
	files        int
	err          error
	delay        time.Duration
	hang         chan struct{}
}

func (f *fakeInvoker) Invoke(capability string, input map[string]any) (map[string]any, error) {
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if f.hang != nil {
		<-f.hang
	}
	f.mu.Lock()
	f.calls = append(f.calls, input)
	f.capabilities = append(f.capabilities, capability)
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	count := f.files
	if count <= 0 {
		count = 1
	}
	files := make([]any, 0, count)
	for index := 0; index < count; index++ {
		files = append(files, map[string]any{
			"media_id":  fmt.Sprintf("media_%d", len(f.calls)*10+index),
			"path":      fmt.Sprintf(`C:\work\out-%d-%d.png`, len(f.calls), index),
			"sha256":    fmt.Sprintf("sha-%d", index),
			"width":     float64(1024),
			"height":    float64(1024),
			"mime_type": "image/png",
		})
	}
	return map[string]any{
		"files":      files,
		"service_id": "fake-service",
		"model":      "fake-model",
		"request_id": "req-1",
	}, nil
}

func (f *fakeInvoker) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func newTestService(t *testing.T) *service {
	t.Helper()
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	t.Setenv(brokerURLEnv, "")
	t.Setenv(brokerTokenEnv, "")
	instance := &service{jobs: newJobManager()}
	// 作业是后台 goroutine：测试结束前必须等它收敛，否则它会在下一个测试
	// 换掉数据目录之后才写血缘，把别人的断言弄脏。
	t.Cleanup(func() {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if instance.jobs.runningCount() == 0 {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	})
	return instance
}

func waitForJob(t *testing.T, manager *jobManager, id string) jobRecord {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		record, ok := manager.snapshot(id)
		if ok && record.State != jobStateQueued && record.State != jobStateRunning {
			return record
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("作业没有在超时内结束: %s", id)
	return jobRecord{}
}

func TestBatchStartExpandsMatrixAndWritesResults(t *testing.T) {
	service := newTestService(t)
	workspace := t.TempDir()
	invoker := &fakeInvoker{files: 2}
	service.invoker = invoker

	result, rpcError := service.batchStart(batchStartInput{
		WorkspaceRoot: workspace,
		Prompt:        "展馆大厅",
		Styles:        []string{"clean-product", "night-city"},
		Sizes:         []string{"1024x1024"},
		Variants:      2,
	})
	if rpcError != nil {
		t.Fatalf("启动矩阵失败: %s", rpcError.Message)
	}
	payload := result.(map[string]any)
	jobID := payload["job_id"].(string)
	if payload["images_total"].(int) != 4 {
		t.Fatalf("矩阵张数不对: %+v", payload)
	}

	record := waitForJob(t, service.jobs, jobID)
	if record.State != jobStateSucceeded {
		t.Fatalf("作业应收敛为成功: %+v", record)
	}
	if record.Completed != 2 || record.Failed != 0 {
		t.Fatalf("完成计数不对: %+v", record)
	}
	if invoker.callCount() != 2 {
		t.Fatalf("每个单元格应只调用一次生图（n=变体数）: %d", invoker.callCount())
	}
	for _, item := range record.Items {
		if item.State != itemStateSucceeded || len(item.Files) != 2 {
			t.Fatalf("单元格结果不对: %+v", item)
		}
		if !strings.Contains(item.Prompt, "风格要求：") && item.StyleID != "" {
			t.Fatalf("风格包提示词没有拼进去: %q", item.Prompt)
		}
	}

	// 产物目录默认落在工作区 .himind/media，且目录被创建。
	expectedDir := filepath.Join(workspace, ".himind", "media")
	if record.OutputDir != expectedDir {
		t.Fatalf("默认产物目录不对: %s", record.OutputDir)
	}
	if info, err := os.Stat(expectedDir); err != nil || !info.IsDir() {
		t.Fatalf("产物目录未创建: %v", err)
	}

	records, err := loadLineage()
	if err != nil {
		t.Fatalf("读取血缘失败: %v", err)
	}
	if len(records) != 4 {
		t.Fatalf("每个产物都应写入血缘：期望 4，实际 %d", len(records))
	}
	if records[0].JobID != jobID || records[0].Capability != capImageGenerate {
		t.Fatalf("血缘缺少作业或能力信息: %+v", records[0])
	}
}

func TestBatchRejectsOversizedMatrix(t *testing.T) {
	service := newTestService(t)
	service.invoker = &fakeInvoker{}
	_, rpcError := service.batchStart(batchStartInput{
		WorkspaceRoot: t.TempDir(),
		Prompt:        "x",
		Styles:        []string{"clean-product", "night-city", "icon-tile", "ink-poster", "brand-minimal", "exhibit-keyvisual"},
		Sizes:         []string{"1024x1024", "1280x720"},
		Variants:      4,
	})
	if rpcError == nil || !strings.Contains(rpcError.Message, "超过单次上限") {
		t.Fatalf("超限矩阵应被拒绝: %+v", rpcError)
	}
}

func TestBatchRequiresWorkspaceAndPrompt(t *testing.T) {
	service := newTestService(t)
	service.invoker = &fakeInvoker{}
	if _, rpcError := service.batchStart(batchStartInput{Prompt: "x"}); rpcError == nil {
		t.Fatal("缺少 workspace_root 应被拒绝")
	}
	if _, rpcError := service.batchStart(batchStartInput{WorkspaceRoot: t.TempDir()}); rpcError == nil {
		t.Fatal("缺少 prompt 应被拒绝")
	}
	if _, rpcError := service.batchStart(batchStartInput{
		WorkspaceRoot: t.TempDir(),
		Prompt:        "x",
		Styles:        []string{"not-exists"},
	}); rpcError == nil {
		t.Fatal("未知风格包应被拒绝")
	}
}

func TestBatchFailureIsRecordedPerCell(t *testing.T) {
	service := newTestService(t)
	service.invoker = &fakeInvoker{err: fmt.Errorf("服务不可用")}
	result, rpcError := service.batchStart(batchStartInput{
		WorkspaceRoot: t.TempDir(),
		Prompt:        "x",
		Styles:        []string{"clean-product"},
		Sizes:         []string{"1024x1024"},
	})
	if rpcError != nil {
		t.Fatalf("启动应成功，失败留到作业里: %s", rpcError.Message)
	}
	jobID := result.(map[string]any)["job_id"].(string)
	record := waitForJob(t, service.jobs, jobID)
	if record.State != jobStateFailed || record.Failed != 1 {
		t.Fatalf("失败的单元格应被记录: %+v", record)
	}
	if !strings.Contains(record.Items[0].Error, "服务不可用") {
		t.Fatalf("错误原因没有保留: %+v", record.Items[0])
	}
}

func TestBatchCancelStopsPendingCells(t *testing.T) {
	service := newTestService(t)
	hang := make(chan struct{})
	invoker := &fakeInvoker{delay: 20 * time.Millisecond}
	invoker.hang = hang
	service.invoker = invoker
	result, rpcError := service.batchStart(batchStartInput{
		WorkspaceRoot: t.TempDir(),
		Prompt:        "x",
		Styles:        []string{"clean-product", "night-city", "ink-poster"},
		Sizes:         []string{"1024x1024"},
	})
	if rpcError != nil {
		t.Fatalf("启动失败: %s", rpcError.Message)
	}
	jobID := result.(map[string]any)["job_id"].(string)
	time.Sleep(10 * time.Millisecond)
	if _, rpcError := service.batchCancel(jobCancelInput{JobID: jobID}); rpcError != nil {
		t.Fatalf("取消失败: %s", rpcError.Message)
	}
	close(hang)
	record := waitForJob(t, service.jobs, jobID)
	if record.State != jobStateCanceled {
		t.Fatalf("取消后状态应为 canceled: %s", record.State)
	}
	canceled := 0
	for _, item := range record.Items {
		if item.State == itemStateCanceled {
			canceled++
		}
	}
	if canceled == 0 {
		t.Fatalf("取消后应留下被取消的单元格: %+v", record.Items)
	}
}

func TestJobManagerMarksStaleRunningJobsAsInterrupted(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", root)
	directory := filepath.Join(root, "jobs")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}
	stale := `{"id":"job_stale","state":"running","total":1,"completed":0,"items":[{"index":0,"state":"running"}]}`
	if err := os.WriteFile(filepath.Join(directory, "job_stale.json"), []byte(stale), 0o644); err != nil {
		t.Fatalf("写入作业失败: %v", err)
	}
	manager := newJobManager()
	record, ok := manager.snapshot("job_stale")
	if !ok {
		t.Fatal("遗留作业应被载入")
	}
	if record.State != jobStateInterrupted {
		t.Fatalf("运行中的遗留作业应改判为中断: %s", record.State)
	}
	if record.Items[0].State != itemStateCanceled {
		t.Fatalf("遗留作业的项应改判为取消: %s", record.Items[0].State)
	}
}

func TestBatchWaitReturnsSyncResultForWorkflows(t *testing.T) {
	service := newTestService(t)
	service.invoker = &fakeInvoker{files: 1}
	result, rpcError := service.batchStart(batchStartInput{
		WorkspaceRoot: t.TempDir(),
		Prompt:        "展馆入口",
		Styles:        []string{"clean-product"},
		Sizes:         []string{"1024x1024"},
		Wait:          true,
		WaitSeconds:   10,
	})
	if rpcError != nil {
		t.Fatalf("同步模式启动失败: %s", rpcError.Message)
	}
	payload := result.(map[string]any)
	if payload["state"] != jobStateSucceeded {
		t.Fatalf("同步模式应直接返回终态: %+v", payload)
	}
	if payload["file_count"] != 1 {
		t.Fatalf("同步模式应汇总产物: %+v", payload)
	}
	if _, ok := payload["job"].(jobRecord); !ok {
		t.Fatalf("同步模式应带回完整作业: %T", payload["job"])
	}
}

func TestBatchWithoutWaitReturnsImmediately(t *testing.T) {
	service := newTestService(t)
	service.invoker = &fakeInvoker{files: 1, delay: 30 * time.Millisecond}
	result, rpcError := service.batchStart(batchStartInput{
		WorkspaceRoot: t.TempDir(),
		Prompt:        "x",
		Styles:        []string{"clean-product"},
		Sizes:         []string{"1024x1024"},
	})
	if rpcError != nil {
		t.Fatalf("启动失败: %s", rpcError.Message)
	}
	payload := result.(map[string]any)
	if _, exists := payload["files"]; exists {
		t.Fatalf("非同步模式不应等待产物: %+v", payload)
	}
	if payload["state"] == jobStateSucceeded {
		t.Fatalf("刚启动时不该已经完成: %+v", payload)
	}
}

func TestFilenameForSanitizesAndKeepsCellInfo(t *testing.T) {
	got := filenameFor("wechat cover/2026", "clean-product", "1024x1024")
	if strings.ContainsAny(got, `/\:*?"<>| `) {
		t.Fatalf("文件名仍含非法字符: %q", got)
	}
	if !strings.Contains(got, "clean-product") || !strings.Contains(got, "1024x1024") {
		t.Fatalf("文件名应保留单元格信息: %q", got)
	}
}
