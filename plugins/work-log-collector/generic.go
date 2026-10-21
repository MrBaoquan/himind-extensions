package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 本文件实现「通用可配置来源」：用声明式规格（路径 + 格式 + 字段映射）接入任意
// AI 客户端的会话存储，而不必为每个客户端写死解析代码。内置一组预设覆盖常见
// 客户端，用户还可以用 sources.json 覆盖或新增来源。采集全程只读。

const sourcesConfigSchema = "work-log-sources.v1"

// genericSource 是一个声明式来源规格。字段路径使用点号取值，例如 "message.content"、
// "payload.text"；数组下标用 "." 加数字，例如 "content.0.text"。
type genericSource struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Native   bool   `json:"native,omitempty"`
	Preset   bool   `json:"preset,omitempty"`
	Root     string `json:"root,omitempty"`
	Glob     string `json:"glob,omitempty"`
	Format   string `json:"format,omitempty"` // jsonl | json | json_array
	Resolver string `json:"resolver,omitempty"`

	SessionID string `json:"session_id,omitempty"`
	// 时间字段按顺序尝试，命中即用；都取不到时回退文件时间/文件名日期。
	TimeFields []string `json:"time_fields,omitempty"`
	// 值为 0=秒、3=毫秒、6=微秒，用于把整数时间戳换算成时间；缺省按 10 位/13 位猜测。
	TimeUnit       *int   `json:"time_unit,omitempty"`
	TitleField     string `json:"title_field,omitempty"`
	PromptField    string `json:"prompt_field,omitempty"`
	AssistantField string `json:"assistant_field,omitempty"`
	// 当 prompt/assistant 用同一个内容字段 + 角色字段区分时使用。
	RoleField      string   `json:"role_field,omitempty"`
	UserRoles      []string `json:"user_roles,omitempty"`
	AssistantRoles []string `json:"assistant_roles,omitempty"`
	// 内容字段：用于 role 模式下提取正文。
	ContentFields []string `json:"content_fields,omitempty"`

	ProjectField    string `json:"project_field,omitempty"`
	DirectoryField  string `json:"directory_field,omitempty"`
	ProjectStrategy string `json:"project_strategy,omitempty"` // field | dir_base | git_root

	// json 对象里真正的记录数组路径；json_array 格式可省略。
	RecordsPath string `json:"records_path,omitempty"`
	// 单文件即一个会话（按文件名取 session_id）。
	SessionFromFile bool `json:"session_from_file,omitempty"`
	// 文件名日期正则（含一个捕获组，形如 2026-10-09）。
	FileNameDate string `json:"file_name_date,omitempty"`

	Experimental bool   `json:"experimental,omitempty"`
	Note         string `json:"note,omitempty"`
}

type sourcesConfig struct {
	SchemaVersion string          `json:"schema_version"`
	Sources       []genericSource `json:"sources"`
}

// ---- 预设 ----

