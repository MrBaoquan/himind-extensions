package main

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MrBaoquan/himind-extensions/sdk/jsonrpc"
	_ "modernc.org/sqlite"
)

func TestResolveWindowLookback(t *testing.T) {
	now := time.Now()

	from, to, date := resolveWindow(input{})
	if date != "" || from != now.Format(dateLayout) || to != now.Format(dateLayout) {
		t.Fatalf("默认应仅覆盖今天: from=%s to=%s date=%s", from, to, date)
	}

	from, to, _ = resolveWindow(input{LookbackDays: 2})
	if to != now.Format(dateLayout) || from != now.AddDate(0, 0, -1).Format(dateLayout) {
		t.Fatalf("lookback=2 应为昨天+今天: from=%s to=%s", from, to)
	}

	from, _, _ = resolveWindow(input{LookbackDays: 3})
	if from != now.AddDate(0, 0, -2).Format(dateLayout) {
		t.Fatalf("lookback=3 起点应为前天: from=%s", from)
	}

	// 显式 date 优先，lookback_days 不生效。
	from, to, date = resolveWindow(input{Date: "2026-01-02", LookbackDays: 5})
	if date != "2026-01-02" || from != "" || to != "" {
		t.Fatalf("显式 date 应优先: from=%s to=%s date=%s", from, to, date)
	}
}

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
	// 扫描会落一份会话素材 artifact，测试里固定写到临时数据根，避免污染真实用户目录。
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
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
	if byID["claude-code"]["exists"] != true {
		t.Fatalf("Claude Code 目录应被识别为存在: %#v", byID["claude-code"])
	}
	if byID["opencode"]["exists"] != false {
		t.Fatalf("临时 home 不应有 OpenCode 库: %#v", byID["opencode"])
	}
}

// TestScanLegacyClaudeAlias 验证历史写法 source=claude 仍归一到 claude-code。
func TestScanLegacyClaudeAlias(t *testing.T) {
	home := writeFixture(t)
	out := call(t, "work-log.session.scan", `{"home":`+quote(home)+`,"date":"2026-10-09","limit":10,"source":"claude"}`)
	if out["total"].(float64) != 1 {
		t.Fatalf("source=claude 别名应只选到 claude-code 来源，实际 %v", out["total"])
	}
	session := out["sessions"].([]any)[0].(map[string]any)
	if session["source"] != "claude-code" {
		t.Fatalf("别名应归一到标准 ID claude-code: %#v", session["source"])
	}
}

// TestNormalizeSourceID 验证历史来源 ID 全部归一到标准客户端 ID。
func TestNormalizeSourceID(t *testing.T) {
	cases := map[string]string{
		"claude":         "claude-code",
		"copilot":        "github-copilot",
		"vscode-copilot": "vscode",
		"claude-code":    "claude-code",
		"github-copilot": "github-copilot",
		"vscode":         "vscode",
		"codex":          "codex",
		"all":            "all",
	}
	for in, want := range cases {
		if got := normalizeSourceID(in); got != want {
			t.Fatalf("normalizeSourceID(%q) = %q, want %q", in, got, want)
		}
	}
	if got := legacySourceKey("github-copilot"); got != "copilot" {
		t.Fatalf("github-copilot 的历史键应为 copilot，实际 %q", got)
	}
	if got := legacySourceKey("vscode"); got != "vscode-copilot" {
		t.Fatalf("vscode 的历史键应为 vscode-copilot，实际 %q", got)
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
		case "claude-code":
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

// TestScanCodexWritesLocalOffset 验证素材里的时间戳统一换算成本机时区。
//
// Codex 原始 rollout 里是 UTC（后缀 Z），而 OpenCode 等来源是本地偏移。素材若原样透传，
// 同一天里就会混着两种时钟，读素材的人和 AI 都会把 UTC 那半当成本地时间（本机 +08:00
// 时正好差 8 小时）。这里锁定两件事：写出的偏移是本机偏移，且时刻本身没有被改变。
func TestScanCodexWritesLocalOffset(t *testing.T) {
	home := writeFixture(t)
	day := time.Date(2026, 10, 9, 0, 0, 0, 0, time.Local)
	digests, err := scanCodex(filepath.Join(home, ".codex"), day, day.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("扫描 Codex 失败: %v", err)
	}
	if len(digests) != 1 {
		t.Fatalf("应扫描到 1 个 Codex 会话，实际 %d", len(digests))
	}
	digest := digests[0]
	parsed, err := time.Parse(time.RFC3339, digest.StartedAt)
	if err != nil {
		t.Fatalf("started_at 应为 RFC3339: %q", digest.StartedAt)
	}
	_, wantOffset := time.Now().Zone()
	_, gotOffset := parsed.Zone()
	if gotOffset != wantOffset {
		t.Fatalf("素材时间戳应为本机偏移 %d 秒，实际 %q（%d 秒）", wantOffset, digest.StartedAt, gotOffset)
	}
	if want := time.Date(2026, 10, 9, 2, 0, 0, 0, time.UTC); !parsed.Equal(want) {
		t.Fatalf("时间点不应被改动：%s != %s", parsed.UTC(), want)
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

func TestMeaningfulTitleSkipsSystemBlocks(t *testing.T) {
	prompts := []string{
		"<environment_context>\n  <current_date>2026-10-09</current_date>\n</environment_context>\n我要修复登录问题",
		"## My request:\n把打包脚本里的版本号改成变量",
	}
	if got := meaningfulTitle(prompts, 80); got != "我要修复登录问题" {
		t.Fatalf("标题应跳过系统块取真实请求，实际 %q", got)
	}
	if got := meaningfulTitle([]string{"<environment_context>\n</environment_context>"}, 80); got != "" {
		t.Fatalf("只有系统块时应返回空标题，实际 %q", got)
	}
}

func TestScanEmitsMaterialArtifact(t *testing.T) {
	home := writeFixture(t)
	material := t.TempDir()
	out := call(t, "work-log.session.scan", `{"home":`+quote(home)+`,"date":"2026-10-09","limit":10,"material_root":`+quote(material)+`}`)
	arts, ok := out["artifacts"].([]any)
	if !ok || len(arts) != 1 {
		t.Fatalf("扫描应产出 1 个会话素材 artifact，实际 %#v", out["artifacts"])
	}
	artifact := arts[0].(map[string]any)
	if artifact["artifact_type"] != "session_digest" || artifact["artifact_id"] != "session-digest" {
		t.Fatalf("素材 artifact 标识异常: %#v", artifact)
	}
	uri, _ := artifact["uri"].(string)
	path := strings.TrimPrefix(uri, "file:///")
	path = filepath.FromSlash(path)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("素材文件未写入或不可读: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("素材文件不是合法 JSON: %v", err)
	}
	if _, ok := payload["sessions"].([]any); !ok {
		t.Fatalf("素材应符合 session-digest schema（含 sessions）: %v", payload)
	}
	if _, ok := payload["total"]; !ok {
		t.Fatalf("素材应符合 session-digest schema（含 total）: %v", payload)
	}
}

func TestMaterialRootsPreferExplicitThenDataRootThenWorkspace(t *testing.T) {
	dataRoot := t.TempDir()
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", dataRoot)
	explicit := t.TempDir()
	workspace := t.TempDir()

	roots := materialRoots(input{MaterialRoot: explicit, WorkspaceRoot: workspace})
	if len(roots) < 3 {
		t.Fatalf("素材目录候选过少: %#v", roots)
	}
	if roots[0] != explicit {
		t.Fatalf("显式 material_root 应排第一: %#v", roots)
	}
	if roots[1] != filepath.Join(dataRoot, "material") {
		t.Fatalf("扩展私有数据目录应排第二: %#v", roots)
	}
	if roots[2] != filepath.Join(workspace, ".himind", "material") {
		t.Fatalf("运行工作区应作为回退候选: %#v", roots)
	}
}

func TestMaterialRootsIgnoreUnresolvedWorkspaceLiteral(t *testing.T) {
	// 宿主没有解析 input.workspace_root 时，传下来的是字面量而不是真实路径；
	// 按相对目录处理会把素材落到进程当前目录里，因此必须忽略。
	for _, literal := range []string{"input.workspace_root", "${workspace_root}"} {
		for _, root := range materialRoots(input{WorkspaceRoot: literal}) {
			if strings.Contains(root, "workspace_root") {
				t.Fatalf("未解析的工作区字面量不应进入候选: %#v", root)
			}
		}
	}
}

func TestWriteMaterialFallsBackToWorkspace(t *testing.T) {
	// 私有目录不可写（父路径是文件）时，素材应回退写到工作区，而不是直接失败。
	blocker := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("准备阻塞路径失败: %v", err)
	}
	workspace := t.TempDir()
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", filepath.Join(blocker, "data"))
	t.Setenv("LOCALAPPDATA", filepath.Join(blocker, "local"))

	artifact, err := writeMaterial(
		input{MaterialRoot: filepath.Join(blocker, "explicit"), WorkspaceRoot: workspace},
		"2026-01-01", "2026-01-02", map[string]any{"sessions": []any{}},
	)
	if err != nil {
		t.Fatalf("应回退到工作区写入: %v", err)
	}
	path := filepath.FromSlash(strings.TrimPrefix(artifact["uri"].(string), "file:///"))
	want := filepath.Join(workspace, ".himind", "material", "session-digest-2026-01-01_2026-01-02.json")
	if !strings.EqualFold(path, want) {
		t.Fatalf("素材应写到工作区素材目录: %s", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("素材文件不存在: %v", err)
	}
}

func TestWriteMaterialReportsEveryFailedRoot(t *testing.T) {
	// 全部候选都不可写时必须带出每个目录的真实系统错误，而不是只回一句「写入失败」。
	blocker := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("准备阻塞路径失败: %v", err)
	}
	tempRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempRoot, "HiMindAgent"), []byte("x"), 0o644); err != nil {
		t.Fatalf("准备阻塞临时目录失败: %v", err)
	}
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, ".himind"), []byte("x"), 0o644); err != nil {
		t.Fatalf("准备阻塞工作目录失败: %v", err)
	}
	explicit := filepath.Join(blocker, "explicit")
	dataRoot := filepath.Join(blocker, "data")
	t.Chdir(workDir)
	t.Setenv("TEMP", tempRoot)
	t.Setenv("TMP", tempRoot)
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", dataRoot)
	t.Setenv("LOCALAPPDATA", filepath.Join(blocker, "local"))

	_, err := writeMaterial(input{MaterialRoot: explicit}, "2026-01-01", "2026-01-02", map[string]any{})
	if err == nil {
		t.Fatalf("全部候选不可写时应返回错误")
	}
	message := err.Error()
	if !strings.Contains(message, "会话素材写入失败") {
		t.Fatalf("错误应说明素材写入失败: %v", err)
	}
	for _, want := range []string{explicit, filepath.Join(dataRoot, "material")} {
		if !strings.Contains(message, want) {
			t.Fatalf("错误应逐个列出失败目录 %s: %v", want, err)
		}
	}
}

