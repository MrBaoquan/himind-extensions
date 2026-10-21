package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 本文件实现每日计划与时间块。计划是「意图」，日志仍是「事实」：
// 计划块记录准备在什么时间做什么，实际投入仍由工作日志/专注记录归集。

const planSchemaVersion = "work-log-plan.v1"

var planBlockKinds = map[string]bool{
	"focus": true, "meeting": true, "admin": true, "break": true, "buffer": true, "personal": true,
}

var planBlockStatuses = map[string]bool{
	"planned": true, "active": true, "done": true, "skipped": true, "moved": true,
}

type planBlock struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	WorkItemID    string `json:"work_item_id,omitempty"`
	ProjectID     string `json:"project_id,omitempty"`
	Title         string `json:"title"`
	Start         string `json:"start"`
	End           string `json:"end"`
	Status        string `json:"status"`
	Locked        bool   `json:"locked,omitempty"`
	Source        string `json:"source,omitempty"`
	Rationale     string `json:"rationale,omitempty"`
	ActualMinutes int    `json:"actual_minutes,omitempty"`
}

type planRisk struct {
	Level   string `json:"level"`
	Message string `json:"message"`
}

type planCapacity struct {
	WorkMinutes      int `json:"work_minutes"`
	MeetingMinutes   int `json:"meeting_minutes"`
	BreakMinutes     int `json:"break_minutes"`
	BufferMinutes    int `json:"buffer_minutes"`
	AvailableMinutes int `json:"available_minutes"`
	CommittedMinutes int `json:"committed_minutes"`
	RemainingMinutes int `json:"remaining_minutes"`
}

type dailyPlan struct {
	SchemaVersion string       `json:"schema_version"`
	Date          string       `json:"date"`
	Timezone      string       `json:"timezone,omitempty"`
	Revision      int          `json:"revision"`
	Mode          string       `json:"mode,omitempty"`
	Capacity      planCapacity `json:"capacity"`
	FocusItems    []string     `json:"focus_items,omitempty"`
	Blocks        []planBlock  `json:"blocks"`
	Risks         []planRisk   `json:"risks,omitempty"`
	Questions     []string     `json:"questions,omitempty"`
	GeneratedBy   string       `json:"generated_by,omitempty"`
	GeneratedAt   string       `json:"generated_at,omitempty"`
	AcceptedAt    string       `json:"accepted_at,omitempty"`
	UpdatedAt     string       `json:"updated_at,omitempty"`
}

type planValidation struct {
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
}

func planPath(root, date string) string {
	return filepath.Join(root, "plans", date+".json")
}

func defaultPlan(date string) dailyPlan {
	return dailyPlan{
		SchemaVersion: planSchemaVersion,
		Date:          date,
		Timezone:      time.Local.String(),
		Revision:      0,
		Mode:          "balanced",
		Capacity:      planCapacity{WorkMinutes: 480},
		Blocks:        []planBlock{},
		Risks:         []planRisk{},
		Questions:     []string{},
	}
}

func readPlan(root, date string) (dailyPlan, error) {
	body, err := os.ReadFile(planPath(root, date))
	if errors.Is(err, os.ErrNotExist) {
		return defaultPlan(date), nil
	}
	if err != nil {
		return dailyPlan{}, errors.New("计划读取失败")
	}
	var plan dailyPlan
	if err := jsonUnmarshal(body, &plan); err != nil {
		return dailyPlan{}, errors.New("计划不是合法 JSON")
	}
	if strings.TrimSpace(plan.Date) == "" {
		plan.Date = date
	}
	if plan.Blocks == nil {
		plan.Blocks = []planBlock{}
	}
	if plan.Risks == nil {
		plan.Risks = []planRisk{}
	}
	if plan.Questions == nil {
		plan.Questions = []string{}
	}
	return plan, nil
}

func writePlan(root string, plan dailyPlan) error {
	if err := os.MkdirAll(filepath.Join(root, "plans"), 0o755); err != nil {
		return fmt.Errorf("计划目录创建失败：%w", err)
	}
	plan.SchemaVersion = planSchemaVersion
	payload, err := jsonMarshalIndent(plan)
	if err != nil {
		return errors.New("计划序列化失败")
	}
	if err := writeFileAtomic(planPath(root, plan.Date), append(payload, '\n')); err != nil {
		return fmt.Errorf("计划写入失败：%w", err)
	}
	return nil
}

