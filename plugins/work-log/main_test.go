package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/MrBaoquan/himind-extensions/sdk/jsonrpc"
)

// call 通过 handle 走一遍 JSON-RPC 解码，保证测试覆盖真实入口。
func call(t *testing.T, method string, params string) map[string]any {
	t.Helper()
	result, rpcError := handle(jsonrpc.Request{Method: method, Params: []byte(params)})
	if rpcError != nil {
		t.Fatalf("%s 返回错误: %s", method, rpcError.Message)
	}
	// 经过一次 JSON 编解码，与 Agent 侧真实 RPC 边界保持一致（数字统一为 float64）。
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

func withDataRoot(t *testing.T) {
	t.Helper()
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
}

func TestUpsertReadAndMerge(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	params := `{"log_root":` + quote(root) + `,"date":"2026-10-09","entries":[
		{"source":"codex","session_id":"s1","project":"项目看板","category":"development","started_at":"09:30","ended_at":"11:00","title":"实现日志存储","summary":"完成 upsert/list/render"},
		{"source":"manual","project":"HiMind 扩展","category":"docs","title":"整理规范"}
	]}`
	upserted := call(t, "work-log.entry.upsert", params)
	if upserted["count"].(float64) != 2 {
		t.Fatalf("期望写入 2 条，实际 %v", upserted["count"])
	}
	if _, err := os.Stat(filepath.Join(root, "logs", "2026", "2026-10-09.json")); err != nil {
		t.Fatalf("日志文件未落盘: %v", err)
	}

	// 相同会话再次写入应合并而不是新增。
	mergeParams := `{"log_root":` + quote(root) + `,"date":"2026-10-09","entries":[
		{"source":"codex","session_id":"s1","title":"实现日志存储","summary":"已通过 go test"}
	]}`
	merged := call(t, "work-log.entry.upsert", mergeParams)
	if merged["count"].(float64) != 2 {
		t.Fatalf("合并后应仍为 2 条，实际 %v", merged["count"])
	}
	read := call(t, "work-log.entry.read", `{"log_root":`+quote(root)+`,"date":"2026-10-09"}`)
	entries := read["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("读取条目数异常: %d", len(entries))
	}
	var mergedEntry map[string]any
	for _, raw := range entries {
		item := raw.(map[string]any)
		if item["title"] == "实现日志存储" {
			mergedEntry = item
			break
		}
	}
	if mergedEntry == nil {
		t.Fatal("未找到合并后的条目")
	}
	if mergedEntry["summary"] != "已通过 go test" {
		t.Fatalf("合并未更新 summary: %v", mergedEntry["summary"])
	}
	if mergedEntry["created_at"] == "" || mergedEntry["created_at"] == nil {
		t.Fatalf("created_at 应被保留")
	}
}

func TestListFiltersByProject(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-08","entries":[{"project":"项目看板","title":"A"}]}`)
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-09","entries":[{"project":"HiMind 扩展","title":"B"}]}`)

	all := call(t, "work-log.entry.list", `{"log_root":`+quote(root)+`,"from":"2026-10-08","to":"2026-10-09"}`)
	if all["total"].(float64) != 2 {
		t.Fatalf("区间查询应返回 2 条，实际 %v", all["total"])
	}
	filtered := call(t, "work-log.entry.list", `{"log_root":`+quote(root)+`,"from":"2026-10-08","to":"2026-10-09","project":"看板"}`)
	if filtered["total"].(float64) != 1 {
		t.Fatalf("项目过滤应返回 1 条，实际 %v", filtered["total"])
	}
}

// 只给 project（不带 from/to/date）时必须跨全部历史查询：不能被默认收敛到今天，
// 否则新增的「项目推进记录」会漏掉全部历史条目。
func TestListByProjectWithoutRangeSpansHistory(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2025-01-02","entries":[{"project":"项目看板","title":"旧"}]}`)
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-08","entries":[{"project":"项目看板","title":"新"}]}`)
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-09","entries":[{"project":"别的项目","title":"其他"}]}`)

	byProject := call(t, "work-log.entry.list", `{"log_root":`+quote(root)+`,"project":"项目看板"}`)
	if byProject["total"].(float64) != 2 {
		t.Fatalf("不带区间按项目查询应跨历史返回 2 条，实际 %v", byProject["total"])
	}
	all := call(t, "work-log.entry.list", `{"log_root":`+quote(root)+`}`)
	if all["total"].(float64) != 3 {
		t.Fatalf("不带任何条件的查询应返回全部 3 条，实际 %v", all["total"])
	}
	// 单日 date 仍应收敛为一天。
	oneDay := call(t, "work-log.entry.list", `{"log_root":`+quote(root)+`,"date":"2026-10-09"}`)
	if oneDay["total"].(float64) != 1 {
		t.Fatalf("带 date 应只返回当天 1 条，实际 %v", oneDay["total"])
	}
}

func TestArchiveListAndReport(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-09","entries":[
		{"project":"项目看板","category":"development","started_at":"09:30","title":"实现存储","outcomes":["go test 通过"],"next":["补前端"]}
	]}`)

	archive := call(t, "work-log.archive.list", `{"log_root":`+quote(root)+`,"from":"2026-01-01","to":"2026-12-31"}`)
	if archive["total"].(float64) != 1 {
		t.Fatalf("归档应有 1 天，实际 %v", archive["total"])
	}
	all := call(t, "work-log.archive.list", `{"log_root":`+quote(root)+`}`)
	if all["total"].(float64) != 1 {
		t.Fatalf("不带区间应列出全部 1 天，实际 %v", all["total"])
	}

	rendered := call(t, "work-log.report.render", `{"log_root":`+quote(root)+`,"period":"daily","date":"2026-10-09","narrative":"今天完成了核心存储。"}`)
	report := rendered["report"].(map[string]any)
	if report["key"] != "2026-10-09" {
		t.Fatalf("日报键异常: %v", report["key"])
	}
	markdown := report["markdown"].(string)
	for _, want := range []string{"日报", "今天完成了核心存储。", "实现存储", "go test 通过"} {
		if !contains(markdown, want) {
			t.Fatalf("日报 Markdown 缺少 %q:\n%s", want, markdown)
		}
	}
	mdPath := report["md_path"].(string)
	if _, err := os.Stat(mdPath); err != nil {
		t.Fatalf("日报文件未落盘: %v", err)
	}
	read := call(t, "work-log.report.read", `{"log_root":`+quote(root)+`,"period":"daily","date":"2026-10-09"}`)
	if read["key"] != "2026-10-09" {
		t.Fatalf("报告读取键异常: %v", read["key"])
	}
	if read["date_from"] != "2026-10-09" || read["date_to"] != "2026-10-09" {
		t.Fatalf("报告读取应带范围: from=%v to=%v", read["date_from"], read["date_to"])
	}
	call(t, "work-log.report.render", `{"log_root":`+quote(root)+`,"period":"weekly","date":"2026-10-09"}`)
	weekly := call(t, "work-log.report.read", `{"log_root":`+quote(root)+`,"period":"weekly","date":"2026-10-09"}`)
	if weekly["date_from"] != "2026-10-05" || weekly["date_to"] != "2026-10-11" {
		t.Fatalf("周报读取范围异常: from=%v to=%v", weekly["date_from"], weekly["date_to"])
	}
}

// artifactFile 从 uri 读回落盘的 artifact 文件内容。
func artifactFile(t *testing.T, artifact map[string]any) map[string]any {
	t.Helper()
	uri := artifact["uri"].(string)
	path := filepath.FromSlash(strings.TrimPrefix(uri, "file:///"))
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 artifact 文件失败 (%s): %v", uri, err)
	}
	var content map[string]any
	if err := json.Unmarshal(body, &content); err != nil {
		t.Fatalf("artifact 文件必须是合法 JSON (%s): %v", uri, err)
	}
	return content
}

// 工作流会对 exitpoint 的 Artifact 做 JSON schema 校验：uri 必须指向内容合规的 JSON 文件。
func TestReportArtifactIsSchemaJSON(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-09","entries":[{"project":"项目看板","title":"实现存储"}]}`)
	rendered := call(t, "work-log.report.render", `{"log_root":`+quote(root)+`,"period":"daily","date":"2026-10-09"}`)
	arts := rendered["artifacts"].([]any)
	if len(arts) != 1 {
		t.Fatalf("应产出 1 个报告 artifact，实际 %d", len(arts))
	}
	artifact := arts[0].(map[string]any)
	if !strings.HasSuffix(artifact["uri"].(string), ".json") {
		t.Fatalf("报告 artifact 应指向 JSON 文件: %v", artifact["uri"])
	}
	content := artifactFile(t, artifact)
	rep, ok := content["report"].(map[string]any)
	if !ok {
		t.Fatalf("报告 artifact 缺少 report: %v", content)
	}
	for _, key := range []string{"period", "key", "markdown"} {
		if _, ok := rep[key]; !ok {
			t.Fatalf("report 缺少 schema 必需字段 %s: %v", key, rep)
		}
	}
}

