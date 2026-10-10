package main

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/MrBaoquan/himind-extensions/sdk/jsonrpc"
)

// schemaVersion 标识单日日志文件的存储结构，便于将来兼容迁移。
const schemaVersion = "work-log-entry.v1"

// extensionDataNamespace 是扩展私有数据目录的命名空间，仅在用户没有指定
// 全局日志目录、且系统无法提供 Documents 路径时作为兜底。
const extensionDataNamespace = "com.himind.work-log"

type input struct {
	// LogRoot 是全局日志仓库根目录；显式传入会覆盖记忆值并顺手记住。
	LogRoot string `json:"log_root"`
	// Date 是单日操作的目标日期（YYYY-MM-DD）。
	Date string `json:"date"`
	// From / To 是区间查询边界（含）。
	From string `json:"from"`
	To   string `json:"to"`
	// Year / Month / WeekStart 供日历视图按月构建栅格；缺省时从 Date 或今天推导。
	// WeekStart 取 monday（默认）或 sunday。
	Year      int    `json:"year"`
	Month     int    `json:"month"`
	WeekStart string `json:"week_start"`
	// Period 是报告周期：daily / weekly / quarterly / yearly。日报为核心，
	// 其余周期为辅助视图，仅保留能力以兼容历史数据。
	Period string `json:"period"`
	// Entries / Entry 是写入的日志条目，二者可同时使用，Entry 用于单条场景。
	Entries []entry `json:"entries"`
	Entry   *entry  `json:"entry"`
	// Mode 决定合并还是覆盖：merge（默认）/ replace。
	Mode string `json:"mode"`
	// Overwrite 为 true 时，命中同一 entry_id 的写入整条替换而非字段级合并，
	// 让视图编辑可以真正清空某个字段（undefined 字段不参与合并）。
	Overwrite bool `json:"overwrite"`
	// EntryID / EntryIDs 定位要删除的条目（work-log.entry.delete）。
	EntryID  string   `json:"entry_id"`
	EntryIDs []string `json:"entry_ids"`
	// 过滤条件。
	Project  string `json:"project"`
	Category string `json:"category"`
	Source   string `json:"source"`
	Tag      string `json:"tag"`
	// Query 是自由文本检索：在标题、摘要、功能、项目与标签里做不区分大小写的子串匹配。
	Query string `json:"query"`
	Limit int    `json:"limit"`
	// SessionID 用于按会话重归类（work-log.project.assign）。
	SessionID string `json:"session_id"`
	// Narrative / Title 供报告渲染使用：narrative 是 AI 提炼的主线叙述。
	Narrative string `json:"narrative"`
	// Narratives 是按项目 / 功能的模型叙述：键为项目名或 "项目/功能"，值为该组小结。
	Narratives map[string]string `json:"narratives"`
	Title      string            `json:"title"`
	// Periods / CompletedOnly / Force / Lookback 供 work-log.report.aggregate 使用：
	// periods 指定要汇总的周期集合（默认周报+月报）；completed_only 为 true 时只汇总
	// 「已结束」的自然周期（跨期补算，供随日报定时自动汇总）；force 为 true 时覆盖已存在
	// 的归档记录（手动重算）；lookback 限制自动模式单次回溯的周期数量，避免一次性补出大量历史。
	Periods       []string `json:"periods"`
	CompletedOnly *bool    `json:"completed_only"`
	Force         *bool    `json:"force"`
	Lookback      *int     `json:"lookback"`
	// URL 用于 work-log.link.open。
	URL string `json:"url"`
	// Path 用于 work-log.link.reveal：在系统文件管理器中定位一个目录或文件。
	Path string `json:"path"`
	// WorkflowContext 是工作流注入的上下文：上游步骤输出在 workflow_context.steps 里。
	// 具体步骤 id 由工作流在 step.input 里声明，插件不猜工作流结构。
	WorkflowContext map[string]any `json:"workflow_context"`
	// EntriesStep / NarrativeStep 指向上游步骤，用于从工作流取结构化结果。
	EntriesStep   string `json:"entries_step"`
	NarrativeStep string `json:"narrative_step"`
	// BaseStep 指向上游的「确定性素材」步骤（采集器候选条目）。与 EntriesStep（通常是
	// AI 总结结果）配合时，用工时/起止时间/来源/会话等确定性字段兜底，AI 只覆盖人类可读内容，
	// 避免模型把工时时长、时间戳写歪。
	BaseStep string `json:"base_step"`
	// 项目登记：ProjectID/ProjectName 等用于项目管理能力；ResolveProjects 控制写入时
	// 是否按登记表归一项目名。RegisterProjects 已废弃：写入不再自动登记未知项目，
	// 登记只由人工认领 / project.* 能力显式完成（保留字段以兼容旧调用）。
	ProjectID   string `json:"project_id"`
	ProjectName string `json:"project_name"`
	// NewName 供 work-log.project.rename：给项目改展示名（保持稳定 ID，并回绑历史条目）。
	NewName     string   `json:"new_name"`
	Aliases     []string `json:"aliases"`
	Description string   `json:"description"`
	Status      string   `json:"status"`
	Color       string   `json:"color"`
	// Kind 是项目类型：交付项目（场馆/展馆，含展项）/ 平台产品（自研，1 项目=1 路径，无展项）/ 通用沉淀。
	Kind             string `json:"kind"`
	CurrentProject   string `json:"current_project"`
	IncludeStats     *bool  `json:"include_stats"`
	ResolveProjects  *bool  `json:"resolve_projects"`
	RegisterProjects *bool  `json:"register_projects"`
	// 路径映射：项目/展项可通过本机目录/仓库路径稳定识别（路径优先于名称）。
	// Paths/AddPaths/RemovePaths 供 work-log.project.path.set 与 project.upsert 管理映射；
	// RepoPath 是会话仓库根、ExhibitID/ExhibitName 是展项目标选择器。
	Paths        []pathSpec `json:"paths"`
	AddPaths     []pathSpec `json:"add_paths"`
	RemovePaths  []string   `json:"remove_paths"`
	RepoPath     string     `json:"repo_path"`
	ExhibitID    string     `json:"exhibit_id"`
	ExhibitName  string     `json:"exhibit_name"`
	FromProject  string     `json:"from_project"`
	ToProject    string     `json:"to_project"`
	CreateTarget *bool      `json:"create_target"`
	// Exhibits / ExhibitsStep 用于从工作台同步展项（含 source_path/release_path 路径）。
	Exhibits     []map[string]any `json:"exhibits"`
	ExhibitsStep string           `json:"exhibits_step"`
	// ProjectIDs / FromProjects 供项目批量删除与多来源合并（登记管理的一次性清理）。
	ProjectIDs   []string `json:"project_ids"`
	FromProjects []string `json:"from_projects"`
	// ProjectsJSON 供登记表导入（完整登记表 JSON 文本）；Mode=merge（默认）或 replace。
	ProjectsJSON string `json:"projects_json"`
	// EntriesJSON 供日志条目导出/导入（work-log.entry.export / entry.import）：导出产出的 JSON 文本。
	EntriesJSON string `json:"entries_json"`
	// BackupPath 供 work-log.archive.restore 指定要恢复的备份包；留空则用最新一份。
	BackupPath string `json:"backup_path"`
	// Locked 供 work-log.entry.lock 设置/取消条目手工锁定。
	Locked *bool `json:"locked"`
	// DryRun 供迁移维护能力预览改动而不写盘。
	DryRun *bool `json:"dry_run"`
	// CrossDay 供 work-log.entry.dedupe 选择是否跨天合并同一会话：默认 false 时只合并
	// 「同一天、同一会话」的真正重复条目（采集器按活跃日归集后，同一长会话跨天出现是
	// 正常记录，不该被合并）；显式 true 才启用旧的跨天合并（清理历史误录）。
	CrossDay *bool `json:"cross_day"`
	// MaterialRoot 供 work-log.project.autofill 指定「AI 会话素材」目录；缺省用采集器
	// （com.himind.work-log-collector）私有数据目录下的 material/。
	MaterialRoot string `json:"material_root"`
	// Patch 供 work-log.entry.patch 做字段级局部更新：只改显式传入的键，未传的键保持原值。
	Patch map[string]any `json:"patch"`
	// Projects / ProjectsStep 用于从工作台同步项目（含展项）到本地登记表：
	// Projects 直接给数组，ProjectsStep 指向工作流里取工作台项目列表的上游步骤。
	Projects     []map[string]any `json:"projects"`
	ProjectsStep string           `json:"projects_step"`
	// 来源配置（通用可配置来源）：sources 为声明式来源规格数组。
	Sources  []map[string]any `json:"sources"`
	SourceID string           `json:"source_id"`
}

// ref 是条目引用的外部事实（提交、工单、链接等）。
type ref struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// entry 是一条工作日志。字段设计目标是同时支撑"采集草稿"与"汇总报告"。
type entry struct {
	EntryID   string `json:"entry_id"`
	Date      string `json:"date"`
	Source    string `json:"source"`
	SessionID string `json:"session_id"`
	Project   string `json:"project"`
	Feature   string `json:"feature,omitempty"`
	// Exhibit 是「展项」维度（工作台项目下的展项）。项目为主、展项可选：
	// 条目至少应落到项目；能从工作台或素材判定展项时再落到展项。
	Exhibit string `json:"exhibit,omitempty"`
	// ProjectID / ExhibitID 是命中工作台登记后的稳定 ID（未命中留空，供用户确认后再补）。
	ProjectID string `json:"project_id,omitempty"`
	ExhibitID string `json:"exhibit_id,omitempty"`
	// RepoPath 是会话工作目录对应的仓库根（采集器写入的稳定路径事实）。
	// 写入时按「路径 → 项目/展项」映射归类，未命中则留空并进入待认领。
	RepoPath string `json:"repo_path,omitempty"`
	// ProjectHint 是未归类时保留的原始项目名建议（AI/目录名/仓库自述），仅供待认领界面参考。
	ProjectHint string `json:"project_hint,omitempty"`
	Category    string `json:"category"`
	StartedAt   string `json:"started_at"`
	EndedAt     string `json:"ended_at"`
	// Minutes 是该条目的工时（分钟）。MinutesSource 标明来源：measured（按消息间隔实测的活跃时长）
	// / span（仅有起止时间的粗粒度区间）/ estimated（按轮次估算）/ manual（人工填写）。
	Minutes       int      `json:"minutes,omitempty"`
	MinutesSource string   `json:"minutes_source,omitempty"`
	Title         string   `json:"title"`
	Summary       string   `json:"summary"`
	Outcomes      []string `json:"outcomes"`
	Decisions     []string `json:"decisions"`
	Learnings     []string `json:"learnings"`
	Blockers      []string `json:"blockers"`
	Next          []string `json:"next"`
	Tags          []string `json:"tags"`
	Refs          []ref    `json:"refs"`
	CreatedAt     string   `json:"created_at"`
	UpdatedAt     string   `json:"updated_at"`
	// Locked 标记条目被手工锁定：自动重扫/写入不会再改写它（防覆盖），
	// 需要改动时先 work-log.entry.lock 解锁，或在 upsert 里显式 overwrite 整条替换。
	Locked bool `json:"locked,omitempty"`
}

