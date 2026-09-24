package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/MrBaoquan/himind-extensions/sdk/jsonrpc"
)

const ruleVersion = "tech-radar-rules.v1"

// extensionDataNamespace 是扩展私有数据目录的命名空间；
// Agent 注入 HIMIND_PLUGIN_DATA_ROOT 时优先使用，否则落到本机用户目录。
const extensionDataNamespace = "com.himind.tech-radar"

// githubSearchBase 可被测试替换，生产固定为公开 API。
var githubSearchBase = "https://api.github.com/search/repositories"

var httpClient = &http.Client{Timeout: 30 * time.Second}

type input struct {
	// Domains 是领域预设：cv / llm / ar-vr / general。
	Domains []string `json:"domains"`
	// Topics 是自定义领域（OR 组，任一标签命中即可）；与 Domains 可同时使用。
	Topics []string `json:"topics"`
	// ExtraTopics 追加到每个领域的 OR 词，用来临时扩面。
	ExtraTopics   []string `json:"extra_topics"`
	Languages     []string `json:"languages"`
	ExcludeOwners []string `json:"exclude_owners"`
	// MinStars / NewMinStars 是两条赛道的星数下限，避免噪声。
	MinStars    int `json:"min_stars"`
	NewMinStars int `json:"new_min_stars"`
	// 快照要同时喂给报告和 AI 解读，必须控制单条体积与总条数（提示词走命令行，
	// Windows 有 ~32KB 上限）。
	MaxEntries          int `json:"max_entries"`
	DescriptionMaxChars int `json:"description_max_chars"`
	TopicsMax           int `json:"topics_max"`
	// QueryIntervalMs / MaxQueries 控制查询节奏与总量（未鉴权时 GitHub 搜索限流 10 次/分钟）。
	QueryIntervalMs int            `json:"query_interval_ms"`
	MaxQueries      int            `json:"max_queries"`
	WindowDays      int            `json:"window_days"`
	TopN            int            `json:"top_n"`
	ReportRoot      string         `json:"report_root"`
	ReportDate      string         `json:"report_date"`
	Snapshot        map[string]any `json:"snapshot"`
	Insight         map[string]any `json:"insight"`
	Summary         string         `json:"summary"`
	URL             string         `json:"url"`
	// 工作流给每一步注入的上下文：上游步骤输出都在 workflow_context.steps 里。
	// 具体的步骤 id 由工作流在 step.input 里声明，扩展不猜工作流结构。
	WorkflowContext map[string]any `json:"workflow_context"`
	SnapshotStep    string         `json:"snapshot_step"`
	InsightStep     string         `json:"insight_step"`
}

type repoEntry struct {
	EntryID     string   `json:"entry_id"`
	FullName    string   `json:"full_name"`
	HTMLURL     string   `json:"html_url"`
	Description string   `json:"description"`
	Language    string   `json:"language"`
	Stars       int      `json:"stars"`
	StarsGained int      `json:"stars_gained"`
	Topics      []string `json:"topics"`
	FirstSeen   string   `json:"first_seen"`
	RankDelta   int      `json:"rank_delta"`
	IsNew       bool     `json:"is_new"`
	// 下面这些字段用于判断“是不是值得看”，也让报告能自证来源与新鲜度。
	Forks      int      `json:"forks"`
	OpenIssues int      `json:"open_issues"`
	PushedAt   string   `json:"pushed_at"`
	License    string   `json:"license"`
	Homepage   string   `json:"homepage"`
	Archived   bool     `json:"archived"`
	Domains    []string `json:"domains"`
	Lanes      []string `json:"lanes"`
}

type snapshot struct {
	GeneratedAt string         `json:"generated_at"`
	RuleVersion string         `json:"rule_version"`
	WindowDays  int            `json:"window_days"`
	Topics      []string       `json:"topics"`
	Domains     []string       `json:"domains"`
	Lanes       []string       `json:"lanes"`
	MinStars    int            `json:"min_stars"`
	NewMinStars int            `json:"new_min_stars"`
	QueryCount  int            `json:"query_count"`
	TrendBasis  string         `json:"trend_basis"`
	Entries     []repoEntry    `json:"entries"`
	RateLimit   map[string]any `json:"rate_limit"`
}

type historyRecord struct {
	Date    string      `json:"date"`
	Entries []repoEntry `json:"entries"`
}

func main() {
	if err := jsonrpc.Serve(os.Stdin, os.Stdout, handle); err != nil {
		fmt.Fprintln(os.Stderr, "科技雷达插件已停止:", err)
	}
}

func handle(request jsonrpc.Request) (any, *jsonrpc.Error) {
	var in input
	if rpcError := jsonrpc.DecodeParams(request, &in); rpcError != nil {
		return nil, rpcError
	}
	switch request.Method {
	case "tech-radar.collect":
		result, err := collect(in)
		return rpcResult(result, err)
	case "tech-radar.report":
		result, err := render(in)
		return rpcResult(result, err)
	case "tech-radar.archive.list":
		result, err := listArchive(in)
		return rpcResult(result, err)
	case "tech-radar.archive.read":
		result, err := readArchivedReport(in)
		return rpcResult(result, err)
	case "tech-radar.link.open":
		result, err := openLink(in)
		return rpcResult(result, err)
	default:
		return nil, &jsonrpc.Error{Code: -32602, Message: "不支持的科技雷达能力"}
	}
}

func rpcResult(result any, err error) (any, *jsonrpc.Error) {
	if err != nil {
		return nil, &jsonrpc.Error{Code: -32000, Message: err.Error()}
	}
	return result, nil
}