// genericPresets 返回内置的通用来源预设。多数为非标准/较少验证的本地格式，
// 标记 experimental 提示用户核对路径与字段；用户可在 sources.json 里覆盖。
func genericPresets() []genericSource {
	return []genericSource{
		{
			ID: "cursor", Label: "Cursor", Preset: true, Experimental: true,
			Root: "~/.cursor/projects", Glob: "**/*.jsonl", Format: "jsonl",
			SessionID: "sessionId", TimeFields: []string{"timestamp", "createdAt", "created_at"},
			RoleField: "role", UserRoles: []string{"user"}, AssistantRoles: []string{"assistant"},
			ContentFields:   []string{"text", "content", "message"},
			ProjectStrategy: "git_root",
			Note:            "Cursor 本地 agent 记录，字段随版本变化，必要时在 sources.json 覆盖",
		},
		{
			ID: "gemini-cli", Label: "Gemini CLI", Preset: true, Experimental: true,
			Root: "~/.gemini", Glob: "**/chats/*.json", Format: "json",
			SessionID: "sessionId", TimeFields: []string{"timestamp", "startTime", "start_time"},
			PromptField: "prompt", AssistantField: "response", RecordsPath: "messages",
			ProjectStrategy: "git_root",
			Note:            "Gemini CLI 会话，目录结构随版本变化",
		},
		{
			ID: "cline", Label: "Cline / Roo Code", Preset: true, Experimental: true,
			Root: "%APPDATA%/Code/User/globalStorage/saoudrizwan.claude-dev", Glob: "tasks/*/api_conversation_history.json", Format: "json_array",
			SessionFromFile: true, TimeFields: []string{"ts", "timestamp"},
			RoleField: "role", UserRoles: []string{"user"}, AssistantRoles: []string{"assistant"},
			ContentFields:   []string{"content", "text"},
			ProjectStrategy: "dir_base",
			Note:            "VS Code + Cline 扩展的历史会话",
		},
		{
			ID: "continue", Label: "Continue", Preset: true, Experimental: true,
			Root: "~/.continue/sessions", Glob: "*.json", Format: "json",
			SessionID: "sessionId", TimeFields: []string{"dateCreated", "createdAt", "timestamp"},
			PromptField: "message.content", AssistantField: "message.content", RecordsPath: "history",
			ProjectStrategy: "dir_base",
			Note:            "Continue 扩展的历史会话",
		},
		{
			ID: "windsurf", Label: "Windsurf", Preset: true, Experimental: true,
			Root: "~/.codeium/windsurf", Glob: "**/*.jsonl", Format: "jsonl",
			SessionID: "sessionId", TimeFields: []string{"timestamp", "createdAt"},
			RoleField: "role", UserRoles: []string{"user"}, AssistantRoles: []string{"assistant"},
			ContentFields:   []string{"text", "content"},
			ProjectStrategy: "git_root",
			Note:            "Windsurf 本地记录",
		},
		{
			ID: "lingma", Label: "通义灵码", Preset: true, Experimental: true,
			Root: "~/.lingma", Glob: "**/*.json", Format: "json",
			SessionID: "sessionId", TimeFields: []string{"timestamp", "createTime", "created_at"},
			RoleField: "role", UserRoles: []string{"user"}, AssistantRoles: []string{"assistant"},
			ContentFields:   []string{"content", "text"},
			ProjectStrategy: "git_root",
			Note:            "通义灵码本地会话，结构随版本变化",
		},
		{
			ID: "codebuddy-cli", Label: "CodeBuddy CLI", Preset: true, Experimental: true,
			Root: "~/.codebuddy", Glob: "**/*.jsonl", Format: "jsonl",
			SessionID: "sessionId", TimeFields: []string{"timestamp", "created_at"},
			RoleField: "role", UserRoles: []string{"user"}, AssistantRoles: []string{"assistant"},
			ContentFields:   []string{"content", "text"},
			ProjectStrategy: "git_root",
			Note:            "腾讯 CodeBuddy 本地会话",
		},
		{
			ID: "kiro", Label: "Kiro", Preset: true, Experimental: true,
			Root: "~/.kiro", Glob: "**/*.json", Format: "json",
			SessionID: "sessionId", TimeFields: []string{"timestamp", "createdAt"},
			RoleField: "role", UserRoles: []string{"user"}, AssistantRoles: []string{"assistant"},
			ContentFields:   []string{"content", "text"},
			ProjectStrategy: "git_root",
			Note:            "AWS Kiro 本地会话",
		},
		{
			ID: "workbuddy", Label: "WorkBuddy", Preset: true, Experimental: true,
			Root: "~/.workbuddy", Glob: "**/*.json", Format: "json",
			SessionID: "sessionId", TimeFields: []string{"timestamp", "createdAt", "time"},
			RoleField: "role", UserRoles: []string{"user"}, AssistantRoles: []string{"assistant"},
			ContentFields:   []string{"content", "text"},
			ProjectStrategy: "git_root",
			Note:            "WorkBuddy 本地会话",
		},
	}
}