func TestEntryArtifactMatchesSchema(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	upserted := call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-09","entries":[{"project":"项目看板","title":"A"}]}`)
	arts := upserted["artifacts"].([]any)
	if len(arts) != 1 {
		t.Fatalf("应产出 1 个条目 artifact，实际 %d", len(arts))
	}
	content := artifactFile(t, arts[0].(map[string]any))
	if content["date"] != "2026-10-09" {
		t.Fatalf("条目 artifact date 异常: %v", content["date"])
	}
	if content["count"].(float64) != 1 {
		t.Fatalf("条目 artifact 应带 count=1: %v", content["count"])
	}
	if _, ok := content["entries"].([]any); !ok {
		t.Fatalf("条目 artifact 缺少 entries: %v", content)
	}
}

func TestArchiveCalendar(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-09","entries":[
		{"project":"项目看板","category":"development","title":"A"},
		{"project":"HiMind 扩展","category":"docs","title":"B"}
	]}`)
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-20","entries":[{"project":"项目看板","title":"C"}]}`)

	cal := call(t, "work-log.archive.calendar", `{"log_root":`+quote(root)+`,"year":2026,"month":10}`)
	if cal["month_key"] != "2026-10" {
		t.Fatalf("月份键异常: %v", cal["month_key"])
	}
	if cal["days_in_month"].(float64) != 31 || cal["active_days"].(float64) != 2 || cal["total_entries"].(float64) != 3 {
		t.Fatalf("月历统计异常: days_in_month=%v active=%v total=%v", cal["days_in_month"], cal["active_days"], cal["total_entries"])
	}
	// 2026-10-01 是周四，周一起始时首列索引应为 4（周一=1 … 周日=7）。
	if cal["first_weekday"].(float64) != 4 {
		t.Fatalf("首日星期异常: %v", cal["first_weekday"])
	}
	days := cal["days"].([]any)
	if len(days) != 31 {
		t.Fatalf("当月天数应完整为 31，实际 %d", len(days))
	}
	byDate := map[string]map[string]any{}
	for _, raw := range days {
		item := raw.(map[string]any)
		byDate[item["date"].(string)] = item
	}
	day9 := byDate["2026-10-09"]
	if day9["entry_count"].(float64) != 2 || day9["in_month"] != true {
		t.Fatalf("10-09 统计异常: %#v", day9)
	}
	if len(day9["projects"].([]any)) != 2 {
		t.Fatalf("10-09 项目数异常: %#v", day9["projects"])
	}
	if byDate["2026-10-20"]["entry_count"].(float64) != 1 {
		t.Fatalf("10-20 统计异常: %#v", byDate["2026-10-20"])
	}
	// 无日志的日子也要存在且计 0，保证日历可以连续渲染。
	if byDate["2026-10-15"]["entry_count"].(float64) != 0 {
		t.Fatalf("空白天应计 0: %#v", byDate["2026-10-15"])
	}
	// 栅格应含相邻月补位日，并标记为非本月。
	weeks := cal["weeks"].([]any)
	if len(weeks) < 5 {
		t.Fatalf("栅格周数异常: %d", len(weeks))
	}
	firstCell := weeks[0].([]any)[0].(map[string]any)
	if firstCell["in_month"] != false {
		t.Fatalf("栅格首格应为上月补位: %#v", firstCell)
	}

	// 只给 date 时也应能推导出月份。
	byDateCal := call(t, "work-log.archive.calendar", `{"log_root":`+quote(root)+`,"date":"2026-10-09"}`)
	if byDateCal["month_key"] != "2026-10" {
		t.Fatalf("按 date 推导月份异常: %v", byDateCal["month_key"])
	}
}