func collect(in input) (any, error) {
	root, err := reportRoot(in.ReportRoot)
	if err != nil {
		return nil, err
	}
	groups, domainNames, err := queryGroups(in)
	if err != nil {
		return nil, err
	}
	window := in.WindowDays
	if window <= 0 {
		window = 7
	}
	topN := in.TopN
	if topN <= 0 {
		topN = 10
	}
	minStars := in.MinStars
	if minStars <= 0 {
		minStars = defaultMinStars
	}
	newMinStars := in.NewMinStars
	if newMinStars <= 0 {
		newMinStars = defaultNewMinStars
	}
	maxEntries := in.MaxEntries
	if maxEntries <= 0 {
		maxEntries = defaultMaxEntries
	}
	descriptionMax := in.DescriptionMaxChars
	if descriptionMax <= 0 {
		descriptionMax = defaultDescriptionMaxChars
	}
	topicsMax := in.TopicsMax
	if topicsMax <= 0 {
		topicsMax = defaultTopicsMax
	}
	truncatedEntries := false
	date := reportDate(in.ReportDate)
	since := time.Now().UTC().AddDate(0, 0, -window).Format("2006-01-02")

	// 两条赛道：
	//   active —— 长期活跃且体量大的基础盘（pushed + stars 下限，按星数排序）
	//   rising —— 窗口内新建但已经起量的新星
	// 只看 created:>date 会得到“刚建仓、几十星”的噪声，这是上一版质量差的根因。
	lanes := []struct {
		id       string
		created  bool
		minStars int
	}{
		{id: "active", created: false, minStars: minStars},
		{id: "rising", created: true, minStars: newMinStars},
	}

	rateLimit := map[string]any{}
	queryCount := 0
	queryBudget := maxQueries(in)
	authenticated := strings.TrimSpace(os.Getenv("HIMIND_TECH_RADAR_GITHUB_TOKEN")) != ""
	interval := queryInterval(in, authenticated)
	truncated := false
	merged := map[string]*repoEntry{}
	order := make([]string, 0, len(groups)*len(lanes)*topN)
	for _, group := range groups {
		for _, lane := range lanes {
			if queryCount >= queryBudget {
				truncated = true
				break
			}
			if queryCount > 0 && interval > 0 {
				// 未鉴权时限流是 10 次/分钟，放慢节奏比被 403 掉更划算。
				time.Sleep(interval)
			}
			query := fmt.Sprintf("topic:%s %s stars:>%d fork:false archived:false", group.topic, pushedOrCreated(lane.created, since), lane.minStars)
			collected, limit, searchErr := searchRepositories(query, in.Languages, topN*4)
			queryCount++
			if searchErr != nil {
				// 单条查询失败（典型是限流）不应毁掉整期报告：记录原因继续跑其它查询。
				if rateLimit["error"] == nil {
					rateLimit["error"] = searchErr.Error()
				}
				continue
			}
			mergeRateLimit(rateLimit, limit)
			// 每条查询只取前 topN 条，控制快照规模；接口已按星数倒序返回。
			if len(collected) > topN {
				collected = collected[:topN]
			}
			for _, entry := range collected {
				key := entry.EntryID
				existing, ok := merged[key]
				if !ok {
					copy := entry
					copy.Domains = []string{group.name}
					copy.Lanes = []string{lane.id}
					merged[key] = &copy
					order = append(order, key)
					continue
				}
				if existing.Stars < entry.Stars {
					existing.Stars = entry.Stars
					existing.Description = entry.Description
					existing.Language = entry.Language
					existing.PushedAt = entry.PushedAt
					existing.Forks = entry.Forks
					existing.OpenIssues = entry.OpenIssues
					existing.License = entry.License
					existing.Homepage = entry.Homepage
				}
				existing.Domains = appendUnique(existing.Domains, group.name)
				existing.Lanes = appendUnique(existing.Lanes, lane.id)
			}
		}
		if truncated {
			break
		}
	}
	if len(merged) == 0 {
		return nil, errors.New("本次没有采集到任何仓库：请检查主题/星数下限，或稍后重试（可能是 GitHub 限流）")
	}

	excluded := map[string]struct{}{}
	for _, owner := range in.ExcludeOwners {
		excluded[strings.ToLower(strings.TrimSpace(owner))] = struct{}{}
	}
	filtered := make([]repoEntry, 0, len(order))
	for _, key := range order {
		entry := *merged[key]
		owner := strings.ToLower(strings.SplitN(entry.FullName, "/", 2)[0])
		if _, skip := excluded[owner]; skip {
			continue
		}
		filtered = append(filtered, trimEntry(entry, descriptionMax, topicsMax))
	}
	// 总条数上限：快照要进提示词（命令行有长度上限），宁可少而准。
	if maxEntries > 0 && len(filtered) > maxEntries {
		filtered = filtered[:maxEntries]
		truncatedEntries = true
	}

	ledgerPath := filepath.Join(root, "tech-radar-history.json")
	ledger, err := readHistory(ledgerPath)
	if err != nil {
		return nil, err
	}
	previous := latestBefore(ledger, date)
	annotateHistory(filtered, ledger, previous, date)
	// 快照顺序 = 榜单顺序：先看“涨了多少”，再看“本来多大”。
	sort.SliceStable(filtered, func(left, right int) bool {
		if filtered[left].StarsGained != filtered[right].StarsGained {
			return filtered[left].StarsGained > filtered[right].StarsGained
		}
		if filtered[left].Stars != filtered[right].Stars {
			return filtered[left].Stars > filtered[right].Stars
		}
		return filtered[left].FullName < filtered[right].FullName
	})

	snap := snapshot{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		RuleVersion: ruleVersion,
		WindowDays:  window,
		// 严格 schema 校验下 null ≠ 空数组：一律归一成 []，避免“没有主题”时报告被拒。
		Topics:      nonNilStrings(in.Topics),
		Domains:     nonNilStrings(domainNames),
		Lanes:       []string{"active", "rising"},
		MinStars:    minStars,
		NewMinStars: newMinStars,
		QueryCount:  queryCount,
		TrendBasis:  trendBasis(ledger, date),
		Entries:     filtered,
		RateLimit:   rateLimit,
	}
	if truncated {
		snap.RateLimit["truncated"] = true
		snap.RateLimit["budget"] = queryBudget
	}
	if truncatedEntries {
		snap.RateLimit["entries_truncated"] = true
		snap.RateLimit["max_entries"] = maxEntries
	}
	if err := saveHistory(ledgerPath, ledger, date, filtered); err != nil {
		return nil, err
	}
	// Artifact 内容必须与声明的 schema 一致：历史台账是扩展内部事实，
	// 快照才是对外交付的 Artifact，因此单独落一份可被校验的快照文件。
	snapshotPath := filepath.Join(root, "snapshots", date+".json")
	if err := os.MkdirAll(filepath.Dir(snapshotPath), 0o755); err != nil {
		return nil, errors.New("快照目录创建失败")
	}
	if err := writeJSONFile(snapshotPath, snap); err != nil {
		return nil, errors.New("快照写入失败")
	}
	digest, err := sha256OfFile(snapshotPath)
	if err != nil {
		return nil, errors.New("快照摘要计算失败")
	}
	return map[string]any{
		"snapshot": snap,
		"artifacts": []map[string]any{{
			"artifact_id":   "tech-radar-snapshot",
			"artifact_type": "tech_radar_snapshot",
			"name":          "热点采集快照 " + date,
			"uri":           "file:///" + strings.ReplaceAll(snapshotPath, "\\", "/"),
			"sha256":        digest,
		}},
		"report_date": date,
	}, nil
}

