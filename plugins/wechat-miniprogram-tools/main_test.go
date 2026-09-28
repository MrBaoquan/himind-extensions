package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MrBaoquan/himind-extensions/sdk/jsonrpc"
)

func invoke(t *testing.T, method string, params any) any {
	t.Helper()
	request, err := jsonrpc.NewRequest(1, method, params)
	if err != nil {
		t.Fatal(err)
	}
	result, rpcError := handle(request)
	if rpcError != nil {
		t.Fatalf("%s failed: %s", method, rpcError.Message)
	}
	return result
}

func TestProjectInspectReadsRealProjectConfig(t *testing.T) {
	workspace := t.TempDir()
	projectRoot := filepath.Join(workspace, "miniprogram")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	config := map[string]any{
		"appid":           "wx-test-appid",
		"miniprogramRoot": "src/",
		"compileType":     "miniprogram",
	}
	data, _ := json.Marshal(config)
	if err := os.WriteFile(filepath.Join(projectRoot, "project.config.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	result := invoke(t, "wechat.miniprogram.project.inspect", map[string]any{
		"workspace_root": workspace,
		"project_root":   projectRoot,
	}).(map[string]any)
	if result["ok"] != true || result["app_id"] != "wx-test-appid" {
		t.Fatalf("unexpected inspect result: %#v", result)
	}
}

