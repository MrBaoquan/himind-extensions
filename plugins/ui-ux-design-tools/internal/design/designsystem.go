package design

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

type GenerateInput struct {
	Query       string `json:"query"`
	ProjectName string `json:"project_name"`
	Variance    int    `json:"variance"`
	Motion      int    `json:"motion"`
	Density     int    `json:"density"`
}

func (c *Catalog) Generate(in GenerateInput) (map[string]any, error) {
	query := strings.TrimSpace(in.Query)
	if query == "" {
		return nil, errInvalid("设计需求不能为空")
	}
	if len([]rune(query)) > 1000 {
		return nil, errInvalid("设计需求不能超过 1000 个字符")
	}
	for name, value := range map[string]int{"variance": in.Variance, "motion": in.Motion, "density": in.Density} {
		if value != 0 && (value < 1 || value > 10) {
			return nil, errInvalid(fmt.Sprintf("%s 必须在 1 到 10 之间", name))
		}
	}
	variance := defaultDial(in.Variance)
	motion := defaultDial(in.Motion)
	density := defaultDial(in.Density)
	projectName := strings.TrimSpace(in.ProjectName)
	if projectName == "" {
		projectName = "未命名项目"
	}

	productQuery := query
	if containsAny(strings.ToLower(ExpandQuery(query)), "dashboard", "analytics", "monitoring", "data dense", "kpi", "metric") {
		productQuery += " dashboard analytics kpi metric data dense operations monitoring"
	}
	productHit, productOK := c.best(productQuery, "product", 1)
	product := Record{}
	if productOK {
		product = productHit.Fields
	}
	productType := product["Product Type"]
	styleQuery := query + " " + product["Primary Style Recommendation"] + " " + varianceKeywords(variance)
	if containsAny(strings.ToLower(ExpandQuery(query)), "dashboard", "analytics", "monitoring", "data dense") {
		styleQuery += " data-dense dashboard"
	}
	styleHit, styleOK := c.best(styleQuery, "style", 1)
	style := Record{}
	if styleOK {
		style = styleHit.Fields
	}
	preferredStyles := splitList(product["Primary Style Recommendation"])
	if len(preferredStyles) > 0 {
		if row, ok := c.exact("style", "Style Category", preferredStyles[0]); ok {
			style = row
			styleOK = true
		}
	}
	if !styleOK {
		style = c.first("style")
	}
	colorQuery := query + " " + productType + " " + product["Color Palette Focus"]
	colorHit, colorOK := c.best(colorQuery, "color", 1)
	colors := Record{}
	if colorOK {
		colors = colorHit.Fields
	}
	if !colorOK && productType != "" {
		if row, ok := c.exact("color", "Product Type", productType); ok {
			colors = row
			colorOK = true
		}
	}
	if !colorOK {
		colors = c.first("color")
	}
	typographyQuery := query + " " + productType + " " + product["Key Considerations"]
	typographyHit, typographyOK := c.best(typographyQuery, "typography", 1)
	typography := Record{}
	if typographyOK {
		typography = typographyHit.Fields
	} else {
		typography = c.first("typography")
	}
	landing := Record{}
	dashboardFocused := strings.HasPrefix(strings.ToUpper(strings.TrimSpace(product["Landing Page Pattern"])), "N/A")
	if !dashboardFocused {
		landingQuery := query + " " + product["Landing Page Pattern"]
		landingHit, found := c.best(landingQuery, "landing", 1)
		if found {
			landing = landingHit.Fields
		} else {
			landing = c.first("landing")
		}
	}
	reasoningQuery := productType
	if reasoningQuery == "" {
		reasoningQuery = query
	}
	reasoningHit, reasoningOK := c.best(reasoningQuery, "reasoning", 1)
	reasoning := Record{}
	if reasoningOK {
		reasoning = reasoningHit.Fields
	}

	guidelines := c.guidelines(query)
	charts := c.chartSuggestions(query + " " + productType)
	motionPreset := c.motionPreset(motion)
	spacing := spacingScale(density)
	contrastChecks := buildContrastChecks(colors)
	warnings := []string{}
	if !productOK {
		warnings = append(warnings, "没有找到匹配的产品类型，使用通用设计规则。")
	}

	if !colorOK {
		warnings = append(warnings, "没有找到匹配配色，使用数据快照中的默认配色。")
	}
	if !typographyOK {
		warnings = append(warnings, "没有找到匹配字体搭配，使用数据快照中的默认字体。")
	}

	result := map[string]any{
		"status":  "ok",
		"project": map[string]any{"name": projectName, "query": query},
		"dials":   map[string]any{"variance": variance, "motion": motion, "density": density, "variance_label": varianceLabel(variance), "motion_label": motionLabel(motion), "density_label": densityLabel(density)},
		"product": map[string]any{
			"identity": pick(product, "Product Type"),
			"matched":  productOK,
			"fields":   map[string]string{"product_type": pick(product, "Product Type"), "keywords": pick(product, "Keywords"), "primary_style": pick(product, "Primary Style Recommendation"), "secondary_styles": pick(product, "Secondary Styles"), "landing_pattern": pick(product, "Landing Page Pattern"), "dashboard_style": pick(product, "Dashboard Style (if applicable)"), "color_focus": pick(product, "Color Palette Focus"), "key_considerations": pick(product, "Key Considerations")},
		},
		"pattern": dashboardPattern(product, landing, dashboardFocused),
		"style": map[string]any{
			"identity":        pick(style, "Style Category"),
			"keywords":        pick(style, "Keywords"),
			"best_for":        pick(style, "Best For"),
			"performance":     pick(style, "Performance"),
			"accessibility":   pick(style, "Accessibility"),
			"prompt_keywords": pick(style, "AI Prompt Keywords"),
			"css_keywords":    pick(style, "CSS/Technical Keywords"),
			"checklist":       pick(style, "Implementation Checklist"),
		},
		"colors": map[string]any{
			"identity": pick(colors, "Product Type"),
			"roles": map[string]string{
				"primary": pick(colors, "Primary"), "on_primary": pick(colors, "On Primary"),
				"secondary": pick(colors, "Secondary"), "on_secondary": pick(colors, "On Secondary"),
				"accent": pick(colors, "Accent"), "on_accent": pick(colors, "On Accent"),
				"background": pick(colors, "Background"), "foreground": pick(colors, "Foreground"),
				"card": pick(colors, "Card"), "card_foreground": pick(colors, "Card Foreground"),
				"muted": pick(colors, "Muted"), "muted_foreground": pick(colors, "Muted Foreground"),
				"border": pick(colors, "Border"), "destructive": pick(colors, "Destructive"),
				"on_destructive": pick(colors, "On Destructive"), "ring": pick(colors, "Ring"),
			},
			"notes":           pick(colors, "Notes"),
			"contrast_checks": contrastChecks,
			"css_variables":   cssVariables(colors),
		},
		"typography": map[string]any{
			"pairing":          pick(typography, "Font Pairing Name"),
			"heading_font":     pick(typography, "Heading Font"),
			"body_font":        pick(typography, "Body Font"),
			"mood":             pick(typography, "Mood/Style Keywords"),
			"best_for":         pick(typography, "Best For"),
			"google_fonts_url": pick(typography, "Google Fonts URL"),
			"css_import":       pick(typography, "CSS Import"),
			"notes":            pick(typography, "Notes"),
		},
		"spacing":       spacing,
		"motion":        motionPreset,
		"charts":        charts,
		"guidelines":    guidelines,
		"reasoning":     map[string]any{"matched": reasoningOK, "pattern": pick(reasoning, "Recommended_Pattern"), "style_priority": pick(reasoning, "Style_Priority"), "color_mood": pick(reasoning, "Color_Mood"), "typography_mood": pick(reasoning, "Typography_Mood"), "effects": pick(reasoning, "Key_Effects"), "anti_patterns": pick(reasoning, "Anti_Patterns")},
		"anti_patterns": mergeLists(splitList(pick(style, "Do Not Use For")), splitList(pick(reasoning, "Anti_Patterns"))),
		"checklist":     deliveryChecklist(),
		"cjk_fonts":     c.CJKGuidance(query),
		"warnings":      warnings,
		"source":        c.upstreamMeta(),
	}
	return result, nil
}