// dayFile 是一天的日志文件，按天分片便于人读、diff 和迁移。
type dayFile struct {
	Date          string  `json:"date"`
	SchemaVersion string  `json:"schema_version"`
	Entries       []entry `json:"entries"`
	Count         int     `json:"count"`
}

func main() {
	if err := jsonrpc.Serve(os.Stdin, os.Stdout, handle); err != nil {
		fmt.Fprintln(os.Stderr, "工作日志插件已停止:", err)
	}
}

func handle(request jsonrpc.Request) (any, *jsonrpc.Error) {
	var in input
	if rpcError := jsonrpc.DecodeParams(request, &in); rpcError != nil {
		return nil, rpcError
	}
	switch request.Method {
	case "work-log.entry.upsert":
		result, err := upsert(in)
		return rpcResult(result, err)
	case "work-log.entry.list":
		result, err := listEntries(in)
		return rpcResult(result, err)
	case "work-log.entry.read":
		result, err := readEntries(in)
		return rpcResult(result, err)
	case "work-log.entry.delete":
		result, err := deleteEntry(in)
		return rpcResult(result, err)
	case "work-log.archive.list":
		result, err := archiveList(in)
		return rpcResult(result, err)
	case "work-log.archive.calendar":
		result, err := archiveCalendar(in)
		return rpcResult(result, err)
	case "work-log.project.list":
		result, err := projectListCap(in)
		return rpcResult(result, err)
	case "work-log.project.upsert":
		result, err := projectUpsertCap(in)
		return rpcResult(result, err)
	case "work-log.project.rename":
		result, err := projectRenameCap(in)
		return rpcResult(result, err)
	case "work-log.project.delete":
		result, err := projectDeleteCap(in)
		return rpcResult(result, err)
	case "work-log.project.resolve":
		result, err := projectResolveCap(in)
		return rpcResult(result, err)
	case "work-log.project.assign":
		result, err := projectAssignCap(in)
		return rpcResult(result, err)
	case "work-log.project.path.set":
		result, err := projectPathSetCap(in)
		return rpcResult(result, err)
	case "work-log.project.mapPath":
		result, err := projectMapPathCap(in)
		return rpcResult(result, err)
	case "work-log.project.claim":
		result, err := projectClaimCap(in)
		return rpcResult(result, err)
	case "work-log.project.merge":
		result, err := projectMergeCap(in)
		return rpcResult(result, err)
	case "work-log.project.autofill":
		result, err := projectAutofillCap(in)
		return rpcResult(result, err)
	case "work-log.project.export":
		result, err := projectExportCap(in)
		return rpcResult(result, err)
	case "work-log.project.import":
		result, err := projectImportCap(in)
		return rpcResult(result, err)
	case "work-log.entry.patch":
		result, err := entryPatchCap(in)
		return rpcResult(result, err)
	case "work-log.archive.migrate":
		result, err := archiveMigrateCap(in)
		return rpcResult(result, err)
	case "work-log.entry.dedupe":
		result, err := archiveDedupeCap(in)
		return rpcResult(result, err)
	case "work-log.entry.export":
		result, err := entryExportCap(in)
		return rpcResult(result, err)
	case "work-log.entry.import":
		result, err := entryImportCap(in)
		return rpcResult(result, err)
	case "work-log.entry.lock":
		result, err := entryLockCap(in)
		return rpcResult(result, err)
	case "work-log.entry.reconcile":
		result, err := entryReconcileCap(in)
		return rpcResult(result, err)
	case "work-log.archive.backup":
		result, err := archiveBackupCap(in)
		return rpcResult(result, err)
	case "work-log.archive.restore":
		result, err := archiveRestoreCap(in)
		return rpcResult(result, err)
	case "work-log.project.sync":
		result, err := projectSyncCap(in)
		return rpcResult(result, err)
	case "work-log.source.list":
		result, err := sourceListCap(in)
		return rpcResult(result, err)
	case "work-log.source.save":
		result, err := sourceSaveCap(in)
		return rpcResult(result, err)
	case "work-log.report.render":
		result, err := renderReport(in)
		return rpcResult(result, err)
	case "work-log.report.read":
		result, err := readReport(in)
		return rpcResult(result, err)
	case "work-log.report.aggregate":
		result, err := aggregateReports(in)
		return rpcResult(result, err)
	case "work-log.link.open":
		result, err := openLink(in)
		return rpcResult(result, err)
	case "work-log.link.reveal":
		result, err := revealPath(in)
		return rpcResult(result, err)
	default:
		return nil, &jsonrpc.Error{Code: -32602, Message: "不支持的工作日志能力"}
	}
}

func rpcResult(result any, err error) (any, *jsonrpc.Error) {
	if err != nil {
		return nil, &jsonrpc.Error{Code: -32000, Message: err.Error()}
	}
	return result, nil
}

// upstreamStepOutput 读取工作流注入的上游步骤输出。
//
// 步骤 id 由工作流在 step.input 里显式声明（entries_step / narrative_step），
// 插件不硬编码某条工作流的步骤名，也不猜执行顺序。
func upstreamStepOutput(in input, stepID string) map[string]any {
	stepID = strings.TrimSpace(stepID)
	if stepID == "" || len(in.WorkflowContext) == 0 {
		return nil
	}
	steps, ok := in.WorkflowContext["steps"].(map[string]any)
	if !ok {
		return nil
	}
	output, ok := steps[stepID].(map[string]any)
	if !ok {
		return nil
	}
	return output
}

// resolveEntries 取待写入的条目：显式 entries/entry 优先，否则取 EntriesStep 的上游输出。
// 若同时声明了 BaseStep（确定性素材），则按「来源+会话」把 AI 总结的人类可读字段叠加到
// 素材的确定性字段上，工时/起止时间等一律以素材为准。
func resolveEntries(in input) []entry {
	overrides := in.Entries
	if len(overrides) == 0 {
		overrides = entriesFromStep(in, in.EntriesStep)
	}
	base := entriesFromStep(in, in.BaseStep)
	if len(base) == 0 || len(overrides) == 0 {
		return overrides
	}
	index := make(map[string]entry, len(base))
	for _, item := range base {
		if key := sessionEntryKey(item); key != "" {
			index[key] = item
		}
	}
	merged := make([]entry, 0, len(overrides))
	for _, item := range overrides {
		if key := sessionEntryKey(item); key != "" {
			if baseItem, ok := index[key]; ok {
				merged = append(merged, mergeBaseEntry(baseItem, item))
				continue
			}
		}
		merged = append(merged, item)
	}
	return merged
}

