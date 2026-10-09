// Command work-log-collector 探测本机 AI 客户端的会话存储，把指定日期的会话整理成
// 工作日志素材摘要。
//
// 采集器不写工作日志库：写入日志由 com.himind.work-log 插件统一负责，保证日志库只有
// 一个写入者。它至多把会话素材落一份临时 artifact（扩展私有数据目录），交给工作流的
// AI 总结步骤读取。SQLite 会话库（OpenCode/Copilot）通过可选的 sqlite3 CLI 读取，
// 缺失时如实标记为不可用，不引入第三方 Go 依赖。
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/MrBaoquan/himind-extensions/sdk/jsonrpc"
)

const dateLayout = "2006-01-02"

func main() {
	if err := jsonrpc.Serve(os.Stdin, os.Stdout, handle); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type input struct {
	Source      string            `json:"source"`
	From        string            `json:"from"`
	To          string            `json:"to"`
	Date        string            `json:"date"`
	SessionID   string            `json:"session_id"`
	Home        string            `json:"home"`
	Roots       map[string]string `json:"roots"`
	SQLite3Path string            `json:"sqlite3_path"`
	Limit       int               `json:"limit"`
	MaxPrompts  int               `json:"max_prompts"`
	MaxChars    int               `json:"max_chars"`
	// 通用可配置来源：内联规格优先，其次配置文件，最后内置预设。
	LogRoot           string          `json:"log_root"`
	SourcesConfig     []genericSource `json:"sources_config"`
	SourcesConfigPath string          `json:"sources_config_path"`
	IncludePresets    *bool           `json:"include_presets"`
	// MaterialRoot 是会话素材文件的写入目录；缺省落到扩展私有数据目录。素材只供工作流
	// 的 AI 总结步骤读取，绝不写入工作日志库。
	MaterialRoot string `json:"material_root"`
}

func handle(request jsonrpc.Request) (any, *jsonrpc.Error) {
	var in input
	if rpcError := jsonrpc.DecodeParams(request, &in); rpcError != nil {
		return nil, rpcError
	}
	switch request.Method {
	case "work-log.session.sources":
		return rpcResult(sourcesCap(in))
	case "work-log.session.scan":
		return rpcResult(scanCap(in))
	default:
		return nil, &jsonrpc.Error{Code: -32602, Message: "不支持的工作日志采集能力"}
	}
}

func rpcResult(result any, err error) (any, *jsonrpc.Error) {
	if err != nil {
		return nil, &jsonrpc.Error{Code: -32000, Message: err.Error()}
	}
	return result, nil
}

// ---- 来源定义 ----

type sourceSpec struct {
	ID         string
	Label      string
	Kind       string // sqlite | jsonl
	DefaultRel string
}

func allSpecs() []sourceSpec {
	return []sourceSpec{
		{ID: "codex", Label: "Codex", Kind: "jsonl", DefaultRel: ".codex"},
		{ID: "claude", Label: "Claude Code", Kind: "jsonl", DefaultRel: ".claude/projects"},
		{ID: "opencode", Label: "OpenCode", Kind: "sqlite", DefaultRel: ".local/share/opencode/opencode.db"},
		{ID: "copilot", Label: "GitHub Copilot", Kind: "sqlite", DefaultRel: ".copilot/session-store.db"},
	}
}

type sourceStatus struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	Kind         string `json:"kind"`
	Path         string `json:"path"`
	Exists       bool   `json:"exists"`
	Supported    bool   `json:"supported"`
	Files        int    `json:"files,omitempty"`
	Preset       bool   `json:"preset,omitempty"`
	Experimental bool   `json:"experimental,omitempty"`
	Note         string `json:"note,omitempty"`
}

func resolveHome(in input) (string, error) {
	if h := strings.TrimSpace(in.Home); h != "" {
		return h, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("无法定位用户主目录，可在参数里用 home 指定")
	}
	return home, nil
}

func sourcePath(spec sourceSpec, home string, roots map[string]string) string {
	if roots != nil {
		if v := strings.TrimSpace(roots[spec.ID]); v != "" {
			return v
		}
	}
	return filepath.Join(home, filepath.FromSlash(spec.DefaultRel))
}

