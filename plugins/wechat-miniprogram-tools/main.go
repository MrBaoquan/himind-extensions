package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/MrBaoquan/himind-extensions/sdk/jsonrpc"
)

const defaultArtifactDir = ".himind/artifacts/wechat-miniprogram"
const defaultWechatideHome = `C:\Program Files (x86)\Tencent\微信web开发者工具`
const wechatidePollInterval = 2 * time.Second

var scriptPattern = regexp.MustCompile(`^[A-Za-z0-9:_-]+$`)
var venuePattern = regexp.MustCompile(`^[a-z0-9_-]+$`)

type input struct {
	WorkspaceRoot       string            `json:"workspace_root"`
	SourceRoot          string            `json:"source_root"`
	ProjectRoot         string            `json:"project_root"`
	WorkflowContext     map[string]any    `json:"workflow_context"`
	Candidate           map[string]any    `json:"candidate"`
	AppID               string            `json:"app_id"`
	PrivateKeyPath      string            `json:"private_key_path"`
	UploadChannel       string            `json:"upload_channel"`
	WechatideClient     string            `json:"wechatide_client"`
	PackageManager      string            `json:"package_manager"`
	InstallMode         string            `json:"install_mode"`
	Script              string            `json:"script"`
	BuildScript         string            `json:"build_script"`
	Venue               string            `json:"venue"`
	Environment         string            `json:"environment"`
	Scripts             []string          `json:"scripts"`
	ArtifactDir         string            `json:"artifact_dir"`
	ArtifactPaths       map[string]string `json:"artifact_paths"`
	QrcodeOutputDest    string            `json:"qrcode_output_dest"`
	Description         string            `json:"description"`
	Requirement         string            `json:"requirement"`
	AcceptanceCriteria  []string          `json:"acceptance_criteria"`
	Constraints         []string          `json:"constraints"`
	Stage               string            `json:"stage"`
	EvidencePaths       []string          `json:"evidence_paths"`
	ChangeSummary       string            `json:"change_summary"`
	Version             string            `json:"version"`
	CommitSHA           string            `json:"commit_sha"`
	ReviewNotes         string            `json:"review_notes"`
	Tester              string            `json:"tester"`
	TesterID            string            `json:"tester_id"`
	Passed              bool              `json:"passed"`
	ResponsibilityChain []map[string]any  `json:"responsibility_chain"`
	Notes               string            `json:"notes"`
	SubmissionReceipt   string            `json:"submission_receipt"`
	ManualEvidencePath  string            `json:"manual_evidence_path"`
	ReleaseEvidencePath string            `json:"release_evidence_path"`
	TargetVersion       string            `json:"target_version"`
	RollbackRequested   bool              `json:"rollback_requested"`
	Confirm             bool              `json:"confirm"`
	TimeoutSeconds      int               `json:"timeout_seconds"`
	RequireCIInstalled  bool              `json:"require_ci_installed"`
}

type artifact struct {
	ArtifactID   string `json:"artifact_id"`
	ArtifactType string `json:"artifact_type"`
	Name         string `json:"name"`
	URI          string `json:"uri"`
	SHA256       string `json:"sha256"`
	SizeBytes    int64  `json:"size_bytes"`
}

func main() {
	if err := jsonrpc.Serve(os.Stdin, os.Stdout, handle); err != nil {
		fmt.Fprintln(os.Stderr, "wechat mini program tools stopped:", err)
	}
}

func handle(request jsonrpc.Request) (any, *jsonrpc.Error) {
	var in input
	if rpcError := jsonrpc.DecodeParams(request, &in); rpcError != nil {
		return nil, rpcError
	}
	switch request.Method {
	case "wechat.miniprogram.requirements.snapshot":
		return requirementsSnapshot(in)
	case "wechat.miniprogram.project.inspect":
		return projectInspect(in)
	case "wechat.miniprogram.ci.readiness":
		return ciReadiness(in)
	case "wechat.miniprogram.development.record":
		return developmentRecord(in)
	case "wechat.miniprogram.dependencies.prepare":
		return dependenciesPrepare(in)
	case "wechat.miniprogram.test":
		return runTests(in)
	case "wechat.miniprogram.build":
		return runBuild(in)
	case "wechat.miniprogram.preview":
		return runPreview(in)
	case "wechat.miniprogram.upload":
		return runUpload(in)
	case "wechat.miniprogram.acceptance.record":
		return acceptanceRecord(in)
	case "wechat.miniprogram.review.prepare":
		return prepareReview(in)
	case "wechat.miniprogram.review.submit":
		return recordReviewSubmission(in)
	case "wechat.miniprogram.release.record":
		return recordRelease(in)
	case "wechat.miniprogram.rollback":
		return recordRollback(in)
	case "wechat.miniprogram.delivery.verify":
		return verifyDelivery(in)
	default:
		return nil, jsonrpc.InvalidParams("unsupported WeChat mini program capability")
	}
}

func requirementsSnapshot(in input) (any, *jsonrpc.Error) {
	workspaceRoot, err := ensureWorkspace(in.WorkspaceRoot)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	if strings.TrimSpace(in.Requirement) == "" {
		return nil, jsonrpc.InvalidParams("requirement 不能为空")
	}
	artifactDir, err := ensureOutputWithin(
		workspaceRoot,
		defaultOr(in.ArtifactDir, filepath.Join(workspaceRoot, defaultArtifactDir)),
	)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	path := filepath.Join(artifactDir, "requirement-snapshot.json")
	payload := map[string]any{
		"schema_version":      "wechat_requirement_snapshot.v1",
		"requirement":         strings.TrimSpace(in.Requirement),
		"acceptance_criteria": in.AcceptanceCriteria,
		"constraints":         in.Constraints,
		"recorded_at":         time.Now().UTC().Format(time.RFC3339),
	}
	if err := writeJSON(path, payload); err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	item, err := artifactFor(path, "requirement-snapshot", "requirement_snapshot", "需求与验收快照")
	if err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	return map[string]any{"ok": true, "artifacts": []artifact{item}}, nil
}

func projectInspect(in input) (any, *jsonrpc.Error) {
	projectRoot, artifactDir, err := resolvePaths(in)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	configPath := filepath.Join(projectRoot, "project.config.json")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return nil, jsonrpc.InternalError(fmt.Sprintf("读取 project.config.json 失败: %v", err))
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, jsonrpc.InternalError(fmt.Sprintf("project.config.json 格式无效: %v", err))
	}
	appID, _ := config["appid"].(string)
	if strings.TrimSpace(appID) == "" {
		return nil, jsonrpc.InternalError("project.config.json 缺少 appid")
	}
	miniprogramRoot, _ := config["miniprogramRoot"].(string)
	if strings.TrimSpace(miniprogramRoot) == "" {
		miniprogramRoot = "./"
	}
	compileType, _ := config["compileType"].(string)
	if strings.TrimSpace(compileType) == "" {
		compileType = "miniprogram"
	}
	return map[string]any{
		"ok":               true,
		"project_root":     projectRoot,
		"config_path":      configPath,
		"app_id":           appID,
		"miniprogram_root": miniprogramRoot,
		"compile_type":     compileType,
		"artifact_dir":     artifactDir,
	}, nil
}