func searchRepositories(query string, languages []string, limit int) ([]repoEntry, map[string]any, error) {
	endpoint, err := url.Parse(githubSearchBase)
	if err != nil {
		return nil, nil, err
	}
	values := endpoint.Query()
	values.Set("q", query)
	values.Set("sort", "stars")
	values.Set("order", "desc")
	values.Set("per_page", fmt.Sprintf("%d", clamp(limit, 1, 100)))
	endpoint.RawQuery = values.Encode()

	request, err := http.NewRequest(http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, nil, errors.New("GitHub 搜索地址无效")
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "HiMind-TechRadar")
	// token 由 Agent 侧注入运行环境，插件不接收、不落盘、不输出。
	if token := strings.TrimSpace(os.Getenv("HIMIND_TECH_RADAR_GITHUB_TOKEN")); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := httpClient.Do(request)
	if err != nil {
		return nil, nil, errors.New("GitHub 搜索请求失败")
	}
	defer response.Body.Close()
	rateLimit := map[string]any{
		"remaining":     intFromHeader(response.Header.Get("x-ratelimit-remaining")),
		"resets_at":     response.Header.Get("x-ratelimit-reset"),
		"authenticated": request.Header.Get("Authorization") != "",
	}
	if response.StatusCode != http.StatusOK {
		return nil, rateLimit, fmt.Errorf("GitHub 搜索返回 HTTP %d", response.StatusCode)
	}
	var payload struct {
		Items []struct {
			FullName    string   `json:"full_name"`
			HTMLURL     string   `json:"html_url"`
			Description string   `json:"description"`
			Language    string   `json:"language"`
			Stars       int      `json:"stargazers_count"`
			Forks       int      `json:"forks_count"`
			OpenIssues  int      `json:"open_issues_count"`
			PushedAt    string   `json:"pushed_at"`
			Homepage    string   `json:"homepage"`
			Archived    bool     `json:"archived"`
			Topics      []string `json:"topics"`
			License     *struct {
				SPDXID string `json:"spdx_id"`
			} `json:"license"`
		} `json:"items"`
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return nil, rateLimit, errors.New("GitHub 搜索响应读取失败")
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, rateLimit, errors.New("GitHub 搜索响应不是合法 JSON")
	}
	allowedLanguages := map[string]struct{}{}
	for _, language := range languages {
		allowedLanguages[strings.ToLower(strings.TrimSpace(language))] = struct{}{}
	}
	entries := make([]repoEntry, 0, len(payload.Items))
	for _, item := range payload.Items {
		if strings.TrimSpace(item.FullName) == "" || strings.TrimSpace(item.HTMLURL) == "" {
			continue
		}
		if len(allowedLanguages) > 0 {
			if _, ok := allowedLanguages[strings.ToLower(item.Language)]; !ok {
				continue
			}
		}
		entries = append(entries, repoEntry{
			EntryID:     strings.ToLower(item.FullName),
			FullName:    item.FullName,
			HTMLURL:     item.HTMLURL,
			Description: strings.TrimSpace(item.Description),
			Language:    item.Language,
			Stars:       item.Stars,
			Forks:       item.Forks,
			OpenIssues:  item.OpenIssues,
			PushedAt:    item.PushedAt,
			Homepage:    strings.TrimSpace(item.Homepage),
			Archived:    item.Archived,
			Topics:      nonNilStrings(item.Topics),
			License:     licenseID(item.License),
		})
	}
	return entries, rateLimit, nil
}