// entriesFromStep 读取某个上游步骤输出里的 entries 数组。
func entriesFromStep(in input, stepID string) []entry {
	if strings.TrimSpace(stepID) == "" {
		return nil
	}
	output := upstreamStepOutput(in, stepID)
	if output == nil {
		return nil
	}
	raw, ok := output["entries"]
	if !ok {
		return nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var entries []entry
	if json.Unmarshal(data, &entries) != nil {
		return nil
	}
	return entries
}

// sessionEntryKey 用「来源+会话」作为同一次会话的稳定键；没有会话 id 时不参与合并。
func sessionEntryKey(item entry) string {
	session := strings.TrimSpace(item.SessionID)
	if session == "" {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(item.Source)) + "|" + session
}

// mergeBaseEntry 以素材条目为底，叠加 AI 总结的人类可读字段；确定性字段（工时、起止时间、
// 来源、会话、日期）不被 AI 覆盖。
func mergeBaseEntry(base, ai entry) entry {
	merged := base
	if value := strings.TrimSpace(ai.Title); value != "" {
		merged.Title = value
	}
	if value := strings.TrimSpace(ai.Summary); value != "" {
		merged.Summary = value
	}
	if value := strings.TrimSpace(ai.Category); value != "" {
		merged.Category = value
	}
	if value := strings.TrimSpace(ai.Project); value != "" {
		merged.Project = value
	}
	if value := strings.TrimSpace(ai.Feature); value != "" {
		merged.Feature = value
	}
	if len(ai.Outcomes) > 0 {
		merged.Outcomes = ai.Outcomes
	}
	if len(ai.Decisions) > 0 {
		merged.Decisions = ai.Decisions
	}
	if len(ai.Learnings) > 0 {
		merged.Learnings = ai.Learnings
	}
	if len(ai.Blockers) > 0 {
		merged.Blockers = ai.Blockers
	}
	if len(ai.Next) > 0 {
		merged.Next = ai.Next
	}
	merged.Tags = appendUniqueStrings(base.Tags, ai.Tags...)
	merged.Tags = appendUniqueStrings(merged.Tags, "AI总结")
	return merged
}

// resolveNarrative 优先用显式 narrative，否则取上游步骤提炼的主线叙述。
func resolveNarrative(in input) string {
	if text := strings.TrimSpace(in.Narrative); text != "" {
		return text
	}
	output := upstreamStepOutput(in, in.NarrativeStep)
	if output == nil {
		return ""
	}
	text, _ := output["narrative"].(string)
	return strings.TrimSpace(text)
}

// ---------- 写入 ----------

func upsert(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	date := normalizeDate(in.Date)
	if !validDate(date) {
		return nil, errors.New("date 必须是 YYYY-MM-DD 格式")
	}
	// 若提供了工作台项目（含展项），先并入登记表；后续条目归一与项目/展项落点会用到。
	// 同步失败不阻断写入：登记表已有内容仍可继续按名称归一。
	if len(in.Projects) > 0 || strings.TrimSpace(in.ProjectsStep) != "" {
		_, _ = projectSyncCap(in)
	}
	incoming := append([]entry{}, resolveEntries(in)...)
	if in.Entry != nil {
		incoming = append(incoming, *in.Entry)
	}
	if len(incoming) == 0 {
		// 直接写入（手工录入）没有条目是误用，仍然报错。
		// 但若声明了上游步骤（采集素材 / AI 总结），说明这是工作流链路：
		// 当天确实没有真实工作时应视为「无日志」的成功空操作，
		// 让定时采集在无工作日平稳跳过，而不是把整次运行判失败。
		if strings.TrimSpace(in.EntriesStep) == "" && strings.TrimSpace(in.BaseStep) == "" {
			return nil, errors.New("entries 不能为空：请至少提供一条日志")
		}
		return map[string]any{
			"date":    date,
			"count":   0,
			"entries": []entry{},
			"no_op":   true,
			"reason":  "本日没有可写入的日志条目",
		}, nil
	}

	unlock := lockRoot(root)
	defer unlock()

	day, err := readDay(root, date)
	if err != nil {
		return nil, err
	}
	// 不再支持整日清空：早期曾用 mode=replace 先清空当天再写入，风险是连同手工条目
	// 一起抹掉。写入一律按 entry_id 幂等合并；需要删除请用 work-log.entry.delete。
	now := time.Now().Format(time.RFC3339)
	lockedSkipped := 0
	for _, raw := range incoming {
		item := normalizeEntry(raw, date, now)
		existing := -1
		for index := range day.Entries {
			if day.Entries[index].EntryID == item.EntryID {
				existing = index
				break
			}
		}
		if existing >= 0 {
			// 手工锁定的条目不再被自动写入/合并改写（防覆盖）；只有显式 overwrite 才允许整条替换。
			if day.Entries[existing].Locked && !in.Overwrite {
				lockedSkipped++
				continue
			}
			// 合并语义：保留首次创建时间，逐字段以新值覆盖，空字段不冲掉旧值。
			// overwrite=true 时改为整条替换，让编辑可以真正清空字段。
			item.CreatedAt = day.Entries[existing].CreatedAt
			if in.Overwrite {
				day.Entries[existing] = item
			} else {
				day.Entries[existing] = mergeEntry(day.Entries[existing], item)
			}
		} else {
			day.Entries = append(day.Entries, item)
		}
	}
	sortEntries(day.Entries)
	day.Date = date
	day.SchemaVersion = schemaVersion
	resolve := in.ResolveProjects == nil || *in.ResolveProjects
	if _, err := applyProjectRegistry(root, day.Entries, resolve); err != nil {
		return nil, err
	}
	// 用登记表补上稳定的项目/展项 ID（工作台同步进来的项目也会在这里被命中）。
	if resolve {
		if pf, err := readProjects(root); err == nil {
			enrichEntryLinks(pf, day.Entries)
		}
	}
	if err := writeDay(root, day); err != nil {
		return nil, err
	}
	path := dayPath(root, date)
	digest, err := sha256OfFile(path)
	if err != nil {
		return nil, errors.New("日志摘要计算失败")
	}
	return map[string]any{
		"date":           date,
		"count":          len(day.Entries),
		"entries":        day.Entries,
		"locked_skipped": lockedSkipped,
		"artifacts": []map[string]any{{
			"artifact_id":   "work-log-entry",
			"artifact_type": "work_log_entry",
			"name":          "工作日志 " + date,
			"uri":           fileURIPath(path),
			"sha256":        digest,
		}},
	}, nil
}

// mergeEntry 用 incoming 覆盖 current，但不让 incoming 的空值冲掉已有内容。
func mergeEntry(current, incoming entry) entry {
	merged := incoming
	if merged.Project == "" {
		merged.Project = current.Project
	}
	if merged.Feature == "" {
		merged.Feature = current.Feature
	}
	if merged.Category == "" {
		merged.Category = current.Category
	}
	if merged.Minutes == 0 {
		merged.Minutes = current.Minutes
	}
	if merged.MinutesSource == "" {
		merged.MinutesSource = current.MinutesSource
	}
	if merged.Source == "" {
		merged.Source = current.Source
	}
	if merged.SessionID == "" {
		merged.SessionID = current.SessionID
	}
	if merged.Title == "" {
		merged.Title = current.Title
	}
	if merged.Summary == "" {
		merged.Summary = current.Summary
	}
	if merged.StartedAt == "" {
		merged.StartedAt = current.StartedAt
	}
	if merged.EndedAt == "" {
		merged.EndedAt = current.EndedAt
	}
	if len(merged.Outcomes) == 0 {
		merged.Outcomes = current.Outcomes
	}
	if len(merged.Decisions) == 0 {
		merged.Decisions = current.Decisions
	}
	if len(merged.Learnings) == 0 {
		merged.Learnings = current.Learnings
	}
	if len(merged.Blockers) == 0 {
		merged.Blockers = current.Blockers
	}
	if len(merged.Next) == 0 {
		merged.Next = current.Next
	}
	if len(merged.Tags) == 0 {
		merged.Tags = current.Tags
	}
	if len(merged.Refs) == 0 {
		merged.Refs = current.Refs
	}
	// 锁定是可累积的：已锁定或本次显式锁定的条目保持锁定，自动合并不会解锁。
	merged.Locked = current.Locked || merged.Locked
	return merged
}

// normalizeEntry 清洗单条日志：补日期、去空、归一空数组、生成稳定 ID。
func normalizeEntry(item entry, date, now string) entry {
	item.Date = date
	item.Source = fallbackValue(strings.TrimSpace(item.Source), "manual")
	item.EntryID = strings.TrimSpace(item.EntryID)
	item.Project = strings.TrimSpace(item.Project)
	item.Exhibit = strings.TrimSpace(item.Exhibit)
	item.RepoPath = strings.TrimSpace(item.RepoPath)
	item.ProjectHint = strings.TrimSpace(item.ProjectHint)
	item.Category = fallbackValue(strings.TrimSpace(item.Category), defaultCategory)
	item.SessionID = strings.TrimSpace(item.SessionID)
	item.StartedAt = strings.TrimSpace(item.StartedAt)
	item.EndedAt = strings.TrimSpace(item.EndedAt)
	item.Title = strings.TrimSpace(item.Title)
	item.Summary = strings.TrimSpace(item.Summary)
	item.Outcomes = nonNilStrings(trimList(item.Outcomes))
	item.Decisions = nonNilStrings(trimList(item.Decisions))
	item.Learnings = nonNilStrings(trimList(item.Learnings))
	item.Blockers = nonNilStrings(trimList(item.Blockers))
	item.Next = nonNilStrings(trimList(item.Next))
	item.Tags = nonNilStrings(trimList(item.Tags))
	item.Refs = nonNilRefs(item.Refs)
	if item.EntryID == "" {
		item.EntryID = makeEntryID(item)
	}
	if item.CreatedAt == "" {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	return item
}

// makeEntryID 生成稳定去重键：优先按「来源+会话+日期」指纹，保证同一会话被重复
// 采集/重扫时幂等合并（时间等字段变化不影响身份）；没有会话时退回标题指纹，避免
// 同一天两条手工记录互相覆盖；标题也为空时才按纳秒生成一次性键。
func makeEntryID(item entry) string {
	if item.SessionID != "" {
		return "wl-" + shortHash(strings.Join([]string{item.Source, item.SessionID, item.Date}, "|"))
	}
	if item.Title != "" {
		return "wl-" + shortHash(strings.Join([]string{item.Source, item.Date, item.Title}, "|"))
	}
	return fmt.Sprintf("wl-%s-%d", strings.ReplaceAll(item.Date, "-", ""), time.Now().UnixNano())
}

func shortHash(basis string) string {
	sum := sha1.Sum([]byte(basis))
	return hex.EncodeToString(sum[:])[:12]
}

// ---------- 查询 ----------

func listEntries(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	from, to, err := resolveRange(in.From, in.To, in.Date)
	if err != nil {
		return nil, err
	}
	all, err := collectEntries(root, from, to)
	if err != nil {
		return nil, err
	}
	filtered := make([]entry, 0, len(all))
	for _, item := range all {
		if !matches(item, in) {
			continue
		}
		filtered = append(filtered, item)
	}
	limit := in.Limit
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return map[string]any{
		"log_root": root,
		"from":     from,
		"to":       to,
		"total":    len(filtered),
		"entries":  filtered,
	}, nil
}

func readEntries(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	date := normalizeDate(in.Date)
	if !validDate(date) {
		return nil, errors.New("date 必须是 YYYY-MM-DD 格式")
	}
	day, err := readDay(root, date)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"log_root": root,
		"date":     date,
		"entries":  nonNilEntries(day.Entries),
	}, nil
}

// deleteEntry 从某天的日志里移除指定 entry_id 的条目。
//
// 删除到当天无剩余条目时，连同当天的日志文件一并移除，避免日历上残留
// 「有文件但 0 条」的空白日期。删除是显式动作，不做采集侧的幂等合并。
func deleteEntry(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	date := normalizeDate(in.Date)
	if !validDate(date) {
		return nil, errors.New("date 必须是 YYYY-MM-DD 格式")
	}
	targets := map[string]struct{}{}
	if id := strings.TrimSpace(in.EntryID); id != "" {
		targets[id] = struct{}{}
	}
	for _, id := range in.EntryIDs {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			targets[trimmed] = struct{}{}
		}
	}
	if len(targets) == 0 {
		return nil, errors.New("需要 entry_id 或 entry_ids")
	}

	unlock := lockRoot(root)
	defer unlock()

	day, err := readDay(root, date)
	if err != nil {
		return nil, err
	}
	kept := make([]entry, 0, len(day.Entries))
	removed := 0
	for _, item := range day.Entries {
		if _, hit := targets[item.EntryID]; hit {
			removed++
			continue
		}
		kept = append(kept, item)
	}
	if removed == 0 {
		return map[string]any{
			"log_root": root,
			"date":     date,
			"removed":  0,
			"count":    len(day.Entries),
		}, nil
	}
	if len(kept) == 0 {
		if err := os.Remove(dayPath(root, date)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("日志文件删除失败")
		}
		return map[string]any{
			"log_root":    root,
			"date":        date,
			"removed":     removed,
			"count":       0,
			"deleted_day": true,
		}, nil
	}
	day.Entries = kept
	if err := writeDay(root, day); err != nil {
		return nil, err
	}
	return map[string]any{
		"log_root": root,
		"date":     date,
		"removed":  removed,
		"count":    len(kept),
	}, nil
}

