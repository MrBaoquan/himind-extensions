package catalog

import (
	"fmt"
	"sort"
	"strings"

	"github.com/MrBaoquan/himind-extensions/tooling/distribution"
)

// DeclaredDependency 是源码清单里声明的一条依赖：只说明「需要谁、至少什么版本」，
// 不含任何定位信息。发布时由 PinDependencies 解析成发布清单里的精确 pin。
type DeclaredDependency struct {
	Kind       string
	ID         string
	Required   bool
	MinVersion string
}

// DeclaredDependencies 读取源码清单的依赖声明。
//
// 插件与技能用 `plugin_dependencies`（带 required / min_version），工作流用
// `dependencies.plugins` 与 `dependencies.skills`。两种写法都归一成同一个结构，
// 后面的解析与门禁只有一份实现。
//
// 工作流的依赖列表允许两种写法：字符串表示本分发内的必需依赖；对象可以补
// `required` 与 `min_version`，用于跨分发依赖（依赖由别的分发发布，无法在这里
// 解析到确定制品）或需要声明版本下限的场景。
func DeclaredDependencies(kind string, value Manifest) ([]DeclaredDependency, error) {
	switch kind {
	case distribution.KindPlugin, distribution.KindSkill:
		declared := make([]DeclaredDependency, 0, len(value.PluginDependencies))
		for _, item := range value.PluginDependencies {
			id := dependencyText(item, "plugin_id")
			if id == "" {
				id = dependencyText(item, "id")
			}
			if id == "" {
				return nil, fmt.Errorf("plugin_dependencies 里有一条缺少 plugin_id")
			}
			required, err := dependencyRequired(item, id)
			if err != nil {
				return nil, err
			}
			declared = append(declared, DeclaredDependency{
				Kind: distribution.KindPlugin, ID: id,
				Required: required, MinVersion: dependencyText(item, "min_version"),
			})
		}
		return declared, nil
	case distribution.KindWorkflow:
		declared := make([]DeclaredDependency, 0, 4)
		for _, field := range []struct {
			key  string
			kind string
		}{
			{"skills", distribution.KindSkill},
			{"plugins", distribution.KindPlugin},
		} {
			items, err := dependencyItems(value.Dependencies[field.key], field.kind)
			if err != nil {
				return nil, fmt.Errorf("dependencies.%s: %w", field.key, err)
			}
			declared = append(declared, items...)
		}
		return declared, nil
	default:
		return nil, fmt.Errorf("不支持的扩展类型 %q", kind)
	}
}

// Published 是一个扩展在索引里的最新规范发布，供依赖 pin 定位使用。
type Published struct {
	Version     string
	SHA256      string
	Tag         string
	DownloadURL string
	ArtifactID  string
}

// Published 返回某扩展在索引里最新的规范发布。
//
// 只认 `release_tag` 是规范 tag 的条目：历史扁平 tag 的条目不能作为依赖 pin 的
// 依据，否则会把「已重做分发」这件事重新绕回旧命名。
func (c *Catalog) LatestPublished(kind, id string) (Published, bool) {
	best := Published{}
	found := false
	for _, entry := range c.Entries(kind) {
		if IDOf(kind, entry) != id {
			continue
		}
		tag, err := entryString(entry, "release_tag")
		if err != nil {
			continue
		}
		tagKind, tagID, version, err := distribution.ParseReleaseTag(tag)
		if err != nil || tagKind != kind || tagID != id {
			continue
		}
		if found && CompareVersions(version, best.Version) <= 0 {
			continue
		}
		best = Published{
			Version: version, SHA256: textOf(entry, "sha256"), Tag: tag,
			DownloadURL: textOf(entry, "download_url"), ArtifactID: textOf(entry, "artifact_id"),
		}
		found = true
	}
	return best, found
}