func licenseID(license *struct {
	SPDXID string `json:"spdx_id"`
}) string {
	if license == nil {
		return ""
	}
	id := strings.TrimSpace(license.SPDXID)
	if id == "NOASSERTION" {
		return ""
	}
	return id
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// trimEntry 控制单条载荷体积：快照会同时进报告与 AI 提示词。
func trimEntry(entry repoEntry, descriptionMax int, topicsMax int) repoEntry {
	entry.Description = truncateRunes(entry.Description, descriptionMax)
	if topicsMax > 0 && len(entry.Topics) > topicsMax {
		entry.Topics = entry.Topics[:topicsMax]
	}
	return entry
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return strings.TrimSpace(string(runes[:limit])) + "…"
}

// 领域预设：每个方向用少量“伞形标签”，每个标签单独一次查询。
//
// GitHub 搜索的 `topic:` 限定符**不支持 OR**（带括号返回 0 条，不带括号直接 422），
// 所以这里不做 OR 拼装，而是「一个标签一次查询」，再在本地按仓库去重合并。
var domainPresets = map[string][]string{
	"cv":      {"computer-vision"},
	"llm":     {"llm"},
	"ar-vr":   {"virtual-reality", "augmented-reality"},
	"general": {"machine-learning"},
}

const (
	defaultMinStars    = 800
	defaultNewMinStars = 150
	// 未鉴权时 GitHub 搜索限流为 10 次/分钟，默认放慢查询节奏；有 token 时不必等待。
	unauthenticatedQueryIntervalMs = 7000
	// 单次运行最多打多少个查询，避免把额度打光。
	defaultMaxQueries = 16
	// 快照规模：既保证报告有料，也要让提示词留在命令行长度限制内。
	defaultMaxEntries          = 48
	defaultDescriptionMaxChars = 260
	defaultTopicsMax           = 8
)

// defaultDomains 让「什么都不填」也是一次有意义的采集。
// 默认值的归属放在扩展这边：工作流不写死领域，用户输入才能真正覆盖它。
var defaultDomains = []string{"cv", "llm", "ar-vr"}

type queryGroup struct {
	name  string
	topic string
}

// queryGroups 把领域预设与自定义主题展开成「一个标签一次查询」的清单。
func queryGroups(in input) ([]queryGroup, []string, error) {
	domains := in.Domains
	if len(domains) == 0 && len(in.Topics) == 0 && len(in.ExtraTopics) == 0 {
		domains = defaultDomains
	}
	groups := make([]queryGroup, 0, len(domains)+1)
	names := make([]string, 0, len(domains))
	for _, raw := range domains {
		key := strings.ToLower(strings.TrimSpace(raw))
		if key == "" {
			continue
		}
		topics, ok := domainPresets[key]
		if !ok {
			return nil, nil, fmt.Errorf("未知领域预设：%s（可选：cv / llm / ar-vr / general）", raw)
		}
		for _, topic := range topics {
			groups = append(groups, queryGroup{name: key, topic: topic})
		}
		names = append(names, key)
	}
	custom := make([]string, 0)
	for _, topic := range append(append([]string{}, in.Topics...), in.ExtraTopics...) {
		trimmed := strings.TrimSpace(topic)
		if trimmed == "" {
			continue
		}
		groups = append(groups, queryGroup{name: "custom", topic: trimmed})
		custom = append(custom, trimmed)
	}
	if len(custom) > 0 {
		names = append(names, "custom")
	}
	if len(groups) == 0 {
		return nil, nil, errors.New("domains 或 topics 至少需要一个")
	}
	return groups, names, nil
}

func queryInterval(in input, authenticated bool) time.Duration {
	if authenticated {
		return 0
	}
	if in.QueryIntervalMs > 0 {
		return time.Duration(in.QueryIntervalMs) * time.Millisecond
	}
	// 运行时可覆盖：CI/测试用 0 免等待，运维也能按额度调整节奏。
	if raw := strings.TrimSpace(os.Getenv("HIMIND_TECH_RADAR_QUERY_INTERVAL_MS")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value >= 0 {
			return time.Duration(value) * time.Millisecond
		}
	}
	return time.Duration(unauthenticatedQueryIntervalMs) * time.Millisecond
}

func maxQueries(in input) int {
	if in.MaxQueries > 0 {
		return in.MaxQueries
	}
	return defaultMaxQueries
}

func pushedOrCreated(created bool, since string) string {
	if created {
		return "created:>" + since
	}
	return "pushed:>" + since
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func mergeRateLimit(target map[string]any, source map[string]any) {
	if source == nil {
		return
	}
	for key, value := range source {
		if key == "remaining" {
			// 取所有查询里最小的剩余额度：那才是还能用几次的真实情况。
			current, ok := target[key].(int)
			next, nextOK := value.(int)
			if ok && nextOK && current <= next {
				continue
			}
		}
		target[key] = value
	}
}

func annotateHistory(entries []repoEntry, ledger []historyRecord, previous historyRecord, date string) {
	previousRank := map[string]int{}
	firstSeen := map[string]string{}
	for index, entry := range previous.Entries {
		previousRank[entry.EntryID] = index + 1
		if entry.FirstSeen != "" {
			firstSeen[entry.EntryID] = entry.FirstSeen
		}
	}
	// 星数增量只能自己算：GitHub 搜索不提供增长速度。取“最近一次见过的星数”，
	// 这样隔了几天再出现也能算出真实增量（而不是固定 0）。
	lastStars := map[string]int{}
	lastSeenDate := map[string]string{}
	knownSeen := map[string]string{}
	for _, record := range ledger {
		if record.Date >= date {
			continue
		}
		for _, entry := range record.Entries {
			if _, ok := lastStars[entry.EntryID]; !ok {
				lastStars[entry.EntryID] = entry.Stars
				lastSeenDate[entry.EntryID] = record.Date
			}
			if _, ok := knownSeen[entry.EntryID]; !ok && entry.FirstSeen != "" {
				knownSeen[entry.EntryID] = entry.FirstSeen
			}
		}
	}
	for index := range entries {
		entry := &entries[index]
		if rank, ok := previousRank[entry.EntryID]; ok {
			entry.RankDelta = rank - (index + 1)
			entry.IsNew = false
		} else {
			entry.IsNew = true
			entry.RankDelta = 0
		}
		if stars, ok := lastStars[entry.EntryID]; ok && stars > 0 && entry.Stars > stars {
			entry.StarsGained = entry.Stars - stars
		}
		if seen, ok := firstSeen[entry.EntryID]; ok {
			entry.FirstSeen = seen
		} else if seen, ok := knownSeen[entry.EntryID]; ok {
			entry.FirstSeen = seen
		} else {
			entry.FirstSeen = date
		}
	}
	_ = lastSeenDate
}

// trendBasis 说明“升温榜”的依据，避免第一期报告出现空榜却不说原因。
func trendBasis(ledger []historyRecord, date string) string {
	days := 0
	for _, record := range ledger {
		if record.Date < date {
			days++
		}
	}
	if days == 0 {
		return "本期为第一期：星数增量需要至少两期数据，升温榜从下一期开始有值。"
	}
	return fmt.Sprintf("基于本扩展已归档的 %d 期历史快照计算星数增量（GitHub 搜索接口本身不提供增长速度）。", days)
}

func render(in input) (any, error) {
	root, err := reportRoot(in.ReportRoot)
	if err != nil {
		return nil, err
	}
	snapshotValue := in.Snapshot
	if len(snapshotValue) == 0 {
		snapshotValue = upstreamSnapshot(in)
	}
	if len(snapshotValue) == 0 {
		return nil, errors.New("snapshot 不能为空：需要显式 snapshot 或 workflow_context 里的上游步骤输出")
	}
	insight := in.Insight
	if len(insight) == 0 {
		insight = upstreamInsight(in)
	}
	summary := strings.TrimSpace(in.Summary)
	if summary == "" {
		if fromInsight, ok := insight["summary"].(string); ok {
			summary = strings.TrimSpace(fromInsight)
		}
	}
	if summary == "" && len(insight) == 0 {
		// 降级时也要让归档自解释：这份报告只含确定性排名。
		summary = "本期为规则稿：解读步骤未产出（AI 不可用或超时），榜单按确定性规则排序。"
	}
	raw, err := json.Marshal(snapshotValue)
	if err != nil {
		return nil, errors.New("snapshot 无法序列化")
	}
	var snap snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil, errors.New("snapshot 结构无效")
	}
	date := reportDate(in.ReportDate)
	directory := filepath.Join(root, date)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, errors.New("报告目录创建失败")
	}
	htmlPath := filepath.Join(directory, "index.html")
	jsonPath := filepath.Join(directory, "report.json")
	markdown := renderMarkdown(date, snap, insight, summary)
	if err := os.WriteFile(htmlPath, []byte(renderHTML(date, snap, insight, summary)), 0o644); err != nil {
		return nil, errors.New("报告 HTML 写入失败")
	}
	// 视图按天回看时消费扩展自己保存的快照与解读，扩展是这些数据的所有者，
	// 平台只保管 Run 与 Artifact 事实，两者互不越权。
	if err := writeJSONFile(filepath.Join(directory, "snapshot.json"), snapshotValue); err != nil {
		return nil, errors.New("快照归档写入失败")
	}
	if err := writeJSONFile(filepath.Join(directory, "insight.json"), insight); err != nil {
		return nil, errors.New("解读归档写入失败")
	}
	newCount := 0
	for _, entry := range snap.Entries {
		if entry.IsNew {
			newCount++
		}
	}
	report := map[string]any{
		"report_date":       date,
		"html_path":         htmlPath,
		"json_path":         jsonPath,
		"entry_count":       len(snap.Entries),
		"new_entry_count":   newCount,
		"snapshot_sha256":   digestOf(snap),
		"rule_version":      snap.RuleVersion,
		"prompt_version":    "tech-radar-insight.v1",
		"insight_model":     insightString(insight, "model"),
		"insight_service":   insightString(insight, "service_source"),
		"insight_provider":  insightString(insight, "provider"),
		"degraded":          len(insight) == 0,
		"summary":           summary,
		"dingtalk_markdown": markdown,
	}
	if body, err := json.MarshalIndent(report, "", "  "); err == nil {
		_ = os.WriteFile(jsonPath, body, 0o644)
	}
	reportDigest, err := sha256OfFile(jsonPath)
	if err != nil {
		return nil, errors.New("报告摘要计算失败")
	}
	if err := appendArchive(root, archiveRecord{
		ReportDate:     date,
		GeneratedAt:    snap.GeneratedAt,
		EntryCount:     len(snap.Entries),
		NewEntryCount:  newCount,
		Summary:        summary,
		HTMLPath:       htmlPath,
		JSONPath:       jsonPath,
		SnapshotSHA256: digestOf(snap),
		RuleVersion:    snap.RuleVersion,
		PromptVersion:  "tech-radar-insight.v1",
		Degraded:       len(insight) == 0,
	}); err != nil {
		return nil, err
	}
	return map[string]any{
		"report": report,
		"artifacts": []map[string]any{{
			"artifact_id":   "tech-radar-report",
			"artifact_type": "tech_radar_report",
			"name":          "科技雷达报告 " + date,
			// Artifact 内容按声明 schema 校验，因此指向结构化报告而不是网页；
			// 网页路径在报告 JSON 里作为事实字段保留。
			"uri":    "file:///" + strings.ReplaceAll(jsonPath, "\\", "/"),
			"sha256": reportDigest,
		}},
	}, nil
}

