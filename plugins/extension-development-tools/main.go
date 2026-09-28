package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/MrBaoquan/himind-extensions/sdk/jsonrpc"
	"github.com/MrBaoquan/himind-extensions/tooling/metaguide"
	"github.com/MrBaoquan/himind-extensions/tooling/pluginpack"
	"github.com/MrBaoquan/himind-extensions/tooling/pluginproject"
	"github.com/MrBaoquan/himind-extensions/tooling/skillproject"
	"github.com/MrBaoquan/himind-extensions/tooling/workflowproject"
	validator "github.com/MrBaoquan/himind-extensions/tools/cmd/himind-plugin-validate"
)

type input struct {
	WorkspaceRoot    string   `json:"workspace_root"`
	OutputDir        string   `json:"output_dir"`
	Path             string   `json:"path"`
	Output           string   `json:"output"`
	Name             string   `json:"name"`
	DisplayName      string   `json:"display_name"`
	Template         string   `json:"template"`
	Slug             string   `json:"slug"`
	ID               string   `json:"id"`
	Version          string   `json:"version"`
	MinAgentVersion  string   `json:"min_agent_version"`
	Description      string   `json:"description"`
	Author           string   `json:"author"`
	Categories       []string `json:"categories"`
	ReleaseNotes     string   `json:"release_notes"`
	Kind             string   `json:"kind"`
	SupportedClients []string `json:"supported_clients"`
}

func main() {
	if err := jsonrpc.Serve(os.Stdin, os.Stdout, handle); err != nil {
		fmt.Fprintln(os.Stderr, "extension development tools stopped:", err)
	}
}

func handle(request jsonrpc.Request) (any, *jsonrpc.Error) {
	var in input
	if rpcError := jsonrpc.DecodeParams(request, &in); rpcError != nil {
		return nil, rpcError
	}
	switch request.Method {
	case "extension.environment.preflight":
		return preflight(in.Kind), nil
	case "extension.plugin.scaffold":
		return pluginScaffold(in)
	case "extension.plugin.validate":
		return validatePath(in.WorkspaceRoot, in.Path, validator.ValidatePath)
	case "extension.plugin.build":
		return pluginBuild(in)
	case "extension.plugin.package":
		if err := ensureWithin(in.WorkspaceRoot, in.Path); err != nil {
			return nil, jsonrpc.InvalidParams(err.Error())
		}
		if err := ensureWithin(in.WorkspaceRoot, in.Output); err != nil {
			return nil, jsonrpc.InvalidParams(err.Error())
		}
		if err := pluginpack.Package(in.Path, in.Output); err != nil {
			return nil, jsonrpc.InternalError(err.Error())
		}
		return map[string]any{"ok": true, "output": in.Output}, nil
	case "extension.skill.scaffold":
		return skillScaffold(in)
	case "extension.skill.validate":
		return validatePath(in.WorkspaceRoot, in.Path, skillproject.Validate)
	case "extension.skill.package":
		if err := ensureWithin(in.WorkspaceRoot, in.Path); err != nil {
			return nil, jsonrpc.InvalidParams(err.Error())
		}
		if err := ensureWithin(in.WorkspaceRoot, in.Output); err != nil {
			return nil, jsonrpc.InvalidParams(err.Error())
		}
		if err := skillproject.Package(in.Path, in.Output); err != nil {
			return nil, jsonrpc.InternalError(err.Error())
		}
		return map[string]any{"ok": true, "output": in.Output}, nil
	case "extension.workflow.scaffold":
		return workflowScaffold(in)
	case "extension.workflow.validate":
		return validatePath(in.WorkspaceRoot, in.Path, workflowproject.Validate)
	case "extension.workflow.build":
		return workflowBuild(in)
	case "extension.workflow.package":
		if err := ensureWithin(in.WorkspaceRoot, in.Path); err != nil {
			return nil, jsonrpc.InvalidParams(err.Error())
		}
		if err := ensureWithin(in.WorkspaceRoot, in.Output); err != nil {
			return nil, jsonrpc.InvalidParams(err.Error())
		}
		if err := workflowproject.Package(in.Path, in.Output); err != nil {
			return nil, jsonrpc.InternalError(err.Error())
		}
		return map[string]any{"ok": true, "output": in.Output}, nil
	default:
		return nil, jsonrpc.InvalidParams("unsupported extension development capability")
	}
}

