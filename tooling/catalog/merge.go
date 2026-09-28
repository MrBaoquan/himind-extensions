package catalog

import (
	"os"
	"strings"
)

// LoadMerged 读取多个市场索引并合并成一份只读查询索引。
//
// 用途只有一个：解析依赖 pin。一个仓的索引只记录「本仓发布过什么」，而依赖
// 可能由别的分发仓发布（项目仓的工作流依赖官方仓的插件）。把两边的索引合起来
// 查，`PinDependencies` 才能把跨分发依赖 pin 到真正发货的那个仓。
//
// 合并是纯查询行为：不写盘、不校验 schema 之外的语义、不改任何一方的索引。
// 路径不存在会被跳过（视作该索引为空），让本地只克隆一个仓的场景仍能给出
// 「依赖解析不到」这种明确结论，而不是在解析文件时崩掉。
func LoadMerged(paths ...string) (*Catalog, error) {
	merged := New("", "", "")
	// 能力包是「哪个仓托管了这些扩展」的事实，只属于各自的索引；合并索引只用
	// 来查依赖，不携带任何一方的能力包，避免调用方误把它当成一份真实索引。
	merged.FeaturePacks = []FeaturePack{}
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			continue
		}
		value, err := Load(path)
		if err != nil {
			return nil, err
		}
		if merged.DistributionID == "" {
			merged.DistributionID = value.DistributionID
			merged.Channel = value.Channel
			merged.CatalogID = value.CatalogID
		}
		merged.Plugins = append(merged.Plugins, value.Plugins...)
		merged.Skills = append(merged.Skills, value.Skills...)
		merged.Workflows = append(merged.Workflows, value.Workflows...)
	}
	return merged, nil
}