// 报告分三段：升温榜（自有历史算出的星数增量）、活跃基础盘（按领域）、新星。
// 每一段都写清依据，避免“只有一串仓库名”看不出所以然。
type reportSection struct {
	title   string
	note    string
	entries []repoEntry
}

func reportSections(snap snapshot, perSection int) []reportSection {
	rising := make([]repoEntry, 0, len(snap.Entries))
	for _, entry := range snap.Entries {
		if entry.StarsGained > 0 {
			rising = append(rising, entry)
		}
	}
	sort.SliceStable(rising, func(left, right int) bool {
		if rising[left].StarsGained != rising[right].StarsGained {
			return rising[left].StarsGained > rising[right].StarsGained
		}
		return rising[left].Stars > rising[right].Stars
	})

	activeByDomain := map[string][]repoEntry{}
	activeOrder := make([]string, 0, len(snap.Domains))
	for _, entry := range snap.Entries {
		if !hasValue(entry.Lanes, "active") {
			continue
		}
		for _, domain := range entry.Domains {
			if _, ok := activeByDomain[domain]; !ok {
				activeOrder = append(activeOrder, domain)
			}
			activeByDomain[domain] = append(activeByDomain[domain], entry)
		}
	}
	for _, domain := range activeOrder {
		sort.SliceStable(activeByDomain[domain], func(left, right int) bool {
			return activeByDomain[domain][left].Stars > activeByDomain[domain][right].Stars
		})
	}

	risingNew := make([]repoEntry, 0, len(snap.Entries))
	for _, entry := range snap.Entries {
		if hasValue(entry.Lanes, "rising") {
			risingNew = append(risingNew, entry)
		}
	}
	sort.SliceStable(risingNew, func(left, right int) bool {
		return risingNew[left].Stars > risingNew[right].Stars
	})

	sections := make([]reportSection, 0, len(activeOrder)+2)
	// 记录已经被某个分组覆盖的条目：没被覆盖的（例如旧快照没有 lanes 元数据）
	// 统一进“其他收录”，报告绝不静默丢数据。
	covered := map[string]struct{}{}
	markCovered := func(entries []repoEntry) {
		for _, entry := range entries {
			covered[entry.EntryID] = struct{}{}
		}
	}
	if len(rising) > 0 {
		markCovered(rising)
		sections = append(sections, reportSection{
			title:   "升温榜",
			note:    snap.TrendBasis,
			entries: capEntries(rising, perSection),
		})
	}
	for _, domain := range activeOrder {
		markCovered(activeByDomain[domain])
		sections = append(sections, reportSection{
			title:   "活跃基础盘 · " + domainLabel(domain),
			note:    fmt.Sprintf("近 %d 天有推送、星数 ≥ %d 的长期项目，按总星数排序。", snap.WindowDays, snap.MinStars),
			entries: capEntries(activeByDomain[domain], perSection),
		})
	}
	if len(risingNew) > 0 {
		markCovered(risingNew)
		sections = append(sections, reportSection{
			title:   "新星",
			note:    fmt.Sprintf("近 %d 天新建、星数 ≥ %d 且已起量的项目。", snap.WindowDays, snap.NewMinStars),
			entries: capEntries(risingNew, perSection),
		})
	}
	leftover := make([]repoEntry, 0)
	for _, entry := range snap.Entries {
		if _, ok := covered[entry.EntryID]; !ok {
			leftover = append(leftover, entry)
		}
	}
	if len(leftover) > 0 {
		sort.SliceStable(leftover, func(left, right int) bool {
			return leftover[left].Stars > leftover[right].Stars
		})
		sections = append(sections, reportSection{
			title:   "其他收录",
			note:    "未归入上述分类的条目（例如历史快照缺少赛道标记）。",
			entries: capEntries(leftover, perSection),
		})
	}
	return sections
}

func capEntries(entries []repoEntry, limit int) []repoEntry {
	if limit <= 0 || len(entries) <= limit {
		return entries
	}
	return entries[:limit]
}

