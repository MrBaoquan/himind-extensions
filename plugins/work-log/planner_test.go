package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MrBaoquan/himind-extensions/sdk/jsonrpc"
)

func callError(t *testing.T, method string, params string) string {
	t.Helper()
	_, rpcError := handle(jsonrpc.Request{Method: method, Params: []byte(params)})
	if rpcError == nil {
		t.Fatalf("%s 期望返回错误，实际成功", method)
	}
	return rpcError.Message
}

func boardProject(t *testing.T, board map[string]any, name string) map[string]any {
	t.Helper()
	projects, _ := board["projects"].([]any)
	for _, raw := range projects {
		item, _ := raw.(map[string]any)
		if item["name"] == name {
			return item
		}
	}
	t.Fatalf("工作台中未找到项目 %s", name)
	return nil
}

func TestProjectBoardAndWorkUpdate(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-19","entries":[{"project":"当前工作台","category":"开发","title":"调研项目状态","minutes":60,"next":["实现看板"]}]}`)

	board := call(t, "work-log.project.board", `{"log_root":`+quote(root)+`}`)
	project := boardProject(t, board, "当前工作台")
	if project["state"] != workStateInProgress {
		t.Fatalf("有日志的项目默认状态应为 in_progress，实际 %v", project["state"])
	}
	if project["priority"] != priorityP2 {
		t.Fatalf("默认优先级应为 P2，实际 %v", project["priority"])
	}

	updated := call(t, "work-log.project.work.update", `{"log_root":`+quote(root)+`,"project_name":"当前工作台","patch":{"state":"blocked","priority":"P0","goal":"完成第一版","target_date":"2026-11-15","blockers":["等待设计确认"],"next_steps":[{"text":"设计数据结构"},{"text":"实现接口"}]}}`)
	work := updated["work"].(map[string]any)
	if work["state"] != workStateBlocked || work["priority"] != priorityP0 {
		t.Fatalf("工作状态未更新: %+v", work)
	}
	steps := work["next_steps"].([]any)
	if len(steps) != 2 {
		t.Fatalf("下一步数量异常: %d", len(steps))
	}
	if steps[0].(map[string]any)["id"] == "" {
		t.Fatalf("下一步应自动生成稳定 ID: %+v", steps[0])
	}

	done := call(t, "work-log.project.work.update", `{"log_root":`+quote(root)+`,"project_name":"当前工作台","patch":{"state":"done","completion_summary":"第一版完成"}}`)
	doneWork := done["work"].(map[string]any)
	if doneWork["completed_at"] == "" {
		t.Fatalf("完成项目应写入 completed_at: %+v", doneWork)
	}
	if doneWork["progress"].(float64) != 100 {
		t.Fatalf("完成项目进度应为 100，实际 %v", doneWork["progress"])
	}
}

func TestWorkItemsCRUD(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.project.upsert", `{"log_root":`+quote(root)+`,"project_name":"项目 A"}`)

	created := call(t, "work-log.item.upsert", `{"log_root":`+quote(root)+`,"work_item":{"project":"项目 A","title":"实现登录","estimate_minutes":90,"remaining_minutes":90,"priority":"P1","due_date":"2026-10-24"}}`)
	item := created["item"].(map[string]any)
	id := item["id"].(string)
	if id == "" || item["remaining_minutes"].(float64) != 90 {
		t.Fatalf("工作项创建异常: %+v", item)
	}

	open := call(t, "work-log.item.list", `{"log_root":`+quote(root)+`,"include_done":false}`)
	if open["count"].(float64) != 1 {
		t.Fatalf("未完成工作项数量异常: %v", open["count"])
	}

	done := call(t, "work-log.item.update", `{"log_root":`+quote(root)+`,"work_item_id":`+quote(id)+`,"patch":{"status":"done"}}`)
	doneItem := done["item"].(map[string]any)
	if doneItem["completed_at"] == "" {
		t.Fatalf("完成工作项应写入 completed_at: %+v", doneItem)
	}
	if remaining, ok := doneItem["remaining_minutes"].(float64); ok && remaining != 0 {
		t.Fatalf("完成工作项剩余耗时应为 0: %+v", doneItem)
	}

	open = call(t, "work-log.item.list", `{"log_root":`+quote(root)+`,"include_done":false}`)
	if open["count"].(float64) != 0 {
		t.Fatalf("完成后不应出现在未完成列表: %v", open["count"])
	}
}