// resolveSQLite 定位 sqlite3 CLI：优先显式参数，其次 PATH，最后环境变量 SQLITE3。
func resolveSQLite(in input) (string, string) {
	if p := strings.TrimSpace(in.SQLite3Path); p != "" {
		if fileExists(p) {
			return p, ""
		}
		return "", "参数指定的 sqlite3_path 不存在"
	}
	if p, err := exec.LookPath("sqlite3"); err == nil {
		return p, ""
	}
	if p := strings.TrimSpace(os.Getenv("SQLITE3")); p != "" && fileExists(p) {
		return p, ""
	}
	return "", "未找到 sqlite3 CLI，SQLite 类来源不可用；可用 sqlite3_path 指定路径"
}

func sourcesCap(in input) (any, error) {
	home, err := resolveHome(in)
	if err != nil {
		return nil, err
	}
	sqlitePath, sqliteNote := resolveSQLite(in)
	statuses := make([]sourceStatus, 0)
	for _, spec := range allSpecs() {
		path := sourcePath(spec, home, in.Roots)
		status := sourceStatus{ID: spec.ID, Label: spec.Label, Kind: spec.Kind, Path: path, Exists: sourceExists(spec, path)}
		switch {
		case spec.Kind == "sqlite" && sqlitePath == "":
			status.Supported = false
			status.Note = "缺少 sqlite3 CLI"
		case !status.Exists:
			status.Supported = false
			status.Note = "未发现会话存储"
		default:
			status.Supported = true
		}
		statuses = append(statuses, status)
	}
	genericSpecs, configPath, err := loadGenericSources(in, home)
	if err != nil {
		return nil, err
	}
	for _, spec := range genericSpecs {
		root := expandPath(spec.Root, home)
		files := collectFiles(root, spec.Glob)
		status := sourceStatus{
			ID:           spec.ID,
			Label:        spec.Label,
			Kind:         genericKind(spec),
			Path:         root,
			Exists:       pathExists(root),
			Files:        len(files),
			Preset:       spec.Preset,
			Experimental: spec.Experimental,
			Note:         formatSourceNote(spec),
		}
		status.Supported = status.Exists && len(files) > 0
		statuses = append(statuses, status)
	}
	return map[string]any{
		"home":        home,
		"sqlite3":     map[string]any{"available": sqlitePath != "", "path": sqlitePath, "note": sqliteNote},
		"sources":     statuses,
		"config_path": configPath,
	}, nil
}

// genericKind 用于来源列表展示：把通用规格的格式映射成可读种类。
func genericKind(spec genericSource) string {
	if strings.TrimSpace(spec.Format) != "" {
		return "generic-" + strings.ToLower(strings.TrimSpace(spec.Format))
	}
	return "generic"
}

// pathExists 判断路径（文件或目录）是否存在。
func pathExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// ---- 扫描 ----