// ---- 配置加载 ----

// loadGenericSources 合并三层来源：内置预设 < 配置文件的同名覆盖 < 能力入参内联。
// 配置文件默认取 <log_root>/config/sources.json，也可用 sources_config_path 指定。
func loadGenericSources(in input, home string) ([]genericSource, string, error) {
	merged := map[string]genericSource{}
	order := make([]string, 0)
	put := func(spec genericSource) {
		if _, ok := merged[spec.ID]; !ok {
			order = append(order, spec.ID)
		}
		merged[spec.ID] = spec
	}
	if in.IncludePresets == nil || *in.IncludePresets {
		for _, preset := range genericPresets() {
			put(preset)
		}
	}
	configPath, err := resolveSourcesConfigPath(in, home)
	if err != nil {
		return nil, "", err
	}
	if configPath != "" {
		body, err := os.ReadFile(configPath)
		if err == nil {
			var cfg sourcesConfig
			if json.Unmarshal(body, &cfg) == nil {
				for _, spec := range cfg.Sources {
					spec.Preset = false
					put(spec)
				}
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, configPath, err
		}
	}
	if len(in.SourcesConfig) > 0 {
		for _, spec := range in.SourcesConfig {
			spec.Preset = false
			put(spec)
		}
	}
	specs := make([]genericSource, 0, len(order))
	for _, id := range order {
		specs = append(specs, merged[id])
	}
	return specs, configPath, nil
}

func resolveSourcesConfigPath(in input, home string) (string, error) {
	if p := strings.TrimSpace(in.SourcesConfigPath); p != "" {
		return expandPath(p, ""), nil
	}
	root := strings.TrimSpace(in.LogRoot)
	if root == "" {
		root = collectorDefaultLogRoot(home)
	}
	if root == "" {
		return "", nil
	}
	resolved, err := expandLogRoot(root)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, "config", "sources.json"), nil
}

// collectorDefaultLogRoot 复刻工作日志插件的默认日志根，便于采集器在未显式传入
// log_root 时也能读到 <根>/config/sources.json。只读，不创建目录。
func collectorDefaultLogRoot(home string) string {
	if configured := strings.TrimSpace(os.Getenv("HIMIND_WORK_LOG_ROOT")); configured != "" {
		return configured
	}
	if strings.TrimSpace(home) == "" {
		home = strings.TrimSpace(os.Getenv("USERPROFILE"))
		if home == "" {
			if resolved, err := os.UserHomeDir(); err == nil {
				home = resolved
			}
		}
	}
	if home != "" {
		return filepath.Join(home, "Documents", "HiMind", "工作日志")
	}
	base := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "HiMindAgent", "extension-data", "com.himind.work-log")
}

// expandLogRoot 只做路径规整，不创建目录（采集器只读）。
func expandLogRoot(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("log_root 为空")
	}
	if strings.HasPrefix(value, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		value = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(value, "~"), "/"))
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	return abs, nil
}

// expandPath 展开 ~、环境变量（%APPDATA% 等）与相对 home 的路径。
func expandPath(value, home string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = os.ExpandEnv(value)
	// Windows 风格的 %VAR% 也展开一次。
	value = expandPercentEnv(value)
	if strings.HasPrefix(value, "~") {
		base := home
		if base == "" {
			base, _ = os.UserHomeDir()
		}
		value = filepath.Join(base, strings.TrimPrefix(strings.TrimPrefix(value, "~"), "/"))
	} else if !filepath.IsAbs(value) && home != "" {
		value = filepath.Join(home, value)
	}
	return filepath.Clean(value)
}

var percentEnvPattern = regexp.MustCompile(`%([A-Za-z_][A-Za-z0-9_]*)%`)

