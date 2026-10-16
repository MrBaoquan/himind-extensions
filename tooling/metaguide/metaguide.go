// Package metaguide 定义扩展元数据的长度约束。
//
// 长度按字符数计算：1 个汉字、字母、数字或符号均计 1。约束分两档，
// Recommended 是建议值，Max 是硬上限；脚手架和校验只在超过 Max 时拒绝。
// Recommended 为 0 表示该字段只有硬上限，用于 Capability ID 这类结构性标识。
package metaguide

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Field 描述一个元数据字段的长度约束。
type Field struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Recommended int    `json:"recommended"`
	Max         int    `json:"max"`
}

// 扩展元数据字段约束。
var (
	// StableID 是完整稳定 ID，例如 com.himind.skill.example。
	StableID = Field{Key: "id", Label: "稳定 ID", Recommended: 48, Max: 64}
	// Slug 是目录名或稳定 ID 的最后一段，会出现在安装路径里。
	Slug = Field{Key: "slug", Label: "目录名或 ID 末段", Recommended: 28, Max: 32}
	// DisplayName 是用户可见的中文名称，出现在列表卡片和标题上。
	DisplayName = Field{Key: "name", Label: "显示名称", Recommended: 14, Max: 18}
	// Description 是清单里的用途说明，出现在市场卡片上。
	Description = Field{Key: "description", Label: "用途说明", Recommended: 60, Max: 120}
	// TriggerDescription 是 SKILL.md frontmatter 的 description，需要同时承载用途与触发场景。
	TriggerDescription = Field{Key: "trigger_description", Label: "SKILL.md 触发说明", Recommended: 140, Max: 160}
	// ReleaseNotes 是版本更新说明，出现在更新日志里。
	ReleaseNotes = Field{Key: "release_notes", Label: "版本更新说明", Recommended: 60, Max: 120}
	// CapabilityID 是插件 Capability ID；它是结构性标识，只设硬上限。
	CapabilityID = Field{Key: "capability_id", Label: "Capability ID", Max: 48}
	// CapabilityDescription 是插件 Capability 说明。
	CapabilityDescription = Field{Key: "capability_description", Label: "Capability 说明", Recommended: 30, Max: 48}
	// CommandTitle 是插件贡献的命令或视图标题。
	CommandTitle = Field{Key: "command_title", Label: "命令与视图标题", Recommended: 12, Max: 16}
	// StepTitle 是工作流步骤标题。
	StepTitle = Field{Key: "step_title", Label: "步骤标题", Recommended: 12, Max: 16}
)

var capabilitySegmentPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// Fields 返回全部字段约束，按稳定 ID、目录名、名称、说明、更新说明的顺序排列。
func Fields() []Field {
	return []Field{StableID, Slug, DisplayName, Description, TriggerDescription, ReleaseNotes, CapabilityID, CapabilityDescription, CommandTitle, StepTitle}
}

// CheckCapabilityIDShape 校验 ADR 0119 中与能力 ID 自身有关的结构规则。
// 首段域和 capability_namespace 属于清单上下文校验，由 Agent 候选测试负责。
func CheckCapabilityIDShape(id string) error {
	if Length(id) > CapabilityID.Max {
		return fmt.Errorf("Capability ID is too long: %d chars, limit %d", Length(id), CapabilityID.Max)
	}
	segments := strings.Split(id, ".")
	if len(segments) < 2 || len(segments) > 5 {
		return fmt.Errorf("Capability ID must contain 2–5 dot-separated segments")
	}
	for _, segment := range segments {
		if !capabilitySegmentPattern.MatchString(segment) {
			return fmt.Errorf("Capability ID segment %q must match ^[a-z][a-z0-9]*(-[a-z0-9]+)*$", segment)
		}
	}
	last := segments[len(segments)-1]
	if len(last) > 1 && last[0] == 'v' {
		allDigits := true
		for _, char := range last[1:] {
			if char < '0' || char > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			return fmt.Errorf("Capability ID must not contain a version suffix; use a version field")
		}
	}
	for index := 1; index < len(segments); index++ {
		parentTokens := strings.Split(segments[index-1], "-")
		childTokens := strings.Split(segments[index], "-")
		for _, child := range childTokens {
			for _, parent := range parentTokens {
				if child == parent {
					return fmt.Errorf("Capability ID child segment %q repeats parent segment %q", segments[index], segments[index-1])
				}
			}
		}
	}
	return nil
}

