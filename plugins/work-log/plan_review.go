package main

import (
	"errors"
	"strings"
	"time"
)

// 计划复盘：实际完成情况以当日结束后的日志为准。
// 当天只返回 pending；历史日期用当天日志按项目投入分配，得到每个时间块的
// 完成/部分完成/未完成，避免白天就开始“结算”造成误判。

type planReviewBlock struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Kind           string `json:"kind"`
	PlannedMinutes int    `json:"planned_minutes"`
	ActualMinutes  int    `json:"actual_minutes"`
	Status         string `json:"status"` // pending / done / partial / skipped
	Counted        bool   `json:"counted"`
	Evidence       string `json:"evidence,omitempty"`
}

type planReview struct {
	Date              string            `json:"date"`
	Finalized         bool              `json:"finalized"`
	Note              string            `json:"note"`
	PlannedMinutes    int               `json:"planned_minutes"`
	ActualMinutes     int               `json:"actual_minutes"`
	CompletionPercent int               `json:"completion_percent"`
	Blocks            []planReviewBlock `json:"blocks"`
}

func planReviewCap(in input) (any, error) {
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
	today := time.Now().Format("2006-01-02")
	review := planReview{Date: date, Blocks: []planReviewBlock{}}
	for _, block := range plan.Blocks {
		planned := planBlockMinutes(block)
		item := planReviewBlock{
			ID:             block.ID,
			Title:          block.Title,
			Kind:           block.Kind,
			PlannedMinutes: planned,
			Status:         "pending",
		}
		review.Blocks = append(review.Blocks, item)
	}
	if date >= today {
		review.Note = "当日结束后按日志结算实际完成"
		return map[string]any{"log_root": root, "review": review}, nil
	}
	review.Finalized = true
	review.Note = "已按当日日志结算"

	pf, err := readProjects(root)
	if err != nil {
		return nil, err
	}
	projectNameByID := map[string]string{}
	projectIDByName := map[string]string{}
	for _, p := range pf.Projects {
		projectNameByID[p.ID] = p.Name
		projectIDByName[normalizeProjectKey(p.Name)] = p.ID
	}
	itemsFile, err := readWorkItems(root)
	if err != nil {
		return nil, err
	}
	itemByID := map[string]workItem{}
	for _, item := range itemsFile.Items {
		itemByID[item.ID] = item
	}
	entries, err := collectEntries(root, date, date)
	if err != nil {
		return nil, err
	}
	actualByProject := map[string]int{}
	for _, entry := range entries {
		pid := strings.TrimSpace(entry.ProjectID)
		if pid == "" {
			pid = projectIDByName[normalizeProjectKey(entry.Project)]
		}
		if pid == "" {
			continue
		}
		actualByProject[pid] += entryMinutes(entry)
	}

	plannedTotal := 0
	actualTotal := 0
	for i := range review.Blocks {
		block := review.Blocks[i]
		if block.Kind != "focus" && block.Kind != "admin" {
			block.Status = "done"
			block.ActualMinutes = block.PlannedMinutes
			block.Evidence = "固定时间块，不计入工作完成率"
			review.Blocks[i] = block
			continue
		}
		block.Counted = true
		plannedTotal += block.PlannedMinutes
		var source planBlock
		for _, candidate := range plan.Blocks {
			if candidate.ID == block.ID {
				source = candidate
				break
			}
		}
		pid := strings.TrimSpace(source.ProjectID)
		workItemID := strings.TrimSpace(source.WorkItemID)
		if pid == "" && workItemID != "" {
			if item, ok := itemByID[workItemID]; ok {
				pid = item.ProjectID
			}
		}
		if pid == "" {
			for _, item := range itemsFile.Items {
				if strings.TrimSpace(item.Title) == strings.TrimSpace(block.Title) {
					pid = item.ProjectID
					break
				}
			}
		}
		if pid == "" {
			pid = projectIDByName[normalizeProjectKey(block.Title)]
		}
		allocated := 0
		if pid != "" {
			allocated = actualByProject[pid]
			if allocated > block.PlannedMinutes {
				allocated = block.PlannedMinutes
			}
		}
		if workItemID != "" {
			if item, ok := itemByID[workItemID]; ok && item.Status == "done" && strings.TrimSpace(item.CompletedAt) != "" {
				if completed, ok := parseTimeAny(item.CompletedAt); ok && completed.Format("2006-01-02") == date {
					allocated = block.PlannedMinutes
				}
			}
		}
		if pid != "" {
			actualByProject[pid] -= allocated
			if actualByProject[pid] < 0 {
				actualByProject[pid] = 0
			}
		}
		block.ActualMinutes = allocated
		actualTotal += allocated
		switch {
		case block.PlannedMinutes > 0 && allocated*100 >= block.PlannedMinutes*80:
			block.Status = "done"
			block.Evidence = "当日日志投入已覆盖计划"
		case allocated > 0:
			block.Status = "partial"
			block.Evidence = "当日日志投入部分覆盖计划"
		default:
			block.Status = "skipped"
			block.Evidence = "当日无对应项目日志"
		}
		review.Blocks[i] = block
	}
	review.PlannedMinutes = plannedTotal
	review.ActualMinutes = actualTotal
	if plannedTotal > 0 {
		review.CompletionPercent = int(float64(actualTotal) / float64(plannedTotal) * 100)
		if review.CompletionPercent > 100 {
			review.CompletionPercent = 100
		}
	}
	return map[string]any{"log_root": root, "review": review}, nil
}
