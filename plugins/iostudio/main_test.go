package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"himind-plugin/iostudio/internal/himindjsonrpc"
	studio "himind-plugin/iostudio/internal/iostudio"
)

func call(t *testing.T, service *service, method string, params any) (any, *himindjsonrpc.Error) {
	t.Helper()
	payload, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return service.handle(himindjsonrpc.Request{Method: method, Params: payload})
}

// 未找到 CLI 时必须给出可操作的指引，而不是一句"失败"。
func TestRuntimeStatusWithoutCLIExplainsHowToFix(t *testing.T) {
	service := &service{
		loaded: true,
		cliErr: studio.ErrCLINotFound,
	}
	result, rpcError := call(t, service, capRuntimeStatus, map[string]any{})
	if rpcError != nil {
		t.Fatal(rpcError)
	}
	payload := result.(map[string]any)
	if payload["available"] != false {
		t.Fatalf("CLI 缺失时应报告不可用: %#v", payload)
	}
	message, _ := payload["message"].(string)
	if !strings.Contains(message, "cli_path") {
		t.Fatalf("提示应说明如何指定 CLI: %s", message)
	}
}

func TestUnknownMethodIsRejected(t *testing.T) {
	_, rpcError := call(t, &service{}, "iostudio.nope", map[string]any{})
	if rpcError == nil || rpcError.Code != -32601 {
		t.Fatalf("未知能力应返回 -32601: %#v", rpcError)
	}
}

func TestBuildRequiresWorkspaceRoot(t *testing.T) {
	service := &service{loaded: true, cli: studio.CLI{Path: "x"}}
	_, rpcError := call(t, service, capMotionBuild, map[string]any{"spec_path": "a.spec.json"})
	if rpcError == nil {
		t.Fatal("缺少 workspace_root 应被拒绝")
	}
	if !strings.Contains(rpcError.Message, "workspace_root") {
		t.Fatalf("错误信息应指明缺少什么: %s", rpcError.Message)
	}
}

// 工作区约束在能力层统一执行。
func TestBuildRejectsPathsOutsideWorkspace(t *testing.T) {
	service := &service{loaded: true, cli: studio.CLI{Path: "x"}}
	root := t.TempDir()

	_, rpcError := call(t, service, capMotionBuild, map[string]any{
		"workspace_root": root,
		"spec_path":      filepath.Join("..", "outside.spec.json"),
	})
	if rpcError == nil {
		t.Fatal("越界的 spec_path 应被拒绝")
	}
}

func TestBuildRejectsMissingSpec(t *testing.T) {
	service := &service{loaded: true, cli: studio.CLI{Path: "x"}}
	root := t.TempDir()
	_, rpcError := call(t, service, capMotionBuild, map[string]any{
		"workspace_root": root,
		"spec_path":      "absent.spec.json",
	})
	if rpcError == nil {
		t.Fatal("规格文件不存在应被拒绝")
	}
	if !strings.Contains(rpcError.Message, "spec_path") {
		t.Fatalf("错误信息应指明字段: %s", rpcError.Message)
	}
}

func TestDevicesReportsMissingTableClearly(t *testing.T) {
	dir := t.TempDir()
	service := &service{loaded: true, cli: studio.CLI{
		Path:      filepath.Join(dir, "IOStudio.exe"),
		ConfigDir: filepath.Join(dir, "Config"),
	}}
	result, rpcError := call(t, service, capDevices, map[string]any{})
	if rpcError != nil {
		t.Fatal(rpcError)
	}
	payload := result.(map[string]any)
	if payload["error"] == nil {
		t.Fatalf("没有设备表时应明确说明: %#v", payload)
	}
}

func TestDevicesFiltersByName(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "Config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	table := `<?xml version="1.0"?><IORoot>
	  <Device Name="A" DllName="X"><OAction Name="Go"><Key Name="OAxis_01"/></OAction></Device>
	  <Device Name="B" DllName="Y"><OAction Name="Stop"><Key Name="OAxis_02"/></OAction></Device>
	</IORoot>`
	if err := os.WriteFile(filepath.Join(configDir, "IODevice.xml"), []byte(table), 0o644); err != nil {
		t.Fatal(err)
	}
	service := &service{loaded: true, cli: studio.CLI{
		Path:        filepath.Join(dir, "IOStudio.exe"),
		ConfigDir:   configDir,
		DeviceTable: "IODevice.xml",
	}}

	result, rpcError := call(t, service, capDevices, map[string]any{"device": "B"})
	if rpcError != nil {
		t.Fatal(rpcError)
	}
	parsed, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(parsed), `"B"`) || strings.Contains(string(parsed), `"A"`) {
		t.Fatalf("按名过滤未生效: %s", parsed)
	}

	// 名字不存在时要给出可用名单，而不是空结果。
	result, rpcError = call(t, service, capDevices, map[string]any{"device": "Nope"})
	if rpcError != nil {
		t.Fatal(rpcError)
	}
	parsed, _ = json.Marshal(result)
	if !strings.Contains(string(parsed), "devices") {
		t.Fatalf("设备名不存在时应回报可用设备名: %s", parsed)
	}
}
