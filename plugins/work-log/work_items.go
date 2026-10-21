package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// 本文件实现「工作项」：项目下可执行的最小规划单位。
// 工作项不等于完整任务系统，只承载标题、预计/剩余/实际耗时、截止日期、依赖等
// 规划所需字段；日志仍然是实际发生事实的唯一来源。

const workItemsSchemaVersion = "work-log-work-items.v1"

type workItem struct {
	ID                 string   `json:"id"`
	ProjectID          string   `json:"project_id,omitempty"`
	Project            string   `json:"project,omitempty"`
	Feature            string   `json:"feature,omitempty"`
	Exhibit            string   `json:"exhibit,omitempty"`
	Title              string   `json:"title"`
	Status             string   `json:"status"`
	Priority           string   `json:"priority"`
	EstimateMinutes    int      `json:"estimate_minutes,omitempty"`
	RemainingMinutes   int      `json:"remaining_minutes,omitempty"`
	ActualMinutes      int      `json:"actual_minutes,omitempty"`
	EstimateConfidence string   `json:"estimate_confidence,omitempty"`
	EstimateSource     string   `json:"estimate_source,omitempty"`
	DueDate            string   `json:"due_date,omitempty"`
	ScheduledFor       string   `json:"scheduled_for,omitempty"`
	DependsOn          []string `json:"depends_on,omitempty"`
	Tags               []string `json:"tags,omitempty"`
	Source             string   `json:"source,omitempty"`
	Notes              string   `json:"notes,omitempty"`
	CreatedAt          string   `json:"created_at"`
	UpdatedAt          string   `json:"updated_at"`
	CompletedAt        string   `json:"completed_at,omitempty"`
}

type workItemsFile struct {
	SchemaVersion string     `json:"schema_version"`
	UpdatedAt     string     `json:"updated_at"`
	Items         []workItem `json:"items"`
}

func workItemsPath(root string) string {
	return filepath.Join(root, "work-items.json")
}

func readWorkItems(root string) (workItemsFile, error) {
	body, err := os.ReadFile(workItemsPath(root))
	if errors.Is(err, os.ErrNotExist) {
		return workItemsFile{SchemaVersion: workItemsSchemaVersion, Items: []workItem{}}, nil
	}
	if err != nil {
		return workItemsFile{}, errors.New("工作项读取失败")
	}
	var file workItemsFile
	if err := json.Unmarshal(body, &file); err != nil {
		return workItemsFile{}, errors.New("工作项不是合法 JSON")
	}
	migrateWorkItemsFile(&file)
	return file, nil
}

func writeWorkItems(root string, file workItemsFile) error {
	file.SchemaVersion = workItemsSchemaVersion
	file.UpdatedAt = time.Now().Format(time.RFC3339)
	if file.Items == nil {
		file.Items = []workItem{}
	}
	payload, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return errors.New("工作项序列化失败")
	}
	if err := writeFileAtomic(workItemsPath(root), append(payload, '\n')); err != nil {
		return errors.New("工作项写入失败")
	}
	return nil
}

func migrateWorkItemsFile(file *workItemsFile) {
	file.SchemaVersion = workItemsSchemaVersion
	if file.Items == nil {
		file.Items = []workItem{}
	}
	for i := range file.Items {
		item := &file.Items[i]
		if strings.TrimSpace(item.Status) == "" {
			item.Status = "todo"
		}
		if strings.TrimSpace(item.Priority) == "" {
			item.Priority = priorityP2
		}
		if item.EstimateConfidence == "" {
			item.EstimateConfidence = "medium"
		}
		if item.EstimateSource == "" {
			item.EstimateSource = "manual"
		}
		if item.DependsOn == nil {
			item.DependsOn = []string{}
		}
		if item.Tags == nil {
			item.Tags = []string{}
		}
	}
}

func validWorkItemStatus(value string) bool {
	switch strings.TrimSpace(value) {
	case "todo", "in_progress", "blocked", "done", "cancelled":
		return true
	default:
		return false
	}
}

func validEstimateConfidence(value string) bool {
	switch strings.TrimSpace(value) {
	case "low", "medium", "high":
		return true
	default:
		return false
	}
}

func validEstimateSource(value string) bool {
	switch strings.TrimSpace(value) {
	case "manual", "historical", "ai":
		return true
	default:
		return false
	}
}

func workItemStatusRank(status string) int {
	switch status {
	case "in_progress":
		return 0
	case "blocked":
		return 1
	case "todo":
		return 2
	case "done":
		return 3
	case "cancelled":
		return 4
	default:
		return 5
	}
}

func makeWorkItemID(item workItem) string {
	basis := item.ProjectID + "|" + strings.TrimSpace(item.Title) + "|" + item.CreatedAt
	return "wi-" + shortHash(basis)
}

