package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// 一键智能排期：不做黑盒 AI 调用，而是用确定性的优先级、截止日期、剩余耗时和
// 现有锁定块生成可执行计划。Agent 仍可在此基础上做语义层调整。

type planProjectInfo struct {
	Name       string
	Priority   string
	State      string
	Goal       string
	Progress   int
	TargetDate string
	Blockers   []string
	NextSteps  []nextStep
}

type schedulablePlanItem struct {
	WorkItemID       string
	ProjectID        string
	ProjectName      string
	Title            string
	Priority         string
	RemainingMinutes int
	DueDate          string
	ProjectState     string
	ProjectPriority  string
}

type planInterval struct {
	start int
	end   int
}

func minutesToClock(minutes int) string {
	if minutes < 0 {
		minutes = 0
	}
	if minutes > 1439 {
		minutes = 1439
	}
	return fmt.Sprintf("%02d:%02d", minutes/60, minutes%60)
}

func remainingForPlanItem(item workItem) int {
	if item.RemainingMinutes > 0 {
		return item.RemainingMinutes
	}
	if item.EstimateMinutes > 0 {
		return item.EstimateMinutes
	}
	return 30
}

func subtractPlanBlock(intervals []planInterval, block planBlock) []planInterval {
	start, okStart := parseClock(block.Start)
	end, okEnd := parseClock(block.End)
	if !okStart || !okEnd || end <= start {
		return intervals
	}
	out := make([]planInterval, 0, len(intervals)+1)
	for _, interval := range intervals {
		if end <= interval.start || start >= interval.end {
			out = append(out, interval)
			continue
		}
		if start > interval.start {
			out = append(out, planInterval{start: interval.start, end: start})
		}
		if end < interval.end {
			out = append(out, planInterval{start: end, end: interval.end})
		}
	}
	return out
}

func findPlanInterval(intervals []planInterval, need int) int {
	for i, interval := range intervals {
		if interval.end-interval.start >= need {
			return i
		}
	}
	return -1
}

func nextStepsFromBoard(value any) []nextStep {
	if steps, ok := value.([]nextStep); ok {
		return steps
	}
	if list, ok := value.([]any); ok {
		out := make([]nextStep, 0, len(list))
		for _, raw := range list {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			out = append(out, nextStep{
				ID:   mapStr(item, "id"),
				Text: mapStr(item, "text"),
				Done: mapBool(item, "done"),
				Due:  mapStr(item, "due"),
			})
		}
		return out
	}
	return nil
}

func planProjectMap(board map[string]any) map[string]planProjectInfo {
	out := map[string]planProjectInfo{}
	add := func(value any) {
		items, _ := value.([]map[string]any)
		if items == nil {
			if list, ok := value.([]any); ok {
				for _, raw := range list {
					if item, ok := raw.(map[string]any); ok {
						items = append(items, item)
					}
				}
			}
		}
		for _, item := range items {
			id := mapStr(item, "id")
			if id == "" {
				continue
			}
			out[id] = planProjectInfo{
				Name:       mapStr(item, "name"),
				Priority:   mapStr(item, "priority"),
				State:      mapStr(item, "state"),
				Goal:       mapStr(item, "goal"),
				Progress:   asInt(item["progress"]),
				TargetDate: mapStr(item, "target_date"),
				Blockers:   asTextList(item["blockers"]),
				NextSteps:  nextStepsFromBoard(item["next_steps"]),
			}
		}
	}
	add(board["current"])
	add(board["planned"])
	return out
}