func dashboardPattern(product, landing Record, dashboardFocused bool) map[string]any {
	if dashboardFocused {
		return map[string]any{
			"name": product["Landing Page Pattern"], "section_order": "核心指标摘要 > 筛选与时间范围 > 主要数据视图 > 钻取与详情 > 状态反馈",
			"cta_placement": "主操作贴近当前上下文，不使用营销落地页 CTA。", "conversion_focus": "监控、判断、钻取与操作效率",
			"recommended_effects": "仅保留状态反馈和必要过渡", "source": "himind-local-guidance",
		}
	}
	return map[string]any{
		"name": pick(landing, "Pattern Name"), "section_order": pick(landing, "Section Order"),
		"cta_placement": pick(landing, "Primary CTA Placement"), "conversion_focus": pick(landing, "Conversion Optimization"),
		"recommended_effects": pick(landing, "Recommended Effects"), "source": "upstream-snapshot",
	}
}

func defaultDial(value int) int {
	if value == 0 {
		return 5
	}
	return value
}

func (c *Catalog) exact(domain, field, value string) (Record, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return Record{}, false
	}
	rows, err := c.Rows(domain)
	if err != nil {
		return Record{}, false
	}
	for _, row := range rows {
		if strings.EqualFold(strings.TrimSpace(row[field]), value) {
			return row, true
		}
	}
	return Record{}, false
}

