// styles.go —— 风格包：把「什么风格、什么参数、什么尺寸」固化成可复用配方。
//
// 内核的 media.image.generate 只接受裸 prompt。做系列素材时真正会漂移的不是
// 模型，而是人每次都换一种说法；风格包把提示词骨架、负向描述、默认尺寸与
// Provider 参数一起固定下来，使同一批素材可复现。
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const styleStoreFile = "styles.json"

// Style 是一个风格包。内置包与用户包同构，用 Builtin 区分。
type Style struct {
	ID           string         `json:"id"`
	Label        string         `json:"label"`
	Description  string         `json:"description"`
	PromptSuffix string         `json:"prompt_suffix"`
	Avoid        string         `json:"avoid"`
	Sizes        []string       `json:"sizes"`
	Params       map[string]any `json:"params,omitempty"`
	Builtin      bool           `json:"builtin"`
	CreatedAt    string         `json:"created_at,omitempty"`
	UpdatedAt    string         `json:"updated_at,omitempty"`
}

// builtinStyles 是随插件发布的起步配方。它们是**默认值**，不是能力边界：
// 用户可以覆盖同名 ID，也可以新建自己的包。
func builtinStyles() []Style {
	return []Style{
		{
			ID:           "clean-product",
			Label:        "干净产品图",
			Description:  "白底或浅灰底的棚拍质感，适合电商主图与物料封面。",
			PromptSuffix: "干净背景、柔和棚拍光、主体居中、真实材质、边缘清晰",
			Avoid:        "杂乱背景、文字水印、多余道具、过度锐化",
			Sizes:        []string{"1024x1024", "1280x720"},
			Builtin:      true,
		},
		{
			ID:           "exhibit-keyvisual",
			Label:        "展项主视觉",
			Description:  "展馆展项的开场视觉：有纵深、有灯光层次，主体明确。",
			PromptSuffix: "电影感构图、层次分明的灯光、克制的色彩、画面留出标题空间",
			Avoid:        "廉价霓虹、密集文字、杂乱装饰",
			Sizes:        []string{"1280x720", "1920x1080"},
			Builtin:      true,
		},
		{
			ID:           "icon-tile",
			Label:        "图标瓦片",
			Description:  "扩展与功能图标用的方形瓦片：深色底、单一符号、无文字。",
			PromptSuffix: "深色石墨底、圆角方形瓦片、居中单一线性符号、柔和玻璃质感、无文字无字母",
			Avoid:        "文字、数字、多主体、边框、水印",
			Sizes:        []string{"1024x1024"},
			Builtin:      true,
		},
		{
			ID:           "night-city",
			Label:        "城市夜景",
			Description:  "夜景与灯光氛围素材，适合背景板与转场。",
			PromptSuffix: "夜色、城市灯光、湿润地面反光、长曝光质感、冷暖对比",
			Avoid:        "白天光线、人群特写、文字招牌特写",
			Sizes:        []string{"1920x1080", "1280x720"},
			Builtin:      true,
		},
		{
			ID:           "ink-poster",
			Label:        "水墨海报",
			Description:  "水墨与大留白的版式感，适合文化类展项与封面。",
			PromptSuffix: "水墨晕染、大量留白、单色主调、纸张纹理、克制笔触",
			Avoid:        "照片写实、鲜艳渐变、密集元素",
			Sizes:        []string{"1024x1365"},
			Builtin:      true,
		},
		{
			ID:           "brand-minimal",
			Label:        "极简品牌",
			Description:  "几何与纯色块的品牌向视觉，方便叠加标题与 logo。",
			PromptSuffix: "几何构成、纯色块、微对比、居中对称、可叠加标题的留白",
			Avoid:        "复杂纹理、渐变光斑、写实人物",
			Sizes:        []string{"1200x628", "1024x1024"},
			Builtin:      true,
		},
	}
}

func styleIndex() map[string]Style {
	index := map[string]Style{}
	for _, style := range builtinStyles() {
		index[style.ID] = style
	}
	return index
}

func styleStorePath() (string, error) {
	root, err := dataRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, styleStoreFile), nil
}

// loadCustomStyles 读取用户风格包；文件不存在视为空集。
func loadCustomStyles() ([]Style, error) {
	path, err := styleStorePath()
	if err != nil {
		return nil, err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取风格包失败: %w", err)
	}
	var payload struct {
		Styles []Style `json:"styles"`
	}
	if err := json.Unmarshal(content, &payload); err != nil {
		return nil, fmt.Errorf("风格包文件损坏: %w", err)
	}
	return payload.Styles, nil
}

func saveCustomStyles(styles []Style) error {
	path, err := styleStorePath()
	if err != nil {
		return err
	}
	sort.Slice(styles, func(left, right int) bool { return styles[left].ID < styles[right].ID })
	payload := map[string]any{"schema_version": 1, "styles": styles}
	content, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化风格包失败: %w", err)
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, content, 0o644); err != nil {
		return fmt.Errorf("写入风格包失败: %w", err)
	}
	return os.Rename(temporary, path)
}

