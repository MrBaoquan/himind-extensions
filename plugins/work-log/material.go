package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/MrBaoquan/himind-extensions/sdk/capability"
	"github.com/MrBaoquan/himind-extensions/sdk/jsonrpc"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	_ "time/tzdata"
	"unicode/utf8"
)

// Material is derived only from Agent-owned facts. This package never reads
// client JSONL/SQLite files and has no legacy collection backend.
type materialInput struct {
	LogRoot            string   `json:"log_root"`
	MaterialRoot       string   `json:"material_root"`
	From               string   `json:"from"`
	To                 string   `json:"to"`
	Date               string   `json:"date"`
	LookbackDays       int      `json:"lookback_days"`
	Timezone           string   `json:"timezone"`
	Source             string   `json:"source"`
	Sources            []string `json:"sources"`
	Scope              string   `json:"scope"`
	WorkspaceProjectID string   `json:"workspace_project_id"`
	IncludeChildren    bool     `json:"include_children"`
	Limit              int      `json:"limit"`
	MaxPrompts         int      `json:"max_prompts"`
	MaxChars           int      `json:"max_chars"`
	Strict             bool     `json:"strict"`
	ExcludePaths       []string `json:"exclude_paths"`
	ExcludeKeywords    []string `json:"exclude_keywords"`
}

type materialSource struct {
	Source       string `json:"source"`
	Availability string `json:"availability"`
	ErrorCode    string `json:"error_code"`
}

