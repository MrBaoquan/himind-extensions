package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MrBaoquan/himind-extensions/sdk/jsonrpc"
)

func call(t *testing.T, method string, params string) map[string]any {
	t.Helper()
	result, rpcError := handle(jsonrpc.Request{Method: method, Params: []byte(params)})
	if rpcError != nil {
		t.Fatalf("%s 返回错误: %s", method, rpcError.Message)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("%s 结果序列化失败: %v", method, err)
	}
	var value map[string]any
	if err := json.Unmarshal(encoded, &value); err != nil {
		t.Fatalf("%s 结果反序列化失败: %v", method, err)
	}
	return value
}

func quote(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

// writeFixture 构造一个临时的 home：包含一个 Codex rollout 与一个 Claude 会话索引。
func writeFixture(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	codexPath := filepath.Join(home, ".codex", "sessions", "2026", "10", "09", "rollout-2026-10-09T10-00-00-abc.jsonl")
	if err := os.MkdirAll(filepath.Dir(codexPath), 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"timestamp":"2026-10-09T02:00:00Z","type":"session_meta","payload":{"session_id":"codex-1","id":"codex-1","cwd":"F:\\proj\\demo","timestamp":"2026-10-09T02:00:00Z"}}`,
		`{"timestamp":"2026-10-09T02:00:05Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"实现登录功能"}]}}`,
		`{"timestamp":"2026-10-09T02:01:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"已完成登录功能并补充测试"}]}}`,
	}
	if err := os.WriteFile(codexPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	claudePath := filepath.Join(home, ".claude", "projects", "p1", "sessions-index.json")
	if err := os.MkdirAll(filepath.Dir(claudePath), 0o755); err != nil {
		t.Fatal(err)
	}
	index := `{"entries":[{"sessionId":"claude-1","fullPath":"x.jsonl","firstPrompt":"修复打包脚本","messageCount":5,"created":"2026-10-09T03:00:00Z","modified":"2026-10-09T03:30:00Z","projectPath":"c:\\work\\pack"}]}`
	if err := os.WriteFile(claudePath, []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestSourcesDetectsByKind(t *testing.T) {
	home := writeFixture(t)
	out := call(t, "work-log.session.sources", `{"home":`+quote(home)+`}`)
	sources := out["sources"].([]any)
	byID := map[string]map[string]any{}
	for _, raw := range sources {
		item := raw.(map[string]any)
		byID[item["id"].(string)] = item
	}
	if byID["codex"]["exists"] != true || byID["codex"]["supported"] != true {
		t.Fatalf("Codex 目录应被识别为可用: %#v", byID["codex"])
	}
	if byID["claude"]["exists"] != true {
		t.Fatalf("Claude 目录应被识别为存在: %#v", byID["claude"])
	}
	if byID["opencode"]["exists"] != false {
		t.Fatalf("临时 home 不应有 OpenCode 库: %#v", byID["opencode"])
	}
}

func TestScanCodexAndClaude(t *testing.T) {
	home := writeFixture(t)
	out := call(t, "work-log.session.scan", `{"home":`+quote(home)+`,"date":"2026-10-09","limit":10}`)
	if out["total"].(float64) != 2 {
		t.Fatalf("应扫描到 2 个会话，实际 %v", out["total"])
	}
	sessions := out["sessions"].([]any)
	var codex, claude map[string]any
	for _, raw := range sessions {
		item := raw.(map[string]any)
		switch item["source"] {
		case "codex":
			codex = item
		case "claude":
			claude = item
		}
	}
	if codex == nil || claude == nil {
		t.Fatalf("缺少来源会话: %#v", sessions)
	}
	if codex["session_id"] != "codex-1" || codex["project"] != "demo" {
		t.Fatalf("Codex 会话字段异常: %#v", codex)
	}
	prompts := codex["user_prompts"].([]any)
	if len(prompts) != 1 || prompts[0] != "实现登录功能" {
		t.Fatalf("Codex 用户提示提取异常: %#v", prompts)
	}
	if codex["date"] != "2026-10-09" {
		t.Fatalf("Codex 日期应为本地 2026-10-09: %v", codex["date"])
	}
	if claude["session_id"] != "claude-1" || claude["turn_count"].(float64) != 5 {
		t.Fatalf("Claude 会话字段异常: %#v", claude)
	}
}

func TestScanDateFilterExcludes(t *testing.T) {
	home := writeFixture(t)
	out := call(t, "work-log.session.scan", `{"home":`+quote(home)+`,"date":"2026-10-08","limit":10}`)
	if out["total"].(float64) != 0 {
		t.Fatalf("日期不匹配应返回 0，实际 %v", out["total"])
	}
}

func TestScanReportsSkipped(t *testing.T) {
	home := writeFixture(t)
	out := call(t, "work-log.session.scan", `{"home":`+quote(home)+`,"date":"2026-10-09"}`)
	skipped := out["skipped"].([]any)
	if len(skipped) == 0 {
		t.Fatalf("tmp home 应报告被跳过的来源")
	}
}

func TestScanEmitsCandidateEntries(t *testing.T) {
	home := writeFixture(t)
	out := call(t, "work-log.session.scan", `{"home":`+quote(home)+`,"date":"2026-10-09","limit":10}`)
	entries, ok := out["entries"].([]any)
	if !ok || len(entries) == 0 {
		t.Fatalf("扫描结果应带候选条目: %#v", out["entries"])
	}
	found := false
	for _, raw := range entries {
		item := raw.(map[string]any)
		if item["source"] == "codex" && item["session_id"] == "codex-1" {
			found = true
			if item["title"] != "实现登录功能" {
				t.Fatalf("候选条目标题应取自首条用户提示: %#v", item)
			}
		}
	}
	if !found {
		t.Fatalf("候选条目缺少 codex 会话: %#v", entries)
	}
}

func TestBadDateRejected(t *testing.T) {
	home := writeFixture(t)
	_, rpcError := handle(jsonrpc.Request{Method: "work-log.session.scan", Params: []byte(`{"home":` + quote(home) + `,"from":"2026-13-01"}`)})
	if rpcError == nil {
		t.Fatal("非法日期应被拒绝")
	}
}

func TestUnknownCapabilityRejected(t *testing.T) {
	_, rpcError := handle(jsonrpc.Request{Method: "work-log.session.nope", Params: []byte(`{}`)})
	if rpcError == nil {
		t.Fatal("未知能力应被拒绝")
	}
}

func TestExtractText(t *testing.T) {
	if got := extractText(`{"text":"hello"}`); got != "hello" {
		t.Fatalf("text 提取异常: %q", got)
	}
	if got := extractText(`{"content":[{"text":"a"},{"text":"b"}]}`); got != "a\nb" {
		t.Fatalf("content 提取异常: %q", got)
	}
	if got := extractText(`not json`); got != "" {
		t.Fatalf("非法 JSON 应返回空: %q", got)
	}
}

func TestResolveRangeSwaps(t *testing.T) {
	_, _, from, to, err := resolveRange("2026-10-10", "2026-10-01", "")
	if err != nil {
		t.Fatal(err)
	}
	if from != "2026-10-01" || to != "2026-10-10" {
		t.Fatalf("区间应被纠正为升序: %s..%s", from, to)
	}
}

func TestGenericConfiguredSourceJSONL(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".acidump")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := strings.Join([]string{
		`{"ts":"2026-10-09T09:10:00+08:00","role":"user","content":"实现通用来源"}`,
		`{"ts":"2026-10-09T09:12:00+08:00","role":"assistant","content":"已实现 jsonl 解析"}`,
	}, "\n")
	if err := os.WriteFile(filepath.Join(dir, "s1.jsonl"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}

	params := `{"home":` + quote(home) + `,"log_root":` + quote(home) + `,"date":"2026-10-09","include_presets":false,"sources_config":[{
		"id":"acidump","label":"Acidump","root":` + quote(dir) + `,"glob":"**/*.jsonl","format":"jsonl",
		"time_fields":["ts"],"role_field":"role","user_roles":["user"],"assistant_roles":["assistant"],
		"content_fields":["content"],"project_strategy":"dir_base"}]}`
	result := call(t, "work-log.session.scan", params)
	entries := result["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("通用来源应产出 1 条，实际 %d（skipped=%v）", len(entries), result["skipped"])
	}
	item := entries[0].(map[string]any)
	if item["source"] != "acidump" {
		t.Fatalf("来源应为 acidump，实际 %v", item["source"])
	}
	if item["title"] != "实现通用来源" {
		t.Fatalf("标题应取首条用户消息，实际 %v", item["title"])
	}
	if item["project"] != ".acidump" {
		t.Fatalf("项目应按目录名归类，实际 %v", item["project"])
	}
}

func TestGenericGitRootProject(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "workspace", "my-repo")
	sub := filepath.Join(repo, "src", "pkg")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `[{"ts":"2026-10-09T10:00:00+08:00","role":"user","content":"修一个 bug"}]`
	if err := os.WriteFile(filepath.Join(sub, "db.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	params := `{"home":` + quote(home) + `,"log_root":` + quote(home) + `,"date":"2026-10-09","include_presets":false,"sources_config":[{
		"id":"mytool","root":` + quote(sub) + `,"glob":"*.json","format":"json_array",
		"time_fields":["ts"],"role_field":"role","user_roles":["user"],"content_fields":["content"],
		"project_strategy":"git_root"}]}`
	result := call(t, "work-log.session.scan", params)
	entries := result["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("期望 1 条，实际 %d", len(entries))
	}
	if project := entries[0].(map[string]any)["project"]; project != "my-repo" {
		t.Fatalf("项目应为 git 仓库名 my-repo，实际 %v", project)
	}
}

func TestSourcesCapIncludesConfigured(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".acidump")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "s1.jsonl"), []byte(`{"ts":"2026-10-09T09:10:00+08:00"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	params := `{"home":` + quote(home) + `,"log_root":` + quote(home) + `,"include_presets":false,"sources_config":[{
		"id":"acidump","root":` + quote(dir) + `,"glob":"**/*.jsonl","format":"jsonl"}]}`
	result := call(t, "work-log.session.sources", params)
	sources := result["sources"].([]any)
	found := false
	for _, raw := range sources {
		item := raw.(map[string]any)
		if item["id"] == "acidump" {
			found = true
			if item["supported"] != true {
				t.Fatalf("已配置来源应可用: %#v", item)
			}
		}
	}
	if !found {
		t.Fatalf("来源列表应包含已配置来源: %#v", sources)
	}
}