func hasValue(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func domainLabel(domain string) string {
	return map[string]string{
		"cv":      "计算机视觉",
		"llm":     "大模型 / LLM",
		"ar-vr":   "AR / VR",
		"general": "综合 AI",
		"custom":  "自定义主题",
	}[domain]
}

func entryBadges(entry repoEntry) string {
	badges := make([]string, 0, 4)
	if entry.StarsGained > 0 {
		badges = append(badges, fmt.Sprintf("近 %s 天 +%d 星", entry.FirstSeen, entry.StarsGained))
	}
	if entry.IsNew {
		badges = append(badges, "首次收录")
	}
	if len(entry.Domains) > 0 {
		labels := make([]string, 0, len(entry.Domains))
		for _, domain := range entry.Domains {
			labels = append(labels, domainLabel(domain))
		}
		badges = append(badges, strings.Join(labels, "/"))
	}
	return strings.Join(badges, " · ")
}

func renderMarkdown(date string, snap snapshot, insight map[string]any, summary string) string {
	var builder strings.Builder
	builder.WriteString("### 科技雷达 · " + date + "\n\n")
	if strings.TrimSpace(summary) != "" {
		builder.WriteString(summary + "\n\n")
	}
	builder.WriteString(fmt.Sprintf(
		"来源：公开 GitHub 搜索接口 · 领域 %s · 窗口 %d 天 · 星数下限 %d/%d · 共 %d 次查询\n\n",
		strings.Join(snap.Domains, "、"), snap.WindowDays, snap.MinStars, snap.NewMinStars, snap.QueryCount,
	))
	if model := insightString(insight, "model"); model != "" {
		builder.WriteString("解读模型：" + model + "\n\n")
	}
	for _, section := range reportSections(snap, 8) {
		builder.WriteString("#### " + section.title + "\n")
		builder.WriteString(section.note + "\n\n")
		for index, entry := range section.entries {
			builder.WriteString(fmt.Sprintf(
				"%d. [%s](%s) ⭐%d · %s · %s\n",
				index+1, entry.FullName, entry.HTMLURL, entry.Stars,
				fallback(entry.Language, "未标注语言"), fallback(entry.License, "未标注许可"),
			))
			if reason := insightReason(insight, entry.EntryID); reason != "" {
				builder.WriteString("   " + reason + "\n")
			}
		}
		builder.WriteString("\n")
	}
	if limited := limitedNote(snap); limited != "" {
		builder.WriteString(limited + "\n")
	}
	return builder.String()
}

func limitedNote(snap snapshot) string {
	total := len(snap.Entries)
	shown := 0
	for _, section := range reportSections(snap, 8) {
		shown += len(section.entries)
	}
	if total <= shown {
		return ""
	}
	return fmt.Sprintf("本次共采集 %d 条，另有 %d 条见完整报告与归档视图。", total, total-shown)
}

func fallback(value string, when string) string {
	if strings.TrimSpace(value) == "" {
		return when
	}
	return value
}

func renderHTML(date string, snap snapshot, insight map[string]any, summary string) string {
	var sections strings.Builder
	for _, section := range reportSections(snap, 10) {
		var rows strings.Builder
		for index, entry := range section.entries {
			badge := ""
			if entry.StarsGained > 0 {
				badge = fmt.Sprintf(`<span class="badge up">+%d</span>`, entry.StarsGained)
			} else if entry.IsNew {
				badge = `<span class="badge new">首次收录</span>`
			}
			reason := insightReason(insight, entry.EntryID)
			reasonHTML := ""
			if reason != "" {
				reasonHTML = `<p class="reason">` + html.EscapeString(reason) + `</p>`
			}
			rows.WriteString(fmt.Sprintf(
				`<li><div class="head"><span class="rank">%d</span><a href="%s">%s</a>%s<span class="stars">⭐%d</span></div>%s<p class="meta">%s · %s · %s · %s</p></li>`,
				index+1,
				html.EscapeString(entry.HTMLURL),
				html.EscapeString(entry.FullName),
				badge,
				entry.Stars,
				reasonHTML,
				html.EscapeString(fallback(entry.Language, "未标注语言")),
				html.EscapeString(fallback(entry.License, "未标注许可")),
				html.EscapeString(shortDate(entry.PushedAt)),
				html.EscapeString(entryBadges(entry)),
			))
		}
		sections.WriteString(
			`<section><h2>` + html.EscapeString(section.title) + `</h2><p class="note">` +
				html.EscapeString(section.note) + `</p><ul>` + rows.String() + `</ul></section>`,
		)
	}
	summaryHTML := ""
	if strings.TrimSpace(summary) != "" {
		summaryHTML = `<p class="summary">` + html.EscapeString(summary) + `</p>`
	}
	return `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><title>科技雷达 ` + html.EscapeString(date) + `</title>
<style>
body{margin:0;padding:32px;background:#f7f8fa;color:#111827;font:14px/1.6 -apple-system,"Segoe UI","Microsoft YaHei",sans-serif}
main{max-width:860px;margin:0 auto;background:#fff;border:1px solid #e5e7eb;border-radius:8px;padding:28px}
h1{margin:0 0 4px;font-size:22px}
h2{margin:26px 0 2px;font-size:15px}
.sub,.note{color:#6b7280;font-size:12px;margin:0}
.summary{background:#eff6ff;border-left:3px solid #2563eb;padding:10px 12px;border-radius:0 6px 6px 0;margin:16px 0 0}
ul{list-style:none;padding:0;margin:0}
li{padding:14px 0;border-bottom:1px solid #f1f3f5}
.head{display:flex;align-items:center;gap:8px;flex-wrap:wrap}
.rank{color:#94a3b8;font-variant-numeric:tabular-nums;min-width:20px}
a{color:#1d4ed8;text-decoration:none;font-weight:600}
.stars{margin-left:auto;color:#6b7280;font-variant-numeric:tabular-nums}
.badge{padding:1px 6px;border-radius:4px;font-size:11px}
.badge.new{background:#ecfdf5;color:#047857}
.badge.up{background:#fffbeb;color:#b45309}
.reason{margin:6px 0 0;color:#374151;font-size:13px}
.meta{margin:4px 0 0;color:#6b7280;font-size:12px}
footer{margin-top:18px;color:#94a3b8;font-size:11px}
</style></head><body><main>
<h1>科技雷达 · ` + html.EscapeString(date) + `</h1>
<p class="sub">来源：公开 GitHub 搜索接口 · 领域 ` + html.EscapeString(strings.Join(snap.Domains, "、")) +
		` · 窗口 ` + fmt.Sprintf("%d", snap.WindowDays) + ` 天 · 星数下限 ` +
		fmt.Sprintf("%d", snap.MinStars) + `/` + fmt.Sprintf("%d", snap.NewMinStars) +
		` · 共 ` + fmt.Sprintf("%d", snap.QueryCount) + ` 次查询 · 生成于 ` + html.EscapeString(snap.GeneratedAt) + `</p>
` + summaryHTML + sections.String() + insightCredit(snap, insight) + `
<footer>仅采集公开信息；星数增量由本扩展的历史快照计算，GitHub 搜索接口不提供增长速度。</footer>
</main></body></html>`
}

// insightCredit 说明这条解读是谁给的：模型 + 服务来源，便于口径对账。
func insightCredit(_ snapshot, insight map[string]any) string {
	model := insightString(insight, "model")
	if model == "" {
		return "\n<p class=\"sub\">本期没有智能解读（规则稿）：条目按确定性排序，未经模型加工。</p>"
	}
	service := map[string]string{
		"managed": "平台托管服务",
		"custom":  "本机自定义 AI 服务",
		"native":  "Runtime 自带配置",
	}[insightString(insight, "service_source")]
	if service == "" {
		service = insightString(insight, "service_source")
	}
	credit := "\n<p class=\"sub\">解读模型：" + html.EscapeString(model)
	if service != "" {
		credit += " · " + html.EscapeString(service)
	}
	return credit + "</p>"
}

func shortDate(value string) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) >= 10 {
		return "最后推送 " + trimmed[:10]
	}
	return trimmed
}