func matches(item entry, in input) bool {
	if p := strings.TrimSpace(in.Project); p != "" && !strings.Contains(strings.ToLower(item.Project), strings.ToLower(p)) {
		return false
	}
	if c := strings.TrimSpace(in.Category); c != "" && !strings.EqualFold(item.Category, c) {
		return false
	}
	if s := strings.TrimSpace(in.Source); s != "" && !strings.EqualFold(item.Source, s) {
		return false
	}
	if t := strings.TrimSpace(in.Tag); t != "" {
		found := false
		for _, tag := range item.Tags {
			if strings.EqualFold(tag, t) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if q := strings.TrimSpace(in.Query); q != "" && !entryMatchText(item, q) {
		return false
	}
	return true
}

// entryMatchText 在标题、摘要、功能、项目与标签里做不区分大小写的子串匹配。
func entryMatchText(item entry, query string) bool {
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" {
		return true
	}
	haystack := []string{item.Title, item.Summary, item.Feature, item.Project, categoryLabel(item.Category)}
	haystack = append(haystack, item.Tags...)
	for _, value := range haystack {
		if strings.Contains(strings.ToLower(value), needle) {
			return true
		}
	}
	return false
}

type daySummary struct {
	Date         string   `json:"date"`
	EntryCount   int      `json:"entry_count"`
	TotalMinutes int      `json:"total_minutes,omitempty"`
	Projects     []string `json:"projects"`
	Categories   []string `json:"categories"`
}

func archiveList(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	dates, err := listDates(root)
	if err != nil {
		return nil, err
	}
	// 视图需要「不传区间即列出全部」，因此这里只在显式提供边界时才过滤。
	from := strings.TrimSpace(in.From)
	to := strings.TrimSpace(in.To)
	if anchor := strings.TrimSpace(in.Date); anchor != "" {
		if from == "" {
			from = anchor
		}
		if to == "" {
			to = anchor
		}
	}
	if from != "" && !validDate(from) {
		return nil, errors.New("from 必须是 YYYY-MM-DD 格式")
	}
	if to != "" && !validDate(to) {
		return nil, errors.New("to 必须是 YYYY-MM-DD 格式")
	}
	if from != "" && to != "" && from > to {
		from, to = to, from
	}
	summaries := make([]daySummary, 0, len(dates))
	for _, date := range dates {
		if from != "" && date < from {
			continue
		}
		if to != "" && date > to {
			continue
		}
		day, err := readDay(root, date)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, summarizeDay(date, day.Entries))
	}
	sort.SliceStable(summaries, func(left, right int) bool { return summaries[left].Date > summaries[right].Date })
	limit := in.Limit
	if limit > 0 && len(summaries) > limit {
		summaries = summaries[:limit]
	}
	reports, err := listReports(root)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"log_root": root,
		"total":    len(summaries),
		"days":     summaries,
		"reports":  reports,
	}, nil
}

func summarizeDay(date string, entries []entry) daySummary {
	projects := map[string]struct{}{}
	categories := map[string]struct{}{}
	for _, item := range entries {
		if item.Project != "" {
			projects[item.Project] = struct{}{}
		}
		if item.Category != "" {
			categories[item.Category] = struct{}{}
		}
	}
	return daySummary{
		Date:         date,
		EntryCount:   len(entries),
		TotalMinutes: groupMinutes(entries),
		Projects:     sortedKeys(projects),
		Categories:   sortedKeys(categories),
	}
}

// summarizeDayFiltered 只在命中项目过滤时统计当天的条目，用于「按项目看日历」。
func summarizeDayFiltered(date string, entries []entry, project string) daySummary {
	keyword := strings.ToLower(strings.TrimSpace(project))
	projects := map[string]struct{}{}
	categories := map[string]struct{}{}
	count := 0
	minutes := 0
	for _, item := range entries {
		if !strings.Contains(strings.ToLower(item.Project), keyword) {
			continue
		}
		count++
		minutes += entryMinutes(item)
		if item.Project != "" {
			projects[item.Project] = struct{}{}
		}
		if item.Category != "" {
			categories[item.Category] = struct{}{}
		}
	}
	return daySummary{
		Date:         date,
		EntryCount:   count,
		TotalMinutes: minutes,
		Projects:     sortedKeys(projects),
		Categories:   sortedKeys(categories),
	}
}

// calendarCell 是月历里的一天：既承载当天的计数，也承载宿主渲染所需的星期信息。
type calendarCell struct {
	Date         string   `json:"date"`
	Day          int      `json:"day"`
	InMonth      bool     `json:"in_month"`
	IsToday      bool     `json:"is_today"`
	EntryCount   int      `json:"entry_count"`
	TotalMinutes int      `json:"total_minutes,omitempty"`
	Projects     []string `json:"projects"`
	Categories   []string `json:"categories"`
}

// archiveCalendar 按月构建可日历查看的日志统计。
//
// 这是「按日为核心」的主视图：以自然月为窗口给出完整的 7 列栅格（含相邻月的补位日），
// 每天标注条目数、项目与分类，便于一眼看出哪些天有记录。补位日也会带上相邻月的真实
// 计数，避免格子里出现「有日志却空白」的错觉。
func archiveCalendar(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	year, month, err := resolveYearMonth(in)
	if err != nil {
		return nil, err
	}
	weekStart := 1 // 1=周一开头（默认），0=周日开头。
	if strings.EqualFold(strings.TrimSpace(in.WeekStart), "sunday") {
		weekStart = 0
	}
	first := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.Local)
	daysInMonth := time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.Local).Day()
	last := time.Date(year, time.Month(month), daysInMonth, 0, 0, 0, 0, time.Local)

	// 计算栅格首尾：向前补到周首，再向后补满整周。
	firstISO := int(first.Weekday())
	if firstISO == 0 {
		firstISO = 7
	}
	lead := firstISO - 1
	if weekStart == 0 {
		lead = int(first.Weekday())
	}
	lastISO := int(last.Weekday())
	if lastISO == 0 {
		lastISO = 7
	}
	trail := 7 - lastISO
	if weekStart == 0 {
		trail = 6 - int(last.Weekday())
	}

	today := time.Now().Format("2006-01-02")
	weeks := make([][]calendarCell, 0, 6)
	week := make([]calendarCell, 0, 7)
	days := make([]calendarCell, 0, 31)
	activeDays := 0
	totalEntries := 0
	totalMinutes := 0
	for cursor := first.AddDate(0, 0, -lead); !cursor.After(last.AddDate(0, 0, trail)); cursor = cursor.AddDate(0, 0, 1) {
		date := cursor.Format("2006-01-02")
		day, err := readDay(root, date)
		if err != nil {
			return nil, err
		}
		summary := summarizeDay(date, day.Entries)
		if project := strings.TrimSpace(in.Project); project != "" {
			summary = summarizeDayFiltered(date, day.Entries, project)
		}
		inMonth := cursor.Year() == year && int(cursor.Month()) == month
		cell := calendarCell{
			Date:         date,
			Day:          cursor.Day(),
			InMonth:      inMonth,
			IsToday:      date == today,
			EntryCount:   summary.EntryCount,
			TotalMinutes: summary.TotalMinutes,
			Projects:     summary.Projects,
			Categories:   summary.Categories,
		}
		if inMonth {
			days = append(days, cell)
			if summary.EntryCount > 0 {
				activeDays++
			}
			totalEntries += summary.EntryCount
			totalMinutes += summary.TotalMinutes
		}
		week = append(week, cell)
		if len(week) == 7 {
			weeks = append(weeks, week)
			week = make([]calendarCell, 0, 7)
		}
	}
	weekStartName := "monday"
	if weekStart == 0 {
		weekStartName = "sunday"
	}
	return map[string]any{
		"log_root":      root,
		"year":          year,
		"month":         month,
		"month_key":     fmt.Sprintf("%04d-%02d", year, month),
		"week_start":    weekStartName,
		"first_weekday": firstISO,
		"days_in_month": daysInMonth,
		"today":         today,
		"active_days":   activeDays,
		"total_entries": totalEntries,
		"total_minutes": totalMinutes,
		"days":          days,
		"weeks":         weeks,
	}, nil
}

// resolveYearMonth 优先用显式 year/month，否则从 date 推导，最后回落到今天。
func resolveYearMonth(in input) (int, int, error) {
	if in.Year != 0 || in.Month != 0 {
		if in.Year < 1970 || in.Year > 9999 {
			return 0, 0, errors.New("year 超出范围")
		}
		if in.Month < 1 || in.Month > 12 {
			return 0, 0, errors.New("month 必须在 1-12 之间")
		}
		return in.Year, in.Month, nil
	}
	anchor := normalizeDate(in.Date)
	if !validDate(anchor) {
		anchor = time.Now().Format("2006-01-02")
	}
	t, err := time.Parse("2006-01-02", anchor)
	if err != nil {
		return 0, 0, errors.New("date 必须是 YYYY-MM-DD 格式")
	}
	return t.Year(), int(t.Month()), nil
}

type reportRecord struct {
	Period    string `json:"period"`
	Key       string `json:"key"`
	Title     string `json:"title"`
	Markdown  string `json:"markdown_path"`
	HTML      string `json:"html_path"`
	UpdatedAt string `json:"updated_at"`
}

func listReports(root string) ([]reportRecord, error) {
	records := make([]reportRecord, 0)
	periods := []string{"daily", "weekly", "monthly", "quarterly", "yearly"}
	for _, period := range periods {
		dir := filepath.Join(root, "reports", period)
		names, err := readDirNames(dir)
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			if !strings.HasSuffix(name, ".md") {
				continue
			}
			key := strings.TrimSuffix(name, ".md")
			path := filepath.Join(dir, name)
			info, statErr := os.Stat(path)
			updated := ""
			if statErr == nil {
				updated = info.ModTime().Format(time.RFC3339)
			}
			records = append(records, reportRecord{
				Period:    period,
				Key:       key,
				Title:     periodLabel(period) + " " + key,
				Markdown:  path,
				HTML:      filepath.Join(dir, key+".html"),
				UpdatedAt: updated,
			})
		}
	}
	sort.SliceStable(records, func(left, right int) bool { return records[left].UpdatedAt > records[right].UpdatedAt })
	return records, nil
}

// ---------- 报告 ----------

// reportTarget 解析报告目标：周期（默认 daily）与锚点日期（默认今天）。
func reportTarget(in input) (string, string, error) {
	period := strings.ToLower(strings.TrimSpace(in.Period))
	if period == "" {
		period = "daily"
	}
	if !isKnownPeriod(period) {
		return "", "", errors.New("period 只能是 daily / weekly / monthly / quarterly / yearly")
	}
	anchor := normalizeDate(in.Date)
	if !validDate(anchor) {
		return "", "", errors.New("date 必须是 YYYY-MM-DD 格式")
	}
	return period, anchor, nil
}