func expandPercentEnv(value string) string {
	return percentEnvPattern.ReplaceAllStringFunc(value, func(match string) string {
		name := match[1 : len(match)-1]
		if v, ok := os.LookupEnv(name); ok {
			return v
		}
		return match
	})
}

// ---- 取值辅助 ----

// dig 按点号路径从嵌套 map/数组里取值，支持 "a.b.0.c" 形式的下标。
func dig(value any, path string) any {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	current := value
	for _, seg := range strings.Split(path, ".") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		switch node := current.(type) {
		case map[string]any:
			next, ok := node[seg]
			if !ok {
				return nil
			}
			current = next
		case []any:
			index, err := strconv.Atoi(seg)
			if err != nil || index < 0 || index >= len(node) {
				return nil
			}
			current = node[index]
		default:
			return nil
		}
	}
	return current
}

// firstDig 返回第一个命中路径的值。
func firstDig(value any, paths ...string) any {
	for _, path := range paths {
		if path == "" {
			continue
		}
		if got := dig(value, path); got != nil {
			return got
		}
	}
	return nil
}

// textList 把一个值规整成字符串列表：字符串、[]string、[]any（元素可为字符串或含 text 的对象）。
func textList(value any) []string {
	switch node := value.(type) {
	case nil:
		return nil
	case string:
		if strings.TrimSpace(node) == "" {
			return nil
		}
		return []string{node}
	case float64:
		return []string{strconv.FormatFloat(node, 'f', -1, 64)}
	case bool:
		return []string{strconv.FormatBool(node)}
	case []any:
		var out []string
		for _, item := range node {
			out = append(out, textList(item)...)
		}
		return out
	case map[string]any:
		for _, key := range []string{"text", "content", "value", "message"} {
			if text := textList(node[key]); len(text) > 0 {
				return text
			}
		}
		return nil
	default:
		return nil
	}
}

// firstText 从对象里按多个候选字段取第一段非空文本。
func firstText(value any, paths ...string) string {
	for _, path := range paths {
		if path == "" {
			continue
		}
		for _, text := range textList(dig(value, path)) {
			if strings.TrimSpace(text) != "" {
				return text
			}
		}
	}
	return ""
}

var timeLayouts = []string{
	time.RFC3339Nano, time.RFC3339,
	"2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02 15:04",
	"2006/01/02 15:04:05", "2006-01-02",
}

// parseTimeValue 尽力把字段值解析成时间：字符串按常见布局，数字按时间戳。
func parseTimeValue(value any, unit *int) (time.Time, bool) {
	switch node := value.(type) {
	case nil:
		return time.Time{}, false
	case string:
		text := strings.TrimSpace(node)
		if text == "" {
			return time.Time{}, false
		}
		if num, err := strconv.ParseInt(text, 10, 64); err == nil {
			return fromUnix(num, unit), true
		}
		for _, layout := range timeLayouts {
			if parsed, err := time.ParseInLocation(layout, text, time.Local); err == nil {
				return parsed, true
			}
		}
		return time.Time{}, false
	case float64:
		return fromUnix(int64(node), unit), true
	case int64:
		return fromUnix(node, unit), true
	case json.Number:
		if num, err := node.Int64(); err == nil {
			return fromUnix(num, unit), true
		}
	}
	return time.Time{}, false
}

func fromUnix(value int64, unit *int) time.Time {
	if unit != nil {
		switch *unit {
		case 0:
			return time.Unix(value, 0)
		case 3:
			return time.UnixMilli(value)
		case 6:
			return time.UnixMicro(value)
		}
	}
	// 猜测：13 位毫秒，16 位微秒，其余按秒。
	switch {
	case value > 1_000_000_000_000_000:
		return time.UnixMicro(value)
	case value > 1_000_000_000_000:
		return time.UnixMilli(value)
	default:
		return time.Unix(value, 0)
	}
}

// ---- 文件遍历 ----