type sessionDigest struct {
	Source            string   `json:"source"`
	SessionID         string   `json:"session_id"`
	Project           string   `json:"project,omitempty"`
	Feature           string   `json:"feature,omitempty"`
	Directory         string   `json:"directory,omitempty"`
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

func scanCap(in input) (any, error) {
	home, err := resolveHome(in)
	if err != nil {
		return nil, err
	}
	start, end, from, to, err := resolveRange(in.From, in.To, in.Date)
	if err != nil {
		return nil, err
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 100
	}
	maxPrompts := in.MaxPrompts
	if maxPrompts <= 0 {
		maxPrompts = 8
	}
	maxChars := in.MaxChars
	if maxChars <= 0 {
		maxChars = 4000
	}

	sqlitePath, _ := resolveSQLite(in)
	selected := strings.TrimSpace(in.Source)
	if selected == "" {
		selected = "all"
	}

	var digests []sessionDigest
	skipped := make([]map[string]any, 0)
	for _, spec := range allSpecs() {
		if selected != "all" && selected != spec.ID {
			continue
		}
		path := sourcePath(spec, home, in.Roots)
		if !sourceExists(spec, path) {
			skipped = append(skipped, map[string]any{"source": spec.ID, "reason": "未发现会话存储", "path": path})
			continue
		}
		var found []sessionDigest
		switch spec.ID {
		case "codex":
			found, err = scanCodex(path, start, end)
		case "claude":
			found, err = scanClaude(path, start, end)
		case "opencode":
			if sqlitePath == "" {
				skipped = append(skipped, map[string]any{"source": spec.ID, "reason": "缺少 sqlite3 CLI", "path": path})
				continue
			}
			found, err = scanOpenCode(sqlitePath, path, start, end)
		case "copilot":
			if sqlitePath == "" {
				skipped = append(skipped, map[string]any{"source": spec.ID, "reason": "缺少 sqlite3 CLI", "path": path})
				continue
			}
			found, err = scanCopilot(sqlitePath, path, start, end)
		}
		if err != nil {
			skipped = append(skipped, map[string]any{"source": spec.ID, "reason": err.Error(), "path": path})
			continue
		}
		digests = append(digests, found...)
	}

	// 通用可配置来源（内置预设 + 配置文件 + 内联规格）。
	genericSpecs, configPath, err := loadGenericSources(in, home)
	if err != nil {
		return nil, err
	}
	for _, spec := range genericSpecs {
		if selected != "all" && selected != spec.ID {
			continue
		}
		root := expandPath(spec.Root, home)
		files := collectFiles(root, spec.Glob)
		if len(files) == 0 {
			if !pathExists(root) {
				skipped = append(skipped, map[string]any{"source": spec.ID, "reason": "未发现会话存储", "path": root})
			} else {
				skipped = append(skipped, map[string]any{"source": spec.ID, "reason": "匹配路径下没有会话文件", "path": root})
			}
			continue
		}
		found, scanErr := scanGeneric(spec, home, start, end)
		if scanErr != nil {
			skipped = append(skipped, map[string]any{"source": spec.ID, "reason": scanErr.Error(), "path": root})
			continue
		}
		digests = append(digests, found...)
	}

	// 按会话标识过滤与去重。
	filtered := make([]sessionDigest, 0, len(digests))
	seen := map[string]bool{}
	for _, d := range digests {
		if id := strings.TrimSpace(in.SessionID); id != "" && d.SessionID != id {
			continue
		}
		key := d.Source + "|" + d.SessionID
		if seen[key] {
			continue
		}
		seen[key] = true
		trimDigest(&d, maxPrompts, maxChars)
		filtered = append(filtered, d)
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].StartedAt > filtered[j].StartedAt })
	truncated := false
	if len(filtered) > limit {
		filtered = filtered[:limit]
		truncated = true
	}
	result := map[string]any{
		"home":        home,
		"from":        from,
		"to":          to,
		"sessions":    filtered,
		"entries":     candidateEntries(filtered),
		"total":       len(filtered),
		"truncated":   truncated,
		"skipped":     skipped,
		"config_path": configPath,
	}
	// 会话素材落一份 artifact 文件，供工作流的 AI 总结步骤以 input_artifacts 消费。
	// 这只是素材，不写日志库；写入的仍是 com.himind.work-log。
	artifact, err := writeMaterial(in, from, to, result)
	if err != nil {
		return nil, err
	}
	result["artifacts"] = []map[string]any{artifact}
	return result, nil
}

// writeMaterial 把会话素材写成一份符合 session-digest schema 的 JSON 文件，并返回
// 供工作流引用的 artifact 描述（含 file:// URI 与 sha256）。
func writeMaterial(in input, from, to string, content map[string]any) (map[string]any, error) {
	root, err := materialRoot(in)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, errors.New("会话素材目录创建失败")
	}
	payload, err := json.MarshalIndent(content, "", "  ")
	if err != nil {
		return nil, errors.New("会话素材序列化失败")
	}
	path := filepath.Join(root, fmt.Sprintf("session-digest-%s_%s.json", from, to))
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		return nil, errors.New("会话素材写入失败")
	}
	sum := sha256.Sum256(payload)
	return map[string]any{
		"artifact_id":   "session-digest",
		"artifact_type": "session_digest",
		"name":          "会话素材 " + from + " ~ " + to,
		"uri":           "file:///" + strings.ReplaceAll(path, "\\", "/"),
		"sha256":        hex.EncodeToString(sum[:]),
	}, nil
}

// materialRoot 解析会话素材目录：显式入参 > 扩展私有数据目录 > 系统临时目录。
func materialRoot(in input) (string, error) {
	if value := strings.TrimSpace(in.MaterialRoot); value != "" {
		abs, err := filepath.Abs(value)
		if err != nil {
			return "", errors.New("material_root 无效")
		}
		return abs, nil
	}
	base := strings.TrimSpace(os.Getenv("HIMIND_PLUGIN_DATA_ROOT"))
	if base == "" {
		if local := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); local != "" {
			base = filepath.Join(local, "HiMindAgent", "extension-data", "com.himind.work-log-collector")
		} else {
			base = filepath.Join(os.TempDir(), "HiMindAgent", "extension-data", "com.himind.work-log-collector")
		}
	}
	return filepath.Join(base, "material"), nil
}