// renderPeriodReport 渲染并落盘一个报告周期。本期没有任何条目时返回 rendered=false，
// 不落空文件（避免无工作日堆积空白报告）；成功时返回值里带 md/html/json 路径。
func renderPeriodReport(root, period, anchor, title, narrative string, narratives map[string]string) (map[string]any, error) {
	start, end, key, err := periodRange(period, anchor)
	if err != nil {
		return nil, err
	}
	entries, err := collectEntries(root, start, end)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		// 本期没有任何日志条目：不落空报告，避免在无工作日在 reports 目录
		// 里堆积「本期没有记录」的空白文件；工作流链路据此正常收尾。
		return map[string]any{
			"period":      period,
			"key":         key,
			"date_from":   start,
			"date_to":     end,
			"entry_count": 0,
			"rendered":    false,
			"reason":      "本期没有日志条目，跳过报告生成",
		}, nil
	}
	if strings.TrimSpace(title) == "" {
		title = periodLabel(period) + " · " + key
	} else {
		title = strings.TrimSpace(title)
	}
	markdown := renderMarkdown(title, period, key, start, end, entries, narrative, narratives)
	htmlBody := renderHTML(title, period, key, start, end, entries, narrative, narratives)

	dir := filepath.Join(root, "reports", period)
	mdPath := filepath.Join(dir, key+".md")
	htmlPath := filepath.Join(dir, key+".html")
	if err := writeFileAtomic(mdPath, []byte(markdown)); err != nil {
		return nil, errors.New("报告 Markdown 写入失败")
	}
	if err := writeFileAtomic(htmlPath, []byte(htmlBody)); err != nil {
		return nil, errors.New("报告 HTML 写入失败")
	}
	report := map[string]any{
		"period":         period,
		"key":            key,
		"title":          title,
		"date_from":      start,
		"date_to":        end,
		"entry_count":    len(entries),
		"total_minutes":  groupMinutes(entries),
		"narrative_used": narrative != "" || len(narratives) > 0,
		"md_path":        mdPath,
		"html_path":      htmlPath,
		"markdown":       markdown,
		"rendered":       true,
	}
	measuredMinutes, estimatedMinutes := splitMinutes(entries)
	report["measured_minutes"] = measuredMinutes
	report["estimated_minutes"] = estimatedMinutes
	jsonPath := filepath.Join(dir, key+".json")
	jsonBody, err := json.MarshalIndent(map[string]any{"report": report}, "", "  ")
	if err != nil {
		return nil, errors.New("报告 JSON 序列化失败")
	}
	if err := writeFileAtomic(jsonPath, append(jsonBody, '\n')); err != nil {
		return nil, errors.New("报告 JSON 写入失败")
	}
	report["json_path"] = jsonPath
	return report, nil
}

func renderReport(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	period, anchor, err := reportTarget(in)
	if err != nil {
		return nil, err
	}
	report, err := renderPeriodReport(root, period, anchor, strings.TrimSpace(in.Title), resolveNarrative(in), in.Narratives)
	if err != nil {
		return nil, err
	}
	if rendered, ok := report["rendered"].(bool); ok && !rendered {
		return report, nil
	}
	jsonPath, _ := report["json_path"].(string)
	jsonDigest, err := sha256OfFile(jsonPath)
	if err != nil {
		return nil, errors.New("报告摘要计算失败")
	}
	title, _ := report["title"].(string)
	return map[string]any{
		"report": report,
		"artifacts": []map[string]any{{
			"artifact_id":   "work-log-report",
			"artifact_type": "work_log_report",
			"name":          title,
			"uri":           fileURIPath(jsonPath),
			"sha256":        jsonDigest,
		}},
	}, nil
}

// ---------- 周期汇总 ----------

// defaultAggregatePeriods 是自动汇总的默认周期集合：周报 + 月报。
var defaultAggregatePeriods = []string{"weekly", "monthly"}

func normalizeAggregatePeriods(values []string) ([]string, error) {
	if len(values) == 0 {
		return append([]string(nil), defaultAggregatePeriods...), nil
	}
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, raw := range values {
		period := strings.ToLower(strings.TrimSpace(raw))
		if period == "" || seen[period] {
			continue
		}
		if !isKnownPeriod(period) {
			return nil, errors.New("periods 只支持 daily / weekly / monthly / quarterly / yearly")
		}
		seen[period] = true
		out = append(out, period)
	}
	if len(out) == 0 {
		return append([]string(nil), defaultAggregatePeriods...), nil
	}
	return out, nil
}

// reportExists 判断某个自然周期的归档记录是否已经存在（以 .md 为准）。
func reportExists(root, period, key string) bool {
	_, err := os.Stat(filepath.Join(root, "reports", period, key+".md"))
	return err == nil
}

// periodShift 把锚点日期前后移动整数个自然周期。
func periodShift(period, anchor string, delta int) (string, error) {
	parsed, err := time.Parse("2006-01-02", anchor)
	if err != nil {
		return "", errors.New("date 必须是 YYYY-MM-DD 格式")
	}
	switch period {
	case "daily":
		parsed = parsed.AddDate(0, 0, delta)
	case "weekly":
		parsed = parsed.AddDate(0, 0, delta*7)
	case "monthly":
		parsed = parsed.AddDate(0, delta, 0)
	case "quarterly":
		parsed = parsed.AddDate(0, delta*3, 0)
	case "yearly":
		parsed = parsed.AddDate(delta, 0, 0)
	default:
		return "", errors.New("period 只能是 daily / weekly / monthly / quarterly / yearly")
	}
	return parsed.Format("2006-01-02"), nil
}

// aggregatePeriod 汇总锚点所在的单个自然周期。force=false 时若已有归档记录则跳过，
// 保证同一自然周 / 月只会存在一条记录。
func aggregatePeriod(root, period, anchor string, force bool, narrative string, narratives map[string]string) (map[string]any, error) {
	start, end, key, err := periodRange(period, anchor)
	if err != nil {
		return nil, err
	}
	if !force && reportExists(root, period, key) {
		return map[string]any{
			"period": period, "key": key, "date_from": start, "date_to": end,
			"status": "skipped", "reason": "该周期已有归档记录，未重复生成",
		}, nil
	}
	report, err := renderPeriodReport(root, period, anchor, "", narrative, narratives)
	if err != nil {
		return nil, err
	}
	if rendered, ok := report["rendered"].(bool); ok && !rendered {
		return map[string]any{
			"period": period, "key": key, "date_from": start, "date_to": end,
			"status": "empty", "reason": "本期没有日志条目",
		}, nil
	}
	return map[string]any{
		"period": period, "key": key, "date_from": start, "date_to": end,
		"status":        "rendered",
		"title":         report["title"],
		"entry_count":   report["entry_count"],
		"total_minutes": report["total_minutes"],
		"md_path":       report["md_path"],
		"html_path":     report["html_path"],
		"json_path":     report["json_path"],
	}, nil
}

// aggregateCompleted 从锚点回溯「已结束」的自然周期，逐个补算缺失的归档。锚点所在
// 的当前周期一定不算已结束（先退回上一期）；回溯到早于最早日志的周期即停止；单次
// 最多处理 lookback 个有数据的周期，避免一次性补出大量历史。已存在的记录默认跳过。
func aggregateCompleted(root, period, anchor string, force bool, lookback int, narrative string, narratives map[string]string) ([]map[string]any, int, error) {
	dates, err := listDates(root)
	if err != nil {
		return nil, 0, err
	}
	results := make([]map[string]any, 0)
	if len(dates) == 0 {
		return results, 0, nil
	}
	earliest := dates[0]
	probe := anchor
	_, end, _, err := periodRange(period, probe)
	if err != nil {
		return nil, 0, err
	}
	if end >= anchor {
		probe, err = periodShift(period, probe, -1)
		if err != nil {
			return nil, 0, err
		}
	}
	skipped, counted := 0, 0
	for step := 0; step < 400; step++ {
		start, periodEnd, key, err := periodRange(period, probe)
		if err != nil {
			return nil, 0, err
		}
		if periodEnd >= anchor || periodEnd < earliest {
			break
		}
		entries, err := collectEntries(root, start, periodEnd)
		if err != nil {
			return nil, 0, err
		}
		if len(entries) == 0 {
			probe, err = periodShift(period, probe, -1)
			if err != nil {
				return nil, 0, err
			}
			continue
		}
		counted++
		if !force && reportExists(root, period, key) {
			skipped++
		} else {
			item, err := aggregatePeriod(root, period, probe, force, narrative, narratives)
			if err != nil {
				return nil, 0, err
			}
			results = append(results, item)
		}
		if counted >= lookback {
			break
		}
		probe, err = periodShift(period, probe, -1)
		if err != nil {
			return nil, 0, err
		}
	}
	return results, skipped, nil
}

func aggregateReports(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	anchor := normalizeDate(in.Date)
	if !validDate(anchor) {
		return nil, errors.New("date 必须是 YYYY-MM-DD 格式")
	}
	periods, err := normalizeAggregatePeriods(in.Periods)
	if err != nil {
		return nil, err
	}
	completedOnly := true
	if in.CompletedOnly != nil {
		completedOnly = *in.CompletedOnly
	}
	force := in.Force != nil && *in.Force
	lookback := 24
	if in.Lookback != nil && *in.Lookback > 0 {
		lookback = *in.Lookback
	}
	narrative := resolveNarrative(in)

	results := make([]map[string]any, 0)
	rendered, skipped, empty := 0, 0, 0
	for _, period := range periods {
		if completedOnly {
			items, skipCount, err := aggregateCompleted(root, period, anchor, force, lookback, narrative, in.Narratives)
			if err != nil {
				return nil, err
			}
			skipped += skipCount
			for _, item := range items {
				switch item["status"] {
				case "rendered":
					rendered++
				case "empty":
					empty++
				}
				results = append(results, item)
			}
			continue
		}
		item, err := aggregatePeriod(root, period, anchor, force, narrative, in.Narratives)
		if err != nil {
			return nil, err
		}
		switch item["status"] {
		case "rendered":
			rendered++
		case "skipped":
			skipped++
		case "empty":
			empty++
		}
		results = append(results, item)
	}
	return map[string]any{
		"log_root":       root,
		"date":           anchor,
		"periods":        periods,
		"completed_only": completedOnly,
		"force":          force,
		"lookback":       lookback,
		"rendered":       rendered,
		"skipped":        skipped,
		"empty":          empty,
		"results":        results,
	}, nil
}