func parseClock(value string) (int, bool) {
	value = strings.TrimSpace(value)
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return 0, false
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil || hour < 0 || hour > 23 {
		return 0, false
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil || minute < 0 || minute > 59 {
		return 0, false
	}
	return hour*60 + minute, true
}

func planBlockMinutes(block planBlock) int {
	start, ok1 := parseClock(block.Start)
	end, ok2 := parseClock(block.End)
	if !ok1 || !ok2 || end <= start {
		return 0
	}
	return end - start
}

func planBlockLabel(block planBlock) string {
	return block.Start + "-" + block.End + " " + block.Title
}

func normalizePlan(root string, plan dailyPlan, incrementRevision bool) (dailyPlan, planValidation) {
	validation := planValidation{Errors: []string{}, Warnings: []string{}}
	plan.Date = normalizeDate(plan.Date)
	if !validDate(plan.Date) {
		validation.Errors = append(validation.Errors, "计划日期必须是 YYYY-MM-DD")
		return plan, validation
	}
	if strings.TrimSpace(plan.Timezone) == "" {
		plan.Timezone = time.Local.String()
	}
	if strings.TrimSpace(plan.Mode) == "" {
		plan.Mode = "balanced"
	}
	if plan.Capacity.WorkMinutes <= 0 {
		plan.Capacity.WorkMinutes = 480
	}
	if plan.Capacity.WorkMinutes > 1440 {
		validation.Errors = append(validation.Errors, "每日可用工时不能超过 1440 分钟")
	}
	if plan.Blocks == nil {
		plan.Blocks = []planBlock{}
	}
	if plan.Risks == nil {
		plan.Risks = []planRisk{}
	}
	if plan.Questions == nil {
		plan.Questions = []string{}
	}

	items, _ := readWorkItems(root)
	itemIndex := map[string]workItem{}
	for _, item := range items.Items {
		itemIndex[item.ID] = item
	}

	blocks := make([]planBlock, 0, len(plan.Blocks))
	seenIDs := map[string]bool{}
	for idx, block := range plan.Blocks {
		block.Kind = strings.TrimSpace(block.Kind)
		if block.Kind == "" {
			block.Kind = "focus"
		}
		if !planBlockKinds[block.Kind] {
			validation.Errors = append(validation.Errors, "未知时间块类型："+block.Kind)
			continue
		}
		block.Status = strings.TrimSpace(block.Status)
		if block.Status == "" {
			block.Status = "planned"
		}
		if !planBlockStatuses[block.Status] {
			validation.Errors = append(validation.Errors, "未知时间块状态："+block.Status)
			continue
		}
		block.Start = strings.TrimSpace(block.Start)
		block.End = strings.TrimSpace(block.End)
		start, okStart := parseClock(block.Start)
		end, okEnd := parseClock(block.End)
		if !okStart || !okEnd || end <= start {
			validation.Errors = append(validation.Errors, "时间块起止时间无效："+planBlockLabel(block))
			continue
		}
		if block.WorkItemID != "" {
			if item, ok := itemIndex[block.WorkItemID]; ok {
				if strings.TrimSpace(block.Title) == "" {
					block.Title = item.Title
				}
				if strings.TrimSpace(block.ProjectID) == "" {
					block.ProjectID = item.ProjectID
				}
			} else {
				validation.Warnings = append(validation.Warnings, "时间块引用了不存在的工作项："+block.WorkItemID)
			}
		}
		block.Title = strings.TrimSpace(block.Title)
		if block.Title == "" {
			block.Title = planBlockKindLabel(block.Kind)
		}
		block.Source = strings.TrimSpace(block.Source)
		if block.Source == "" {
			block.Source = "manual"
		}
		block.ID = strings.TrimSpace(block.ID)
		if block.ID == "" {
			block.ID = "blk-" + shortHash(plan.Date+"|"+strconv.Itoa(idx)+"|"+block.Start+"|"+block.Title)
		}
		if seenIDs[block.ID] {
			block.ID = block.ID + "-" + strconv.Itoa(idx)
		}
		seenIDs[block.ID] = true
		blocks = append(blocks, block)
	}

	sort.SliceStable(blocks, func(i, j int) bool {
		li, _ := parseClock(blocks[i].Start)
		lj, _ := parseClock(blocks[j].Start)
		return li < lj
	})
	plan.Blocks = blocks

	capacity := planCapacity{WorkMinutes: plan.Capacity.WorkMinutes}
	committed := 0
	meeting := 0
	breakMinutes := 0
	buffer := 0
	for _, block := range plan.Blocks {
		minutes := planBlockMinutes(block)
		switch block.Kind {
		case "focus", "admin":
			committed += minutes
		case "meeting", "personal":
			meeting += minutes
		case "break":
			breakMinutes += minutes
		case "buffer":
			buffer += minutes
		}
	}
	capacity.MeetingMinutes = meeting
	capacity.BreakMinutes = breakMinutes
	capacity.BufferMinutes = buffer
	capacity.CommittedMinutes = committed
	capacity.AvailableMinutes = capacity.WorkMinutes - meeting - breakMinutes
	capacity.RemainingMinutes = capacity.AvailableMinutes - committed - buffer
	plan.Capacity = capacity
	plan.FocusItems = plan.FocusItems[:0]
	for _, block := range plan.Blocks {
		if block.Kind == "focus" && block.WorkItemID != "" {
			found := false
			for _, id := range plan.FocusItems {
				if id == block.WorkItemID {
					found = true
					break
				}
			}
			if !found {
				plan.FocusItems = append(plan.FocusItems, block.WorkItemID)
			}
		}
	}

	// 重叠检查：时间块之间不允许重叠；相邻允许。
	intervals := make([]struct {
		start int
		end   int
		label string
	}, 0, len(plan.Blocks))
	for _, block := range plan.Blocks {
		start, _ := parseClock(block.Start)
		end, _ := parseClock(block.End)
		intervals = append(intervals, struct {
			start int
			end   int
			label string
		}{start: start, end: end, label: planBlockLabel(block)})
	}
	sort.SliceStable(intervals, func(i, j int) bool { return intervals[i].start < intervals[j].start })
	for i := 1; i < len(intervals); i++ {
		if intervals[i].start < intervals[i-1].end {
			validation.Errors = append(validation.Errors, "时间块重叠："+intervals[i-1].label+" / "+intervals[i].label)
		}
	}
	if capacity.RemainingMinutes < 0 {
		validation.Warnings = append(validation.Warnings, "计划已超出可用容量 "+strconv.Itoa(-capacity.RemainingMinutes)+" 分钟")
	}

	if incrementRevision {
		if plan.Revision < 1 {
			plan.Revision = 1
		} else {
			plan.Revision++
		}
	}
	plan.UpdatedAt = time.Now().Format(time.RFC3339)
	return plan, validation
}

func planBlockKindLabel(kind string) string {
	switch kind {
	case "focus":
		return "专注"
	case "meeting":
		return "会议"
	case "admin":
		return "行政"
	case "break":
		return "休息"
	case "buffer":
		return "缓冲"
	case "personal":
		return "个人事务"
	default:
		return kind
	}
}

func planGetCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	date := normalizeDate(strings.TrimSpace(in.Date))
	if !validDate(date) {
		return nil, errors.New("需要 date（YYYY-MM-DD）")
	}
	plan, err := readPlan(root, date)
	if err != nil {
		return nil, err
	}
	plan, validation := normalizePlan(root, plan, false)
	return map[string]any{
		"log_root": root,
		"plan":     plan,
		"warnings": validation.Warnings,
	}, nil
}

func planSaveCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	if in.Plan == nil {
		return nil, errors.New("需要 plan")
	}
	plan := *in.Plan
	if strings.TrimSpace(plan.Date) == "" {
		plan.Date = strings.TrimSpace(in.Date)
	}
	unlock := lockRoot(root)
	defer unlock()
	if existing, err := readPlan(root, normalizeDate(plan.Date)); err == nil && existing.Revision > plan.Revision {
		plan.Revision = existing.Revision
	}
	normalized, validation := normalizePlan(root, plan, true)
	normalized.SchemaVersion = planSchemaVersion
	if len(validation.Errors) > 0 {
		return nil, errors.New(strings.Join(validation.Errors, "；"))
	}
	for _, warning := range validation.Warnings {
		found := false
		for _, risk := range normalized.Risks {
			if risk.Message == warning {
				found = true
				break
			}
		}
		if !found {
			normalized.Risks = append(normalized.Risks, planRisk{Level: "warning", Message: warning})
		}
	}
	if err := writePlan(root, normalized); err != nil {
		return nil, err
	}
	return map[string]any{
		"log_root": root,
		"plan":     normalized,
		"warnings": validation.Warnings,
	}, nil
}

func planValidateCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	if in.Plan == nil {
		return nil, errors.New("需要 plan")
	}
	_, validation := normalizePlan(root, *in.Plan, false)
	return map[string]any{
		"valid":    len(validation.Errors) == 0,
		"errors":   validation.Errors,
		"warnings": validation.Warnings,
	}, nil
}

func planListCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	from := normalizeDate(strings.TrimSpace(in.From))
	to := normalizeDate(strings.TrimSpace(in.To))
	if !validDate(from) || !validDate(to) {
		return nil, errors.New("需要 from / to（YYYY-MM-DD）")
	}
	entries, err := os.ReadDir(filepath.Join(root, "plans"))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{"log_root": root, "count": 0, "plans": []dailyPlan{}}, nil
	}
	if err != nil {
		return nil, errors.New("计划目录读取失败")
	}
	plans := make([]dailyPlan, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			continue
		}
		date := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if date < from || date > to {
			continue
		}
		plan, err := readPlan(root, date)
		if err != nil {
			continue
		}
		plan, _ = normalizePlan(root, plan, false)
		plans = append(plans, plan)
	}
	sort.SliceStable(plans, func(i, j int) bool { return plans[i].Date < plans[j].Date })
	return map[string]any{
		"log_root": root,
		"count":    len(plans),
		"plans":    plans,
	}, nil
}

// jsonUnmarshal / jsonMarshalIndent 集中处理计划 JSON，避免各文件重复 import encoding/json。
func jsonUnmarshal(body []byte, value any) error {
	return json.Unmarshal(body, value)
}

func jsonMarshalIndent(value any) ([]byte, error) {
	return json.MarshalIndent(value, "", "  ")
}