// ---- 工时与功能推断 ----

const (
	// idleGapMinutes 是相邻消息可计入工时的最大间隔；超过则视为离开/挂机，不计入。
	idleGapMinutes = 20
	// maxEntryMinutes 是单条日志工时的上限（8 小时），避免异常长会话把统计拉爆。
	maxEntryMinutes = 480
)

// activeMinutes 用相邻消息的时间间隔估算「活跃工时」：只累计不超过 idleGapMinutes
// 的间隔，从而尽量剔除离开、挂机时间。至少两条消息才有意义。
func activeMinutes(times []time.Time) int {
	if len(times) < 2 {
		return 0
	}
	ordered := append([]time.Time(nil), times...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Before(ordered[j]) })
	total := 0.0
	for index := 1; index < len(ordered); index++ {
		gap := ordered[index].Sub(ordered[index-1]).Minutes()
		if gap <= 0 {
			continue
		}
		if gap > idleGapMinutes {
			gap = idleGapMinutes
		}
		total += gap
	}
	return clampMinutes(int(math.Round(total)))
}

// spanMinutes 用起止时间给出粗粒度工时（精度低于 activeMinutes，仅在没有逐条时间时使用）。
func spanMinutes(started, ended time.Time) int {
	if started.IsZero() || ended.IsZero() || !ended.After(started) {
		return 0
	}
	return clampMinutes(int(math.Round(ended.Sub(started).Minutes())))
}

// estimateMinutes 在完全没有时间信息时，按轮次给出保守估算。
func estimateMinutes(turns int) int {
	if turns <= 0 {
		return 0
	}
	return clampMinutes(turns * 3)
}

func clampMinutes(minutes int) int {
	if minutes < 0 {
		return 0
	}
	if minutes > maxEntryMinutes {
		return maxEntryMinutes
	}
	return minutes
}

// featureFromDirectory 尽力从会话工作目录推断「功能/模块」：优先取 git 仓库内、
// 相对仓库根的末级目录；没有仓库信息时，若目录名与项目名不同则取目录名。
func featureFromDirectory(directory, project string) string {
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return ""
	}
	clean := filepath.Clean(directory)
	if root := gitRoot(clean); root != "" {
		relative, err := filepath.Rel(root, clean)
		if err == nil && relative != "." && relative != "" && !strings.HasPrefix(relative, "..") {
			parts := strings.Split(filepath.ToSlash(relative), "/")
			last := strings.TrimSpace(parts[len(parts)-1])
			if last != "" && last != "." {
				return last
			}
		}
		return ""
	}
	base := filepath.Base(clean)
	if base != "" && base != "." && base != string(filepath.Separator) && !strings.EqualFold(base, strings.TrimSpace(project)) {
		return base
	}
	return ""
}