func readReport(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	period := strings.ToLower(strings.TrimSpace(in.Period))
	if period == "" {
		period = "daily"
	}
	if !isKnownPeriod(period) {
		return nil, errors.New("period 只能是 daily / weekly / monthly / quarterly / yearly")
	}
	anchor := normalizeDate(in.Date)
	if !validDate(anchor) {
		return nil, errors.New("date 必须是 YYYY-MM-DD 格式")
	}
	start, end, key, err := periodRange(period, anchor)
	if err != nil {
		return nil, err
	}
	mdPath := filepath.Join(root, "reports", period, key+".md")
	htmlPath := filepath.Join(root, "reports", period, key+".html")
	mdBody, err := os.ReadFile(mdPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("该周期还没有报告：" + key)
	}
	if err != nil {
		return nil, errors.New("报告 Markdown 读取失败")
	}
	htmlBody, _ := os.ReadFile(htmlPath)
	return map[string]any{
		"log_root":  root,
		"period":    period,
		"key":       key,
		"date_from": start,
		"date_to":   end,
		"markdown":  string(mdBody),
		"html":      string(htmlBody),
		"md_path":   mdPath,
		"html_path": htmlPath,
	}, nil
}

// ---------- 存储 ----------

func logRoot(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	explicit := trimmed != ""
	if !explicit {
		trimmed = readState().LogRoot
		// 记忆的日志目录若已被删除或不可访问（例如被临时测试/自动化误写），
		// 退回默认目录，避免界面突然显示成“一条日志都没有”。
		// 故意不覆盖记忆值：外接磁盘等临时不可用时，稍后可自动恢复。
		if trimmed != "" {
			if info, err := os.Stat(trimmed); err != nil || !info.IsDir() {
				trimmed = ""
			}
		}
	}
	if trimmed == "" {
		trimmed = defaultRoot()
	}
	root, err := filepath.Abs(trimmed)
	if err != nil {
		return "", errors.New("log_root 无效")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", errors.New("log_root 创建失败")
	}
	if explicit {
		rememberRoot(root)
	}
	return root, nil
}

// defaultRoot 把全局日志放到用户可见的文档目录，便于备份、同步和笔记软件直接读取；
// 只有在拿不到用户目录时才退回扩展私有数据目录。
func defaultRoot() string {
	if configured := strings.TrimSpace(os.Getenv("HIMIND_WORK_LOG_ROOT")); configured != "" {
		return configured
	}
	home := strings.TrimSpace(os.Getenv("USERPROFILE"))
	if home == "" {
		if resolved, err := os.UserHomeDir(); err == nil {
			home = resolved
		}
	}
	if home != "" {
		return filepath.Join(home, "Documents", "HiMind", "工作日志")
	}
	base := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "HiMindAgent", "extension-data", extensionDataNamespace)
}

type pluginState struct {
	LogRoot string `json:"log_root"`
}

func statePath() string {
	if configured := strings.TrimSpace(os.Getenv("HIMIND_PLUGIN_DATA_ROOT")); configured != "" {
		return filepath.Join(configured, "state.json")
	}
	base := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "HiMindAgent", "extension-data", extensionDataNamespace, "state.json")
}

func readState() pluginState {
	body, err := os.ReadFile(statePath())
	if err != nil {
		return pluginState{}
	}
	var state pluginState
	if err := json.Unmarshal(body, &state); err != nil {
		return pluginState{}
	}
	return state
}

func rememberRoot(root string) {
	state := readState()
	if state.LogRoot == root {
		return
	}
	state.LogRoot = root
	path := statePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	if payload, err := json.MarshalIndent(state, "", "  "); err == nil {
		_ = writeFileAtomic(path, append(payload, '\n'))
	}
}

func dayPath(root, date string) string {
	year := date
	if len(date) >= 4 {
		year = date[:4]
	}
	return filepath.Join(root, "logs", year, date+".json")
}

func readDay(root, date string) (dayFile, error) {
	path := dayPath(root, date)
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return dayFile{Date: date, SchemaVersion: schemaVersion, Entries: []entry{}}, nil
	}
	if err != nil {
		return dayFile{}, errors.New("日志文件读取失败")
	}
	var day dayFile
	if err := json.Unmarshal(body, &day); err != nil {
		return dayFile{}, errors.New("日志文件不是合法 JSON：" + date)
	}
	day.Date = date
	migrateDayFile(&day)
	return day, nil
}

func writeDay(root string, day dayFile) error {
	day.Count = len(day.Entries)
	payload, err := json.MarshalIndent(day, "", "  ")
	if err != nil {
		return errors.New("日志序列化失败")
	}
	if err := writeFileAtomic(dayPath(root, day.Date), append(payload, '\n')); err != nil {
		return errors.New("日志写入失败")
	}
	return nil
}

func listDates(root string) ([]string, error) {
	logsDir := filepath.Join(root, "logs")
	dates := make([]string, 0)
	years, err := readDirNames(logsDir)
	if err != nil {
		return nil, err
	}
	for _, year := range years {
		names, err := readDirNames(filepath.Join(logsDir, year))
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			if !strings.HasSuffix(name, ".json") {
				continue
			}
			date := strings.TrimSuffix(name, ".json")
			if validDate(date) {
				dates = append(dates, date)
			}
		}
	}
	sort.Strings(dates)
	return dates, nil
}

func collectEntries(root, from, to string) ([]entry, error) {
	dates, err := listDates(root)
	if err != nil {
		return nil, err
	}
	collected := make([]entry, 0)
	for _, date := range dates {
		if date < from || date > to {
			continue
		}
		day, err := readDay(root, date)
		if err != nil {
			return nil, err
		}
		collected = append(collected, day.Entries...)
	}
	sortEntries(collected)
	return collected, nil
}

// ---------- 周期与日期 ----------

// resolveRange 归一 entry.list 的查询区间。
//
// 语义：显式 date 视为单日锚点，只在其缺失的一侧补位；from/to 任一缺失则该侧取
// 开区间，最终由 listDates 只遍历真实存在的日期文件。注意「不传区间」不能收敛到
// 今天——否则「只按项目查询历史推进」会漏掉全部历史记录。
func resolveRange(from, to, date string) (string, string, error) {
	start := strings.TrimSpace(from)
	end := strings.TrimSpace(to)
	anchor := strings.TrimSpace(date)
	if anchor != "" {
		if !validDate(anchor) {
			return "", "", errors.New("date 必须是 YYYY-MM-DD 格式")
		}
		if start == "" {
			start = anchor
		}
		if end == "" {
			end = anchor
		}
	}
	if start == "" {
		start = "0001-01-01"
	}
	if end == "" {
		end = "9999-12-31"
	}
	if !validDate(start) || !validDate(end) {
		return "", "", errors.New("from / to 必须是 YYYY-MM-DD 格式")
	}
	if start > end {
		start, end = end, start
	}
	return start, end, nil
}

func isKnownPeriod(period string) bool {
	switch period {
	case "daily", "weekly", "monthly", "quarterly", "yearly":
		return true
	default:
		return false
	}
}

