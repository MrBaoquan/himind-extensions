package design

import (
	"encoding/json"
	"io/fs"
	"strings"
	"unicode"
)

type CJKFontPair struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Heading []string `json:"heading"`
	Body    []string `json:"body"`
	BestFor []string `json:"best_for"`
	Note    string   `json:"note"`
}

type CJKGuidance struct {
	SchemaVersion int           `json:"schema_version"`
	UpdatedAt     string        `json:"updated_at"`
	Source        string        `json:"source"`
	FontFiles     bool          `json:"font_files_bundled"`
	FontPairs     []CJKFontPair `json:"font_pairs"`
}

func (c *Catalog) CJKGuidance(query string) map[string]any {
	guidance := CJKGuidance{}
	if data, err := fs.ReadFile(c.fsys, "data/cjk-guidance.json"); err == nil {
		_ = json.Unmarshal(data, &guidance)
	}
	selected := CJKFontPair{ID: "technical-ui", Name: "数据界面黑体", Heading: []string{"HarmonyOS Sans SC", "Noto Sans SC", "Microsoft YaHei"}, Body: []string{"Noto Sans SC", "Microsoft YaHei", "PingFang SC"}, Note: "中文界面优先使用本地可用的现代黑体。"}
	lower := strings.ToLower(query)
	matched := false
	for _, pair := range guidance.FontPairs {
		for _, term := range pair.BestFor {
			if strings.Contains(lower, strings.ToLower(term)) {
				selected = pair
				matched = true
				break
			}
		}
		if matched {
			break
		}
	}
	return map[string]any{
		"selected":           selected,
		"is_cjk":             containsHan(query),
		"source":             "himind-local-guidance",
		"font_files_bundled": false,
		"note":               "字体只作为搭配与回退建议，不随插件分发字体文件；上线前必须确认授权、字重和离线可用性。",
		"updated_at":         guidance.UpdatedAt,
	}
}

func containsHan(value string) bool {
	for _, r := range value {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}