// collectFiles 在 root 下按 glob（支持 **）收集文件；root 是文件时直接返回。
func collectFiles(root, glob string) []string {
	info, err := os.Stat(root)
	if err != nil {
		return nil
	}
	if !info.IsDir() {
		return []string{root}
	}
	if strings.TrimSpace(glob) == "" {
		glob = "**/*"
	}
	matcher := globMatcher(glob)
	var files []string
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			base := entry.Name()
			if base == "node_modules" || base == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		if matcher(filepath.ToSlash(rel)) {
			files = append(files, path)
		}
		return nil
	})
	sort.Strings(files)
	return files
}

// globMatcher 把 glob 编译成匹配函数，支持 **、*、?。
func globMatcher(glob string) func(string) bool {
	pattern := globToRegexp(filepath.ToSlash(glob))
	re := regexp.MustCompile("^" + pattern + "$")
	return func(path string) bool { return re.MatchString(path) }
}

func globToRegexp(glob string) string {
	var builder strings.Builder
	runes := []rune(glob)
	for i := 0; i < len(runes); i++ {
		switch runes[i] {
		case '*':
			if i+1 < len(runes) && runes[i+1] == '*' {
				// ** 或 **/
				i++
				if i+1 < len(runes) && runes[i+1] == '/' {
					i++
					builder.WriteString("(?:.*/)?")
				} else {
					builder.WriteString(".*")
				}
			} else {
				builder.WriteString("[^/]*")
			}
		case '?':
			builder.WriteString("[^/]")
		case '.', '+', '(', ')', '|', '^', '$', '{', '}', '[', ']', '\\':
			builder.WriteString("\\" + string(runes[i]))
		default:
			builder.WriteRune(runes[i])
		}
	}
	return builder.String()
}

// fileNameDate 用正则（含一个日期捕获组）从文件名里取日期。
func fileNameDate(file, pattern string) string {
	if strings.TrimSpace(pattern) == "" {
		return ""
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return ""
	}
	match := re.FindStringSubmatch(filepath.Base(file))
	if len(match) >= 2 {
		return match[1]
	}
	return ""
}

// ---- 通用扫描 ----