// periodRange 把锚点日期展开成周期边界与稳定的报告键。
func periodRange(period, anchor string) (string, string, string, error) {
	parsed, err := time.Parse("2006-01-02", anchor)
	if err != nil {
		return "", "", "", errors.New("date 必须是 YYYY-MM-DD 格式")
	}
	switch period {
	case "daily":
		return anchor, anchor, anchor, nil
	case "weekly":
		weekday := int(parsed.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		monday := parsed.AddDate(0, 0, -(weekday - 1))
		sunday := monday.AddDate(0, 0, 6)
		year, week := monday.ISOWeek()
		return monday.Format("2006-01-02"), sunday.Format("2006-01-02"), fmt.Sprintf("%04d-W%02d", year, week), nil
	case "monthly":
		start := time.Date(parsed.Year(), parsed.Month(), 1, 0, 0, 0, 0, time.UTC)
		end := start.AddDate(0, 1, -1)
		return start.Format("2006-01-02"), end.Format("2006-01-02"), fmt.Sprintf("%04d-%02d", parsed.Year(), int(parsed.Month())), nil
	case "quarterly":
		quarter := (int(parsed.Month())-1)/3 + 1
		startMonth := time.Month((quarter-1)*3 + 1)
		start := time.Date(parsed.Year(), startMonth, 1, 0, 0, 0, 0, time.UTC)
		end := start.AddDate(0, 3, -1)
		return start.Format("2006-01-02"), end.Format("2006-01-02"), fmt.Sprintf("%04d-Q%d", parsed.Year(), quarter), nil
	case "yearly":
		start := time.Date(parsed.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
		end := time.Date(parsed.Year(), 12, 31, 0, 0, 0, 0, time.UTC)
		return start.Format("2006-01-02"), end.Format("2006-01-02"), fmt.Sprintf("%04d", parsed.Year()), nil
	default:
		return "", "", "", errors.New("period 只能是 daily / weekly / monthly / quarterly / yearly")
	}
}

func normalizeDate(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed != "" {
		return trimmed
	}
	return time.Now().Format("2006-01-02")
}

func validDate(value string) bool {
	if len(value) != len("2006-01-02") {
		return false
	}
	_, err := time.Parse("2006-01-02", value)
	return err == nil
}

func periodLabel(period string) string {
	switch period {
	case "daily":
		return "日报"
	case "weekly":
		return "周报"
	case "monthly":
		return "月报"
	case "quarterly":
		return "季度总结"
	case "yearly":
		return "年度总结"
	default:
		return "报告"
	}
}

// ---------- 渲染 ----------

func renderMarkdown(title, period, key, from, to string, entries []entry, narrative string, narratives map[string]string) string {
	var builder strings.Builder
	builder.WriteString("# " + title + "\n\n")
	builder.WriteString(fmt.Sprintf("周期：%s · 覆盖 %s ~ %s · 共 %d 条记录\n\n", key, from, to, len(entries)))
	if narrative != "" {
		builder.WriteString(narrative + "\n\n")
	}
	builder.WriteString("## 概览\n\n")
	builder.WriteString(overviewLines(entries))
	builder.WriteString("\n")
	if period == "daily" {
		builder.WriteString("## 时间线\n\n")
		builder.WriteString(timelineLines(entries))
		builder.WriteString("\n")
	} else {
		builder.WriteString("## 按日期\n\n")
		builder.WriteString(byDateLines(entries))
		builder.WriteString("\n")
	}
	builder.WriteString("## 按项目 · 按功能\n\n")
	builder.WriteString(byProjectFeatureLines(entries, narratives))
	builder.WriteString("\n")
	if lines := collectLines(entries, func(item entry) []string { return item.Next }); len(lines) > 0 {
		builder.WriteString("## 后续事项\n\n")
		for _, line := range lines {
			builder.WriteString("- " + line + "\n")
		}
		builder.WriteString("\n")
	}
	if lines := collectLines(entries, func(item entry) []string { return item.Blockers }); len(lines) > 0 {
		builder.WriteString("## 阻塞与风险\n\n")
		for _, line := range lines {
			builder.WriteString("- " + line + "\n")
		}
		builder.WriteString("\n")
	}
	return builder.String()
}

// ---- 工时 ----

// entryMinutes 取条目工时（分钟），无有效值返回 0。
func entryMinutes(item entry) int {
	if item.Minutes > 0 {
		return item.Minutes
	}
	return 0
}

func groupMinutes(items []entry) int {
	total := 0
	for _, item := range items {
		total += entryMinutes(item)
	}
	return total
}

// splitMinutes 把工时拆成"实测/区间"与"估算"两类，便于报告透明标注。
func splitMinutes(items []entry) (measured, estimated int) {
	for _, item := range items {
		if item.Minutes <= 0 {
			continue
		}
		if item.MinutesSource == "estimated" {
			estimated += item.Minutes
		} else {
			measured += item.Minutes
		}
	}
	return measured, estimated
}

// minutesText 把分钟数渲染成人读文本。
func minutesText(m int) string {
	if m <= 0 {
		return "—"
	}
	if m < 60 {
		return fmt.Sprintf("%d 分钟", m)
	}
	hours := m / 60
	rest := m % 60
	if rest == 0 {
		return fmt.Sprintf("%d 小时", hours)
	}
	return fmt.Sprintf("%d 小时 %d 分", hours, rest)
}

func hoursText(m int) string {
	return fmt.Sprintf("%.1f 小时", float64(m)/60.0)
}

// minutesSourceLabel 把来源标识翻译成人读标签。
func minutesSourceLabel(source string) string {
	switch source {
	case "measured":
		return "实测"
	case "span":
		return "区间"
	case "estimated":
		return "估算"
	case "manual":
		return "人工"
	default:
		return "工时"
	}
}

func overviewLines(entries []entry) string {
	projects := map[string]int{}
	categories := map[string]int{}
	sources := map[string]int{}
	for _, item := range entries {
		projects[fallbackValue(item.Project, "未归类")]++
		categories[categoryLabel(item.Category)]++
		sources[fallbackValue(item.Source, "manual")]++
	}
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("- 记录条目：%d 条\n", len(entries)))
	builder.WriteString("- 涉及项目：" + countText(projects) + "\n")
	builder.WriteString("- 工作类型：" + countText(categories) + "\n")
	builder.WriteString("- 素材来源：" + countText(sources) + "\n")
	measured, estimated := splitMinutes(entries)
	total := measured + estimated
	if total > 0 {
		builder.WriteString(fmt.Sprintf("- 记录工时：约 %s（实测 %s / 估算 %s）\n", hoursText(total), hoursText(measured), hoursText(estimated)))
	}
	return builder.String()
}

func timelineLines(entries []entry) string {
	if len(entries) == 0 {
		return "本期没有记录。\n"
	}
	var builder strings.Builder
	for _, item := range entries {
		meta := make([]string, 0, 4)
		if span := timeSpan(item); span != "" {
			meta = append(meta, span)
		}
		if item.Project != "" {
			meta = append(meta, item.Project)
		}
		if item.Feature != "" {
			meta = append(meta, "功能:"+item.Feature)
		}
		if item.Exhibit != "" {
			meta = append(meta, "展项:"+item.Exhibit)
		}
		meta = append(meta, "["+categoryLabel(item.Category)+"]")
		builder.WriteString("- " + strings.Join(meta, " · ") + " " + fallbackValue(item.Title, "(未命名)") + "\n")
		if item.Summary != "" {
			builder.WriteString("  " + item.Summary + "\n")
		}
		if item.Minutes > 0 {
			builder.WriteString("  - 工时：" + minutesText(item.Minutes) + "（" + minutesSourceLabel(item.MinutesSource) + "）\n")
		}
		appendBullet(&builder, "交付", item.Outcomes)
		appendBullet(&builder, "决策", item.Decisions)
		appendBullet(&builder, "阻塞", item.Blockers)
		appendBullet(&builder, "后续", item.Next)
	}
	return builder.String()
}

func byDateLines(entries []entry) string {
	byDate := map[string][]entry{}
	for _, item := range entries {
		byDate[item.Date] = append(byDate[item.Date], item)
	}
	dates := make([]string, 0, len(byDate))
	for date := range byDate {
		dates = append(dates, date)
	}
	sort.Strings(dates)
	if len(dates) == 0 {
		return "本期没有记录。\n"
	}
	var builder strings.Builder
	for _, date := range dates {
		builder.WriteString("### " + date + fmt.Sprintf("（%d 条）\n\n", len(byDate[date])))
		for _, item := range byDate[date] {
			builder.WriteString("- " + fallbackValue(item.Title, "(未命名)"))
			if item.Project != "" {
				builder.WriteString(" · " + item.Project)
			}
			builder.WriteString("\n")
		}
		builder.WriteString("\n")
	}
	return builder.String()
}

func byProjectFeatureLines(entries []entry, narratives map[string]string) string {
	if len(entries) == 0 {
		return "本期没有记录。\n"
	}
	byProject := map[string][]entry{}
	for _, item := range entries {
		key := fallbackValue(item.Project, "未归类")
		byProject[key] = append(byProject[key], item)
	}
	names := make([]string, 0, len(byProject))
	for name := range byProject {
		names = append(names, name)
	}
	sort.SliceStable(names, func(left, right int) bool {
		if len(byProject[names[left]]) != len(byProject[names[right]]) {
			return len(byProject[names[left]]) > len(byProject[names[right]])
		}
		return names[left] < names[right]
	})
	var builder strings.Builder
	for _, name := range names {
		items := byProject[name]
		builder.WriteString(fmt.Sprintf("### %s（%d 条 · 约 %s）\n\n", name, len(items), hoursText(groupMinutes(items))))
		if note := lookupNarrative(narratives, name); note != "" {
			builder.WriteString(note + "\n\n")
		}
		builder.WriteString(featureBlocks(name, items, narratives))
	}
	return builder.String()
}

// featureBlocks 把同一项目下的条目按功能分组，逐组给出确定性的内容小结。
func featureBlocks(project string, items []entry, narratives map[string]string) string {
	byFeature := map[string][]entry{}
	for _, item := range items {
		key := fallbackValue(item.Feature, "未分类功能")
		byFeature[key] = append(byFeature[key], item)
	}
	features := make([]string, 0, len(byFeature))
	for name := range byFeature {
		features = append(features, name)
	}
	sort.SliceStable(features, func(left, right int) bool {
		if len(byFeature[features[left]]) != len(byFeature[features[right]]) {
			return len(byFeature[features[left]]) > len(byFeature[features[right]])
		}
		return features[left] < features[right]
	})
	var builder strings.Builder
	for _, feature := range features {
		group := byFeature[feature]
		builder.WriteString(fmt.Sprintf("#### 功能：%s（%d 条 · 约 %s）\n\n", feature, len(group), hoursText(groupMinutes(group))))
		if note := lookupNarrative(narratives, project+"/"+feature); note != "" {
			builder.WriteString(note + "\n\n")
		}
		for _, item := range group {
			line := "- " + fallbackValue(item.Title, "(未命名)")
			if item.Exhibit != "" {
				line += "〔展项：" + item.Exhibit + "〕"
			}
			if item.Summary != "" {
				line += "：" + item.Summary
			}
			if item.Minutes > 0 {
				line += fmt.Sprintf("（%s·%s）", minutesText(item.Minutes), minutesSourceLabel(item.MinutesSource))
			}
			builder.WriteString(line + "\n")
		}
		writeAggregate(&builder, "完成", collectLines(group, func(item entry) []string { return item.Outcomes }))
		writeAggregate(&builder, "决策", collectLines(group, func(item entry) []string { return item.Decisions }))
		writeAggregate(&builder, "经验", collectLines(group, func(item entry) []string { return item.Learnings }))
		writeAggregate(&builder, "后续", collectLines(group, func(item entry) []string { return item.Next }))
		builder.WriteString("\n")
	}
	return builder.String()
}

func writeAggregate(builder *strings.Builder, label string, lines []string) {
	if len(lines) == 0 {
		return
	}
	builder.WriteString("  - " + label + "：" + strings.Join(lines, " / ") + "\n")
}

func lookupNarrative(narratives map[string]string, key string) string {
	if len(narratives) == 0 {
		return ""
	}
	for _, candidate := range []string{key, strings.TrimSpace(key)} {
		if text := strings.TrimSpace(narratives[candidate]); text != "" {
			return text
		}
	}
	return ""
}

func renderHTML(title, period, key, from, to string, entries []entry, narrative string, narratives map[string]string) string {
	sections := make([]string, 0, 6)
	narrativeHTML := ""
	if narrative != "" {
		narrativeHTML = `<p class="narrative">` + html.EscapeString(narrative) + `</p>`
	}
	sections = append(sections, `<section><h2>概览</h2><ul class="plain">`+mdListToHTML(overviewLines(entries))+`</ul></section>`)
	if period == "daily" {
		sections = append(sections, `<section><h2>时间线</h2>`+timelineHTML(entries)+`</section>`)
	} else {
		sections = append(sections, `<section><h2>按日期</h2>`+mdListToHTML(byDateLines(entries))+`</section>`)
	}
	sections = append(sections, `<section><h2>按项目 · 按功能</h2>`+mdListToHTML(byProjectFeatureLines(entries, narratives))+`</section>`)
	if lines := collectLines(entries, func(item entry) []string { return item.Next }); len(lines) > 0 {
		sections = append(sections, `<section><h2>后续事项</h2><ul>`+stringListHTML(lines)+`</ul></section>`)
	}
	if lines := collectLines(entries, func(item entry) []string { return item.Blockers }); len(lines) > 0 {
		sections = append(sections, `<section><h2>阻塞与风险</h2><ul class="warn">`+stringListHTML(lines)+`</ul></section>`)
	}
	return `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><title>` + html.EscapeString(title) + `</title>
<style>
body{margin:0;padding:32px;background:#f7f8fa;color:#111827;font:14px/1.7 -apple-system,"Segoe UI","Microsoft YaHei",sans-serif}
main{max-width:820px;margin:0 auto;background:#fff;border:1px solid #e5e7eb;border-radius:10px;padding:28px}
h1{margin:0 0 4px;font-size:22px}
h2{margin:26px 0 8px;font-size:15px;color:#374151;border-bottom:1px solid #eef2f7;padding-bottom:6px}
.sub{color:#6b7280;font-size:12px;margin:0}
.narrative{background:#eff6ff;border-left:3px solid #2563eb;padding:10px 12px;border-radius:0 6px 6px 0;margin:16px 0 0}
ul{margin:6px 0;padding-left:20px}
ul.plain{list-style:none;padding-left:0}
ul.warn li{color:#b45309}
li{margin:2px 0}
.item{padding:10px 0;border-bottom:1px solid #f1f3f5}
.item .head{display:flex;gap:8px;flex-wrap:wrap;align-items:baseline}
.time{color:#94a3b8;font-variant-numeric:tabular-nums}
.cat{padding:1px 6px;border-radius:4px;font-size:11px;background:#eef2ff;color:#4338ca}
.proj{color:#0f766e;font-size:12px}
.feat{color:#7c3aed;font-size:12px}
.mins{color:#0369a1;font-size:12px;font-variant-numeric:tabular-nums}
h3{margin:18px 0 6px;font-size:14px}
h4{margin:14px 0 4px;font-size:13px;color:#4b5563}
.summary{margin:4px 0 0;color:#374151}
footer{margin-top:18px;color:#94a3b8;font-size:11px}
</style></head><body><main>
<h1>` + html.EscapeString(title) + `</h1>
<p class="sub">周期 ` + html.EscapeString(key) + ` · 覆盖 ` + html.EscapeString(from) + ` ~ ` + html.EscapeString(to) + ` · 共 ` + fmt.Sprintf("%d", len(entries)) + ` 条记录</p>
` + narrativeHTML + strings.Join(sections, "\n") + `
<footer>由工作日志工具按确定性规则聚合；主线叙述（如有）来自所选模型服务。</footer>
</main></body></html>`
}

func timelineHTML(entries []entry) string {
	if len(entries) == 0 {
		return `<p class="sub">本期没有记录。</p>`
	}
	var builder strings.Builder
	for _, item := range entries {
		builder.WriteString(`<div class="item"><div class="head">`)
		if span := timeSpan(item); span != "" {
			builder.WriteString(`<span class="time">` + html.EscapeString(span) + `</span>`)
		}
		builder.WriteString(`<strong>` + html.EscapeString(fallbackValue(item.Title, "(未命名)")) + `</strong>`)
		builder.WriteString(`<span class="cat">` + html.EscapeString(categoryLabel(item.Category)) + `</span>`)
		if item.Project != "" {
			builder.WriteString(`<span class="proj">` + html.EscapeString(item.Project) + `</span>`)
		}
		if item.Feature != "" {
			builder.WriteString(`<span class="feat">` + html.EscapeString(item.Feature) + `</span>`)
		}
		if item.Minutes > 0 {
			builder.WriteString(`<span class="mins">` + html.EscapeString(minutesText(item.Minutes)) + ` · ` + html.EscapeString(minutesSourceLabel(item.MinutesSource)) + `</span>`)
		}
		builder.WriteString(`</div>`)
		if item.Summary != "" {
			builder.WriteString(`<p class="summary">` + html.EscapeString(item.Summary) + `</p>`)
		}
		builder.WriteString(`</div>`)
	}
	return builder.String()
}

func mdListToHTML(markdown string) string {
	var builder strings.Builder
	for _, line := range strings.Split(markdown, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#### ") {
			builder.WriteString(`<h4>` + html.EscapeString(strings.TrimPrefix(trimmed, "#### ")) + `</h4>`)
			continue
		}
		if strings.HasPrefix(trimmed, "### ") {
			builder.WriteString(`<h3>` + html.EscapeString(strings.TrimPrefix(trimmed, "### ")) + `</h3>`)
			continue
		}
		if strings.HasPrefix(trimmed, "- ") {
			builder.WriteString(`<li>` + html.EscapeString(strings.TrimPrefix(trimmed, "- ")) + `</li>`)
			continue
		}
		builder.WriteString(`<li>` + html.EscapeString(trimmed) + `</li>`)
	}
	return builder.String()
}