func (c *Catalog) best(query, domain string, top int) (SearchHit, bool) {
	result, err := c.Search(query, domain, "", top)
	if err != nil || len(result.Matches) == 0 {
		return SearchHit{}, false
	}
	return result.Matches[0], true
}

func (c *Catalog) first(domain string) Record {
	rows, err := c.Rows(domain)
	if err != nil || len(rows) == 0 {
		return Record{}
	}
	return rows[0]
}

func (c *Catalog) guidelines(query string) []map[string]any {
	result, err := c.Search(query+" accessibility keyboard focus contrast usability", "ux", "", 5)
	rows := []Record{}
	if err == nil {
		for _, hit := range result.Matches {
			rows = append(rows, hit.Fields)
		}
	}
	if len(rows) < 5 {
		fallback, err := c.Search("accessibility keyboard focus contrast usability", "ux", "", 8)
		if err == nil {
			for _, hit := range fallback.Matches {
				rows = append(rows, hit.Fields)
				if len(rows) >= 5 {
					break
				}
			}
		}
	}
	out := []map[string]any{}
	seen := map[string]bool{}
	for _, row := range rows {
		key := row["Issue"]
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, map[string]any{"category": row["Category"], "issue": row["Issue"], "description": row["Description"], "do": row["Do"], "dont": row["Don't"], "severity": row["Severity"], "platform": row["Platform"]})
		if len(out) == 5 {
			break
		}
	}
	return out
}

func (c *Catalog) chartSuggestions(query string) []map[string]any {
	result, err := c.Search(query, "chart", "", 3)
	if err != nil || len(result.Matches) == 0 {
		return []map[string]any{}
	}
	out := []map[string]any{}
	for _, hit := range result.Matches {
		row := hit.Fields
		out = append(out, map[string]any{"data_type": row["Data Type"], "best_chart": row["Best Chart Type"], "secondary_options": row["Secondary Options"], "when_to_use": row["When to Use"], "when_not_to_use": row["When NOT to Use"], "accessibility": row["Accessibility Notes"], "library": row["Library Recommendation"]})
	}
	return out
}

func (c *Catalog) motionPreset(dial int) map[string]any {
	tier := motionLabel(dial)
	intent := "hover"
	if tier == "Standard" {
		intent = "scroll reveal stagger"
	} else if tier == "Complex" {
		intent = "page transition parallax"
	}
	result, err := c.Search(tier+" "+intent+" motion", "gsap", "", 3)
	if err != nil || len(result.Matches) == 0 {
		return map[string]any{"tier": tier, "matched": false}
	}
	row := result.Matches[0].Fields
	return map[string]any{"tier": tier, "matched": true, "category": row["Category"], "trigger": row["Trigger"], "duration": row["Duration"], "easing": row["Easing"], "snippet": row["GSAP Snippet"], "framework_notes": row["Framework Notes"], "do": row["Do"], "dont": row["Don't"], "performance_notes": row["Performance Notes"]}
}

func varianceKeywords(value int) string {
	switch {
	case value <= 3:
		return "minimal centered symmetric grid-based"
	case value >= 8:
		return "bold asymmetric bento grid experimental"
	default:
		return "modern balanced structured"
	}
}

func varianceLabel(value int) string {
	switch {
	case value <= 3:
		return "克制居中"
	case value >= 8:
		return "大胆非对称"
	default:
		return "均衡现代"
	}
}

func motionLabel(value int) string {
	switch {
	case value <= 3:
		return "Subtle"
	case value >= 8:
		return "Complex"
	default:
		return "Standard"
	}
}

func densityLabel(value int) string {
	switch {
	case value <= 3:
		return "宽松"
	case value >= 8:
		return "紧凑/仪表盘"
	default:
		return "标准"
	}
}

func spacingScale(value int) map[string]any {
	if value <= 3 {
		return map[string]any{"label": "宽松", "scale": map[string]string{"xs": "4px", "sm": "8px", "md": "24px", "lg": "32px", "xl": "48px", "2xl": "64px", "3xl": "96px"}}
	}
	if value >= 8 {
		return map[string]any{"label": "紧凑/仪表盘", "scale": map[string]string{"xs": "2px", "sm": "4px", "md": "8px", "lg": "12px", "xl": "16px", "2xl": "24px", "3xl": "32px"}}
	}
	return map[string]any{"label": "标准", "scale": map[string]string{"xs": "4px", "sm": "8px", "md": "16px", "lg": "24px", "xl": "32px", "2xl": "48px", "3xl": "64px"}}
}