// PinDependencies 把声明层依赖解析成发布清单里的精确 pin。
//
// 规则：
//   - 索引里有该扩展的规范发布 → 精确到 `version + sha256 + tag + 下载地址`；
//   - 索引里没有、但声明了最低版本 → 只写最低版本并标记 `pinned=false`（非必需依赖）；
//   - 索引里没有、又是必需依赖 → 阻断发布，而不是发一个装不上的版本。
//
// 依赖顺序按声明解析结果排序（插件在前、技能在后），保证同一份输入产出同样字节。
func PinDependencies(declared []DeclaredDependency, c *Catalog, repository string) ([]distribution.ReleaseDependency, error) {
	repository = strings.TrimSpace(repository)
	pins := make([]distribution.ReleaseDependency, 0, len(declared))
	seen := map[string]bool{}
	for _, item := range declared {
		if !distribution.KnownKind(item.Kind) {
			return nil, fmt.Errorf("依赖类型不受支持: %q", item.Kind)
		}
		key := item.Kind + "/" + item.ID
		if seen[key] {
			continue
		}
		seen[key] = true
		pin := distribution.ReleaseDependency{
			Kind: item.Kind, ID: item.ID, Required: item.Required, MinVersion: item.MinVersion,
		}
		published, ok := c.LatestPublished(item.Kind, item.ID)
		switch {
		case ok:
			if item.MinVersion != "" && CompareVersions(published.Version, item.MinVersion) < 0 {
				return nil, fmt.Errorf("依赖 %s 需要不低于 %s，索引里最高是 %s",
					item.ID, item.MinVersion, published.Version)
			}
			pin.Version = published.Version
			pin.SHA256 = published.SHA256
			pin.Pinned = published.Version != "" && published.SHA256 != ""
			if repository != "" && published.Tag != "" {
				pin.Source = distribution.ReleaseDependencySource{
					Kind: "github", ID: "github:" + repository, Repository: repository,
					Reference: published.Tag, ArtifactURL: published.DownloadURL,
				}
			}
		case item.Required:
			return nil, fmt.Errorf("必需依赖 %s 还没有规范发布，先发布依赖再发布依赖它的扩展", item.ID)
		default:
			pin.Version = item.MinVersion
		}
		pins = append(pins, pin)
	}
	sort.SliceStable(pins, func(i, j int) bool {
		if pins[i].Kind != pins[j].Kind {
			return pins[i].Kind < pins[j].Kind
		}
		return pins[i].ID < pins[j].ID
	})
	return pins, nil
}

// dependencyText 读取依赖对象里的字符串字段。
func dependencyText(item map[string]interface{}, key string) string {
	value, ok := item[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

// dependencyRequired 读取一条依赖的 required 标记，缺省按必需处理。
func dependencyRequired(item map[string]interface{}, id string) (bool, error) {
	raw, ok := item["required"]
	if !ok || raw == nil {
		return true, nil
	}
	flag, isBool := raw.(bool)
	if !isBool {
		return false, fmt.Errorf("依赖 %s 的 required 不是布尔值", id)
	}
	return flag, nil
}

// dependencyItems 读取工作流的依赖列表，接受字符串与对象两种写法。
func dependencyItems(raw interface{}, kind string) ([]DeclaredDependency, error) {
	if raw == nil {
		return nil, nil
	}
	items, ok := raw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("必须是数组")
	}
	declared := make([]DeclaredDependency, 0, len(items))
	for _, item := range items {
		switch value := item.(type) {
		case string:
			id := strings.TrimSpace(value)
			if id == "" {
				return nil, fmt.Errorf("必须是非空字符串 ID")
			}
			declared = append(declared, DeclaredDependency{Kind: kind, ID: id, Required: true})
		case map[string]interface{}:
			id := dependencyText(value, kind+"_id")
			if id == "" {
				id = dependencyText(value, "id")
			}
			if id == "" {
				return nil, fmt.Errorf("依赖对象缺少 %s_id", kind)
			}
			required, err := dependencyRequired(value, id)
			if err != nil {
				return nil, err
			}
			declared = append(declared, DeclaredDependency{
				Kind: kind, ID: id, Required: required,
				MinVersion: dependencyText(value, "min_version"),
			})
		default:
			return nil, fmt.Errorf("依赖条目必须是字符串或对象")
		}
	}
	return declared, nil
}

// textOf 读取索引条目里的字符串字段。
func textOf(entry map[string]interface{}, key string) string {
	value, ok := entry[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}