func TestUpsertFromWorkflowStep(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	// 模拟工作流注入的上下文：上游结构化步骤的输出里有 entries 与 narrative。
	context := `"workflow_context":{"steps":{"WL-STRUCTURE":{"entries":[` +
		`{"source":"codex","session_id":"s9","project":"demo","category":"development","title":"上游结构化"}` +
		`],"narrative":"今天完成了上游管道。"}}}`
	upserted := call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-09","entries_step":"WL-STRUCTURE",`+context+`}`)
	if upserted["count"].(float64) != 1 {
		t.Fatalf("上游条目应写入 1 条，实际 %v", upserted["count"])
	}
	read := call(t, "work-log.entry.read", `{"log_root":`+quote(root)+`,"date":"2026-10-09"}`)
	entries := read["entries"].([]any)
	item := entries[0].(map[string]any)
	if item["title"] != "上游结构化" || item["session_id"] != "s9" {
		t.Fatalf("上游条目字段异常: %#v", item)
	}
	rendered := call(t, "work-log.report.render", `{"log_root":`+quote(root)+`,"period":"daily","date":"2026-10-09","narrative_step":"WL-STRUCTURE",`+context+`}`)
	report := rendered["report"].(map[string]any)
	if report["narrative_used"] != true {
		t.Fatalf("报告应使用上游主线叙述")
	}
	if !contains(report["markdown"].(string), "今天完成了上游管道。") {
		t.Fatalf("报告 Markdown 缺少上游叙述")
	}
}

