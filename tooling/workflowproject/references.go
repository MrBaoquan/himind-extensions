package workflowproject

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/MrBaoquan/himind-extensions/tooling/metaguide"
)

// 工作流的能力引用与形态检查。
//
// 与技能侧同一类缺口：workflow.json 的 capabilities 是**依赖声明**，而 README 与
// 视图描述里还会直接引用能力 ID —— 「引用了但没声明」两边都不报错。
// 共用 metaguide 的扫描判据，避免两处实现漂移。

var workflowCapabilityShape = regexp.MustCompile(`^[a-z][a-z0-9-]*(?:\.[a-z][a-z0-9-]*){1,4}$`)

// validateCapabilityShape 检查声明的能力 ID 形态。
func validateCapabilityShape(manifest Manifest) error {
	for _, id := range manifest.Capabilities {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			return fmt.Errorf("workflow capability id must be non-empty")
		}
		if !workflowCapabilityShape.MatchString(trimmed) {
			return fmt.Errorf(
				"invalid capability id %q: use lowercase dotted segments (2-5), hyphens inside a word",
				trimmed)
		}
	}
	return nil
}

// validateCapabilityReferences 检查文档里引用的能力是否都已声明。
//
// 依赖锚点取两类：声明的 capabilities，以及 dependencies.plugins 的 slug。
// 后者同样界定了命名空间 —— 工作流常在正文里提到它会调用的插件能力。
func validateCapabilityReferences(manifest Manifest, documents map[string][]byte) error {
	slugs := make([]string, 0, len(manifest.Dependencies.Plugins))
	for _, dependency := range manifest.Dependencies.Plugins {
		slugs = append(slugs, metaguide.SlugOfPluginID(dependency.ID))
	}
	if len(manifest.Capabilities) == 0 && len(slugs) == 0 {
		return nil
	}

	missing := map[string]bool{}
	for _, content := range documents {
		for _, token := range metaguide.UndeclaredCapabilityReferences(
			string(content), manifest.Capabilities, slugs) {
			missing[token] = true
		}
	}
	if len(missing) == 0 {
		return nil
	}
	items := make([]string, 0, len(missing))
	for token := range missing {
		items = append(items, token)
	}
	return fmt.Errorf(
		"workflow documents reference capabilities that are not declared: %s; "+
			"add them to \"capabilities\" (or remove the reference)",
		strings.Join(items, ", "))
}