func deliveryChecklist() []string {
	return []string{
		"不使用 Emoji 充当图标；图标按钮提供可读名称。",
		"所有可点击元素具备指针反馈、按下反馈和可见焦点。",
		"正文对比度至少 4.5:1，大号文本至少 3:1；颜色不作为唯一信息通道。",
		"键盘可完成核心任务，焦点不被固定层遮挡。",
		"尊重 prefers-reduced-motion，并提供静态最终状态。",
		"在 360、768、1024、1440 宽度和 200% 缩放下检查重排与溢出。",
		"中文、长文本、空数据、错误数据和极端数值均有明确布局。",
	}
}

func pick(row Record, fields ...string) string {
	for _, field := range fields {
		if value := strings.TrimSpace(row[field]); value != "" {
			return value
		}
	}
	return ""
}

func splitList(value string) []string {
	value = strings.NewReplacer(" + ", ",", ";", ",", "；", ",", "，", ",").Replace(value)
	parts := strings.Split(value, ",")
	out := []string{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func mergeLists(lists ...[]string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, list := range lists {
		for _, value := range list {
			if value != "" && !seen[value] {
				seen[value] = true
				out = append(out, value)
			}
		}
	}
	sort.Strings(out)
	return out
}

func cssVariables(colors Record) map[string]string {
	roles := map[string]string{
		"primary": "Primary", "on-primary": "On Primary", "secondary": "Secondary", "on-secondary": "On Secondary",
		"accent": "Accent", "on-accent": "On Accent", "background": "Background", "foreground": "Foreground",
		"card": "Card", "card-foreground": "Card Foreground", "muted": "Muted", "muted-foreground": "Muted Foreground",
		"border": "Border", "destructive": "Destructive", "on-destructive": "On Destructive", "ring": "Ring",
	}
	out := map[string]string{}
	for variable, field := range roles {
		if value := strings.TrimSpace(colors[field]); value != "" {
			out["--color-"+variable] = value
		}
	}
	return out
}

func buildContrastChecks(colors Record) []map[string]any {
	pairs := []struct{ Foreground, Background, Required string }{
		{"Foreground", "Background", "4.5"},
		{"Primary", "On Primary", "4.5"},
		{"Accent", "On Accent", "4.5"},
		{"Muted Foreground", "Muted", "4.5"},
	}
	out := []map[string]any{}
	for _, pair := range pairs {
		ratio, err := contrastRatio(colors[pair.Foreground], colors[pair.Background])
		if err != nil {
			continue
		}
		required, _ := strconv.ParseFloat(pair.Required, 64)
		out = append(out, map[string]any{"foreground": colors[pair.Foreground], "background": colors[pair.Background], "pair": pair.Foreground + " / " + pair.Background, "ratio": math.Round(ratio*100) / 100, "required": required, "passes_aa_normal": ratio >= required})
	}
	return out
}

func contrastRatio(foreground, background string) (float64, error) {
	fg, err := hexRGB(foreground)
	if err != nil {
		return 0, err
	}
	bg, err := hexRGB(background)
	if err != nil {
		return 0, err
	}
	l1, l2 := relativeLuminance(fg), relativeLuminance(bg)
	if l1 < l2 {
		l1, l2 = l2, l1
	}
	return (l1 + 0.05) / (l2 + 0.05), nil
}

type rgb struct{ r, g, b float64 }

func hexRGB(value string) (rgb, error) {
	value = strings.TrimSpace(strings.TrimPrefix(value, "#"))
	if len(value) == 3 {
		value = string([]byte{value[0], value[0], value[1], value[1], value[2], value[2]})
	}
	if len(value) != 6 {
		return rgb{}, fmt.Errorf("invalid hex color: %s", value)
	}
	component := func(start int) (float64, error) {
		n, err := strconv.ParseInt(value[start:start+2], 16, 64)
		if err != nil {
			return 0, err
		}
		return float64(n) / 255, nil
	}
	r, err := component(0)
	if err != nil {
		return rgb{}, err
	}
	g, err := component(2)
	if err != nil {
		return rgb{}, err
	}
	b, err := component(4)
	if err != nil {
		return rgb{}, err
	}
	return rgb{r: r, g: g, b: b}, nil
}

func relativeLuminance(color rgb) float64 {
	convert := func(value float64) float64 {
		if value <= 0.04045 {
			return value / 12.92
		}
		return math.Pow((value+0.055)/1.055, 2.4)
	}
	return 0.2126*convert(color.r) + 0.7152*convert(color.g) + 0.0722*convert(color.b)
}