func stringListHTML(lines []string) string {
	var builder strings.Builder
	for _, line := range lines {
		builder.WriteString(`<li>` + html.EscapeString(line) + `</li>`)
	}
	return builder.String()
}

func appendBullet(builder *strings.Builder, label string, values []string) {
	for _, value := range values {
		builder.WriteString("  - " + label + "：" + value + "\n")
	}
}

func collectLines(entries []entry, pick func(entry) []string) []string {
	seen := map[string]struct{}{}
	lines := make([]string, 0)
	for _, item := range entries {
		for _, line := range pick(item) {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			if _, ok := seen[trimmed]; ok {
				continue
			}
			seen[trimmed] = struct{}{}
			lines = append(lines, trimmed)
		}
	}
	return lines
}

func timeSpan(item entry) string {
	start := strings.TrimSpace(item.StartedAt)
	end := strings.TrimSpace(item.EndedAt)
	if start == "" && end == "" {
		return ""
	}
	if start == "" {
		return shortTime(end)
	}
	if end == "" {
		return shortTime(start)
	}
	startTime, startErr := time.Parse(time.RFC3339, start)
	endTime, endErr := time.Parse(time.RFC3339, end)
	if startErr == nil && endErr == nil {
		startLocal := startTime.Local()
		endLocal := endTime.Local()
		if startLocal.Format("2006-01-02") == endLocal.Format("2006-01-02") {
			return startLocal.Format("15:04") + "–" + endLocal.Format("15:04")
		}
		return startLocal.Format("01-02 15:04") + "–" + endLocal.Format("01-02 15:04")
	}
	if start == end {
		return shortTime(start)
	}
	return shortTime(start) + "–" + shortTime(end)
}

// shortTime 把 RFC3339 时间戳渲染成 HH:MM；无法解析时原样返回。
func shortTime(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.Local().Format("15:04")
	}
	return value
}

func countText(counts map[string]int) string {
	if len(counts) == 0 {
		return "无"
	}
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.SliceStable(names, func(left, right int) bool {
		if counts[names[left]] != counts[names[right]] {
			return counts[names[left]] > counts[names[right]]
		}
		return names[left] < names[right]
	})
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s（%d）", name, counts[name]))
	}
	return strings.Join(parts, "、")
}

// ---------- 打开链接 ----------

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
	command := exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	if err := command.Start(); err != nil {
		return nil, errors.New("打开链接失败：" + err.Error())
	}
	_ = command.Process.Release()
	return map[string]any{"opened": true, "url": target}, nil
}

// revealPath 在系统文件管理器里定位日志目录（或指定目录/文件）。
//
// 未显式传 path 时用日志根目录；目录不存在则先创建，避免「打开一个不存在的路径」
// 让资源管理器弹错误框。风险等级是 local_action（只打开窗口，不改内容）。
func revealPath(in input) (any, error) {
	if runtime.GOOS != "windows" {
		return nil, errors.New("当前平台暂不支持打开文件管理器：" + runtime.GOOS)
	}
	target := strings.TrimSpace(in.Path)
	if target == "" {
		root, err := logRoot(in.LogRoot)
		if err != nil {
			return nil, err
		}
		target = root
	}
	info, err := os.Stat(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if mkErr := os.MkdirAll(target, 0o755); mkErr != nil {
				return nil, errors.New("目标路径不存在且无法创建")
			}
			info = nil
		} else {
			return nil, errors.New("读取目标路径失败：" + err.Error())
		}
	}
	command := exec.Command("explorer.exe", target)
	if info != nil && !info.IsDir() {
		command = exec.Command("explorer.exe", "/select,"+target)
	}
	if err := command.Start(); err != nil {
		return nil, errors.New("打开文件管理器失败：" + err.Error())
	}
	_ = command.Process.Release()
	return map[string]any{"opened": true, "path": target}, nil
}

// ---------- 工具函数 ----------

// 日志条目的受控类型（category）。这里是唯一的规范值集合：UI 选择、AI 结果 schema、
// 报告显示与旧数据归一都以它为准，写入 schema 也用同一枚举。
const defaultCategory = "其他"

// legacyCategoryLabels 把历史遗留的英文类型键映射到规范中文值，保证旧条目在报告与
// 视图里也显示为中文。新写入只能使用规范值（见 plugin.json 的 category 枚举）。
var legacyCategoryLabels = map[string]string{
	"development": "开发",
	"feature":     "开发",
	"fix":         "修复",
	"bugfix":      "修复",
	"design":      "设计",
	"research":    "调研",
	"docs":        "文档",
	"document":    "文档",
	"review":      "评审",
	"meeting":     "会议",
	"ops":         "运维",
	"operation":   "运维",
	"other":       "其他",
	"misc":        "其他",
}

// categoryLabel 返回条目的展示用类型：规范值原样返回，已知旧英文键归一为中文，
// 未知值回退为原值（空值回退为「其他」）。
func categoryLabel(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return defaultCategory
	}
	if label, ok := legacyCategoryLabels[strings.ToLower(trimmed)]; ok {
		return label
	}
	return trimmed
}

func fallbackValue(value, when string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return when
	}
	return trimmed
}

func trimList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// appendUniqueStrings 依次追加非空且未出现过的字符串，保持首次出现顺序。
func appendUniqueStrings(base []string, values ...string) []string {
	seen := make(map[string]bool, len(base)+len(values))
	out := make([]string, 0, len(base)+len(values))
	for _, value := range append(append([]string{}, base...), values...) {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		out = append(out, trimmed)
	}
	return out
}

func nonNilRefs(values []ref) []ref {
	if values == nil {
		return []ref{}
	}
	return values
}

func nonNilEntries(values []entry) []entry {
	if values == nil {
		return []entry{}
	}
	return values
}

func sortedKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortEntries(entries []entry) {
	sort.SliceStable(entries, func(left, right int) bool {
		if entries[left].StartedAt != entries[right].StartedAt {
			return entries[left].StartedAt < entries[right].StartedAt
		}
		return entries[left].Title < entries[right].Title
	})
}

func readDirNames(dir string) ([]string, error) {
	items, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("目录读取失败：" + filepath.Base(dir))
	}
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.Name())
	}
	return names, nil
}

func sha256OfFile(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

func fileURIPath(path string) string {
	return "file:///" + strings.ReplaceAll(path, "\\", "/")
}
