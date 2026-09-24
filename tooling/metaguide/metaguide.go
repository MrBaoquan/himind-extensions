// Package metaguide 定义扩展元数据的长度约束。
//
// 长度按字符数计算：1 个汉字、字母、数字或符号均计 1。约束分两档，
// Recommended 是建议值，Max 是硬上限；脚手架和校验只在超过 Max 时拒绝。
// Recommended 为 0 表示该字段只有硬上限，用于 Capability ID 这类结构性标识。
package metaguide

import (
	"fmt"
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
	CapabilityID = Field{Key: "capability_id", Label: "Capability ID", Max: 40}
	// CapabilityDescription 是插件 Capability 说明。
	CapabilityDescription = Field{Key: "capability_description", Label: "Capability 说明", Recommended: 30, Max: 48}
	// CommandTitle 是插件贡献的命令或视图标题。
	CommandTitle = Field{Key: "command_title", Label: "命令与视图标题", Recommended: 12, Max: 16}
	// StepTitle 是工作流步骤标题。
	StepTitle = Field{Key: "step_title", Label: "步骤标题", Recommended: 12, Max: 16}
)

// Fields 返回全部字段约束，按稳定 ID、目录名、名称、说明、更新说明的顺序排列。
func Fields() []Field {
	return []Field{StableID, Slug, DisplayName, Description, TriggerDescription, ReleaseNotes, CapabilityID, CapabilityDescription, CommandTitle, StepTitle}
}

// Rules 是一句话文案规则，跟数值约束一起返回给创作方。
const Rules = "名称用名词短语只说是什么；说明写“做什么 + 什么时候用”一句，不复述需求、不列功能清单；更新说明只写本次变化；不用“一站式、全方位、赋能、助力”这类空词。"

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