// allStyles 合并内置与用户包：用户包覆盖同 ID 的内置包，并保留内置标记来源。
func allStyles() ([]Style, error) {
	custom, err := loadCustomStyles()
	if err != nil {
		return nil, err
	}
	merged := map[string]Style{}
	for _, style := range builtinStyles() {
		merged[style.ID] = style
	}
	for _, style := range custom {
		style.Builtin = false
		merged[style.ID] = style
	}
	result := make([]Style, 0, len(merged))
	for _, style := range merged {
		result = append(result, style)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Builtin != result[right].Builtin {
			return result[left].Builtin
		}
		return result[left].ID < result[right].ID
	})
	return result, nil
}

// findStyle 按 ID 取一个风格包。
func findStyle(id string) (Style, bool, error) {
	styles, err := allStyles()
	if err != nil {
		return Style{}, false, err
	}
	trimmed := strings.TrimSpace(id)
	for _, style := range styles {
		if style.ID == trimmed {
			return style, true, nil
		}
	}
	return Style{}, false, nil
}

var styleIDPattern = func(value string) bool {
	if value == "" || len(value) > 48 {
		return false
	}
	for index, char := range value {
		switch {
		case char >= 'a' && char <= 'z':
		case char >= '0' && char <= '9':
		case char == '-':
			if index == 0 || index == len(value)-1 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// normalizeStyle 校验并补齐一个待保存的风格包。
func normalizeStyle(input Style) (Style, error) {
	style := input
	style.ID = strings.ToLower(strings.TrimSpace(style.ID))
	if !styleIDPattern(style.ID) {
		return Style{}, fmt.Errorf("风格包 ID 只能用小写字母、数字和连字符，且不能以连字符开头或结尾: %q", input.ID)
	}
	if strings.TrimSpace(style.Label) == "" {
		return Style{}, fmt.Errorf("风格包需要 label")
	}
	if len([]rune(style.Label)) > 40 {
		return Style{}, fmt.Errorf("label 不能超过 40 个字符")
	}
	if strings.TrimSpace(style.PromptSuffix) == "" {
		return Style{}, fmt.Errorf("风格包需要 prompt_suffix：它决定生成的风格")
	}
	if len([]rune(style.PromptSuffix)) > 600 {
		return Style{}, fmt.Errorf("prompt_suffix 不能超过 600 个字符")
	}
	if len([]rune(style.Description)) > 200 {
		return Style{}, fmt.Errorf("description 不能超过 200 个字符")
	}
	if len([]rune(style.Avoid)) > 300 {
		return Style{}, fmt.Errorf("avoid 不能超过 300 个字符")
	}
	style.Label = strings.TrimSpace(style.Label)
	style.Description = strings.TrimSpace(style.Description)
	style.PromptSuffix = strings.TrimSpace(style.PromptSuffix)
	style.Avoid = strings.TrimSpace(style.Avoid)
	style.Sizes = normalizeSizes(style.Sizes)
	if len(style.Sizes) == 0 {
		style.Sizes = []string{"1024x1024"}
	}
	if len(style.Sizes) > 6 {
		style.Sizes = style.Sizes[:6]
	}
	style.Builtin = false
	return style, nil
}

// normalizeSizes 过滤非法尺寸，保持调用方给出的顺序。
func normalizeSizes(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(strings.ToLower(value))
		if trimmed == "" {
			continue
		}
		var width, height int
		if _, err := fmt.Sscanf(trimmed, "%dx%d", &width, &height); err != nil {
			continue
		}
		if width < 64 || height < 64 || width > 4096 || height > 4096 {
			continue
		}
		normalized := fmt.Sprintf("%dx%d", width, height)
		if seen[normalized] {
			continue
		}
		seen[normalized] = true
		result = append(result, normalized)
	}
	return result
}

// composePrompt 把基础提示词与风格包拼成最终 prompt。
//
// 顺序刻意固定为「主体描述 → 风格 → 避免项」，因为模型对靠后的约束更敏感，
// 避免项放最后能显著减少跑偏。
func composePrompt(base string, style Style) string {
	parts := make([]string, 0, 3)
	if trimmed := strings.TrimSpace(base); trimmed != "" {
		parts = append(parts, strings.TrimSpace(trimmed))
	}
	if trimmed := strings.TrimSpace(style.PromptSuffix); trimmed != "" {
		parts = append(parts, "风格要求："+trimmed)
	}
	if trimmed := strings.TrimSpace(style.Avoid); trimmed != "" {
		parts = append(parts, "不要出现："+trimmed)
	}
	return strings.Join(parts, "。")
}