// CapabilityIDRules 返回插件创作方必须在写入前看到的 ADR 0119 命名规则。
// 长度上限从 CapabilityID 读取，避免规则文本与字段约束再次分叉。
func CapabilityIDRules() []string {
	domains := strings.Join(CapabilityIDDomains(), ", ")
	return []string{
		fmt.Sprintf("Capability ID 使用 2–5 个以 `.` 分隔的段，总长不超过 %d 个字符。", CapabilityID.Max),
		"每段必须匹配 `^[a-z][a-z0-9]*(-[a-z0-9]+)*$`；词内复合使用 `-`。禁止下划线、驼峰和把版本号写入 ID。",
		fmt.Sprintf("首段必须是 ADR 0119 领域注册表中的域（%s）或插件命名空间。", domains),
		"插件使用多段命名空间时必须在 plugin.json 声明 capability_namespace，且每个能力 ID 都必须位于该命名空间下。",
		"相邻段不得重复；子段不得重复父段已经表达的语义。",
	}
}

// CapabilityIDDomains 返回 ADR 0119 注册的共享领域，使用副本避免调用方改写规则。
func CapabilityIDDomains() []string {
	return []string{
		"system", "ai", "media", "workbench", "business", "engineering", "extension",
		"skill", "plugin", "workflow", "mcp", "market", "schedule", "distribution",
		"svn", "storage", "device", "network", "vcs", "workspace",
	}
}

// Rules 是一句话文案规则，跟数值约束一起返回给创作方。
//
// 词表必须**在这里列全**：创作方实际收到的是 environment.preflight 返回的
// authoring_rules，若词表只写在规范正文里，创作方根本看不到它，也就无从遵守。
// 这里与 BannedWords 保持一致，改一处要同时改另一处。
const Rules = "名称用名词短语只说是什么（插件含「工具」，技能用「助手」或知识名，工作流用交付物名词）；" +
	"说明写“做什么 + 什么时候用”一句，不复述需求、不列功能清单、不写实现细节；更新说明只写本次变化；" +
	"不用「智能、自动、一键、全能、极致、管家、专家、大师」这类承诺词与岗位词，" +
	"也不用「一站式、全方位、赋能、助力、强大、领先、生态」这类无法验收的空词；" +
	"每个能力都要在 contributes.commands 里给一条命令标题（能力靠它被搜到，库/雷达这类存量例外可暂缺）。"

// BannedWords 是显示名与文案里的禁用词。
//
// 为什么以**数据**而不是散文形式存在：规范写了这两组词，但规范自己不重抄词表、
// 创作助手又只说“见规范”，于是创作方两边都收不到完整清单 —— 实测出现过
// 「媒体智能」这样必然违规的名字一路走到提审（名字只有 4 字，长度校验必过）。
// 把词表放在这里，创作方（authoring_rules）、脚手架与校验器就能读同一份。
var BannedWords = []string{
	// 规范 §二4：承诺词与岗位词
	"智能", "自动", "一键", "全能", "极致", "管家", "专家", "大师",
	// 规范 §三4：无法验收的空词
	"一站式", "全方位", "赋能", "助力", "强大", "领先", "生态",
}

// SharedDomains 是内核领域注册表里的共享域（规范 §二7）。
// 能力首段落在其中的插件，无需声明 capability_namespace。
var SharedDomains = []string{"media", "workbench", "business", "workspace", "extension"}