func preflight(kind string) map[string]any {
	kind = strings.TrimSpace(strings.ToLower(kind))
	if kind == "" {
		kind = "plugin"
	}
	result := map[string]any{
		"checked_at": time.Now().UTC().Format(time.RFC3339),
		"kind":       kind,
		"state":      "blocked",
		"ready":      false,
		"blockers":   []map[string]any{},
		"warnings":   []map[string]any{},
		"next_steps": []string{},
		// 创作规则与字段约束随预检一起返回：只写在技能正文里，AI 不一定读到；
		// 只写在常量里，没有任何环节会把它送出去（曾长期如此）。
		"authoring_rules":   metaguide.Rules,
		"field_constraints": metaguide.Fields(),
	}
	addBlocker := func(code, stage, message, remediation string, retryable bool) {
		result["blockers"] = append(result["blockers"].([]map[string]any), map[string]any{
			"code":        code,
			"stage":       stage,
			"severity":    "error",
			"message":     message,
			"remediation": remediation,
			"retryable":   retryable,
		})
	}
	if kind == "skill" {
		result["state"] = "ready"
		result["ready"] = true
		result["go"] = map[string]any{"required": false}
		result["next_steps"] = []string{"继续执行 extension.skill.scaffold、validate、package，然后调用 Agent 的 extension.test"}
		return result
	}
	if kind == "workflow" {
		result["state"] = "ready"
		result["ready"] = true
		result["go"] = map[string]any{"required": false}
		result["next_steps"] = []string{"继续执行 extension.workflow.scaffold、validate、build、package，然后调用 Agent 的 extension.test"}
		return result
	}
	if kind != "plugin" {
		addBlocker("invalid_extension_kind", "preflight", "kind 必须是 plugin、skill 或 workflow", "使用 kind=plugin、kind=skill 或 kind=workflow 重新调用", false)
		return result
	}
	result["go"] = map[string]any{"installed": false, "required": true}
	path, err := exec.LookPath("go")
	if err != nil {
		addBlocker("toolchain_missing", "toolchain", "未在 PATH 中找到 Go 工具链", "安装 Go 并确保 go 在 PATH 中，然后重新预检", true)
		result["next_steps"] = []string{"修复工具链后重新调用 extension.environment.preflight"}
		return result
	}
	versionCommand := exec.Command(path, "version")
	configureHiddenCommand(versionCommand)
	output, err := versionCommand.CombinedOutput()
	goResult := map[string]any{"installed": true, "path": path, "version": firstLine(string(output))}
	if err != nil {
		goResult["error"] = err.Error()
	}
	result["go"] = goResult
	if err != nil {
		addBlocker("toolchain_unavailable", "toolchain", "Go 工具链无法执行", "修复 Go 安装或 PATH 后重新预检", true)
		result["next_steps"] = []string{"修复工具链后重新调用 extension.environment.preflight"}
	} else {
		result["state"] = "ready"
		result["ready"] = true
		result["next_steps"] = []string{"继续执行 extension.plugin.scaffold、validate、build、package，然后调用 Agent 的 extension.test"}
	}
	return result
}

func pluginScaffold(in input) (any, *jsonrpc.Error) {
	if err := ensureWithin(in.WorkspaceRoot, in.OutputDir); err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	result, err := pluginproject.Create(pluginproject.Config{Name: in.Name, DisplayName: in.DisplayName, Description: in.Description, Author: in.Author, Categories: in.Categories, ReleaseNotes: in.ReleaseNotes, Template: in.Template, OutputDir: in.OutputDir})
	if err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	return result, nil
}

func skillScaffold(in input) (any, *jsonrpc.Error) {
	if err := ensureWithin(in.WorkspaceRoot, in.OutputDir); err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	root, err := skillproject.Create(skillproject.CreateConfig{Slug: in.Slug, ID: in.ID, Name: in.Name, Version: in.Version, Description: in.Description, Author: in.Author, Categories: in.Categories, ReleaseNotes: in.ReleaseNotes, MinAgentVersion: in.MinAgentVersion, Clients: in.SupportedClients, OutputDir: in.OutputDir})
	if err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	return map[string]any{"root": root}, nil
}

