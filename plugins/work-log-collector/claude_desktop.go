package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// 本文件解析 Claude Desktop（桌面端）的本地会话：Code 标签页（claude-code-sessions）
// 与 local agent mode / Cowork（local-agent-mode-sessions）。
//
// 桌面端把会话元数据写成 <profile 根>/<子目录>/**/local_<uuid>.json，正文沿用 Claude
// Code 的 JSONL 转写格式：
//   - Code 标签页：~/.claude/projects/<工作目录编码>/<cliSessionId>.jsonl
//   - local agent mode：<元数据同目录>/<sessionId 前 8 位>/.claude/projects/session/<cliSessionId>.jsonl
//
// 采集全程只读；不写回任何 Claude Desktop 文件。
//
// 注意：Code 标签页的转写与 Claude Code CLI 共用 ~/.claude/projects。原生 claude 来源
// 只读该目录下的 sessions-index.json，而桌面端 Code 会话不写索引，因此两者当前不重叠。

const claudeDesktopSourceID = "claude-desktop"

// claudeDesktopSubdirs 是桌面端每个 profile 下会写会话的两类目录。
var claudeDesktopSubdirs = []string{"local-agent-mode-sessions", "claude-code-sessions"}

// claudeDesktopSession 是会话元数据（local_<uuid>.json）里采集需要的字段。元数据里还有
// 体量很大的 systemPrompt 等字段，刻意不解析，避免把整段系统提示读进内存。
type claudeDesktopSession struct {
	SessionID           string   `json:"sessionId"`
	CLISessionID        string   `json:"cliSessionId"`
	Cwd                 string   `json:"cwd"`
	OriginCwd           string   `json:"originCwd"`
	UserSelectedFolders []string `json:"userSelectedFolders"`
	Title               string   `json:"title"`
	InitialMessage      string   `json:"initialMessage"`
	Model               string   `json:"model"`
	CreatedAt           int64    `json:"createdAt"`
	LastActivityAt      int64    `json:"lastActivityAt"`
	CompletedTurns      int      `json:"completedTurns"`
	IsArchived          bool     `json:"isArchived"`
}

// countClaudeDesktopSessions 统计多个桌面端根目录下的会话元数据文件数，供来源列表展示。
func countClaudeDesktopSessions(roots []string) int {
	total := 0
	for _, root := range roots {
		total += len(claudeDesktopSessionFiles(root))
	}
	return total
}

// claudeDesktopSessionFiles 收集一个桌面端根目录下的会话元数据文件。只进入两个已知的
// 会话子目录，并跳过体积很大的 skills-plugin，避免整盘遍历客户端安装目录。
func claudeDesktopSessionFiles(root string) []string {
	files := make([]string, 0)
	for _, sub := range claudeDesktopSubdirs {
		base := filepath.Join(root, sub)
		if !dirExists(base) {
			continue
		}
		_ = filepath.WalkDir(base, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if entry.IsDir() {
				if entry.Name() == "skills-plugin" {
					return filepath.SkipDir
				}
				return nil
			}
			if isClaudeDesktopSessionFile(entry.Name()) {
				files = append(files, path)
			}
			return nil
		})
	}
	sort.Strings(files)
	return files
}

// isClaudeDesktopSessionFile 判断是否是桌面端会话元数据文件（local_<uuid>.json）。
func isClaudeDesktopSessionFile(name string) bool {
	return strings.HasPrefix(name, "local_") && strings.HasSuffix(name, ".json")
}

// scanClaudeDesktop 扫描一个或多个桌面端根目录下的会话。同一会话可能同时出现在多个
// profile 根（如 Claude 与 Claude-3p），按 sessionId 只保留 lastActivityAt 最新的一份。
func scanClaudeDesktop(roots []string, home string, start, end time.Time) ([]sessionDigest, error) {
	type candidate struct {
		meta claudeDesktopSession
		file string
	}
	best := map[string]candidate{}
	order := make([]string, 0)
	for _, root := range roots {
		for _, file := range claudeDesktopSessionFiles(root) {
			meta, ok := readClaudeDesktopSession(file)
			if !ok || strings.TrimSpace(meta.SessionID) == "" {
				continue
			}
			if existing, seen := best[meta.SessionID]; seen {
				if meta.LastActivityAt <= existing.meta.LastActivityAt {
					continue
				}
			} else {
				order = append(order, meta.SessionID)
			}
			best[meta.SessionID] = candidate{meta: meta, file: file}
		}
	}
	digests := make([]sessionDigest, 0, len(order))
	for _, id := range order {
		item := best[id]
		if digest, ok := claudeDesktopDigest(item.meta, item.file, home, start, end); ok {
			digests = append(digests, digest)
		}
	}
	return digests, nil
}