func TestPeriodRange(t *testing.T) {
	cases := []struct {
		period string
		anchor string
		from   string
		to     string
		key    string
	}{
		{"daily", "2026-10-09", "2026-10-09", "2026-10-09", "2026-10-09"},
		{"weekly", "2026-10-09", "2026-10-05", "2026-10-11", ""},
		{"monthly", "2026-10-09", "2026-10-01", "2026-10-31", "2026-10"},
		{"quarterly", "2026-10-09", "2026-10-01", "2026-12-31", "2026-Q4"},
		{"yearly", "2026-10-09", "2026-01-01", "2026-12-31", "2026"},
	}
	for _, testCase := range cases {
		from, to, key, err := periodRange(testCase.period, testCase.anchor)
		if err != nil {
			t.Fatalf("%s 计算失败: %v", testCase.period, err)
		}
		if from != testCase.from || to != testCase.to {
			t.Fatalf("%s 区间错误: %s~%s", testCase.period, from, to)
		}
		if testCase.key != "" && key != testCase.key {
			t.Fatalf("%s 键错误: %s", testCase.period, key)
		}
	}
}

func countReportFiles(t *testing.T, dir string) int {
	t.Helper()
	names, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	count := 0
	for _, name := range names {
		if strings.HasSuffix(name.Name(), ".md") {
			count++
		}
	}
	return count
}

func TestAggregateCatchUpIsIdempotent(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-05","entries":[{"project":"P","title":"甲"}]}`)
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-08","entries":[{"project":"P","title":"乙"}]}`)
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-12","entries":[{"project":"P","title":"丙"}]}`)

	// 锚点定在 2026-10-20：10-05 与 10-12 两个自然周都已结束，当前周尚未结束。
	params := `{"log_root":` + quote(root) + `,"date":"2026-10-20","periods":["weekly"],"completed_only":true,"lookback":8}`
	first := call(t, "work-log.report.aggregate", params)
	if first["rendered"].(float64) < 2 {
		t.Fatalf("跨期补算应生成至少 2 份周报，实际 %v", first["rendered"])
	}
	weeklyDir := filepath.Join(root, "reports", "weekly")
	if got := countReportFiles(t, weeklyDir); got != 2 {
		t.Fatalf("每个自然周应只有一份周报，实际 %d", got)
	}
	// 尚未结束的当前周不应被汇总。
	_, _, currentKey, err := periodRange("weekly", "2026-10-20")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(weeklyDir, currentKey+".md")); !os.IsNotExist(err) {
		t.Fatalf("尚未结束的当前周不应生成周报")
	}

	// 再跑一次不得重复生成，文件数保持不变。
	second := call(t, "work-log.report.aggregate", params)
	if second["rendered"].(float64) != 0 || second["skipped"].(float64) < 2 {
		t.Fatalf("重复运行应跳过全部，实际 rendered=%v skipped=%v", second["rendered"], second["skipped"])
	}
	if got := countReportFiles(t, weeklyDir); got != 2 {
		t.Fatalf("重复运行后周报数不应变化，实际 %d", got)
	}
}

