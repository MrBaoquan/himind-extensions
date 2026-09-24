// Package distribution 定义扩展制品的分发落点约束。
//
// 落点用可组合的列表表达，而不是单一枚举：后续增加「内网文件服务器」
// 「私有 registry」时不必改 schema，也不影响既有落点。约束由扩展作者
// 在清单里声明，仓库级默认值写在 extensions.json，发布脚本和 Agent 都
// 按同一份声明决定制品发到哪里。
package distribution

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Target 是一个分发落点。
type Target string

const (
	// Workbench 是组织工作台：制品进入 Dashboard 审核流程后对内分发。
	Workbench Target = "workbench"
	// Github 是 GitHub Release：制品与目录清单公开发布，供扩展源直接安装。
	Github Target = "github"
)

// All 返回全部落点，顺序即规范化顺序。
func All() []Target {
	return []Target{Workbench, Github}
}

// Default 返回未声明时的默认落点：仅工作台。
func Default() []Target {
	return []Target{Workbench}
}

// Parse 解析清单或仓库配置里的 distribution_targets。
//
// 空数组视为「哪也不发」，直接拒绝：它在发布时不可判定，必须在入口处拦下。
// 未知落点同样拒绝，避免拼写错误被当成新落点静默接受。
func Parse(raw []string) ([]Target, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("distribution_targets must declare at least one of %s", list(All()))
	}
	targets := make([]Target, 0, len(raw))
	for _, item := range raw {
		value := Target(strings.TrimSpace(item))
		if !known(value) {
			return nil, fmt.Errorf("unknown distribution target %q, allowed: %s", item, list(All()))
		}
		if contains(targets, value) {
			return nil, fmt.Errorf("duplicate distribution target %q", item)
		}
		targets = append(targets, value)
	}
	return sortTargets(targets), nil
}

// Allows 报告目标集合是否包含指定落点。
func Allows(targets []Target, target Target) bool {
	return contains(targets, target)
}

// Describe 用中文列出落点，供日志、报告和 UI 文案复用。
func Describe(targets []Target) string {
	if len(targets) == 0 {
		return "未声明"
	}
	labels := make([]string, 0, len(targets))
	for _, target := range sortTargets(targets) {
		labels = append(labels, label(target))
	}
	return strings.Join(labels, " + ")
}

// FromManifest 读取清单顶层的 distribution_targets。
// declared 为 false 表示清单没有声明该字段，调用方据此决定是继承默认值还是报错。
func FromManifest(data []byte) (targets []Target, declared bool, err error) {
	var raw struct {
		DistributionTargets *[]string `json:"distribution_targets"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, false, err
	}
	if raw.DistributionTargets == nil {
		return nil, false, nil
	}
	targets, err = Parse(*raw.DistributionTargets)
	if err != nil {
		return nil, true, err
	}
	return targets, true, nil
}

func known(target Target) bool {
	return contains(All(), target)
}

func contains(targets []Target, target Target) bool {
	for _, item := range targets {
		if item == target {
			return true
		}
	}
	return false
}

// sortTargets 按 All 的固定顺序排列，保证清单、报告和日志里的顺序稳定。
func sortTargets(targets []Target) []Target {
	sorted := make([]Target, 0, len(targets))
	for _, target := range All() {
		if contains(targets, target) {
			sorted = append(sorted, target)
		}
	}
	return sorted
}

func label(target Target) string {
	switch target {
	case Workbench:
		return "工作台"
	case Github:
		return "GitHub"
	default:
		return string(target)
	}
}

func list(targets []Target) string {
	values := make([]string, 0, len(targets))
	for _, target := range targets {
		values = append(values, string(target))
	}
	return strings.Join(values, ", ")
}