func materialCap(request jsonrpc.Request) (any, *jsonrpc.Error) {
	var in materialInput
	if e := jsonrpc.DecodeParams(request, &in); e != nil {
		return nil, e
	}
	if request.Method == "work-log.material.sources" {
		if !capability.Available() {
			return rpcResult(nil, errors.New("Agent 会话服务不可用，请通过 Agent 调用工作日志"))
		}
		var result map[string]any
		err := capability.Invoke(context.Background(), "workspace.session.sources", materialScope(in), &result)
		return rpcResult(result, err)
	}
	return rpcResult(materialCollect(in))
}
func materialScope(in materialInput) map[string]any {
	q := map[string]any{"scope": "all"}
	if in.Scope != "" {
		q["scope"] = in.Scope
	}
	if in.WorkspaceProjectID != "" {
		q["scope"] = "project"
		q["project_id"] = in.WorkspaceProjectID
		q["include_children"] = in.IncludeChildren
	}
	return q
}
func materialWindow(in materialInput) (time.Time, time.Time, string, string, error) {
	zone := time.Local
	if in.Timezone != "" {
		var err error
		zone, err = time.LoadLocation(in.Timezone)
		if err != nil {
			return time.Time{}, time.Time{}, "", "", errors.New("无效时区")
		}
	}
	from, to := in.From, in.To
	if in.Date != "" {
		if from == "" {
			from = in.Date
		}
		if to == "" {
			to = in.Date
		}
	}
	if from == "" && to == "" {
		days := in.LookbackDays
		if days == 0 {
			days = 2
		}
		if days < 1 || days > 31 {
			return time.Time{}, time.Time{}, "", "", errors.New("回看天数必须为 1–31")
		}
		now := time.Now().In(zone)
		from = now.AddDate(0, 0, -days+1).Format("2006-01-02")
		to = now.Format("2006-01-02")
	}
	start, e1 := time.ParseInLocation("2006-01-02", from, zone)
	end, e2 := time.ParseInLocation("2006-01-02", to, zone)
	if e1 != nil || e2 != nil || end.Before(start) || end.Sub(start) > 30*24*time.Hour {
		return start, end, from, to, errors.New("日期范围必须为有效的 1–31 个自然日")
	}
	return start, end.AddDate(0, 0, 1), from, to, nil
}
func materialCollect(in materialInput) (any, error) {
	if !capability.Available() {
		return nil, errors.New("Agent 会话服务不可用；工作日志不再直接读取客户端存储")
	}
	start, end, from, to, err := materialWindow(in)
	if err != nil {
		return nil, err
	}
	if in.Limit == 0 {
		in.Limit = 500
	}
	if in.MaxPrompts == 0 {
		in.MaxPrompts = 12
	}
	if in.MaxChars == 0 {
		in.MaxChars = 8000
	}
	if in.Limit < 1 || in.Limit > 500 || in.MaxPrompts < 1 || in.MaxPrompts > 50 || in.MaxChars < 200 || in.MaxChars > 20000 {
		return nil, errors.New("素材条数或正文预算无效")
	}
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	var discovered struct {
		Sources []materialSource `json:"sources"`
	}
	if err = capability.Invoke(ctx, "workspace.session.sources", materialScope(in), &discovered); err != nil {
		return nil, fmt.Errorf("读取 Agent 来源失败：%w", err)
	}
	selected := map[string]bool{}
	for _, id := range in.Sources {
		selected[agentSourceID(id)] = true
	}
	if in.Source != "" && in.Source != "all" {
		selected[agentSourceID(in.Source)] = true
	}
	statuses := make([]materialSource, 0)
	known := map[string]bool{}
	for _, s := range discovered.Sources {
		known[s.Source] = true
		if len(selected) > 0 {
			if selected[s.Source] {
				statuses = append(statuses, s)
			}
		} else if s.Availability == "available" || s.Availability == "partial" || s.Availability == "error" {
			statuses = append(statuses, s)
		}
	}
	for id := range selected {
		if !known[id] {
			return nil, fmt.Errorf("Agent 不支持或未登记会话来源：%s", id)
		}
	}
	var digests []materialDigest
	warnings := []map[string]any{}
	complete := true
	calls := 1
	for _, source := range statuses {
		if source.Availability == "unavailable" || source.Availability == "error" {
			complete = false
			warnings = append(warnings, map[string]any{"source": source.Source, "reason": source.ErrorCode})
			continue
		}
		request := materialScope(in)
		request["sources"] = []string{source.Source}
		request["from"] = start.Format(time.RFC3339)
		request["to"] = end.Format(time.RFC3339)
		request["limit"] = 200
		request["max_chars"] = 8000
		var rows []agentActivity
		seen := map[string]bool{}
		done := false
		for calls < 240 {
			calls++
			var page agentPage
			if e := capability.Invoke(ctx, "workspace.session.activity", request, &page); e != nil {
				return nil, fmt.Errorf("Agent 活动读取失败（%s），没有切换到私有存储回退：%w", source.Source, e)
			}
			if page.SchemaVersion != "session_history.v1" {
				return nil, errors.New("Agent 会话响应版本不受支持")
			}
			for _, r := range page.Activities {
				key := r.Session.ID + "|" + r.Message.ID
				if !seen[key] {
					seen[key] = true
					rows = append(rows, r)
				}
			}
			if len(rows) > 20000 {
				return nil, errors.New("活动素材超出消息预算，请缩小日期或工作区范围")
			}
			if !page.Complete {
				complete = false
			}
			if page.Undated > 0 {
				warnings = append(warnings, map[string]any{"source": source.Source, "reason": "消息缺少活动时间，未伪造日期", "count": page.Undated})
			}
			warnings = append(warnings, page.Skipped...)
			if page.NextCursor == "" {
				done = true
				break
			}
			if request["cursor"] == page.NextCursor {
				return nil, errors.New("Agent 活动游标未推进")
			}
			request["cursor"] = page.NextCursor
		}
		if !done {
			return nil, errors.New("活动分页超出调用预算，请缩小日期或工作区范围")
		}
		digests = append(digests, agentDigests(rows, start.Location())...)
	}
	if in.Strict && !complete {
		return nil, errors.New("Agent 来源或消息不完整，严格模式未生成素材")
	}
	var privacy struct {
		ExcludePaths    []string `json:"exclude_paths"`
		ExcludeKeywords []string `json:"exclude_keywords"`
	}
	if data, e := os.ReadFile(filepath.Join(root, "config", "privacy.json")); e == nil {
		if e = json.Unmarshal(data, &privacy); e != nil {
			return nil, errors.New("日志隐私配置无效，停止生成素材")
		}
	} else if !os.IsNotExist(e) {
		return nil, errors.New("日志隐私配置不可读，停止生成素材")
	}
	privacy.ExcludePaths = append(privacy.ExcludePaths, in.ExcludePaths...)
	privacy.ExcludeKeywords = append(privacy.ExcludeKeywords, in.ExcludeKeywords...)
	filtered := make([]materialDigest, 0, len(digests))
	excluded := 0
	for _, d := range digests {
		blocked := false
		dir := normalizePathKey(d.Directory)
		for _, p := range privacy.ExcludePaths {
			k := normalizePathKey(p)
			if k != "" && (dir == k || strings.HasPrefix(dir, k+"/")) {
				blocked = true
			}
		}
		content := strings.ToLower(strings.Join(append(append([]string{}, d.UserPrompts...), d.AssistantExcerpts...), "\n"))
		for _, key := range privacy.ExcludeKeywords {
			if key != "" && (strings.Contains(content, strings.ToLower(key)) || d.Truncated) {
				blocked = true
			}
		}
		if blocked {
			excluded++
			continue
		}
		if len(d.UserPrompts) == 0 && len(d.AssistantExcerpts) == 0 {
			continue
		}
		d.RepoPath = d.Directory
		trimMaterialDigest(&d, in.MaxPrompts, in.MaxChars)
		filtered = append(filtered, d)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].StartedAt != filtered[j].StartedAt {
			return filtered[i].StartedAt > filtered[j].StartedAt
		}
		return filtered[i].Source+filtered[i].SessionID+filtered[i].Date < filtered[j].Source+filtered[j].SessionID+filtered[j].Date
	})
	truncated := len(filtered) > in.Limit
	if truncated {
		filtered = filtered[:in.Limit]
		complete = false
	}
	allocateAgentMinutes(filtered)
	result := map[string]any{"from": from, "to": to, "sessions": filtered, "entries": materialEntries(filtered), "total": len(filtered), "scope": materialScope(in), "privacy_filtered": len(privacy.ExcludePaths) > 0 || len(privacy.ExcludeKeywords) > 0, "provider": "agent", "complete": complete, "truncated": truncated, "warnings": warnings, "skipped": warnings, "source_statuses": statuses, "privacy_excluded": excluded, "timezone": start.Location().String(), "time_basis": "message_timestamp; activity intervals are estimates"}
	materialDir := in.MaterialRoot
	if materialDir == "" {
		materialDir, err = sessionMaterialDir("")
		if err != nil {
			return nil, err
		}
	}
	body, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return nil, err
	}
	if len(body) > 4*1024*1024 {
		return nil, errors.New("素材超过 4 MiB，请缩小范围或正文预算")
	}
	path := filepath.Join(materialDir, fmt.Sprintf("session-digest-%s_%s.json", from, to))
	if err = writeFileAtomic(path, body); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	result["artifacts"] = []map[string]any{{"artifact_id": "session-digest", "artifact_type": "session_digest", "name": "Agent 会话素材 " + from + " ~ " + to, "uri": fileURIPath(path), "sha256": hex.EncodeToString(sum[:])}}
	return result, nil
}
func agentSourceID(id string) string {
	if id == "claude-code" {
		return "claude_code"
	}
	return id
}

