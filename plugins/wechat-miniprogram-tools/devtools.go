package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/MrBaoquan/himind-extensions/sdk/jsonrpc"
)

const wechatideBridgeVersion = "0.3.9"

type devtoolsInput struct {
	WorkspaceRoot      string   `json:"workspace_root"`
	ProjectRoot        string   `json:"project_root"`
	AppID              string   `json:"app_id"`
	Env                string   `json:"env"`
	Action             string   `json:"action"`
	DataAction         string   `json:"data_action"`
	StructAction       string   `json:"struct_action"`
	StorageAction      string   `json:"storage_action"`
	Link               string   `json:"link"`
	Page               string   `json:"page"`
	PagePath           string   `json:"page_path"`
	Query              string   `json:"query"`
	Scene              *int     `json:"scene"`
	CompileType        string   `json:"compile_type"`
	QRFormat           string   `json:"qr_format"`
	QROutput           string   `json:"qr_output"`
	Pattern            string   `json:"pattern"`
	Path               string   `json:"path"`
	Wait               *int     `json:"wait"`
	WaitForSelector    string   `json:"wait_for_selector"`
	Selector           string   `json:"selector"`
	Value              string   `json:"value"`
	Name               string   `json:"name"`
	X                  *float64 `json:"x"`
	Y                  *float64 `json:"y"`
	StartX             *float64 `json:"start_x"`
	StartY             *float64 `json:"start_y"`
	EndX               *float64 `json:"end_x"`
	EndY               *float64 `json:"end_y"`
	Duration           *int     `json:"duration"`
	CoordinateSpace    string   `json:"coordinate_space"`
	ImageWidth         *int     `json:"image_width"`
	ImageHeight        *int     `json:"image_height"`
	DataPath           string   `json:"data_path"`
	Patch              any      `json:"patch"`
	Method             string   `json:"method"`
	Args               []any    `json:"args"`
	Result             any      `json:"result"`
	URL                string   `json:"url"`
	Delta              *int     `json:"delta"`
	ScrollTop          *float64 `json:"scroll_top"`
	FnSource           string   `json:"fn_source"`
	IncludeFailed      bool     `json:"include_failed"`
	ClearHistory       bool     `json:"clear_history"`
	CollectionName     string   `json:"collection_name"`
	QueryDoc           any      `json:"query"`
	Projection         any      `json:"projection"`
	Sort               any      `json:"sort"`
	Limit              *int     `json:"limit"`
	Offset             *int     `json:"offset"`
	Names              string   `json:"names"`
	CloudPath          string   `json:"cloud_path"`
	MaxAge             *int     `json:"max_age"`
	Documents          any      `json:"documents"`
	Update             any      `json:"update"`
	IsMulti            bool     `json:"is_multi"`
	Upsert             bool     `json:"upsert"`
	UpdateOptions      any      `json:"update_options"`
	LocalPath          string   `json:"local_path"`
	IsDirectory        bool     `json:"is_directory"`
	RemoteNPMInstall   bool     `json:"remote_npm_install"`
	File               string   `json:"file"`
	CacheAction        string   `json:"cache_action"`
	WXAction           string   `json:"wx_action"`
	TicketAction       string   `json:"ticket_action"`
	Ticket             string   `json:"ticket"`
	InstallRoot        string   `json:"install_root"`
	WaitSeconds        *int     `json:"wait_seconds"`
	WindowMode         string   `json:"window_mode"`
	PrivateKeyPath     string   `json:"private_key_path"`
	RequireCIInstalled bool     `json:"require_ci_installed"`
	Channel            string   `json:"channel"`
	Description        string   `json:"description"`
	Version            string   `json:"version"`
	UploadVersion      string   `json:"upload_version"`
	TaskID             string   `json:"task_id"`
	Type               string   `json:"type"`
	Scope              string   `json:"scope"`
	RuntimeAction      string   `json:"runtime_action"`
}

func isDevtoolsMethod(method string) bool {
	return strings.HasPrefix(method, "wechat.miniprogram.devtools.") ||
		strings.HasPrefix(method, "wechat.miniprogram.cloud.")
}

func handleDevtools(request jsonrpc.Request) (any, *jsonrpc.Error) {
	var in devtoolsInput
	if rpcError := jsonrpc.DecodeParams(request, &in); rpcError != nil {
		return nil, rpcError
	}
	switch request.Method {
	case "wechat.miniprogram.devtools.environment.get":
		return devtoolsEnvironment(in)
	case "wechat.miniprogram.devtools.login.start":
		return devtoolsLoginStart(in)
	case "wechat.miniprogram.devtools.task.get":
		return devtoolsTaskGet(in)
	case "wechat.miniprogram.devtools.project.list":
		return devtoolsProjectList(in)
	case "wechat.miniprogram.devtools.project.import":
		return devtoolsProjectImport(in)
	case "wechat.miniprogram.devtools.project.remove":
		return devtoolsProjectRemove(in)
	case "wechat.miniprogram.devtools.window.manage":
		return devtoolsWindowManage(in)
	case "wechat.miniprogram.devtools.compile.inspect":
		return devtoolsCompileInspect(in)
	case "wechat.miniprogram.devtools.compile.run":
		return devtoolsCompileRun(in)
	case "wechat.miniprogram.devtools.preview.create":
		return devtoolsPreviewCreate(in)
	case "wechat.miniprogram.devtools.debug.inspect":
		return devtoolsDebugInspect(in)
	case "wechat.miniprogram.devtools.debug.mutate":
		return devtoolsDebugMutate(in)
	case "wechat.miniprogram.devtools.automation.element":
		return devtoolsAutomationElement(in)
	case "wechat.miniprogram.devtools.automation.page":
		return devtoolsAutomationPage(in)
	case "wechat.miniprogram.devtools.automation.game":
		return devtoolsAutomationGame(in)
	case "wechat.miniprogram.devtools.automation.evaluate":
		return devtoolsAutomationEvaluate(in)
	case "wechat.miniprogram.cloud.read":
		return devtoolsCloudRead(in)
	case "wechat.miniprogram.cloud.write":
		return devtoolsCloudWrite(in)
	case "wechat.miniprogram.devtools.installer.check":
		return devtoolsInstallerCheck(in)
	default:
		return nil, jsonrpc.InvalidParams("unsupported WeChat devtools capability")
	}
}

