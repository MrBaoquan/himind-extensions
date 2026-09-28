package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MrBaoquan/himind-extensions/sdk/jsonrpc"
	"github.com/MrBaoquan/himind-extensions/tooling/metaguide"
	"github.com/MrBaoquan/himind-extensions/tooling/pluginproject"
	"github.com/MrBaoquan/himind-extensions/tooling/workflowproject"
	validator "github.com/MrBaoquan/himind-extensions/tools/cmd/himind-plugin-validate"
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

func TestPreflightReturnsAuthoringGuidance(t *testing.T) {
	for _, kind := range []string{"plugin", "skill", "workflow"} {
		result, ok := invoke(t, "extension.environment.preflight", map[string]any{"kind": kind}).(map[string]any)
		if !ok {
			t.Fatalf("preflight %s returned an unexpected result type", kind)
		}
		if result["authoring_rules"] != metaguide.Rules {
			t.Fatalf("preflight %s must return the authoring rules, got %v", kind, result["authoring_rules"])
		}
		fields, ok := result["field_constraints"].([]metaguide.Field)
		if !ok || len(fields) == 0 {
			t.Fatalf("preflight %s must return the field constraints, got %T", kind, result["field_constraints"])
		}
	}
}

func TestSkillWorkflowInBlankWorkspace(t *testing.T) {
	workspace := t.TempDir()
	outputDir := filepath.Join(workspace, "skills")
	result := invoke(t, "extension.skill.scaffold", map[string]any{
		"workspace_root": workspace,
		"output_dir":     outputDir,
		"slug":           "blank-skill",
		"name":           "空白目录技能",
		"description":    "验证技能开发流程。",
		"author":         "测试用户",
		"categories":     []string{"software-engineering"},
		"release_notes":  "新增空白目录技能开发流程。",
	})
	root := result.(map[string]any)["root"].(string)
	invoke(t, "extension.skill.validate", map[string]any{
		"workspace_root": workspace,
		"path":           root,
	})
	packagePath := filepath.Join(workspace, "dist", "blank-skill.hmskill")
	invoke(t, "extension.skill.package", map[string]any{
		"workspace_root": workspace,
		"path":           root,
		"output":         packagePath,
	})
	invoke(t, "extension.skill.validate", map[string]any{
		"workspace_root": workspace,
		"path":           packagePath,
	})
}

func TestPluginWorkflowInBlankWorkspace(t *testing.T) {
	workspace := t.TempDir()
	outputDir := filepath.Join(workspace, "plugins")
	result := invoke(t, "extension.plugin.scaffold", map[string]any{
		"workspace_root": workspace,
		"output_dir":     outputDir,
		"name":           "blank-plugin",
		"display_name":   "空白目录插件",
		"description":    "验证插件开发流程。",
		"author":         "测试用户",
		"categories":     []string{"software-engineering"},
		"release_notes":  "新增空白目录插件开发流程。",
		"template":       "readonly-tool",
	})
	root := result.(pluginproject.Result).Root
	invoke(t, "extension.plugin.validate", map[string]any{
		"workspace_root": workspace,
		"path":           root,
	})
	invoke(t, "extension.plugin.build", map[string]any{
		"workspace_root": workspace,
		"path":           root,
	})
	packagePath := filepath.Join(workspace, "dist", "blank-plugin.hmpkg")
	invoke(t, "extension.plugin.package", map[string]any{
		"workspace_root": workspace,
		"path":           root,
		"output":         packagePath,
	})
	invoke(t, "extension.plugin.validate", map[string]any{
		"workspace_root": workspace,
		"path":           packagePath,
	})
}