func TestAggregateManualForceOverwritesSingleRecord(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-14","entries":[{"project":"P","title":"甲"}]}`)

	// 手动模式：锚点所在周即便尚未结束也允许汇总。
	forced := call(t, "work-log.report.aggregate", `{"log_root":`+quote(root)+`,"date":"2026-10-14","periods":["weekly"],"completed_only":false,"force":true}`)
	if forced["rendered"].(float64) != 1 {
		t.Fatalf("手动汇总应生成 1 份，实际 %v", forced["rendered"])
	}
	// 不 force 时命中已有记录应跳过。
	skipped := call(t, "work-log.report.aggregate", `{"log_root":`+quote(root)+`,"date":"2026-10-14","periods":["weekly"],"completed_only":false,"force":false}`)
	if skipped["rendered"].(float64) != 0 || skipped["skipped"].(float64) != 1 {
		t.Fatalf("已有记录应跳过，实际 rendered=%v skipped=%v", skipped["rendered"], skipped["skipped"])
	}
	// force 覆盖重算后仍只有一份记录。
	if again := call(t, "work-log.report.aggregate", `{"log_root":`+quote(root)+`,"date":"2026-10-14","periods":["weekly"],"completed_only":false,"force":true}`); again["rendered"].(float64) != 1 {
		t.Fatalf("覆盖重算应仍生成 1 份，实际 %v", again["rendered"])
	}
	if got := countReportFiles(t, filepath.Join(root, "reports", "weekly")); got != 1 {
		t.Fatalf("同一自然周应始终只有一份记录，实际 %d", got)
	}
}

func TestAggregateDefaultPeriodsWeeklyAndMonthly(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-09-10","entries":[{"project":"P","title":"甲"}]}`)
	result := call(t, "work-log.report.aggregate", `{"log_root":`+quote(root)+`,"date":"2026-11-05"}`)
	periods := result["periods"].([]any)
	if len(periods) != 2 || periods[0] != "weekly" || periods[1] != "monthly" {
		t.Fatalf("默认周期应为 [weekly monthly]，实际 %v", periods)
	}
	if result["rendered"].(float64) < 1 {
		t.Fatalf("应至少汇总 1 份记录，实际 %v", result["rendered"])
	}
}

func TestUpsertRejectsBadDate(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	_, rpcError := handle(jsonrpc.Request{
		Method: "work-log.entry.upsert",
		Params: []byte(`{"log_root":` + quote(root) + `,"date":"2026/10/09","entries":[{"title":"x"}]}`),
	})
	if rpcError == nil {
		t.Fatal("非法日期应当被拒绝")
	}
}

func TestOpenLinkValidation(t *testing.T) {
	if _, rpcError := handle(jsonrpc.Request{Method: "work-log.link.open", Params: []byte(`{"url":""}`)}); rpcError == nil {
		t.Fatal("空 url 应被拒绝")
	}
	if _, rpcError := handle(jsonrpc.Request{Method: "work-log.link.open", Params: []byte(`{"url":"no-scheme"}`)}); rpcError == nil {
		t.Fatal("缺少协议前缀的 url 应被拒绝")
	}
}

func TestProjectRegistryNormalizesAndAggregates(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.project.upsert", `{"log_root":`+quote(root)+`,"project_name":"项目看板","aliases":["project-kanban"]}`)
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-09","entries":[
		{"source":"codex","session_id":"s1","project":"project-kanban","title":"甲"},
		{"source":"codex","session_id":"s2","project":"项目看板","title":"乙"}
	]}`)
	read := call(t, "work-log.entry.read", `{"log_root":`+quote(root)+`,"date":"2026-10-09"}`)
	for _, raw := range read["entries"].([]any) {
		if item := raw.(map[string]any); item["project"] != "项目看板" {
			t.Fatalf("别名应归一为规范项目名，实际 %v", item["project"])
		}
	}
	list := call(t, "work-log.project.list", `{"log_root":`+quote(root)+`}`)
	projects := list["projects"].([]any)
	if len(projects) != 1 {
		t.Fatalf("期望 1 个项目，实际 %d", len(projects))
	}
	if got := projects[0].(map[string]any)["entry_count"].(float64); got != 2 {
		t.Fatalf("项目条目数应为 2，实际 %v", got)
	}
}

func TestProjectAssignReclassifies(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.project.upsert", `{"log_root":`+quote(root)+`,"project_name":"新项目"}`)
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-07","entries":[{"source":"manual","project":"旧项目","title":"X"}]}`)
	assigned := call(t, "work-log.project.assign", `{"log_root":`+quote(root)+`,"from":"2026-10-01","to":"2026-10-31","current_project":"旧项目","project":"新项目"}`)
	if assigned["updated"].(float64) != 1 {
		t.Fatalf("应重归类 1 条，实际 %v", assigned["updated"])
	}
	read := call(t, "work-log.entry.read", `{"log_root":`+quote(root)+`,"date":"2026-10-07"}`)
	if item := read["entries"].([]any)[0].(map[string]any); item["project"] != "新项目" {
		t.Fatalf("重归类后项目应为新项目，实际 %v", item["project"])
	}
}