func workflowScaffold(in input) (any, *jsonrpc.Error) {
	if err := ensureWithin(in.WorkspaceRoot, in.OutputDir); err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	result, err := workflowproject.Create(workflowproject.Config{
		Slug:            in.Slug,
		ID:              in.ID,
		Name:            in.Name,
		Description:     in.Description,
		Author:          in.Author,
		Version:         in.Version,
		MinAgentVersion: in.MinAgentVersion,
		ReleaseNotes:    in.ReleaseNotes,
		Categories:      in.Categories,
		Template:        in.Template,
		OutputDir:       in.OutputDir,
	})
	if err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	return result, nil
}

func workflowBuild(in input) (any, *jsonrpc.Error) {
	if err := ensureWithin(in.WorkspaceRoot, in.Path); err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	manifest, err := workflowproject.Build(in.Path)
	if err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	return map[string]any{
		"ok":          true,
		"path":        in.Path,
		"workflow_id": manifest.ID,
		"version":     manifest.Version,
		"step_count":  len(manifest.Steps),
	}, nil
}

func pluginBuild(in input) (any, *jsonrpc.Error) {
	if err := ensureWithin(in.WorkspaceRoot, in.Path); err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	manifest, err := validator.ReadManifest(in.Path)
	if err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	if filepath.IsAbs(manifest.Entry) || strings.Contains(filepath.ToSlash(manifest.Entry), "../") {
		return nil, jsonrpc.InvalidParams("plugin entry must remain inside the project")
	}
	if result, runErr := runGo(in.Path, "test", "./..."); runErr != nil {
		return nil, jsonrpc.InternalError(result)
	}
	entry := filepath.Join(in.Path, filepath.FromSlash(manifest.Entry))
	if err := os.MkdirAll(filepath.Dir(entry), 0755); err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	result, runErr := runGo(in.Path, "build", "-o", entry, ".")
	if runErr != nil {
		return nil, jsonrpc.InternalError(result)
	}
	return map[string]any{"ok": true, "entry": entry, "output": result}, nil
}

func validatePath(workspace, target string, validate func(string) error) (any, *jsonrpc.Error) {
	if err := ensureWithin(workspace, target); err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	if err := validate(target); err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	return map[string]any{"ok": true, "path": target}, nil
}

func ensureWithin(workspace, target string) error {
	workspace = strings.TrimSpace(workspace)
	target = strings.TrimSpace(target)
	if workspace == "" || target == "" {
		return fmt.Errorf("workspace_root and target path are required")
	}
	workspaceAbs, err := filepath.Abs(workspace)
	if err != nil {
		return err
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(workspaceAbs, targetAbs)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("path must remain inside workspace_root")
	}
	return nil
}

func runGo(directory string, args ...string) (string, error) {
	goPath, err := exec.LookPath("go")
	if err != nil {
		return "未在 PATH 中找到 Go 工具链", err
	}
	// 构建上限要小于 Agent 侧给本能力声明的 timeout_seconds，否则进程会先被
	// Agent 杀掉，调用方拿到的是一句「超时」而不是能定位问题的编译输出。
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, goPath, args...)
	configureHiddenCommand(command)
	command.Dir = directory
	output, err := command.CombinedOutput()
	text := trimCommandOutput(string(output))
	if ctx.Err() != nil {
		return text + "\n命令超时", ctx.Err()
	}
	return text, err
}

// commandOutputLimit 是回传给 AI 的命令输出字节上限。
const commandOutputLimit = 16000

// trimCommandOutput 超限时保留首尾两段。
//
// 只留尾部的写法会把最该看的内容丢掉：go build 先报失败的包，末尾往往只剩
// 一句 "exit status 1"。中间被省略的部分显式标出字节数，避免看起来像完整输出。
func trimCommandOutput(text string) string {
	if len(text) <= commandOutputLimit {
		return text
	}
	head := commandOutputLimit / 2
	for head > 0 && !utf8.RuneStart(text[head]) {
		head--
	}
	tail := len(text) - (commandOutputLimit - commandOutputLimit/2)
	for tail < len(text) && !utf8.RuneStart(text[tail]) {
		tail++
	}
	// 按实际裁掉的中段报数：裁剪点回退到字符边界后会比预算多丢几个字节。
	skipped := tail - head
	return text[:head] +
		"\n...（中间省略 " + strconv.Itoa(skipped) + " 字节，完整输出请在本机重新执行该命令）...\n" +
		text[tail:]
}

func firstLine(value string) string {
	for _, line := range strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			return strings.TrimSpace(line)
		}
	}
	return ""
}