func readClaudeDesktopSession(file string) (claudeDesktopSession, bool) {
	body, err := os.ReadFile(file)
	if err != nil {
		return claudeDesktopSession{}, false
	}
	var meta claudeDesktopSession
	if json.Unmarshal(body, &meta) != nil {
		return claudeDesktopSession{}, false
	}
	return meta, true
}

// claudeDesktopDigest 把一份会话元数据（叠加转写正文）折算成当天的会话素材。
func claudeDesktopDigest(meta claudeDesktopSession, file, home string, start, end time.Time) (sessionDigest, bool) {
	created := timeFromMillis(meta.CreatedAt)
	activity := timeFromMillis(meta.LastActivityAt)
	lo, hi := created, activity
	if lo.IsZero() {
		lo = activity
	}
	if hi.IsZero() {
		hi = created
	}
	// 没有任何时间信息，或会话活动区间与本窗口不相交，则跳过。
	if lo.IsZero() || hi.IsZero() || !lo.Before(end) || hi.Before(start) {
		return sessionDigest{}, false
	}

	transcript := claudeDesktopTranscriptPath(meta, file, home)
	var (
		prompts   []string
		excerpts  []string
		times     []time.Time
		tokensIn  int64
		tokensOut int64
	)
	if transcript != "" && !transcriptBeforeWindow(transcript, start) {
		prompts, excerpts, times, tokensIn, tokensOut = parseClaudeTranscript(transcript, start, end)
	}

	// 归日与起止取窗口内的活动时间；窗口内没有逐条时间时，退回落在窗口内的创建/最后活动时间。
	var started, ended time.Time
	switch {
	case len(times) > 0:
		started, ended = times[0], times[len(times)-1]
	case inRange(activity, start, end):
		started, ended = activity, activity
	case inRange(created, start, end):
		started, ended = created, created
	default:
		return sessionDigest{}, false
	}

	dir := claudeDesktopDirectory(meta, file)
	digest := sessionDigest{
		Source:            claudeDesktopSourceID,
		SessionID:         meta.SessionID,
		Directory:         dir,
		Title:             firstLine(meta.Title, 80),
		Date:              started.Local().Format(dateLayout),
		StartedAt:         stampRFC3339(started),
		UserPrompts:       prompts,
		AssistantExcerpts: excerpts,
		TokensInput:       tokensIn,
		TokensOutput:      tokensOut,
		Ref:               file,
	}
	if transcript != "" {
		digest.Ref = transcript
	}
	if dir != "" {
		digest.Project = filepath.Base(filepath.Clean(dir))
	}
	// 没有转写正文时，用元数据的首条消息兜底，保证素材里仍有可总结的内容。
	if len(digest.UserPrompts) == 0 {
		if text := strings.TrimSpace(meta.InitialMessage); text != "" {
			digest.UserPrompts = []string{text}
		}
	}
	if digest.Title == "" {
		digest.Title = firstLine(meta.InitialMessage, 80)
	}
	digest.TurnCount = meta.CompletedTurns
	if digest.TurnCount <= 0 {
		digest.TurnCount = len(digest.UserPrompts)
	}
	if !ended.IsZero() {
		digest.EndedAt = stampRFC3339(ended)
	}
	digest.Minutes, digest.MinutesSource = resolveMinutes(times, started, ended, digest.TurnCount)
	return digest, true
}

// claudeDesktopDirectory 解析会话的真实工作目录：优先用户挂载的文件夹，其次原始工作目录；
// local agent mode 的 cwd 指向 VM 沙箱，不能当作项目路径。
func claudeDesktopDirectory(meta claudeDesktopSession, file string) string {
	for _, folder := range meta.UserSelectedFolders {
		if strings.TrimSpace(folder) != "" {
			return folder
		}
	}
	if origin := strings.TrimSpace(meta.OriginCwd); origin != "" {
		return origin
	}
	if strings.Contains(toSlashLower(file), "local-agent-mode-sessions") {
		return ""
	}
	return strings.TrimSpace(meta.Cwd)
}