func TestProjectAutoRegistersUnknownOnWrite(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-06","entries":[{"source":"codex","session_id":"z1","project":"第三方仓库","title":"Z"}]}`)
	list := call(t, "work-log.project.list", `{"log_root":`+quote(root)+`}`)
	if len(list["projects"].([]any)) != 1 {
		t.Fatalf("写入未知项目时应自动登记，实际 %v", list["projects"])
	}
	if _, err := os.Stat(projectsPath(root)); err != nil {
		t.Fatalf("项目登记文件未落盘: %v", err)
	}
}

func TestSourceConfigSaveAndList(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	saved := call(t, "work-log.source.save", `{"log_root":`+quote(root)+`,"sources":[{"id":"myclient","label":"我的客户端","root":"~/.myclient","glob":"**/*.jsonl","format":"jsonl"}]}`)
	if saved["count"].(float64) != 1 {
		t.Fatalf("保存来源数异常: %v", saved["count"])
	}
	list := call(t, "work-log.source.list", `{"log_root":`+quote(root)+`}`)
	if !list["exists"].(bool) {
		t.Fatal("来源配置应已存在")
	}
	if len(list["sources"].([]any)) != 1 {
		t.Fatalf("来源配置读取异常: %v", list["sources"])
	}
	if _, err := os.Stat(sourcesConfigPath(root)); err != nil {
		t.Fatalf("来源配置文件未落盘: %v", err)
	}
}

func TestReportGroupsByProjectAndFeatureWithMinutes(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-09","entries":[
		{"source":"codex","session_id":"s1","project":"项目看板","feature":"日历视图","category":"development","title":"实现月历","summary":"完成月历栅格","minutes":90,"minutes_source":"measured","outcomes":["月历可用"]},
		{"source":"codex","session_id":"s2","project":"项目看板","feature":"项目登记表","category":"development","title":"项目归一","minutes":30,"minutes_source":"estimated"}
	]}`)
	narratives := `,"narratives":{"项目看板":"看板项目整体推进顺利","项目看板/日历视图":"日历视图今天落地"}`
	rendered := call(t, "work-log.report.render", `{"log_root":`+quote(root)+`,"period":"daily","date":"2026-10-09"`+narratives+`}`)
	report := rendered["report"].(map[string]any)
	markdown := report["markdown"].(string)
	for _, want := range []string{
		"## 按项目 · 按功能",
		"### 项目看板（2 条 · 约 2.0 小时）",
		"看板项目整体推进顺利",
		"#### 功能：日历视图（1 条 · 约 1.5 小时）",
		"日历视图今天落地",
		"完成月历",
		"完成：月历可用",
		"记录工时：约 2.0 小时（实测 1.5 小时 / 估算 0.5 小时）",
	} {
		if !contains(markdown, want) {
			t.Fatalf("日报 Markdown 缺少 %q:\n%s", want, markdown)
		}
	}
	if report["total_minutes"].(float64) != 120 {
		t.Fatalf("报告总工时应为 120 分钟，实际 %v", report["total_minutes"])
	}
	if report["narrative_used"] != true {
		t.Fatalf("提供 narratives 时应标记使用了叙述")
	}
}

func TestMergePreservesFeatureAndMinutes(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-09","entries":[
		{"source":"codex","session_id":"m1","project":"P","feature":"F","minutes":45,"minutes_source":"measured","title":"T"}
	]}`)
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-09","entries":[
		{"source":"codex","session_id":"m1","title":"T2"}
	]}`)
	read := call(t, "work-log.entry.read", `{"log_root":`+quote(root)+`,"date":"2026-10-09"}`)
	item := read["entries"].([]any)[0].(map[string]any)
	if item["title"] != "T2" {
		t.Fatalf("合并应更新标题，实际 %v", item["title"])
	}
	if item["feature"] != "F" {
		t.Fatalf("合并不应冲掉 feature，实际 %v", item["feature"])
	}
	if item["minutes"].(float64) != 45 || item["minutes_source"] != "measured" {
		t.Fatalf("合并不应冲掉工时，实际 %v / %v", item["minutes"], item["minutes_source"])
	}
}

func TestCalendarTotalsMinutes(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-09","entries":[
		{"project":"项目看板","title":"A","minutes":60,"minutes_source":"measured"},
		{"project":"项目看板","title":"B","minutes":30,"minutes_source":"estimated"}
	]}`)
	cal := call(t, "work-log.archive.calendar", `{"log_root":`+quote(root)+`,"year":2026,"month":10}`)
	if cal["total_minutes"].(float64) != 90 {
		t.Fatalf("月历总工时应为 90 分钟，实际 %v", cal["total_minutes"])
	}
	for _, raw := range cal["days"].([]any) {
		day := raw.(map[string]any)
		if day["date"] == "2026-10-09" && day["total_minutes"].(float64) != 90 {
			t.Fatalf("当日工时应为 90 分钟，实际 %v", day["total_minutes"])
		}
	}
	// 按项目过滤时工时应同步收窄。
	filtered := call(t, "work-log.archive.calendar", `{"log_root":`+quote(root)+`,"year":2026,"month":10,"project":"项目看板"}`)
	if filtered["total_minutes"].(float64) != 90 {
		t.Fatalf("按项目过滤工时异常: %v", filtered["total_minutes"])
	}
}