func boardProjectIDs(board map[string]any) []string {
	seen := map[string]bool{}
	out := make([]string, 0)
	add := func(value any) {
		items, _ := value.([]map[string]any)
		if items == nil {
			if list, ok := value.([]any); ok {
				for _, raw := range list {
					if item, ok := raw.(map[string]any); ok {
						items = append(items, item)
					}
				}
			}
		}
		for _, item := range items {
			id := mapStr(item, "id")
			if id != "" && !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	add(board["current"])
	add(board["planned"])
	return out
}

func projectPlaceholder(projectID string, info planProjectInfo) schedulablePlanItem {
	title := ""
	for _, step := range info.NextSteps {
		if !step.Done && strings.TrimSpace(step.Text) != "" {
			title = step.Text
			break
		}
	}
	if title == "" && info.State == workStateBlocked && len(info.Blockers) > 0 {
		title = "解除阻塞：" + info.Blockers[0]
	}
	if title == "" && strings.TrimSpace(info.Goal) != "" {
		title = info.Goal
	}
	if title == "" {
		title = info.Name
	}
	minutes := 60
	switch normalizePriority(info.Priority) {
	case priorityP0:
		minutes = 90
	case priorityP1:
		minutes = 60
	case priorityP2:
		minutes = 45
	case priorityP3:
		minutes = 30
	}
	return schedulablePlanItem{
		ProjectID:        projectID,
		ProjectName:      info.Name,
		Title:            title,
		Priority:         normalizePriority(info.Priority),
		RemainingMinutes: minutes,
		DueDate:          info.TargetDate,
		ProjectState:     info.State,
		ProjectPriority:  normalizePriority(info.Priority),
	}
}
func autoPlanCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	today := time.Now().Format("2006-01-02")
	date := normalizeDate(strings.TrimSpace(in.Date))
	if date == "" || !validDate(date) {
		date = today
	}
	if date != today {
		return nil, errors.New("一键智能排期只支持当天（" + today + "）；历史或未来日期请手动调整计划")
	}
	unlock := lockRoot(root)
	defer unlock()

	existing, err := readPlan(root, date)
	if err != nil {
		return nil, err
	}
	boardResult, err := projectBoardCap(in)
	if err != nil {
		return nil, err
	}
	board, _ := boardResult.(map[string]any)
	projectInfo := planProjectMap(board)

	itemsFile, err := readWorkItems(root)
	if err != nil {
		return nil, err
	}
	candidates := make([]schedulablePlanItem, 0)
	hasWorkItems := map[string]bool{}
	workItemCount := 0
	for _, item := range itemsFile.Items {
		if item.Status == "done" || item.Status == "cancelled" || item.Status == "blocked" {
			continue
		}
		info, ok := projectInfo[item.ProjectID]
		if !ok {
			continue
		}
		candidates = append(candidates, schedulablePlanItem{
			WorkItemID:       item.ID,
			ProjectID:        item.ProjectID,
			ProjectName:      info.Name,
			Title:            item.Title,
			Priority:         item.Priority,
			RemainingMinutes: remainingForPlanItem(item),
			DueDate:          item.DueDate,
			ProjectState:     info.State,
			ProjectPriority:  info.Priority,
		})
		hasWorkItems[item.ProjectID] = true
		workItemCount++
	}
	for _, projectID := range boardProjectIDs(board) {
		if hasWorkItems[projectID] {
			continue
		}
		info, ok := projectInfo[projectID]
		if !ok {
			continue
		}
		candidates = append(candidates, projectPlaceholder(projectID, info))
	}
	projectCount := len(candidates) - workItemCount
	if len(candidates) == 0 {
		return nil, errors.New("当前没有可排期的项目或工作项：请先记录最近工作或新增工作项")
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		ls, rs := stateRank(left.ProjectState), stateRank(right.ProjectState)
		if ls != rs {
			return ls < rs
		}
		lp, rp := priorityRank(left.ProjectPriority), priorityRank(right.ProjectPriority)
		if lp != rp {
			return lp < rp
		}
		lip, rip := priorityRank(left.Priority), priorityRank(right.Priority)
		if lip != rip {
			return lip < rip
		}
		if left.DueDate != right.DueDate {
			if left.DueDate == "" {
				return false
			}
			if right.DueDate == "" {
				return true
			}
			return left.DueDate < right.DueDate
		}
		return left.RemainingMinutes < right.RemainingMinutes
	})

	fixed := make([]planBlock, 0)
	hasBreak := false
	hasLockedBuffer := false
	for _, block := range existing.Blocks {
		if block.Kind == "break" {
			hasBreak = true
		}
		if block.Kind == "buffer" && block.Locked {
			hasLockedBuffer = true
		}
		if block.Kind == "meeting" || block.Kind == "personal" || block.Kind == "break" || block.Locked {
			fixed = append(fixed, block)
		}
	}
	if !hasBreak {
		lunch := planBlock{Kind: "break", Title: "午休", Start: "12:00", End: "13:00", Status: "planned", Source: "auto", Locked: true}
		afternoon := planBlock{Kind: "break", Title: "短休", Start: "15:00", End: "15:10", Status: "planned", Source: "auto", Locked: true}
		fixed = append(fixed, lunch, afternoon)
	}

	// 默认工作窗口：09:00-12:00、13:00-18:00。已有会议/锁定块会从窗口中扣除。
	free := []planInterval{{start: 9 * 60, end: 12 * 60}, {start: 13 * 60, end: 18 * 60}}
	for _, block := range fixed {
		free = subtractPlanBlock(free, block)
	}
	available := 0
	for _, interval := range free {
		available += interval.end - interval.start
	}
	if available < 15 {
		return nil, errors.New("今天没有足够的可用时间，请先清理固定会议或锁定块")
	}

	blocks := append([]planBlock{}, fixed...)
	risks := make([]planRisk, 0)
	totalRemaining := 0
	for _, item := range candidates {
		totalRemaining += item.RemainingMinutes
	}
	target := int(float64(available) * 0.8)
	if totalRemaining > target {
		risks = append(risks, planRisk{
			Level:   "warning",
			Message: fmt.Sprintf("待排工作量约 %d 分钟，今日可承诺约 %d 分钟，未排部分会保留到后续日期", totalRemaining, target),
		})
	}

	scheduled := 0
	unscheduled := 0
	budget := target
	for _, item := range candidates {
		remaining := item.RemainingMinutes
		if budget < 15 {
			unscheduled += remaining
			continue
		}
		for remaining > 0 {
			need := remaining
			if need > 90 {
				need = 90
			}
			if need > budget {
				need = budget
			}
			if need < 15 {
				unscheduled += remaining
				break
			}
			idx := findPlanInterval(free, need)
			if idx < 0 && need > 30 {
				need = 30
				idx = findPlanInterval(free, need)
			}
			if idx < 0 && need > 15 {
				need = 15
				idx = findPlanInterval(free, need)
			}
			if idx < 0 {
				unscheduled += remaining
				break
			}
			interval := free[idx]
			duration := interval.end - interval.start
			if duration > need {
				duration = need
			}
			if duration > remaining {
				duration = remaining
			}
			start := interval.start
			end := start + duration
			blocks = append(blocks, planBlock{
				Kind:       "focus",
				WorkItemID: item.WorkItemID,
				ProjectID:  item.ProjectID,
				Title:      item.Title,
				Start:      minutesToClock(start),
				End:        minutesToClock(end),
				Status:     "planned",
				Source:     "auto",
				Rationale:  "按项目/工作项优先级、截止日期与剩余耗时自动排期",
			})
			scheduled += duration
			budget -= duration
			remaining -= duration
			free[idx].start = end
			if remaining > 0 && free[idx].end-free[idx].start >= 10 {
				blocks = append(blocks, planBlock{
					Kind:   "break",
					Title:  "短休",
					Start:  minutesToClock(free[idx].start),
					End:    minutesToClock(free[idx].start + 10),
					Status: "planned",
					Source: "auto",
					Locked: true,
				})
				free[idx].start += 10
			}
			if free[idx].end-free[idx].start < 10 {
				free = append(free[:idx], free[idx+1:]...)
			}
		}
	}
	if scheduled == 0 {
		return nil, errors.New("没有可用的专注时间，请检查会议和锁定块")
	}
	if unscheduled > 0 {
		risks = append(risks, planRisk{
			Level:   "warning",
			Message: fmt.Sprintf("仍有约 %d 分钟工作未排入今日计划", unscheduled),
		})
	}

	// 在最后一段空闲时间里留一块缓冲；已有锁定缓冲则保留它，避免重复。
	if !hasLockedBuffer && len(free) > 0 {
		last := free[len(free)-1]
		bufferMinutes := last.end - last.start
		if bufferMinutes > 45 {
			bufferMinutes = 45
		}
		if bufferMinutes >= 15 {
			blocks = append(blocks, planBlock{
				Kind:   "buffer",
				Title:  "缓冲",
				Start:  minutesToClock(last.end - bufferMinutes),
				End:    minutesToClock(last.end),
				Status: "planned",
				Source: "auto",
				Locked: true,
			})
		}
	}

	plan := existing
	plan.Date = date
	plan.Blocks = blocks
	plan.Risks = risks
	plan.Questions = []string{}
	plan.GeneratedBy = "auto-scheduler"
	plan.GeneratedAt = time.Now().Format(time.RFC3339)
	normalized, validation := normalizePlan(root, plan, true)
	normalized.SchemaVersion = planSchemaVersion
	if len(validation.Errors) > 0 {
		return nil, errors.New(strings.Join(validation.Errors, "；"))
	}
	for _, warning := range validation.Warnings {
		normalized.Risks = append(normalized.Risks, planRisk{Level: "warning", Message: warning})
	}
	if err := writePlan(root, normalized); err != nil {
		return nil, err
	}
	return map[string]any{
		"log_root":            root,
		"plan":                normalized,
		"scheduled_minutes":   scheduled,
		"unscheduled_minutes": unscheduled,
		"schedulable_count":   len(candidates),
		"work_item_count":     workItemCount,
		"project_count":       projectCount,
		"warnings":            validation.Warnings,
	}, nil
}