func TestWorkflowInBlankWorkspace(t *testing.T) {
	workspace := t.TempDir()
	outputDir := filepath.Join(workspace, "workflows")
	result := invoke(t, "extension.workflow.scaffold", map[string]any{
		"workspace_root":    workspace,
		"output_dir":        outputDir,
		"slug":              "blank-workflow",
		"id":                "com.example.workflow.blank",
		"name":              "空白目录工作流",
		"description":       "验证工作流开发流程。",
		"author":            "测试用户",
		"version":           "0.1.0",
		"min_agent_version": "0.3.47",
		"release_notes":     "新增空白目录工作流开发流程。",
		"template":          "segmented",
	}).(workflowproject.Result)
	root := result.Root
	invoke(t, "extension.workflow.validate", map[string]any{
		"workspace_root": workspace,
		"path":           root,
	})
	invoke(t, "extension.workflow.build", map[string]any{
		"workspace_root": workspace,
		"path":           root,
	})
	packagePath := filepath.Join(workspace, "dist", "blank-workflow.hmwf")
	invoke(t, "extension.workflow.package", map[string]any{
		"workspace_root": workspace,
		"path":           root,
		"output":         packagePath,
	})
	invoke(t, "extension.workflow.validate", map[string]any{
		"workspace_root": workspace,
		"path":           packagePath,
	})
}

func TestSkillPreflightDoesNotRequireGo(t *testing.T) {
	result := invoke(t, "extension.environment.preflight", map[string]any{"kind": "skill"}).(map[string]any)
	if result["ready"] != true {
		t.Fatalf("skill preflight should be ready without external toolchains: %#v", result)
	}
}

func TestWorkflowPreflightDoesNotRequireGo(t *testing.T) {
	result := invoke(t, "extension.environment.preflight", map[string]any{"kind": "workflow"}).(map[string]any)
	if result["ready"] != true {
		t.Fatalf("workflow preflight should be ready without external toolchains: %#v", result)
	}
}