func TestTimeSpanFormatsTimestamps(t *testing.T) {
	item := entry{StartedAt: "2026-10-09T09:30:00+08:00", EndedAt: "2026-10-09T11:00:00+08:00"}
	if got := timeSpan(item); got != "09:30–11:00" {
		t.Fatalf("同天的时间戳应渲染为 HH:MM 区间，实际 %q", got)
	}
	if got := minutesText(90); got != "1 小时 30 分" {
		t.Fatalf("工时文本异常: %q", got)
	}
	if got := hoursText(90); got != "1.5 小时" {
		t.Fatalf("小时文本异常: %q", got)
	}
}

func TestDeleteEntryRemovesOneAndTrimsDay(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-09","entries":[
		{"source":"manual","title":"保留"},
		{"source":"manual","title":"删除"}
	]}`)
	read := call(t, "work-log.entry.read", `{"log_root":`+quote(root)+`,"date":"2026-10-09"}`)
	var targetID string
	for _, raw := range read["entries"].([]any) {
		if item := raw.(map[string]any); item["title"] == "删除" {
			targetID = item["entry_id"].(string)
		}
	}
	if targetID == "" {
		t.Fatal("未找到待删除条目")
	}
	deleted := call(t, "work-log.entry.delete", `{"log_root":`+quote(root)+`,"date":"2026-10-09","entry_id":`+quote(targetID)+`}`)
	if deleted["removed"].(float64) != 1 || deleted["count"].(float64) != 1 {
		t.Fatalf("删除结果异常: %#v", deleted)
	}
	after := call(t, "work-log.entry.read", `{"log_root":`+quote(root)+`,"date":"2026-10-09"}`)
	entries := after["entries"].([]any)
	if len(entries) != 1 || entries[0].(map[string]any)["title"] != "保留" {
		t.Fatalf("删除后剩余条目异常: %#v", entries)
	}
}

func TestDeleteLastEntryRemovesDayFile(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-09","entries":[{"source":"manual","title":"唯一"}]}`)
	path := filepath.Join(root, "logs", "2026", "2026-10-09.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("日志文件应存在: %v", err)
	}
	read := call(t, "work-log.entry.read", `{"log_root":`+quote(root)+`,"date":"2026-10-09"}`)
	id := read["entries"].([]any)[0].(map[string]any)["entry_id"].(string)
	deleted := call(t, "work-log.entry.delete", `{"log_root":`+quote(root)+`,"date":"2026-10-09","entry_id":`+quote(id)+`}`)
	if deleted["deleted_day"] != true {
		t.Fatalf("删空后应标记删除当天文件: %#v", deleted)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("删空后当天文件应被移除: %v", err)
	}
	cal := call(t, "work-log.archive.calendar", `{"log_root":`+quote(root)+`,"year":2026,"month":10}`)
	if cal["total_entries"].(float64) != 0 {
		t.Fatalf("删空后月历不应再有条目: %v", cal["total_entries"])
	}
}

func TestDeleteRequiresTarget(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	if _, rpcError := handle(jsonrpc.Request{Method: "work-log.entry.delete", Params: []byte(`{"log_root":` + quote(root) + `,"date":"2026-10-09"}`)}); rpcError == nil {
		t.Fatal("缺少 entry_id 时应被拒绝")
	}
}

