// Command work-log-collector 探测本机 AI 客户端的会话存储，把指定日期的会话整理成
// 工作日志素材摘要。
//
// 采集器不写工作日志库：写入日志由 com.himind.work-log 插件统一负责，保证日志库只有
// 一个写入者。它至多把会话素材落一份临时 artifact（扩展私有数据目录），交给工作流的
// AI 总结步骤读取。SQLite 会话库（OpenCode/Copilot）由内置的纯 Go 驱动读取，无外部依赖。
// 原生来源包括 Codex、Claude Code、Claude Desktop、OpenCode、Copilot 与 VS Code
// Copilot Chat；其余客户端走声明式通用来源（见 generic.go）。
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/MrBaoquan/himind-extensions/sdk/jsonrpc"
	_ "modernc.org/sqlite"
)

const dateLayout = "2006-01-02"

func main() {
	if err := jsonrpc.Serve(os.Stdin, os.Stdout, handle); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type input struct {
	Source     string            `json:"source"`
	From       string            `json:"from"`
	To         string            `json:"to"`
	Date       string            `json:"date"`
	SessionID  string            `json:"session_id"`
	Home       string            `json:"home"`
	Roots      map[string]string `json:"roots"`
	Limit      int               `json:"limit"`
	MaxPrompts int               `json:"max_prompts"`
	MaxChars   int               `json:"max_chars"`
	// 通用可配置来源：内联规格优先，其次配置文件，最后内置预设。
	LogRoot           string          `json:"log_root"`
	SourcesConfig     []genericSource `json:"sources_config"`
	SourcesConfigPath string          `json:"sources_config_path"`
	IncludePresets    *bool           `json:"include_presets"`
	// ExcludePaths / ExcludeKeywords 是隐私排除规则：命中路径前缀或关键词的会话整条跳过，
	// 内容绝不写入素材与日志库。也可写在 <log_root>/config/privacy.json。
	ExcludePaths      []string `json:"exclude_paths"`
	ExcludeKeywords   []string `json:"exclude_keywords"`
	PrivacyConfigPath string   `json:"privacy_config_path"`
	// MaterialRoot 是会话素材文件的写入目录；缺省落到扩展私有数据目录。素材只供工作流
	// 的 AI 总结步骤读取，绝不写入工作日志库。
	MaterialRoot string `json:"material_root"`
	// WorkspaceRoot 是本次运行的工作区（工作流引擎注入 input.workspace_root）。插件宿主
	// 可能以受限令牌运行，只放行工作区与临时目录；此时素材改落到 <工作区>/.himind/material。
	WorkspaceRoot string `json:"workspace_root"`
	// LookbackDays 是「未显式指定 date/from/to」时默认回看的自然日数：1=仅今天（默认），
	// 2=昨天+今天。配合写入侧按「来源+会话+日期」的稳定指纹幂等合并，可把前一天较晚开始、
	// 当天定时运行时还没发生的工作在次日补回。
	LookbackDays int `json:"lookback_days"`
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
	Kind       string // sqlite | jsonl | vscode-chat
	DefaultRel string
	// DefaultRels 用于一个客户端存在多个存储根的场景（如 VS Code 稳定版 + Insiders）。
	DefaultRels []string
}

func allSpecs() []sourceSpec {
	return []sourceSpec{
		{ID: "codex", Label: "Codex", Kind: "jsonl", DefaultRel: ".codex"},
		{ID: "claude-code", Label: "Claude Code", Kind: "jsonl", DefaultRel: ".claude/projects"},
		{ID: "claude-desktop", Label: "Claude Desktop", Kind: "claude-desktop", DefaultRels: []string{
			"AppData/Local/Claude-3p",
			"AppData/Local/Claude",
			"AppData/Roaming/Claude",
		}},
		{ID: "opencode", Label: "OpenCode", Kind: "sqlite", DefaultRel: ".local/share/opencode/opencode.db"},
		{ID: "github-copilot", Label: "GitHub Copilot", Kind: "sqlite", DefaultRel: ".copilot/session-store.db"},
		{ID: "vscode", Label: "VS Code GitHub Copilot", Kind: "vscode-chat", DefaultRels: []string{
			"AppData/Roaming/Code/User/workspaceStorage",
			"AppData/Roaming/Code - Insiders/User/workspaceStorage",
		}},
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

// resolveHome 解析用户主目录。
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

// legacySourceAliases 历史来源 ID → 标准客户端 ID（与 Agent 的客户端命名对齐）。
// 旧配置、旧工作流入参与旧日志里可能还写着这些旧名。
var legacySourceAliases = map[string]string{
	"claude":         "claude-code",
	"copilot":        "github-copilot",
	"vscode-copilot": "vscode",
}

// normalizeSourceID 把来源 ID 归一到标准客户端 ID，供 source 过滤参数使用。
func normalizeSourceID(id string) string {
	trimmed := strings.ToLower(strings.TrimSpace(id))
	if canonical, ok := legacySourceAliases[trimmed]; ok {
		return canonical
	}
	return trimmed
}

// legacySourceKey 返回标准客户端 ID 对应的历史来源 ID（供 roots 覆盖键回退）。
func legacySourceKey(canonical string) string {
	for legacy, target := range legacySourceAliases {
		if target == canonical {
			return legacy
		}
	}
	return ""
}

// sourcePaths 解析来源的存储根：显式 roots 覆盖优先（含历史别名键）；否则用 DefaultRels（多根）或 DefaultRel（单根）。
func sourcePaths(spec sourceSpec, home string, roots map[string]string) []string {
	if roots != nil {
		if v := strings.TrimSpace(roots[spec.ID]); v != "" {
			return []string{v}
		}
		if legacy := legacySourceKey(spec.ID); legacy != "" {
			if v := strings.TrimSpace(roots[legacy]); v != "" {
				return []string{v}
			}
		}
	}
	if len(spec.DefaultRels) > 0 {
		out := make([]string, 0, len(spec.DefaultRels))
		for _, rel := range spec.DefaultRels {
			out = append(out, filepath.Join(home, filepath.FromSlash(rel)))
		}
		return out
	}
	return []string{filepath.Join(home, filepath.FromSlash(spec.DefaultRel))}
}

// existingSourcePaths 返回实际存在的存储根（可能多个）。
func existingSourcePaths(spec sourceSpec, home string, roots map[string]string) []string {
	paths := sourcePaths(spec, home, roots)
	found := make([]string, 0, len(paths))
	for _, p := range paths {
		if sourceExists(spec, p) {
			found = append(found, p)
		}
	}
	return found
}

func sourcesCap(in input) (any, error) {
	home, err := resolveHome(in)
	if err != nil {
		return nil, err
	}
	statuses := make([]sourceStatus, 0)
	for _, spec := range allSpecs() {
		paths := sourcePaths(spec, home, in.Roots)
		existing := existingSourcePaths(spec, home, in.Roots)
		status := sourceStatus{ID: spec.ID, Label: spec.Label, Kind: spec.Kind, Path: strings.Join(paths, " | "), Exists: len(existing) > 0}
		switch {
		case !status.Exists:
			status.Supported = false
			status.Note = "未发现会话存储"
		default:
			status.Supported = true
			switch spec.Kind {
			case "vscode-chat":
				status.Files = countVSCodeChatSessions(existing)
			case "claude-desktop":
				status.Files = countClaudeDesktopSessions(existing)
			}
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
	Source    string `json:"source"`
	SessionID string `json:"session_id"`
	Project   string `json:"project,omitempty"`
	Feature   string `json:"feature,omitempty"`
	Directory string `json:"directory,omitempty"`
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

func scanCap(in input) (any, error) {
	home, err := resolveHome(in)
	if err != nil {
		return nil, err
	}
	from, to, date := resolveWindow(in)
	start, end, from, to, err := resolveRange(from, to, date)
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

	selected := strings.TrimSpace(in.Source)
	if selected == "" {
		selected = "all"
	}
	selected = normalizeSourceID(selected)

	// 通用可配置来源（内置预设 + 配置文件 + 内联规格）。
	genericSpecs, configPath, err := loadGenericSources(in, home)
	if err != nil {
		return nil, err
	}

	// 逐自然日扫描：跨天的会话会分别归入各自活动日的素材，与「单日运行」的归日口径
	// 完全一致。lookback_days>1 时，靠写入侧「来源+会话+日期」的稳定指纹幂等合并，
	// 把前一天较晚发生、当天定时运行时还没出现的工作在次日补回，不会重复计入。
	digests := make([]sessionDigest, 0)
	skipped := make([]map[string]any, 0)
	skipSeen := map[string]bool{}
	for dayStart := start; dayStart.Before(end); dayStart = dayStart.AddDate(0, 0, 1) {
		dayEnd := dayStart.AddDate(0, 0, 1)
		if dayEnd.After(end) {
			dayEnd = end
		}
		dayDigests, daySkipped := scanSources(in, home, selected, genericSpecs, dayStart, dayEnd)
		digests = append(digests, dayDigests...)
		for _, item := range daySkipped {
			key := fmt.Sprintf("%v|%v", item["source"], item["reason"])
			if skipSeen[key] {
				continue
			}
			skipSeen[key] = true
			skipped = append(skipped, item)
		}
	}

	// 隐私排除规则：命中路径前缀或关键词的会话整条跳过，绝不进入素材与日志库。
	privacy := loadPrivacy(in, home)

	// 按会话标识过滤与去重，并丢弃没有任何用户内容的空会话（例如被误识别的客户端
	// 状态文件，workbuddy 的 window-state 之类），避免空壳污染素材与 AI 上下文。
	filtered := make([]sessionDigest, 0, len(digests))
	seen := map[string]bool{}
	empty := 0
	excluded := 0
	for _, d := range digests {
		if id := strings.TrimSpace(in.SessionID); id != "" && d.SessionID != id {
			continue
		}
		if reason := privacyBlocked(&d, privacy); reason != "" {
			excluded++
			continue
		}
		if len(d.UserPrompts) == 0 && len(d.AssistantExcerpts) == 0 {
			empty++
			continue
		}
		key := d.Source + "|" + d.SessionID + "|" + d.Date
		if seen[key] {
			continue
		}
		seen[key] = true
		trimDigest(&d, maxPrompts, maxChars)
		filtered = append(filtered, d)
	}
	if empty > 0 {
		skipped = append(skipped, map[string]any{"source": "all", "reason": "跳过无用户内容的空会话", "count": empty})
	}
	if excluded > 0 {
		skipped = append(skipped, map[string]any{"source": "all", "reason": "命中隐私排除规则，已跳过", "count": excluded})
	}
	for i := range filtered {
		enrichSessionPath(&filtered[i])
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

// scanSources 扫描一个自然日窗口内的全部会话来源（内置 + 通用可配置），返回素材与
// 跳过原因。按日调用可保证跨天会话分别落到各自活动日。
func scanSources(in input, home, selected string, genericSpecs []genericSource, start, end time.Time) ([]sessionDigest, []map[string]any) {
	digests := make([]sessionDigest, 0)
	skipped := make([]map[string]any, 0)
	for _, spec := range allSpecs() {
		if selected != "all" && selected != spec.ID {
			continue
		}
		existing := existingSourcePaths(spec, home, in.Roots)
		if len(existing) == 0 {
			skipped = append(skipped, map[string]any{"source": spec.ID, "reason": "未发现会话存储", "path": strings.Join(sourcePaths(spec, home, in.Roots), " | ")})
			continue
		}
		var found []sessionDigest
		var scanErr error
		switch spec.ID {
		case "codex":
			found, scanErr = scanCodex(existing[0], start, end)
		case "claude-code":
			found, scanErr = scanClaudeCode(existing[0], start, end)
		case "claude-desktop":
			found, scanErr = scanClaudeDesktop(existing, home, start, end)
		case "vscode":
			found, scanErr = scanVSCodeCopilot(existing, start, end)
		case "opencode":
			found, scanErr = scanOpenCode(existing[0], start, end)
		case "github-copilot":
			found, scanErr = scanCopilot(existing[0], start, end)
		}
		if scanErr != nil {
			skipped = append(skipped, map[string]any{"source": spec.ID, "reason": scanErr.Error(), "path": strings.Join(existing, " | ")})
			continue
		}
		digests = append(digests, found...)
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
	return digests, skipped
}

// writeMaterial 把会话素材写成一份符合 session-digest schema 的 JSON 文件，并返回
// 供工作流引用的 artifact 描述（含 file:// URI 与 sha256）。
//
// 逐个候选目录真实写入，第一个成功者生效：插件宿主可能以受限令牌运行（沙箱只放行
// 工作区与临时目录），固定目录未必可写；一次写入失败就整条工作流失败代价太大。
// 全部候选都失败时把每个候选的真实系统错误带出来，避免只回一句「写入失败」。
func writeMaterial(in input, from, to string, content map[string]any) (map[string]any, error) {
	payload, err := json.MarshalIndent(content, "", "  ")
	if err != nil {
		return nil, errors.New("会话素材序列化失败")
	}
	roots := materialRoots(in)
	if len(roots) == 0 {
		return nil, errors.New("会话素材写入失败：没有可用的素材目录")
	}
	name := fmt.Sprintf("session-digest-%s_%s.json", from, to)
	failures := make([]string, 0, len(roots))
	for _, root := range roots {
		if err := os.MkdirAll(root, 0o755); err != nil {
			failures = append(failures, fmt.Sprintf("%s：目录创建失败（%v）", root, err))
			continue
		}
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, payload, 0o644); err != nil {
			failures = append(failures, fmt.Sprintf("%s：写入失败（%v）", path, err))
			continue
		}
		sum := sha256.Sum256(payload)
		return map[string]any{
			"artifact_id":   "session-digest",
			"artifact_type": "session_digest",
			"name":          "会话素材 " + from + " ~ " + to,
			"uri":           "file:///" + strings.ReplaceAll(normalizeLocalPath(path), "\\", "/"),
			"sha256":        hex.EncodeToString(sum[:]),
		}, nil
	}
	return nil, fmt.Errorf("会话素材写入失败：%s", strings.Join(failures, "；"))
}

// materialRoots 按优先级列出会话素材目录候选：
//  1. 显式 material_root（工作流或调试指定）
//  2. 扩展私有数据目录（HIMIND_PLUGIN_DATA_ROOT，缺省 <LOCALAPPDATA>/HiMindAgent/extension-data/<id>）
//  3. <workspace_root>/.himind/material（引擎注入的运行工作区）
//  4. <HIMIND_AI_WORKSPACE>/.himind/material
//  5. <进程当前目录>/.himind/material
//  6. <系统临时目录>/HiMindAgent/<id>/material
//
// 前两项是历史行为，未受权限限制时素材仍落在扩展私有目录，行为不变。
func materialRoots(in input) []string {
	roots := make([]string, 0, 6)
	add := func(path string) {
		path = normalizeLocalPath(path)
		if path == "" {
			return
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return
		}
		abs = normalizeLocalPath(abs)
		for _, existing := range roots {
			if strings.EqualFold(existing, abs) {
				return
			}
		}
		roots = append(roots, abs)
	}
	// 只有绝对路径才算真实工作区：宿主没有解析 input.workspace_root 时传下来的是字面量，
	// 不能按相对目录落到进程当前目录里。
	addWorkspace := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || !filepath.IsAbs(value) {
			return
		}
		add(filepath.Join(value, ".himind", "material"))
	}

	add(in.MaterialRoot)
	if base := strings.TrimSpace(os.Getenv("HIMIND_PLUGIN_DATA_ROOT")); base != "" {
		add(filepath.Join(base, "material"))
	} else if local := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); local != "" {
		add(filepath.Join(local, "HiMindAgent", "extension-data", "com.himind.work-log-collector", "material"))
	}
	addWorkspace(in.WorkspaceRoot)
	addWorkspace(os.Getenv("HIMIND_AI_WORKSPACE"))
	if cwd, err := os.Getwd(); err == nil {
		addWorkspace(cwd)
	}
	add(filepath.Join(os.TempDir(), "HiMindAgent", "com.himind.work-log-collector", "material"))
	return roots
}

// normalizeLocalPath 去掉宿主传入的 Win32 扩展长度前缀（\\?\、\??\、\\?\UNC\）：
// 宿主把工作区按 \\?\F:\... 形式传给插件，而工作流引擎要求素材 artifact 的 file URI
// 是普通本地路径，带前缀会被判成「不是本地文件 URI」而让整步失败。
func normalizeLocalPath(path string) string {
	trimmed := strings.TrimSpace(path)
	switch {
	case strings.HasPrefix(trimmed, `\\?\UNC\`):
		return `\\` + trimmed[len(`\\?\UNC\`):]
	case strings.HasPrefix(trimmed, `\\?\`):
		return trimmed[len(`\\?\`):]
	case strings.HasPrefix(trimmed, `\??\`):
		return trimmed[len(`\??\`):]
	default:
		return trimmed
	}
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

// featureFromRepo 取会话工作目录相对仓库根（repo_path）的末级目录作为「功能/模块」。
func featureFromRepo(directory, repoPath string) string {
	directory = strings.TrimSpace(directory)
	repoPath = strings.TrimSpace(repoPath)
	if directory == "" || repoPath == "" {
		return ""
	}
	rel, err := filepath.Rel(filepath.Clean(repoPath), filepath.Clean(directory))
	if err != nil || rel == "." || rel == "" || strings.HasPrefix(rel, "..") {
		return ""
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	last := strings.TrimSpace(parts[len(parts)-1])
	if last == "" || last == "." {
		return ""
	}
	return last
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

// gitMainRoot 把「最近 git 根」解析成真正的主仓库工作树：链接工作树（git worktree add
// 出来的目录）里 `.git` 是一个文件，内容为 `gitdir: <主仓库>/.git/worktrees/<名>`；把它
// 截到 `/.git/` 之前即主仓库工作树根。嵌套模块/子模块（gitdir 更深）同样按此截断。
// 不是链接工作树（`.git` 是目录）或无法解析时返回空。
func gitMainRoot(root string) string {
	gi := filepath.Join(root, ".git")
	info, err := os.Stat(gi)
	if err != nil || info.IsDir() {
		return ""
	}
	body, err := os.ReadFile(gi)
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(body))
	line = strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
	if line == "" {
		return ""
	}
	normalized := filepath.ToSlash(line)
	// 兼容 `gitdir: /abs/path` 与 `gitdir: ../relative`（相对工作树目录）。
	if !strings.HasPrefix(normalized, "/") && !isWindowsAbs(normalized) {
		normalized = filepath.ToSlash(filepath.Join(root, filepath.FromSlash(normalized)))
	}
	idx := strings.Index(strings.ToLower(normalized), "/.git/")
	if idx <= 0 {
		return ""
	}
	mainRoot := strings.TrimSpace(filepath.FromSlash(normalized[:idx]))
	if mainRoot == "" {
		return ""
	}
	return filepath.Clean(mainRoot)
}

// isWindowsAbs 判断路径是否为 Windows 盘符绝对路径（形如 C:/...）。
func isWindowsAbs(p string) bool {
	if len(p) < 3 {
		return false
	}
	c := p[0]
	if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
		return false
	}
	return p[1] == ':' && (p[2] == '/' || p[2] == '\\')
}

// resolveRepoRoot 返回会话工作目录对应的稳定仓库根：链接工作树解析到主仓库工作树，
// 否则退回普通 git 根。
func resolveRepoRoot(dir string) string {
	root := gitRoot(dir)
	if root == "" {
		return ""
	}
	if main := gitMainRoot(root); main != "" {
		return main
	}
	return root
}

// vcsRoot 向上查找最近的版本库根（如 .svn 目录），找不到返回空。
func vcsRoot(dir, marker string) string {
	for {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// enrichSessionPath 用会话工作目录补齐稳定路径事实 repo_path：优先仓库自述清单
// .himind/project.json（同时读取声明的项目/展项），其次 git/svn 仓库根，最后退回工作目录本身。
func enrichSessionPath(d *sessionDigest) {
	dir := strings.TrimSpace(d.Directory)
	if dir == "" {
		return
	}
	clean := filepath.Clean(dir)
	if root, project, exhibit := manifestAt(clean); root != "" {
		d.RepoPath = root
		if project != "" {
			d.Project = project
		}
		if exhibit != "" {
			d.Exhibit = exhibit
		}
		return
	}
	if root := gitRoot(clean); root != "" {
		// 链接工作树（如 .copilot 的 copilot-worktrees）解析到主仓库工作树，避免把
		// C 盘缓存里的工作树目录当成项目主路径。
		if main := gitMainRoot(root); main != "" {
			d.RepoPath = main
		} else {
			d.RepoPath = root
		}
		return
	}
	if root := vcsRoot(clean, ".svn"); root != "" {
		d.RepoPath = root
		return
	}
	d.RepoPath = clean
}

// manifestAt 从目录向上查找 .himind/project.json，返回其所在目录与声明的项目/展项。
func manifestAt(dir string) (root, project, exhibit string) {
	for {
		candidate := filepath.Join(dir, ".himind", "project.json")
		if body, err := os.ReadFile(candidate); err == nil {
			p, e := parseProjectManifest(body)
			return dir, p, e
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", ""
		}
		dir = parent
	}
}

// parseProjectManifest 从工程清单里尽力解析项目/展项名（兼容不同键名与嵌套对象）。
func parseProjectManifest(body []byte) (string, string) {
	var raw map[string]any
	if json.Unmarshal(body, &raw) != nil {
		return "", ""
	}
	project := manifestText(raw, "project_name", "projectName", "project")
	exhibit := manifestText(raw, "exhibit_name", "exhibitName", "exhibit", "target_exhibit", "targetExhibit")
	if project == "" {
		if nested, ok := raw["project"].(map[string]any); ok {
			project = manifestText(nested, "name", "project_name")
		}
	}
	if exhibit == "" {
		if nested, ok := raw["exhibit"].(map[string]any); ok {
			exhibit = manifestText(nested, "name", "exhibit_name")
		}
	}
	return strings.TrimSpace(project), strings.TrimSpace(exhibit)
}

// manifestText 取第一个非空字符串键（键名直取，不做点路径遍历）。
func manifestText(raw map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := raw[key].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
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
			feature = featureFromRepo(digest.Directory, digest.RepoPath)
		}
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
		if minutes > 0 {
			item["minutes"] = minutes
			item["minutes_source"] = minutesSource
		}
		entries = append(entries, item)
	}
	return entries
}

// resolveWindow 解析目标窗口：显式 from/to/date 优先；三者都为空时按 lookback_days
// 取 [今天-(n-1), 今天]，默认 n=1（即仅今天），n=2 时为「昨天+今天」。
func resolveWindow(in input) (string, string, string) {
	from := strings.TrimSpace(in.From)
	to := strings.TrimSpace(in.To)
	date := strings.TrimSpace(in.Date)
	if from == "" && to == "" && date == "" {
		days := in.LookbackDays
		if days < 1 {
			days = 1
		}
		now := time.Now()
		to = now.Format(dateLayout)
		from = now.AddDate(0, 0, -(days - 1)).Format(dateLayout)
	}
	return from, to, date
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
		// 用文件修改时间做廉价预筛：窗口开始前就没再写过，文件里不可能有窗口内记录。
		// 不再按文件名日期判断，避免把「跨日续写」的长会话文件整份跳过。
		if info, statErr := os.Stat(file); statErr == nil && info.ModTime().Before(start) {
			continue
		}
		digest, ok, err := parseCodexFile(file, start, end)
		if err != nil || !ok {
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

func parseCodexFile(file string, start, end time.Time) (sessionDigest, bool, error) {
	handle, err := os.Open(file)
	if err != nil {
		return sessionDigest{}, false, err
	}
	defer handle.Close()

	digest := sessionDigest{Source: "codex", Ref: file}
	var created time.Time
	var winTimes []time.Time
	var userPrompts, assistantExcerpts []string
	decoder := json.NewDecoder(handle)
	for {
		var record codexRecord
		if err := decoder.Decode(&record); err != nil {
			break
		}
		var ts time.Time
		if parsed, err := time.Parse(time.RFC3339, record.Timestamp); err == nil {
			ts = parsed
			if created.IsZero() || ts.Before(created) {
				created = ts
			}
		}
		inWindow := !ts.IsZero() && inRange(ts, start, end)
		if inWindow {
			winTimes = append(winTimes, ts)
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
			}
		case "response_item":
			// 只把落在窗口内的对话计入当日素材，长会话据此按活跃日切分。
			if !inWindow {
				continue
			}
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
	started, ended := activityRange(winTimes, created, created)
	if started.IsZero() {
		return sessionDigest{}, false, nil
	}
	if len(winTimes) == 0 && !inRange(created, start, end) {
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
	digest.Minutes, digest.MinutesSource = resolveMinutes(winTimes, started, ended, digest.TurnCount)
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

// activityRange 从「窗口内消息时间点」得出会话在本窗口内的起止时间；窗口内没有任何
// 时间点时退回兜底时间（通常是会话自身的创建/更新时间）。跨多日的长会话因此可以在
// 每个活跃日各算一段，而不是只算创建当天。
func activityRange(times []time.Time, fallbackStart, fallbackEnd time.Time) (time.Time, time.Time) {
	var started, ended time.Time
	for _, t := range times {
		if t.IsZero() {
			continue
		}
		if started.IsZero() || t.Before(started) {
			started = t
		}
		if t.After(ended) {
			ended = t
		}
	}
	if started.IsZero() {
		started = fallbackStart
	}
	if ended.IsZero() {
		ended = fallbackEnd
	}
	return started, ended
}

// ---- Claude Code 索引 ----

type claudeCodeIndex struct {
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

func scanClaudeCode(root string, start, end time.Time) ([]sessionDigest, error) {
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
		var index claudeCodeIndex
		if json.Unmarshal(data, &index) != nil {
			return nil
		}
		for _, item := range index.Entries {
			created, createdErr := time.Parse(time.RFC3339, item.Created)
			modified, _ := time.Parse(time.RFC3339, item.Modified)
			createdIn := createdErr == nil && inRange(created, start, end)
			modifiedIn := !modified.IsZero() && inRange(modified, start, end)
			// 会话可能创建于更早、但当天仍在活跃：只要创建或最后修改落在窗口内就纳入，
			// 归日取窗口内的时间点，避免长会话在后续活跃日整体丢失。
			if !createdIn && !modifiedIn {
				continue
			}
			started, ended := created, modified
			if !createdIn {
				started = modified
			}
			digest := sessionDigest{
				Source:      "claude-code",
				SessionID:   item.SessionID,
				Project:     filepath.Base(filepath.Clean(item.ProjectPath)),
				Directory:   item.ProjectPath,
				Title:       firstLine(item.FirstPrompt, 80),
				Date:        started.Local().Format(dateLayout),
				StartedAt:   started.Format(time.RFC3339),
				TurnCount:   item.MessageCount,
				UserPrompts: nonEmpty([]string{item.FirstPrompt}),
				Ref:         path,
			}
			if !ended.IsZero() {
				digest.EndedAt = ended.Format(time.RFC3339)
			}
			digest.Minutes, digest.MinutesSource = resolveMinutes(nil, started, ended, digest.TurnCount)
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

func scanOpenCode(db string, start, end time.Time) ([]sessionDigest, error) {
	startMs := start.UnixMilli()
	endMs := end.UnixMilli()
	sessions, err := querySQLiteJSON(db, fmt.Sprintf(
		"SELECT id, project_id, slug, directory, title, model, cost, tokens_input, tokens_output, time_created, time_updated FROM session_v2 "+
			"WHERE time_created < %d AND (time_created >= %d OR id IN (SELECT DISTINCT session_id FROM session_message WHERE time_created >= %d AND time_created < %d)) "+
			"ORDER BY time_created DESC",
		endMs, startMs, startMs, endMs))
	if err != nil {
		return nil, err
	}
	projects := map[string]string{}
	if rows, err := querySQLiteJSON(db, "SELECT id, name, worktree FROM project"); err == nil {
		for _, row := range rows {
			name := asString(row, "name")
			if name == "" {
				name = filepath.Base(asString(row, "worktree"))
			}
			projects[asString(row, "id")] = name
		}
	}
	messages, err := querySQLiteJSON(db, fmt.Sprintf(
		"SELECT session_id, type, data, time_created FROM session_message WHERE time_created >= %d AND time_created < %d ORDER BY time_created",
		startMs, endMs))
	if err != nil {
		return nil, err
	}
	prompts := map[string][]string{}
	excerpts := map[string][]string{}
	msgTimes := map[string][]time.Time{}
	actTimes := map[string][]time.Time{}
	for _, row := range messages {
		id := asString(row, "session_id")
		if created := asInt64(row, "time_created"); created > 0 {
			actTimes[id] = append(actTimes[id], time.UnixMilli(created))
		}
		text := extractText(asString(row, "data"))
		if strings.TrimSpace(text) == "" {
			continue
		}
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
		// 会话可能创建于更早、但当天仍在活跃：归日与起止都取窗口内活动时间，
		// 长会话据此在每个活跃日各算一段，而不是只算创建当天。
		started, ended := activityRange(actTimes[id], created, updated)
		digest := sessionDigest{
			Source:            "opencode",
			SessionID:         id,
			Project:           projects[asString(row, "project_id")],
			Directory:         asString(row, "directory"),
			Title:             asString(row, "title"),
			Date:              started.Local().Format(dateLayout),
			StartedAt:         started.Format(time.RFC3339),
			UserPrompts:       prompts[id],
			AssistantExcerpts: excerpts[id],
			Cost:              asFloat(row, "cost"),
			TokensInput:       asInt64(row, "tokens_input"),
			TokensOutput:      asInt64(row, "tokens_output"),
			Ref:               db + "#" + id,
		}
		if !ended.IsZero() {
			digest.EndedAt = ended.Format(time.RFC3339)
		}
		digest.TurnCount = len(digest.UserPrompts)
		digest.Minutes, digest.MinutesSource = resolveMinutes(msgTimes[id], started, ended, digest.TurnCount)
		digests = append(digests, digest)
	}
	return digests, nil
}

// ---- Copilot SQLite ----

func scanCopilot(db string, start, end time.Time) ([]sessionDigest, error) {
	fromText := start.Format("2006-01-02 15:04:05")
	toText := end.Format("2006-01-02 15:04:05")
	sessions, err := querySQLiteJSON(db, fmt.Sprintf(
		"SELECT id, cwd, repository, summary, created_at, updated_at FROM sessions "+
			"WHERE created_at < '%s' AND (created_at >= '%s' OR id IN (SELECT DISTINCT session_id FROM turns WHERE timestamp >= '%s' AND timestamp < '%s')) "+
			"ORDER BY created_at DESC",
		toText, fromText, fromText, toText))
	if err != nil {
		return nil, err
	}
	turns, err := querySQLiteJSON(db, fmt.Sprintf(
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
		// 会话可能创建于更早、但当天仍在活跃：归日取窗口内的轮次时间。
		started, ended := activityRange(turnTimes[id], created, updated)
		digest := sessionDigest{
			Source:            "github-copilot",
			SessionID:         id,
			Project:           firstNonEmpty(asString(row, "repository"), filepath.Base(asString(row, "cwd"))),
			Directory:         asString(row, "cwd"),
			Title:             title,
			Date:              started.Local().Format(dateLayout),
			StartedAt:         started.Format(time.RFC3339),
			UserPrompts:       prompts[id],
			AssistantExcerpts: excerpts[id],
			Ref:               db + "#" + id,
		}
		if !ended.IsZero() {
			digest.EndedAt = ended.Format(time.RFC3339)
		}
		digest.TurnCount = len(digest.UserPrompts)
		digest.Minutes, digest.MinutesSource = resolveMinutes(turnTimes[id], started, ended, digest.TurnCount)
		digests = append(digests, digest)
	}
	return digests, nil
}

// ---- VS Code Copilot Chat ----

// vscodePatchLine 是 VS Code 会话文件的增量补丁行：k 是字段路径，v 是值。
// kind 0=初始状态，1=按路径赋值，2=向路径上的数组追加。
type vscodePatchLine struct {
	Kind int             `json:"kind"`
	K    []any           `json:"k"`
	V    json.RawMessage `json:"v"`
}

type vscodeChatResponsePart struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type vscodeChatRequest struct {
	RequestID string `json:"requestId"`
	Timestamp int64  `json:"timestamp"`
	Message   struct {
		Text string `json:"text"`
	} `json:"message"`
	Response   []vscodeChatResponsePart `json:"response"`
	ModelState struct {
		CompletedAt int64 `json:"completedAt"`
	} `json:"modelState"`
}

type vscodeChatHeader struct {
	Version      int                 `json:"version"`
	CreationDate int64               `json:"creationDate"`
	CustomTitle  string              `json:"customTitle"`
	SessionID    string              `json:"sessionId"`
	Requests     []vscodeChatRequest `json:"requests"`
}

// scanVSCodeCopilot 扫描一个或多个 VS Code workspaceStorage 根下的 Copilot Chat 会话
// （稳定版与 Insiders 可同时覆盖）。
func scanVSCodeCopilot(roots []string, start, end time.Time) ([]sessionDigest, error) {
	var digests []sessionDigest
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			wsDir := filepath.Join(root, entry.Name())
			chatDir := filepath.Join(wsDir, "chatSessions")
			if !dirExists(chatDir) {
				continue
			}
			dir := vscodeWorkspaceFolder(wsDir)
			files, err := os.ReadDir(chatDir)
			if err != nil {
				continue
			}
			for _, file := range files {
				if file.IsDir() || !strings.HasSuffix(file.Name(), ".jsonl") {
					continue
				}
				// 文件最后修改时间早于区间起点，说明当天没有活动，快速跳过。
				if info, err := file.Info(); err == nil && info.ModTime().Before(start) {
					continue
				}
				digest, ok := parseVSCodeChatFile(filepath.Join(chatDir, file.Name()), dir, start, end)
				if !ok {
					continue
				}
				digests = append(digests, digest)
			}
		}
	}
	return digests, nil
}

// parseVSCodeChatFile 回放 VS Code 会话文件的补丁序列，抽出会话标识、时间、用户提问与助手回答。
// 只把落在 [start, end) 窗口内的请求计入素材；跨多日的长会话据此在每个活跃日各算一段。
func parseVSCodeChatFile(file, dir string, start, end time.Time) (sessionDigest, bool) {
	handle, err := os.Open(file)
	if err != nil {
		return sessionDigest{}, false
	}
	defer handle.Close()

	reader := bufio.NewReaderSize(handle, 1<<20)
	var header vscodeChatHeader
	receivedHeader := false
	requests := make([]vscodeChatRequest, 0)
	completed := int64(0)
	for {
		line, err := reader.ReadBytes('\n')
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) > 0 {
			var patch vscodePatchLine
			if json.Unmarshal(trimmed, &patch) == nil {
				switch patch.Kind {
				case 0:
					if json.Unmarshal(patch.V, &header) == nil {
						receivedHeader = true
						requests = append(requests, header.Requests...)
					}
				case 2:
					// 追加新请求：k == ["requests"]
					if len(patch.K) == 1 && patchKeyName(patch.K[0]) == "requests" {
						var items []vscodeChatRequest
						if json.Unmarshal(patch.V, &items) == nil {
							requests = append(requests, items...)
						}
					}
					// 追加请求响应分片：k == ["requests", i, "response"]
					if len(patch.K) == 3 && patchKeyName(patch.K[0]) == "requests" && patchKeyName(patch.K[2]) == "response" {
						if idx := patchKeyIndex(patch.K[1]); idx >= 0 && idx < len(requests) {
							var items []vscodeChatResponsePart
							if json.Unmarshal(patch.V, &items) == nil {
								requests[idx].Response = append(requests[idx].Response, items...)
							}
						}
					}
				case 1:
					// 记录请求完成时间：k == ["requests", i, "modelState"]
					if len(patch.K) == 3 && patchKeyName(patch.K[0]) == "requests" && patchKeyName(patch.K[2]) == "modelState" {
						if idx := patchKeyIndex(patch.K[1]); idx >= 0 && idx < len(requests) {
							var state struct {
								CompletedAt int64 `json:"completedAt"`
							}
							if json.Unmarshal(patch.V, &state) == nil && state.CompletedAt > completed {
								completed = state.CompletedAt
							}
						}
					}
				}
			}
		}
		if err != nil {
			break
		}
	}
	if !receivedHeader || strings.TrimSpace(header.SessionID) == "" {
		return sessionDigest{}, false
	}

	digest := sessionDigest{Source: "vscode", SessionID: header.SessionID, Directory: dir, Ref: file}
	if dir != "" {
		digest.Project = filepath.Base(dir)
	}
	digest.Title = firstLine(header.CustomTitle, 80)

	created := timeFromMillis(header.CreationDate)
	var times []time.Time
	for _, req := range requests {
		ts := timeFromMillis(req.Timestamp)
		if ts.IsZero() || !inRange(ts, start, end) {
			continue
		}
		times = append(times, ts)
		if text := cleanVSCodeText(req.Message.Text); strings.TrimSpace(text) != "" {
			digest.UserPrompts = append(digest.UserPrompts, text)
			if digest.Title == "" {
				digest.Title = firstLine(text, 80)
			}
		}
		if text := vscodeResponseText(req.Response); strings.TrimSpace(text) != "" {
			digest.AssistantExcerpts = append(digest.AssistantExcerpts, firstLine(text, 240))
		}
	}
	if done := timeFromMillis(completed); !done.IsZero() && inRange(done, start, end) {
		times = append(times, done)
	}
	started, ended := activityRange(times, created, created)
	if started.IsZero() {
		return sessionDigest{}, false
	}
	if len(times) == 0 && !inRange(created, start, end) {
		return sessionDigest{}, false
	}
	digest.StartedAt = started.Format(time.RFC3339)
	if !ended.IsZero() {
		digest.EndedAt = ended.Format(time.RFC3339)
	}
	digest.Date = started.Local().Format(dateLayout)
	digest.TurnCount = len(digest.UserPrompts)
	digest.Minutes, digest.MinutesSource = resolveMinutes(times, started, ended, digest.TurnCount)
	return digest, true
}

// vscodeResponseText 汇总响应分片里可作为素材摘要的文本。
func vscodeResponseText(parts []vscodeChatResponsePart) string {
	var out []string
	for _, part := range parts {
		switch part.Kind {
		case "text", "markdown", "thinking", "progress":
			if strings.TrimSpace(part.Value) != "" {
				out = append(out, part.Value)
			}
		}
	}
	return strings.Join(out, "\n")
}

// cleanVSCodeText 去掉 VS Code 注入的附件/上下文块，尽量只留用户真实问题。
func cleanVSCodeText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if strings.HasPrefix(text, "<") {
		if idx := strings.LastIndex(text, "</attachments>"); idx >= 0 {
			text = strings.TrimSpace(text[idx+len("</attachments>"):])
		}
		if idx := strings.LastIndex(text, "</context>"); idx >= 0 {
			text = strings.TrimSpace(text[idx+len("</context>"):])
		}
	}
	return text
}

// vscodeWorkspaceFolder 从 workspaceStorage/<hash>/workspace.json 解析该工作区对应的本地目录。
func vscodeWorkspaceFolder(wsDir string) string {
	data, err := os.ReadFile(filepath.Join(wsDir, "workspace.json"))
	if err != nil {
		return ""
	}
	var meta struct {
		Folder    string `json:"folder"`
		Workspace string `json:"workspace"`
	}
	if json.Unmarshal(data, &meta) != nil {
		return ""
	}
	path := decodeFileURI(firstNonEmpty(meta.Folder, meta.Workspace))
	if path == "" {
		return ""
	}
	if strings.HasSuffix(strings.ToLower(path), ".code-workspace") {
		return filepath.Dir(path)
	}
	return path
}

// decodeFileURI 把 file:///c%3A/... 这类 URI 还原成本地路径。
func decodeFileURI(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(s), "file://") {
		s = s[len("file://"):]
	}
	if unescaped, err := url.PathUnescape(s); err == nil {
		s = unescaped
	}
	if len(s) >= 3 && s[0] == '/' && s[2] == ':' {
		s = s[1:]
	}
	return filepath.FromSlash(s)
}

func timeFromMillis(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

func patchKeyName(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func patchKeyIndex(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return -1
}

// countVSCodeChatSessions 统计多个 workspaceStorage 根下的会话文件数。
func countVSCodeChatSessions(roots []string) int {
	total := 0
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			files, err := os.ReadDir(filepath.Join(root, entry.Name(), "chatSessions"))
			if err != nil {
				continue
			}
			for _, file := range files {
				if !file.IsDir() && strings.HasSuffix(file.Name(), ".jsonl") {
					total++
				}
			}
		}
	}
	return total
}

// ---- 隐私排除 ----

// privacyConfig 是隐私排除规则：路径前缀与关键词黑名单。命中的会话整条跳过。
type privacyConfig struct {
	ExcludePaths    []string `json:"exclude_paths"`
	ExcludeKeywords []string `json:"exclude_keywords"`
}

// loadPrivacy 合并隐私规则：先读 <log_root>/config/privacy.json（或显式 privacy_config_path），
// 再叠加调用方内联传入的 exclude_paths / exclude_keywords。
func loadPrivacy(in input, home string) privacyConfig {
	cfg := privacyConfig{}
	path := strings.TrimSpace(in.PrivacyConfigPath)
	if path == "" {
		if root := strings.TrimSpace(expandPath(in.LogRoot, home)); root != "" {
			path = filepath.Join(root, "config", "privacy.json")
		}
	}
	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(data, &cfg)
		}
	}
	cfg.ExcludePaths = append(cfg.ExcludePaths, in.ExcludePaths...)
	cfg.ExcludeKeywords = append(cfg.ExcludeKeywords, in.ExcludeKeywords...)
	return cfg
}

// privacyBlocked 判断会话是否命中隐私排除规则，返回命中原因（空串表示不排除）。
func privacyBlocked(d *sessionDigest, cfg privacyConfig) string {
	dir := toSlashLower(d.Directory)
	repo := toSlashLower(d.RepoPath)
	for _, raw := range cfg.ExcludePaths {
		prefix := toSlashLower(strings.TrimSpace(raw))
		if prefix == "" {
			continue
		}
		if strings.HasPrefix(dir, prefix) || strings.HasPrefix(repo, prefix) {
			return "命中隐私排除路径"
		}
	}
	if len(cfg.ExcludeKeywords) > 0 {
		haystack := strings.ToLower(strings.Join([]string{
			d.Title,
			strings.Join(d.UserPrompts, "\n"),
			strings.Join(d.AssistantExcerpts, "\n"),
		}, "\n"))
		for _, raw := range cfg.ExcludeKeywords {
			needle := strings.ToLower(strings.TrimSpace(raw))
			if needle != "" && strings.Contains(haystack, needle) {
				return "命中隐私排除关键词"
			}
		}
	}
	return ""
}

// toSlashLower 归一化路径：反斜杠转正斜杠并转小写，便于大小写不敏感的前缀匹配。
func toSlashLower(value string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "\\", "/"))
}

// ---- SQLite 辅助 ----

// querySQLiteJSON 用内置的纯 Go SQLite 驱动只读查询会话库，无需外部 sqlite3 CLI。
func querySQLiteJSON(db, query string) ([]map[string]any, error) {
	dsn := "file:" + filepath.ToSlash(db) + "?mode=ro"
	handle, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开会话库失败: %w", err)
	}
	defer handle.Close()

	rows, err := handle.Query(query)
	if err != nil {
		return nil, fmt.Errorf("读取会话库失败: %w", err)
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0)
	for rows.Next() {
		cells := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(columns))
		for i, name := range columns {
			row[name] = normalizeSQLiteValue(cells[i])
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// normalizeSQLiteValue 把驱动返回值收敛成 JSON 友好的基础类型。
func normalizeSQLiteValue(value any) any {
	switch v := value.(type) {
	case []byte:
		return string(v)
	case time.Time:
		return v.Format(time.RFC3339)
	default:
		return v
	}
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

// sourceExists 按来源类型判断存储是否存在：目录型（JSONL / VS Code / Claude Desktop 会话）是目录，SQLite 源是文件。
func sourceExists(spec sourceSpec, path string) bool {
	if spec.Kind == "jsonl" || spec.Kind == "vscode-chat" || spec.Kind == "claude-desktop" {
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