func ciReadiness(in input) (any, *jsonrpc.Error) {
	projectRoot, _, err := resolvePaths(in)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	configPath := filepath.Join(projectRoot, "project.config.json")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return nil, jsonrpc.InternalError(fmt.Sprintf("读取 project.config.json 失败: %v", err))
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, jsonrpc.InternalError(fmt.Sprintf("project.config.json 格式无效: %v", err))
	}
	projectAppID := strings.TrimSpace(asString(config["appid"]))
	appID, err := resolveProjectAppID(strings.TrimSpace(in.AppID), projectAppID)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	if appID == "" {
		return nil, jsonrpc.InvalidParams("app_id 不能为空")
	}
	uploadChannel, err := normalizeUploadChannel(in.UploadChannel)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	if uploadChannel == "wechatide" {
		clientName := defaultOr(in.WechatideClient, "Copilot")
		executable, err := resolveWechatideExecutable()
		if err != nil {
			return nil, jsonrpc.InvalidParams(err.Error())
		}
		payload, output, err := runWechatideTool(
			projectRoot,
			clientName,
			"check_wechatide_status",
			nil,
			30*time.Second,
		)
		if err != nil {
			return nil, jsonrpc.InvalidParams(err.Error())
		}
		result := wechatideResult(payload)
		loggedIn := asBool(result["success"]) && !asBool(result["loginExpired"])
		if !loggedIn {
			return nil, jsonrpc.InvalidParams(
				"wechatide 未登录或登录已过期，请先在微信开发者工具完成登录",
			)
		}
		return map[string]any{
			"ok":                true,
			"app_id":            appID,
			"project_root":      projectRoot,
			"upload_channel":    "wechatide",
			"wechatide_path":    executable,
			"wechatide_client":  clientName,
			"login_expired":     false,
			"ready_for_upload":  true,
			"missing":           []string{},
			"wechatide_output":  tail(output, 4000),
			"raw_result_status": asString(result["status"]),
		}, nil
	}
	privateKey, err := resolveFile(projectRoot, in.PrivateKeyPath)
	if err != nil || !isFile(privateKey) {
		return nil, jsonrpc.InvalidParams("private_key_path 必须是可读取的本机文件")
	}
	privateKeyBytes, err := os.ReadFile(privateKey)
	if err != nil || len(bytes.TrimSpace(privateKeyBytes)) == 0 {
		return nil, jsonrpc.InvalidParams("private_key_path 不能为空")
	}
	requireReal := isTruthyEnv("HIMIND_WECHAT_REQUIRE_REAL_CI")
	if requireReal && !bytes.Contains(bytes.ToUpper(privateKeyBytes), []byte("PRIVATE KEY")) {
		return nil, jsonrpc.InvalidParams("真实微信验收要求 PEM 格式的上传私钥")
	}
	nodePath, err := exec.LookPath("node")
	if err != nil {
		return nil, jsonrpc.InvalidParams("未找到 node 命令")
	}
	nodeVersion, err := runCommand(projectRoot, "node", []string{"--version"}, 30*time.Second)
	if err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	resolveScript := `
const fs = require('fs');
const path = require('path');
let resolved;
try {
  resolved = require.resolve('miniprogram-ci', { paths: [process.cwd()] });
} catch (error) {
  process.stderr.write(error && error.message ? error.message : String(error));
  process.exit(2);
}
let root = path.dirname(resolved);
while (root !== path.dirname(root) && !fs.existsSync(path.join(root, 'package.json'))) {
  root = path.dirname(root);
}
const pkg = JSON.parse(fs.readFileSync(path.join(root, 'package.json'), 'utf8'));
process.stdout.write(JSON.stringify({ path: root, version: pkg.version || '' }));
`
	output, err := runCommand(projectRoot, "node", []string{"-e", resolveScript}, 30*time.Second)
	if err != nil {
		if in.RequireCIInstalled {
			return nil, jsonrpc.InvalidParams(fmt.Sprintf(
				"miniprogram-ci 不可用，请在项目安装依赖后重试: %v",
				err,
			))
		}
		return map[string]any{
			"ok":                     true,
			"app_id":                 appID,
			"app_id_matches":         true,
			"project_root":           projectRoot,
			"node_path":              nodePath,
			"node_version":           strings.TrimSpace(nodeVersion),
			"miniprogram_ci_path":    "",
			"miniprogram_ci_version": "",
			"ci_installed":           false,
			"contract_double":        false,
			"private_key_readable":   true,
			"real_ci_required":       requireReal,
			"ready_for_upload":       false,
			"missing":                []string{"miniprogram-ci"},
		}, nil
	}
	ciInfo, err := parseLastJSONObject(output)
	if err != nil {
		return nil, jsonrpc.InternalError(fmt.Sprintf("miniprogram-ci 信息格式无效: %v", err))
	}
	ciPath := asString(ciInfo["path"])
	ciVersion := asString(ciInfo["version"])
	normalizedPath := strings.ToLower(filepath.ToSlash(ciPath))
	contractDouble := ciVersion == "0.0.0-local" ||
		strings.Contains(normalizedPath, "/vendor/miniprogram-ci") ||
		strings.Contains(normalizedPath, "himind-workflow-acceptance")
	if requireReal && contractDouble {
		return nil, jsonrpc.InvalidParams(
			"真实微信验收不允许使用本地 miniprogram-ci Contract Double",
		)
	}
	return map[string]any{
		"ok":                     true,
		"app_id":                 appID,
		"app_id_matches":         true,
		"project_root":           projectRoot,
		"node_path":              nodePath,
		"node_version":           strings.TrimSpace(nodeVersion),
		"miniprogram_ci_path":    ciPath,
		"miniprogram_ci_version": ciVersion,
		"ci_installed":           true,
		"contract_double":        contractDouble,
		"private_key_readable":   true,
		"real_ci_required":       requireReal,
		"ready_for_upload":       !contractDouble || !requireReal,
		"missing":                []string{},
	}, nil
}

func developmentRecord(in input) (any, *jsonrpc.Error) {
	workspaceRoot, err := ensureWorkspace(in.WorkspaceRoot)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	projectRoot, err := ensureWorkspace(defaultOr(in.ProjectRoot, workspaceRoot))
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	stage := strings.TrimSpace(strings.ToLower(in.Stage))
	if stage != "plan" && stage != "changes" {
		return nil, jsonrpc.InvalidParams("stage 必须是 plan 或 changes")
	}
	if strings.TrimSpace(in.ChangeSummary) == "" {
		return nil, jsonrpc.InvalidParams("change_summary 不能为空")
	}
	evidence, err := resolveEvidencePaths(projectRoot, in.EvidencePaths)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	artifactDir, err := ensureOutputWithin(
		workspaceRoot,
		defaultOr(in.ArtifactDir, filepath.Join(workspaceRoot, defaultArtifactDir)),
	)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	path := filepath.Join(artifactDir, "development-"+stage+".json")
	payload := map[string]any{
		"schema_version": "wechat_development_record.v1",
		"stage":          stage,
		"project_root":   projectRoot,
		"change_summary": strings.TrimSpace(in.ChangeSummary),
		"commit_sha":     strings.TrimSpace(in.CommitSHA),
		"evidence_paths": evidence,
		"recorded_at":    time.Now().UTC().Format(time.RFC3339),
	}
	if err := writeJSON(path, payload); err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	item, err := artifactFor(path, "development-record", "development_record", "开发变更记录")
	if err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	return map[string]any{"ok": true, "stage": stage, "artifacts": []artifact{item}}, nil
}