// TypeWords 是显示名应含的类型词，按形态区分（规范 §二2）。
// 插件是确定性能力的提供方，用「工具」；技能与工作流另有约定，不在本包强制。
var TypeWords = []string{"工具", "库", "雷达", "采集器", "助手"}

// BannedIn 返回 value 命中的禁用词；未命中返回 nil。
func BannedIn(value string) []string {
	var hits []string
	for _, word := range BannedWords {
		if strings.Contains(value, word) {
			hits = append(hits, word)
		}
	}
	return hits
}

// IsSharedDomain 报告 name 是否是内核领域注册表里的共享域。
func IsSharedDomain(name string) bool {
	for _, domain := range SharedDomains {
		if name == domain {
			return true
		}
	}
	return false
}

// CommonPrefix 返回一组能力 ID 的共同前缀（按 "." 分段）。
//
// 例如 media.toolkit.probe 与 media.toolkit.job.status → media.toolkit；
// extension.plugin.scaffold 与 extension.skill.scaffold → extension。
// 空输入返回空字符串。
func CommonPrefix(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	prefix := strings.Split(ids[0], ".")
	prefix = prefix[:len(prefix)-1] // 去掉动作段，只留前缀
	for _, id := range ids[1:] {
		parts := strings.Split(id, ".")
		parts = parts[:len(parts)-1]
		limit := len(prefix)
		if len(parts) < limit {
			limit = len(parts)
		}
		shared := 0
		for shared < limit && prefix[shared] == parts[shared] {
			shared++
		}
		prefix = prefix[:shared]
	}
	return strings.Join(prefix, ".")
}

// SuggestedNamespace 返回这组能力应有的 capability_namespace 声明。
// 返回空字符串表示**无需声明**（首段就是插件 slug，或首段是共享域）。
//
// 判据来自规范 §二8：前缀是多段时必须显式声明；未声明时首段只允许共享域或插件 slug。
func SuggestedNamespace(pluginID string, ids []string) string {
	common := CommonPrefix(ids)
	if common == "" {
		return ""
	}
	segments := strings.Split(common, ".")
	if len(segments) >= 2 {
		return common
	}
	if segments[0] == SlugOf(pluginID) || IsSharedDomain(segments[0]) {
		return ""
	}
	return common
}

// CheckNamespace 校验 capability_namespace 与能力 ID 是否自洽。
//
// 规则（规范 §二8）：
//   - 声明了就必须**真的是所有能力 ID 的前缀** —— 否则那些不匹配的能力会落到
//     别人的命名空间里，而内核按「先到者胜」注册，冲突时会被静默跳过。
//   - 未声明时，首段必须是插件 slug 或共享域；若能力都挂在多段前缀下
//     （如 media.toolkit.*），必须显式声明。
func CheckNamespace(pluginID, declared string, ids []string) error {
	declared = strings.TrimSpace(declared)
	if declared != "" {
		for _, id := range ids {
			if !strings.HasPrefix(id, declared+".") {
				return fmt.Errorf(
					"capability_namespace %q is not a prefix of capability %q; every capability must live under the declared namespace",
					declared, id)
			}
		}
		return nil
	}
	suggested := SuggestedNamespace(pluginID, ids)
	if suggested == "" {
		return nil
	}
	return fmt.Errorf(
		"capability_namespace is required: capabilities live under %q, which is not the plugin slug; declare \"capability_namespace\": %q",
		suggested, suggested)
}

// HasRecommended 报告该字段是否给出建议值。
func HasRecommended(field Field) bool {
	return field.Recommended > 0
}

// Length 返回去掉首尾空白后的字符数。
func Length(value string) int {
	return utf8.RuneCountInString(strings.TrimSpace(value))
}

// SlugOf 返回稳定 ID 的最后一段，用作目录名长度判断。
func SlugOf(id string) string {
	if index := strings.LastIndex(id, "."); index >= 0 {
		return id[index+1:]
	}
	return id
}