type agentSession struct {
	ID          string `json:"id"`
	Source      string `json:"source"`
	NativeID    string `json:"native_id"`
	ProjectID   string `json:"project_id"`
	ProjectName string `json:"project_name"`
	Directory   string `json:"working_directory"`
	Title       string `json:"title"`
}
type agentActivity struct {
	Session   agentSession `json:"session"`
	Kind      string       `json:"kind"`
	Truncated bool         `json:"truncated"`
	Message   struct {
		ID        string `json:"id"`
		Role      string `json:"role"`
		Content   string `json:"content"`
		Timestamp string `json:"timestamp"`
	} `json:"message"`
	Evidence map[string]any `json:"evidence"`
}
type agentPage struct {
	SchemaVersion string          `json:"schema_version"`
	Activities    []agentActivity `json:"activities"`
	Sources       []struct {
		Source       string `json:"source"`
		Availability string `json:"availability"`
		ErrorCode    string `json:"error_code"`
	} `json:"sources"`
	NextCursor string           `json:"next_cursor"`
	Complete   bool             `json:"complete"`
	Undated    int              `json:"undated_messages"`
	Skipped    []map[string]any `json:"skipped"`
}

type materialDigest struct {
	WorkspaceProjectID string             `json:"workspace_project_id,omitempty"`
	Provider           string             `json:"provider,omitempty"`
	MinutesMethod      string             `json:"minutes_method,omitempty"`
	Evidence           []map[string]any   `json:"evidence,omitempty"`
	EvidenceTruncated  bool               `json:"evidence_truncated,omitempty"`
	ActivityIntervals  []activityInterval `json:"activity_intervals,omitempty"`
	OverlapAdjusted    bool               `json:"overlap_adjusted,omitempty"`
	Source             string             `json:"source"`
	SessionID          string             `json:"session_id"`
	Project            string             `json:"project,omitempty"`
	Feature            string             `json:"feature,omitempty"`
	Directory          string             `json:"directory,omitempty"`
	// RepoPath 是会话工作目录对应的仓库根（路径事实）：优先取仓库自述清单 `.himind/project.json`
	// 所在目录，其次 git/svn 仓库根，最后退回工作目录本身。写入端据此稳定归类。
	RepoPath string `json:"repo_path,omitempty"`
	// Exhibit 是仓库自述清单里声明的展项（可选）。
	Exhibit           string   `json:"exhibit,omitempty"`
	Title             string   `json:"title,omitempty"`
	Date              string   `json:"date"`
	StartedAt         string   `json:"started_at,omitempty"`
	EndedAt           string   `json:"ended_at,omitempty"`
	Minutes           int      `json:"minutes,omitempty"`
	MinutesSource     string   `json:"minutes_source,omitempty"`
	TurnCount         int      `json:"turn_count"`
	UserPrompts       []string `json:"user_prompts"`
	AssistantExcerpts []string `json:"assistant_excerpts,omitempty"`
	Cost              float64  `json:"cost,omitempty"`
	TokensInput       int64    `json:"tokens_input,omitempty"`
	TokensOutput      int64    `json:"tokens_output,omitempty"`
	Ref               string   `json:"ref,omitempty"`
	Truncated         bool     `json:"truncated,omitempty"`
}