func TestPlanSaveGetAndValidate(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.project.upsert", `{"log_root":`+quote(root)+`,"project_name":"项目 A"}`)
	created := call(t, "work-log.item.upsert", `{"log_root":`+quote(root)+`,"work_item":{"project":"项目 A","title":"实现登录","estimate_minutes":90}}`)
	itemID := created["item"].(map[string]any)["id"].(string)

	plan := `{"date":"2026-10-19","capacity":{"work_minutes":480},"blocks":[` +
		`{"kind":"focus","work_item_id":` + quote(itemID) + `,"start":"09:30","end":"11:00"},` +
		`{"kind":"break","title":"休息","start":"11:00","end":"11:15"},` +
		`{"kind":"meeting","title":"周会","start":"12:00","end":"13:00"},` +
		`{"kind":"buffer","title":"缓冲","start":"16:00","end":"16:30"}]}`
	saved := call(t, "work-log.plan.save", `{"log_root":`+quote(root)+`,"plan":`+plan+`}`)
	savedPlan := saved["plan"].(map[string]any)
	if savedPlan["revision"].(float64) != 1 {
		t.Fatalf("首次保存 revision 应为 1，实际 %v", savedPlan["revision"])
	}
	capacity := savedPlan["capacity"].(map[string]any)
	if capacity["committed_minutes"].(float64) != 90 || capacity["meeting_minutes"].(float64) != 60 {
		t.Fatalf("容量计算异常: %+v", capacity)
	}
	blocks := savedPlan["blocks"].([]any)
	if blocks[0].(map[string]any)["title"] != "实现登录" {
		t.Fatalf("时间块应自动补齐工作项标题: %+v", blocks[0])
	}

	got := call(t, "work-log.plan.get", `{"log_root":`+quote(root)+`,"date":"2026-10-19"}`)
	gotPlan := got["plan"].(map[string]any)
	if gotPlan["revision"].(float64) != 1 {
		t.Fatalf("读取不应增加 revision，实际 %v", gotPlan["revision"])
	}

	savedAgain := call(t, "work-log.plan.save", `{"log_root":`+quote(root)+`,"plan":`+plan+`}`)
	if savedAgain["plan"].(map[string]any)["revision"].(float64) != 2 {
		t.Fatalf("再次保存 revision 应递增")
	}

	overlap := `{"date":"2026-10-19","blocks":[` +
		`{"kind":"focus","title":"A","start":"09:00","end":"10:00"},` +
		`{"kind":"focus","title":"B","start":"09:30","end":"10:30"}]}`
	message := callError(t, "work-log.plan.save", `{"log_root":`+quote(root)+`,"plan":`+overlap+`}`)
	if !strings.Contains(message, "重叠") {
		t.Fatalf("重叠计划应被拒绝，实际错误: %s", message)
	}

	listed := call(t, "work-log.plan.list", `{"log_root":`+quote(root)+`,"from":"2026-10-19","to":"2026-10-19"}`)
	if listed["count"].(float64) != 1 {
		t.Fatalf("计划列表数量异常: %v", listed["count"])
	}
}

func TestBackupIncludesWorkItemsAndPlans(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.project.upsert", `{"log_root":`+quote(root)+`,"project_name":"备份项目"}`)
	call(t, "work-log.item.upsert", `{"log_root":`+quote(root)+`,"work_item":{"project":"备份项目","title":"备份工作项","estimate_minutes":30}}`)
	call(t, "work-log.plan.save", `{"log_root":`+quote(root)+`,"plan":{"date":"2026-10-19","blocks":[{"kind":"focus","title":"备份计划","start":"09:00","end":"09:30"}]}}`)
	backup := call(t, "work-log.archive.backup", `{"log_root":`+quote(root)+`}`)
	path := backup["path"].(string)
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("备份包打开失败: %v", err)
	}
	defer reader.Close()
	names := map[string]bool{}
	for _, file := range reader.File {
		names[file.Name] = true
	}
	for _, want := range []string{"work-items.json", "plans/2026-10-19.json", "projects.json"} {
		if !names[want] {
			t.Fatalf("备份缺少 %s，实际文件: %v", want, names)
		}
	}
}