func scanGeneric(spec genericSource, home string, start, end time.Time) ([]sessionDigest, error) {
	root := expandPath(spec.Root, home)
	files := collectFiles(root, spec.Glob)
	if len(files) == 0 {
		return nil, nil
	}
	type group struct {
		digest    sessionDigest
		userTexts []string
		asstTexts []string
		first     time.Time
		started   time.Time
		ended     time.Time
		times     []time.Time
	}
	groups := map[string]*group{}
	order := make([]string, 0)
	var firstDir string
	for _, file := range files {
		fileTime := fileModTime(file)
		dateHint := fileNameDate(file, spec.FileNameDate)
		records, err := readGenericRecords(file, spec)
		if err != nil {
			continue
		}
		if firstDir == "" {
			firstDir = filepath.Dir(file)
		}
		for _, record := range records {
			key := genericSessionKey(spec, record, file)
			g, ok := groups[key]
			if !ok {
				g = &group{digest: sessionDigest{Source: spec.ID, SessionID: key, Ref: file}}
				groups[key] = g
				order = append(order, key)
			}
			if g.digest.Directory == "" {
				g.digest.Directory = firstText(record, spec.DirectoryField)
			}
			if spec.ProjectField != "" && g.digest.Project == "" {
				g.digest.Project = firstText(record, spec.ProjectField)
			}
			// 时间：优先字段，其次文件时间/文件名日期。
			when, ok := genericRecordTime(spec, record, fileTime, dateHint)
			if ok {
				if g.first.IsZero() || when.Before(g.first) {
					g.first = when
				}
				// 只有落在窗口内的时间点才用于归日与工时，跨日长会话据此按活跃日切分。
				if inRange(when, start, end) {
					if g.started.IsZero() || when.Before(g.started) {
						g.started = when
					}
					if when.After(g.ended) {
						g.ended = when
					}
					g.times = append(g.times, when)
				}
			}
			role := strings.ToLower(firstText(record, spec.RoleField))
			text := genericRecordText(spec, record)
			if strings.TrimSpace(text) == "" {
				continue
			}
			// 窗口外的对话不计入当日素材；无时间信息的记录按尽力纳入。
			if ok && !inRange(when, start, end) {
				continue
			}
			if spec.TitleField != "" && g.digest.Title == "" {
				g.digest.Title = firstLine(firstText(record, spec.TitleField), 80)
			}
			switch {
			case role != "" && containsFold(spec.UserRoles, role):
				g.userTexts = append(g.userTexts, text)
			case role != "" && containsFold(spec.AssistantRoles, role):
				g.asstTexts = append(g.asstTexts, firstLine(text, 240))
			case spec.RoleField == "" && spec.PromptField != "":
				g.userTexts = append(g.userTexts, firstLine(text, 400))
			default:
				g.asstTexts = append(g.asstTexts, firstLine(text, 240))
			}
		}
	}
	digests := make([]sessionDigest, 0, len(order))
	for _, key := range order {
		g := groups[key]
		if g.started.IsZero() {
			// 窗口内没有时间点：退回会话最早时间，再退回文件日期兜底。
			if !g.first.IsZero() {
				g.started = g.first
			} else if hint := fileNameDate(g.digest.Ref, spec.FileNameDate); hint != "" {
				if parsed, err := time.ParseInLocation(dateLayout, hint, time.Local); err == nil {
					g.started = parsed
				}
			}
		}
		if g.started.IsZero() || !inRange(g.started, start, end) {
			continue
		}
		g.digest.Date = g.started.Local().Format(dateLayout)
		g.digest.StartedAt = stampRFC3339(g.started)
		if !g.ended.IsZero() {
			g.digest.EndedAt = stampRFC3339(g.ended)
		}
		g.digest.UserPrompts = g.userTexts
		g.digest.AssistantExcerpts = g.asstTexts
		g.digest.TurnCount = len(g.userTexts)
		if g.digest.Title == "" && len(g.userTexts) > 0 {
			g.digest.Title = firstLine(g.userTexts[0], 80)
		}
		if g.digest.Project == "" {
			g.digest.Project = genericProject(spec, g.digest.Directory, firstDir, root)
		}
		g.digest.Minutes, g.digest.MinutesSource = resolveMinutes(g.times, g.started, g.ended, g.digest.TurnCount)
		digests = append(digests, g.digest)
	}
	return digests, nil
}

func genericSessionKey(spec genericSource, record any, file string) string {
	if !spec.SessionFromFile {
		if id := firstText(record, spec.SessionID); id != "" {
			return id
		}
		if spec.SessionID == "" {
			if id := firstText(record, "sessionId", "session_id", "conversationId", "conversation_id", "chatId"); id != "" {
				return id
			}
		}
	}
	base := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
	if base == "" {
		return file
	}
	return base
}