func TestMaterialRootsStripExtendedLengthPrefix(t *testing.T) {
	// 宿主把工作区按 \\?\F:\... 传下来；带前缀的路径写出来会变成引擎不认的 file URI。
	roots := materialRoots(input{MaterialRoot: `\\?\F:\ws\material`})
	if len(roots) == 0 {
		t.Fatalf("应至少给出一个候选目录")
	}
	if roots[0] != `F:\ws\material` {
		t.Fatalf("应去掉扩展长度前缀: %#v", roots)
	}
	for _, root := range roots {
		if strings.Contains(root, `?`) {
			t.Fatalf("候选目录不应带扩展长度前缀: %#v", root)
		}
	}

	workspace := t.TempDir()
	artifact, err := writeMaterial(input{WorkspaceRoot: `\\?\` + workspace}, "2026-01-01", "2026-01-01", map[string]any{})
	if err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	uri := artifact["uri"].(string)
	if !strings.HasPrefix(uri, "file:///") || strings.Contains(uri, "?") {
		t.Fatalf("artifact URI 必须是普通本地文件 URI: %s", uri)
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

func TestActiveMinutesClipsIdleGaps(t *testing.T) {
	base := time.Date(2026, 10, 9, 9, 0, 0, 0, time.Local)
	times := []time.Time{
		base,
		base.Add(5 * time.Minute),
		base.Add(10 * time.Minute),
		base.Add(200 * time.Minute), // 长时间离开应被截断为 idleGapMinutes。
	}
	if got := activeMinutes(times); got != 30 {
		t.Fatalf("活跃工时应为 5+5+20=30 分钟，实际 %d", got)
	}
	if got := activeMinutes([]time.Time{base}); got != 0 {
		t.Fatalf("单条消息不应产生工时，实际 %d", got)
	}
}

func TestResolveMinutesPrefersMeasured(t *testing.T) {
	base := time.Date(2026, 10, 9, 9, 0, 0, 0, time.Local)
	times := []time.Time{base, base.Add(10 * time.Minute)}
	if minutes, source := resolveMinutes(times, base, base.Add(2*time.Hour), 3); source != "measured" || minutes != 10 {
		t.Fatalf("应优先实测，实际 %d/%s", minutes, source)
	}
	if minutes, source := resolveMinutes(nil, base, base.Add(45*time.Minute), 3); source != "span" || minutes != 45 {
		t.Fatalf("无逐条时间时应用区间，实际 %d/%s", minutes, source)
	}
	if minutes, source := resolveMinutes(nil, time.Time{}, time.Time{}, 4); source != "estimated" || minutes != 12 {
		t.Fatalf("完全无时间时应按轮次估算，实际 %d/%s", minutes, source)
	}
}

func TestFeatureFromDirectory(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "workspace", "my-repo")
	sub := filepath.Join(repo, "src", "calendar")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := featureFromDirectory(sub, "my-repo"); got != "calendar" {
		t.Fatalf("git 仓库内应取相对根的末级目录，实际 %q", got)
	}
	if got := featureFromDirectory(repo, "my-repo"); got != "" {
		t.Fatalf("仓库根不应推断出功能，实际 %q", got)
	}
	loose := filepath.Join(home, "loose", "billing")
	if err := os.MkdirAll(loose, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := featureFromDirectory(loose, "loose"); got != "billing" {
		t.Fatalf("非仓库且目录名不等于项目时应取目录名，实际 %q", got)
	}
}

func TestCandidateEntriesCarryFeatureAndMinutes(t *testing.T) {
	digests := []sessionDigest{{
		Source: "codex", SessionID: "s1", Project: "proj", Directory: "F:\\proj\\mod",
		Title: "T", StartedAt: "2026-10-09T09:00:00+08:00", EndedAt: "2026-10-09T10:00:00+08:00",
		Minutes: 42, MinutesSource: "measured", TurnCount: 3,
	}}
	entries := candidateEntries(digests)
	if len(entries) != 1 {
		t.Fatalf("应产出 1 条，实际 %d", len(entries))
	}
	item := entries[0]
	if item["minutes"].(int) != 42 || item["minutes_source"] != "measured" {
		t.Fatalf("候选条目应携带实测工时: %#v", item)
	}
	if item["started_at"] != "2026-10-09T09:00:00+08:00" {
		t.Fatalf("候选条目应保留真实开始时间: %#v", item)
	}
	if item["feature"] != "mod" {
		t.Fatalf("候选条目应推断出功能模块，实际 %#v", item["feature"])
	}
}

func TestScanCandidateEntriesMeasureMinutes(t *testing.T) {
	home := writeFixture(t)
	out := call(t, "work-log.session.scan", `{"home":`+quote(home)+`,"date":"2026-10-09","limit":10}`)
	entries := out["entries"].([]any)
	bySession := map[string]map[string]any{}
	for _, raw := range entries {
		item := raw.(map[string]any)
		bySession[item["session_id"].(string)] = item
	}
	codex := bySession["codex-1"]
	if codex == nil || codex["minutes_source"] != "measured" {
		t.Fatalf("Codex 会话应按消息间隔实测工时: %#v", codex)
	}
	claude := bySession["claude-1"]
	if claude == nil || claude["minutes_source"] != "span" || claude["minutes"].(float64) != 30 {
		t.Fatalf("Claude 会话应按起止区间给出工时: %#v", claude)
	}
}

// TestEnrichSessionPathUsesManifest 验证仓库自述清单优先，并读取声明的项目/展项。
func TestEnrichSessionPathUsesManifest(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "proj")
	sub := filepath.Join(repo, "assets", "scenes")
	if err := os.MkdirAll(filepath.Join(repo, ".himind"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"project_name":"深圳科技馆","exhibit_name":"太空环"}`
	if err := os.WriteFile(filepath.Join(repo, ".himind", "project.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	digest := sessionDigest{Source: "codex", SessionID: "s1", Directory: sub, Project: "proj"}
	enrichSessionPath(&digest)
	if digest.RepoPath != repo {
		t.Fatalf("repo_path 应为清单所在目录，实际 %q", digest.RepoPath)
	}
	if digest.Project != "深圳科技馆" || digest.Exhibit != "太空环" {
		t.Fatalf("应读取清单声明的项目/展项，实际 project=%q exhibit=%q", digest.Project, digest.Exhibit)
	}
}

// TestEnrichSessionPathResolvesWorktree 验证链接工作树解析到主仓库工作树根，
// 而不是停在 C 盘缓存里的工作树目录。
func TestEnrichSessionPathResolvesWorktree(t *testing.T) {
	home := t.TempDir()
	mainRepo := filepath.Join(home, "WebProjects", "AndX")
	sub := filepath.Join(mainRepo, "src")
	if err := os.MkdirAll(filepath.Join(mainRepo, ".git", "worktrees", "wt-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// 模拟 C 盘缓存里的链接工作树：.git 为文件，指向主仓库。
	worktree := filepath.Join(home, "cache", "copilot-worktrees", "AndX", "wt-1")
	if err := os.MkdirAll(filepath.Join(worktree, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitdir := "gitdir: " + filepath.ToSlash(filepath.Join(mainRepo, ".git", "worktrees", "wt-1"))
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte(gitdir), 0o644); err != nil {
		t.Fatal(err)
	}

	digest := sessionDigest{Source: "copilot", SessionID: "s3", Directory: filepath.Join(worktree, "pkg")}
	enrichSessionPath(&digest)
	if digest.RepoPath != mainRepo {
		t.Fatalf("链接工作树应解析到主仓库工作树 %q，实际 %q", mainRepo, digest.RepoPath)
	}
}

// TestGitMainRootWindowsBackslash 验证 gitdir 用反斜杠、且指向更深模块时也能截到主仓库根。
func TestGitMainRootWindowsBackslash(t *testing.T) {
	home := t.TempDir()
	mainRepo := filepath.Join(home, "IODevice")
	if err := os.MkdirAll(filepath.Join(mainRepo, ".git", "worktrees", "wt-2", "modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(home, "cache", "wt-2", "IODevice")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	gitdir := "gitdir: " + strings.ReplaceAll(filepath.Join(mainRepo, ".git", "worktrees", "wt-2", "modules", "IODevice"), "/", "\\")
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte(gitdir), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := gitMainRoot(worktree); got != mainRepo {
		t.Fatalf("应截断到主仓库根 %q，实际 %q", mainRepo, got)
	}
}

// TestEnrichSessionPathFallsBackToGitRoot 验证没有清单时退回 git 仓库根。
func TestEnrichSessionPathFallsBackToGitRoot(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "my-repo")
	sub := filepath.Join(repo, "src", "calendar")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	digest := sessionDigest{Source: "codex", SessionID: "s2", Directory: sub}
	enrichSessionPath(&digest)
	if digest.RepoPath != repo {
		t.Fatalf("无清单时应退回 git 仓库根，实际 %q", digest.RepoPath)
	}
}

// TestCandidateEntriesCarryRepoPath 验证候选条目携带 repo_path 与展项。
func TestCandidateEntriesCarryRepoPath(t *testing.T) {
	digests := []sessionDigest{{
		Source: "codex", SessionID: "s1", Project: "proj",
		RepoPath: "F:\\work\\repo", Exhibit: "太空环",
		Title: "T", StartedAt: "2026-10-09T09:00:00+08:00",
	}}
	entries := candidateEntries(digests)
	if len(entries) != 1 || entries[0]["repo_path"] != "F:\\work\\repo" {
		t.Fatalf("候选条目应携带 repo_path: %#v", entries)
	}
	if entries[0]["exhibit"] != "太空环" {
		t.Fatalf("候选条目应携带展项: %#v", entries[0])
	}
}

// writeVSCodeChatFixture 构造一个 workspaceStorage/<hash>：含 workspace.json 与一个会话文件。
func writeVSCodeChatFixture(t *testing.T, wsDir, workspaceJSON, fileName string, sessionID, title string, start time.Time) {
	t.Helper()
	chatDir := filepath.Join(wsDir, "chatSessions")
	if err := os.MkdirAll(chatDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, "workspace.json"), []byte(workspaceJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	header := map[string]any{
		"version": 3, "creationDate": start.UnixMilli(), "customTitle": title, "sessionId": sessionID,
		"requests": []any{
			map[string]any{"requestId": "r1", "timestamp": start.UnixMilli(), "message": map[string]any{"text": "实现登录功能"}, "response": []any{}},
		},
	}
	headerLine, _ := json.Marshal(map[string]any{"kind": 0, "v": header})
	appendLine, _ := json.Marshal(map[string]any{"kind": 2, "k": []any{"requests"}, "v": []any{
		map[string]any{"requestId": "r2", "timestamp": start.Add(30 * time.Minute).UnixMilli(), "message": map[string]any{"text": "补充回归测试"}, "response": []any{}},
	}})
	responseLine, _ := json.Marshal(map[string]any{"kind": 2, "k": []any{"requests", 0, "response"}, "v": []any{
		map[string]any{"kind": "markdown", "value": "已完成登录功能并补充测试"},
	}})
	content := strings.Join([]string{string(headerLine), string(appendLine), string(responseLine)}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(chatDir, fileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestScanVSCodeCopilot 验证稳定版与 Insiders 两个根下的 Copilot Chat 会话都能被采集。
func TestScanVSCodeCopilot(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	home := t.TempDir()
	start := time.Date(2026, 10, 9, 2, 0, 0, 0, time.UTC)
	stableWs := filepath.Join(home, "AppData", "Roaming", "Code", "User", "workspaceStorage", "stablehash")
	writeVSCodeChatFixture(t, stableWs, `{"folder":"file:///f%3A/WebProjects/demo"}`, "vs-1.jsonl", "vs-1", "构建并启动FileSharer项目", start)
	insidersWs := filepath.Join(home, "AppData", "Roaming", "Code - Insiders", "User", "workspaceStorage", "insidershash")
	writeVSCodeChatFixture(t, insidersWs, `{"folder":"file:///f%3A/WebProjects/insiders-proj"}`, "vs-2.jsonl", "vs-2", "Insiders 会话", start)

	sources := call(t, "work-log.session.sources", `{"home":`+quote(home)+`}`)
	byID := map[string]map[string]any{}
	for _, raw := range sources["sources"].([]any) {
		item := raw.(map[string]any)
		byID[item["id"].(string)] = item
	}
	if byID["vscode"]["exists"] != true || byID["vscode"]["supported"] != true {
		t.Fatalf("VS Code GitHub Copilot 应被识别为可用: %#v", byID["vscode"])
	}
	if byID["vscode"]["files"].(float64) != 2 {
		t.Fatalf("应统计到 2 个会话文件: %#v", byID["vscode"])
	}

	out := call(t, "work-log.session.scan", `{"home":`+quote(home)+`,"date":"2026-10-09","limit":10,"source":"vscode"}`)
	if out["total"].(float64) != 2 {
		t.Fatalf("应扫描到 2 个 VS Code 会话，实际 %v", out["total"])
	}
	seen := map[string]map[string]any{}
	for _, raw := range out["sessions"].([]any) {
		item := raw.(map[string]any)
		seen[item["session_id"].(string)] = item
	}
	first := seen["vs-1"]
	if first == nil {
		t.Fatalf("缺少 vs-1 会话: %#v", seen)
	}
	if first["source"] != "vscode" {
		t.Fatalf("来源应标记为 vscode: %#v", first)
	}
	if first["project"] != "demo" {
		t.Fatalf("应通过 workspace.json 解析出项目目录名 demo: %#v", first["project"])
	}
	if first["title"] != "构建并启动FileSharer项目" {
		t.Fatalf("标题应取会话 customTitle: %#v", first["title"])
	}
	if first["date"] != "2026-10-09" {
		t.Fatalf("日期应为本地 2026-10-09: %v", first["date"])
	}
	prompts := first["user_prompts"].([]any)
	if len(prompts) != 2 || prompts[0] != "实现登录功能" || prompts[1] != "补充回归测试" {
		t.Fatalf("应回放追加的请求: %#v", prompts)
	}
	excerpts := first["assistant_excerpts"].([]any)
	if len(excerpts) != 1 || !strings.Contains(excerpts[0].(string), "已完成登录功能") {
		t.Fatalf("应回放追加的响应分片: %#v", excerpts)
	}
	if seen["vs-2"] == nil || seen["vs-2"]["project"] != "insiders-proj" {
		t.Fatalf("应采集 Insiders 根下的会话: %#v", seen["vs-2"])
	}
}

// TestVSCodeCopilotSkipsEmptySession 验证无用户内容的空会话被跳过。
func TestVSCodeCopilotSkipsEmptySession(t *testing.T) {
	home := t.TempDir()
	start := time.Date(2026, 10, 9, 2, 0, 0, 0, time.UTC)
	wsDir := filepath.Join(home, "AppData", "Roaming", "Code", "User", "workspaceStorage", "emptyhash")
	chatDir := filepath.Join(wsDir, "chatSessions")
	if err := os.MkdirAll(chatDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, "workspace.json"), []byte(`{"folder":"file:///f%3A/WebProjects/empty"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	header := map[string]any{"version": 3, "creationDate": start.UnixMilli(), "sessionId": "vs-empty", "requests": []any{}}
	line, _ := json.Marshal(map[string]any{"kind": 0, "v": header})
	if err := os.WriteFile(filepath.Join(chatDir, "vs-empty.jsonl"), []byte(string(line)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	digests, err := scanVSCodeCopilot([]string{filepath.Dir(filepath.Dir(chatDir))}, start, start.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(digests) != 1 {
		t.Fatalf("应扫描到 1 个会话（内容为空由后续空会话过滤处理）: %#v", digests)
	}
	if len(digests[0].UserPrompts) != 0 {
		t.Fatalf("空会话不应有用户提示: %#v", digests[0].UserPrompts)
	}
}

// TestScanOpenCodePureGoSQLite 验证内置纯 Go 驱动能直接读取 OpenCode 会话库，
// 不需要外部 sqlite3 CLI。
func TestScanOpenCodePureGoSQLite(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	schema := []string{
		`CREATE TABLE project (id TEXT PRIMARY KEY, name TEXT, worktree TEXT)`,
		`CREATE TABLE session_v2 (id TEXT PRIMARY KEY, project_id TEXT, slug TEXT, directory TEXT, title TEXT, model TEXT, cost REAL, tokens_input INTEGER, tokens_output INTEGER, time_created INTEGER, time_updated INTEGER)`,
		`CREATE TABLE session_message (session_id TEXT, type TEXT, data TEXT, time_created INTEGER)`,
	}
	for _, stmt := range schema {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO project (id, name, worktree) VALUES ('p1','项目看板','F:/proj/kb')`); err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 10, 9, 1, 0, 0, 0, time.UTC).UnixMilli()
	updated := created + 45*60*1000
	if _, err := db.Exec(`INSERT INTO session_v2 (id, project_id, slug, directory, title, model, cost, tokens_input, tokens_output, time_created, time_updated) VALUES ('oc-1','p1','s','F:/proj/kb/src','实现日历','x',0.5,100,200,?,?)`, created, updated); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO session_message (session_id, type, data, time_created) VALUES ('oc-1','user','{"text":"实现日历视图"}',?)`, created); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO session_message (session_id, type, data, time_created) VALUES ('oc-1','assistant','{"content":[{"text":"已完成月历栅格"}]}',?)`, created+30*60*1000); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	start := time.Date(2026, 10, 9, 0, 0, 0, 0, time.Local)
	digests, err := scanOpenCode(dbPath, start, start.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("纯 Go 驱动读取 OpenCode 库失败: %v", err)
	}
	if len(digests) != 1 {
		t.Fatalf("应扫描到 1 个 OpenCode 会话，实际 %d", len(digests))
	}
	d := digests[0]
	if d.SessionID != "oc-1" || d.Project != "项目看板" || d.Title != "实现日历" {
		t.Fatalf("OpenCode 会话字段异常: %#v", d)
	}
	if len(d.UserPrompts) != 1 || d.UserPrompts[0] != "实现日历视图" {
		t.Fatalf("用户提示提取异常: %#v", d.UserPrompts)
	}
	if len(d.AssistantExcerpts) != 1 || !strings.Contains(d.AssistantExcerpts[0], "月历栅格") {
		t.Fatalf("助手摘要提取异常: %#v", d.AssistantExcerpts)
	}
	if d.TokensInput != 100 || d.TokensOutput != 200 {
		t.Fatalf("token 统计异常: %#v", d)
	}
}

// TestScanPrivacyExcludePath 验证命中排除路径前缀的会话被整条跳过，其它来源照常保留。
func TestScanPrivacyExcludePath(t *testing.T) {
	home := writeFixture(t)
	out := call(t, "work-log.session.scan", `{"home":`+quote(home)+`,"date":"2026-10-09","limit":10,"exclude_paths":["f:/proj"]}`)
	if out["total"].(float64) != 1 {
		t.Fatalf("排除 codex 路径后应只剩 1 条，实际 %v", out["total"])
	}
	sessions := out["sessions"].([]any)
	if sessions[0].(map[string]any)["source"] != "claude-code" {
		t.Fatalf("保留的应是 claude-code 会话: %#v", sessions[0])
	}
}

// TestScanPrivacyExcludeKeyword 验证命中排除关键词的会话被整条跳过。
func TestScanPrivacyExcludeKeyword(t *testing.T) {
	home := writeFixture(t)
	out := call(t, "work-log.session.scan", `{"home":`+quote(home)+`,"date":"2026-10-09","limit":10,"exclude_keywords":["实现登录"]}`)
	if out["total"].(float64) != 1 {
		t.Fatalf("排除关键词后应只剩 1 条，实际 %v", out["total"])
	}
	sessions := out["sessions"].([]any)
	if sessions[0].(map[string]any)["source"] != "claude-code" {
		t.Fatalf("保留的应是 claude-code 会话: %#v", sessions[0])
	}
}

// TestScanPrivacyConfigFile 验证可从 <log_root>/config/privacy.json 读取排除规则。
func TestScanPrivacyConfigFile(t *testing.T) {
	home := writeFixture(t)
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	config := `{"exclude_keywords":["实现登录"]}`
	if err := os.WriteFile(filepath.Join(configDir, "privacy.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	out := call(t, "work-log.session.scan", `{"home":`+quote(home)+`,"log_root":`+quote(root)+`,"date":"2026-10-09","limit":10}`)
	if out["total"].(float64) != 1 {
		t.Fatalf("privacy.json 排除后应只剩 1 条，实际 %v", out["total"])
	}
}

// TestScanOpenCodeCrossDaySession 回归：跨多日的长会话在「创建日」之外的活跃日也必须被
// 采集，且每天只取当天消息。修复前只看 time_created，导致后续活跃日整体丢失。
func TestScanOpenCodeCrossDaySession(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE project (id TEXT PRIMARY KEY, name TEXT, worktree TEXT)`,
		`CREATE TABLE session_v2 (id TEXT PRIMARY KEY, project_id TEXT, slug TEXT, directory TEXT, title TEXT, model TEXT, cost REAL, tokens_input INTEGER, tokens_output INTEGER, time_created INTEGER, time_updated INTEGER)`,
		`CREATE TABLE session_message (session_id TEXT, type TEXT, data TEXT, time_created INTEGER)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO project (id, name, worktree) VALUES ('p1','IODevice','O:/DevModules/IODevice')`); err != nil {
		t.Fatal(err)
	}
	day1 := time.Date(2026, 9, 22, 10, 0, 0, 0, time.Local)
	day2 := time.Date(2026, 9, 24, 10, 0, 0, 0, time.Local)
	if _, err := db.Exec(`INSERT INTO session_v2 (id, project_id, slug, directory, title, model, cost, tokens_input, tokens_output, time_created, time_updated) VALUES ('ses-long','p1','s','O:/DevModules/IODevice','IOUI Simulator架构','x',0,0,0,?,?)`, day1.UnixMilli(), day2.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	insert := func(kind, data string, when time.Time) {
		if _, err := db.Exec(`INSERT INTO session_message (session_id, type, data, time_created) VALUES ('ses-long',?,?,?)`, kind, data, when.UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	insert("user", `{"text":"第一天：审查Simulator架构"}`, day1)
	insert("user", `{"text":"第二天：推进时间线创作"}`, day2)
	insert("assistant", `{"content":[{"text":"第二天已完成时间线改造"}]}`, day2.Add(30*time.Minute))
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	start2 := time.Date(2026, 9, 24, 0, 0, 0, 0, time.Local)
	got2, err := scanOpenCode(dbPath, start2, start2.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(got2) != 1 {
		t.Fatalf("跨日会话在活跃日应被采集，实际 %d 条: %#v", len(got2), got2)
	}
	if got2[0].Date != "2026-09-24" {
		t.Fatalf("归日应为活跃日 2026-09-24，实际 %q", got2[0].Date)
	}
	if len(got2[0].UserPrompts) != 1 || got2[0].UserPrompts[0] != "第二天：推进时间线创作" {
		t.Fatalf("应只取当天用户提示，实际 %#v", got2[0].UserPrompts)
	}
	if len(got2[0].AssistantExcerpts) != 1 || !strings.Contains(got2[0].AssistantExcerpts[0], "第二天已完成") {
		t.Fatalf("应只取当天助手摘要，实际 %#v", got2[0].AssistantExcerpts)
	}

	start1 := time.Date(2026, 9, 22, 0, 0, 0, 0, time.Local)
	got1, err := scanOpenCode(dbPath, start1, start1.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(got1) != 1 || got1[0].Date != "2026-09-22" {
		t.Fatalf("创建日归日异常: %#v", got1)
	}
	if len(got1[0].UserPrompts) != 1 || got1[0].UserPrompts[0] != "第一天：审查Simulator架构" {
		t.Fatalf("创建日应只取当天用户提示，实际 %#v", got1[0].UserPrompts)
	}
}

// TestScanVSCodeCopilotCrossDaySession 回归：VS Code 会话在创建日之外的活跃日也能采集，
// 且只回放当天的请求。
func TestScanVSCodeCopilotCrossDaySession(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	home := t.TempDir()
	root := filepath.Join(home, "AppData", "Roaming", "Code", "User", "workspaceStorage")
	wsDir := filepath.Join(root, "hash")
	chatDir := filepath.Join(wsDir, "chatSessions")
	if err := os.MkdirAll(chatDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, "workspace.json"), []byte(`{"folder":"file:///o%3A/DevModules/IODevice"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	day1 := time.Date(2026, 9, 22, 9, 0, 0, 0, time.Local)
	day2 := time.Date(2026, 9, 24, 9, 0, 0, 0, time.Local)
	header := map[string]any{"version": 3, "creationDate": day1.UnixMilli(), "customTitle": "IOUI Simulator架构审查", "sessionId": "vs-long", "requests": []any{
		map[string]any{"requestId": "r1", "timestamp": day1.UnixMilli(), "message": map[string]any{"text": "第一天：审查Simulator架构"}, "response": []any{}},
	}}
	headerLine, _ := json.Marshal(map[string]any{"kind": 0, "v": header})
	appendLine, _ := json.Marshal(map[string]any{"kind": 2, "k": []any{"requests"}, "v": []any{
		map[string]any{"requestId": "r2", "timestamp": day2.UnixMilli(), "message": map[string]any{"text": "第二天：推进时间线创作"}, "response": []any{}},
	}})
	if err := os.WriteFile(filepath.Join(chatDir, "vs-long.jsonl"), []byte(strings.Join([]string{string(headerLine), string(appendLine)}, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	digests, err := scanVSCodeCopilot([]string{root}, day2, day2.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(digests) != 1 {
		t.Fatalf("跨日 VS Code 会话在活跃日应被采集，实际 %d 条: %#v", len(digests), digests)
	}
	if digests[0].Date != "2026-09-24" {
		t.Fatalf("归日应为活跃日 2026-09-24，实际 %q", digests[0].Date)
	}
	if len(digests[0].UserPrompts) != 1 || digests[0].UserPrompts[0] != "第二天：推进时间线创作" {
		t.Fatalf("应只取当天请求，实际 %#v", digests[0].UserPrompts)
	}
}

// TestScanCodexCrossDaySession 回归：Codex 长会话在创建日之外的活跃日也能采集。
func TestScanCodexCrossDaySession(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	home := t.TempDir()
	path := filepath.Join(home, ".codex", "sessions", "2026", "09", "22", "rollout-2026-09-22T09-00-00-long.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"timestamp":"2026-09-22T01:00:00Z","type":"session_meta","payload":{"session_id":"codex-long","id":"codex-long","cwd":"F:\\proj\\demo","timestamp":"2026-09-22T01:00:00Z"}}`,
		`{"timestamp":"2026-09-22T01:00:05Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"第一天需求"}]}}`,
		`{"timestamp":"2026-09-24T01:00:05Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"第二天需求"}]}}`,
		`{"timestamp":"2026-09-24T01:01:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"第二天产出"}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	day2 := time.Date(2026, 9, 24, 0, 0, 0, 0, time.Local)
	digests, err := scanCodex(filepath.Join(home, ".codex"), day2, day2.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(digests) != 1 {
		t.Fatalf("跨日 Codex 会话在活跃日应被采集，实际 %d 条: %#v", len(digests), digests)
	}
	if len(digests[0].UserPrompts) != 1 || digests[0].UserPrompts[0] != "第二天需求" {
		t.Fatalf("应只取当天用户提示，实际 %#v", digests[0].UserPrompts)
	}
	if len(digests[0].AssistantExcerpts) != 1 || !strings.Contains(digests[0].AssistantExcerpts[0], "第二天产出") {
		t.Fatalf("应只取当天助手摘要，实际 %#v", digests[0].AssistantExcerpts)
	}
}

// TestScanCopilotCrossDaySession 回归：Copilot CLI 长会话在创建日之外的活跃日也能采集。
func TestScanCopilotCrossDaySession(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "session-store.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE sessions (id TEXT PRIMARY KEY, cwd TEXT, repository TEXT, summary TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE turns (session_id TEXT, user_message TEXT, assistant_response TEXT, timestamp TEXT)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO sessions (id, cwd, repository, summary, created_at, updated_at) VALUES ('cp-long','O:/DevModules/IODevice','','长会话','2026-09-22 09:00:00','2026-09-24 09:00:00')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO turns (session_id, user_message, assistant_response, timestamp) VALUES ('cp-long','第一天需求','第一天产出','2026-09-22 09:00:00')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO turns (session_id, user_message, assistant_response, timestamp) VALUES ('cp-long','第二天需求','第二天产出','2026-09-24 09:00:00')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	day2 := time.Date(2026, 9, 24, 0, 0, 0, 0, time.Local)
	digests, err := scanCopilot(dbPath, day2, day2.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(digests) != 1 {
		t.Fatalf("跨日 Copilot 会话在活跃日应被采集，实际 %d 条: %#v", len(digests), digests)
	}
	if len(digests[0].UserPrompts) != 1 || digests[0].UserPrompts[0] != "第二天需求" {
		t.Fatalf("应只取当天用户消息，实际 %#v", digests[0].UserPrompts)
	}
	if len(digests[0].AssistantExcerpts) != 1 || !strings.Contains(digests[0].AssistantExcerpts[0], "第二天产出") {
		t.Fatalf("应只取当天助手摘要，实际 %#v", digests[0].AssistantExcerpts)
	}
}

// writeClaudeDesktopTranscript 写一份 Claude Code 格式的 JSONL 转写。
func writeClaudeDesktopTranscript(t *testing.T, path string, lines []string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := strings.Join(lines, "\n")
	if content != "" {
		content += "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// claudeDesktopUserLine / claudeDesktopAssistantLine 生成一份转写记录。
func claudeDesktopUserLine(when time.Time, origin, text string) string {
	record := map[string]any{
		"type": "user", "timestamp": when.Format(time.RFC3339Nano),
		"origin":  map[string]any{"kind": origin},
		"message": map[string]any{"role": "user", "content": text},
	}
	encoded, _ := json.Marshal(record)
	return string(encoded)
}

func claudeDesktopAssistantLine(when time.Time, text string, tokensIn, tokensOut int) string {
	record := map[string]any{
		"type": "assistant", "timestamp": when.Format(time.RFC3339Nano),
		"message": map[string]any{
			"role":    "assistant",
			"content": []any{map[string]any{"type": "text", "text": text}},
			"usage":   map[string]any{"input_tokens": tokensIn, "output_tokens": tokensOut},
		},
	}
	encoded, _ := json.Marshal(record)
	return string(encoded)
}

// writeClaudeDesktopMeta 写一份会话元数据（local_<uuid>.json）。
func writeClaudeDesktopMeta(t *testing.T, path string, fields map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeClaudeDesktopFixture 构造一个 Claude Desktop profile：一个 Code 标签页会话
// （元数据 + ~/.claude/projects 转写）与一个 local agent mode 会话（元数据 + 短目录转写）。
func writeClaudeDesktopFixture(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	root := filepath.Join(home, "AppData", "Local", "Claude-3p")
	work := filepath.Join(home, "WebProjects", "demo")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 9, 10, 0, 0, 0, time.Local)

	// Code 标签页：cwd 是真实仓库，转写落在 ~/.claude/projects/<编码>/<cliSessionId>.jsonl。
	codeDir := filepath.Join(root, "claude-code-sessions", "acct", "org")
	codeCLI := "aaaaaaaa-0000-4000-8000-000000000001"
	writeClaudeDesktopMeta(t, filepath.Join(codeDir, "local_bbbbbbbb-0000-4000-8000-000000000001.json"), map[string]any{
		"sessionId":      "local_bbbbbbbb-0000-4000-8000-000000000001",
		"cliSessionId":   codeCLI,
		"cwd":            work,
		"originCwd":      work,
		"title":          "实现登录功能",
		"model":          "claude-sonnet-4",
		"createdAt":      start.UnixMilli(),
		"lastActivityAt": start.Add(30 * time.Minute).UnixMilli(),
		"completedTurns": 2,
	})
	writeClaudeDesktopTranscript(t, filepath.Join(home, ".claude", "projects", encodeClaudeProjectDir(work), codeCLI+".jsonl"), []string{
		claudeDesktopUserLine(start, "human", "实现登录功能"),
		claudeDesktopAssistantLine(start.Add(time.Minute), "已完成登录功能并补充测试", 120, 45),
		// 工具结果不是真人输入，必须被过滤掉。
		claudeDesktopUserLine(start.Add(2*time.Minute), "tool", "tool result payload"),
	})

	// local agent mode：用户挂载真实文件夹，cwd 是 VM 沙箱；转写落在会话短目录下。
	agentDir := filepath.Join(root, "local-agent-mode-sessions", "acct", "org")
	agentCLI := "cccccccc-0000-4000-8000-000000000002"
	agentSession := "local_ab12cd34-0000-4000-8000-000000000002"
	writeClaudeDesktopMeta(t, filepath.Join(agentDir, agentSession+".json"), map[string]any{
		"sessionId":           agentSession,
		"cliSessionId":        agentCLI,
		"cwd":                 filepath.Join(agentDir, "ab12cd34", "outputs"),
		"userSelectedFolders": []string{work},
		"title":               "修复打包脚本",
		"initialMessage":      "修复打包脚本",
		"createdAt":           start.Add(time.Hour).UnixMilli(),
		"lastActivityAt":      start.Add(90 * time.Minute).UnixMilli(),
	})
	writeClaudeDesktopTranscript(t, filepath.Join(agentDir, "ab12cd34", ".claude", "projects", "session", agentCLI+".jsonl"), []string{
		claudeDesktopUserLine(start.Add(time.Hour), "human", "修复打包脚本"),
		claudeDesktopAssistantLine(start.Add(70*time.Minute), "已修正版本号变量", 60, 20),
	})
}

// TestScanClaudeDesktopSources 验证来源探测能发现 Claude Desktop profile 并统计会话数。
func TestScanClaudeDesktopSources(t *testing.T) {
	home := t.TempDir()
	writeClaudeDesktopFixture(t, home)
	out := call(t, "work-log.session.sources", `{"home":`+quote(home)+`}`)
	byID := map[string]map[string]any{}
	for _, raw := range out["sources"].([]any) {
		item := raw.(map[string]any)
		byID[item["id"].(string)] = item
	}
	source := byID["claude-desktop"]
	if source == nil {
		t.Fatalf("来源列表应包含 claude-desktop: %#v", byID)
	}
	if source["exists"] != true || source["supported"] != true {
		t.Fatalf("Claude Desktop 应被识别为可用: %#v", source)
	}
	if source["files"].(float64) != 2 {
		t.Fatalf("应统计到 2 个会话元数据文件: %#v", source["files"])
	}
}

// TestScanClaudeDesktop 验证 Code 标签页与 local agent mode 会话都能被采集，正文与 token 正确。
func TestScanClaudeDesktop(t *testing.T) {
	home := t.TempDir()
	writeClaudeDesktopFixture(t, home)
	out := call(t, "work-log.session.scan", `{"home":`+quote(home)+`,"date":"2026-10-09","limit":10,"source":"claude-desktop"}`)
	if out["total"].(float64) != 2 {
		t.Fatalf("应扫描到 2 个 Claude Desktop 会话，实际 %v（skipped=%v）", out["total"], out["skipped"])
	}
	sessions := map[string]map[string]any{}
	for _, raw := range out["sessions"].([]any) {
		item := raw.(map[string]any)
		sessions[item["session_id"].(string)] = item
	}
	code := sessions["local_bbbbbbbb-0000-4000-8000-000000000001"]
	if code == nil {
		t.Fatalf("缺少 Code 标签页会话: %#v", sessions)
	}
	if code["source"] != "claude-desktop" || code["title"] != "实现登录功能" {
		t.Fatalf("Code 会话字段异常: %#v", code)
	}
	if code["project"] != "demo" || code["date"] != "2026-10-09" {
		t.Fatalf("Code 会话项目/日期异常: %#v", code)
	}
	if code["tokens_input"].(float64) != 120 || code["tokens_output"].(float64) != 45 {
		t.Fatalf("Code 会话 token 统计异常: %#v", code)
	}
	prompts := code["user_prompts"].([]any)
	if len(prompts) != 1 || prompts[0] != "实现登录功能" {
		t.Fatalf("应过滤工具结果、只取真人输入: %#v", prompts)
	}
	excerpts := code["assistant_excerpts"].([]any)
	if len(excerpts) != 1 || !strings.Contains(excerpts[0].(string), "已完成登录功能") {
		t.Fatalf("助手摘要异常: %#v", excerpts)
	}

	agent := sessions["local_ab12cd34-0000-4000-8000-000000000002"]
	if agent == nil {
		t.Fatalf("缺少 local agent mode 会话: %#v", sessions)
	}
	if agent["title"] != "修复打包脚本" || agent["project"] != "demo" {
		t.Fatalf("agent 会话字段异常: %#v", agent)
	}
	if agentPrompts := agent["user_prompts"].([]any); len(agentPrompts) != 1 || agentPrompts[0] != "修复打包脚本" {
		t.Fatalf("agent 会话用户提示异常: %#v", agent["user_prompts"])
	}
}

// TestScanClaudeDesktopFallsBackToMetadata 验证转写缺失时用元数据兜底，仍能产出素材。
func TestScanClaudeDesktopFallsBackToMetadata(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	home := t.TempDir()
	root := filepath.Join(home, "AppData", "Local", "Claude-3p")
	agentDir := filepath.Join(root, "local-agent-mode-sessions", "acct", "org")
	start := time.Date(2026, 10, 9, 9, 0, 0, 0, time.Local)
	writeClaudeDesktopMeta(t, filepath.Join(agentDir, "local_deadbeef-0000-4000-8000-000000000003.json"), map[string]any{
		"sessionId":           "local_deadbeef-0000-4000-8000-000000000003",
		"cliSessionId":        "dddddddd-0000-4000-8000-000000000003",
		"cwd":                 filepath.Join(agentDir, "deadbeef", "outputs"),
		"userSelectedFolders": []string{filepath.Join(home, "WebProjects", "demo")},
		"title":               "整理需求文档",
		"initialMessage":      "把需求整理成文档",
		"createdAt":           start.UnixMilli(),
		"lastActivityAt":      start.Add(20 * time.Minute).UnixMilli(),
	})

	out := call(t, "work-log.session.scan", `{"home":`+quote(home)+`,"date":"2026-10-09","limit":10,"source":"claude-desktop"}`)
	if out["total"].(float64) != 1 {
		t.Fatalf("转写缺失时应按元数据兜底产出 1 条，实际 %v（skipped=%v）", out["total"], out["skipped"])
	}
	item := out["sessions"].([]any)[0].(map[string]any)
	if item["title"] != "整理需求文档" {
		t.Fatalf("应使用元数据标题: %#v", item["title"])
	}
	prompts := item["user_prompts"].([]any)
	if len(prompts) != 1 || prompts[0] != "把需求整理成文档" {
		t.Fatalf("应回退到 initialMessage: %#v", prompts)
	}
}

// TestScanClaudeDesktopCrossDaySession 回归：桌面端长会话在创建日之外的活跃日也能采集，
// 且只取当天的对话。
func TestScanClaudeDesktopCrossDaySession(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	home := t.TempDir()
	root := filepath.Join(home, "AppData", "Local", "Claude-3p")
	work := filepath.Join(home, "WebProjects", "demo")
	codeDir := filepath.Join(root, "claude-code-sessions", "acct", "org")
	cli := "eeeeeeee-0000-4000-8000-000000000004"
	day1 := time.Date(2026, 9, 22, 10, 0, 0, 0, time.Local)
	day2 := time.Date(2026, 9, 24, 10, 0, 0, 0, time.Local)
	writeClaudeDesktopMeta(t, filepath.Join(codeDir, "local_ffffffff-0000-4000-8000-000000000004.json"), map[string]any{
		"sessionId":      "local_ffffffff-0000-4000-8000-000000000004",
		"cliSessionId":   cli,
		"cwd":            work,
		"originCwd":      work,
		"title":          "跨日长会话",
		"createdAt":      day1.UnixMilli(),
		"lastActivityAt": day2.Add(time.Minute).UnixMilli(),
		"completedTurns": 2,
	})
	writeClaudeDesktopTranscript(t, filepath.Join(home, ".claude", "projects", encodeClaudeProjectDir(work), cli+".jsonl"), []string{
		claudeDesktopUserLine(day1, "human", "第一天需求"),
		claudeDesktopUserLine(day2, "human", "第二天需求"),
		claudeDesktopAssistantLine(day2.Add(time.Minute), "第二天产出", 10, 5),
	})

	digests, err := scanClaudeDesktop([]string{root}, home, day2, day2.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(digests) != 1 {
		t.Fatalf("跨日桌面端会话在活跃日应被采集，实际 %d 条: %#v", len(digests), digests)
	}
	if digests[0].Date != "2026-09-24" {
		t.Fatalf("归日应为活跃日 2026-09-24，实际 %q", digests[0].Date)
	}
	if len(digests[0].UserPrompts) != 1 || digests[0].UserPrompts[0] != "第二天需求" {
		t.Fatalf("应只取当天用户提示，实际 %#v", digests[0].UserPrompts)
	}
	if len(digests[0].AssistantExcerpts) != 1 || !strings.Contains(digests[0].AssistantExcerpts[0], "第二天产出") {
		t.Fatalf("应只取当天助手摘要，实际 %#v", digests[0].AssistantExcerpts)
	}
}

// TestEncodeClaudeProjectDir 验证工作目录编码与 Claude Code 的目录名规则一致。
func TestEncodeClaudeProjectDir(t *testing.T) {
	cases := map[string]string{
		`F:\WebProjects\demo`:      "F--WebProjects-demo",
		`c:\Users\me\source-repos`: "c--Users-me-source-repos",
		`/home/me/proj`:            "-home-me-proj",
	}
	for in, want := range cases {
		if got := encodeClaudeProjectDir(in); got != want {
			t.Fatalf("encodeClaudeProjectDir(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestClaudeDesktopShortID 验证 local agent mode 的会话短目录取自 sessionId 前缀。
func TestClaudeDesktopShortID(t *testing.T) {
	if got := claudeDesktopShortID("local_c58e56b0-45c5-4dde-9555-3b7e40e98b02"); got != "c58e56b0" {
		t.Fatalf("短目录应为 c58e56b0，实际 %q", got)
	}
	if got := claudeDesktopShortID("local_withoutdash"); got != "" {
		t.Fatalf("无短横线时应返回空，实际 %q", got)
	}
}