// Check 在字段超过硬上限时返回错误。
func Check(field Field, value string) error {
	length := Length(value)
	if length <= field.Max {
		return nil
	}
	if HasRecommended(field) {
		return fmt.Errorf("%s is too long: %d chars, limit %d (recommended %d)", field.Key, length, field.Max, field.Recommended)
	}
	return fmt.Errorf("%s is too long: %d chars, limit %d", field.Key, length, field.Max)
}

// ── 能力引用一致性 ─────────────────────────────────────
//
// 技能与工作流的清单都有一份 capabilities（依赖声明），正文里还会直接引用能力 ID。
// 「正文引用了但清单没声明」这类缺口两边都不会报错，实测踩过两次：
// 我自己的技能引用了 iostudio.motion.validate 却没声明；work-log 的正文引用了
// entry.read / project.assign / source.save 三条同样没声明。二者都一路绿到运行时。
//
// 判据刻意收得很紧，避免把普通词误判成能力。

// capabilityTokenPattern 匹配形如 a.b.c 的标识。
var capabilityTokenPattern = regexp.MustCompile(`\b[a-z][a-z0-9-]*(?:\.[a-z][a-z0-9-]*)+\b`)

// fileExtensions 用于识别「这看起来是文件名，不是能力」。
// 不追求穷尽 —— 漏掉的只会变成一次人工确认，不会漏报真缺口。
var fileExtensions = map[string]bool{
	"json": true, "md": true, "txt": true, "yaml": true, "yml": true,
	"go": true, "cs": true, "py": true, "js": true, "ts": true, "tsx": true,
	"xml": true, "ini": true, "cfg": true, "conf": true, "csv": true, "log": true,
	"exe": true, "dll": true, "so": true, "hpp": true, "cpp": true, "h": true,
	"sh": true, "bat": true, "ps1": true, "cmd": true, "zip": true,
	"hmpkg": true, "hmskill": true, "hmwf": true,
	"html": true, "css": true, "svg": true, "png": true,
}

// SlugOfPluginID 返回插件稳定 ID 的末段，用作命名空间。
func SlugOfPluginID(id string) string {
	if index := strings.LastIndex(id, "."); index >= 0 {
		return id[index+1:]
	}
	return id
}

// UndeclaredCapabilityReferences 在 content 里找出「看起来是能力、但没声明」的标识。
//
// declared 是清单里已声明的能力 ID；providerSlugs 是插件依赖的 slug。
// 二者共同界定命名空间 —— 只认首段落在这两个来源里的标识，所以 `git.commit.message`
// 这类普通词不会被误判。排除项：已声明的公共前缀（命名空间本身不是能力）、
// 以及末段是文件扩展名的（`distribution.sample.json`）。
func UndeclaredCapabilityReferences(content string, declared, providerSlugs []string) []string {
	namespaces := map[string]bool{}
	known := map[string]bool{}
	isDeclared := map[string]bool{}

	for _, id := range declared {
		isDeclared[id] = true
		segments := strings.Split(id, ".")
		if len(segments) > 0 {
			namespaces[segments[0]] = true
		}
		for i := 1; i < len(segments); i++ {
			known[strings.Join(segments[:i], ".")] = true
		}
	}
	for _, slug := range providerSlugs {
		namespaces[slug] = true
		known[slug] = true
	}
	if len(namespaces) == 0 {
		// 没有任何声明 → 无从判断哪些标识是能力，跳过而不是全量报错。
		return nil
	}

	found := map[string]bool{}
	for _, token := range capabilityTokenPattern.FindAllString(content, -1) {
		segments := strings.Split(token, ".")
		if fileExtensions[segments[len(segments)-1]] {
			continue
		}
		if !namespaces[segments[0]] || known[token] || isDeclared[token] {
			continue
		}
		found[token] = true
	}
	if len(found) == 0 {
		return nil
	}
	items := make([]string, 0, len(found))
	for token := range found {
		items = append(items, token)
	}
	sort.Strings(items)
	return items
}
