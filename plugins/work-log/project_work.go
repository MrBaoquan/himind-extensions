package main

import (
	"errors"
	"sort"
	"strings"
	"time"
)

// 本文件实现项目工作状态（当前工作台）：优先级、目标、下一步、阻塞、完成，
// 以及基于日志的最近活动/本周投入等派生信号。
//
// 设计原则：
//   - 日志是事实，项目工作状态是意图；不根据日志文本自动判定完成。
//   - Status 仍表示登记生命周期（active/archived）；
//     Work.State 表示工作状态（planned/in_progress/blocked/review/done/paused）。

const (
	workStatePlanned    = "planned"
	workStateInProgress = "in_progress"
	workStateBlocked    = "blocked"
	workStateReview     = "review"
	workStateDone       = "done"
	workStatePaused     = "paused"
	workStateDormant    = "dormant"

	priorityP0 = "P0"
	priorityP1 = "P1"
	priorityP2 = "P2"
	priorityP3 = "P3"

	stagePlanning    = "planning"
	stageDesign      = "design"
	stageDevelopment = "development"
	stageIntegration = "integration"
	stageTesting     = "testing"
	stageAcceptance  = "acceptance"
	stageDelivery    = "delivery"
	stageMaintenance = "maintenance"
	stageDone        = "done"
	stageUnknown     = "unknown"
)

type projectWork struct {
	State             string     `json:"state"`
	Priority          string     `json:"priority"`
	Progress          int        `json:"progress,omitempty"`
	Goal              string     `json:"goal,omitempty"`
	TargetDate        string     `json:"target_date,omitempty"`
	NextSteps         []nextStep `json:"next_steps"`
	Blockers          []string   `json:"blockers"`
	CompletedAt       string     `json:"completed_at,omitempty"`
	CompletionSummary string     `json:"completion_summary,omitempty"`
	UpdatedAt         string     `json:"updated_at,omitempty"`
}