func dependenciesPrepare(in input) (any, *jsonrpc.Error) {
	sourceRoot, _, err := resolveSourcePaths(in)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	manager := strings.TrimSpace(strings.ToLower(in.PackageManager))
	if manager == "" {
		manager = "npm"
	}
	mode := strings.TrimSpace(strings.ToLower(in.InstallMode))
	if mode == "" {
		mode = "ci"
	}
	args := []string{"install"}
	switch manager {
	case "npm":
		if mode == "ci" {
			args = []string{"ci"}
		}
	case "pnpm":
		args = []string{"install", "--frozen-lockfile"}
		if mode == "install" {
			args = []string{"install"}
		}
	case "yarn":
		args = []string{"install", "--immutable"}
		if mode == "install" {
			args = []string{"install"}
		}
	default:
		return nil, jsonrpc.InvalidParams("package_manager 必须是 npm、pnpm 或 yarn")
	}
	output, err := runCommand(sourceRoot, manager, args, timeout(in.TimeoutSeconds, 15*time.Minute))
	if err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	return map[string]any{
		"ok":              true,
		"package_manager": manager,
		"install_mode":    mode,
		"output":          output,
	}, nil
}

func runTests(in input) (any, *jsonrpc.Error) {
	sourceRoot, artifactDir, err := resolveSourcePaths(in)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	manager := strings.TrimSpace(strings.ToLower(in.PackageManager))
	if manager == "" {
		manager = "npm"
	}
	scripts := in.Scripts
	if len(scripts) == 0 {
		scripts = []string{"lint", "typecheck", "test"}
	}
	results := make([]map[string]any, 0, len(scripts))
	failed := make([]string, 0)
	for _, script := range scripts {
		if !scriptPattern.MatchString(script) {
			return nil, jsonrpc.InvalidParams("测试脚本名称不安全")
		}
		output, runErr := runScript(sourceRoot, manager, script, timeout(in.TimeoutSeconds, 20*time.Minute))
		result := map[string]any{
			"script": script,
			"passed": runErr == nil,
			"output": output,
		}
		if runErr != nil {
			result["error"] = runErr.Error()
			failed = append(failed, script)
		}
		results = append(results, result)
	}
	commitSHA, err := candidateCommitSHA(in, sourceRoot)
	if err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	checks := make([]map[string]any, 0, len(results))
	for _, result := range results {
		status := "passed"
		if result["passed"] != true {
			status = "failed"
		}
		checks = append(checks, map[string]any{
			"name":   result["script"],
			"status": status,
			"detail": result["output"],
		})
	}
	reportPath := filepath.Join(artifactDir, "test-report.json")
	report := map[string]any{
		"commit_sha": commitSHA,
		"status":     map[bool]string{true: "passed", false: "failed"}[len(failed) == 0],
		"checks":     checks,
	}
	if err := writeJSON(reportPath, report); err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	item, err := artifactFor(reportPath, "test-report", "test_report", "测试报告")
	if err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	if len(failed) > 0 {
		return map[string]any{
			"ok":        false,
			"passed":    false,
			"failed":    failed,
			"artifacts": []artifact{item},
		}, jsonrpc.InternalError("小程序工程测试失败: " + strings.Join(failed, ", "))
	}
	return map[string]any{
		"ok":        true,
		"passed":    true,
		"artifacts": []artifact{item},
	}, nil
}

func runBuild(in input) (any, *jsonrpc.Error) {
	sourceRoot, _, err := resolveSourcePaths(in)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	if strings.TrimSpace(in.Venue) != "" || strings.TrimSpace(in.Environment) != "" {
		venue, environment, err := resolveBuildTarget(in.Venue, in.Environment)
		if err != nil {
			return nil, jsonrpc.InvalidParams(err.Error())
		}
		scriptPath := filepath.Join(sourceRoot, "scripts", "wx-cli.js")
		if !isFile(scriptPath) {
			return nil, jsonrpc.InvalidParams("展馆构建需要 source_root/scripts/wx-cli.js")
		}
		output, err := runCommand(
			sourceRoot,
			"node",
			[]string{scriptPath, "build", "--env", venue, "--server", environment},
			timeout(in.TimeoutSeconds, 30*time.Minute),
		)
		if err != nil {
			return nil, jsonrpc.InternalError(err.Error())
		}
		projectRoot := defaultProjectRoot(sourceRoot, in.ProjectRoot)
		projectRoot, err = ensureWorkspace(projectRoot)
		if err != nil {
			return nil, jsonrpc.InvalidParams(err.Error())
		}
		appID, err := projectConfigAppID(projectRoot)
		if err != nil {
			return nil, jsonrpc.InternalError(err.Error())
		}
		return map[string]any{
			"ok":          true,
			"venue":       venue,
			"environment": environment,
			"app_id":      appID,
			"script":      "scripts/wx-cli.js build",
			"output":      output,
		}, nil
	}
	manager := strings.TrimSpace(strings.ToLower(in.PackageManager))
	if manager == "" {
		manager = "npm"
	}
	script := strings.TrimSpace(in.BuildScript)
	if script == "" {
		script = strings.TrimSpace(in.Script)
	}
	if script == "" {
		script = "build"
	}
	if !scriptPattern.MatchString(script) {
		return nil, jsonrpc.InvalidParams("构建脚本名称不安全")
	}
	output, err := runScript(sourceRoot, manager, script, timeout(in.TimeoutSeconds, 30*time.Minute))
	if err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	return map[string]any{
		"ok":     true,
		"script": script,
		"output": output,
	}, nil
}