func genericRecordTime(spec genericSource, record any, fileTime time.Time, dateHint string) (time.Time, bool) {
	if len(spec.TimeFields) > 0 {
		if parsed, ok := parseTimeValue(firstDig(record, spec.TimeFields...), spec.TimeUnit); ok {
			return parsed, true
		}
	} else {
		for _, key := range []string{"timestamp", "createdAt", "created_at", "time", "ts", "startTime"} {
			if parsed, ok := parseTimeValue(dig(record, key), spec.TimeUnit); ok {
				return parsed, true
			}
		}
	}
	if !fileTime.IsZero() {
		return fileTime, true
	}
	if dateHint != "" {
		if parsed, err := time.ParseInLocation(dateLayout, dateHint, time.Local); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func genericRecordText(spec genericSource, record any) string {
	if spec.PromptField != "" || spec.AssistantField != "" {
		// 同时提供 prompt/assistant 字段时，取非空的那个（按记录一般只有一个）。
		if text := firstText(record, spec.PromptField); text != "" {
			return text
		}
		if text := firstText(record, spec.AssistantField); text != "" {
			return text
		}
	}
	if len(spec.ContentFields) > 0 {
		return firstText(record, spec.ContentFields...)
	}
	return firstText(record, "text", "content", "message", "value")
}

// genericProject 在记录里没有项目字段时，按策略推断项目：git 仓库名 > 目录名。
func genericProject(spec genericSource, directory, fallbackDir, root string) string {
	strategy := spec.ProjectStrategy
	if strategy == "" {
		strategy = "git_root"
	}
	dir := directory
	if dir == "" {
		dir = fallbackDir
	}
	switch strategy {
	case "field":
		return ""
	case "dir_base":
		return baseName(dir)
	default: // git_root
		if project := gitRootProject(dir); project != "" {
			return project
		}
		return baseName(dir)
	}
}

// ---- 记录读取 ----

func readGenericRecords(file string, spec genericSource) ([]any, error) {
	format := strings.ToLower(strings.TrimSpace(spec.Format))
	switch format {
	case "jsonl":
		return readJSONL(file)
	case "json", "json_array":
		return readJSONFile(file, spec)
	default:
		// 未知格式按扩展名猜测。
		if strings.EqualFold(filepath.Ext(file), ".jsonl") {
			return readJSONL(file)
		}
		return readJSONFile(file, spec)
	}
}

func readJSONL(file string) ([]any, error) {
	handle, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	var records []any
	scanner := bufio.NewScanner(handle)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var object any
		if json.Unmarshal([]byte(line), &object) != nil {
			continue
		}
		records = append(records, object)
	}
	return records, scanner.Err()
}

func readJSONFile(file string, spec genericSource) ([]any, error) {
	body, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var root any
	if json.Unmarshal(body, &root) != nil {
		return nil, errors.New("不是合法 JSON")
	}
	if spec.RecordsPath != "" {
		if records := dig(root, spec.RecordsPath); records != nil {
			return toRecordSlice(records), nil
		}
	}
	return toRecordSlice(root), nil
}

func toRecordSlice(value any) []any {
	switch node := value.(type) {
	case []any:
		return node
	case map[string]any:
		// 常见容器字段名兜底。
		for _, key := range []string{"messages", "history", "records", "items", "turns", "events"} {
			if list, ok := node[key].([]any); ok && len(list) > 0 {
				return list
			}
		}
		return []any{node}
	default:
		return nil
	}
}

// ---- 通用辅助 ----

func fileModTime(file string) time.Time {
	info, err := os.Stat(file)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), target) {
			return true
		}
	}
	return false
}

func baseName(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	cleaned := filepath.Clean(path)
	base := filepath.Base(cleaned)
	if base == "." || base == string(filepath.Separator) {
		return ""
	}
	return base
}

// gitRootProject 从目录向上寻找最近的 .git，返回仓库根目录名；找不到返回空。
func gitRootProject(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	current := filepath.Clean(dir)
	for {
		if info, err := os.Stat(filepath.Join(current, ".git")); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			return filepath.Base(current)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}

// applyProjectStrategy 供采集侧统一使用：目录存在时按策略推断项目名。
func applyProjectStrategy(strategy, directory, fallback string) string {
	switch strings.TrimSpace(strategy) {
	case "dir_base":
		if name := baseName(directory); name != "" {
			return name
		}
		return baseName(fallback)
	case "field":
		return ""
	default:
		if name := gitRootProject(directory); name != "" {
			return name
		}
		if name := baseName(directory); name != "" {
			return name
		}
		return baseName(fallback)
	}
}

// formatSourceNote 生成来源状态说明。
func formatSourceNote(spec genericSource) string {
	if spec.Note != "" {
		return spec.Note
	}
	if spec.Experimental {
		return "通用来源（实验性），可在 sources.json 覆盖字段"
	}
	return ""
}
