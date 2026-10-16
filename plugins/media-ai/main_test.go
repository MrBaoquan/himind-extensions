package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"himind-plugin/media-ai/internal/himindjsonrpc"
	"himind-plugin/media-ai/internal/media/runtime"
)

func call(t *testing.T, service *service, method string, params any) (any, *himindjsonrpc.Error) {
	t.Helper()
	payload, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return service.handle(himindjsonrpc.Request{Method: method, Params: payload})
}

func TestUnknownMethodIsRejected(t *testing.T) {
	_, rpcError := call(t, &service{}, "media.ai.nope", map[string]any{})
	if rpcError == nil || rpcError.Code != -32601 {
		t.Fatalf("未知能力应返回 -32601: %#v", rpcError)
	}
}

// 状态要如实说明缺什么，并给出可操作的下一步（而不是只说"不可用"）。
func TestRuntimeStatusExplainsWhatIsMissing(t *testing.T) {
	result, rpcError := call(t, &service{}, capRuntimeStatus, map[string]any{})
	if rpcError != nil {
		t.Fatal(rpcError)
	}
	payload := result.(map[string]any)
	for _, key := range []string{"runtime", "release", "binary_installed", "installed_models", "ready"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("状态缺少 %s: %#v", key, payload)
		}
	}
	if payload["ready"] == true {
		t.Skip("本机运行时已就绪，跳过缺失提示的断言")
	}
	missing, ok := payload["missing"].([]string)
	if !ok || len(missing) == 0 {
		t.Fatalf("未就绪时应列出缺什么: %#v", payload)
	}
	message, _ := payload["message"].(string)
	if !strings.Contains(message, "runtime.install") && !strings.Contains(message, "ffmpeg") {
		t.Fatalf("提示应给出可操作的补救方式: %s", message)
	}
}

func TestRuntimeStatusReportsLicense(t *testing.T) {
	result, rpcError := call(t, &service{}, capRuntimeStatus, map[string]any{})
	if rpcError != nil {
		t.Fatal(rpcError)
	}
	payload := result.(map[string]any)
	// 外部产物是独立许可的第三方组件，许可信息必须随状态一起可见。
	if _, ok := payload["license"]; !ok {
		t.Fatalf("状态应带出许可信息: %#v", payload)
	}
	if _, ok := payload["license_note"]; !ok {
		t.Fatalf("状态应带出许可说明: %#v", payload)
	}
}

func TestModelsListsInstallState(t *testing.T) {
	result, rpcError := call(t, &service{}, capModels, map[string]any{})
	if rpcError != nil {
		t.Fatal(rpcError)
	}
	payload := result.(map[string]any)
	items, ok := payload["models"].([]map[string]any)
	if !ok || len(items) == 0 {
		t.Fatalf("应列出可选模型: %#v", payload)
	}
	for _, item := range items {
		for _, key := range []string{"id", "size_mb", "installed", "note"} {
			if _, ok := item[key]; !ok {
				t.Errorf("模型项缺少 %s: %#v", key, item)
			}
		}
	}
}

func TestTranscribeRequiresWorkspaceRoot(t *testing.T) {
	_, rpcError := call(t, &service{}, capTranscribe, map[string]any{"path": "a.mp4"})
	if rpcError == nil {
		t.Fatal("缺少 workspace_root 应被拒绝")
	}
	if !strings.Contains(rpcError.Message, "workspace_root") {
		t.Fatalf("错误信息应指明缺少什么: %s", rpcError.Message)
	}
}

func TestTranscribeRejectsPathOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	_, rpcError := call(t, &service{}, capTranscribe, map[string]any{
		"workspace_root": root,
		"path":           filepath.Join("..", "outside.mp4"),
	})
	if rpcError == nil {
		t.Fatal("越界路径应被拒绝")
	}
}

func TestTranscribeRejectsMissingFile(t *testing.T) {
	root := t.TempDir()
	_, rpcError := call(t, &service{}, capTranscribe, map[string]any{
		"workspace_root": root,
		"path":           "absent.mp4",
	})
	if rpcError == nil {
		t.Fatal("文件不存在应被拒绝")
	}
	if !strings.Contains(rpcError.Message, "path") {
		t.Fatalf("错误信息应指明字段: %s", rpcError.Message)
	}
}

// 未知模型要给出可用清单，而不是含糊地失败。
func TestTranscribeRejectsUnknownModel(t *testing.T) {
	root := t.TempDir()
	sample := filepath.Join(root, "a.mp4")
	if err := os.WriteFile(sample, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, rpcError := call(t, &service{}, capTranscribe, map[string]any{
		"workspace_root": root,
		"path":           "a.mp4",
		"model":          "不存在",
	})
	if rpcError == nil {
		t.Fatal("未知模型应被拒绝")
	}
	lock, err := runtime.ReadLock(runtime.EmbeddedLock())
	if err != nil {
		t.Fatal(err)
	}
	// 提示里应带上真实可用的模型 id
	if !strings.Contains(rpcError.Message, lock.Models[0].ID) {
		t.Fatalf("错误信息应列出可用模型: %s", rpcError.Message)
	}
}

func TestJobStatusRequiresJobID(t *testing.T) {
	_, rpcError := call(t, &service{}, capJobStatus, map[string]any{})
	if rpcError == nil {
		t.Fatal("缺少 job_id 应被拒绝")
	}
}

func TestJobCancelRequiresJobID(t *testing.T) {
	_, rpcError := call(t, &service{}, capJobCancel, map[string]any{})
	if rpcError == nil {
		t.Fatal("缺少 job_id 应被拒绝")
	}
}

func TestJobStatusUnknownID(t *testing.T) {
	_, rpcError := call(t, &service{}, capJobStatus, map[string]any{"job_id": "deadbeefdeadbeef"})
	if rpcError == nil {
		t.Fatal("不存在的作业应报错")
	}
}