func runPreview(in input) (any, *jsonrpc.Error) {
	projectRoot, artifactDir, err := resolvePaths(in)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	appID, err := validateCIInput(in, projectRoot, artifactDir)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	qrcode := strings.TrimSpace(in.QrcodeOutputDest)
	if qrcode == "" {
		qrcode = filepath.Join(artifactDir, "preview.png")
	} else {
		qrcode, err = ensureOutputWithin(in.WorkspaceRoot, qrcode)
		if err != nil {
			return nil, jsonrpc.InvalidParams(err.Error())
		}
	}
	output, err := runCIRunner(projectRoot, map[string]any{
		"operation":     "preview",
		"app_id":        appID,
		"private_key":   in.PrivateKeyPath,
		"project_root":  projectRoot,
		"description":   in.Description,
		"qrcode_output": qrcode,
	}, timeout(in.TimeoutSeconds, 15*time.Minute))
	if err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	candidateID, commitSHA, treeDigest, err := candidateIdentity(in)
	if err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	manifestPath := filepath.Join(artifactDir, "preview-manifest.json")
	if err := writeJSON(manifestPath, map[string]any{
		"candidate_id":        candidateID,
		"app_id":              appID,
		"version":             strings.TrimSpace(in.Version),
		"commit_sha":          commitSHA,
		"tree_digest":         treeDigest,
		"preview_artifact_id": "preview",
		"qr_uri":              fileURI(qrcode),
		"generated_at":        time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	item, err := artifactFor(manifestPath, "preview", "wechat_preview", "微信预览码")
	if err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	return map[string]any{
		"ok":        true,
		"operation": "preview",
		"ci_output": output,
		"artifacts": []artifact{item},
	}, nil
}

func runUpload(in input) (any, *jsonrpc.Error) {
	projectRoot, artifactDir, err := resolvePaths(in)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	uploadChannel, err := normalizeUploadChannel(in.UploadChannel)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	if strings.TrimSpace(in.Version) == "" {
		return nil, jsonrpc.InvalidParams("version 不能为空")
	}
	appID := ""
	uploadStatus := "uploaded"
	wechatideTaskID := ""
	var output map[string]any
	switch uploadChannel {
	case "wechatide":
		appID, err = projectConfigAppID(projectRoot)
		if err != nil {
			return nil, jsonrpc.InvalidParams(err.Error())
		}
		output, wechatideTaskID, err = runWechatideUpload(
			in,
			projectRoot,
			timeout(in.TimeoutSeconds, 15*time.Minute),
		)
		if err != nil {
			return nil, jsonrpc.InternalError(err.Error())
		}
	default:
		appID, err = validateCIInput(in, projectRoot, artifactDir)
		if err != nil {
			return nil, jsonrpc.InvalidParams(err.Error())
		}
		output, err = runCIRunner(projectRoot, map[string]any{
			"operation":    "upload",
			"app_id":       appID,
			"private_key":  in.PrivateKeyPath,
			"project_root": projectRoot,
			"version":      strings.TrimSpace(in.Version),
			"description":  in.Description,
		}, timeout(in.TimeoutSeconds, 15*time.Minute))
		if err != nil {
			return nil, jsonrpc.InternalError(err.Error())
		}
	}
	candidateID, commitSHA, treeDigest, err := candidateIdentity(in)
	if err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	evidencePath := filepath.Join(artifactDir, "experience-version.json")
	evidence := map[string]any{
		"schema_version":    "wechat_experience_version.v1",
		"candidate_id":      candidateID,
		"app_id":            appID,
		"venue":             strings.TrimSpace(in.Venue),
		"environment":       strings.TrimSpace(in.Environment),
		"upload_channel":    uploadChannel,
		"upload_status":     uploadStatus,
		"wechatide_task_id": wechatideTaskID,
		"version":           strings.TrimSpace(in.Version),
		"commit_sha":        commitSHA,
		"tree_digest":       treeDigest,
		"description":       in.Description,
		"uploaded_at":       time.Now().UTC().Format(time.RFC3339),
		"ci_output":         output,
	}
	if err := writeJSON(evidencePath, evidence); err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	item, err := artifactFor(evidencePath, "experience-version", "wechat_experience_version", "体验版记录")
	if err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	return map[string]any{
		"ok":                true,
		"operation":         "upload",
		"upload_channel":    uploadChannel,
		"upload_status":     uploadStatus,
		"wechatide_task_id": wechatideTaskID,
		"version":           strings.TrimSpace(in.Version),
		"artifacts":         []artifact{item},
	}, nil
}

func acceptanceRecord(in input) (any, *jsonrpc.Error) {
	workspaceRoot, err := ensureWorkspace(in.WorkspaceRoot)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	projectRoot, err := ensureWorkspace(defaultOr(in.ProjectRoot, workspaceRoot))
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	if strings.TrimSpace(in.Tester) == "" {
		return nil, jsonrpc.InvalidParams("tester 不能为空")
	}
	evidence, err := resolveEvidencePaths(projectRoot, in.EvidencePaths)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	if len(evidence) == 0 {
		return nil, jsonrpc.InvalidParams("至少提供一个真实验收证据文件")
	}
	testerID := strings.TrimSpace(in.TesterID)
	if testerID == "" {
		testerID = strings.TrimSpace(in.Tester)
	}
	if in.Passed && testerID == "" {
		return nil, jsonrpc.InvalidParams("passed=true 时 tester_id 不能为空")
	}
	evidenceSignature, err := evidenceManifestSignature(evidence)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	responsibilityChain := in.ResponsibilityChain
	if len(responsibilityChain) == 0 {
		responsibilityChain = []map[string]any{{
			"actor_id":    testerID,
			"role":        "acceptance_tester",
			"action":      "accept",
			"occurred_at": time.Now().UTC().Format(time.RFC3339),
		}}
	}
	artifactDir, err := ensureOutputWithin(
		workspaceRoot,
		defaultOr(in.ArtifactDir, filepath.Join(workspaceRoot, defaultArtifactDir)),
	)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	candidateID, commitSHA, treeDigest, err := candidateIdentity(in)
	if err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	path := filepath.Join(artifactDir, "acceptance-report.json")
	payload := map[string]any{
		"schema_version":       "wechat_acceptance_report.v1",
		"candidate_id":         candidateID,
		"commit_sha":           commitSHA,
		"tree_digest":          treeDigest,
		"project_root":         projectRoot,
		"tester":               strings.TrimSpace(in.Tester),
		"tester_id":            testerID,
		"passed":               in.Passed,
		"evidence_paths":       evidence,
		"evidence_signature":   evidenceSignature,
		"responsibility_chain": responsibilityChain,
		"attested_at":          time.Now().UTC().Format(time.RFC3339),
		"notes":                strings.TrimSpace(in.Notes),
		"recorded_at":          time.Now().UTC().Format(time.RFC3339),
	}
	if err := writeJSON(path, payload); err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	item, err := artifactFor(path, "acceptance-report", "acceptance_report", "人工验收报告")
	if err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	if !in.Passed {
		return map[string]any{
			"ok":        false,
			"passed":    false,
			"artifacts": []artifact{item},
		}, nil
	}
	return map[string]any{
		"ok":        true,
		"passed":    true,
		"artifacts": []artifact{item},
	}, nil
}

func prepareReview(in input) (any, *jsonrpc.Error) {
	projectRoot, artifactDir, err := resolvePaths(in)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	if strings.TrimSpace(in.Version) == "" {
		return nil, jsonrpc.InvalidParams("version 不能为空")
	}
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	candidateID, commitSHA, treeDigest, err := candidateIdentity(in)
	if err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	path := filepath.Join(artifactDir, "review-package.json")
	payload := map[string]any{
		"schema_version": "wechat_review_package.v1",
		"candidate_id":   candidateID,
		"commit_sha":     commitSHA,
		"tree_digest":    treeDigest,
		"project_root":   projectRoot,
		"version":        strings.TrimSpace(in.Version),
		"review_notes":   in.ReviewNotes,
		"checklist": []string{
			"确认体验版可由测试账号访问",
			"确认页面路径和审核说明一致",
			"确认不存在未授权数据或测试入口",
		},
		"platform_submission": "manual_or_not_available",
		"generated_at":        time.Now().UTC().Format(time.RFC3339),
	}
	if err := writeJSON(path, payload); err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	return map[string]any{
		"ok":          true,
		"review_path": path,
	}, nil
}

func recordReviewSubmission(in input) (any, *jsonrpc.Error) {
	workspaceRoot, err := ensureWorkspace(in.WorkspaceRoot)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	artifactDir, err := ensureOutputWithin(workspaceRoot, defaultOr(in.ArtifactDir, filepath.Join(workspaceRoot, defaultArtifactDir)))
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	if strings.TrimSpace(in.SubmissionReceipt) == "" && strings.TrimSpace(in.ManualEvidencePath) == "" {
		return map[string]any{
			"ok":              false,
			"submitted":       false,
			"manual_required": true,
			"message":         "微信审核平台未提供可用自动提交接口；请人工提交后提供 submission_receipt 或 manual_evidence_path",
		}, jsonrpc.InternalError("微信审核提交需要真实人工证据，不能由模型猜测成功")
	}
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	candidateID, commitSHA, treeDigest, err := candidateIdentity(in)
	if err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	payload := map[string]any{
		"schema_version":     "wechat_review_submission.v1",
		"candidate_id":       candidateID,
		"commit_sha":         commitSHA,
		"tree_digest":        treeDigest,
		"submitted":          true,
		"submission_receipt": in.SubmissionReceipt,
		"evidence_path":      in.ManualEvidencePath,
		"recorded_at":        time.Now().UTC().Format(time.RFC3339),
	}
	path := filepath.Join(artifactDir, "review-submission.json")
	if err := writeJSON(path, payload); err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	return map[string]any{"ok": true, "submitted": true, "evidence_path": path}, nil
}

func recordRelease(in input) (any, *jsonrpc.Error) {
	workspaceRoot, err := ensureWorkspace(in.WorkspaceRoot)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	if strings.TrimSpace(in.Version) == "" {
		return nil, jsonrpc.InvalidParams("version 不能为空")
	}
	artifactDir, err := ensureOutputWithin(workspaceRoot, defaultOr(in.ArtifactDir, filepath.Join(workspaceRoot, defaultArtifactDir)))
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	candidateID, commitSHA, treeDigest, err := candidateIdentity(in)
	if err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	path := filepath.Join(artifactDir, "release-record.json")
	record := map[string]any{
		"schema_version":   "wechat_release_record.v1",
		"candidate_id":     candidateID,
		"app_id":           in.AppID,
		"version":          strings.TrimSpace(in.Version),
		"commit_sha":       commitSHA,
		"tree_digest":      treeDigest,
		"release_status":   "released",
		"release_evidence": in.ReleaseEvidencePath,
		"recorded_at":      time.Now().UTC().Format(time.RFC3339),
		"rollback_target":  "",
	}
	if err := writeJSON(path, record); err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	item, err := artifactFor(path, "release-record", "release_record", "发布与回滚记录")
	if err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	return map[string]any{"ok": true, "artifacts": []artifact{item}}, nil
}

func recordRollback(in input) (any, *jsonrpc.Error) {
	workspaceRoot, err := ensureWorkspace(in.WorkspaceRoot)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	if strings.TrimSpace(in.TargetVersion) == "" {
		return nil, jsonrpc.InvalidParams("target_version 不能为空")
	}
	artifactDir, err := ensureOutputWithin(workspaceRoot, defaultOr(in.ArtifactDir, filepath.Join(workspaceRoot, defaultArtifactDir)))
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	if in.RollbackRequested && !in.Confirm {
		return nil, jsonrpc.InvalidParams("执行回滚记录前必须确认")
	}
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	candidateID, commitSHA, treeDigest, err := candidateIdentity(in)
	if err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	path := filepath.Join(artifactDir, "release-record.json")
	record := map[string]any{
		"schema_version":    "wechat_release_record.v1",
		"candidate_id":      candidateID,
		"app_id":            in.AppID,
		"version":           strings.TrimSpace(in.Version),
		"commit_sha":        commitSHA,
		"tree_digest":       treeDigest,
		"release_status":    map[bool]string{true: "rollback_recorded", false: "monitoring"}[in.RollbackRequested],
		"rollback_target":   strings.TrimSpace(in.TargetVersion),
		"rollback_executed": in.RollbackRequested && in.Confirm,
		"recorded_at":       time.Now().UTC().Format(time.RFC3339),
	}
	if err := writeJSON(path, record); err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	item, err := artifactFor(path, "release-record", "release_record", "发布与回滚记录")
	if err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	return map[string]any{
		"ok":                 true,
		"rollback_requested": in.RollbackRequested,
		"rollback_executed":  in.RollbackRequested && in.Confirm,
		"artifacts":          []artifact{item},
	}, nil
}

func verifyDelivery(in input) (any, *jsonrpc.Error) {
	workspaceRoot, err := ensureWorkspace(in.WorkspaceRoot)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	appID := strings.TrimSpace(in.AppID)
	version := strings.TrimSpace(in.Version)
	if appID == "" || version == "" {
		return nil, jsonrpc.InvalidParams("app_id 和 version 不能为空")
	}
	candidateID, commitSHA, treeDigest, err := candidateIdentity(in)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	required := []string{"preview", "experience-version", "acceptance-report", "release-record"}
	for _, artifactID := range required {
		if strings.TrimSpace(in.ArtifactPaths[artifactID]) == "" {
			return nil, jsonrpc.InvalidParams(fmt.Sprintf("artifact_paths.%s 不能为空", artifactID))
		}
	}

	preview, err := readDeliveryArtifact(workspaceRoot, in.ArtifactPaths["preview"])
	if err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	if err := requireDeliveryIdentity(preview, candidateID, commitSHA, treeDigest); err != nil {
		return nil, jsonrpc.InternalError("preview " + err.Error())
	}
	if asString(preview["app_id"]) != appID || asString(preview["version"]) != version {
		return nil, jsonrpc.InternalError("preview AppID 或版本与当前交付不一致")
	}
	qrPath, err := resolveArtifactPath(workspaceRoot, asString(preview["qr_uri"]))
	if err != nil || !isNonEmptyFile(qrPath) {
		return nil, jsonrpc.InternalError("preview qr_uri 不是可读取的非空文件")
	}

	experience, err := readDeliveryArtifact(workspaceRoot, in.ArtifactPaths["experience-version"])
	if err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	if err := requireDeliveryIdentity(experience, candidateID, commitSHA, treeDigest); err != nil {
		return nil, jsonrpc.InternalError("experience-version " + err.Error())
	}
	if asString(experience["app_id"]) != appID || asString(experience["version"]) != version {
		return nil, jsonrpc.InternalError("experience-version AppID 或版本与当前交付不一致")
	}
	if strings.TrimSpace(asString(experience["uploaded_at"])) == "" {
		return nil, jsonrpc.InternalError("experience-version 缺少 uploaded_at")
	}

	acceptance, err := readDeliveryArtifact(workspaceRoot, in.ArtifactPaths["acceptance-report"])
	if err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	if err := requireDeliveryIdentity(acceptance, candidateID, commitSHA, treeDigest); err != nil {
		return nil, jsonrpc.InternalError("acceptance-report " + err.Error())
	}
	if acceptance["passed"] != true {
		return nil, jsonrpc.InternalError("acceptance-report 必须明确 passed=true")
	}
	evidencePaths, ok := acceptance["evidence_paths"].([]any)
	if !ok || len(evidencePaths) == 0 {
		return nil, jsonrpc.InternalError("acceptance-report 缺少验收证据")
	}
	for _, raw := range evidencePaths {
		path, err := resolveArtifactPath(workspaceRoot, asString(raw))
		if err != nil || !isNonEmptyFile(path) {
			return nil, jsonrpc.InternalError("acceptance-report 引用的验收证据不存在或为空")
		}
	}

	release, err := readDeliveryArtifact(workspaceRoot, in.ArtifactPaths["release-record"])
	if err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	if err := requireDeliveryIdentity(release, candidateID, commitSHA, treeDigest); err != nil {
		return nil, jsonrpc.InternalError("release-record " + err.Error())
	}
	if asString(release["app_id"]) != appID || asString(release["version"]) != version {
		return nil, jsonrpc.InternalError("release-record AppID 或版本与当前交付不一致")
	}
	if asString(release["release_status"]) != "released" {
		return nil, jsonrpc.InternalError("release-record 状态必须为 released")
	}
	releaseEvidence, err := resolveArtifactPath(workspaceRoot, asString(release["release_evidence"]))
	if err != nil || !isNonEmptyFile(releaseEvidence) {
		return nil, jsonrpc.InternalError("release-record 缺少真实发布证据")
	}

	return map[string]any{
		"ok":           true,
		"verified":     true,
		"candidate_id": candidateID,
		"commit_sha":   commitSHA,
		"tree_digest":  treeDigest,
		"checks":       required,
	}, nil
}

func readDeliveryArtifact(workspaceRoot, value string) (map[string]any, error) {
	path, err := resolveArtifactPath(workspaceRoot, value)
	if err != nil {
		return nil, err
	}
	if !isNonEmptyFile(path) {
		return nil, fmt.Errorf("artifact 文件不存在或为空: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("artifact 不是有效 JSON: %w", err)
	}
	return result, nil
}

func resolveArtifactPath(workspaceRoot, value string) (string, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return "", errors.New("artifact path 不能为空")
	}
	if parsed, err := url.Parse(raw); err == nil && parsed.Scheme == "file" {
		raw = parsed.Path
		if runtime.GOOS == "windows" && strings.HasPrefix(raw, "/") {
			raw = strings.TrimPrefix(raw, "/")
		}
		if decoded, err := url.PathUnescape(raw); err == nil {
			raw = decoded
		}
	}
	return ensureOutputWithin(workspaceRoot, filepath.FromSlash(raw))
}

func requireDeliveryIdentity(value map[string]any, candidateID, commitSHA, treeDigest string) error {
	if asString(value["candidate_id"]) != candidateID ||
		asString(value["commit_sha"]) != commitSHA ||
		asString(value["tree_digest"]) != treeDigest {
		return errors.New("Candidate 绑定不一致")
	}
	return nil
}

func isNonEmptyFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

func resolvePaths(in input) (string, string, error) {
	projectRoot, _, artifactDir, err := resolvePathsWithSource(in)
	return projectRoot, artifactDir, err
}

func resolveSourcePaths(in input) (string, string, error) {
	_, sourceRoot, artifactDir, err := resolvePathsWithSource(in)
	return sourceRoot, artifactDir, err
}

func resolvePathsWithSource(in input) (string, string, string, error) {
	sourceRoot := defaultOr(in.SourceRoot, in.WorkspaceRoot)
	projectRoot := defaultProjectRoot(sourceRoot, in.ProjectRoot)
	sourceRoot, err := ensureWorkspace(sourceRoot)
	if err != nil {
		return "", "", "", err
	}
	projectRoot, err = ensureWorkspace(projectRoot)
	if err != nil {
		return "", "", "", err
	}
	if strings.TrimSpace(in.WorkspaceRoot) != "" {
		if _, err := ensureWithin(in.WorkspaceRoot, projectRoot); err != nil {
			return "", "", "", err
		}
		if _, err := ensureWithin(in.WorkspaceRoot, sourceRoot); err != nil {
			return "", "", "", err
		}
	}
	artifactDir := strings.TrimSpace(in.ArtifactDir)
	if artifactDir == "" {
		artifactDir = filepath.Join(sourceRoot, defaultArtifactDir)
	}
	artifactDir, err = ensureOutputWithin(sourceRoot, artifactDir)
	if err != nil {
		return "", "", "", err
	}
	return projectRoot, sourceRoot, artifactDir, nil
}

func defaultProjectRoot(sourceRoot, configured string) string {
	if value := strings.TrimSpace(configured); value != "" {
		return value
	}
	candidate := filepath.Join(sourceRoot, "dist", "wx")
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return candidate
	}
	return sourceRoot
}

func resolveEvidencePaths(base string, paths []string) ([]string, error) {
	result := make([]string, 0, len(paths))
	for _, item := range paths {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		resolved, err := resolveFile(base, item)
		if err != nil {
			return nil, err
		}
		if !isFile(resolved) {
			return nil, fmt.Errorf("验收证据文件不存在: %s", resolved)
		}
		result = append(result, resolved)
	}
	return result, nil
}

func evidenceManifestSignature(paths []string) (string, error) {
	entries := make([]string, 0, len(paths))
	for _, raw := range paths {
		path := strings.TrimSpace(raw)
		if path == "" {
			return "", errors.New("验收证据路径不能为空")
		}
		canonical := filepath.Clean(path)
		digest, err := sha256File(canonical)
		if err != nil {
			return "", err
		}
		entries = append(entries, canonical+"|"+digest)
	}
	if len(entries) == 0 {
		return "", errors.New("至少提供一个真实验收证据文件")
	}
	sort.Strings(entries)
	digest := sha256.Sum256([]byte(strings.Join(entries, "\n")))
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func candidateIdentity(in input) (string, string, string, error) {
	if in.Candidate == nil {
		return "", "", "", errors.New("candidate is required")
	}
	candidateID := strings.TrimSpace(asString(in.Candidate["candidate_id"]))
	commitSHA := strings.TrimSpace(asString(in.Candidate["commit_sha"]))
	treeDigest := strings.TrimSpace(asString(in.Candidate["tree_digest"]))
	if candidateID == "" || commitSHA == "" || treeDigest == "" {
		return "", "", "", errors.New("candidate identity is incomplete")
	}
	return candidateID, commitSHA, treeDigest, nil
}

func candidateCommitSHA(in input, projectRoot string) (string, error) {
	if in.Candidate != nil {
		if commitSHA := strings.TrimSpace(asString(in.Candidate["commit_sha"])); commitSHA != "" {
			return commitSHA, nil
		}
	}
	output, err := runGit(projectRoot, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(output), nil
}

func runGit(directory string, args ...string) (string, error) {
	command := exec.Command("git", args...)
	configureHiddenCommand(command)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("Git 命令失败: %v: %s", err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func asString(value any) string {
	text, _ := value.(string)
	return text
}

func isTruthyEnv(key string) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	return value == "1" || value == "true" || value == "yes"
}

func parseLastJSONObject(value string) (map[string]any, error) {
	for index := strings.LastIndex(value, "{"); index >= 0; index = strings.LastIndex(value[:index], "{") {
		var result map[string]any
		if err := json.Unmarshal([]byte(value[index:]), &result); err == nil {
			return result, nil
		}
	}
	return nil, errors.New("JSON object not found")
}

func validateCIInput(in input, projectRoot, artifactDir string) (string, error) {
	projectAppID, err := projectConfigAppID(projectRoot)
	if err != nil {
		return "", err
	}
	appID, err := resolveProjectAppID(strings.TrimSpace(in.AppID), projectAppID)
	if err != nil {
		return "", err
	}
	privateKey, err := resolveFile(projectRoot, in.PrivateKeyPath)
	if err != nil || !isFile(privateKey) {
		return "", errors.New("private_key_path 必须是可读取的本机文件")
	}
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return "", err
	}
	if !isFile(filepath.Join(projectRoot, "project.config.json")) {
		return "", errors.New("project_root 缺少 project.config.json")
	}
	return appID, nil
}

func normalizeUploadChannel(value string) (string, error) {
	channel := strings.ToLower(strings.TrimSpace(value))
	if channel == "" {
		channel = "ci"
	}
	if channel != "ci" && channel != "wechatide" {
		return "", fmt.Errorf("upload_channel 取值非法: %s", value)
	}
	return channel, nil
}

func resolveWechatideExecutable() (string, error) {
	root := defaultOr(os.Getenv("WECHAT_DEVTOOLS_HOME"), defaultWechatideHome)
	executable := filepath.Join(root, "wechatide.cmd")
	if !isFile(executable) {
		return "", fmt.Errorf("找不到 wechatide CLI: %s", executable)
	}
	return executable, nil
}

func runWechatideTool(
	directory string,
	clientName string,
	toolName string,
	args []string,
	timeoutDuration time.Duration,
) (map[string]any, string, error) {
	executable, err := resolveWechatideExecutable()
	if err != nil {
		return nil, "", err
	}
	commandArgs := []string{"-c", defaultOr(clientName, "Copilot"), toolName}
	commandArgs = append(commandArgs, args...)
	output, err := runCommand(directory, executable, commandArgs, timeoutDuration)
	if err != nil {
		return nil, output, err
	}
	payload, err := parseLastJSONObject(output)
	if err != nil {
		return nil, output, fmt.Errorf("wechatide 返回结果格式无效: %w", err)
	}
	return payload, output, nil
}

func runWechatideUpload(
	in input,
	projectRoot string,
	timeoutDuration time.Duration,
) (map[string]any, string, error) {
	clientName := defaultOr(in.WechatideClient, "Copilot")
	payload, output, err := runWechatideTool(
		projectRoot,
		clientName,
		"upload",
		wechatideUploadArgs(projectRoot, in.Version, in.Description),
		timeoutDuration,
	)
	if err != nil {
		return nil, "", err
	}
	status, taskID, message := classifyWechatidePayload(payload)
	if status == "uploaded" {
		return wechatideOutput(payload, output, "", "uploaded"), "", nil
	}
	if status == "failed" {
		return nil, "", fmt.Errorf("wechatide 上传失败: %s", message)
	}
	if taskID == "" {
		return nil, "", errors.New("wechatide 上传未返回 taskId")
	}

	deadline := time.Now().Add(timeoutDuration)
	taskNotFoundSince := time.Time{}
	for time.Now().Before(deadline) {
		time.Sleep(wechatidePollInterval)
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		pollPayload, pollOutput, err := runWechatideTool(
			projectRoot,
			clientName,
			"polling_task_result",
			[]string{"--task-id", taskID},
			remaining,
		)
		if err != nil {
			if isWechatideTaskNotFound(err.Error()) {
				if taskNotFoundSince.IsZero() {
					taskNotFoundSince = time.Now()
				}
				if time.Since(taskNotFoundSince) < 30*time.Second {
					continue
				}
			}
			return nil, taskID, err
		}
		taskNotFoundSince = time.Time{}
		pollStatus, _, pollMessage := classifyWechatidePayload(pollPayload)
		switch pollStatus {
		case "uploaded":
			return wechatideOutput(pollPayload, pollOutput, taskID, "uploaded"), taskID, nil
		case "failed":
			if isWechatideTaskNotFound(pollMessage) {
				if taskNotFoundSince.IsZero() {
					taskNotFoundSince = time.Now()
				}
				if time.Since(taskNotFoundSince) < 30*time.Second {
					continue
				}
			}
			return nil, taskID, fmt.Errorf("wechatide 上传失败: %s", pollMessage)
		}
	}
	return nil, taskID, fmt.Errorf(
		"wechatide 上传等待用户确认超时，taskId=%s；请确认开发者工具中的上传操作后重试",
		taskID,
	)
}

func isWechatideTaskNotFound(message string) bool {
	normalized := strings.ToLower(strings.TrimSpace(message))
	return strings.Contains(normalized, "task not found") ||
		strings.Contains(normalized, "任务不存在")
}

func wechatideUploadArgs(projectRoot, version, description string) []string {
	args := []string{
		"--project", projectRoot,
		"--upload-version", strings.TrimSpace(version),
	}
	if description := strings.TrimSpace(description); description != "" {
		args = append(args, "--desc", description)
	}
	return args
}

func classifyWechatidePayload(payload map[string]any) (status, taskID, message string) {
	result := wechatideResult(payload)
	taskID = firstNonEmpty(asString(result["taskId"]), asString(payload["taskId"]))
	message = firstNonEmpty(
		asString(payload["message"]),
		asString(result["message"]),
		asString(payload["errorType"]),
	)
	resultStatus := strings.ToLower(asString(result["status"]))
	if resultStatus == "" {
		resultStatus = strings.ToLower(asString(payload["status"]))
	}
	if resultText, ok := payload["result"].(string); ok {
		message = firstNonEmpty(message, resultText)
		if strings.TrimSpace(message) != "" {
			return "failed", taskID, message
		}
	}
	if ok, exists := payload["ok"].(bool); exists && !ok {
		return "failed", taskID, firstNonEmpty(message, "wechatide 调用失败")
	}
	if resultStatus == "failed" ||
		resultStatus == "error" ||
		resultStatus == "cancelled" ||
		resultStatus == "canceled" ||
		resultStatus == "expired" ||
		resultStatus == "denied" ||
		asBool(payload["error"]) {
		return "failed", taskID, message
	}
	if resultSuccess, ok := result["success"].(bool); ok && resultSuccess {
		return "uploaded", taskID, message
	}
	if resultStatus == "pending" ||
		resultStatus == "waiting" ||
		resultStatus == "confirmation" ||
		(taskID != "" &&
			resultStatus != "success" &&
			resultStatus != "succeeded" &&
			resultStatus != "completed" &&
			resultStatus != "uploaded") {
		return "pending", taskID, message
	}
	if asBool(result["success"]) ||
		resultStatus == "success" ||
		resultStatus == "succeeded" ||
		resultStatus == "completed" ||
		resultStatus == "uploaded" {
		return "uploaded", taskID, message
	}
	return "failed", taskID, firstNonEmpty(message, "未识别的 wechatide 返回状态")
}

func wechatideResult(payload map[string]any) map[string]any {
	if result, ok := payload["result"].(map[string]any); ok {
		return result
	}
	return map[string]any{}
}

func wechatideOutput(
	payload map[string]any,
	rawOutput string,
	taskID string,
	status string,
) map[string]any {
	return map[string]any{
		"channel":    "wechatide",
		"status":     status,
		"task_id":    taskID,
		"result":     wechatideResult(payload),
		"raw_output": tail(rawOutput, 4000),
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func asBool(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}

func resolveBuildTarget(venueValue, environmentValue string) (string, string, error) {
	venue := strings.ToLower(strings.TrimSpace(venueValue))
	if venue == "" {
		return "", "", errors.New("venue 不能为空")
	}
	if !venuePattern.MatchString(venue) {
		return "", "", fmt.Errorf("venue 取值非法: %s", venueValue)
	}
	environment := strings.ToLower(strings.TrimSpace(environmentValue))
	if environment == "" {
		environment = "development"
	}
	if environment != "development" && environment != "production" {
		return "", "", fmt.Errorf("environment 取值非法: %s", environmentValue)
	}
	return venue, environment, nil
}

func projectConfigAppID(projectRoot string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(projectRoot, "project.config.json"))
	if err != nil {
		return "", fmt.Errorf("读取 project.config.json 失败: %w", err)
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		return "", fmt.Errorf("project.config.json 格式无效: %w", err)
	}
	appID := strings.TrimSpace(asString(config["appid"]))
	if appID == "" {
		return "", errors.New("project.config.json 缺少 appid")
	}
	return appID, nil
}

func resolveProjectAppID(inputAppID, projectAppID string) (string, error) {
	inputAppID = strings.TrimSpace(inputAppID)
	projectAppID = strings.TrimSpace(projectAppID)
	if projectAppID == "" {
		return "", errors.New("project.config.json 缺少 appid")
	}
	if inputAppID != "" && inputAppID != projectAppID {
		return "", fmt.Errorf(
			"AppID 不一致: 输入为 %s，project.config.json 为 %s",
			inputAppID,
			projectAppID,
		)
	}
	return projectAppID, nil
}

func runCIRunner(
	projectRoot string,
	payload map[string]any,
	timeout time.Duration,
) (map[string]any, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	runner := filepath.Join(filepath.Dir(executable), "miniprogram-ci-runner.js")
	if !isFile(runner) {
		return nil, fmt.Errorf("缺少 miniprogram-ci runner: %s", runner)
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	payloadPath, err := os.CreateTemp("", "himind-wechat-ci-*.json")
	if err != nil {
		return nil, err
	}
	payloadName := payloadPath.Name()
	defer os.Remove(payloadName)
	if _, err := payloadPath.Write(payloadBytes); err != nil {
		_ = payloadPath.Close()
		return nil, err
	}
	if err := payloadPath.Close(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, "node", runner)
	configureHiddenCommand(command)
	command.Dir = projectRoot
	command.Env = append(os.Environ(), "HIMIND_WECHAT_CI_PAYLOAD="+payloadName)
	output, err := command.CombinedOutput()
	text := tail(string(output), 32000)
	if ctx.Err() != nil {
		return nil, errors.New("miniprogram-ci 执行超时")
	}
	if err != nil {
		return nil, fmt.Errorf("miniprogram-ci 执行失败: %v: %s", err, text)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return map[string]any{"output": text}, nil
	}
	return result, nil
}

func runScript(projectRoot, manager, script string, timeout time.Duration) (string, error) {
	switch manager {
	case "npm":
		return runCommand(projectRoot, manager, []string{"run", script, "--if-present"}, timeout)
	case "pnpm":
		return runCommand(projectRoot, manager, []string{"run", script}, timeout)
	case "yarn":
		return runCommand(projectRoot, manager, []string{"run", script}, timeout)
	default:
		return "", errors.New("package_manager 必须是 npm、pnpm 或 yarn")
	}
}

func runCommand(directory, name string, args []string, timeout time.Duration) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("未找到命令 %s", name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var command *exec.Cmd
	if runtime.GOOS == "windows" && isWindowsScript(path) {
		commandArgs := []string{"/d", "/s", "/c", "call", path}
		commandArgs = append(commandArgs, args...)
		command = exec.CommandContext(ctx, "cmd.exe", commandArgs...)
	} else {
		command = exec.CommandContext(ctx, path, args...)
	}
	configureHiddenCommand(command)
	command.Dir = directory
	output, err := command.CombinedOutput()
	text := tail(string(output), 32000)
	if ctx.Err() != nil {
		return text, fmt.Errorf("命令执行超时: %s", name)
	}
	if err != nil {
		return text, fmt.Errorf("命令执行失败 %s: %v: %s", name, err, text)
	}
	return text, nil
}

func ensureWorkspace(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("workspace_root 或 project_root 不能为空")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("workspace path 必须是目录")
	}
	return filepath.Clean(absolute), nil
}

func resolveFile(base, path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("file path 不能为空")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}

func ensureWithin(workspace, target string) (string, error) {
	workspaceAbs, err := filepath.Abs(strings.TrimSpace(workspace))
	if err != nil {
		return "", err
	}
	targetAbs, err := filepath.Abs(strings.TrimSpace(target))
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(workspaceAbs, targetAbs)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || filepath.IsAbs(relative) {
		return "", errors.New("path 必须位于 workspace_root 内")
	}
	return filepath.Clean(targetAbs), nil
}

func ensureOutputWithin(workspace, target string) (string, error) {
	if strings.TrimSpace(workspace) == "" {
		return "", errors.New("workspace path 不能为空")
	}
	if strings.TrimSpace(target) == "" {
		return "", errors.New("output path 不能为空")
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(workspace, target)
	}
	return ensureWithin(workspace, target)
}

func artifactFor(path, id, artifactType, name string) (artifact, error) {
	info, err := os.Stat(path)
	if err != nil {
		return artifact{}, err
	}
	hash, err := sha256File(path)
	if err != nil {
		return artifact{}, err
	}
	return artifact{
		ArtifactID:   id,
		ArtifactType: artifactType,
		Name:         name,
		URI:          fileURI(path),
		SHA256:       hash,
		SizeBytes:    info.Size(),
	}, nil
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func fileURI(path string) string {
	absolute, _ := filepath.Abs(path)
	return "file:///" + strings.ReplaceAll(filepath.ToSlash(absolute), " ", "%20")
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func defaultOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func timeout(value int, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return time.Duration(value) * time.Second
}

func tail(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[len(value)-limit:]
}

func isWindowsScript(path string) bool {
	extension := strings.ToLower(filepath.Ext(path))
	return extension == ".cmd" || extension == ".bat"
}