func TestUpsertOverwriteClearsFields(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-09","entries":[{"source":"manual","title":"编辑前","decisions":["旧决策"],"outcomes":["旧交付"]}]}`)
	read := call(t, "work-log.entry.read", `{"log_root":`+quote(root)+`,"date":"2026-10-09"}`)
	id := read["entries"].([]any)[0].(map[string]any)["entry_id"].(string)
	// overwrite 下用显式空数组清空 outcomes，只保留 title。
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-09","overwrite":true,"entries":[{"entry_id":`+quote(id)+`,"source":"manual","title":"编辑后","outcomes":[]}]}`)
	after := call(t, "work-log.entry.read", `{"log_root":`+quote(root)+`,"date":"2026-10-09"}`)
	item := after["entries"].([]any)[0].(map[string]any)
	if item["title"] != "编辑后" {
		t.Fatalf("overwrite 应更新标题: %v", item["title"])
	}
	if got := item["outcomes"].([]any); len(got) != 0 {
		t.Fatalf("overwrite 应清空 outcomes，实际 %#v", got)
	}
	if item["created_at"] == "" || item["created_at"] == nil {
		t.Fatalf("overwrite 应保留 created_at")
	}
}

func TestConcurrentUpsertsDoNotLoseEntries(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	const n = 20
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			params := `{"log_root":` + quote(root) + `,"date":"2026-10-09","entries":[{"source":"codex","session_id":"s` + strconv.Itoa(index) + `","title":"T` + strconv.Itoa(index) + `"}]}`
			if _, rpcError := handle(jsonrpc.Request{Method: "work-log.entry.upsert", Params: []byte(params)}); rpcError != nil {
				t.Errorf("并发写入失败: %s", rpcError.Message)
			}
		}(i)
	}
	wg.Wait()
	read := call(t, "work-log.entry.read", `{"log_root":`+quote(root)+`,"date":"2026-10-09"}`)
	if got := len(read["entries"].([]any)); got != n {
		t.Fatalf("并发写入应保留全部 %d 条，实际 %d（存在丢更新）", n, got)
	}
}

func TestUpsertMergesAISummaryOntoDeterministicBase(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	// AI 总结步骤只提供人类可读字段，且故意带上错误工时；base 步骤提供确定性字段。
	params := `{"log_root":` + quote(root) + `,"date":"2026-10-09",
		"entries_step":"WL-SUMMARY","base_step":"WL-COLLECT",
		"workflow_context":{"steps":{
			"WL-SUMMARY":{"entries":[{"source":"codex","session_id":"s1","title":"修复编辑保存报错","summary":"定位到整条克隆回传 created_at 被 schema 拒绝，改为白名单透传。","category":"修复","minutes":9999,"minutes_source":"estimated","outcomes":["编辑保存恢复正常"]}],"narrative":"今天修复了日志编辑保存。"},
			"WL-COLLECT":{"entries":[{"source":"codex","session_id":"s1","project":"项目看板","feature":"工作日志","category":"other","started_at":"2026-10-09T09:00:00+08:00","ended_at":"2026-10-09T09:45:00+08:00","minutes":45,"minutes_source":"measured","title":"raw title","tags":["自动采集"]}]}
		}}}`
	call(t, "work-log.entry.upsert", params)
	read := call(t, "work-log.entry.read", `{"log_root":`+quote(root)+`,"date":"2026-10-09"}`)
	entries := read["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("按会话合并后应只有 1 条，实际 %d", len(entries))
	}
	item := entries[0].(map[string]any)
	if item["title"] != "修复编辑保存报错" {
		t.Fatalf("title 应取 AI 总结，实际 %v", item["title"])
	}
	if item["project"] != "项目看板" || item["feature"] != "工作日志" {
		t.Fatalf("project/feature 应取确定性素材，实际 %v / %v", item["project"], item["feature"])
	}
	if item["category"] != "修复" {
		t.Fatalf("category 应取 AI 总结，实际 %v", item["category"])
	}
	// AI 的 minutes 必须被确定性素材覆盖。
	if item["minutes"].(float64) != 45 {
		t.Fatalf("minutes 应以素材为准(45)，实际 %v", item["minutes"])
	}
	if item["minutes_source"] != "measured" {
		t.Fatalf("minutes_source 应以素材为准，实际 %v", item["minutes_source"])
	}
	tags := toStrings(item["tags"])
	if !contains(strings.Join(tags, ","), "AI总结") {
		t.Fatalf("合并后应带 AI总结 标记，实际 %v", tags)
	}
}

func toStrings(value any) []string {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}

func quote(value string) string {
	body, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(body)
}
