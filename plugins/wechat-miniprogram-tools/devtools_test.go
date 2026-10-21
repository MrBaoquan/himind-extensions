package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MrBaoquan/himind-extensions/sdk/jsonrpc"
)

func writeProjectConfig(t *testing.T, root, appid string) {
	t.Helper()
	config := map[string]any{"appid": appid, "compileType": "miniprogram"}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "project.config.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDevtoolsEnvironmentUsesFakeCLI(t *testing.T) {
	t.Setenv("HIMIND_WECHATIDE_FAKE", "1")
	workspace := t.TempDir()
	result := invoke(t, "wechat.miniprogram.devtools.environment.get", map[string]any{
		"workspace_root": workspace,
	}).(map[string]any)
	if result["ready"] != true {
		t.Fatalf("expected ready environment, got %#v", result)
	}
}

func TestDevtoolsRejectsProjectOutsideWorkspace(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	request, err := jsonrpc.NewRequest(1, "wechat.miniprogram.devtools.window.manage", map[string]any{
		"workspace_root": workspace,
		"project_root":   outside,
		"action":         "open",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, rpcError := handle(request)
	if rpcError == nil {
		t.Fatal("expected project path outside workspace to be rejected")
	}
}

func TestProjectImportRejectsNonHTTPSLink(t *testing.T) {
	workspace := t.TempDir()
	request, err := jsonrpc.NewRequest(1, "wechat.miniprogram.devtools.project.import", map[string]any{
		"workspace_root": workspace,
		"project_root":   workspace,
		"action":         "import_minicode",
		"link":           "http://developers.weixin.qq.com/s/example",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, rpcError := handle(request)
	if rpcError == nil {
		t.Fatal("expected non-HTTPS link to be rejected")
	}
}

func TestCloudWriteRequiresExplicitStructAction(t *testing.T) {
	t.Setenv("HIMIND_WECHATIDE_FAKE", "1")
	workspace := t.TempDir()
	request, err := jsonrpc.NewRequest(1, "wechat.miniprogram.cloud.write", map[string]any{
		"workspace_root":  workspace,
		"app_id":          "wx-test-appid",
		"env":             "dev-1",
		"action":          "db_write_struct",
		"collection_name": "users",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, rpcError := handle(request)
	if rpcError == nil {
		t.Fatal("expected missing struct_action to be rejected")
	}
}