func TestRejectsPathsOutsideWorkspace(t *testing.T) {
	workspace := t.TempDir()
	request, err := jsonrpc.NewRequest(1, "extension.skill.validate", map[string]any{
		"workspace_root": workspace,
		"path":           filepath.Join(workspace, "..", "outside"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, rpcError := handle(request); rpcError == nil {
		t.Fatal("expected path outside workspace to be rejected")
	}
}

// TestTrimCommandOutputKeepsHeadAndTail 锁定超长命令输出的截断规则：
// 只留尾部会丢掉 go build 最先报出的编译错误，因此首尾都要保留且标记省略量。
func TestTrimCommandOutputKeepsHeadAndTail(t *testing.T) {
	exact := strings.Repeat("a", commandOutputLimit)
	if got := trimCommandOutput(exact); got != exact {
		t.Fatal("output at the limit must pass through unchanged")
	}

	// 用多字节字符验证裁剪点会回退到字符边界，不会切出半个汉字。
	head := strings.Repeat("开始报错。", 1000)
	tail := strings.Repeat("退出状态码 1。", 1000)
	text := head + tail
	got := trimCommandOutput(text)
	if len(text) <= commandOutputLimit {
		t.Fatalf("fixture must exceed the limit, got %d bytes", len(text))
	}
	if !utf8.ValidString(got) {
		t.Fatalf("truncated output must stay valid UTF-8: %q", got)
	}
	if strings.Count(got, "中间省略") != 1 {
		t.Fatalf("truncated output must mark the omission exactly once: %q", got)
	}
	markerStart := strings.Index(got, "\n...（中间省略 ")
	if markerStart < 0 {
		t.Fatalf("truncated output must keep the omission marker: %q", got)
	}
	markerEnd := markerStart + strings.Index(got[markerStart:], "...\n") + len("...\n")
	if head, kept := got[:markerStart], got[markerEnd:]; !strings.HasPrefix(head, "开始报错。") || !strings.HasSuffix(kept, "退出状态码 1。") {
		t.Fatal("truncation must keep both the first and the last lines")
	}
	retained := markerStart + len(got) - markerEnd
	if retained > commandOutputLimit || retained < commandOutputLimit-3 {
		t.Fatalf("truncation must keep about %d bytes, kept %d", commandOutputLimit, retained)
	}
}

// fakeGoSource 是一个假的 go 命令：记录每次调用，并按环境变量决定失败或在构建时产出入口文件。
const fakeGoSource = `package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	args := strings.Join(os.Args[1:], " ")
	if log := os.Getenv("HIMIND_FAKE_GO_LOG"); log != "" {
		if file, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintln(file, args)
			file.Close()
		}
	}
	if pattern := os.Getenv("HIMIND_FAKE_GO_FAIL"); pattern != "" && strings.Contains(args, pattern) {
		fmt.Fprintln(os.Stderr, "go: simulated failure")
		os.Exit(1)
	}
	if entry := os.Getenv("HIMIND_FAKE_GO_CREATE"); entry != "" {
		if err := os.WriteFile(entry, []byte("fake binary"), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}
`

// installFakeGoShim 用真实工具链编译一个假的 go 命令放到 PATH 最前面，
// 让构建顺序和失败反馈可以被断言，而不必真的编译插件工程。
func installFakeGoShim(t *testing.T) string {
	t.Helper()
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Skip("building the fake go shim requires a go toolchain")
	}
	directory := t.TempDir()
	source := filepath.Join(directory, "main.go")
	if err := os.WriteFile(source, []byte(fakeGoSource), 0o644); err != nil {
		t.Fatal(err)
	}
	name := "go"
	if runtime.GOOS == "windows" {
		name = "go.exe"
	}
	build := exec.Command(realGo, "build", "-o", filepath.Join(directory, name), source)
	build.Env = append(os.Environ(), "GO111MODULE=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("failed to build the fake go shim: %v\n%s", err, output)
	}
	logPath := filepath.Join(directory, "go-invocations.log")
	if err := os.WriteFile(logPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HIMIND_FAKE_GO_LOG", logPath)
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func readGoInvocations(t *testing.T, logPath string) []string {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	var invocations []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			invocations = append(invocations, strings.TrimSpace(line))
		}
	}
	return invocations
}

func scaffoldReadonlyPlugin(t *testing.T, workspace string) string {
	t.Helper()
	return invoke(t, "extension.plugin.scaffold", map[string]any{
		"workspace_root": workspace,
		"output_dir":     filepath.Join(workspace, "plugins"),
		"name":           "build-chain",
		"display_name":   "构建链路校验",
		"description":    "验证插件构建链路。",
		"author":         "测试用户",
		"categories":     []string{"software-engineering"},
		"release_notes":  "新增构建链路校验。",
		"template":       "readonly-tool",
	}).(pluginproject.Result).Root
}

func pluginEntryPath(t *testing.T, root string) string {
	t.Helper()
	manifest, err := validator.ReadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, filepath.FromSlash(manifest.Entry))
}

// TestPluginBuildRunsTestsBeforeBuild 锁定构建顺序：先 go test 再 go build，产物落在 manifest 声明的入口。
func TestPluginBuildRunsTestsBeforeBuild(t *testing.T) {
	workspace := t.TempDir()
	root := scaffoldReadonlyPlugin(t, workspace)
	entry := pluginEntryPath(t, root)
	logPath := installFakeGoShim(t)
	t.Setenv("HIMIND_FAKE_GO_CREATE", entry)
	result, ok := invoke(t, "extension.plugin.build", map[string]any{
		"workspace_root": workspace,
		"path":           root,
	}).(map[string]any)
	if !ok || result["ok"] != true {
		t.Fatalf("build should succeed, got %#v", result)
	}
	invocations := readGoInvocations(t, logPath)
	if len(invocations) != 2 {
		t.Fatalf("expected a test run followed by a build, got %v", invocations)
	}
	if !strings.Contains(invocations[0], "test ./...") {
		t.Fatalf("plugins must be tested before they are built, got %q", invocations[0])
	}
	if !strings.Contains(invocations[1], "build -o") {
		t.Fatalf("the second invocation must build the manifest entry, got %q", invocations[1])
	}
	if _, err := os.Stat(entry); err != nil {
		t.Fatalf("build must produce the manifest entry: %v", err)
	}
}

// TestPluginBuildStopsAtFailingTests 锁定失败反馈：测试不过就不构建，并把命令输出带回给调用方。
func TestPluginBuildStopsAtFailingTests(t *testing.T) {
	workspace := t.TempDir()
	root := scaffoldReadonlyPlugin(t, workspace)
	entry := pluginEntryPath(t, root)
	logPath := installFakeGoShim(t)
	t.Setenv("HIMIND_FAKE_GO_FAIL", "test")
	message := failureMessage(t, "extension.plugin.build", map[string]any{
		"workspace_root": workspace,
		"path":           root,
	})
	if !strings.Contains(message, "go: simulated failure") {
		t.Fatalf("the build failure must surface the command output, got %q", message)
	}
	invocations := readGoInvocations(t, logPath)
	if len(invocations) != 1 || !strings.Contains(invocations[0], "test ./...") {
		t.Fatalf("a failing test run must skip the build, got %v", invocations)
	}
	if _, err := os.Stat(entry); !os.IsNotExist(err) {
		t.Fatalf("a failed test run must not produce an entry file, stat err=%v", err)
	}
}

func failureMessage(t *testing.T, method string, params any) string {
	t.Helper()
	request, err := jsonrpc.NewRequest(1, method, params)
	if err != nil {
		t.Fatal(err)
	}
	_, rpcError := handle(request)
	if rpcError == nil {
		t.Fatalf("%s should fail", method)
	}
	return rpcError.Message
}

// TestScaffoldRejectsOverlongMetadata 锁定元数据长度约束：名称、说明和更新说明超限时脚手架必须拒绝。
func TestScaffoldRejectsOverlongMetadata(t *testing.T) {
	workspace := t.TempDir()
	outputDir := filepath.Join(workspace, "out")
	longName := strings.Repeat("超长名称", 6)
	longDescription := strings.Repeat("用途说明", 31)
	longNotes := strings.Repeat("更新说明", 31)
	brief := "验证元数据长度约束。"
	skillParams := func(name, description, notes string) map[string]any {
		return map[string]any{
			"workspace_root": workspace,
			"output_dir":     outputDir,
			"slug":           "constraint-check",
			"name":           name,
			"description":    description,
			"author":         "测试用户",
			"categories":     []string{"software-engineering"},
			"release_notes":  notes,
		}
	}
	pluginParams := func(displayName, notes string) map[string]any {
		return map[string]any{
			"workspace_root": workspace,
			"output_dir":     outputDir,
			"name":           "constraint-check",
			"display_name":   displayName,
			"description":    brief,
			"author":         "测试用户",
			"categories":     []string{"software-engineering"},
			"release_notes":  notes,
			"template":       "readonly-tool",
		}
	}
	workflowParams := func(name string) map[string]any {
		return map[string]any{
			"workspace_root": workspace,
			"output_dir":     outputDir,
			"slug":           "constraint-check",
			"id":             "com.example.workflow.constraint-check",
			"name":           name,
			"description":    brief,
			"author":         "测试用户",
			"release_notes":  brief,
			"template":       "strict",
		}
	}
	cases := []struct {
		method string
		params map[string]any
		expect string
	}{
		{"extension.skill.scaffold", skillParams(longName, brief, brief), "name is too long"},
		{"extension.skill.scaffold", skillParams("长度约束校验", longDescription, brief), "description is too long"},
		{"extension.skill.scaffold", skillParams("长度约束校验", brief, longNotes), "release_notes is too long"},
		{"extension.plugin.scaffold", pluginParams(longName, brief), "name is too long"},
		{"extension.plugin.scaffold", pluginParams("长度约束校验", longNotes), "release_notes is too long"},
		{"extension.workflow.scaffold", workflowParams(longName), "name is too long"},
	}
	for _, item := range cases {
		if message := failureMessage(t, item.method, item.params); !strings.Contains(message, item.expect) {
			t.Fatalf("%s should reject %s, got %q", item.method, item.expect, message)
		}
	}
}
