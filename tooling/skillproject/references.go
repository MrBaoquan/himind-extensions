package skillproject

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/MrBaoquan/himind-extensions/tooling/metaguide"
)

// 技能的能力引用与形态检查。
//
// 为什么需要：技能清单里的 capabilities 是**依赖声明** —— 缺失依赖要报 blocked，
// 而正文里凭空引用一个没声明的能力，两边都不会报错。实测踩过两次：
// 我自己的技能引用了 iostudio.motion.validate 却没声明；work-log 的正文引用了
// entry.read / project.assign / source.save 三条同样没声明。两者都一路绿到运行时。
//
// 扫描范围是**全部内容文件**（SKILL.md、references/*、agents/*），不只 SKILL.md ——
// 判据常常写在参考文件里（我那次漏声明在两个文件里都有）。

// 不参与扫描：清单本身与校验和清单。
var skipReferenceScan = map[string]bool{"skill.json": true, "checksums.sha256": true}

// validateCapabilityReferences 检查正文引用的能力是否都已声明。
func validateCapabilityReferences(manifest Manifest, files map[string][]byte) error {
	declared := make([]string, 0, len(manifest.Capabilities))
	for _, capability := range manifest.Capabilities {
		declared = append(declared, capability.ID)
	}
	slugs := make([]string, 0, len(manifest.PluginDependencies))
	for _, dependency := range manifest.PluginDependencies {
		slugs = append(slugs, metaguide.SlugOfPluginID(dependency.PluginID))
	}

	missing := map[string]bool{}
	for name, content := range files {
		if skipReferenceScan[name] {
			continue
		}
		for _, token := range metaguide.UndeclaredCapabilityReferences(string(content), declared, slugs) {
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
		"skill body references capabilities that are not declared in skill.json: %s; "+
			"add them to \"capabilities\" (or remove the reference)",
		strings.Join(items, ", "))
}

// capabilityIDShape 是能力 ID 的形态约束（ADR 0119：小写、`.` 分层、词内用 `-`、2–5 段）。
var capabilityIDShape = regexp.MustCompile(`^[a-z][a-z0-9-]*(?:\.[a-z][a-z0-9-]*){1,4}$`)

// validateCapabilityShape 检查声明的能力 ID 本身是否合规。
//
// 插件侧的能力 ID 由内核与插件校验器管（ADR 0119 那套），技能侧此前**完全没有对应检查** ——
// 写错形态的能力 ID 会一路通过，直到运行时对不上。
func validateCapabilityShape(manifest Manifest) error {
	for _, capability := range manifest.Capabilities {
		id := strings.TrimSpace(capability.ID)
		if id == "" {
			return fmt.Errorf("capability id is required")
		}
		if !capabilityIDShape.MatchString(id) {
			return fmt.Errorf(
				"invalid capability id %q: use lowercase dotted segments (2-5), hyphens inside a word, for example media.toolkit.probe",
				id)
		}
		if strings.TrimSpace(capability.Provider) == "" {
			// provider 决定运行时向谁要这个能力；缺失会让依赖解析没有落点。
			return fmt.Errorf("capability %s must declare a provider plugin id", id)
		}
	}
	return nil
}