func legacyAgentSource(source string) string {
	if source == "claude_code" {
		return "claude-code"
	}
	return source
}

func agentDigests(rows []agentActivity, location *time.Location) []materialDigest {
	type bucket struct {
		digest materialDigest
		times  []time.Time
	}
	buckets := map[string]*bucket{}
	sort.SliceStable(rows, func(i, j int) bool {
		a, _ := time.Parse(time.RFC3339, rows[i].Message.Timestamp)
		b, _ := time.Parse(time.RFC3339, rows[j].Message.Timestamp)
		return a.Before(b)
	})
	for _, row := range rows {
		if row.Kind != "dialogue" || (row.Message.Role != "user" && row.Message.Role != "assistant") {
			continue
		}
		at, err := time.Parse(time.RFC3339, row.Message.Timestamp)
		if err != nil {
			continue
		}
		at = at.In(location)
		source := legacyAgentSource(row.Session.Source)
		date := at.Format("2006-01-02")
		key := source + "|" + row.Session.NativeID + "|" + date
		b := buckets[key]
		if b == nil {
			b = &bucket{digest: materialDigest{Source: source, SessionID: row.Session.NativeID, Date: date, Project: row.Session.ProjectName, Directory: row.Session.Directory, Title: row.Session.Title, WorkspaceProjectID: row.Session.ProjectID, Provider: "agent", MinutesSource: "estimated", MinutesMethod: "message_intervals_v1"}}
			buckets[key] = b
		}
		b.times = append(b.times, at)
		b.digest.Evidence = append(b.digest.Evidence, row.Evidence)
		b.digest.Truncated = b.digest.Truncated || row.Truncated
		if row.Message.Role == "user" {
			b.digest.UserPrompts = append(b.digest.UserPrompts, row.Message.Content)
			b.digest.TurnCount++
		} else {
			b.digest.AssistantExcerpts = append(b.digest.AssistantExcerpts, row.Message.Content)
		}
	}
	keys := make([]string, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var result []materialDigest
	for _, key := range keys {
		b := buckets[key]
		sort.Slice(b.times, func(i, j int) bool { return b.times[i].Before(b.times[j]) })
		b.digest.StartedAt = b.times[0].Format(time.RFC3339)
		b.digest.EndedAt = b.times[len(b.times)-1].Format(time.RFC3339)
		for i := 1; i < len(b.times); i++ {
			a, z := b.times[i-1], b.times[i]
			if !z.After(a) || z.Sub(a) > 20*time.Minute {
				continue
			}
			b.digest.ActivityIntervals = append(b.digest.ActivityIntervals, activityInterval{a.Format(time.RFC3339), z.Format(time.RFC3339)})
		}
		b.digest.Ref = fmt.Sprintf("himind-session:%s:%s", strings.ReplaceAll(b.digest.Source, "claude-code", "claude_code"), b.digest.SessionID)
		result = append(result, b.digest)
	}
	return result
}

// Allocate each selected Agent interval once. Legacy estimates lack comparable interval
// evidence and are deliberately not folded into this calculation.

func allocateAgentMinutes(digests []materialDigest) {
	type span struct{ from, to time.Time }
	var occupied []span
	indexes := make([]int, 0, len(digests))
	for i := range digests {
		if digests[i].Provider == "agent" {
			indexes = append(indexes, i)
		}
	}
	sort.Slice(indexes, func(i, j int) bool {
		a, b := digests[indexes[i]], digests[indexes[j]]
		return a.Source+"|"+a.SessionID+"|"+a.Date < b.Source+"|"+b.SessionID+"|"+b.Date
	})
	for _, i := range indexes {
		d := &digests[i]
		seconds := 0.0
		d.OverlapAdjusted = false
		for _, interval := range d.ActivityIntervals {
			a, ea := time.Parse(time.RFC3339, interval.From)
			z, ez := time.Parse(time.RFC3339, interval.To)
			if ea != nil || ez != nil || !z.After(a) {
				continue
			}
			remaining := []span{{a, z}}
			for _, used := range occupied {
				var next []span
				for _, s := range remaining {
					if !used.to.After(s.from) || !used.from.Before(s.to) {
						next = append(next, s)
						continue
					}
					d.OverlapAdjusted = true
					if used.from.After(s.from) {
						next = append(next, span{s.from, used.from})
					}
					if used.to.Before(s.to) {
						next = append(next, span{used.to, s.to})
					}
				}
				remaining = next
			}
			for _, s := range remaining {
				seconds += s.to.Sub(s.from).Seconds()
				occupied = append(occupied, s)
			}
		}
		d.Minutes = min(480, max(0, int(seconds/60)))
	}
}

func trimMaterialDigest(d *materialDigest, maxPrompts, maxChars int) {
	if strings.TrimSpace(d.Title) == "" {
		d.Title = materialTitle(d.UserPrompts)
	}
	if len(d.Evidence) > 32 {
		d.Evidence = d.Evidence[:32]
		d.EvidenceTruncated = true
	}
	budget := maxChars
	trimList := func(items []string) []string {
		out := make([]string, 0, len(items))
		for _, item := range items {
			if budget <= 0 {
				d.Truncated = true
				break
			}
			text := strings.TrimSpace(item)
			if text == "" {
				continue
			}
			if len(text) > budget {
				for len(text) > budget {
					_, size := utf8.DecodeLastRuneInString(text)
					text = text[:len(text)-size]
				}
				d.Truncated = true
			}
			budget -= len(text)
			out = append(out, text)
		}
		return out
	}
	d.UserPrompts = trimList(d.UserPrompts)
	d.AssistantExcerpts = trimList(d.AssistantExcerpts)
	if len(d.UserPrompts) > maxPrompts {
		d.UserPrompts = d.UserPrompts[:maxPrompts]
		d.Truncated = true
	}
}

func materialEntries(digests []materialDigest) []map[string]any {
	entries := make([]map[string]any, 0, len(digests))
	for _, digest := range digests {
		summary := strings.TrimSpace(strings.Join(digest.AssistantExcerpts, "\n"))
		if summary == "" {
			summary = strings.TrimSpace(strings.Join(digest.UserPrompts, "\n"))
		}
		feature := strings.TrimSpace(digest.Feature)
		minutes := digest.Minutes
		minutesSource := digest.MinutesSource

		item := map[string]any{
			"source":     digest.Source,
			"session_id": digest.SessionID,
			"date":       digest.Date,
			"project":    digest.Project,
			"category":   "其他",
			"started_at": digest.StartedAt,
			"ended_at":   digest.EndedAt,
			"title":      digest.Title,
			"summary":    summary,
			"tags":       []string{"自动采集"},
		}
		if repoPath := strings.TrimSpace(digest.RepoPath); repoPath != "" {
			item["repo_path"] = repoPath
		}
		if exhibit := strings.TrimSpace(digest.Exhibit); exhibit != "" {
			item["exhibit"] = exhibit
		}
		if feature != "" {
			item["feature"] = feature
		}
		if digest.Provider == "agent" {
			item["provider"] = digest.Provider
			item["workspace_project_id"] = digest.WorkspaceProjectID
			item["minutes_method"] = digest.MinutesMethod
			item["evidence"] = digest.Evidence
			item["evidence_truncated"] = digest.EvidenceTruncated
			item["activity_intervals"] = append([]activityInterval{}, digest.ActivityIntervals...)
			item["overlap_adjusted"] = digest.OverlapAdjusted
			item["refs"] = []map[string]string{{"type": "session", "value": digest.Ref}}
		}
		if minutes > 0 || digest.Provider == "agent" {
			item["minutes"] = minutes
			item["minutes_source"] = minutesSource
		}
		entries = append(entries, item)
	}
	return entries
}

// resolveWindow 解析目标窗口：显式 from/to/date 优先；三者都为空时按 lookback_days
// 取 [今天-(n-1), 今天]，默认 n=1（即仅今天），n=2 时为「昨天+今天」。

func materialTitle(prompts []string) string {
	for _, p := range prompts {
		p = strings.TrimSpace(p)
		if p != "" {
			return string([]rune(p)[:min(80, len([]rune(p)))])
		}
	}
	return "会话活动"
}