func devtoolsBlocker(code, message, remediation string, retryable bool) map[string]any {
	return map[string]any{"code": code, "message": message, "remediation": remediation, "retryable": retryable}
}

func devtoolsResult(operation string) map[string]any {
	return map[string]any{"ok": true, "operation": operation, "status": "success", "blockers": []map[string]any{}}
}

func resolveDevtoolsProject(in devtoolsInput) (string, string, error) {
	workspace, err := ensureWorkspace(in.WorkspaceRoot)
	if err != nil {
		return "", "", err
	}
	project, err := ensureWorkspace(defaultOr(in.ProjectRoot, workspace))
	if err != nil {
		return "", "", err
	}
	project, err = ensureWithin(workspace, project)
	if err != nil {
		return "", "", err
	}
	return workspace, project, nil
}

func readProjectConfig(projectRoot string) (map[string]any, error) {
	data, err := os.ReadFile(filepath.Join(projectRoot, "project.config.json"))
	if err != nil {
		return nil, err
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	return config, nil
}

func runWechatide(tool string, args []string, commandTimeout time.Duration) (map[string]any, error) {
	return runWechatideWithClient(tool, args, "", commandTimeout)
}

func runWechatideWithClient(tool string, args []string, clientOverride string, commandTimeout time.Duration) (map[string]any, error) {
	if os.Getenv("HIMIND_WECHATIDE_FAKE") == "1" {
		return fakeWechatideOutput(tool), nil
	}
	binary := strings.TrimSpace(os.Getenv("HIMIND_WECHATIDE_BIN"))
	var resolved string
	var err error
	if binary != "" {
		resolved, err = exec.LookPath(binary)
	} else {
		resolved, err = resolveWechatideExecutable()
		if err != nil {
			if path, lookErr := exec.LookPath("wechatide"); lookErr == nil {
				resolved = path
				err = nil
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("WECHATIDE_CLI_UNAVAILABLE: %w", err)
	}
	client := strings.TrimSpace(clientOverride)
	if client == "" {
		client = strings.TrimSpace(os.Getenv("HIMIND_WECHATIDE_CLIENT"))
	}
	if client == "" {
		client = "Copilot"
	}
	if !validClientName(client) {
		return nil, errors.New("WECHATIDE_CLIENT_INVALID")
	}
	full := []string{"-c", client, tool}
	full = append(full, args...)
	directory, dirErr := os.Getwd()
	if dirErr != nil {
		directory = "."
	}
	text, err := runCommand(directory, resolved, full, commandTimeout)
	if err != nil {
		if strings.Contains(err.Error(), "超时") {
			return nil, errors.New("WECHATIDE_TIMEOUT")
		}
		return nil, fmt.Errorf("WECHATIDE_COMMAND_FAILED: %v: %s", err, tail(text, 8000))
	}
	var result map[string]any
	if json.Unmarshal([]byte(text), &result) == nil {
		return result, nil
	}
	if parsed, parseErr := parseLastJSONObject(text); parseErr == nil {
		return parsed, nil
	}
	return map[string]any{"output": text}, nil
}

func validClientName(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, ch := range value {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == ' ' || ch == '-' || ch == '_' || ch == '.' {
			continue
		}
		return false
	}
	return true
}

func fakeWechatideOutput(tool string) map[string]any {
	switch tool {
	case "check_wechatide_status":
		return map[string]any{"versionRelation": "equal", "loginExpired": false, "tokenRequired": false, "loginUser": "fake-user"}
	case "project_list":
		return map[string]any{"projects": []any{}}
	case "open_project_window":
		return map[string]any{"windowId": "fake-window"}
	case "simulator_screenshot":
		return map[string]any{"path": "fake.png", "imageWidth": 1280, "imageHeight": 720}
	case "get_simulator_console", "get_simulator_network":
		return map[string]any{"lines": []any{}}
	case "automation_runtime_info":
		return map[string]any{"currentPage": "pages/index/index"}
	case "polling_task_result":
		return map[string]any{"status": "success"}
	case "upload":
		return map[string]any{"status": "pending", "taskId": "fake-task"}
	default:
		return map[string]any{"ok": true, "tool": tool, "fake": true}
	}
}

func devtoolsEnvironment(in devtoolsInput) (any, *jsonrpc.Error) {
	workspace, err := ensureWorkspace(in.WorkspaceRoot)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	result := devtoolsResult("wechat.miniprogram.devtools.environment.get")
	result["workspace_root"] = workspace
	status, statusErr := runWechatide("check_wechatide_status", []string{"--skill-version", wechatideBridgeVersion}, 20*time.Second)
	if statusErr != nil {
		result["ok"] = false
		result["status"] = "blocked"
		result["ready"] = false
		result["blockers"] = []map[string]any{devtoolsBlocker("WECHATIDE_CLI_UNAVAILABLE", statusErr.Error(), "安装或修复微信开发者工具 CLI，并确认非沙箱桌面会话", true)}
		return result, nil
	}
	result["ready"] = true
	result["status_detail"] = status
	if strings.TrimSpace(in.ProjectRoot) != "" {
		_, project, projectErr := resolveDevtoolsProject(in)
		if projectErr != nil {
			result["ok"] = false
			result["ready"] = false
			result["blockers"] = []map[string]any{devtoolsBlocker("PROJECT_PATH_INVALID", projectErr.Error(), "确认 project_root 位于 workspace_root 内", true)}
			return result, nil
		}
		if _, configErr := readProjectConfig(project); configErr != nil {
			result["ok"] = false
			result["ready"] = false
			result["blockers"] = []map[string]any{devtoolsBlocker("PROJECT_CONFIG_INVALID", configErr.Error(), "修正 project.config.json 后重试", true)}
		}
	}
	return result, nil
}

func devtoolsLoginStart(in devtoolsInput) (any, *jsonrpc.Error) {
	if _, err := ensureWorkspace(in.WorkspaceRoot); err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	loginType := defaultOr(in.Type, "window")
	if loginType != "window" && loginType != "image" {
		return nil, jsonrpc.InvalidParams("type 必须是 window 或 image")
	}
	result, err := runWechatide("login", []string{"--type", loginType}, 30*time.Second)
	if err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	response := devtoolsResult("wechat.miniprogram.devtools.login.start")
	response["login"] = result
	if status, _ := result["status"].(string); status == "pending" {
		response["status"] = "pending"
		response["task_id"] = result["taskId"]
	}
	return response, nil
}

func devtoolsTaskGet(in devtoolsInput) (any, *jsonrpc.Error) {
	if _, err := ensureWorkspace(in.WorkspaceRoot); err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	if strings.TrimSpace(in.TaskID) == "" {
		return nil, jsonrpc.InvalidParams("task_id 不能为空")
	}
	waitSeconds := 0
	if in.WaitSeconds != nil {
		waitSeconds = *in.WaitSeconds
	}
	if waitSeconds < 0 || waitSeconds > 30 {
		return nil, jsonrpc.InvalidParams("wait_seconds 必须在 0 到 30 之间")
	}
	deadline := time.Now().Add(time.Duration(waitSeconds) * time.Second)
	var last map[string]any
	for {
		result, err := runWechatide("polling_task_result", []string{"--task-id", in.TaskID}, 30*time.Second)
		if err != nil {
			return nil, jsonrpc.InternalError(err.Error())
		}
		last = result
		status, _ := result["status"].(string)
		if status != "pending" || time.Now().After(deadline) {
			break
		}
		time.Sleep(2 * time.Second)
	}
	response := devtoolsResult("wechat.miniprogram.devtools.task.get")
	response["task"] = last
	if status, _ := last["status"].(string); status != "" {
		response["status"] = status
	}
	response["task_id"] = in.TaskID
	return response, nil
}

func devtoolsProjectList(in devtoolsInput) (any, *jsonrpc.Error) {
	if _, err := ensureWorkspace(in.WorkspaceRoot); err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	scope := defaultOr(in.Scope, "miniprogram")
	if scope != "miniprogram" && scope != "other" && scope != "all" {
		return nil, jsonrpc.InvalidParams("scope 必须是 miniprogram、other 或 all")
	}
	result, err := runWechatide("project_list", []string{"--scope", scope}, 30*time.Second)
	if err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	response := devtoolsResult("wechat.miniprogram.devtools.project.list")
	response["projects"] = result
	return response, nil
}

func devtoolsProjectImport(in devtoolsInput) (any, *jsonrpc.Error) {
	_, project, err := resolveDevtoolsProject(in)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	action := defaultOr(in.Action, "import")
	switch action {
	case "import":
		result, runErr := runWechatide("project_import", []string{"--project", project}, 60*time.Second)
		if runErr != nil {
			return nil, jsonrpc.InternalError(runErr.Error())
		}
		return map[string]any{"ok": true, "operation": "wechat.miniprogram.devtools.project.import", "action": action, "result": result}, nil
	case "share":
		result, runErr := runWechatide("share_minicode", []string{"--project", project}, 120*time.Second)
		if runErr != nil {
			return nil, jsonrpc.InternalError(runErr.Error())
		}
		return map[string]any{"ok": true, "operation": "wechat.miniprogram.devtools.project.import", "action": action, "result": result}, nil
	case "import_minicode":
		if err := validateMiniCodeLink(in.Link); err != nil {
			return nil, jsonrpc.InvalidParams(err.Error())
		}
		result, runErr := runWechatide("import_minicode", []string{"--link", in.Link}, 120*time.Second)
		if runErr != nil {
			return nil, jsonrpc.InternalError(runErr.Error())
		}
		return map[string]any{"ok": true, "operation": "wechat.miniprogram.devtools.project.import", "action": action, "result": result, "requires_review": true, "review_note": "导入后必须先检查项目配置、依赖脚本和可执行代码，再打开项目窗口"}, nil
	default:
		return nil, jsonrpc.InvalidParams("action 必须是 import、share 或 import_minicode")
	}
}

func validateMiniCodeLink(link string) error {
	parsed, err := url.Parse(strings.TrimSpace(link))
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return errors.New("import_minicode 只接受 HTTPS 链接")
	}
	host := strings.ToLower(parsed.Hostname())
	allowed := host == "developers.weixin.qq.com" || host == "mp.weixin.qq.com" || host == "servicewechat.com" || strings.HasSuffix(host, ".weixin.qq.com") || strings.HasSuffix(host, ".servicewechat.com")
	if !allowed {
		return errors.New("import_minicode 链接域名不在允许列表内")
	}
	return nil
}

func devtoolsProjectRemove(in devtoolsInput) (any, *jsonrpc.Error) {
	_, project, err := resolveDevtoolsProject(in)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	result, runErr := runWechatide("project_remove", []string{"--project", project}, 60*time.Second)
	if runErr != nil {
		return nil, jsonrpc.InternalError(runErr.Error())
	}
	return map[string]any{"ok": true, "operation": "wechat.miniprogram.devtools.project.remove", "result": result}, nil
}

func devtoolsWindowManage(in devtoolsInput) (any, *jsonrpc.Error) {
	_, project, err := resolveDevtoolsProject(in)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	action := strings.TrimSpace(in.Action)
	switch action {
	case "open":
		mode := defaultOr(in.WindowMode, "liteMode")
		if mode != "liteMode" && mode != "fullMode" {
			return nil, jsonrpc.InvalidParams("window_mode 必须是 liteMode 或 fullMode")
		}
		result, runErr := runWechatide("open_project_window", []string{"--project", project, "--window-mode", mode}, 120*time.Second)
		if runErr != nil {
			return nil, jsonrpc.InternalError(runErr.Error())
		}
		return map[string]any{"ok": true, "operation": "wechat.miniprogram.devtools.window.manage", "action": action, "result": result}, nil
	case "close":
		result, runErr := runWechatide("close_project_window", []string{"--project", project}, 60*time.Second)
		if runErr != nil {
			return nil, jsonrpc.InternalError(runErr.Error())
		}
		return map[string]any{"ok": true, "operation": "wechat.miniprogram.devtools.window.manage", "action": action, "result": result}, nil
	case "quit":
		result, runErr := runWechatide("quit", nil, 60*time.Second)
		if runErr != nil {
			return nil, jsonrpc.InternalError(runErr.Error())
		}
		return map[string]any{"ok": true, "operation": "wechat.miniprogram.devtools.window.manage", "action": action, "result": result}, nil
	default:
		return nil, jsonrpc.InvalidParams("action 必须是 open、close 或 quit")
	}
}

func devtoolsCompileInspect(in devtoolsInput) (any, *jsonrpc.Error) {
	_, project, err := resolveDevtoolsProject(in)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	if strings.TrimSpace(in.File) == "" {
		return nil, jsonrpc.InvalidParams("file 不能为空")
	}
	filePath, err := resolveFile(project, in.File)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	filePath, err = ensureWithin(project, filePath)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	tool := "compile_wxml"
	if strings.HasSuffix(strings.ToLower(filePath), ".wxss") {
		tool = "compile_wxss"
	}
	compileType := defaultOr(in.CompileType, "miniprogram")
	if compileType != "miniprogram" && compileType != "plugin" {
		return nil, jsonrpc.InvalidParams("compile_type 必须是 miniprogram 或 plugin")
	}
	result, runErr := runWechatide(tool, []string{"--project", project, "--file-path", filePath, "--compile-type", compileType}, 60*time.Second)
	if runErr != nil {
		return nil, jsonrpc.InternalError(runErr.Error())
	}
	return map[string]any{"ok": true, "operation": "wechat.miniprogram.devtools.compile.inspect", "result": result}, nil
}

func devtoolsCompileRun(in devtoolsInput) (any, *jsonrpc.Error) {
	_, project, err := resolveDevtoolsProject(in)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	action := strings.TrimSpace(in.Action)
	args := []string{"--project", project}
	var tool string
	switch action {
	case "open_page":
		if strings.TrimSpace(in.Page) == "" {
			return nil, jsonrpc.InvalidParams("page 不能为空")
		}
		tool = "simulator_open_page"
		args = append(args, "--page", in.Page)
		if in.Query != "" {
			args = append(args, "--query", in.Query)
		}
		if in.Scene != nil {
			args = append(args, "--scene", strconv.Itoa(*in.Scene))
		}
	case "refresh":
		tool = "simulator_refresh"
	case "build_npm":
		tool = "build_npm"
		if in.CompileType != "" {
			args = append(args, "--compile-type", in.CompileType)
		}
	default:
		return nil, jsonrpc.InvalidParams("action 必须是 open_page、refresh 或 build_npm")
	}
	result, runErr := runWechatide(tool, args, 180*time.Second)
	if runErr != nil {
		return nil, jsonrpc.InternalError(runErr.Error())
	}
	return map[string]any{"ok": true, "operation": "wechat.miniprogram.devtools.compile.run", "action": action, "result": result}, nil
}

func devtoolsPreviewCreate(in devtoolsInput) (any, *jsonrpc.Error) {
	workspace, project, err := resolveDevtoolsProject(in)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	action := defaultOr(in.Action, "auto")
	args := []string{"--project", project}
	var tool string
	switch action {
	case "auto":
		tool = "auto_preview"
		if in.PagePath != "" {
			args = append(args, "--page-path", in.PagePath)
		}
		if in.Query != "" {
			args = append(args, "--query", in.Query)
		}
		if in.Scene != nil {
			args = append(args, "--scene", strconv.Itoa(*in.Scene))
		}
	case "qrcode":
		tool = "create_preview_qrcode"
		format := defaultOr(in.QRFormat, "window")
		if format != "window" && format != "image" {
			return nil, jsonrpc.InvalidParams("qr_format 必须是 window 或 image")
		}
		args = append(args, "--qr-format", format)
		if in.QROutput != "" {
			output, outputErr := ensureOutputWithin(workspace, in.QROutput)
			if outputErr != nil {
				return nil, jsonrpc.InvalidParams(outputErr.Error())
			}
			args = append(args, "--qr-output", output)
		}
		if in.PagePath != "" {
			args = append(args, "--page-path", in.PagePath)
		}
		if in.Query != "" {
			args = append(args, "--query", in.Query)
		}
		if in.Scene != nil {
			args = append(args, "--scene", strconv.Itoa(*in.Scene))
		}
	default:
		return nil, jsonrpc.InvalidParams("action 必须是 auto 或 qrcode")
	}
	result, runErr := runWechatide(tool, args, 300*time.Second)
	if runErr != nil {
		return nil, jsonrpc.InternalError(runErr.Error())
	}
	return map[string]any{"ok": true, "operation": "wechat.miniprogram.devtools.preview.create", "action": action, "result": result}, nil
}

func devtoolsDebugInspect(in devtoolsInput) (any, *jsonrpc.Error) {
	workspace, project, err := resolveDevtoolsProject(in)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	action := strings.TrimSpace(in.Action)
	args := []string{"--project", project}
	var tool string
	switch action {
	case "runtime_info":
		tool = "automation_runtime_info"
		runtimeAction := defaultOr(in.RuntimeAction, "currentPage")
		if runtimeAction != "pageStack" && runtimeAction != "currentPage" && runtimeAction != "systemInfo" {
			return nil, jsonrpc.InvalidParams("runtime_action 必须是 pageStack、currentPage 或 systemInfo")
		}
		args = append(args, "--action", runtimeAction)
	case "console", "network":
		if action == "console" {
			tool = "get_simulator_console"
		} else {
			tool = "get_simulator_network"
		}
		command := "grep -n ."
		pattern := strings.TrimSpace(in.Pattern)
		if pattern != "" {
			if len(pattern) > 200 || strings.ContainsAny(pattern, "\r\n") {
				return nil, jsonrpc.InvalidParams("pattern 过长或包含换行")
			}
			command = "grep -n -F -- " + pattern
		}
		args = append(args, "--command", command)
	case "screenshot":
		tool = "simulator_screenshot"
		if in.Path != "" {
			output, outputErr := ensureOutputWithin(workspace, in.Path)
			if outputErr != nil {
				return nil, jsonrpc.InvalidParams(outputErr.Error())
			}
			args = append(args, "--path", output)
		}
		if in.Wait != nil {
			args = append(args, "--wait", strconv.Itoa(*in.Wait))
		}
		if in.WaitForSelector != "" {
			args = append(args, "--wait-for-selector", in.WaitForSelector)
		}
	default:
		return nil, jsonrpc.InvalidParams("action 必须是 runtime_info、console、network 或 screenshot")
	}
	result, runErr := runWechatide(tool, args, 120*time.Second)
	if runErr != nil {
		return nil, jsonrpc.InternalError(runErr.Error())
	}
	return map[string]any{"ok": true, "operation": "wechat.miniprogram.devtools.debug.inspect", "action": action, "result": result}, nil
}

func devtoolsDebugMutate(in devtoolsInput) (any, *jsonrpc.Error) {
	_, project, err := resolveDevtoolsProject(in)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	action := strings.TrimSpace(in.Action)
	args := []string{"--project", project}
	var tool string
	switch action {
	case "clear_cache":
		tool = "debug_clear_cache"
		if in.CacheAction == "" {
			return nil, jsonrpc.InvalidParams("cache_action 不能为空")
		}
		args = append(args, "--action", in.CacheAction)
	case "wx_api":
		tool = "automation_wx_api"
		if in.WXAction == "" || in.Method == "" {
			return nil, jsonrpc.InvalidParams("wx_action 和 method 不能为空")
		}
		args = append(args, "--action", in.WXAction, "--method", in.Method)
		if len(in.Args) > 0 {
			path, pathErr := writeTempJSON(in.Args)
			if pathErr != nil {
				return nil, jsonrpc.InternalError(pathErr.Error())
			}
			defer os.Remove(path)
			args = append(args, "--args-file", path)
		}
		if in.Result != nil {
			path, pathErr := writeTempJSON(in.Result)
			if pathErr != nil {
				return nil, jsonrpc.InternalError(pathErr.Error())
			}
			defer os.Remove(path)
			args = append(args, "--result-file", path)
		}
	case "testaccount":
		tool = "automation_testaccount"
		if in.TicketAction == "" {
			return nil, jsonrpc.InvalidParams("ticket_action 不能为空")
		}
		args = append(args, "--action", in.TicketAction)
		if in.Ticket != "" {
			args = append(args, "--ticket", in.Ticket)
		}
	default:
		return nil, jsonrpc.InvalidParams("action 必须是 clear_cache、wx_api 或 testaccount")
	}
	result, runErr := runWechatide(tool, args, 120*time.Second)
	if runErr != nil {
		return nil, jsonrpc.InternalError(runErr.Error())
	}
	return map[string]any{"ok": true, "operation": "wechat.miniprogram.devtools.debug.mutate", "action": action, "result": redactTicket(result)}, nil
}

func redactTicket(value map[string]any) map[string]any {
	result := make(map[string]any, len(value))
	for key, item := range value {
		if strings.EqualFold(key, "ticket") {
			result["ticket_configured"] = item != nil && item != ""
			continue
		}
		result[key] = item
	}
	return result
}

func devtoolsAutomationElement(in devtoolsInput) (any, *jsonrpc.Error) {
	_, project, err := resolveDevtoolsProject(in)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	if strings.TrimSpace(in.Selector) == "" {
		return nil, jsonrpc.InvalidParams("selector 不能为空")
	}
	action := strings.TrimSpace(in.Action)
	if action == "" {
		return nil, jsonrpc.InvalidParams("action 不能为空")
	}
	args := []string{"--project", project, "--selector", in.Selector, "--action", action}
	if in.Value != "" {
		args = append(args, "--value", in.Value)
	}
	if in.Name != "" {
		args = append(args, "--name", in.Name)
	}
	if in.X != nil {
		args = append(args, "--x", formatFloat(*in.X))
	}
	if in.Y != nil {
		args = append(args, "--y", formatFloat(*in.Y))
	}
	if in.Wait != nil {
		args = append(args, "--wait", strconv.Itoa(*in.Wait))
	}
	if in.WaitForSelector != "" {
		args = append(args, "--wait-for-selector", in.WaitForSelector)
	}
	result, runErr := runWechatide("automation_element_action", args, 120*time.Second)
	if runErr != nil {
		return nil, jsonrpc.InternalError(runErr.Error())
	}
	return map[string]any{"ok": true, "operation": "wechat.miniprogram.devtools.automation.element", "action": action, "result": result}, nil
}

func devtoolsAutomationPage(in devtoolsInput) (any, *jsonrpc.Error) {
	_, project, err := resolveDevtoolsProject(in)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	action := strings.TrimSpace(in.Action)
	if action == "" {
		return nil, jsonrpc.InvalidParams("action 不能为空")
	}
	var tool string
	args := []string{"--project", project}
	switch action {
	case "navigateTo", "redirectTo", "navigateBack", "reLaunch", "switchTab":
		tool = "automation_navigate"
		args = append(args, "--action", action)
		if in.URL != "" {
			args = append(args, "--url", in.URL)
		}
		if in.Delta != nil {
			args = append(args, "--delta", strconv.Itoa(*in.Delta))
		}
	case "pageScrollTo":
		tool = "automation_viewport_action"
		args = append(args, "--action", action)
		if in.ScrollTop != nil {
			args = append(args, "--scroll-top", formatFloat(*in.ScrollTop))
		}
	default:
		tool = "automation_page_action"
		args = append(args, "--action", action)
		if in.Selector != "" {
			args = append(args, "--selector", in.Selector)
		}
		if in.DataPath != "" {
			args = append(args, "--data-path", in.DataPath)
		}
		if in.Patch != nil {
			path, pathErr := writeTempJSON(in.Patch)
			if pathErr != nil {
				return nil, jsonrpc.InternalError(pathErr.Error())
			}
			defer os.Remove(path)
			args = append(args, "--patch-file", path)
		}
		if in.Method != "" {
			args = append(args, "--method", in.Method)
		}
		if len(in.Args) > 0 {
			path, pathErr := writeTempJSON(in.Args)
			if pathErr != nil {
				return nil, jsonrpc.InternalError(pathErr.Error())
			}
			defer os.Remove(path)
			args = append(args, "--args-file", path)
		}
	}
	if in.Wait != nil {
		args = append(args, "--wait", strconv.Itoa(*in.Wait))
	}
	if in.WaitForSelector != "" {
		args = append(args, "--wait-for-selector", in.WaitForSelector)
	}
	result, runErr := runWechatide(tool, args, 120*time.Second)
	if runErr != nil {
		return nil, jsonrpc.InternalError(runErr.Error())
	}
	return map[string]any{"ok": true, "operation": "wechat.miniprogram.devtools.automation.page", "action": action, "result": result}, nil
}

func devtoolsAutomationGame(in devtoolsInput) (any, *jsonrpc.Error) {
	_, project, err := resolveDevtoolsProject(in)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	action := strings.TrimSpace(in.Action)
	if action == "" {
		return nil, jsonrpc.InvalidParams("action 不能为空")
	}
	args := []string{"--project", project, "--action", action}
	if in.X != nil {
		args = append(args, "--x", formatFloat(*in.X))
	}
	if in.Y != nil {
		args = append(args, "--y", formatFloat(*in.Y))
	}
	if in.StartX != nil {
		args = append(args, "--start-x", formatFloat(*in.StartX))
	}
	if in.StartY != nil {
		args = append(args, "--start-y", formatFloat(*in.StartY))
	}
	if in.EndX != nil {
		args = append(args, "--end-x", formatFloat(*in.EndX))
	}
	if in.EndY != nil {
		args = append(args, "--end-y", formatFloat(*in.EndY))
	}
	if in.Duration != nil {
		args = append(args, "--duration", strconv.Itoa(*in.Duration))
	}
	if in.CoordinateSpace != "" {
		args = append(args, "--coordinate-space", in.CoordinateSpace)
	}
	if in.ImageWidth != nil {
		args = append(args, "--image-width", strconv.Itoa(*in.ImageWidth))
	}
	if in.ImageHeight != nil {
		args = append(args, "--image-height", strconv.Itoa(*in.ImageHeight))
	}
	if in.Wait != nil {
		args = append(args, "--wait", strconv.Itoa(*in.Wait))
	}
	result, runErr := runWechatide("automation_game_action", args, 120*time.Second)
	if runErr != nil {
		return nil, jsonrpc.InternalError(runErr.Error())
	}
	return map[string]any{"ok": true, "operation": "wechat.miniprogram.devtools.automation.game", "action": action, "result": result}, nil
}

func devtoolsAutomationEvaluate(in devtoolsInput) (any, *jsonrpc.Error) {
	_, project, err := resolveDevtoolsProject(in)
	if err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	action := defaultOr(in.Action, "evaluate")
	switch action {
	case "evaluate":
		if strings.TrimSpace(in.FnSource) == "" {
			return nil, jsonrpc.InvalidParams("fn_source 不能为空")
		}
		result, runErr := runWechatide("automation_evaluate", []string{"--project", project, "--fn-source", in.FnSource}, 120*time.Second)
		if runErr != nil {
			return nil, jsonrpc.InternalError(runErr.Error())
		}
		return map[string]any{"ok": true, "operation": "wechat.miniprogram.devtools.automation.evaluate", "action": action, "result": result}, nil
	case "generate_script":
		args := []string{}
		if in.IncludeFailed {
			args = append(args, "--include-failed")
		}
		if in.ClearHistory {
			args = append(args, "--clear-history")
		}
		result, runErr := runWechatide("automation_generate_script", args, 120*time.Second)
		if runErr != nil {
			return nil, jsonrpc.InternalError(runErr.Error())
		}
		return map[string]any{"ok": true, "operation": "wechat.miniprogram.devtools.automation.evaluate", "action": action, "result": result, "requires_review": true}, nil
	default:
		return nil, jsonrpc.InvalidParams("action 必须是 evaluate 或 generate_script")
	}
}

func devtoolsCloudRead(in devtoolsInput) (any, *jsonrpc.Error) {
	action := strings.TrimSpace(in.Action)
	var tool string
	args := []string{}
	switch action {
	case "env_list":
		tool = "cloud_env_list"
		if in.AppID != "" {
			args = append(args, "--appid", in.AppID)
		}
		if in.ProjectRoot != "" {
			if _, project, err := resolveDevtoolsProject(in); err == nil {
				args = append(args, "--project", project)
			}
		}
	case "fn_list":
		tool = "cloud_fn_list"
		if in.AppID != "" {
			args = append(args, "--appid", in.AppID)
		}
		if in.Env == "" {
			return nil, jsonrpc.InvalidParams("env 不能为空")
		}
		args = append(args, "--env", in.Env)
	case "fn_info":
		tool = "cloud_fn_info"
		if in.Env == "" || in.Names == "" {
			return nil, jsonrpc.InvalidParams("env 和 names 不能为空")
		}
		if in.AppID != "" {
			args = append(args, "--appid", in.AppID)
		}
		args = append(args, "--env", in.Env, "--names", in.Names)
	case "db_read_doc":
		tool = "cloud_db_read_doc"
		if in.AppID == "" || in.Env == "" || in.CollectionName == "" {
			return nil, jsonrpc.InvalidParams("app_id、env 和 collection_name 不能为空")
		}
		args = append(args, "--appid", in.AppID, "--env", in.Env, "--collection-name", in.CollectionName)
		if in.QueryDoc != nil {
			path, pathErr := writeTempJSON(in.QueryDoc)
			if pathErr != nil {
				return nil, jsonrpc.InternalError(pathErr.Error())
			}
			defer os.Remove(path)
			args = append(args, "--query-file", path)
		}
		if in.Projection != nil {
			path, pathErr := writeTempJSON(in.Projection)
			if pathErr != nil {
				return nil, jsonrpc.InternalError(pathErr.Error())
			}
			defer os.Remove(path)
			args = append(args, "--projection-file", path)
		}
		if in.Sort != nil {
			path, pathErr := writeTempJSON(in.Sort)
			if pathErr != nil {
				return nil, jsonrpc.InternalError(pathErr.Error())
			}
			defer os.Remove(path)
			args = append(args, "--sort-file", path)
		}
		if in.Limit != nil {
			args = append(args, "--limit", strconv.Itoa(*in.Limit))
		}
		if in.Offset != nil {
			args = append(args, "--offset", strconv.Itoa(*in.Offset))
		}
	case "db_read_struct":
		tool = "cloud_db_read_struct"
		if in.AppID == "" || in.Env == "" {
			return nil, jsonrpc.InvalidParams("app_id 和 env 不能为空")
		}
		structAction := defaultOr(in.StructAction, "list")
		args = append(args, "--appid", in.AppID, "--env", in.Env, "--action", structAction)
		if in.CollectionName != "" {
			args = append(args, "--collection-name", in.CollectionName)
		}
		if in.Limit != nil {
			args = append(args, "--limit", strconv.Itoa(*in.Limit))
		}
		if in.Offset != nil {
			args = append(args, "--offset", strconv.Itoa(*in.Offset))
		}
	case "storage_query":
		tool = "cloud_query_storage"
		if in.AppID == "" || in.Env == "" || in.CloudPath == "" {
			return nil, jsonrpc.InvalidParams("app_id、env 和 cloud_path 不能为空")
		}
		args = append(args, "--appid", in.AppID, "--env", in.Env, "--action", "list", "--cloud-path", in.CloudPath)
		if in.MaxAge != nil {
			args = append(args, "--max-age", strconv.Itoa(*in.MaxAge))
		}
	default:
		return nil, jsonrpc.InvalidParams("action 必须是 env_list、fn_list、fn_info、db_read_doc、db_read_struct 或 storage_query")
	}
	result, runErr := runWechatide(tool, args, 120*time.Second)
	if runErr != nil {
		return nil, jsonrpc.InternalError(runErr.Error())
	}
	return map[string]any{"ok": true, "operation": "wechat.miniprogram.cloud.read", "action": action, "result": result}, nil
}

func devtoolsCloudWrite(in devtoolsInput) (any, *jsonrpc.Error) {
	action := strings.TrimSpace(in.Action)
	if in.AppID == "" || in.Env == "" {
		return nil, jsonrpc.InvalidParams("app_id 和 env 不能为空")
	}
	args := []string{"--appid", in.AppID, "--env", in.Env}
	var tool string
	switch action {
	case "fn_deploy":
		tool = "cloud_fn_deploy"
		if in.Path == "" {
			return nil, jsonrpc.InvalidParams("path 不能为空")
		}
		args = append(args, "--path", in.Path)
		if in.RemoteNPMInstall {
			args = append(args, "--remote-npm-install")
		}
	case "fn_inc_deploy":
		tool = "cloud_fn_inc_deploy"
		if in.Path == "" || in.File == "" {
			return nil, jsonrpc.InvalidParams("path 和 file 不能为空")
		}
		args = append(args, "--path", in.Path, "--file", in.File)
	case "db_write_doc":
		tool = "cloud_db_write_doc"
		if in.CollectionName == "" || in.DataAction == "" {
			return nil, jsonrpc.InvalidParams("collection_name 和 data_action 不能为空")
		}
		args = append(args, "--collection-name", in.CollectionName, "--action", in.DataAction)
		if in.Documents != nil {
			path, pathErr := writeTempJSON(in.Documents)
			if pathErr != nil {
				return nil, jsonrpc.InternalError(pathErr.Error())
			}
			defer os.Remove(path)
			args = append(args, "--documents-file", path)
		}
		if in.QueryDoc != nil {
			path, pathErr := writeTempJSON(in.QueryDoc)
			if pathErr != nil {
				return nil, jsonrpc.InternalError(pathErr.Error())
			}
			defer os.Remove(path)
			args = append(args, "--query-file", path)
		}
		if in.Update != nil {
			path, pathErr := writeTempJSON(in.Update)
			if pathErr != nil {
				return nil, jsonrpc.InternalError(pathErr.Error())
			}
			defer os.Remove(path)
			args = append(args, "--update-file", path)
		}
		if in.IsMulti {
			args = append(args, "--is-multi")
		}
		if in.Upsert {
			args = append(args, "--upsert")
		}
	case "db_write_struct":
		tool = "cloud_db_write_struct"
		if in.CollectionName == "" || in.StructAction == "" {
			return nil, jsonrpc.InvalidParams("collection_name 和 struct_action 不能为空")
		}
		args = append(args, "--collection-name", in.CollectionName, "--action", in.StructAction)
		if in.UpdateOptions != nil {
			path, pathErr := writeTempJSON(in.UpdateOptions)
			if pathErr != nil {
				return nil, jsonrpc.InternalError(pathErr.Error())
			}
			defer os.Remove(path)
			args = append(args, "--update-options-file", path)
		}
	case "storage_manage":
		tool = "cloud_manage_storage"
		if in.CloudPath == "" || in.StorageAction == "" {
			return nil, jsonrpc.InvalidParams("cloud_path 和 storage_action 不能为空")
		}
		args = append(args, "--action", in.StorageAction, "--cloud-path", in.CloudPath)
		if in.LocalPath != "" {
			localPath, err := ensureWithin(in.WorkspaceRoot, in.LocalPath)
			if err != nil {
				return nil, jsonrpc.InvalidParams(err.Error())
			}
			args = append(args, "--local-path", localPath)
		}
		if in.IsDirectory {
			args = append(args, "--is-directory")
		}
	default:
		return nil, jsonrpc.InvalidParams("action 必须是 fn_deploy、fn_inc_deploy、db_write_doc、db_write_struct 或 storage_manage")
	}
	result, runErr := runWechatide(tool, args, 300*time.Second)
	if runErr != nil {
		return nil, jsonrpc.InternalError(runErr.Error())
	}
	return map[string]any{"ok": true, "operation": "wechat.miniprogram.cloud.write", "action": action, "result": result}, nil
}

func devtoolsInstallerCheck(in devtoolsInput) (any, *jsonrpc.Error) {
	if _, err := ensureWorkspace(in.WorkspaceRoot); err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	result := devtoolsResult("wechat.miniprogram.devtools.installer.check")
	candidates := []string{}
	if in.InstallRoot != "" {
		candidates = append(candidates, filepath.Join(in.InstallRoot, "wechatide.exe"), filepath.Join(in.InstallRoot, "wechatide.cmd"), filepath.Join(in.InstallRoot, "wechatide"))
	}
	if path, err := exec.LookPath("wechatide"); err == nil {
		candidates = append([]string{path}, candidates...)
	}
	for _, candidate := range candidates {
		if !isFile(candidate) {
			continue
		}
		output, err := runCommand(filepath.Dir(candidate), candidate, []string{"-h"}, 10*time.Second)
		result["compatible"] = err == nil
		result["command"] = candidate
		result["detail"] = tail(output, 4000)
		if err != nil {
			result["ok"] = false
			result["status"] = "blocked"
			result["blockers"] = []map[string]any{devtoolsBlocker("WECHATIDE_CLI_UNAVAILABLE", err.Error(), "更新或修复微信开发者工具", true)}
		}
		return result, nil
	}
	result["ok"] = false
	result["status"] = "blocked"
	result["compatible"] = false
	result["reason"] = "not_installed"
	result["blockers"] = []map[string]any{devtoolsBlocker("WECHATIDE_NOT_INSTALLED", "未找到微信开发者工具 CLI", "安装微信开发者工具，或提供 install_root 后重试", true)}
	return result, nil
}

func writeTempJSON(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	file, err := os.CreateTemp("", "himind-wechat-*.json")
	if err != nil {
		return "", err
	}
	name := file.Name()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(name)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}