// resolveWorkItemProject 把工作项归到登记表里的项目；项目不存在时自动登记。
func resolveWorkItemProject(root string, pf *projectsFile, item *workItem) error {
	index := projectIndex(*pf)
	at := -1
	if id := strings.TrimSpace(item.ProjectID); id != "" {
		for i := range pf.Projects {
			if pf.Projects[i].ID == id {
				at = i
				break
			}
		}
		if at < 0 {
			return errors.New("未找到工作项所属项目 ID：" + id)
		}
	}
	if at < 0 {
		name := strings.TrimSpace(item.Project)
		if name == "" {
			return errors.New("工作项需要 project 或 project_id")
		}
		if canonical, ok := resolveProjectName(index, name); ok {
			name = canonical
		}
		at = projectIndexByName(*pf, name)
		if at < 0 {
			if registerProject(pf, index, name) {
				if err := writeProjects(root, *pf); err != nil {
					return err
				}
			}
			at = projectIndexByName(*pf, name)
		}
		if at < 0 {
			return errors.New("无法登记工作项所属项目：" + name)
		}
	}
	item.ProjectID = pf.Projects[at].ID
	item.Project = pf.Projects[at].Name
	return nil
}

func normalizeWorkItem(root string, pf *projectsFile, item workItem, now string) (workItem, error) {
	if err := resolveWorkItemProject(root, pf, &item); err != nil {
		return item, err
	}
	item.Title = strings.TrimSpace(item.Title)
	if item.Title == "" {
		return item, errors.New("工作项标题不能为空")
	}
	item.Status = strings.TrimSpace(item.Status)
	if item.Status == "" {
		item.Status = "todo"
	}
	if !validWorkItemStatus(item.Status) {
		return item, errors.New("无效的工作项状态：" + item.Status)
	}
	item.Priority = normalizePriority(item.Priority)
	if item.EstimateMinutes < 0 {
		item.EstimateMinutes = 0
	}
	if item.RemainingMinutes < 0 {
		item.RemainingMinutes = 0
	}
	if item.ActualMinutes < 0 {
		item.ActualMinutes = 0
	}

	item.EstimateConfidence = strings.TrimSpace(item.EstimateConfidence)
	if item.EstimateConfidence == "" {
		item.EstimateConfidence = "medium"
	}
	if !validEstimateConfidence(item.EstimateConfidence) {
		return item, errors.New("无效的估算置信度：" + item.EstimateConfidence)
	}
	item.EstimateSource = strings.TrimSpace(item.EstimateSource)
	if item.EstimateSource == "" {
		item.EstimateSource = "manual"
	}
	if !validEstimateSource(item.EstimateSource) {
		return item, errors.New("无效的估算来源：" + item.EstimateSource)
	}
	if item.DueDate != "" && !validDate(item.DueDate) {
		return item, errors.New("due_date 必须是 YYYY-MM-DD")
	}
	if item.ScheduledFor != "" && !validDate(item.ScheduledFor) {
		return item, errors.New("scheduled_for 必须是 YYYY-MM-DD")
	}
	item.DependsOn = trimList(item.DependsOn)
	item.Tags = trimList(item.Tags)
	item.Feature = strings.TrimSpace(item.Feature)
	item.Exhibit = strings.TrimSpace(item.Exhibit)
	item.Notes = strings.TrimSpace(item.Notes)
	if item.Source == "" {
		item.Source = "manual"
	}
	if item.CreatedAt == "" {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	if item.ID == "" {
		item.ID = makeWorkItemID(item)
	}
	if item.Status == "done" {
		if item.CompletedAt == "" {
			item.CompletedAt = now
		}
		item.RemainingMinutes = 0
	} else {
		item.CompletedAt = ""
	}
	if item.Status == "cancelled" {
		item.RemainingMinutes = 0
	}
	return item, nil
}

func workItemMatches(item workItem, in input) bool {
	if project := strings.TrimSpace(in.Project); project != "" {
		if !strings.EqualFold(item.Project, project) && !strings.EqualFold(item.ProjectID, project) {
			return false
		}
	}
	status := strings.TrimSpace(in.Status)
	if status != "" && !strings.EqualFold(status, "all") {
		if strings.EqualFold(status, "open") {
			if item.Status == "done" || item.Status == "cancelled" {
				return false
			}
		} else if !strings.EqualFold(item.Status, status) {
			return false
		}
	}
	if priority := strings.TrimSpace(in.Priority); priority != "" && !strings.EqualFold(priority, "all") {
		if !strings.EqualFold(item.Priority, priority) {
			return false
		}
	}
	query := strings.ToLower(strings.TrimSpace(in.Query))
	if query != "" {
		haystack := strings.ToLower(strings.Join([]string{item.Title, item.Project, item.Feature, item.Notes, strings.Join(item.Tags, " ")}, " "))
		if !strings.Contains(haystack, query) {
			return false
		}
	}
	return true
}

func workItemListCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	file, err := readWorkItems(root)
	if err != nil {
		return nil, err
	}
	includeDone := true
	if in.IncludeDone != nil {
		includeDone = *in.IncludeDone
	}
	items := make([]workItem, 0, len(file.Items))
	for _, item := range file.Items {
		if !includeDone && (item.Status == "done" || item.Status == "cancelled") {
			continue
		}
		if !workItemMatches(item, in) {
			continue
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		ls, rs := workItemStatusRank(items[i].Status), workItemStatusRank(items[j].Status)
		if ls != rs {
			return ls < rs
		}
		lp, rp := priorityRank(items[i].Priority), priorityRank(items[j].Priority)
		if lp != rp {
			return lp < rp
		}
		if items[i].DueDate != items[j].DueDate {
			if items[i].DueDate == "" {
				return false
			}
			if items[j].DueDate == "" {
				return true
			}
			return items[i].DueDate < items[j].DueDate
		}
		return items[i].UpdatedAt > items[j].UpdatedAt
	})
	return map[string]any{
		"log_root": root,
		"count":    len(items),
		"items":    items,
	}, nil
}

func workItemUpsertCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	if in.WorkItem == nil {
		return nil, errors.New("需要 work_item")
	}
	unlock := lockRoot(root)
	defer unlock()
	pf, err := readProjects(root)
	if err != nil {
		return nil, err
	}
	now := time.Now().Format(time.RFC3339)
	item, err := normalizeWorkItem(root, &pf, *in.WorkItem, now)
	if err != nil {
		return nil, err
	}
	file, err := readWorkItems(root)
	if err != nil {
		return nil, err
	}
	if item.RemainingMinutes == 0 && item.Status != "done" && item.Status != "cancelled" && item.EstimateMinutes > 0 {
		item.RemainingMinutes = item.EstimateMinutes
	}
	created := true
	for i := range file.Items {
		if file.Items[i].ID == item.ID {
			item.CreatedAt = file.Items[i].CreatedAt
			file.Items[i] = item
			created = false
			break
		}
	}
	if created {
		file.Items = append(file.Items, item)
	}
	if err := writeWorkItems(root, file); err != nil {
		return nil, err
	}
	return map[string]any{
		"log_root": root,
		"created":  created,
		"item":     item,
	}, nil
}

func workItemUpdateCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	id := strings.TrimSpace(in.WorkItemID)
	if id == "" {
		return nil, errors.New("需要 work_item_id")
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
	file, err := readWorkItems(root)
	if err != nil {
		return nil, err
	}
	at := -1
	for i := range file.Items {
		if file.Items[i].ID == id {
			at = i
			break
		}
	}
	if at < 0 {
		return nil, errors.New("未找到工作项：" + id)
	}
	item := file.Items[at]
	now := time.Now().Format(time.RFC3339)
	for key, raw := range in.Patch {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "title":
			item.Title = asText(raw)
		case "project_id":
			item.ProjectID = asText(raw)
		case "project":
			item.Project = asText(raw)
			item.ProjectID = ""
		case "feature":
			item.Feature = asText(raw)
		case "exhibit":
			item.Exhibit = asText(raw)
		case "status":
			item.Status = asText(raw)
		case "priority":
			item.Priority = strings.ToUpper(asText(raw))
		case "estimate_minutes":
			item.EstimateMinutes = asInt(raw)
		case "remaining_minutes":
			item.RemainingMinutes = asInt(raw)
		case "actual_minutes":
			item.ActualMinutes = asInt(raw)
		case "estimate_confidence":
			item.EstimateConfidence = asText(raw)
		case "estimate_source":
			item.EstimateSource = asText(raw)
		case "due_date":
			item.DueDate = asText(raw)
		case "scheduled_for":
			item.ScheduledFor = asText(raw)
		case "depends_on":
			item.DependsOn = asTextList(raw)
		case "tags":
			item.Tags = asTextList(raw)
		case "notes":
			item.Notes = asText(raw)
		default:
			return nil, errors.New("不支持的字段：" + key)
		}
	}
	normalized, err := normalizeWorkItem(root, &pf, item, now)
	if err != nil {
		return nil, err
	}
	normalized.ID = id
	normalized.CreatedAt = item.CreatedAt
	file.Items[at] = normalized
	if err := writeWorkItems(root, file); err != nil {
		return nil, err
	}
	return map[string]any{
		"log_root": root,
		"item":     normalized,
	}, nil
}

func workItemDeleteCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	wanted := map[string]bool{}
	for _, id := range in.WorkItemIDs {
		if id = strings.TrimSpace(id); id != "" {
			wanted[id] = true
		}
	}
	if id := strings.TrimSpace(in.WorkItemID); id != "" {
		wanted[id] = true
	}
	if len(wanted) == 0 {
		return nil, errors.New("需要 work_item_id 或 work_item_ids")
	}
	unlock := lockRoot(root)
	defer unlock()
	file, err := readWorkItems(root)
	if err != nil {
		return nil, err
	}
	kept := make([]workItem, 0, len(file.Items))
	removed := make([]workItem, 0, len(wanted))
	for _, item := range file.Items {
		if wanted[item.ID] {
			removed = append(removed, item)
			continue
		}
		kept = append(kept, item)
	}
	file.Items = kept
	if err := writeWorkItems(root, file); err != nil {
		return nil, err
	}
	return map[string]any{
		"log_root": root,
		"count":    len(removed),
		"removed":  removed,
	}, nil
}