type nextStep struct {
	ID          string `json:"id"`
	Text        string `json:"text"`
	Done        bool   `json:"done"`
	Due         string `json:"due,omitempty"`
	Source      string `json:"source,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
	CompletedAt string `json:"completed_at,omitempty"`
}

type projectEntryStat struct {
	count      int
	days       map[string]struct{}
	first      string
	last       string
	lastTitle  string
	minutes7d  int
	minutes30d int
	recent     []entry
}

type projectStatsResult struct {
	Stats     map[string]*projectEntryStat
	Unmatched map[string]int
	Pending   []entry
}

func validWorkState(value string) bool {
	switch strings.TrimSpace(value) {
	case workStatePlanned, workStateInProgress, workStateBlocked, workStateReview, workStateDone, workStatePaused, workStateDormant:
		return true
	default:
		return false
	}
}

func validPriority(value string) bool {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case priorityP0, priorityP1, priorityP2, priorityP3:
		return true
	default:
		return false
	}
}

func normalizePriority(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if validPriority(value) {
		return value
	}
	return priorityP2
}

// projectWorkFor 返回带默认值的项目工作状态。默认值只在读取时派生，不强制落盘；
// 用户第一次修改时会随项目一起持久化。
func projectWorkFor(p project, stat *projectEntryStat, now time.Time, recentDays int) projectWork {
	work := p.Work
	if strings.TrimSpace(work.State) == "" || !validWorkState(work.State) {
		if p.Status == "archived" {
			work.State = workStatePaused
		} else if stat != nil && stat.count > 0 {
			if recentDays <= 0 {
				recentDays = 14
			}
			if daysSinceDate(stat.last, now) >= 0 && daysSinceDate(stat.last, now) <= recentDays {
				work.State = workStateInProgress
			} else {
				work.State = workStateDormant
			}
		} else {
			work.State = workStatePlanned
		}
	}
	if work.State == workStateDormant && stat != nil && stat.count > 0 && recentDays > 0 && daysSinceDate(stat.last, now) >= 0 && daysSinceDate(stat.last, now) <= recentDays {
		work.State = workStateInProgress
	}
	if strings.TrimSpace(work.Priority) == "" || !validPriority(work.Priority) {
		work.Priority = priorityP2
	}
	work.Priority = normalizePriority(work.Priority)
	if work.NextSteps == nil {
		work.NextSteps = []nextStep{}
	}
	if work.Blockers == nil {
		work.Blockers = []string{}
	}
	if strings.TrimSpace(work.UpdatedAt) == "" {
		work.UpdatedAt = p.UpdatedAt
	}
	if work.State == workStateDone && work.Progress == 0 {
		work.Progress = 100
	}
	return work
}

func nextStepID(projectID, text string) string {
	return "ns-" + shortHash(strings.TrimSpace(projectID)+"|"+strings.TrimSpace(text))
}

func normalizeNextSteps(projectID string, steps []nextStep, now string) []nextStep {
	out := make([]nextStep, 0, len(steps))
	seen := map[string]bool{}
	for _, step := range steps {
		text := strings.TrimSpace(step.Text)
		if text == "" {
			continue
		}
		id := strings.TrimSpace(step.ID)
		if id == "" {
			id = nextStepID(projectID, text)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		due := strings.TrimSpace(step.Due)
		if due != "" && !validDate(due) {
			due = ""
		}
		createdAt := strings.TrimSpace(step.CreatedAt)
		if createdAt == "" {
			createdAt = now
		}
		completedAt := strings.TrimSpace(step.CompletedAt)
		if step.Done {
			if completedAt == "" {
				completedAt = now
			}
		} else {
			completedAt = ""
		}
		source := strings.TrimSpace(step.Source)
		if source == "" {
			source = "manual"
		}
		out = append(out, nextStep{
			ID:          id,
			Text:        text,
			Done:        step.Done,
			Due:         due,
			Source:      source,
			CreatedAt:   createdAt,
			CompletedAt: completedAt,
		})
	}
	return out
}

func nextStepsFromPatch(projectID string, value any, now string) []nextStep {
	arr, ok := value.([]any)
	if !ok {
		if raw, ok := value.([]nextStep); ok {
			return normalizeNextSteps(projectID, raw, now)
		}
		return []nextStep{}
	}
	steps := make([]nextStep, 0, len(arr))
	for _, node := range arr {
		switch item := node.(type) {
		case string:
			steps = append(steps, nextStep{Text: item})
		case map[string]any:
			steps = append(steps, nextStep{
				ID:          mapStr(item, "id"),
				Text:        firstNonEmpty(mapStr(item, "text"), mapStr(item, "title")),
				Done:        mapBool(item, "done"),
				Due:         mapStr(item, "due"),
				Source:      mapStr(item, "source"),
				CreatedAt:   mapStr(item, "created_at"),
				CompletedAt: mapStr(item, "completed_at"),
			})
		}
	}
	return normalizeNextSteps(projectID, steps, now)
}

func mapBool(m map[string]any, key string) bool {
	if m == nil {
		return false
	}
	switch v := m[key].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true") || strings.TrimSpace(v) == "1"
	case float64:
		return v != 0
	case int:
		return v != 0
	default:
		return false
	}
}

// collectProjectStats 扫描全部日志，按规范项目名汇总条目数与近期工时。
func collectProjectStats(root string, index map[string]string) (projectStatsResult, error) {
	result := projectStatsResult{
		Stats:     map[string]*projectEntryStat{},
		Unmatched: map[string]int{},
	}
	entries, err := collectEntries(root, "0000-01-01", "9999-12-31")
	if err != nil {
		return result, err
	}
	now := time.Now()
	today := now.Format("2006-01-02")
	sevenAgo := now.AddDate(0, 0, -6).Format("2006-01-02")
	thirtyAgo := now.AddDate(0, 0, -29).Format("2006-01-02")
	for _, item := range entries {
		raw := strings.TrimSpace(item.Project)
		if raw == "" {
			result.Pending = append(result.Pending, item)
			continue
		}
		canonical, ok := resolveProjectName(index, raw)
		if !ok {
			result.Unmatched[raw]++
			canonical = raw
		}
		stat := result.Stats[canonical]
		if stat == nil {
			stat = &projectEntryStat{days: map[string]struct{}{}}
			result.Stats[canonical] = stat
		}
		stat.count++
		stat.days[item.Date] = struct{}{}
		if stat.first == "" || item.Date < stat.first {
			stat.first = item.Date
		}
		if item.Date > stat.last {
			stat.last = item.Date
			stat.lastTitle = item.Title
		}
		if item.Date >= sevenAgo && item.Date <= today {
			stat.minutes7d += entryMinutes(item)
		}
		if item.Date >= thirtyAgo && item.Date <= today {
			stat.minutes30d += entryMinutes(item)
		}
		stat.recent = append(stat.recent, item)
	}
	for _, stat := range result.Stats {
		sort.SliceStable(stat.recent, func(i, j int) bool {
			if stat.recent[i].Date != stat.recent[j].Date {
				return stat.recent[i].Date > stat.recent[j].Date
			}
			// 同一天内按真实时刻倒序：不同来源记录的时间戳时区不一致，字符串比较会排错。
			if order, ok := compareStamps(stat.recent[i].StartedAt, stat.recent[j].StartedAt); ok {
				return order > 0
			}
			return stat.recent[i].StartedAt > stat.recent[j].StartedAt
		})
		if len(stat.recent) > 30 {
			stat.recent = stat.recent[:30]
		}
	}
	return result, nil
}

func parseDateOnly(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation("2006-01-02", value, time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func daysSinceDate(date string, now time.Time) int {
	t, ok := parseDateOnly(date)
	if !ok {
		return -1
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	last := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, now.Location())
	return int(today.Sub(last).Hours() / 24)
}

func weekStartDate(now time.Time) time.Time {
	weekday := int(now.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	start := now.AddDate(0, 0, -(weekday - 1))
	return time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location())
}

func parseTimeAny(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func stateRank(state string) int {
	switch state {
	case workStateBlocked:
		return 0
	case workStateInProgress:
		return 1
	case workStateReview:
		return 2
	case workStatePlanned:
		return 3
	case workStatePaused:
		return 4
	case workStateDormant:
		return 5
	case workStateDone:
		return 6
	default:
		return 7
	}
}

func priorityRank(priority string) int {
	switch normalizePriority(priority) {
	case priorityP0:
		return 0
	case priorityP1:
		return 1
	case priorityP2:
		return 2
	case priorityP3:
		return 3
	default:
		return 4
	}
}

var projectStageOrder = []string{
	stagePlanning, stageDesign, stageDevelopment, stageIntegration,
	stageTesting, stageAcceptance, stageDelivery, stageMaintenance,
}

var projectStageLabels = map[string]string{
	stagePlanning:    "规划",
	stageDesign:      "设计",
	stageDevelopment: "开发",
	stageIntegration: "联调",
	stageTesting:     "测试",
	stageAcceptance:  "验收",
	stageDelivery:    "交付",
	stageMaintenance: "维护",
	stageDone:        "已完成",
	stageUnknown:     "未判断",
}

var projectStageKeywords = map[string][]string{
	stagePlanning:    {"调研", "需求", "规划", "立项", "可行性", "竞品", "评估", "方案"},
	stageDesign:      {"设计", "原型", "交互", "视觉", "ui", "ue", "架构", "样式"},
	stageDevelopment: {"开发", "实现", "编码", "接口", "功能", "修复", "重构", "优化", "代码"},
	stageIntegration: {"联调", "集成", "对接", "打通", "部署", "环境", "配置"},
	stageTesting:     {"测试", "用例", "回归", "验证", "缺陷", "bug", "qa", "自测"},
	stageAcceptance:  {"验收", "评审", "确认", "演示", "试运行", "初验", "终验"},
	stageDelivery:    {"上线", "发布", "交付", "投产", "迁移", "结项", "上线成功"},
	stageMaintenance: {"运维", "维护", "监控", "告警", "巡检", "支持", "问题处理"},
}

func projectStageLabel(stage string) string {
	if label, ok := projectStageLabels[stage]; ok {
		return label
	}
	return stage
}

func stageIndex(stage string) int {
	for i, item := range projectStageOrder {
		if item == stage {
			return i
		}
	}
	if stage == stageDone {
		return len(projectStageOrder)
	}
	return 0
}

func stageBaseProgress(stage string) int {
	switch stage {
	case stagePlanning:
		return 10
	case stageDesign:
		return 25
	case stageDevelopment:
		return 50
	case stageIntegration:
		return 65
	case stageTesting:
		return 80
	case stageAcceptance:
		return 90
	case stageDelivery:
		return 95
	case stageMaintenance:
		return 85
	case stageDone:
		return 100
	default:
		return 20
	}
}

func clampInt(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func addStageText(scores map[string]int, text string, weight int) {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" || weight <= 0 {
		return
	}
	for stage, keywords := range projectStageKeywords {
		for _, keyword := range keywords {
			if strings.Contains(lower, strings.ToLower(keyword)) {
				scores[stage] += weight
			}
		}
	}
}

// inferProjectStage 用最近日志文本与下一步推断项目阶段。它只做确定性启发式，
// 结果带置信度，用户可随时在项目工作状态里覆盖。
func inferProjectStage(p project, work projectWork, stat *projectEntryStat, now time.Time) (string, string) {
	switch work.State {
	case workStateDone:
		return stageDone, "high"
	case workStateReview:
		return stageAcceptance, "high"
	case workStatePlanned:
		return stagePlanning, "high"
	}
	scores := map[string]int{}
	if stat != nil {
		for _, item := range stat.recent {
			weight := 1
			if daysSinceDate(item.Date, now) <= 14 {
				weight = 2
			}
			addStageText(scores, item.Title, weight*2)
			addStageText(scores, item.Summary, weight*2)
			addStageText(scores, strings.Join(item.Outcomes, " "), weight*2)
			addStageText(scores, strings.Join(item.Next, " "), weight)
			addStageText(scores, strings.Join(item.Blockers, " "), weight)
			addStageText(scores, item.Feature, weight)
			addStageText(scores, strings.Join(item.Tags, " "), weight)
		}
	}
	for _, step := range work.NextSteps {
		if !step.Done {
			addStageText(scores, step.Text, 2)
		}
	}
	best := stageUnknown
	bestScore := 0
	for _, stage := range projectStageOrder {
		score := scores[stage]
		if score > bestScore || (score == bestScore && score > 0 && stageIndex(stage) > stageIndex(best)) {
			best = stage
			bestScore = score
		}
	}
	if bestScore == 0 {
		if work.State == workStateBlocked || p.Kind == "交付项目" || p.Kind == "平台产品" {
			return stageDevelopment, "low"
		}
		return stageUnknown, "low"
	}
	confidence := "low"
	if bestScore >= 6 {
		confidence = "high"
	} else if bestScore >= 3 {
		confidence = "medium"
	}
	return best, confidence
}

func inferProjectProgress(p project, work projectWork, stat *projectEntryStat, stage string, now time.Time) (int, string, string) {
	if work.State == workStateDone {
		return 100, "state", "high"
	}
	if work.Progress > 0 {
		return clampInt(work.Progress, 0, 100), "manual", "high"
	}
	base := stageBaseProgress(stage)
	stepPct, hasSteps := projectPlanCompletion(work)
	if hasSteps {
		if stepPct >= 100 {
			base = 95
		} else if stepPct > 0 {
			base = int(float64(base)*0.3 + float64(stepPct)*0.7)
		}
		confidence := "medium"
		if len(work.NextSteps) >= 3 {
			confidence = "high"
		}
		return clampInt(base, 5, 95), "next_steps", confidence
	}
	if stat != nil {
		recentCount := 0
		for _, item := range stat.recent {
			if daysSinceDate(item.Date, now) >= 0 && daysSinceDate(item.Date, now) <= 14 {
				recentCount++
			}
		}
		if recentCount >= 5 {
			base += 5
		}
		if recentCount >= 10 {
			base += 5
		}
	}
	if work.State == workStateReview {
		base = maxInt(base, 85)
	}
	if work.State == workStatePlanned {
		base = minInt(base, 15)
	}
	return clampInt(base, 5, 95), "stage", "low"
}

func recentDaysFor(in input) int {
	days := 14
	if in.RecentDays != nil && *in.RecentDays > 0 {
		days = *in.RecentDays
	}
	if days > 365 {
		days = 365
	}
	return days
}

func isRecentDate(date string, now time.Time, recentDays int) bool {
	days := daysSinceDate(date, now)
	return days >= 0 && days <= recentDays
}
func projectPlanCompletion(work projectWork) (int, bool) {
	if len(work.NextSteps) == 0 {
		return 0, false
	}
	done := 0
	for _, step := range work.NextSteps {
		if step.Done {
			done++
		}
	}
	return int(float64(done) / float64(len(work.NextSteps)) * 100), true
}

func isPlannedActive(p project, work projectWork, now time.Time) bool {
	if work.State != workStatePlanned {
		return false
	}
	if len(work.NextSteps) > 0 {
		return true
	}
	if work.Priority == priorityP0 || work.Priority == priorityP1 {
		return true
	}
	if target, ok := parseDateOnly(work.TargetDate); ok {
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		if !target.Before(today) {
			return true
		}
	}
	if updated, ok := parseTimeAny(firstNonEmpty(work.UpdatedAt, p.UpdatedAt)); ok {
		return !updated.Before(now.AddDate(0, 0, -30))
	}
	return false
}
func projectBoardItem(p project, work projectWork, stat *projectEntryStat, now time.Time, recentDays int) map[string]any {
	daysSince := -1
	lastDate := ""
	lastTitle := ""
	entryCount, activeDays, minutes7d, minutes30d := 0, 0, 0, 0
	if stat != nil {
		lastDate = stat.last
		lastTitle = stat.lastTitle
		entryCount = stat.count
		activeDays = len(stat.days)
		minutes7d = stat.minutes7d
		minutes30d = stat.minutes30d
		daysSince = daysSinceDate(stat.last, now)
	}
	planCompletion, hasPlan := projectPlanCompletion(work)
	recent := isRecentDate(lastDate, now, recentDays)
	updatedRecently := false
	if updated, ok := parseTimeAny(firstNonEmpty(work.UpdatedAt, p.UpdatedAt)); ok {
		updatedRecently = !updated.Before(now.AddDate(0, 0, -recentDays))
	}
	activeState := work.State == workStateInProgress || work.State == workStateBlocked || work.State == workStateReview
	isCurrent := (recent || updatedRecently) && activeState
	isOther := !isCurrent && work.State != workStateDone
	plannedActive := isPlannedActive(p, work, now)
	activityBucket := "history"
	if work.State == workStateDone {
		activityBucket = "done"
	} else if isCurrent {
		activityBucket = "current"
	} else if plannedActive {
		activityBucket = "planned"
	} else if isOther && daysSince >= 0 && daysSince <= recentDays*2 {
		activityBucket = "recent"
	}
	doneRecent := false
	if work.State == workStateDone {
		if work.CompletedAt != "" {
			if completed, ok := parseTimeAny(work.CompletedAt); ok {
				doneRecent = !completed.Before(now.AddDate(0, 0, -30))
			}
		}
		if !doneRecent && lastDate != "" {
			doneRecent = isRecentDate(lastDate, now, 30)
		}
	}
	stage, stageConfidence := inferProjectStage(p, work, stat, now)
	progress, progressSource, progressConfidence := inferProjectProgress(p, work, stat, stage, now)
	stale := isCurrent && (work.State == workStateInProgress || work.State == workStateReview) && daysSince > 7
	overdue := false
	if work.TargetDate != "" && work.State != workStateDone {
		if target, ok := parseDateOnly(work.TargetDate); ok {
			today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
			overdue = target.Before(today)
		}
	}
	return map[string]any{
		"id":                  p.ID,
		"name":                p.Name,
		"description":         p.Description,
		"status":              p.Status,
		"color":               p.Color,
		"kind":                p.Kind,
		"source":              p.Source,
		"external_id":         p.ExternalID,
		"paths":               pathsToMaps(p.Paths),
		"exhibits":            exhibitsToMaps(p.Exhibits),
		"created_at":          p.CreatedAt,
		"updated_at":          p.UpdatedAt,
		"work":                work,
		"state":               work.State,
		"priority":            work.Priority,
		"manual_progress":     work.Progress,
		"progress":            progress,
		"progress_source":     progressSource,
		"progress_confidence": progressConfidence,
		"stage":               stage,
		"stage_label":         projectStageLabel(stage),
		"stage_confidence":    stageConfidence,
		"recent":              recent,
		"recent_days":         recentDays,
		"is_current":          isCurrent,
		"is_other":            isOther,
		"is_planned":          plannedActive,
		"is_done_recent":      doneRecent,
		"activity_bucket":     activityBucket,
		"goal":                work.Goal,
		"target_date":         work.TargetDate,
		"next_steps":          work.NextSteps,
		"blockers":            work.Blockers,
		"completed_at":        work.CompletedAt,
		"completion_summary":  work.CompletionSummary,
		"last_date":           lastDate,
		"last_title":          lastTitle,
		"entry_count":         entryCount,
		"active_days":         activeDays,
		"minutes_7d":          minutes7d,
		"minutes_30d":         minutes30d,
		"days_since_activity": daysSince,
		"stale":               stale,
		"overdue":             overdue,
		"plan_completion":     planCompletion,
		"has_plan":            hasPlan,
	}
}

// projectWorkUpdateCap 用 patch 语义更新项目工作状态；只改显式传入的字段。
func projectWorkUpdateCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	if len(in.Patch) == 0 {
		return nil, errors.New("patch 不能为空：请至少提供一个要修改的字段")
	}
	unlock := lockRoot(root)
	defer unlock()

	pf, err := readProjects(root)
	if err != nil {
		return nil, err
	}
	index := -1
	if id := strings.TrimSpace(in.ProjectID); id != "" {
		for i := range pf.Projects {
			if pf.Projects[i].ID == id {
				index = i
				break
			}
		}
	}
	if index < 0 {
		name := strings.TrimSpace(in.ProjectName)
		if name == "" {
			name = strings.TrimSpace(in.Project)
		}
		if name != "" {
			canonical, ok := resolveProjectName(projectIndex(pf), name)
			if ok {
				name = canonical
			}
			index = projectIndexByName(pf, name)
		}
	}
	if index < 0 {
		return nil, errors.New("未找到项目：请提供 project_id 或 project_name")
	}

	stats, err := collectProjectStats(root, projectIndex(pf))
	if err != nil {
		return nil, err
	}
	p := &pf.Projects[index]
	work := projectWorkFor(*p, stats.Stats[p.Name], time.Now(), recentDaysFor(in))
	now := time.Now().Format(time.RFC3339)
	stateProvided := false
	progressProvided := false
	stateValue := work.State

	for key, raw := range in.Patch {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "state", "work_state":
			value := strings.TrimSpace(asText(raw))
			if !validWorkState(value) {
				return nil, errors.New("无效的项目状态：" + value)
			}
			work.State = value
			stateValue = value
			stateProvided = true
		case "priority":
			value := strings.ToUpper(strings.TrimSpace(asText(raw)))
			if !validPriority(value) {
				return nil, errors.New("无效的优先级：" + value)
			}
			work.Priority = value
		case "progress":
			value := asInt(raw)
			if value < 0 {
				value = 0
			}
			if value > 100 {
				value = 100
			}
			work.Progress = value
			progressProvided = true
		case "goal":
			work.Goal = strings.TrimSpace(asText(raw))
		case "target_date":
			value := strings.TrimSpace(asText(raw))
			if value != "" && !validDate(value) {
				return nil, errors.New("target_date 必须是 YYYY-MM-DD")
			}
			work.TargetDate = value
		case "blockers":
			work.Blockers = trimList(asTextList(raw))
		case "next_steps":
			work.NextSteps = nextStepsFromPatch(p.ID, raw, now)
		case "completion_summary":
			work.CompletionSummary = strings.TrimSpace(asText(raw))
		case "completed_at":
			value := strings.TrimSpace(asText(raw))
			if value != "" {
				if _, ok := parseTimeAny(value); !ok {
					return nil, errors.New("completed_at 不是合法时间")
				}
			}
			work.CompletedAt = value
		default:
			return nil, errors.New("不支持的字段：" + key)
		}
	}

	if stateProvided {
		if stateValue == workStateDone {
			if strings.TrimSpace(work.CompletedAt) == "" {
				work.CompletedAt = now
			}
			if !progressProvided {
				work.Progress = 100
			}
		} else {
			work.CompletedAt = ""
			work.CompletionSummary = ""
		}
	}
	work.State = stateValue
	work.Priority = normalizePriority(work.Priority)
	work.NextSteps = normalizeNextSteps(p.ID, work.NextSteps, now)
	work.Blockers = nonNilStrings(work.Blockers)
	work.UpdatedAt = now
	p.Work = work
	p.UpdatedAt = now
	if err := writeProjects(root, pf); err != nil {
		return nil, err
	}
	return map[string]any{
		"log_root": root,
		"project":  *p,
		"work":     work,
	}, nil
}

// projectBoardCap 返回「当前工作台」所需的聚合视图。
func projectBoardCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	pf, err := readProjects(root)
	if err != nil {
		return nil, err
	}
	stats, err := collectProjectStats(root, projectIndex(pf))
	if err != nil {
		return nil, err
	}
	now := time.Now()
	today := now.Format("2006-01-02")
	weekStart := weekStartDate(now)
	recentDays := recentDaysFor(in)

	projects := make([]map[string]any, 0, len(pf.Projects))
	attention := make([]map[string]any, 0)
	type candidate struct {
		project project
		work    projectWork
		stat    *projectEntryStat
		current bool
	}
	candidates := make([]candidate, 0, len(pf.Projects))
	summary := map[string]int{
		"current": 0, "recent": 0, "history": 0, "other": 0, "done_recent": 0,
		"planned": 0, "in_progress": 0, "blocked": 0, "review": 0, "blocked_current": 0,
		"paused": 0, "done": 0, "done_this_week": 0, "minutes_this_week": 0,
	}

	for _, p := range pf.Projects {
		if strings.EqualFold(p.Status, "archived") {
			continue
		}
		stat := stats.Stats[p.Name]
		work := projectWorkFor(p, stat, now, recentDays)
		item := projectBoardItem(p, work, stat, now, recentDays)
		projects = append(projects, item)
		isCurrent := item["is_current"] == true
		candidates = append(candidates, candidate{project: p, work: work, stat: stat, current: isCurrent})
		if isCurrent {
			summary["current"]++
			switch work.State {
			case workStatePlanned:
				summary["planned"]++
			case workStateInProgress:
				summary["in_progress"]++
			case workStateBlocked:
				summary["blocked"]++
				summary["blocked_current"]++
			case workStateReview:
				summary["review"]++
			case workStatePaused:
				summary["paused"]++
			}
		} else if item["is_other"] == true {
			summary["other"]++
		}
		if item["is_done_recent"] == true {
			summary["done_recent"]++
		}
		if work.State == workStateDone {
			summary["done"]++
			if work.CompletedAt != "" {
				if t, ok := parseTimeAny(work.CompletedAt); ok && !t.Before(weekStart) {
					summary["done_this_week"]++
				}
			} else if stat != nil && stat.last >= weekStart.Format("2006-01-02") {
				summary["done_this_week"]++
			}
		}
		if stat != nil {
			summary["minutes_this_week"] += stat.minutes7d
		}

		if isCurrent {
			reasons := make([]string, 0, 3)
			if work.State == workStateBlocked {
				reasons = append(reasons, "blocked")
			}
			if item["stale"] == true {
				reasons = append(reasons, "stale")
			}
			if item["overdue"] == true {
				reasons = append(reasons, "overdue")
			}
			if len(reasons) > 0 {
				attention = append(attention, map[string]any{
					"project_id":  p.ID,
					"name":        p.Name,
					"priority":    work.Priority,
					"state":       work.State,
					"reasons":     reasons,
					"days":        item["days_since_activity"],
					"target_date": work.TargetDate,
				})
			}
		}
	}

	projectGroupRank := func(item map[string]any) int {
		if item["is_current"] == true {
			return 0
		}
		if item["activity_bucket"] == "planned" {
			return 1
		}
		if item["is_other"] == true {
			return 2
		}
		if item["is_done_recent"] == true {
			return 3
		}
		return 4
	}
	sort.SliceStable(projects, func(i, j int) bool {
		left, right := projects[i], projects[j]
		lg, rg := projectGroupRank(left), projectGroupRank(right)
		if lg != rg {
			return lg < rg
		}
		ls, rs := stateRank(mapStr(left, "state")), stateRank(mapStr(right, "state"))
		if ls != rs {
			return ls < rs
		}
		lp, rp := priorityRank(mapStr(left, "priority")), priorityRank(mapStr(right, "priority"))
		if lp != rp {
			return lp < rp
		}
		lt, rt := mapStr(left, "target_date"), mapStr(right, "target_date")
		if lt != rt {
			if lt == "" {
				return false
			}
			if rt == "" {
				return true
			}
			return lt < rt
		}
		return mapStr(left, "last_date") > mapStr(right, "last_date")
	})

	sort.SliceStable(attention, func(i, j int) bool {
		lp, rp := priorityRank(mapStr(attention[i], "priority")), priorityRank(mapStr(attention[j], "priority"))
		if lp != rp {
			return lp < rp
		}
		return mapStr(attention[i], "name") < mapStr(attention[j], "name")
	})

	focusCandidates := make([]candidate, 0, len(candidates))
	for _, c := range candidates {
		if !c.current {
			continue
		}
		switch c.work.State {
		case workStateInProgress, workStateBlocked, workStateReview:
			focusCandidates = append(focusCandidates, c)
		}
	}
	if len(focusCandidates) == 0 {
		for _, c := range candidates {
			if c.current && c.work.State == workStatePlanned && (c.work.Priority == priorityP0 || c.work.Priority == priorityP1) {
				focusCandidates = append(focusCandidates, c)
			}
		}
	}
	sort.SliceStable(focusCandidates, func(i, j int) bool {
		lp, rp := priorityRank(focusCandidates[i].work.Priority), priorityRank(focusCandidates[j].work.Priority)
		if lp != rp {
			return lp < rp
		}
		ls, rs := stateRank(focusCandidates[i].work.State), stateRank(focusCandidates[j].work.State)
		if ls != rs {
			return ls < rs
		}
		lt, rt := focusCandidates[i].work.TargetDate, focusCandidates[j].work.TargetDate
		if lt != rt {
			if lt == "" {
				return false
			}
			if rt == "" {
				return true
			}
			return lt < rt
		}
		li, ri := "", ""
		if focusCandidates[i].stat != nil {
			li = focusCandidates[i].stat.last
		}
		if focusCandidates[j].stat != nil {
			ri = focusCandidates[j].stat.last
		}
		return li > ri
	})
	focus := make([]string, 0, 3)
	for _, c := range focusCandidates {
		if len(focus) >= 3 {
			break
		}
		focus = append(focus, c.project.ID)
	}

	currentProjects := make([]map[string]any, 0)
	plannedProjects := make([]map[string]any, 0)
	recentProjects := make([]map[string]any, 0)
	doneProjects := make([]map[string]any, 0)
	historyCount := 0
	for _, item := range projects {
		switch mapStr(item, "activity_bucket") {
		case "current":
			currentProjects = append(currentProjects, item)
		case "planned":
			plannedProjects = append(plannedProjects, item)
		case "recent":
			recentProjects = append(recentProjects, item)
		case "done":
			if item["is_done_recent"] == true {
				doneProjects = append(doneProjects, item)
			} else {
				historyCount++
			}
		default:
			historyCount++
		}
	}
	summary["current"] = len(currentProjects)
	summary["planned"] = len(plannedProjects)
	summary["recent"] = len(recentProjects)
	summary["done_recent"] = len(doneProjects)
	summary["history"] = historyCount
	summary["other"] = len(recentProjects) + historyCount

	return map[string]any{
		"log_root":      root,
		"date":          today,
		"recent_days":   recentDays,
		"summary":       summary,
		"focus":         focus,
		"attention":     attention,
		"current":       currentProjects,
		"planned":       plannedProjects,
		"recent":        recentProjects,
		"done":          doneProjects,
		"history_count": historyCount,
		"projects":      projects,
		"unmatched":     unmatchedList(stats.Unmatched),
	}, nil
}

func unmatchedList(unmatched map[string]int) []map[string]any {
	out := make([]map[string]any, 0, len(unmatched))
	for name, count := range unmatched {
		out = append(out, map[string]any{"name": name, "entry_count": count})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if mapInt(out[i]["entry_count"]) != mapInt(out[j]["entry_count"]) {
			return mapInt(out[i]["entry_count"]) > mapInt(out[j]["entry_count"])
		}
		return mapStr(out[i], "name") < mapStr(out[j], "name")
	})
	return out
}
