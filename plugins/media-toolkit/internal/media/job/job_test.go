package job

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"himind-plugin/media-toolkit/internal/media/ffmpeg"
	"himind-plugin/media-toolkit/internal/media/plugindata"
)

func TestLoadExistingMarksRunningAsInterruptedAndCleansTemp(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	root, err := plugindata.Sub("jobs")
	if err != nil {
		t.Fatal(err)
	}
	id := "0123456789abcdef"
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	tempPath := filepath.Join(t.TempDir(), "partial.mp4")
	if err := os.WriteFile(tempPath, []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	record := Record{
		ID: id, Kind: "video.convert", Status: StatusRunning, Output: filepath.Join(t.TempDir(), "out.mp4"),
		TempOutput: tempPath, CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	payload, _ := json.Marshal(record)
	if err := os.WriteFile(filepath.Join(dir, "job.json"), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(ffmpeg.Tools{})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != StatusInterrupted || loaded.TempOutput != "" {
		t.Fatalf("中断作业未正确收敛: %+v", loaded)
	}
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Fatalf("中断作业的临时文件应被清理: %v", err)
	}
}

func waitJob(t *testing.T, store *Store, id string) *Record {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		record, err := store.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if record.Status == StatusCompleted || record.Status == StatusFailed || record.Status == StatusCancelled {
			return record
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("等待作业结束超时")
	return nil
}

func TestFailedJobLeavesNoPartialOutput(t *testing.T) {
	t.Setenv(InlineWorkerEnv, "1")
	tools, err := ffmpeg.Locate()
	if err != nil {
		t.Skipf("本机没有成对 ffmpeg/ffprobe: %v", err)
	}
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	store, err := NewStore(tools)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	output := filepath.Join(dir, "failed.mp4")
	record, err := store.Submit("test.failure", "missing.mp4", output, "failure test", []string{
		"-i", filepath.Join(dir, "missing.mp4"), output,
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	finished := waitJob(t, store, record.ID)
	if finished.Status != StatusFailed {
		t.Fatalf("无效输入应失败: %+v", finished)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("失败作业不应留下最终产物: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, ".*.part-*"))
	if len(matches) != 0 {
		t.Fatalf("失败作业不应留下临时文件: %#v", matches)
	}
}

func TestSuccessfulJobAtomicallyPublishesOutput(t *testing.T) {
	t.Setenv(InlineWorkerEnv, "1")
	tools, err := ffmpeg.Locate()
	if err != nil {
		t.Skipf("本机没有成对 ffmpeg/ffprobe: %v", err)
	}
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	store, err := NewStore(tools)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	output := filepath.Join(dir, "ok.mp4")
	args := []string{
		"-f", "lavfi", "-i", "color=c=red:s=32x32:d=1", "-c:v", "libx264", "-pix_fmt", "yuv420p", output,
	}
	record, err := store.Submit("test.success", "lavfi", output, "success test", args, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if record.Args[len(record.Args)-1] != output {
		t.Fatalf("公开作业参数应保留最终输出路径: %#v", record.Args)
	}
	if len(record.RunArgs) == 0 || record.RunArgs[len(record.RunArgs)-1] == output || !strings.Contains(record.RunArgs[len(record.RunArgs)-1], ".part-") {
		t.Fatalf("内部执行参数应使用临时输出: %#v", record.RunArgs)
	}
	finished := waitJob(t, store, record.ID)
	if finished.Status != StatusCompleted {
		t.Fatalf("合成视频应成功: %+v", finished)
	}
	info, err := os.Stat(output)
	if err != nil || info.Size() == 0 {
		t.Fatalf("最终产物缺失: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".part-") {
			t.Fatalf("成功作业不应保留临时文件: %s", entry.Name())
		}
	}
}

func TestLoadExistingKeepsJobWithFreshWorkerHeartbeat(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	root, err := plugindata.Sub("jobs")
	if err != nil {
		t.Fatal(err)
	}
	id := "fedcba9876543210"
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	record := Record{ID: id, Kind: "video.convert", Status: StatusRunning, WorkerPID: os.Getpid(), Heartbeat: time.Now().UTC().Format(time.RFC3339)}
	payload, _ := json.Marshal(record)
	if err := os.WriteFile(filepath.Join(dir, "job.json"), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(ffmpeg.Tools{})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != StatusRunning {
		t.Fatalf("心跳新鲜时不应误判中断: %+v", loaded)
	}
}

func TestCancelWritesCrossProcessMarker(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	t.Setenv(InlineWorkerEnv, "1")
	store, err := NewStore(ffmpeg.Tools{})
	if err != nil {
		t.Fatal(err)
	}
	id := "0011223344556677"
	store.jobs[id] = &Record{ID: id, Status: StatusRunning}
	if err := store.Cancel(id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cancelPath(store.root, id)); err != nil {
		t.Fatalf("取消标记未写入: %v", err)
	}
}

func TestRunWorkerExecutesPersistedJob(t *testing.T) {
	tools, err := ffmpeg.Locate()
	if err != nil {
		t.Skipf("本机没有成对 ffmpeg/ffprobe: %v", err)
	}
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	root, err := plugindata.Sub("jobs")
	if err != nil {
		t.Fatal(err)
	}
	id := "abcdefabcdefabcd"
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	outputDir := t.TempDir()
	source := filepath.Join(outputDir, "source.mp4")
	if _, err := tools.Run(context.Background(), ffmpeg.RunOptions{Args: []string{
		"-f", "lavfi", "-i", "testsrc=duration=1:size=160x90:rate=30",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", source,
	}}); err != nil {
		t.Fatalf("合成 worker 测试素材失败: %v", err)
	}
	output := filepath.Join(outputDir, "worker.mp4")
	tempOutput := filepath.Join(outputDir, ".worker.part.mp4")
	logPath := filepath.Join(outputDir, "worker.log")
	record := Record{
		ID: id, Kind: "test.worker", Status: StatusQueued,
		Output: output, TempOutput: tempOutput, LogPath: logPath,
		Args:      []string{"-i", source, "-vf", "scale=80:-2", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", output},
		Heartbeat: time.Now().UTC().Format(time.RFC3339),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	payload, _ := json.Marshal(record)
	if err := os.WriteFile(filepath.Join(dir, "job.json"), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RunWorker(dir); err != nil {
		t.Fatal(err)
	}
	finishedPayload, err := os.ReadFile(filepath.Join(dir, "job.json"))
	if err != nil {
		t.Fatal(err)
	}
	var finished Record
	if err := json.Unmarshal(finishedPayload, &finished); err != nil {
		t.Fatal(err)
	}
	if finished.Status != StatusCompleted {
		t.Fatalf("worker 未完成作业: %+v", finished)
	}
	if info, err := os.Stat(output); err != nil || info.Size() == 0 {
		t.Fatalf("worker 产物缺失: %v", err)
	}
}