// claudeDesktopTranscriptPath 定位会话的 JSONL 转写文件；找不到时返回空。
func claudeDesktopTranscriptPath(meta claudeDesktopSession, file, home string) string {
	cli := strings.TrimSpace(meta.CLISessionID)
	if cli == "" {
		return ""
	}
	if strings.Contains(toSlashLower(file), "local-agent-mode-sessions") {
		short := claudeDesktopShortID(meta.SessionID)
		if short == "" {
			return ""
		}
		candidate := filepath.Join(filepath.Dir(file), short, ".claude", "projects", "session", cli+".jsonl")
		if fileExists(candidate) {
			return candidate
		}
		return ""
	}
	dir := firstNonEmpty(meta.OriginCwd, meta.Cwd)
	if strings.TrimSpace(home) == "" || strings.TrimSpace(dir) == "" {
		return ""
	}
	candidate := filepath.Join(home, ".claude", "projects", encodeClaudeProjectDir(dir), cli+".jsonl")
	if fileExists(candidate) {
		return candidate
	}
	return ""
}

// claudeDesktopShortID 取 local agent mode 的会话目录名：sessionId 去掉 local_ 前缀后的
// 第一段（UUID 前 8 位）。
func claudeDesktopShortID(sessionID string) string {
	trimmed := strings.TrimPrefix(strings.TrimSpace(sessionID), "local_")
	if idx := strings.IndexByte(trimmed, '-'); idx > 0 {
		return trimmed[:idx]
	}
	return ""
}

// encodeClaudeProjectDir 复刻 Claude Code 把工作目录编码成 ~/.claude/projects 子目录名的
// 规则：非字母数字一律替换成连字符（F:\WebProjects\x → F--WebProjects-x）。
func encodeClaudeProjectDir(path string) string {
	var builder strings.Builder
	for _, r := range strings.TrimSpace(path) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			builder.WriteRune(r)
		default:
			builder.WriteByte('-')
		}
	}
	return builder.String()
}

// transcriptBeforeWindow 用文件修改时间做廉价预筛：窗口开始前就没再写过，文件里不可能
// 有窗口内记录。
func transcriptBeforeWindow(path string, start time.Time) bool {
	info, err := os.Stat(path)
	return err == nil && info.ModTime().Before(start)
}

// claudeTranscriptRecord 是 Claude Code JSONL 转写里采集需要的字段。
type claudeTranscriptRecord struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Origin    struct {
		Kind string `json:"kind"`
	} `json:"origin"`
	TurnOrigin string `json:"turnOrigin"`
	Message    struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
		Usage   struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// parseClaudeTranscript 回放 Claude Code 转写，抽出窗口内的真实用户提问、助手摘要、逐条
// 时间点与 token 用量。工具结果、系统注入与附件不计入素材。
func parseClaudeTranscript(file string, start, end time.Time) (prompts, excerpts []string, times []time.Time, tokensIn, tokensOut int64) {
	handle, err := os.Open(file)
	if err != nil {
		return nil, nil, nil, 0, 0
	}
	defer handle.Close()

	scanner := bufio.NewScanner(handle)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var record claudeTranscriptRecord
		if json.Unmarshal(line, &record) != nil {
			continue
		}
		ts, tsErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(record.Timestamp))
		inWindow := tsErr == nil && inRange(ts, start, end)
		switch record.Type {
		case "user":
			if !inWindow || !claudeHumanTurn(record) {
				continue
			}
			if text := strings.TrimSpace(claudeContentText(record.Message.Content)); text != "" {
				prompts = append(prompts, text)
			}
			times = append(times, ts)
		case "assistant":
			if !inWindow {
				continue
			}
			tokensIn += record.Message.Usage.InputTokens
			tokensOut += record.Message.Usage.OutputTokens
			if text := strings.TrimSpace(claudeContentText(record.Message.Content)); text != "" {
				excerpts = append(excerpts, firstLine(text, 240))
			}
			times = append(times, ts)
		}
	}
	return prompts, excerpts, times, tokensIn, tokensOut
}

// claudeHumanTurn 判断一条 user 记录是否来自真人输入（而非工具结果或任务通知）。
func claudeHumanTurn(record claudeTranscriptRecord) bool {
	return strings.EqualFold(record.Origin.Kind, "human") || strings.EqualFold(record.TurnOrigin, "human")
}

// claudeContentText 从 message.content 提取纯文本：字符串直接用，数组只取 text 块。
func claudeContentText(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(trimmed, &text) == nil {
		return text
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(trimmed, &blocks) != nil {
		return ""
	}
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type == "text" || block.Type == "" {
			if strings.TrimSpace(block.Text) != "" {
				parts = append(parts, block.Text)
			}
		}
	}
	return strings.Join(parts, "\n")
}