// insightString 读取解读元数据里的字符串字段（模型、服务来源等）。
func insightString(insight map[string]any, key string) string {
	value, ok := insight[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func insightReason(insight map[string]any, entryID string) string {
	if len(insight) == 0 {
		return ""
	}
	items, ok := insight["insights"].([]any)
	if !ok {
		return ""
	}
	for _, item := range items {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if fmt.Sprintf("%v", record["entry_id"]) == entryID {
			return strings.TrimSpace(fmt.Sprintf("%v", record["reason"]))
		}
	}
	return ""
}

// reportRoot 解析归档目录：显式入参 > 上次记住的目录 > 扩展私有数据目录。
// 显式传参会顺手记住它，这样写入位置和自带“历史归档”视图读的位置永远一致：
// 使用者把报告挪到自己的目录后，视图依然能看到后续报告，而不是读默认目录读到空。
func reportRoot(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	explicit := trimmed != ""
	if !explicit {
		trimmed = readPluginState().ReportRoot
	}
	if trimmed == "" {
		trimmed = defaultDataRoot()
	}
	root, err := filepath.Abs(trimmed)
	if err != nil {
		return "", errors.New("report_root 无效")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", errors.New("report_root 创建失败")
	}
	if explicit {
		rememberReportRoot(root)
	}
	return root, nil
}

// pluginState 是扩展自己的持久配置，只保存与使用者选择有关的少量事实。
type pluginState struct {
	ReportRoot string `json:"report_root"`
}

func pluginStatePath() string {
	return filepath.Join(defaultDataRoot(), "state.json")
}

func readPluginState() pluginState {
	payload, err := os.ReadFile(pluginStatePath())
	if err != nil {
		return pluginState{}
	}
	var state pluginState
	if err := json.Unmarshal(payload, &state); err != nil {
		return pluginState{}
	}
	return state
}

func writePluginState(state pluginState) error {
	path := pluginStatePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, payload, 0o644)
}

// rememberReportRoot 只做尽力而为的持久化：记不住也不该让一次采集失败。
func rememberReportRoot(root string) {
	state := readPluginState()
	if state.ReportRoot == root {
		return
	}
	state.ReportRoot = root
	_ = writePluginState(state)
}

// defaultDataRoot 让扩展自己决定状态存放位置，避免把存储策略推给使用者。
func defaultDataRoot() string {
	if configured := strings.TrimSpace(os.Getenv("HIMIND_PLUGIN_DATA_ROOT")); configured != "" {
		return configured
	}
	base := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "HiMindAgent", "extension-data", extensionDataNamespace)
}

type archiveRecord struct {
	ReportDate     string `json:"report_date"`
	GeneratedAt    string `json:"generated_at"`
	EntryCount     int    `json:"entry_count"`
	NewEntryCount  int    `json:"new_entry_count"`
	Summary        string `json:"summary"`
	HTMLPath       string `json:"html_path"`
	JSONPath       string `json:"json_path"`
	SnapshotSHA256 string `json:"snapshot_sha256"`
	RuleVersion    string `json:"rule_version"`
	PromptVersion  string `json:"prompt_version"`
	Degraded       bool   `json:"degraded"`
}

// appendArchive 维护扩展自己的归档索引，视图按日期读取即可，无需扫描目录。
func appendArchive(root string, record archiveRecord) error {
	path := filepath.Join(root, "index.json")
	records, err := readArchive(path)
	if err != nil {
		return err
	}
	kept := make([]archiveRecord, 0, len(records)+1)
	for _, item := range records {
		if item.ReportDate != record.ReportDate {
			kept = append(kept, item)
		}
	}
	kept = append(kept, record)
	sort.SliceStable(kept, func(left, right int) bool { return kept[left].ReportDate > kept[right].ReportDate })
	if len(kept) > 365 {
		kept = kept[:365]
	}
	body, err := json.MarshalIndent(kept, "", "  ")
	if err != nil {
		return errors.New("归档索引序列化失败")
	}
	return os.WriteFile(path, body, 0o644)
}

func readArchive(path string) ([]archiveRecord, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("归档索引读取失败")
	}
	var records []archiveRecord
	if err := json.Unmarshal(body, &records); err != nil {
		return nil, nil
	}
	return records, nil
}

// upstreamStepOutput 读取工作流注入的上下文里的上游步骤输出。
//
// 步骤 id 由工作流在 step.input 里显式声明（snapshot_step / insight_step），
// 扩展不硬编码某条工作流的步骤名，也不猜执行顺序。
func upstreamStepOutput(in input, stepID string, key string) map[string]any {
	if strings.TrimSpace(stepID) == "" || len(in.WorkflowContext) == 0 {
		return nil
	}
	steps, ok := in.WorkflowContext["steps"].(map[string]any)
	if !ok {
		return nil
	}
	output, ok := steps[strings.TrimSpace(stepID)].(map[string]any)
	if !ok {
		return nil
	}
	if strings.TrimSpace(key) == "" {
		return output
	}
	value, ok := output[key].(map[string]any)
	if !ok {
		return nil
	}
	return value
}

// upstreamSnapshot 在上游采集步骤存在时直接复用它的快照，避免调用方重复传参。
func upstreamSnapshot(in input) map[string]any {
	return upstreamStepOutput(in, in.SnapshotStep, "snapshot")
}

// upstreamInsight 只在解读步骤真的产出了 insights 时才当作解读：
// 解读步骤被降级（on_failure=continue）时上下文里没有该输出，报告按规则稿出。
func upstreamInsight(in input) map[string]any {
	output := upstreamStepOutput(in, in.InsightStep, "")
	if len(output) == 0 {
		return nil
	}
	raw, ok := output["insights"]
	if !ok {
		return nil
	}
	insight := map[string]any{"insights": raw}
	if highlights, ok := output["highlights"]; ok {
		insight["highlights"] = highlights
	}
	if summary, ok := output["summary"].(string); ok && strings.TrimSpace(summary) != "" {
		insight["summary"] = summary
	}
	// 记下解读用的模型与服务来源：报告要能回答“这个结论是谁给的”。
	for _, key := range []string{"model", "service_source", "provider", "endpoint"} {
		if value, ok := output[key]; ok {
			insight[key] = value
		}
	}
	return insight
}