func TestLegacyProjectsWithoutWork(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	legacy := `{"schema_version":"work-log-projects.v1","projects":[{"id":"pj-legacy","name":"旧项目","aliases":[],"status":"active"}]}`
	if err := os.WriteFile(filepath.Join(root, "projects.json"), []byte(legacy), 0o644); err != nil {
		t.Fatalf("写入旧登记表失败: %v", err)
	}
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":"2026-10-19","entries":[{"project":"旧项目","category":"开发","title":"旧日志","minutes":30}]}`)
	board := call(t, "work-log.project.board", `{"log_root":`+quote(root)+`}`)
	project := boardProject(t, board, "旧项目")
	if project["state"] != workStateInProgress {
		t.Fatalf("旧项目有日志时应派生 in_progress，实际 %v", project["state"])
	}
	if project["priority"] != priorityP2 {
		t.Fatalf("旧项目默认优先级应为 P2，实际 %v", project["priority"])
	}
	if steps, ok := project["next_steps"].([]any); !ok || len(steps) != 0 {
		t.Fatalf("旧项目下一步应为空数组，实际 %v", project["next_steps"])
	}
}

func TestProjectBoardRecentWindowAndInference(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	recentDate := time.Now().Format("2006-01-02")
	oldDate := time.Now().AddDate(0, 0, -30).Format("2006-01-02")
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":`+quote(oldDate)+`,"entries":[{"project":"旧项目","category":"调研","title":"早期需求调研","minutes":30}]}`)
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":`+quote(recentDate)+`,"entries":[{"project":"当前项目","category":"开发","title":"实现登录接口并完成自测","summary":"开发登录接口，进入测试阶段","minutes":120}]}`)
	call(t, "work-log.project.work.update", `{"log_root":`+quote(root)+`,"project_name":"当前项目","patch":{"next_steps":[{"text":"准备验收材料"}]}}`)

	board := call(t, "work-log.project.board", `{"log_root":`+quote(root)+`}`)
	current := boardProject(t, board, "当前项目")
	old := boardProject(t, board, "旧项目")
	if current["is_current"] != true {
		t.Fatalf("最近有日志的项目应计入当前工作: %+v", current)
	}
	if old["is_current"] == true || old["is_other"] != true {
		t.Fatalf("30 天前的项目不应计入当前工作: %+v", old)
	}
	if current["stage"] == stageUnknown {
		t.Fatalf("当前项目应能推断出阶段: %+v", current)
	}
	if progress := int(current["progress"].(float64)); progress <= 0 || progress >= 100 {
		t.Fatalf("当前项目应有合理的推断进度，实际 %d", progress)
	}
	if source := current["progress_source"]; source != "stage" && source != "next_steps" {
		t.Fatalf("推断进度来源异常: %v", source)
	}
	summary := board["summary"].(map[string]any)
	if summary["current"].(float64) < 1 || summary["other"].(float64) < 1 {
		t.Fatalf("当前/其他项目统计异常: %+v", summary)
	}

	wide := call(t, "work-log.project.board", `{"log_root":`+quote(root)+`,"recent_days":60}`)
	oldWide := boardProject(t, wide, "旧项目")
	if oldWide["is_current"] != true {
		t.Fatalf("扩大最近活动窗口后旧项目应计入当前工作: %+v", oldWide)
	}

	manual := call(t, "work-log.project.work.update", `{"log_root":`+quote(root)+`,"project_name":"当前项目","patch":{"progress":40}}`)
	_ = manual
	board = call(t, "work-log.project.board", `{"log_root":`+quote(root)+`}`)
	current = boardProject(t, board, "当前项目")
	if current["progress"].(float64) != 40 || current["progress_source"] != "manual" {
		t.Fatalf("手工进度应覆盖推断进度: %+v", current)
	}
}