func TestResolvePathsSeparatesSourceAndBuiltMiniProgramRoot(t *testing.T) {
	workspace := t.TempDir()
	sourceRoot := filepath.Join(workspace, "source")
	projectRoot := filepath.Join(workspace, "dist", "wx")
	if err := os.MkdirAll(sourceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	resolvedProject, resolvedSource, artifactDir, err := resolvePathsWithSource(input{
		WorkspaceRoot: workspace,
		SourceRoot:    sourceRoot,
		ProjectRoot:   projectRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolvedSource != sourceRoot || resolvedProject != projectRoot {
		t.Fatalf("resolved source/project = %s / %s", resolvedSource, resolvedProject)
	}
	if artifactDir != filepath.Join(sourceRoot, defaultArtifactDir) {
		t.Fatalf("artifact dir = %s", artifactDir)
	}
}

func TestResolveBuildTargetDefaultsEnvironmentAndRejectsInvalidValues(t *testing.T) {
	venue, environment, err := resolveBuildTarget("szkjg", "")
	if err != nil {
		t.Fatal(err)
	}
	if venue != "szkjg" || environment != "development" {
		t.Fatalf("unexpected target: %s / %s", venue, environment)
	}
	if _, _, err := resolveBuildTarget("../szkjg", "production"); err == nil {
		t.Fatal("expected invalid venue to be rejected")
	}
	if _, _, err := resolveBuildTarget("szkjg", "staging"); err == nil {
		t.Fatal("expected invalid environment to be rejected")
	}
}

func TestUploadVersionAndDescriptionFallBackToDateDefaults(t *testing.T) {
	// 显式填的版本号与说明必须原样透传，不能被默认值覆盖。
	if version := resolveUploadVersion("1.26.0917"); version != "1.26.0917" {
		t.Fatalf("explicit version = %s", version)
	}
	if description := resolveUploadDescription("修复展馆预约白屏", "1.26.0917"); description != "修复展馆预约白屏" {
		t.Fatalf("explicit description = %s", description)
	}

	// 留空时按 1.YY.MMDD 生成，与仓库脚本 scripts/wx-cli.js 的 getDateVersion 一致。
	now := time.Now()
	want := fmt.Sprintf("1.%02d.%02d%02d", now.Year()%100, int(now.Month()), now.Day())
	if version := resolveUploadVersion("  "); version != want {
		t.Fatalf("generated version = %s, want %s", version, want)
	}
	generated := resolveUploadDescription("", want)
	if !strings.HasPrefix(generated, "v"+want+" ") {
		t.Fatalf("generated description = %s", generated)
	}
	if len(generated) != len("v"+want+" ")+16 {
		t.Fatalf("generated description timestamp = %s", generated)
	}
}

func TestDefaultProjectRootPrefersBuiltMiniProgramDirectory(t *testing.T) {
	sourceRoot := t.TempDir()
	builtRoot := filepath.Join(sourceRoot, "dist", "wx")
	if err := os.MkdirAll(builtRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if resolved := defaultProjectRoot(sourceRoot, ""); resolved != builtRoot {
		t.Fatalf("default project root = %s, want %s", resolved, builtRoot)
	}
	explicit := filepath.Join(sourceRoot, "custom-output")
	if resolved := defaultProjectRoot(sourceRoot, explicit); resolved != explicit {
		t.Fatalf("explicit project root = %s, want %s", resolved, explicit)
	}
}

// 上传通道是实现细节：留空时按「本机有没有可读的上传私钥」自动决定，
// 显式选择则一律照做。用户不该先做一道选择题才知道能不能无人值守上传。
func TestResolveUploadChannelInfersFromPrivateKeyUnlessExplicit(t *testing.T) {
	workspace := t.TempDir()
	privateKeyPath := filepath.Join(workspace, "upload.key")
	if err := os.WriteFile(privateKeyPath, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}

	channel, source, resolved, err := resolveUploadChannel(input{PrivateKeyPath: privateKeyPath}, workspace)
	if err != nil || channel != "ci" || source != "auto_private_key" || resolved != privateKeyPath {
		t.Fatalf("有私钥时应自动走 CI: channel=%s source=%s key=%s err=%v", channel, source, resolved, err)
	}

	channel, source, resolved, err = resolveUploadChannel(input{UploadChannel: "AUTO", PrivateKeyPath: privateKeyPath}, workspace)
	if err != nil || channel != "ci" || source != "auto_private_key" || resolved != privateKeyPath {
		t.Fatalf("auto 与留空等价: channel=%s source=%s key=%s err=%v", channel, source, resolved, err)
	}

	// 没有私钥（或私钥路径读不到）时退回开发者工具通道，并如实说明原因。
	channel, source, resolved, err = resolveUploadChannel(input{PrivateKeyPath: "missing.key"}, t.TempDir())
	if err != nil || channel != "wechatide" || source != "auto_without_private_key" || resolved != "" {
		t.Fatalf("没有私钥时应退回开发者工具: channel=%s source=%s key=%s err=%v", channel, source, resolved, err)
	}

	// 显式选择优先于自动推断：选了 wechatide 就不管本机有没有私钥。
	channel, source, _, err = resolveUploadChannel(input{UploadChannel: "wechatide", PrivateKeyPath: privateKeyPath}, workspace)
	if err != nil || channel != "wechatide" || source != "explicit" {
		t.Fatalf("显式 wechatide 应被尊重: channel=%s source=%s err=%v", channel, source, err)
	}
	channel, source, resolved, err = resolveUploadChannel(input{UploadChannel: "ci", PrivateKeyPath: privateKeyPath}, workspace)
	if err != nil || channel != "ci" || source != "explicit" || resolved != privateKeyPath {
		t.Fatalf("显式 ci 应被尊重: channel=%s source=%s key=%s err=%v", channel, source, resolved, err)
	}

	if _, _, _, err := resolveUploadChannel(input{UploadChannel: "ftp"}, workspace); err == nil {
		t.Fatal("expected invalid upload channel")
	}
}

func TestClassifyWechatidePayloads(t *testing.T) {
	status, taskID, _ := classifyWechatidePayload(map[string]any{
		"ok": true,
		"result": map[string]any{
			"success": true,
			"status":  "pending",
			"taskId":  "task-1",
		},
	})
	// wechatide 的 upload 先返回 success=true + pending：这是「已受理、等确认」，
	// 不是上传成功，必须继续轮询确认结果。
	if status != "pending" || taskID != "task-1" {
		t.Fatalf("pending result = %s / %s", status, taskID)
	}
	status, taskID, _ = classifyWechatidePayload(map[string]any{
		"ok": true,
		"result": map[string]any{
			"success": false,
			"status":  "pending",
			"taskId":  "task-2",
		},
	})
	if status != "pending" || taskID != "task-2" {
		t.Fatalf("unconfirmed result = %s / %s", status, taskID)
	}
	status, _, _ = classifyWechatidePayload(map[string]any{
		"ok": true,
		"result": map[string]any{
			"success": true,
		},
	})
	if status != "uploaded" {
		t.Fatalf("success result = %s", status)
	}
	status, _, _ = classifyWechatidePayload(map[string]any{
		"ok": false,
		"result": map[string]any{
			"status":  "failed",
			"message": "denied",
		},
	})
	if status != "failed" {
		t.Fatalf("failed result = %s", status)
	}
}

func TestWechatideUploadPollsPendingTaskUntilSuccess(t *testing.T) {
	root := t.TempDir()
	script := `@echo off
node "%~dp0fake-wechatide.js" %*
`
	if err := os.WriteFile(filepath.Join(root, "wechatide.cmd"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	helper := `const args = process.argv.slice(2);
const tool = args[2];
if (tool === "upload") {
  process.stdout.write(JSON.stringify({ok:true,result:{status:"pending",taskId:"task-1"}}));
} else {
  process.stdout.write(JSON.stringify({ok:true,result:{success:true,status:"success"}}));
}
`
	if err := os.WriteFile(filepath.Join(root, "fake-wechatide.js"), []byte(helper), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WECHAT_DEVTOOLS_HOME", root)

	output, taskID, err := runWechatideUpload(input{
		WechatideClient: "Copilot",
		Version:         "1.0.0",
		Description:     "acceptance",
	}, root, "1.0.0", "acceptance", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if taskID != "task-1" || output["status"] != "uploaded" {
		t.Fatalf("unexpected upload result: task=%s output=%#v", taskID, output)
	}
}

func TestWechatideUploadRetriesTransientMissingTask(t *testing.T) {
	root := t.TempDir()
	script := `@echo off
node "%~dp0fake-wechatide.js" %*
`
	if err := os.WriteFile(filepath.Join(root, "wechatide.cmd"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	helper := `const fs = require("fs");
const path = require("path");
const tool = process.argv.slice(2)[2];
if (tool === "upload") {
  process.stdout.write(JSON.stringify({ok:true,result:{status:"pending",taskId:"task-1"}}));
} else {
  const counter = path.join(process.cwd(), "poll-count");
  let count = 0;
  try { count = Number(fs.readFileSync(counter, "utf8")); } catch {}
  count += 1;
  fs.writeFileSync(counter, String(count));
  if (count < 3) {
    process.stdout.write(JSON.stringify({ok:false,errorType:"MCP_TOOL_ERROR",message:"Task not found"}));
    process.exitCode = 1;
  } else {
    process.stdout.write(JSON.stringify({ok:true,result:{success:true,status:"success"}}));
  }
}
`
	if err := os.WriteFile(filepath.Join(root, "fake-wechatide.js"), []byte(helper), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WECHAT_DEVTOOLS_HOME", root)

	output, taskID, err := runWechatideUpload(input{
		WechatideClient: "Copilot",
		Version:         "1.0.0",
		Description:     "acceptance",
	}, root, "1.0.0", "acceptance", 20*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if taskID != "task-1" || output["status"] != "uploaded" {
		t.Fatalf("unexpected upload result: task=%s output=%#v", taskID, output)
	}
}

// 回归：工作流启动时用户不填版本号与说明，插件必须用解析后的日期版本号
// 调用 wechatide CLI，而不是把空值直接透传（否则 wechatide 返回
// MCP error -32602: upload-version too_small）。
func TestUploadFillsDateVersionForWechatideChannel(t *testing.T) {
	root := t.TempDir()
	script := `@echo off
node "%~dp0fake-wechatide.js" %*
`
	if err := os.WriteFile(filepath.Join(root, "wechatide.cmd"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	argsFile := filepath.Join(root, "upload-args.json")
	helper := fmt.Sprintf(`const fs = require("fs");
fs.writeFileSync(%q, JSON.stringify(process.argv.slice(2)));
process.stdout.write(JSON.stringify({ok:true,result:{success:true,status:"success"}}));
`, argsFile)
	if err := os.WriteFile(filepath.Join(root, "fake-wechatide.js"), []byte(helper), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WECHAT_DEVTOOLS_HOME", root)

	projectRoot := filepath.Join(root, "miniprogram")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(map[string]any{"appid": "wx-test-appid"})
	if err := os.WriteFile(filepath.Join(projectRoot, "project.config.json"), config, 0o644); err != nil {
		t.Fatal(err)
	}

	result := invoke(t, "wechat.miniprogram.upload", map[string]any{
		"workspace_root": projectRoot,
		"upload_channel": "wechatide",
		"venue":          "hdcybwg",
		"environment":    "development",
		"candidate": map[string]any{
			"candidate_id": "cand-1",
			"commit_sha":   "0000000000000000000000000000000000000000",
			"tree_digest":  "digest-1",
		},
	}).(map[string]any)

	version := strings.TrimSpace(asString(result["version"]))
	if !strings.HasPrefix(version, "1.") {
		t.Fatalf("upload 未生成日期版本号: %#v", result)
	}
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("未捕获 wechatide 调用参数: %v", err)
	}
	var captured []string
	if err := json.Unmarshal(raw, &captured); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(captured, " "), "--upload-version "+version) {
		t.Fatalf("上传参数未带上解析后的版本号 %s: %v", version, captured)
	}
	if strings.Contains(strings.Join(captured, " "), "--upload-version  ") {
		t.Fatalf("上传参数版本号为空: %v", captured)
	}
}

// wechatide 上传必须先拿到用户确认才叫成功：确认前不能返回 uploaded。
func TestWechatideUploadWaitsForDevtoolsConfirmation(t *testing.T) {
	root := t.TempDir()
	script := `@echo off
node "%~dp0fake-wechatide.js" %*
`
	if err := os.WriteFile(filepath.Join(root, "wechatide.cmd"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	helper := `const tool = process.argv.slice(2)[2];
if (tool === "upload") {
  process.stdout.write(JSON.stringify({ok:true,result:{success:true,taskId:"confirmation_upload_1",taskType:"confirmation_upload",status:"pending",message:"Waiting for user confirmation."}}));
} else {
  process.stdout.write(JSON.stringify({ok:true,result:{success:true,status:"success",message:"uploaded"}}));
}
`
	if err := os.WriteFile(filepath.Join(root, "fake-wechatide.js"), []byte(helper), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WECHAT_DEVTOOLS_HOME", root)

	output, taskID, err := runWechatideUpload(input{
		WechatideClient: "Copilot",
	}, root, "1.26.0929", "acceptance", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if taskID != "confirmation_upload_1" || output["status"] != "uploaded" {
		t.Fatalf("确认后应视为上传成功: task=%s output=%#v", taskID, output)
	}
}

// 用户始终没在开发者工具里确认时，必须如实报「等待确认」，不能谎报成功。
func TestWechatideUploadReportsUnconfirmedInsteadOfSuccess(t *testing.T) {
	root := t.TempDir()
	script := `@echo off
node "%~dp0fake-wechatide.js" %*
`
	if err := os.WriteFile(filepath.Join(root, "wechatide.cmd"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	helper := `process.stdout.write(JSON.stringify({ok:true,result:{success:true,taskId:"confirmation_upload_2",taskType:"confirmation_upload",status:"pending",message:"Waiting for user confirmation."}}));
`
	if err := os.WriteFile(filepath.Join(root, "fake-wechatide.js"), []byte(helper), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WECHAT_DEVTOOLS_HOME", root)

	output, taskID, err := runWechatideUpload(input{
		WechatideClient: "Copilot",
	}, root, "1.26.0929", "acceptance", time.Second)
	if err == nil {
		t.Fatalf("未确认的上传不得返回成功: task=%s output=%#v", taskID, output)
	}
	if taskID != "confirmation_upload_2" {
		t.Fatalf("应保留待确认任务号: %s", taskID)
	}
	if !strings.Contains(err.Error(), "确认") {
		t.Fatalf("错误信息应指向开发者工具内确认: %v", err)
	}
}

// 未确认的上传不能留下「体验版已上传」的记录：只能写「待确认」，等真正确认后再覆盖。
func TestUnconfirmedUploadWritesPendingEvidenceInsteadOfSuccess(t *testing.T) {
	root := t.TempDir()
	writeFakeWechatide(t, root, `process.stdout.write(JSON.stringify({ok:true,result:{success:true,taskId:"confirmation_upload_ev",taskType:"confirmation_upload",status:"pending",message:"Waiting for user confirmation."}}));`)
	t.Setenv("WECHAT_DEVTOOLS_HOME", root)
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	originalWindow := wechatideConfirmWindow
	wechatideConfirmWindow = 500 * time.Millisecond
	defer func() { wechatideConfirmWindow = originalWindow }()

	projectRoot := filepath.Join(root, "miniprogram")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(map[string]any{"appid": "wx-test-appid"})
	if err := os.WriteFile(filepath.Join(projectRoot, "project.config.json"), config, 0o644); err != nil {
		t.Fatal(err)
	}

	request, err := jsonrpc.NewRequest(1, "wechat.miniprogram.upload", map[string]any{
		"workspace_root":  projectRoot,
		"upload_channel":  "wechatide",
		"venue":           "hdcybwg",
		"environment":     "development",
		"timeout_seconds": 60,
		"candidate": map[string]any{
			"candidate_id": "cand-1",
			"commit_sha":   "0000000000000000000000000000000000000000",
			"tree_digest":  "digest-1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, rpcError := handle(request); rpcError == nil {
		t.Fatal("未确认的上传不得返回成功")
	}

	raw, err := os.ReadFile(filepath.Join(projectRoot, defaultArtifactDir, "experience-version.json"))
	if err != nil {
		t.Fatalf("未确认时也应留下真实状态的体验版记录: %v", err)
	}
	var evidence map[string]any
	if err := json.Unmarshal(raw, &evidence); err != nil {
		t.Fatal(err)
	}
	if evidence["upload_status"] != "pending_confirmation" {
		t.Fatalf("未确认的记录不能写成已上传: %#v", evidence)
	}
	if evidence["wechatide_task_id"] != "confirmation_upload_ev" {
		t.Fatalf("待确认记录应带上待确认任务号: %#v", evidence)
	}
	if evidence["venue"] != "hdcybwg" || evidence["environment"] != "development" {
		t.Fatalf("待确认记录应保留本次上传的目标: %#v", evidence)
	}
}

func writeFakeWechatide(t *testing.T, root, helper string) {
	t.Helper()
	script := "@echo off\nnode \"%~dp0fake-wechatide.js\" %*\n"
	if err := os.WriteFile(filepath.Join(root, "wechatide.cmd"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "fake-wechatide.js"), []byte(helper), 0o755); err != nil {
		t.Fatal(err)
	}
}

func counterValue(t *testing.T, root, name string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		return 0
	}
	value, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("计数器 %s 内容非法: %v", name, err)
	}
	return value
}

const countingWechatideHelper = `const fs = require("fs");
const path = require("path");
function bump(name) {
  const file = path.join(process.cwd(), name);
  let count = 0;
  try { count = Number(fs.readFileSync(file, "utf8")); } catch (error) {}
  fs.writeFileSync(file, String(count + 1));
}
const tool = process.argv.slice(2)[2];
if (tool === "upload") {
  bump("upload-count");
  process.stdout.write(JSON.stringify({ok:true,result:{success:true,taskId:"confirmation_upload_1",taskType:"confirmation_upload",status:"pending",message:"Waiting for user confirmation."}}));
} else {
  bump("poll-count");
  process.stdout.write(JSON.stringify({ok:true,result:{success:true,status:"success",message:"uploaded"}}));
}
`

// 上一轮已发起、还没确认的上传，本轮应当续等旧任务，而不是再弹一个确认框。
func TestWechatideUploadAdoptsPendingConfirmationWithoutResending(t *testing.T) {
	root := t.TempDir()
	writeFakeWechatide(t, root, countingWechatideHelper)
	t.Setenv("WECHAT_DEVTOOLS_HOME", root)
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	savePendingUpload(pendingWechatideUpload{TaskID: "confirmation_upload_1", ProjectRoot: root, Version: "1.26.0929"})

	output, taskID, err := runWechatideUpload(input{WechatideClient: "Copilot"}, root, "1.26.0929", "acceptance", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if taskID != "confirmation_upload_1" || output["status"] != "uploaded" {
		t.Fatalf("续等确认任务应视为上传成功: task=%s output=%#v", taskID, output)
	}
	if count := counterValue(t, root, "upload-count"); count != 0 {
		t.Fatalf("已存在待确认任务时不得重新发起上传，upload 调用次数=%d", count)
	}
	if _, ok := loadPendingUpload(root); ok {
		t.Fatal("确认完成后应清理待确认状态")
	}
}

// 无人确认时重跑：保持待确认状态，如实报等待确认，且不重复发起上传。
func TestWechatideUploadKeepsPendingConfirmationAcrossRuns(t *testing.T) {
	root := t.TempDir()
	helper := `const fs = require("fs");
const path = require("path");
function bump(name) {
  const file = path.join(process.cwd(), name);
  let count = 0;
  try { count = Number(fs.readFileSync(file, "utf8")); } catch (error) {}
  fs.writeFileSync(file, String(count + 1));
}
if (process.argv.slice(2)[2] === "upload") {
  bump("upload-count");
}
process.stdout.write(JSON.stringify({ok:true,result:{success:true,taskId:"confirmation_upload_9",taskType:"confirmation_upload",status:"pending",message:"Waiting for user confirmation."}}));
`
	writeFakeWechatide(t, root, helper)
	t.Setenv("WECHAT_DEVTOOLS_HOME", root)
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())

	_, firstTaskID, firstErr := runWechatideUpload(input{WechatideClient: "Copilot"}, root, "1.26.0929", "acceptance", time.Second)
	if firstErr == nil || firstTaskID != "confirmation_upload_9" {
		t.Fatalf("未确认的上传不得返回成功: task=%s err=%v", firstTaskID, firstErr)
	}
	state, ok := loadPendingUpload(root)
	if !ok || state.TaskID != "confirmation_upload_9" {
		t.Fatalf("未确认的上传应留下待确认状态: %#v ok=%v", state, ok)
	}

	_, secondTaskID, secondErr := runWechatideUpload(input{WechatideClient: "Copilot"}, root, "1.26.0929", "acceptance", time.Second)
	if secondErr == nil {
		t.Fatal("仍处于等待确认时不得返回成功")
	}
	if secondTaskID != "confirmation_upload_9" || !strings.Contains(secondErr.Error(), "确认") {
		t.Fatalf("重跑应续等同一个确认任务: task=%s err=%v", secondTaskID, secondErr)
	}
	if count := counterValue(t, root, "upload-count"); count != 1 {
		t.Fatalf("重跑不得重复发起上传，upload 调用次数=%d", count)
	}

	// 多项目并行开发时，一个项目的待确认任务不能挤掉另一个项目的。
	other := filepath.Join(t.TempDir(), "another-miniapp")
	savePendingUpload(pendingWechatideUpload{TaskID: "confirmation_upload_other", ProjectRoot: other})
	if _, ok := loadPendingUpload(other); !ok {
		t.Fatal("另一个项目的待确认状态应独立保存")
	}
	if kept, ok := loadPendingUpload(root); !ok || kept.TaskID != "confirmation_upload_9" {
		t.Fatalf("新增其它项目待确认任务后，本项目状态应仍然存在: %#v ok=%v", kept, ok)
	}
	clearPendingUpload(other)
	if _, ok := loadPendingUpload(root); !ok {
		t.Fatal("清理其它项目待确认状态不应影响本项目")
	}
}

// 待确认状态属于某个具体项目：换项目时必须丢弃，不能挡住新一轮上传。
func TestWechatideUploadIgnoresPendingConfirmationFromOtherProject(t *testing.T) {
	root := t.TempDir()
	helper := `const fs = require("fs");
const path = require("path");
function bump(name) {
  const file = path.join(process.cwd(), name);
  let count = 0;
  try { count = Number(fs.readFileSync(file, "utf8")); } catch (error) {}
  fs.writeFileSync(file, String(count + 1));
}
if (process.argv.slice(2)[2] === "upload") {
  bump("upload-count");
}
process.stdout.write(JSON.stringify({ok:true,result:{success:true,status:"success"}}));
`
	writeFakeWechatide(t, root, helper)
	t.Setenv("WECHAT_DEVTOOLS_HOME", root)
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	other := filepath.Join(t.TempDir(), "elsewhere")
	savePendingUpload(pendingWechatideUpload{TaskID: "confirmation_upload_other", ProjectRoot: other})

	output, taskID, err := runWechatideUpload(input{WechatideClient: "Copilot"}, root, "1.26.0929", "acceptance", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if taskID != "" || output["status"] != "uploaded" {
		t.Fatalf("换项目后应正常发起上传: task=%s output=%#v", taskID, output)
	}
	if count := counterValue(t, root, "upload-count"); count != 1 {
		t.Fatalf("换项目后应发起一次上传，upload 调用次数=%d", count)
	}
	if state, ok := loadPendingUpload(other); !ok || state.TaskID != "confirmation_upload_other" {
		t.Fatalf("其它项目的待确认状态不应被本项目的上传清掉: %#v ok=%v", state, ok)
	}
}

func TestPendingUploadStateRoundTrip(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	project := `F:\WebProjects\kerun_user`
	if _, ok := loadPendingUpload(project); ok {
		t.Fatal("初始不应存在待确认状态")
	}
	savePendingUpload(pendingWechatideUpload{TaskID: "confirmation_upload_a", ProjectRoot: project})
	state, ok := loadPendingUpload(project)
	if !ok || state.TaskID != "confirmation_upload_a" || state.CreatedAt == "" {
		t.Fatalf("待确认状态应可读取: %#v ok=%v", state, ok)
	}
	if _, ok := loadPendingUpload(`f:\webprojects\kerun_user\`); !ok {
		t.Fatal("同一项目路径的不同写法应命中同一条待确认状态")
	}
	clearPendingUpload(project)
	if _, ok := loadPendingUpload(project); ok {
		t.Fatal("清理后不应再有待确认状态")
	}

	path, ok := pendingUploadStatePath()
	if !ok {
		t.Fatal("应能定位待确认状态文件")
	}
	if err := os.WriteFile(path, []byte("{断掉的 JSON"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadPendingUpload(project); ok {
		t.Fatal("损坏的状态文件应被忽略，而不是中断上传")
	}
}

func TestPendingUploadStateWithoutDataRoot(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", "")
	project := `F:\WebProjects\kerun_user`
	savePendingUpload(pendingWechatideUpload{TaskID: "confirmation_upload_b", ProjectRoot: project})
	if _, ok := loadPendingUpload(project); ok {
		t.Fatal("没有插件数据目录时不应报告待确认状态")
	}
}

// 同一个工程可能被写成源码根，也可能被写成默认产物目录（project_root 留空时就是
// 源码根\dist\wx）。两种写法必须命中同一条待确认状态，否则升级或换入口后会重复向
// 开发者工具发起上传，重复弹确认框。
func TestPendingUploadMatchesSourceRootAndBuiltOutput(t *testing.T) {
	sourceRoot := `F:\WebProjects\kerun_user`
	builtRoot := `F:\WebProjects\kerun_user\dist\wx`

	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	savePendingUpload(pendingWechatideUpload{TaskID: "confirmation_upload_dist", ProjectRoot: builtRoot})
	state, ok := loadPendingUpload(sourceRoot)
	if !ok || state.TaskID != "confirmation_upload_dist" {
		t.Fatalf("按源码根应能续等产物目录发起的待确认任务: %#v ok=%v", state, ok)
	}

	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	savePendingUpload(pendingWechatideUpload{TaskID: "confirmation_upload_src", ProjectRoot: sourceRoot})
	if state, ok := loadPendingUpload(builtRoot); !ok || state.TaskID != "confirmation_upload_src" {
		t.Fatalf("按产物目录应能续等源码根发起的待确认任务: %#v ok=%v", state, ok)
	}

	// 同一工程换一种写法继续发起时，只应保留一条记录，不能留下互相打架的旧键。
	savePendingUpload(pendingWechatideUpload{TaskID: "confirmation_upload_src2", ProjectRoot: builtRoot})
	if store := loadPendingUploadStore(); len(store.Pending) != 1 {
		t.Fatalf("同一工程只应保留一条待确认状态: %#v", store.Pending)
	}
	clearPendingUpload(builtRoot)
	if _, ok := loadPendingUpload(sourceRoot); ok {
		t.Fatal("按产物目录清理后，按源码根也不应再读到待确认状态")
	}
}

func TestSameWorkspacePath(t *testing.T) {
	if !sameWorkspacePath(`F:\WebProjects\kerun_user`, `f:\webprojects\kerun_user\`) {
		t.Fatal("Windows 路径比较应忽略大小写与尾部分隔符")
	}
	if sameWorkspacePath(`F:\WebProjects\kerun_user`, `F:\WebProjects\kerun_user2`) {
		t.Fatal("不同目录不能视为同一工作区")
	}
}

func TestWechatideUploadOmitsEmptyDescription(t *testing.T) {
	root := t.TempDir()
	script := `@echo off
node "%~dp0fake-wechatide.js" %*
`
	if err := os.WriteFile(filepath.Join(root, "wechatide.cmd"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	helper := `const args = process.argv.slice(2);
if (args.includes("--desc")) {
  process.stdout.write(JSON.stringify({ok:true,result:"unexpected empty description"}));
} else {
  process.stdout.write(JSON.stringify({ok:true,result:{success:true,status:"success"}}));
}
`
	if err := os.WriteFile(filepath.Join(root, "fake-wechatide.js"), []byte(helper), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WECHAT_DEVTOOLS_HOME", root)

	output, taskID, err := runWechatideUpload(input{
		WechatideClient: "Copilot",
		Version:         "1.0.0",
		Description:     "   ",
	}, root, "1.0.0", "   ", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if taskID != "" || output["status"] != "uploaded" {
		t.Fatalf("unexpected upload result: task=%s output=%#v", taskID, output)
	}
}

func TestWechatideClassificationPreservesStringError(t *testing.T) {
	status, taskID, message := classifyWechatidePayload(map[string]any{
		"ok":     true,
		"result": "MCP error -32602: Input validation error",
	})
	if status != "failed" || taskID != "" {
		t.Fatalf("unexpected classification: status=%s task=%s", status, taskID)
	}
	if !strings.Contains(message, "Input validation error") {
		t.Fatalf("string error message was lost: %q", message)
	}

	status, taskID, message = classifyWechatidePayload(map[string]any{
		"ok": true,
		"result": map[string]any{
			"taskId": "task-cancelled",
			"status": "cancelled",
		},
	})
	if status != "failed" || taskID != "task-cancelled" {
		t.Fatalf("cancelled task must be terminal: status=%s task=%s message=%s", status, taskID, message)
	}
}

func TestCIReadinessRejectsContractDoubleInRealMode(t *testing.T) {
	workspace, projectRoot, privateKeyPath := ciReadinessFixture(t, "0.0.0-local")
	t.Setenv("HIMIND_WECHAT_REQUIRE_REAL_CI", "1")
	request, err := jsonrpc.NewRequest(1, "wechat.miniprogram.ci.readiness", map[string]any{
		"workspace_root":   workspace,
		"project_root":     projectRoot,
		"app_id":           "wx-test-appid",
		"private_key_path": privateKeyPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, rpcError := handle(request)
	if rpcError == nil {
		t.Fatal("expected Contract Double to be rejected")
	}
}

func TestCIReadinessAcceptsInstalledCIPackage(t *testing.T) {
	workspace, projectRoot, privateKeyPath := ciReadinessFixture(t, "2.0.0")
	result := invoke(t, "wechat.miniprogram.ci.readiness", map[string]any{
		"workspace_root":   workspace,
		"project_root":     projectRoot,
		"private_key_path": privateKeyPath,
	}).(map[string]any)
	if result["ok"] != true || result["app_id"] != "wx-test-appid" || result["miniprogram_ci_version"] != "2.0.0" {
		t.Fatalf("unexpected readiness result: %#v", result)
	}
}

// 配好上传私钥之后，readiness 自己就应报告走 CI 直传，让 UI 与工作流说明有据可依。
func TestCIReadinessReportsInferredUploadChannel(t *testing.T) {
	workspace, projectRoot, privateKeyPath := ciReadinessFixture(t, "2.0.0")
	result := invoke(t, "wechat.miniprogram.ci.readiness", map[string]any{
		"workspace_root":   workspace,
		"project_root":     projectRoot,
		"private_key_path": privateKeyPath,
	}).(map[string]any)
	if result["upload_channel"] != "ci" || result["upload_channel_source"] != "auto_private_key" {
		t.Fatalf("readiness 应自动推断为 CI 直传: %#v", result)
	}
}

func TestCIReadinessCanDeferCIInstallationForDoctor(t *testing.T) {
	workspace, projectRoot, privateKeyPath := ciReadinessFixture(t, "")
	result := invoke(t, "wechat.miniprogram.ci.readiness", map[string]any{
		"workspace_root":       workspace,
		"project_root":         projectRoot,
		"app_id":               "wx-test-appid",
		"private_key_path":     privateKeyPath,
		"require_ci_installed": false,
	}).(map[string]any)
	if result["ok"] != true || result["ci_installed"] != false || result["ready_for_upload"] != false {
		t.Fatalf("unexpected deferred readiness result: %#v", result)
	}
}

func TestCIReadinessStrictModeRequiresInstalledCI(t *testing.T) {
	workspace, projectRoot, privateKeyPath := ciReadinessFixture(t, "")
	request, err := jsonrpc.NewRequest(1, "wechat.miniprogram.ci.readiness", map[string]any{
		"workspace_root":       workspace,
		"project_root":         projectRoot,
		"app_id":               "wx-test-appid",
		"private_key_path":     privateKeyPath,
		"require_ci_installed": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, rpcError := handle(request)
	if rpcError == nil {
		t.Fatal("expected strict readiness to require an installed CI package")
	}
}

func TestAcceptanceFailureReturnsBusinessResultWithArtifact(t *testing.T) {
	workspace := t.TempDir()
	projectRoot := filepath.Join(workspace, "miniprogram")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	evidencePath := filepath.Join(workspace, "acceptance.png")
	if err := os.WriteFile(evidencePath, []byte("evidence"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := invoke(t, "wechat.miniprogram.acceptance.record", map[string]any{
		"workspace_root": workspace,
		"project_root":   projectRoot,
		"tester":         "test-user",
		"passed":         false,
		"evidence_paths": []string{evidencePath},
		"candidate": map[string]any{
			"candidate_id": "candidate-1",
			"commit_sha":   "0123456789abcdef0123456789abcdef01234567",
			"tree_digest":  "tree-1",
		},
	}).(map[string]any)
	if result["ok"] != false || result["passed"] != false {
		t.Fatalf("unexpected failed acceptance result: %#v", result)
	}
	artifacts, ok := result["artifacts"].([]artifact)
	if !ok || len(artifacts) != 1 || artifacts[0].ArtifactID != "acceptance-report" {
		t.Fatalf("accepted failure did not return the acceptance artifact: %#v", result["artifacts"])
	}
}

func TestDeliveryVerificationChecksSemanticArtifactChain(t *testing.T) {
	workspace := t.TempDir()
	qrPath := filepath.Join(workspace, "preview.png")
	evidencePath := filepath.Join(workspace, "acceptance.png")
	releaseEvidencePath := filepath.Join(workspace, "release.json")
	for path, content := range map[string]string{
		qrPath:              "preview",
		evidencePath:        "acceptance",
		releaseEvidencePath: `{"platform":"wechat"}`,
	} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	candidate := map[string]any{
		"candidate_id": "candidate-1",
		"commit_sha":   "0123456789abcdef0123456789abcdef01234567",
		"tree_digest":  "tree-1",
	}
	writeArtifact := func(name string, payload map[string]any) string {
		path := filepath.Join(workspace, name)
		if err := writeJSON(path, payload); err != nil {
			t.Fatal(err)
		}
		return path
	}
	base := map[string]any{
		"candidate_id": candidate["candidate_id"],
		"commit_sha":   candidate["commit_sha"],
		"tree_digest":  candidate["tree_digest"],
	}
	preview := map[string]any{}
	experience := map[string]any{}
	acceptance := map[string]any{}
	release := map[string]any{}
	for key, value := range base {
		preview[key] = value
		experience[key] = value
		acceptance[key] = value
		release[key] = value
	}
	preview["app_id"] = "wx1234567890abcdef"
	preview["version"] = "1.0.0"
	preview["preview_artifact_id"] = "preview"
	preview["qr_uri"] = fileURI(qrPath)
	experience["schema_version"] = "wechat_experience_version.v1"
	experience["app_id"] = preview["app_id"]
	experience["version"] = preview["version"]
	experience["uploaded_at"] = "2026-09-16T00:00:00Z"
	acceptance["passed"] = true
	acceptance["evidence_paths"] = []any{evidencePath}
	release["app_id"] = preview["app_id"]
	release["version"] = preview["version"]
	release["release_status"] = "released"
	release["release_evidence"] = releaseEvidencePath

	previewPath := writeArtifact("preview.json", preview)
	experiencePath := writeArtifact("experience.json", experience)
	acceptancePath := writeArtifact("acceptance.json", acceptance)
	releasePath := writeArtifact("release.json", release)
	result := invoke(t, "wechat.miniprogram.delivery.verify", map[string]any{
		"workspace_root": workspace,
		"app_id":         preview["app_id"],
		"version":        preview["version"],
		"candidate":      candidate,
		"artifact_paths": map[string]string{
			"preview":            previewPath,
			"experience-version": experiencePath,
			"acceptance-report":  acceptancePath,
			"release-record":     releasePath,
		},
	}).(map[string]any)
	if result["ok"] != true || result["verified"] != true {
		t.Fatalf("unexpected delivery verification result: %#v", result)
	}

	preview["version"] = "9.9.9"
	if err := writeJSON(previewPath, preview); err != nil {
		t.Fatal(err)
	}
	request, err := jsonrpc.NewRequest(1, "wechat.miniprogram.delivery.verify", map[string]any{
		"workspace_root": workspace,
		"app_id":         preview["app_id"],
		"version":        "1.0.0",
		"candidate":      candidate,
		"artifact_paths": map[string]string{
			"preview":            previewPath,
			"experience-version": experiencePath,
			"acceptance-report":  acceptancePath,
			"release-record":     releasePath,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, rpcError := handle(request); rpcError == nil {
		t.Fatal("expected semantic artifact mismatch to fail")
	}
}

func ciReadinessFixture(t *testing.T, ciVersion string) (string, string, string) {
	t.Helper()
	workspace := t.TempDir()
	projectRoot := filepath.Join(workspace, "miniprogram")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	config := map[string]any{
		"appid":           "wx-test-appid",
		"miniprogramRoot": "src/",
		"compileType":     "miniprogram",
	}
	data, _ := json.Marshal(config)
	if err := os.WriteFile(filepath.Join(projectRoot, "project.config.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if ciVersion != "" {
		moduleRoot := filepath.Join(projectRoot, "node_modules", "miniprogram-ci")
		if err := os.MkdirAll(moduleRoot, 0o755); err != nil {
			t.Fatal(err)
		}
		packageJSON := map[string]any{
			"name":    "miniprogram-ci",
			"version": ciVersion,
			"main":    "index.js",
		}
		data, _ = json.Marshal(packageJSON)
		if err := os.WriteFile(filepath.Join(moduleRoot, "package.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(moduleRoot, "index.js"), []byte("module.exports = {};\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	privateKeyPath := filepath.Join(workspace, "private.key")
	if err := os.WriteFile(
		privateKeyPath,
		[]byte("-----BEGIN PRIVATE KEY-----\ntest\n-----END PRIVATE KEY-----\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	return workspace, projectRoot, privateKeyPath
}

func TestReviewSubmitRequiresEvidence(t *testing.T) {
	workspace := t.TempDir()
	request, err := jsonrpc.NewRequest(1, "wechat.miniprogram.review.submit", map[string]any{
		"workspace_root": workspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, rpcError := handle(request)
	if rpcError == nil {
		t.Fatal("expected missing submission evidence to block")
	}
}

func TestReleaseRecordWritesArtifact(t *testing.T) {
	workspace := t.TempDir()
	result := invoke(t, "wechat.miniprogram.release.record", map[string]any{
		"workspace_root": workspace,
		"version":        "1.0.0",
		"commit_sha":     "0123456789abcdef",
		"candidate": map[string]any{
			"candidate_id": "candidate-1",
			"commit_sha":   "0123456789abcdef",
			"tree_digest":  "tree-1",
		},
	}).(map[string]any)
	artifacts := result["artifacts"].([]artifact)
	if len(artifacts) != 1 || artifacts[0].ArtifactID != "release-record" {
		t.Fatalf("unexpected artifacts: %#v", artifacts)
	}
	if _, err := os.Stat(filepath.Join(workspace, defaultArtifactDir, "release-record.json")); err != nil {
		t.Fatal(err)
	}
}
