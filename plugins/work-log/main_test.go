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

func TestEntryUpsertDoesNotAutoRegister(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-06","entries":[{"source":"codex","session_id":"z1","project":"第三方仓库","title":"Z"}]}`)
	list := call(t, "work-log.project.list", `{"log_root":`+quote(root)+`}`)
	if len(list["projects"].([]any)) != 0 {
		t.Fatalf("写入不应自动登记未知项目，实际 %v", list["projects"])
	}
	if _, err := os.Stat(projectsPath(root)); err == nil {
		t.Fatal("不应因写入未知项目而落盘项目登记文件")
	}
	read := call(t, "work-log.entry.read", `{"log_root":`+quote(root)+`,"date":"2026-10-06"}`)
	item := read["entries"].([]any)[0].(map[string]any)
	if item["project"] != "第三方仓库" {
		t.Fatalf("未命中项目名应保留用于展示/筛选，实际 %v", item["project"])
	}
	if id, _ := item["project_id"].(string); id != "" {
		t.Fatalf("未登记项目不应带 project_id（待认领），实际 %v", id)
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

// TestUpsertModeReplaceDoesNotWipeDay 验证即便传入 mode=replace，也不会清空当天已有条目
// （该历史开关已移除，防止连带抹掉手工条目）。
func TestUpsertModeReplaceDoesNotWipeDay(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-09","entries":[{"source":"manual","title":"手工补录"}]}`)
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-09","mode":"replace","entries":[{"source":"codex","session_id":"x1","title":"采集条目"}]}`)
	read := call(t, "work-log.entry.read", `{"log_root":`+quote(root)+`,"date":"2026-10-09"}`)
	entries := read["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("mode=replace 不应清空当天条目，应保留 2 条，实际 %d", len(entries))
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

// TestPathMappingResolvesAndMarksPending 验证：路径命中按映射归类；未命中的采集条目
// 留空进「待认领」，且不新增假项目。
func TestPathMappingResolvesAndMarksPending(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.project.upsert", `{"log_root":`+quote(root)+`,"project_name":"深圳科技馆","paths":[{"path":"F:/work/szkjg","kind":"repo"}]}`)
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-09","entries":[
		{"source":"codex","session_id":"a1","repo_path":"F:/work/szkjg/sub/dir","project":"随手起的名字","title":"命中"},
		{"source":"codex","session_id":"a2","repo_path":"F:/work/unknown-repo","project":"unknown-repo","title":"未命中"}
	]}`)
	read := call(t, "work-log.entry.read", `{"log_root":`+quote(root)+`,"date":"2026-10-09"}`)
	got := map[string]map[string]any{}
	for _, raw := range read["entries"].([]any) {
		item := raw.(map[string]any)
		got[item["session_id"].(string)] = item
	}
	if got["a1"]["project"] != "深圳科技馆" {
		t.Fatalf("路径命中应归一为深圳科技馆，实际 %v", got["a1"]["project"])
	}
	if got["a2"]["project"] != "" {
		t.Fatalf("未命中路径应留空，实际 %v", got["a2"]["project"])
	}
	list := call(t, "work-log.project.list", `{"log_root":`+quote(root)+`}`)
	if len(list["projects"].([]any)) != 1 {
		t.Fatalf("未命中不应新增假项目，实际 %d 个", len(list["projects"].([]any)))
	}
	pending := list["pending_paths"].([]any)
	if len(pending) != 1 {
		t.Fatalf("应有 1 个待认领路径，实际 %v", pending)
	}
	if pending[0].(map[string]any)["path"] != "F:/work/unknown-repo" {
		t.Fatalf("待认领路径异常: %v", pending[0])
	}
}

// TestExhibitPathOutranksProjectPath 验证更具体的展项路径优先于项目路径。
func TestExhibitPathOutranksProjectPath(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.project.upsert", `{"log_root":`+quote(root)+`,"project_name":"某馆","paths":[{"path":"F:/work/hall"}]}`)
	call(t, "work-log.project.claim", `{"log_root":`+quote(root)+`,"repo_path":"F:/work/hall/ex1","project_name":"某馆","exhibit_name":"一号展项"}`)
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-08","entries":[{"source":"codex","session_id":"b1","repo_path":"F:/work/hall/ex1/src","title":"展项"}]}`)
	read := call(t, "work-log.entry.read", `{"log_root":`+quote(root)+`,"date":"2026-10-08"}`)
	item := read["entries"].([]any)[0].(map[string]any)
	if item["project"] != "某馆" || item["exhibit"] != "一号展项" {
		t.Fatalf("应命中展项，实际 project=%v exhibit=%v", item["project"], item["exhibit"])
	}
}

// TestClaimBackfillsPendingEntries 验证认领会把历史待认领条目回填到目标项目。
func TestClaimBackfillsPendingEntries(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.project.upsert", `{"log_root":`+quote(root)+`,"project_name":"新馆"}`)
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-05","entries":[{"source":"codex","session_id":"c1","repo_path":"F:/work/mystery","project":"mystery","title":"T"}]}`)
	before := call(t, "work-log.entry.read", `{"log_root":`+quote(root)+`,"date":"2026-10-05"}`)
	if before["entries"].([]any)[0].(map[string]any)["project"] != "" {
		t.Fatal("认领前应留空")
	}
	claimed := call(t, "work-log.project.claim", `{"log_root":`+quote(root)+`,"repo_path":"F:/work/mystery","project_name":"新馆"}`)
	if claimed["updated"].(float64) != 1 {
		t.Fatalf("应回填 1 条，实际 %v", claimed["updated"])
	}
	after := call(t, "work-log.entry.read", `{"log_root":`+quote(root)+`,"date":"2026-10-05"}`)
	if after["entries"].([]any)[0].(map[string]any)["project"] != "新馆" {
		t.Fatal("认领后应归到新馆")
	}
	list := call(t, "work-log.project.list", `{"log_root":`+quote(root)+`}`)
	if len(list["pending_paths"].([]any)) != 0 {
		t.Fatalf("认领后待认领应为空，实际 %v", list["pending_paths"])
	}
}

// TestProjectMergeMovesEntriesAndPaths 验证合并会搬移条目并保留路径映射。
func TestProjectMergeMovesEntriesAndPaths(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.project.upsert", `{"log_root":`+quote(root)+`,"project_name":"目标馆","paths":[{"path":"F:/work/target"}]}`)
	call(t, "work-log.project.upsert", `{"log_root":`+quote(root)+`,"project_name":"旧名","aliases":["legacy"]}`)
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-04","entries":[{"source":"manual","project":"旧名","title":"m"}]}`)
	merged := call(t, "work-log.project.merge", `{"log_root":`+quote(root)+`,"from_project":"旧名","to_project":"目标馆"}`)
	if merged["merged"] != true {
		t.Fatalf("合并失败: %v", merged)
	}
	if merged["updated"].(float64) != 1 {
		t.Fatalf("应改写 1 条，实际 %v", merged["updated"])
	}
	list := call(t, "work-log.project.list", `{"log_root":`+quote(root)+`}`)
	if len(list["projects"].([]any)) != 1 {
		t.Fatalf("合并后应剩 1 个项目，实际 %d", len(list["projects"].([]any)))
	}
	read := call(t, "work-log.entry.read", `{"log_root":`+quote(root)+`,"date":"2026-10-04"}`)
	if read["entries"].([]any)[0].(map[string]any)["project"] != "目标馆" {
		t.Fatal("条目应归到目标馆")
	}
}

// TestProjectMapPathPreview 验证只读路径预览能力。
func TestProjectMapPathPreview(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.project.upsert", `{"log_root":`+quote(root)+`,"project_name":"甲项目"}`)
	call(t, "work-log.project.path.set", `{"log_root":`+quote(root)+`,"project_name":"甲项目","paths":[{"path":"F:/work/jia","kind":"repo"}]}`)
	mapped := call(t, "work-log.project.mapPath", `{"log_root":`+quote(root)+`,"path":"F:/work/jia/app"}`)
	if mapped["matched"] != true || mapped["project"] != "甲项目" || mapped["match_type"] != "project_path" {
		t.Fatalf("路径预览异常: %v", mapped)
	}
	if none := call(t, "work-log.project.mapPath", `{"log_root":`+quote(root)+`,"path":"F:/elsewhere"}`); none["matched"] != false {
		t.Fatalf("不应命中: %v", none)
	}
}

// firstEntryID 读取某天第一条条目的 entry_id。
func firstEntryID(t *testing.T, root, date string) string {
	t.Helper()
	read := call(t, "work-log.entry.read", `{"log_root":`+quote(root)+`,"date":`+quote(date)+`}`)
	items := read["entries"].([]any)
	if len(items) == 0 {
		t.Fatalf("没有条目可读: %s", date)
	}
	return items[0].(map[string]any)["entry_id"].(string)
}

// findProjectByName 在登记表里按名称取一项。
func findProjectByName(t *testing.T, root, name string) map[string]any {
	t.Helper()
	list := call(t, "work-log.project.list", `{"log_root":`+quote(root)+`}`)
	for _, raw := range list["projects"].([]any) {
		p := raw.(map[string]any)
		if p["name"] == name {
			return p
		}
	}
	return nil
}

// TestEntryPatchPreservesUnspecifiedFields 验证局部更新只改传入的键，不注入默认值。
func TestEntryPatchPreservesUnspecifiedFields(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-06","entries":[{"source":"codex","session_id":"p1","project":"甲","category":"开发","title":"原始标题","summary":"原始摘要","minutes":30}]}`)
	id := firstEntryID(t, root, "2026-10-06")
	patched := call(t, "work-log.entry.patch", `{"log_root":`+quote(root)+`,"date":"2026-10-06","entry_id":`+quote(id)+`,"patch":{"feature":"日历视图","project":"乙","minutes":45}}`)
	item := patched["entry"].(map[string]any)
	if item["feature"] != "日历视图" {
		t.Fatalf("feature 未更新: %v", item["feature"])
	}
	if item["project"] != "乙" {
		t.Fatalf("project 未更新: %v", item["project"])
	}
	if item["minutes"].(float64) != 45 {
		t.Fatalf("minutes 未更新: %v", item["minutes"])
	}
	// 未传入的键必须保持原值，尤其不能被默认值（source=manual / category=其他）冲掉。
	if item["source"] != "codex" || item["category"] != "开发" {
		t.Fatalf("未传入的 source/category 被改动: %#v", item)
	}
	if item["summary"] != "原始摘要" || item["title"] != "原始标题" {
		t.Fatalf("未传入的 title/summary 被改动: %#v", item)
	}
	// 显式改了项目名，应把「乙」登记进表并绑定 ID。
	if p := findProjectByName(t, root, "乙"); p == nil {
		t.Fatal("显式设置的项目名应被登记")
	}
	if item["project_id"] == "" {
		t.Fatal("改写项目后应绑定 project_id")
	}
}

// TestProjectDeleteBatch 验证批量删除登记项目。
func TestProjectDeleteBatch(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.project.upsert", `{"log_root":`+quote(root)+`,"project_name":"甲"}`)
	call(t, "work-log.project.upsert", `{"log_root":`+quote(root)+`,"project_name":"乙"}`)
	call(t, "work-log.project.upsert", `{"log_root":`+quote(root)+`,"project_name":"丙"}`)
	pjA := findProjectByName(t, root, "甲")["id"].(string)
	pjB := findProjectByName(t, root, "乙")["id"].(string)
	deleted := call(t, "work-log.project.delete", `{"log_root":`+quote(root)+`,"project_ids":[`+quote(pjA)+`,`+quote(pjB)+`]}`)
	if deleted["count"].(float64) != 2 {
		t.Fatalf("应删除 2 个，实际 %v", deleted["count"])
	}
	list := call(t, "work-log.project.list", `{"log_root":`+quote(root)+`}`)
	if len(list["projects"].([]any)) != 1 || findProjectByName(t, root, "丙") == nil {
		t.Fatalf("应只剩「丙」，实际 %v", list["projects"])
	}
}

// TestProjectMergeMultiSource 验证一次合并多个来源项目。
func TestProjectMergeMultiSource(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.project.upsert", `{"log_root":`+quote(root)+`,"project_name":"目标馆","paths":[{"path":"F:/tgt"}]}`)
	call(t, "work-log.project.upsert", `{"log_root":`+quote(root)+`,"project_name":"甲","paths":[{"path":"F:/jia"}]}`)
	call(t, "work-log.project.upsert", `{"log_root":`+quote(root)+`,"project_name":"乙"}`)
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-03","entries":[{"source":"manual","project":"甲","title":"a"},{"source":"manual","project":"乙","title":"b"}]}`)
	merged := call(t, "work-log.project.merge", `{"log_root":`+quote(root)+`,"from_projects":["甲","乙"],"to_project":"目标馆"}`)
	if merged["merged"] != true || merged["updated"].(float64) != 2 {
		t.Fatalf("多来源合并异常: %v", merged)
	}
	list := call(t, "work-log.project.list", `{"log_root":`+quote(root)+`}`)
	if len(list["projects"].([]any)) != 1 {
		t.Fatalf("应只剩目标项目，实际 %d", len(list["projects"].([]any)))
	}
	target := findProjectByName(t, root, "目标馆")
	paths := target["paths"].([]any)
	if len(paths) != 2 {
		t.Fatalf("来源路径应并入目标，实际 %v", paths)
	}
	read := call(t, "work-log.entry.read", `{"log_root":`+quote(root)+`,"date":"2026-10-03"}`)
	for _, raw := range read["entries"].([]any) {
		if raw.(map[string]any)["project"] != "目标馆" {
			t.Fatalf("条目应全部归到目标馆: %v", raw)
		}
	}
}

// TestProjectExportImport 验证登记表导出后可在另一日志根导入（含路径与别名）。
func TestProjectExportImport(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.project.upsert", `{"log_root":`+quote(root)+`,"project_name":"甲","aliases":["jia"],"paths":[{"path":"F:/jia","kind":"repo"}]}`)
	exported := call(t, "work-log.project.export", `{"log_root":`+quote(root)+`}`)
	body := exported["json"].(string)
	if body == "" || exported["count"].(float64) != 1 {
		t.Fatalf("导出异常: %v", exported)
	}
	root2 := t.TempDir()
	params, _ := json.Marshal(map[string]any{"log_root": root2, "projects_json": body})
	imported := call(t, "work-log.project.import", string(params))
	if imported["created"].(float64) != 1 {
		t.Fatalf("导入应新建 1 个，实际 %v", imported)
	}
	target := findProjectByName(t, root2, "甲")
	if target == nil {
		t.Fatal("导入后应存在「甲」")
	}
	paths := target["paths"].([]any)
	if len(paths) != 1 || paths[0].(map[string]any)["path"] != "F:/jia" {
		t.Fatalf("路径未随导入保留: %v", paths)
	}
}

// TestArchiveMigrateStampsSchema 验证迁移把历史文件补齐到当前 schema，并留备份。
func TestArchiveMigrateStampsSchema(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	dir := filepath.Join(root, "logs", "2026")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 旧格式：没有 schema_version，条目缺 date。
	legacyDay := `{"date":"2026-10-01","entries":[{"source":"manual","title":"旧条目"}]}`
	if err := os.WriteFile(filepath.Join(dir, "2026-10-01.json"), []byte(legacyDay), 0o644); err != nil {
		t.Fatal(err)
	}
	legacyProjects := `{"projects":[{"name":"旧项目","aliases":[],"status":"active"}]}`
	if err := os.WriteFile(filepath.Join(root, "projects.json"), []byte(legacyProjects), 0o644); err != nil {
		t.Fatal(err)
	}
	res := call(t, "work-log.archive.migrate", `{"log_root":`+quote(root)+`}`)
	if res["projects_changed"] != true || res["days_changed"].(float64) != 1 {
		t.Fatalf("迁移应报告改动: %v", res)
	}
	dayBody, _ := os.ReadFile(filepath.Join(dir, "2026-10-01.json"))
	var day map[string]any
	if err := json.Unmarshal(dayBody, &day); err != nil {
		t.Fatal(err)
	}
	if day["schema_version"] != schemaVersion {
		t.Fatalf("日文件 schema 未补齐: %v", day["schema_version"])
	}
	item := day["entries"].([]any)[0].(map[string]any)
	if item["date"] != "2026-10-01" {
		t.Fatalf("条目 date 未补齐: %v", item["date"])
	}
	projBody, _ := os.ReadFile(filepath.Join(root, "projects.json"))
	var pf map[string]any
	if err := json.Unmarshal(projBody, &pf); err != nil {
		t.Fatal(err)
	}
	if pf["projects"].([]any)[0].(map[string]any)["id"] == "" {
		t.Fatal("项目 ID 未补齐")
	}
	if _, err := os.Stat(filepath.Join(dir, "2026-10-01.json.bak")); err != nil {
		t.Fatalf("写前应生成 .bak 备份: %v", err)
	}
}

// TestProjectRenameRebindsEntries 验证项目改名：保持稳定 ID，并把历史条目回绑到新名。
func TestProjectRenameRebindsEntries(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.project.upsert", `{"log_root":`+quote(root)+`,"project_name":"甲"}`)
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-02","entries":[{"source":"manual","project":"甲","title":"a"},{"source":"manual","project":"甲","title":"b"}]}`)
	before := findProjectByName(t, root, "甲")
	id := before["id"].(string)

	renamed := call(t, "work-log.project.rename", `{"log_root":`+quote(root)+`,"project_id":`+quote(id)+`,"new_name":"甲馆"}`)
	if renamed["renamed"] != true || renamed["updated"].(float64) != 2 {
		t.Fatalf("改名应回绑 2 条: %v", renamed)
	}
	if findProjectByName(t, root, "甲") != nil {
		t.Fatal("旧名不应再存在")
	}
	after := findProjectByName(t, root, "甲馆")
	if after == nil || after["id"].(string) != id {
		t.Fatalf("改名应保持稳定 ID，实际 %v", after)
	}
	read := call(t, "work-log.entry.read", `{"log_root":`+quote(root)+`,"date":"2026-10-02"}`)
	for _, raw := range read["entries"].([]any) {
		item := raw.(map[string]any)
		if item["project"] != "甲馆" || item["project_id"] != id {
			t.Fatalf("条目未回绑到新名: %v", item)
		}
	}

	// 新名被其它项目占用时应报错，而不是静默覆盖。
	call(t, "work-log.project.upsert", `{"log_root":`+quote(root)+`,"project_name":"乙"}`)
	_, rpcError := handle(jsonrpc.Request{Method: "work-log.project.rename", Params: []byte(`{"log_root":` + quote(root) + `,"project_id":` + quote(id) + `,"new_name":"乙"}`)})
	if rpcError == nil {
		t.Fatal("名称冲突时应报错")
	}
}

// TestProjectAutofillFromSessionMaterial 验证从会话素材搜一轮自动补全项目路径，且 dry_run 不写盘。
func TestProjectAutofillFromSessionMaterial(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.project.upsert", `{"log_root":`+quote(root)+`,"project_name":"甲馆"}`)
	call(t, "work-log.project.upsert", `{"log_root":`+quote(root)+`,"project_name":"乙馆","paths":[{"path":"F:/keep"}]}`)

	material := t.TempDir()
	digest := `{"sessions":[` +
		`{"project":"甲馆","directory":"F:/work/jiaguan"},` +
		`{"project":"甲馆","repo_path":"F:/work/jiaguan"},` +
		`{"project":"乙馆","directory":"F:/work/yiguan"},` +
		`{"project":"孤儿","directory":"F:/work/orphan"}` +
		`]}`
	if err := os.WriteFile(filepath.Join(material, "session-digest-2026-10-02_2026-10-02.json"), []byte(digest), 0o644); err != nil {
		t.Fatal(err)
	}

	// dry_run：只预览，不写盘。
	preview := call(t, "work-log.project.autofill", `{"log_root":`+quote(root)+`,"material_root":`+quote(material)+`,"dry_run":true}`)
	if preview["filled_count"].(float64) != 1 {
		t.Fatalf("dry_run 应预览 1 个待补项目: %v", preview)
	}
	if len(findProjectByName(t, root, "甲馆")["paths"].([]any)) != 0 {
		t.Fatal("dry_run 不应真正写入路径")
	}

	applied := call(t, "work-log.project.autofill", `{"log_root":`+quote(root)+`,"material_root":`+quote(material)+`}`)
	if applied["filled_count"].(float64) != 1 {
		t.Fatalf("应补 1 个项目路径: %v", applied)
	}
	paths := findProjectByName(t, root, "甲馆")["paths"].([]any)
	if len(paths) != 1 || paths[0].(map[string]any)["path"] != "F:/work/jiaguan" {
		t.Fatalf("路径未自动补全: %v", paths)
	}
	if paths[0].(map[string]any)["source"] != "session" {
		t.Fatalf("自动补全来源应为 session: %v", paths[0])
	}
	// 已有路径的项目不被覆盖。
	kept := findProjectByName(t, root, "乙馆")["paths"].([]any)
	if len(kept) != 1 || kept[0].(map[string]any)["path"] != "F:/keep" {
		t.Fatalf("已有路径不应被覆盖: %v", kept)
	}
}

// TestSessionMaterialDirSiblingLayout 验证未显式给 material_root 时，能从采集器（同级插件）
// 的私有数据目录找到素材：HIMIND_PLUGIN_DATA_ROOT 指向本插件目录，素材在同级目录。
func TestSessionMaterialDirSiblingLayout(t *testing.T) {
	pluginData := t.TempDir()
	own := filepath.Join(pluginData, "com.himind.work-log")
	if err := os.MkdirAll(own, 0o755); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(pluginData, "com.himind.work-log-collector", "material")
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", own)

	got, err := sessionMaterialDir("")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(got) != filepath.Clean(sibling) {
		t.Fatalf("应解析到同级采集器素材目录 %q，实际 %q", sibling, got)
	}
}

// TestProjectAutofillViaSiblingMaterialDir 走真实环境变量路径（不给 material_root）时，
// autofill 应能读到同级采集器素材并补全路径。
func TestProjectAutofillViaSiblingMaterialDir(t *testing.T) {
	pluginData := t.TempDir()
	own := filepath.Join(pluginData, "com.himind.work-log")
	if err := os.MkdirAll(own, 0o755); err != nil {
		t.Fatal(err)
	}
	material := filepath.Join(pluginData, "com.himind.work-log-collector", "material")
	if err := os.MkdirAll(material, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", own)

	root := t.TempDir()
	call(t, "work-log.project.upsert", `{"log_root":`+quote(root)+`,"project_name":"甲馆"}`)
	digest := `{"sessions":[{"project":"甲馆","directory":"F:/work/jiaguan"}]}`
	if err := os.WriteFile(filepath.Join(material, "session-digest-2026-10-02_2026-10-02.json"), []byte(digest), 0o644); err != nil {
		t.Fatal(err)
	}

	applied := call(t, "work-log.project.autofill", `{"log_root":`+quote(root)+`}`)
	if applied["found_material"] != true {
		t.Fatalf("应找到同级采集器素材: %v", applied)
	}
	if applied["filled_count"].(float64) != 1 {
		t.Fatalf("应补 1 个项目路径: %v", applied)
	}
}

// TestEphemeralProjectPath 验证临时/缓存/草稿路径被识别，真实仓库不被误判。
func TestEphemeralProjectPath(t *testing.T) {
	ephemeral := []string{
		filepath.Join(os.TempDir(), "himind-runtime-1", "workspace"),
		`C:\Users\me\AppData\Local\Temp\himind-acp-real-deadbeef\workspace`,
		`C:\Users\me\.copilot\repos\copilot-worktrees\AndX\mrbaoquan-wt`,
		`C:\Users\me\.copilot\chats\2026-09-20\fuzzy-adventure`,
		`C:\Users\me\Documents\Codex\2026-09-29\x-decrypted-3`,
	}
	for _, p := range ephemeral {
		if !ephemeralProjectPath(p) {
			t.Fatalf("应判为临时/缓存路径: %q", p)
		}
	}
	real := []string{
		`F:\WebProjects\AndX`,
		`O:\DevModules\IODevice`,
		`C:\Users\me\.copilot\repos\HiMind-Agent`,
	}
	for _, p := range real {
		if ephemeralProjectPath(p) {
			t.Fatalf("真实项目路径不应判为临时: %q", p)
		}
	}
}

// TestWorktreeMainRoot 验证从链接工作树（.git 为文件）解析到主仓库工作树根。
func TestWorktreeMainRoot(t *testing.T) {
	home := t.TempDir()
	mainRepo := filepath.Join(home, "WebProjects", "AndX")
	if err := os.MkdirAll(filepath.Join(mainRepo, ".git", "worktrees", "wt-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(home, "cache", "copilot-worktrees", "AndX", "wt-1")
	if err := os.MkdirAll(filepath.Join(wt, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitdir := "gitdir: " + filepath.ToSlash(filepath.Join(mainRepo, ".git", "worktrees", "wt-1"))
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte(gitdir), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := worktreeMainRoot(filepath.Join(wt, "pkg")); got != mainRepo {
		t.Fatalf("应解析到主仓库工作树 %q，实际 %q", mainRepo, got)
	}
	// 普通仓库（.git 是目录）不解析。
	if got := worktreeMainRoot(mainRepo); got != "" {
		t.Fatalf("普通仓库不应解析出主仓库根，实际 %q", got)
	}
}

// TestAutofillSkipsCacheKeepsReal 验证同一项目同时有缓存工作树与真实路径时，只绑真实路径。
func TestAutofillSkipsCacheKeepsReal(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.project.upsert", `{"log_root":`+quote(root)+`,"project_name":"甲馆"}`)
	material := t.TempDir()
	digest := `{"sessions":[` +
		`{"project":"甲馆","directory":"C:/Users/me/.copilot/repos/copilot-worktrees/jiaguan/mrbaoquan-very-long-name"},` +
		`{"project":"甲馆","directory":"F:/WebProjects/jiaguan"}` +
		`]}`
	if err := os.WriteFile(filepath.Join(material, "session-digest-2026-10-03_2026-10-03.json"), []byte(digest), 0o644); err != nil {
		t.Fatal(err)
	}
	applied := call(t, "work-log.project.autofill", `{"log_root":`+quote(root)+`,"material_root":`+quote(material)+`}`)
	if applied["filled_count"].(float64) != 1 {
		t.Fatalf("应补 1 个路径: %v", applied)
	}
	filled := applied["filled"].([]any)
	if got := filled[0].(map[string]any)["path"]; got != "F:/WebProjects/jiaguan" {
		t.Fatalf("应绑真实仓库路径而非缓存工作树，实际 %v", got)
	}
	paths := findProjectByName(t, root, "甲馆")["paths"].([]any)
	if len(paths) != 1 || paths[0].(map[string]any)["path"] != "F:/WebProjects/jiaguan" {
		t.Fatalf("登记表里也应只有真实路径: %v", paths)
	}
}

// TestAutofillOnlyCacheLeavesUnbound 验证只有缓存路径的项目不被绑定，并出现在忽略清单里。
func TestAutofillOnlyCacheLeavesUnbound(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.project.upsert", `{"log_root":`+quote(root)+`,"project_name":"只有缓存"}`)
	material := t.TempDir()
	digest := `{"sessions":[{"project":"只有缓存","directory":"C:/Users/me/AppData/Local/Temp/himind-acp-real-1/workspace"}]}`
	if err := os.WriteFile(filepath.Join(material, "session-digest-2026-10-04_2026-10-04.json"), []byte(digest), 0o644); err != nil {
		t.Fatal(err)
	}
	applied := call(t, "work-log.project.autofill", `{"log_root":`+quote(root)+`,"material_root":`+quote(material)+`}`)
	if applied["filled_count"].(float64) != 0 {
		t.Fatalf("只有缓存路径时不应绑定: %v", applied)
	}
	if applied["ignored_count"].(float64) < 1 {
		t.Fatalf("应报告被忽略的缓存路径: %v", applied)
	}
	if len(findProjectByName(t, root, "只有缓存")["paths"].([]any)) != 0 {
		t.Fatalf("不应写入任何路径")
	}
}

// TestPreferCandidatePath 验证候选择优：仓库根优先于普通目录，否则取更短路径。
func TestPreferCandidatePath(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(home, "some", "deeper", "plain")
	if !preferCandidatePath(repo, plain) {
		t.Fatal("仓库根应优先于普通目录（即使更短）")
	}
	if !preferCandidatePath(filepath.Join(home, "a"), filepath.Join(home, "a", "b", "c")) {
		t.Fatal("都不是仓库时应取更短路径")
	}
}

// TestProjectSyncEnrichesOnlyLocalAssociations 验证：工作台同步只增补本地已有关联的项目/展项，
// 不对整个工作台目录做批量导入；本地未实际用到的项目/展项一律跳过。
func TestProjectSyncEnrichesOnlyLocalAssociations(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()

	// 先显式建立本地关联（写入不再自动登记项目/展项，登记只由人工/能力显式完成）。
	seed := `{"projects":[{"name":"邯郸馆","exhibits":[{"name":"诗海词趣","id":"exh_local"}]}]}`
	call(t, "work-log.project.import", `{"log_root":`+quote(root)+`,"projects_json":`+quote(seed)+`}`)
	// 本地只实际用到一个项目，且条目带了一个展项。
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-09","entries":[
		{"source":"codex","session_id":"a1","project":"邯郸馆","exhibit":"诗海词趣","title":"做展项"}
	]}`)
	list := call(t, "work-log.project.list", `{"log_root":`+quote(root)+`}`)
	projects := list["projects"].([]any)
	if len(projects) != 1 {
		t.Fatalf("本地应只有 1 个项目，实际 %d", len(projects))
	}
	if ex := projects[0].(map[string]any)["exhibits"].([]any); len(ex) != 1 {
		t.Fatalf("已登记的展项应保留，实际 %d 个", len(ex))
	}

	// 工作台带来 2 个项目、3 个展项，其中只有一个项目/展项是本地真正用过的。
	syncParams := `{"log_root":` + quote(root) + `,"projects":[
		{"project_name":"邯郸馆","id":"prj_handan"},
		{"project_name":"从未用过的馆","id":"prj_unused"}
	],"exhibits":[
		{"exhibit_name":"诗海词趣","pid":"exh_1","project_id":"prj_handan"},
		{"exhibit_name":"没用过的展项","pid":"exh_2","project_id":"prj_handan"},
		{"exhibit_name":"另一个馆的展项","pid":"exh_3","project_id":"prj_unused"}
	]}`
	res := call(t, "work-log.project.sync", syncParams)
	if res["created"].(float64) != 0 {
		t.Fatalf("同步不应新建项目，实际 created=%v", res["created"])
	}
	if res["skipped"].(float64) == 0 {
		t.Fatalf("同步应跳过无本地关联项，实际 skipped=%v", res["skipped"])
	}

	after := call(t, "work-log.project.list", `{"log_root":`+quote(root)+`}`)
	aps := after["projects"].([]any)
	if len(aps) != 1 {
		t.Fatalf("同步后本地仍应只有 1 个项目，实际 %d", len(aps))
	}
	p := aps[0].(map[string]any)
	if p["external_id"] != "prj_handan" {
		t.Fatalf("已有关联项目应补上 external_id，实际 %v", p["external_id"])
	}
	ex := p["exhibits"].([]any)
	if len(ex) != 1 {
		t.Fatalf("未使用的展项不应被引入，实际 %d 个", len(ex))
	}
	if ex[0].(map[string]any)["external_id"] != "exh_1" {
		t.Fatalf("已有关联展项应补上 external_id，实际 %v", ex[0].(map[string]any)["external_id"])
	}
}

// writeDayFixture 直接写一个日文件，解耦去重测试与写入幂等行为。
func writeDayFixture(t *testing.T, root, date string, entries []map[string]any) {
	t.Helper()
	payload := map[string]any{"date": date, "schema_version": schemaVersion, "entries": entries, "count": len(entries)}
	body, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "logs", yearOfDate(date))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, date+".json"), append(body, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDedupeKeepsCrossDayByDefault 验证默认语义：同一会话跨天出现是采集器按活跃日
// 归集出的正常记录，默认不该被合并。
func TestDedupeKeepsCrossDayByDefault(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	writeDayFixture(t, root, "2026-06-18", []map[string]any{{
		"entry_id": "wl-a", "date": "2026-06-18", "source": "codex", "session_id": "s1",
		"project": "项目看板", "category": "开发", "title": "实现登录", "summary": "实现了登录",
		"outcomes": []string{"完成登录"},
	}})
	writeDayFixture(t, root, "2026-06-19", []map[string]any{{
		"entry_id": "wl-b", "date": "2026-06-19", "source": "codex", "session_id": "s1",
		"project": "项目看板", "category": "开发", "title": "继续登录", "summary": "实现登录并补充测试",
		"outcomes": []string{"补充测试"},
	}})

	preview := call(t, "work-log.entry.dedupe", `{"log_root":`+quote(root)+`}`)
	if preview["dry_run"] != true {
		t.Fatalf("默认应是预览: %v", preview["dry_run"])
	}
	if preview["cross_day"] != false {
		t.Fatalf("默认不应跨天合并: %v", preview["cross_day"])
	}
	if preview["groups"].(float64) != 0 || preview["entries_removed"].(float64) != 0 {
		t.Fatalf("跨天同会话默认不该被合并，实际: %#v", preview)
	}

	apply := call(t, "work-log.entry.dedupe", `{"log_root":`+quote(root)+`,"dry_run":false}`)
	if apply["days_changed"].(float64) != 0 {
		t.Fatalf("默认不应改写任何天，实际 %v", apply["days_changed"])
	}
	d18 := call(t, "work-log.entry.read", `{"log_root":`+quote(root)+`,"date":"2026-06-18"}`)
	d19 := call(t, "work-log.entry.read", `{"log_root":`+quote(root)+`,"date":"2026-06-19"}`)
	if len(d18["entries"].([]any)) != 1 || len(d19["entries"].([]any)) != 1 {
		t.Fatal("跨天记录应原样保留")
	}
}

// TestDedupeMergesSameDayDuplicates 验证默认语义仍会合并「同一天+同一会话」的真正重复条目。
func TestDedupeMergesSameDayDuplicates(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	writeDayFixture(t, root, "2026-06-18", []map[string]any{
		{
			"entry_id": "wl-x", "date": "2026-06-18", "source": "codex", "session_id": "s2",
			"project": "项目看板", "category": "开发", "title": "实现登录", "summary": "实现了登录",
			"outcomes": []string{"完成登录"},
		},
		{
			"entry_id": "wl-y", "date": "2026-06-18", "source": "codex", "session_id": "s2",
			"project": "项目看板", "category": "开发", "title": "实现登录", "summary": "实现登录并补充测试",
			"outcomes": []string{"补充测试"}, "next": []string{"补文档"},
		},
	})

	preview := call(t, "work-log.entry.dedupe", `{"log_root":`+quote(root)+`}`)
	if preview["groups"].(float64) != 1 || preview["entries_removed"].(float64) != 1 {
		t.Fatalf("应识别同天 1 组、移除 1 条: %#v", preview)
	}

	apply := call(t, "work-log.entry.dedupe", `{"log_root":`+quote(root)+`,"dry_run":false}`)
	if apply["backup_dir"] == "" {
		t.Fatal("改写应生成备份目录")
	}
	d18 := call(t, "work-log.entry.read", `{"log_root":`+quote(root)+`,"date":"2026-06-18"}`)
	entries := d18["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("合并后应只剩 1 条，实际 %d", len(entries))
	}
	item := entries[0].(map[string]any)
	if got := toStrings(item["outcomes"]); len(got) != 2 {
		t.Fatalf("outcomes 应取并集，实际 %#v", got)
	}
	if got := toStrings(item["next"]); len(got) != 1 || got[0] != "补文档" {
		t.Fatalf("next 应保留，实际 %#v", got)
	}
	if item["summary"] != "实现登录并补充测试" {
		t.Fatalf("summary 应取更长的一条，实际 %v", item["summary"])
	}
}

// TestDedupeCrossDayOptIn 验证显式 cross_day=true 才退回旧的跨天合并行为（合并到最早日期）。
func TestDedupeCrossDayOptIn(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	writeDayFixture(t, root, "2026-06-18", []map[string]any{{
		"entry_id": "wl-a", "date": "2026-06-18", "source": "codex", "session_id": "s1",
		"project": "项目看板", "category": "开发", "title": "实现登录", "summary": "实现了登录",
		"outcomes": []string{"完成登录"},
	}})
	writeDayFixture(t, root, "2026-06-19", []map[string]any{{
		"entry_id": "wl-b", "date": "2026-06-19", "source": "codex", "session_id": "s1",
		"project": "项目看板", "category": "开发", "title": "实现登录", "summary": "实现登录并补充测试",
		"outcomes": []string{"补充测试"}, "next": []string{"补文档"},
	}})

	preview := call(t, "work-log.entry.dedupe", `{"log_root":`+quote(root)+`,"cross_day":true}`)
	if preview["cross_day"] != true {
		t.Fatalf("应识别跨天开关: %v", preview["cross_day"])
	}
	if preview["groups"].(float64) != 1 || preview["entries_removed"].(float64) != 1 {
		t.Fatalf("应识别 1 组、移除 1 条: %#v", preview)
	}

	apply := call(t, "work-log.entry.dedupe", `{"log_root":`+quote(root)+`,"cross_day":true,"dry_run":false}`)
	if apply["days_changed"].(float64) != 2 {
		t.Fatalf("应改写 2 天，实际 %v", apply["days_changed"])
	}
	d18 := call(t, "work-log.entry.read", `{"log_root":`+quote(root)+`,"date":"2026-06-18"}`)
	d19 := call(t, "work-log.entry.read", `{"log_root":`+quote(root)+`,"date":"2026-06-19"}`)
	e18 := d18["entries"].([]any)
	e19 := d19["entries"].([]any)
	if len(e18) != 1 || len(e19) != 0 {
		t.Fatalf("合并后应只剩最早日期 1 条，实际 d18=%d d19=%d", len(e18), len(e19))
	}
	item := e18[0].(map[string]any)
	if item["date"] != "2026-06-18" {
		t.Fatalf("合并条目应落在最早日期，实际 %v", item["date"])
	}
	if got := toStrings(item["outcomes"]); len(got) != 2 {
		t.Fatalf("outcomes 应取并集，实际 %#v", got)
	}
}

// TestEntryLockPreventsOverwrite 验证手工锁定条目不会被自动写入改写，只有显式 overwrite 才能覆盖。
func TestEntryLockPreventsOverwrite(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-05","entries":[{"source":"codex","session_id":"lk1","project":"P","title":"原始","summary":"原始摘要"}]}`)
	read := call(t, "work-log.entry.read", `{"log_root":`+quote(root)+`,"date":"2026-10-05"}`)
	id := read["entries"].([]any)[0].(map[string]any)["entry_id"].(string)
	locked := call(t, "work-log.entry.lock", `{"log_root":`+quote(root)+`,"date":"2026-10-05","entry_id":`+quote(id)+`,"locked":true}`)
	if locked["updated"].(float64) != 1 || locked["locked"] != true {
		t.Fatalf("锁定结果异常: %v", locked)
	}
	res := call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-05","entries":[{"source":"codex","session_id":"lk1","title":"被改标题"}]}`)
	if res["locked_skipped"].(float64) != 1 {
		t.Fatalf("锁定条目应被跳过，实际 %v", res["locked_skipped"])
	}
	after := call(t, "work-log.entry.read", `{"log_root":`+quote(root)+`,"date":"2026-10-05"}`)
	if got := after["entries"].([]any)[0].(map[string]any)["title"]; got != "原始" {
		t.Fatalf("锁定条目不应被覆盖，实际标题 %v", got)
	}
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-05","overwrite":true,"entries":[{"source":"codex","session_id":"lk1","title":"显式覆盖"}]}`)
	ov := call(t, "work-log.entry.read", `{"log_root":`+quote(root)+`,"date":"2026-10-05"}`)
	if got := ov["entries"].([]any)[0].(map[string]any)["title"]; got != "显式覆盖" {
		t.Fatalf("overwrite=true 应能覆盖锁定条目，实际 %v", got)
	}
}

// TestEntryExportImportRoundTrip 验证导出/导入可往返迁移，且重复导入幂等。
func TestEntryExportImportRoundTrip(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-04","entries":[{"source":"codex","session_id":"e1","project":"项目看板","title":"甲","minutes":30,"minutes_source":"measured"}]}`)
	exported := call(t, "work-log.entry.export", `{"log_root":`+quote(root)+`}`)
	if exported["count"].(float64) != 1 {
		t.Fatalf("导出条目数异常: %v", exported["count"])
	}
	js := exported["json"].(string)
	root2 := t.TempDir()
	imp := call(t, "work-log.entry.import", `{"log_root":`+quote(root2)+`,"entries_json":`+quote(js)+`}`)
	if imp["added"].(float64) != 1 {
		t.Fatalf("导入应新增 1 条，实际 %v", imp["added"])
	}
	read := call(t, "work-log.entry.read", `{"log_root":`+quote(root2)+`,"date":"2026-10-04"}`)
	item := read["entries"].([]any)[0].(map[string]any)
	if item["title"] != "甲" || item["project"] != "项目看板" {
		t.Fatalf("导入条目内容不一致: %v", item)
	}
	imp2 := call(t, "work-log.entry.import", `{"log_root":`+quote(root2)+`,"entries_json":`+quote(js)+`}`)
	if imp2["added"].(float64) != 0 || imp2["updated"].(float64) != 1 {
		t.Fatalf("重复导入应合并而非重复，实际 added=%v updated=%v", imp2["added"], imp2["updated"])
	}
}

// TestArchiveBackupAndRestore 验证整库备份后可从备份包一键恢复。
func TestArchiveBackupAndRestore(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-03","entries":[{"source":"codex","session_id":"b1","project":"P","title":"备份前"}]}`)
	bk := call(t, "work-log.archive.backup", `{"log_root":`+quote(root)+`}`)
	path := bk["path"].(string)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("备份包未生成: %v", err)
	}
	if bk["files"].(float64) < 1 {
		t.Fatalf("备份应包含文件，实际 %v", bk["files"])
	}
	read0 := call(t, "work-log.entry.read", `{"log_root":`+quote(root)+`,"date":"2026-10-03"}`)
	delID := read0["entries"].([]any)[0].(map[string]any)["entry_id"].(string)
	call(t, "work-log.entry.delete", `{"log_root":`+quote(root)+`,"date":"2026-10-03","entry_id":`+quote(delID)+`}`)
	if _, err := os.Stat(filepath.Join(root, "logs", "2026", "2026-10-03.json")); err == nil {
		t.Fatal("删除后当日文件应不存在")
	}
	restored := call(t, "work-log.archive.restore", `{"log_root":`+quote(root)+`,"backup_path":`+quote(path)+`}`)
	if restored["files"].(float64) < 1 {
		t.Fatalf("恢复文件数异常: %v", restored["files"])
	}
	read := call(t, "work-log.entry.read", `{"log_root":`+quote(root)+`,"date":"2026-10-03"}`)
	if len(read["entries"].([]any)) != 1 {
		t.Fatalf("恢复后应回到 1 条，实际 %v", read["entries"])
	}
}

// TestEntryReconcileDetectsMissing 验证重扫对账能发现「有语料但未入账」的会话。
func TestEntryReconcileDetectsMissing(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	material := t.TempDir()
	materialDoc := `{"sessions":[
		{"source":"codex","session_id":"s1","date":"2026-10-02","repo_path":"F:/WebProjects/项目看板","title":"有会话有日志","minutes":10},
		{"source":"codex","session_id":"s2","date":"2026-10-02","repo_path":"F:/WebProjects/项目看板","title":"有会话无日志","minutes":20}
	]}`
	if err := os.WriteFile(filepath.Join(material, "session-digest-2026-10-02_2026-10-02.json"), []byte(materialDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-02","entries":[{"source":"codex","session_id":"s1","project":"项目看板","title":"有会话有日志"}]}`)
	rec := call(t, "work-log.entry.reconcile", `{"log_root":`+quote(root)+`,"date":"2026-10-02","material_root":`+quote(material)+`}`)
	if rec["missing_count"].(float64) != 1 {
		t.Fatalf("应发现 1 条未入账会话，实际 %v", rec["missing_count"])
	}
	if rec["matched"].(float64) != 1 {
		t.Fatalf("应匹配 1 条，实际 %v", rec["matched"])
	}
	missing := rec["missing"].([]any)
	if missing[0].(map[string]any)["session_id"] != "s2" {
		t.Fatalf("未入账会话应为 s2，实际 %v", missing[0])
	}
}