func TestProjectBoardPlannedProjects(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.project.upsert", `{"log_root":`+quote(root)+`,"project_name":"待开始项目","status":"active"}`)
	call(t, "work-log.project.work.update", `{"log_root":`+quote(root)+`,"project_name":"待开始项目","patch":{"next_steps":[{"text":"启动需求调研"}]}}`)
	board := call(t, "work-log.project.board", `{"log_root":`+quote(root)+`}`)
	planned, _ := board["planned"].([]any)
	found := false
	for _, raw := range planned {
		item, _ := raw.(map[string]any)
		if item["name"] == "待开始项目" && item["is_planned"] == true {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("待开始项目应出现在 planned 列表: %+v", board["planned"])
	}
	summary := board["summary"].(map[string]any)
	if summary["planned"].(float64) < 1 {
		t.Fatalf("待开始项目统计异常: %+v", summary)
	}
}

func TestPlanReviewFinalizesPastDay(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	date := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	call(t, "work-log.project.upsert", `{"log_root":`+quote(root)+`,"project_name":"复盘项目"}`)
	created := call(t, "work-log.item.upsert", `{"log_root":`+quote(root)+`,"work_item":{"project":"复盘项目","title":"实现复盘功能","estimate_minutes":90}}`)
	itemID := created["item"].(map[string]any)["id"].(string)
	call(t, "work-log.plan.save", `{"log_root":`+quote(root)+`,"plan":{"date":`+quote(date)+`,"blocks":[{"kind":"focus","work_item_id":`+quote(itemID)+`,"title":"实现复盘功能","start":"09:00","end":"10:30"}]}}`)
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":`+quote(date)+`,"entries":[{"project":"复盘项目","category":"开发","title":"实现复盘功能","minutes":60}]}`)

	reviewResult := call(t, "work-log.plan.review", `{"log_root":`+quote(root)+`,"date":`+quote(date)+`}`)
	review := reviewResult["review"].(map[string]any)
	if review["finalized"] != true {
		t.Fatalf("历史日期应结算: %+v", review)
	}
	if review["completion_percent"].(float64) != 66 {
		t.Fatalf("实际完成比例异常: %+v", review)
	}
	blocks := review["blocks"].([]any)
	block := blocks[0].(map[string]any)
	if block["status"] != "partial" || block["actual_minutes"].(float64) != 60 {
		t.Fatalf("时间块结算异常: %+v", block)
	}

	today := time.Now().Format("2006-01-02")
	todayReview := call(t, "work-log.plan.review", `{"log_root":`+quote(root)+`,"date":`+quote(today)+`}`)
	if todayReview["review"].(map[string]any)["finalized"] != false {
		t.Fatalf("当天计划不应提前结算")
	}
}

func TestAutoPlanSchedulesWorkItems(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	call(t, "work-log.project.upsert", `{"log_root":`+quote(root)+`,"project_name":"自动排期项目"}`)
	call(t, "work-log.item.upsert", `{"log_root":`+quote(root)+`,"work_item":{"project":"自动排期项目","title":"先做P0","priority":"P0","estimate_minutes":90,"remaining_minutes":90}}`)
	call(t, "work-log.item.upsert", `{"log_root":`+quote(root)+`,"work_item":{"project":"自动排期项目","title":"再做P1","priority":"P1","estimate_minutes":60,"remaining_minutes":60}}`)
	date := time.Now().Format("2006-01-02")
	result := call(t, "work-log.plan.auto", `{"log_root":`+quote(root)+`,"date":`+quote(date)+`}`)
	plan := result["plan"].(map[string]any)
	if plan["revision"].(float64) != 1 {
		t.Fatalf("自动排期应生成计划版本: %+v", plan)
	}
	blocks := plan["blocks"].([]any)
	focus := make([]map[string]any, 0)
	for _, raw := range blocks {
		block := raw.(map[string]any)
		if block["kind"] == "focus" {
			focus = append(focus, block)
		}
	}
	if len(focus) == 0 {
		t.Fatalf("自动排期没有生成专注块: %+v", blocks)
	}
	if focus[0]["title"] != "先做P0" {
		t.Fatalf("P0 工作项应优先排期，实际 %+v", focus[0])
	}
	if result["scheduled_minutes"].(float64) <= 0 {
		t.Fatalf("已排分钟数异常: %+v", result)
	}
}

func TestAutoPlanRealisticDay(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	today := time.Now().Format("2006-01-02")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	call(t, "work-log.project.upsert", `{"log_root":`+quote(root)+`,"project_name":"排期验证"}`)
	call(t, "work-log.item.upsert", `{"log_root":`+quote(root)+`,"work_item":{"project":"排期验证","title":"P0 修复登录","priority":"P0","estimate_minutes":90,"remaining_minutes":90}}`)
	call(t, "work-log.item.upsert", `{"log_root":`+quote(root)+`,"work_item":{"project":"排期验证","title":"P1 准备验收","priority":"P1","estimate_minutes":60,"remaining_minutes":60}}`)
	call(t, "work-log.item.upsert", `{"log_root":`+quote(root)+`,"work_item":{"project":"排期验证","title":"P2 整理文档","priority":"P2","estimate_minutes":45,"remaining_minutes":45}}`)
	call(t, "work-log.plan.save", `{"log_root":`+quote(root)+`,"plan":{"date":`+quote(today)+`,"blocks":[{"kind":"meeting","title":"周会","start":"14:00","end":"15:00","locked":true}]}}`)

	result := call(t, "work-log.plan.auto", `{"log_root":`+quote(root)+`,"date":`+quote(today)+`}`)
	plan := result["plan"].(map[string]any)
	blocks := plan["blocks"].([]any)
	focus := make([]map[string]any, 0)
	meetingCount, bufferCount := 0, 0
	lastEnd := 0
	for _, raw := range blocks {
		block := raw.(map[string]any)
		kind := block["kind"].(string)
		if kind == "meeting" {
			meetingCount++
		}
		if kind == "buffer" {
			bufferCount++
		}
		if kind != "focus" {
			continue
		}
		focus = append(focus, block)
		start, _ := parseClock(block["start"].(string))
		end, _ := parseClock(block["end"].(string))
		if start < lastEnd {
			t.Fatalf("自动排期出现重叠: %+v", focus)
		}
		lastEnd = end
	}
	if len(focus) != 3 {
		t.Fatalf("期望 3 个专注块，实际 %d: %+v", len(focus), blocks)
	}
	if focus[0]["title"] != "P0 修复登录" || focus[1]["title"] != "P1 准备验收" || focus[2]["title"] != "P2 整理文档" {
		t.Fatalf("专注块优先级顺序异常: %+v", focus)
	}
	if meetingCount != 1 {
		t.Fatalf("会议块应保留且不重复，实际 %d", meetingCount)
	}
	if bufferCount != 1 {
		t.Fatalf("应生成一个缓冲块，实际 %d", bufferCount)
	}
	if result["scheduled_minutes"].(float64) > 328 {
		t.Fatalf("排期超过 80%% 承诺容量: %v", result["scheduled_minutes"])
	}

	// 重复运行不应重复会议/缓冲块。
	call(t, "work-log.plan.auto", `{"log_root":`+quote(root)+`,"date":`+quote(today)+`}`)
	plan = call(t, "work-log.plan.get", `{"log_root":`+quote(root)+`,"date":`+quote(today)+`}`)["plan"].(map[string]any)
	meetingCount, bufferCount = 0, 0
	for _, raw := range plan["blocks"].([]any) {
		block := raw.(map[string]any)
		if block["kind"] == "meeting" {
			meetingCount++
		}
		if block["kind"] == "buffer" {
			bufferCount++
		}
	}
	if meetingCount != 1 || bufferCount != 1 {
		t.Fatalf("重复排期后块重复: meeting=%d buffer=%d", meetingCount, bufferCount)
	}

	// 非当天日期必须拒绝。
	message := callError(t, "work-log.plan.auto", `{"log_root":`+quote(root)+`,"date":`+quote(yesterday)+`}`)
	if !strings.Contains(message, "只支持当天") {
		t.Fatalf("非当天排期应被拒绝，实际错误: %s", message)
	}
}

func TestAutoPlanUsesCurrentProjectsWithoutWorkItems(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	today := time.Now().Format("2006-01-02")
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"date":`+quote(today)+`,"entries":[{"project":"当前项目","category":"开发","title":"推进登录接口","minutes":60}]}`)
	call(t, "work-log.project.work.update", `{"log_root":`+quote(root)+`,"project_name":"当前项目","patch":{"next_steps":[{"text":"推进登录接口"}]}}`)

	result := call(t, "work-log.plan.auto", `{"log_root":`+quote(root)+`,"date":`+quote(today)+`}`)
	if result["scheduled_minutes"].(float64) <= 0 {
		t.Fatalf("当前项目应可生成排期: %+v", result)
	}
	if result["project_count"].(float64) < 1 || result["work_item_count"].(float64) != 0 {
		t.Fatalf("项目级排期计数异常: %+v", result)
	}
	plan := result["plan"].(map[string]any)
	found := false
	for _, raw := range plan["blocks"].([]any) {
		block := raw.(map[string]any)
		if block["kind"] == "focus" && block["title"] == "推进登录接口" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("当前项目应作为可排期项生成专注块: %+v", plan["blocks"])
	}
}