// gitRoot 向上查找最近的 git 仓库根（.git 目录或 .git 文件），找不到返回空。
func gitRoot(dir string) string {
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// candidateEntries 把会话素材映射成工作日志条目草稿，字段与 com.himind.work-log
// 的写入契约对齐，便于工作流把采集结果直接交给写入能力（确定性、无需人工）。
func candidateEntries(digests []sessionDigest) []map[string]any {
	entries := make([]map[string]any, 0, len(digests))
	for _, digest := range digests {
		summary := strings.TrimSpace(strings.Join(digest.AssistantExcerpts, "\n"))
		if summary == "" {
			summary = strings.TrimSpace(strings.Join(digest.UserPrompts, "\n"))
		}
		feature := strings.TrimSpace(digest.Feature)
		if feature == "" {
			feature = featureFromDirectory(digest.Directory, digest.Project)
		}
		minutes := digest.Minutes
		minutesSource := digest.MinutesSource
		if minutes == 0 {
			if estimated := estimateMinutes(digest.TurnCount); estimated > 0 {
				minutes = estimated
				minutesSource = "estimated"
			}
		}
		item := map[string]any{
			"source":     digest.Source,
			"session_id": digest.SessionID,
			"project":    digest.Project,
			"category":   "其他",
			"started_at": digest.StartedAt,
			"ended_at":   digest.EndedAt,
			"title":      digest.Title,
			"summary":    summary,
			"tags":       []string{"自动采集"},
		}
		if feature != "" {
			item["feature"] = feature
		}
		if minutes > 0 {
			item["minutes"] = minutes
			item["minutes_source"] = minutesSource
		}
		entries = append(entries, item)
	}
	return entries
}

func resolveRange(from, to, date string) (time.Time, time.Time, string, string, error) {
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	date = strings.TrimSpace(date)
	if from == "" && to == "" && date == "" {
		date = time.Now().Format(dateLayout)
	}
	if date != "" {
		if from == "" {
			from = date
		}
		if to == "" {
			to = date
		}
	}
	if !validDate(from) {
		return time.Time{}, time.Time{}, "", "", errors.New("from 必须是 YYYY-MM-DD 格式")
	}
	if !validDate(to) {
		return time.Time{}, time.Time{}, "", "", errors.New("to 必须是 YYYY-MM-DD 格式")
	}
	if from > to {
		from, to = to, from
	}
	start, _ := time.ParseInLocation(dateLayout, from, time.Local)
	endDay, _ := time.ParseInLocation(dateLayout, to, time.Local)
	return start, endDay.AddDate(0, 0, 1), from, to, nil
}

func validDate(value string) bool {
	if len(value) != 10 {
		return false
	}
	parsed, err := time.ParseInLocation(dateLayout, value, time.Local)
	return err == nil && parsed.Format(dateLayout) == value
}

func inRange(t time.Time, start, end time.Time) bool {
	return !t.Before(start) && t.Before(end)
}

func trimDigest(d *sessionDigest, maxPrompts, maxChars int) {
	if strings.TrimSpace(d.Title) == "" {
		d.Title = meaningfulTitle(d.UserPrompts, 80)
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
				text = text[:budget]
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

// ---- Codex JSONL ----

type codexRecord struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

func scanCodex(root string, start, end time.Time) ([]sessionDigest, error) {
	files, err := codexFiles(root)
	if err != nil {
		return nil, err
	}
	var digests []sessionDigest
	for _, file := range files {
		if fileDate := codexFileDate(file); fileDate != "" {
			day, err := time.ParseInLocation(dateLayout, fileDate, time.Local)
			if err == nil && (day.Before(start.AddDate(0, 0, -1)) || !day.Before(end)) {
				continue
			}
		}
		digest, ok, err := parseCodexFile(file)
		if err != nil {
			continue
		}
		if !ok {
			continue
		}
		started, err := time.Parse(time.RFC3339, digest.StartedAt)
		if err != nil || !inRange(started, start, end) {
			continue
		}
		digests = append(digests, digest)
	}
	return digests, nil
}

func codexFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			base := entry.Name()
			if base == ".tmp" || base == "node_modules" || base == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".jsonl") && strings.HasPrefix(name, "rollout-") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

var codexFileDatePattern = regexp.MustCompile(`^rollout-(\d{4}-\d{2}-\d{2})`)

func codexFileDate(file string) string {
	match := codexFileDatePattern.FindStringSubmatch(filepath.Base(file))
	if len(match) == 2 {
		return match[1]
	}
	return ""
}

func parseCodexFile(file string) (sessionDigest, bool, error) {
	handle, err := os.Open(file)
	if err != nil {
		return sessionDigest{}, false, err
	}
	defer handle.Close()

	digest := sessionDigest{Source: "codex", Ref: file}
	var started, ended time.Time
	var times []time.Time
	var userPrompts, assistantExcerpts []string
	decoder := json.NewDecoder(handle)
	for {
		var record codexRecord
		if err := decoder.Decode(&record); err != nil {
			break
		}
		if ts, err := time.Parse(time.RFC3339, record.Timestamp); err == nil {
			if started.IsZero() || ts.Before(started) {
				started = ts
			}
			if ts.After(ended) {
				ended = ts
			}
			times = append(times, ts)
		}
		switch record.Type {
		case "session_meta":
			var meta struct {
				SessionID string `json:"session_id"`
				ID        string `json:"id"`
				Cwd       string `json:"cwd"`
				Timestamp string `json:"timestamp"`
			}
			if json.Unmarshal(record.Payload, &meta) == nil {
				if digest.SessionID == "" {
					digest.SessionID = firstNonEmpty(meta.SessionID, meta.ID)
				}
				if digest.Directory == "" {
					digest.Directory = meta.Cwd
				}
				if started.IsZero() && meta.Timestamp != "" {
					if ts, err := time.Parse(time.RFC3339, meta.Timestamp); err == nil {
						started = ts
					}
				}
			}
		case "response_item":
			var item struct {
				Type    string `json:"type"`
				Role    string `json:"role"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			}
			if json.Unmarshal(record.Payload, &item) != nil || item.Type != "message" {
				continue
			}
			parts := make([]string, 0, len(item.Content))
			for _, block := range item.Content {
				if strings.TrimSpace(block.Text) != "" {
					parts = append(parts, block.Text)
				}
			}
			text := strings.Join(parts, "\n")
			switch item.Role {
			case "user":
				userPrompts = append(userPrompts, text)
			case "assistant", "agent":
				assistantExcerpts = append(assistantExcerpts, firstLine(text, 240))
			}
		}
	}
	if digest.SessionID == "" {
		return sessionDigest{}, false, nil
	}
	if started.IsZero() {
		return sessionDigest{}, false, nil
	}
	digest.StartedAt = started.Format(time.RFC3339)
	digest.EndedAt = ended.Format(time.RFC3339)
	digest.Date = started.Local().Format(dateLayout)
	digest.UserPrompts = userPrompts
	digest.AssistantExcerpts = assistantExcerpts
	digest.TurnCount = len(userPrompts)
	if digest.Directory != "" {
		digest.Project = filepath.Base(digest.Directory)
	}
	digest.Minutes, digest.MinutesSource = resolveMinutes(times, started, ended, digest.TurnCount)
	return digest, true, nil
}

// resolveMinutes 优先用逐条消息间隔实测工时，其次用起止区间，最后按轮次估算。
func resolveMinutes(times []time.Time, started, ended time.Time, turns int) (int, string) {
	if measured := activeMinutes(times); measured > 0 {
		return measured, "measured"
	}
	if span := spanMinutes(started, ended); span > 0 {
		return span, "span"
	}
	if estimated := estimateMinutes(turns); estimated > 0 {
		return estimated, "estimated"
	}
	return 0, ""
}

// ---- Claude Code 索引 ----

type claudeIndex struct {
	Entries []struct {
		SessionID    string `json:"sessionId"`
		FullPath     string `json:"fullPath"`
		FirstPrompt  string `json:"firstPrompt"`
		MessageCount int    `json:"messageCount"`
		Created      string `json:"created"`
		Modified     string `json:"modified"`
		ProjectPath  string `json:"projectPath"`
	} `json:"entries"`
}

func scanClaude(root string, start, end time.Time) ([]sessionDigest, error) {
	var digests []sessionDigest
	walkErr := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Name() != "sessions-index.json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var index claudeIndex
		if json.Unmarshal(data, &index) != nil {
			return nil
		}
		for _, item := range index.Entries {
			created, err := time.Parse(time.RFC3339, item.Created)
			if err != nil || !inRange(created, start, end) {
				continue
			}
			modified, _ := time.Parse(time.RFC3339, item.Modified)
			digest := sessionDigest{
				Source:      "claude",
				SessionID:   item.SessionID,
				Project:     filepath.Base(filepath.Clean(item.ProjectPath)),
				Directory:   item.ProjectPath,
				Title:       firstLine(item.FirstPrompt, 80),
				Date:        created.Local().Format(dateLayout),
				StartedAt:   created.Format(time.RFC3339),
				TurnCount:   item.MessageCount,
				UserPrompts: nonEmpty([]string{item.FirstPrompt}),
				Ref:         path,
			}
			if !modified.IsZero() {
				digest.EndedAt = modified.Format(time.RFC3339)
			}
			digest.Minutes, digest.MinutesSource = resolveMinutes(nil, created, modified, digest.TurnCount)
			if digest.SessionID == "" {
				continue
			}
			digests = append(digests, digest)
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return digests, nil
}

// ---- OpenCode SQLite ----

func scanOpenCode(sqlite3Path, db string, start, end time.Time) ([]sessionDigest, error) {
	startMs := start.UnixMilli()
	endMs := end.UnixMilli()
	sessions, err := runSQLiteJSON(sqlite3Path, db, fmt.Sprintf(
		"SELECT id, project_id, slug, directory, title, model, cost, tokens_input, tokens_output, time_created, time_updated FROM session_v2 WHERE time_created >= %d AND time_created < %d ORDER BY time_created DESC",
		startMs, endMs))
	if err != nil {
		return nil, err
	}
	projects := map[string]string{}
	if rows, err := runSQLiteJSON(sqlite3Path, db, "SELECT id, name, worktree FROM project"); err == nil {
		for _, row := range rows {
			name := asString(row, "name")
			if name == "" {
				name = filepath.Base(asString(row, "worktree"))
			}
			projects[asString(row, "id")] = name
		}
	}
	messages, err := runSQLiteJSON(sqlite3Path, db, fmt.Sprintf(
		"SELECT session_id, type, data, time_created FROM session_message WHERE time_created >= %d AND time_created < %d ORDER BY time_created",
		startMs, endMs))
	if err != nil {
		return nil, err
	}
	prompts := map[string][]string{}
	excerpts := map[string][]string{}
	msgTimes := map[string][]time.Time{}
	for _, row := range messages {
		text := extractText(asString(row, "data"))
		if strings.TrimSpace(text) == "" {
			continue
		}
		id := asString(row, "session_id")
		switch asString(row, "type") {
		case "user":
			prompts[id] = append(prompts[id], text)
		case "assistant":
			excerpts[id] = append(excerpts[id], firstLine(text, 240))
		}
		if created := asInt64(row, "time_created"); created > 0 {
			msgTimes[id] = append(msgTimes[id], time.UnixMilli(created))
		}
	}
	var digests []sessionDigest
	for _, row := range sessions {
		id := asString(row, "id")
		created := time.UnixMilli(asInt64(row, "time_created"))
		updated := time.UnixMilli(asInt64(row, "time_updated"))
		digest := sessionDigest{
			Source:            "opencode",
			SessionID:         id,
			Project:           projects[asString(row, "project_id")],
			Directory:         asString(row, "directory"),
			Title:             asString(row, "title"),
			Date:              created.Local().Format(dateLayout),
			StartedAt:         created.Format(time.RFC3339),
			UserPrompts:       prompts[id],
			AssistantExcerpts: excerpts[id],
			Cost:              asFloat(row, "cost"),
			TokensInput:       asInt64(row, "tokens_input"),
			TokensOutput:      asInt64(row, "tokens_output"),
			Ref:               db + "#" + id,
		}
		if !updated.IsZero() {
			digest.EndedAt = updated.Format(time.RFC3339)
		}
		digest.TurnCount = len(digest.UserPrompts)
		digest.Minutes, digest.MinutesSource = resolveMinutes(msgTimes[id], created, updated, digest.TurnCount)
		digests = append(digests, digest)
	}
	return digests, nil
}

// ---- Copilot SQLite ----

func scanCopilot(sqlite3Path, db string, start, end time.Time) ([]sessionDigest, error) {
	fromText := start.Format("2006-01-02 15:04:05")
	toText := end.Format("2006-01-02 15:04:05")
	sessions, err := runSQLiteJSON(sqlite3Path, db, fmt.Sprintf(
		"SELECT id, cwd, repository, summary, created_at, updated_at FROM sessions WHERE created_at >= '%s' AND created_at < '%s' ORDER BY created_at DESC",
		fromText, toText))
	if err != nil {
		return nil, err
	}
	turns, err := runSQLiteJSON(sqlite3Path, db, fmt.Sprintf(
		"SELECT session_id, user_message, assistant_response, timestamp FROM turns WHERE timestamp >= '%s' AND timestamp < '%s' ORDER BY timestamp",
		fromText, toText))
	if err != nil {
		return nil, err
	}
	prompts := map[string][]string{}
	excerpts := map[string][]string{}
	turnTimes := map[string][]time.Time{}
	for _, row := range turns {
		id := asString(row, "session_id")
		if text := strings.TrimSpace(asString(row, "user_message")); text != "" {
			prompts[id] = append(prompts[id], text)
		}
		if text := strings.TrimSpace(asString(row, "assistant_response")); text != "" {
			excerpts[id] = append(excerpts[id], firstLine(text, 240))
		}
		if ts := parseSQLiteTime(asString(row, "timestamp")); !ts.IsZero() {
			turnTimes[id] = append(turnTimes[id], ts)
		}
	}
	var digests []sessionDigest
	for _, row := range sessions {
		id := asString(row, "id")
		created := parseSQLiteTime(asString(row, "created_at"))
		updated := parseSQLiteTime(asString(row, "updated_at"))
		title := firstLine(asString(row, "summary"), 80)
		if title == "" && len(prompts[id]) > 0 {
			title = firstLine(prompts[id][0], 80)
		}
		digest := sessionDigest{
			Source:            "copilot",
			SessionID:         id,
			Project:           firstNonEmpty(asString(row, "repository"), filepath.Base(asString(row, "cwd"))),
			Directory:         asString(row, "cwd"),
			Title:             title,
			Date:              created.Local().Format(dateLayout),
			StartedAt:         created.Format(time.RFC3339),
			UserPrompts:       prompts[id],
			AssistantExcerpts: excerpts[id],
			Ref:               db + "#" + id,
		}
		if !updated.IsZero() {
			digest.EndedAt = updated.Format(time.RFC3339)
		}
		digest.TurnCount = len(digest.UserPrompts)
		digest.Minutes, digest.MinutesSource = resolveMinutes(turnTimes[id], created, updated, digest.TurnCount)
		digests = append(digests, digest)
	}
	return digests, nil
}

// ---- SQLite 辅助 ----

func runSQLiteJSON(sqlite3Path, db, query string) ([]map[string]any, error) {
	command := exec.Command(sqlite3Path, "-json", db, query)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("sqlite3 执行失败: %s", strings.TrimSpace(stderr.String()))
	}
	text := strings.TrimSpace(stdout.String())
	if text == "" || text == "[]" {
		return nil, nil
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(text), &rows); err != nil {
		return nil, fmt.Errorf("sqlite3 输出不是合法 JSON")
	}
	return rows, nil
}

// extractText 从会话消息的 JSON data 里尽力提取正文。
func extractText(data string) string {
	data = strings.TrimSpace(data)
	if data == "" {
		return ""
	}
	var payload map[string]any
	if json.Unmarshal([]byte(data), &payload) != nil {
		return ""
	}
	if text, ok := payload["text"].(string); ok && strings.TrimSpace(text) != "" {
		return text
	}
	if content, ok := payload["content"].([]any); ok {
		var parts []string
		for _, item := range content {
			if block, ok := item.(map[string]any); ok {
				if text, ok := block["text"].(string); ok && strings.TrimSpace(text) != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func parseSQLiteTime(value string) time.Time {
	value = strings.TrimSpace(value)
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

func asString(row map[string]any, key string) string {
	if value, ok := row[key].(string); ok {
		return value
	}
	return ""
}

func asInt64(row map[string]any, key string) int64 {
	switch value := row[key].(type) {
	case float64:
		return int64(value)
	case int64:
		return value
	case int:
		return int64(value)
	}
	return 0
}

func asFloat(row map[string]any, key string) float64 {
	if value, ok := row[key].(float64); ok {
		return value
	}
	return 0
}

// ---- 通用辅助 ----

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// sourceExists 按来源类型判断存储是否存在：JSONL 源是目录，SQLite 源是文件。
func sourceExists(spec sourceSpec, path string) bool {
	if spec.Kind == "jsonl" {
		return dirExists(path)
	}
	return fileExists(path)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstLine(text string, limit int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if index := strings.IndexAny(text, "\r\n"); index >= 0 {
		text = text[:index]
	}
	if limit > 0 {
		runes := []rune(text)
		if len(runes) > limit {
			text = string(runes[:limit])
		}
	}
	return text
}

// isSystemBlockLine 判断一行是否是客户端注入的系统块（环境上下文、ambient UI 状态等），
// 这类内容不能当作工作标题。
func isSystemBlockLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "<") || strings.HasPrefix(trimmed, "#") {
		return true
	}
	if strings.HasPrefix(trimmed, "This block is automatically supplied") {
		return true
	}
	return false
}

// meaningfulTitle 从用户消息里挑出能代表真实工作的标题：逐行跳过系统块与空行，
// 取第一条有实际语义的文本行。
func meaningfulTitle(prompts []string, limit int) string {
	for _, prompt := range prompts {
		for _, line := range strings.Split(prompt, "\n") {
			if isSystemBlockLine(line) {
				continue
			}
			return firstLine(strings.TrimSpace(line), limit)
		}
	}
	return ""
}

func nonEmpty(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item) != "" {
			out = append(out, item)
		}
	}
	return out
}