// listArchive 是扩展视图的数据入口：视图只消费能力结果，不直接读文件系统。
func listArchive(in input) (any, error) {
	root, err := reportRoot(in.ReportRoot)
	if err != nil {
		return nil, err
	}
	records, err := readArchive(filepath.Join(root, "index.json"))
	if err != nil {
		return nil, err
	}
	limit := in.TopN
	if limit <= 0 || limit > len(records) {
		limit = len(records)
	}
	if strings.TrimSpace(in.ReportDate) != "" {
		filtered := make([]archiveRecord, 0, len(records))
		for _, record := range records {
			if record.ReportDate == in.ReportDate {
				filtered = append(filtered, record)
			}
		}
		records = filtered
	}
	return map[string]any{
		"data_root": root,
		"total":     len(records),
		"records":   records[:minInt(limit, len(records))],
	}, nil
}

// readArchivedReport 是扩展视图的第二条数据通路：视图只拿能力结果，
// 不直接读文件系统，报告 HTML、快照和解读都由扩展进程自己取回。
func readArchivedReport(in input) (any, error) {
	root, err := reportRoot(in.ReportRoot)
	if err != nil {
		return nil, err
	}
	date := strings.TrimSpace(in.ReportDate)
	if date == "" {
		records, listErr := readArchive(filepath.Join(root, "index.json"))
		if listErr != nil {
			return nil, listErr
		}
		if len(records) == 0 {
			return nil, errors.New("归档中还没有报告")
		}
		date = records[0].ReportDate
	}
	if !isReportDate(date) {
		return nil, errors.New("report_date 不合法")
	}
	directory := filepath.Join(root, date)
	htmlBody, err := os.ReadFile(filepath.Join(directory, "index.html"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("该日期还没有报告：" + date)
	}
	if err != nil {
		return nil, errors.New("报告 HTML 读取失败")
	}
	report := map[string]any{}
	if raw, readErr := os.ReadFile(filepath.Join(directory, "report.json")); readErr == nil {
		_ = json.Unmarshal(raw, &report)
	}
	snapshotBody := map[string]any{}
	if raw, readErr := os.ReadFile(filepath.Join(directory, "snapshot.json")); readErr == nil {
		_ = json.Unmarshal(raw, &snapshotBody)
	}
	insight := map[string]any{}
	if raw, readErr := os.ReadFile(filepath.Join(directory, "insight.json")); readErr == nil {
		_ = json.Unmarshal(raw, &insight)
	}
	return map[string]any{
		"report_date": date,
		"report":      report,
		"snapshot":    snapshotBody,
		"insight":     insight,
		"html":        string(htmlBody),
		"markdown":    report["dingtalk_markdown"],
		"html_path":   filepath.Join(directory, "index.html"),
	}, nil
}

func isReportDate(value string) bool {
	if len(value) != len("2006-01-02") {
		return false
	}
	_, err := time.Parse("2006-01-02", value)
	return err == nil
}

// openLink 把链接交给系统默认处理器。
//
// 这是扩展自己的动作，不是宿主给视图开的通用出口：视图能做什么，由本插件
// Manifest 里声明的能力决定，风险等级也写在同一条声明上，用户按自己的判断放行。
// 这里只做参数卫生（非空、长度、控制字符），不做协议白名单。
func openLink(in input) (any, error) {
	target := strings.TrimSpace(in.URL)
	if target == "" {
		return nil, errors.New("url 不能为空")
	}
	if len(target) > 4096 {
		return nil, errors.New("url 过长")
	}
	if strings.ContainsAny(target, "\r\n\x00") {
		return nil, errors.New("url 含非法字符")
	}
	if !strings.Contains(target, ":") {
		return nil, errors.New("url 缺少协议前缀")
	}
	if runtime.GOOS != "windows" {
		return nil, errors.New("当前平台暂不支持打开链接：" + runtime.GOOS)
	}
	// 直接传参数组，不经过 cmd/shell，避免二次解析。
	command := exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	if err := command.Start(); err != nil {
		return nil, errors.New("打开链接失败：" + err.Error())
	}
	_ = command.Process.Release()
	return map[string]any{"opened": true, "url": target}, nil
}

func writeJSONFile(path string, value any) error {
	switch typed := value.(type) {
	case nil:
		value = map[string]any{}
	case map[string]any:
		// 具名的空 map 也必须落成 {}，否则归档里会出现 null 这种不是对象的占位。
		if typed == nil {
			value = map[string]any{}
		}
	}
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o644)
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func reportDate(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed != "" {
		return trimmed
	}
	return time.Now().Format("2006-01-02")
}

func readHistory(path string) ([]historyRecord, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("历史台账读取失败")
	}
	var records []historyRecord
	if err := json.Unmarshal(body, &records); err != nil {
		return nil, nil
	}
	return records, nil
}

func latestBefore(records []historyRecord, date string) historyRecord {
	var latest historyRecord
	for _, record := range records {
		if record.Date < date && record.Date > latest.Date {
			latest = record
		}
	}
	return latest
}

func saveHistory(path string, records []historyRecord, date string, entries []repoEntry) error {
	kept := make([]historyRecord, 0, len(records)+1)
	for _, record := range records {
		if record.Date != date {
			kept = append(kept, record)
		}
	}
	kept = append(kept, historyRecord{Date: date, Entries: entries})
	sort.SliceStable(kept, func(left, right int) bool { return kept[left].Date > kept[right].Date })
	if len(kept) > 60 {
		kept = kept[:60]
	}
	body, err := json.MarshalIndent(kept, "", "  ")
	if err != nil {
		return errors.New("历史台账序列化失败")
	}
	return os.WriteFile(path, body, 0o644)
}

func digestOf(value any) string {
	body, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

// sha256OfFile 计算落盘文件本身的摘要：工作流会用它核对 Artifact 内容，
// 所以必须与交付给平台的字节完全一致。
func sha256OfFile(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

func intFromHeader(value string) int {
	result := 0
	for _, char := range strings.TrimSpace(value) {
		if char < '0' || char > '9' {
			return 0
		}
		result = result*10 + int(char-'0')
	}
	return result
}

func clamp(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}
