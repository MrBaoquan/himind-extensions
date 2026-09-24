package main

import (
	"encoding/json"
	"os"
	"path/filepath"
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

func TestUploadChannelNormalizationAndWechatideResults(t *testing.T) {
	channel, err := normalizeUploadChannel("")
	if err != nil || channel != "ci" {
		t.Fatalf("default channel = %s, err=%v", channel, err)
	}
	channel, err = normalizeUploadChannel("wechatide")
	if err != nil || channel != "wechatide" {
		t.Fatalf("wechatide channel = %s, err=%v", channel, err)
	}
	if _, err := normalizeUploadChannel("ftp"); err == nil {
		t.Fatal("expected invalid upload channel")
	}

	status, taskID, _ := classifyWechatidePayload(map[string]any{
		"ok": true,
		"result": map[string]any{
			"success": true,
			"status":  "pending",
			"taskId":  "task-1",
		},
	})
	if status != "uploaded" || taskID != "task-1" {
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
	}, root, 10*time.Second)
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
	}, root, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if taskID != "task-1" || output["status"] != "uploaded" {
		t.Fatalf("unexpected upload result: task=%s output=%#v", taskID, output)
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
	}, root, 10*time.Second)
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
