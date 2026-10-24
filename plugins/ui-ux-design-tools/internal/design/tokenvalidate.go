package design

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type TokenValidateInput struct {
	WorkspaceRoot string   `json:"workspace_root"`
	Paths         []string `json:"paths"`
	MaxFiles      int      `json:"max_files"`
}

var (
	cssTokenPattern         = regexp.MustCompile(`--[a-zA-Z0-9_-]+\s*:\s*[^;}\n]+`)
	xamlColorElementPattern = regexp.MustCompile(`(?is)<(?:SolidColorBrush|Color)\b[^>]*>`)
	xamlKeyPattern          = regexp.MustCompile(`(?i)\bx:Key\s*=\s*"([^"]+)"`)
	xamlColorPattern        = regexp.MustCompile(`(?i)\bColor\s*=\s*"([^"]+)"`)
	colorPattern            = regexp.MustCompile(`(?i)#[0-9a-f]{3,8}\b|rgba?\([^)]*\)|hsla?\([^)]*\)`)
)

var contrastPairs = []struct {
	Label      string
	Foreground []string
	Background []string
}{
	{"foreground / background", []string{"foreground", "text", "color-foreground", "color-text"}, []string{"background", "bg", "color-background", "color-bg"}},
	{"primary / on-primary", []string{"primary", "color-primary"}, []string{"on-primary", "onprimary", "primary-foreground", "color-on-primary"}},
	{"accent / on-accent", []string{"accent", "color-accent"}, []string{"on-accent", "onaccent", "accent-foreground", "color-on-accent"}},
	{"muted-foreground / muted", []string{"muted-foreground", "mutedforeground", "color-muted-foreground"}, []string{"muted", "color-muted"}},
	{"card-foreground / card", []string{"card-foreground", "cardforeground", "color-card-foreground"}, []string{"card", "color-card"}},
	{"TlTextPrimary / TlPanelBg", []string{"TlTextPrimary"}, []string{"TlPanelBg"}},
	{"TlTextSecondary / TlPanelBg", []string{"TlTextSecondary"}, []string{"TlPanelBg"}},
	{"TlTextMuted / TlPanelBg", []string{"TlTextMuted"}, []string{"TlPanelBg"}},
	{"TlAccentFg / TlAccent", []string{"TlAccentFg"}, []string{"TlAccent"}},
	{"TlSuccessSoftFg / TlSuccessSoftBg", []string{"TlSuccessSoftFg"}, []string{"TlSuccessSoftBg"}},
	{"TlWarningSoftFg / TlWarningSoftBg", []string{"TlWarningSoftFg"}, []string{"TlWarningSoftBg"}},
	{"TlDangerSoftFg / TlDangerSoftBg", []string{"TlDangerSoftFg"}, []string{"TlDangerSoftBg"}},
	{"TlInfoSoftFg / TlInfoSoftBg", []string{"TlInfoSoftFg"}, []string{"TlInfoSoftBg"}},
}

func (c *Catalog) ValidateTokens(in TokenValidateInput) (map[string]any, error) {
	root, err := resolveWorkspaceRoot(in.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	maxFiles := in.MaxFiles
	if maxFiles == 0 {
		maxFiles = 50
	}
	if maxFiles < 1 || maxFiles > 200 {
		return nil, errInvalid("max_files 必须在 1 到 200 之间")
	}
	paths, err := tokenFilePaths(root, in.Paths, maxFiles)
	if err != nil {
		return nil, err
	}
	tokens := map[string]string{}
	hardcoded := []map[string]any{}
	issues := []map[string]any{}
	checked := []string{}
	for _, relative := range paths {
		full, err := safeWorkspaceTarget(root, relative)
		if err != nil {
			return nil, err
		}
		data, err := readSafeFile(root, full, 2<<20)
		if err != nil {
			issues = append(issues, issue("warning", "file_unreadable", relative, 0, err.Error()))
			continue
		}
		checked = append(checked, relative)
		lower := strings.ToLower(relative)
		if strings.HasSuffix(lower, ".json") {
			var doc any
			if json.Unmarshal(data, &doc) == nil {
				collectJSONColors("", doc, tokens)
			} else {
				issues = append(issues, issue("warning", "json_parse_failed", relative, 0, "JSON 令牌文件无法解析"))
			}
			continue
		}
		for _, tokenLine := range cssTokenPattern.FindAllString(string(data), -1) {
			parts := strings.SplitN(tokenLine, ":", 2)
			if len(parts) != 2 {
				continue
			}
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(strings.Split(parts[1], "}")[0])
			if key != "" && value != "" {
				tokens[key] = value
			}
		}
		if isXamlPath(lower) {
			for _, element := range xamlColorElementPattern.FindAllString(string(data), -1) {
				keyMatch := xamlKeyPattern.FindStringSubmatch(element)
				colorMatch := xamlColorPattern.FindStringSubmatch(element)
				if len(keyMatch) == 2 && len(colorMatch) == 2 && colorPattern.MatchString(colorMatch[1]) {
					tokens[strings.TrimSpace(keyMatch[1])] = strings.TrimSpace(colorMatch[1])
				}
			}
		}
		if isCodeOrMarkup(lower) {
			inXamlComment := false
			for lineNo, line := range strings.Split(string(data), "\n") {
				trimmed := strings.TrimSpace(line)
				if isXamlPath(lower) {
					if strings.Contains(trimmed, "<!--") {
						inXamlComment = true
					}
					if inXamlComment {
						if strings.Contains(trimmed, "-->") {
							inXamlComment = false
						}
						continue
					}
					if strings.Contains(trimmed, "x:Key=") &&
						(strings.Contains(trimmed, "<SolidColorBrush") || strings.Contains(trimmed, "<BoxShadows") || strings.Contains(trimmed, "<Color")) {
						continue
					}
				}
				if strings.HasPrefix(trimmed, "--") || strings.HasPrefix(lower, "design-system/") {
					continue
				}
				for _, color := range colorPattern.FindAllString(line, -1) {
					hardcoded = append(hardcoded, map[string]any{"file": relative, "line": lineNo + 1, "value": color, "severity": "warning"})
					if len(hardcoded) >= 200 {
						break
					}
				}
				if len(hardcoded) >= 200 {
					break
				}
			}
		}
	}
	if len(hardcoded) > 0 {
		issues = append(issues, map[string]any{"severity": "warning", "code": "hardcoded_colors", "message": fmt.Sprintf("发现 %d 处硬编码颜色，建议改为语义令牌。", len(hardcoded))})
	}
	lookup := normalizeTokenLookup(tokens)
	checks := []map[string]any{}
	for _, pair := range contrastPairs {
		fgKey, fg := findToken(lookup, pair.Foreground)
		bgKey, bg := findToken(lookup, pair.Background)
		if fg == "" || bg == "" {
			continue
		}
		ratio, err := contrastRatio(fg, bg)
		if err != nil {
			continue
		}
		required := 4.5
		pass := ratio >= required
		check := map[string]any{"pair": pair.Label, "foreground_token": fgKey, "foreground": fg, "background_token": bgKey, "background": bg, "ratio": round2(ratio), "required": required, "passes_aa_normal": pass}
		checks = append(checks, check)
		if !pass {
			issues = append(issues, map[string]any{"severity": "error", "code": "contrast_below_aa", "message": fmt.Sprintf("%s 对比度 %.2f:1 低于 4.5:1。", pair.Label, ratio), "pair": pair.Label})
		}
	}
	errorsCount, warningsCount := 0, 0
	for _, item := range issues {
		if item["severity"] == "error" {
			errorsCount++
		} else {
			warningsCount++
		}
	}
	status := "ok"
	if len(checked) == 0 {
		status = "no_files"
	} else if errorsCount > 0 {
		status = "issues"
	}
	if len(tokens) == 0 {
		issues = append(issues, issue("warning", "no_tokens", "", 0, "未发现可识别的设计令牌。"))
		warningsCount++
	}
	return map[string]any{
		"status":           status,
		"files_checked":    checked,
		"tokens":           tokens,
		"token_count":      len(tokens),
		"hardcoded_colors": hardcoded,
		"contrast_checks":  checks,
		"issues":           issues,
		"summary":          map[string]any{"errors": errorsCount, "warnings": warningsCount, "files": len(checked)},
	}, nil
}

func tokenFilePaths(root string, requested []string, maxFiles int) ([]string, error) {
	if len(requested) > 20 {
		return nil, errInvalid("paths 最多包含 20 个文件")
	}
	if len(requested) > 0 {
		out := []string{}
		for _, item := range requested {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			full, err := safeWorkspaceTarget(root, item)
			if err != nil {
				return nil, err
			}
			info, err := os.Stat(full)
			if err != nil || !info.Mode().IsRegular() {
				return nil, errInvalid("令牌文件不存在或是目录：" + item)
			}
			out = append(out, filepath.ToSlash(item))
		}
		return out, nil
	}
	out := []string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		relative = filepath.ToSlash(relative)
		depth := strings.Count(relative, "/") + 1
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if skippedDirectories[strings.ToLower(entry.Name())] || depth > 5 {
				return filepath.SkipDir
			}
			return nil
		}
		lower := strings.ToLower(relative)
		base := filepath.Base(lower)
		if strings.HasSuffix(lower, ".css") || strings.HasSuffix(lower, ".scss") || strings.HasSuffix(lower, ".html") || strings.HasSuffix(lower, ".json") || strings.HasSuffix(lower, ".xaml") || strings.HasSuffix(lower, ".axaml") || strings.HasPrefix(base, "tailwind.config.") {
			out = append(out, relative)
		}
		if len(out) >= maxFiles {
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("扫描令牌文件失败：%w", err)
	}
	sort.Strings(out)
	return out, nil
}

func isXamlPath(path string) bool {
	return strings.HasSuffix(path, ".xaml") || strings.HasSuffix(path, ".axaml")
}

func isCodeOrMarkup(path string) bool {
	for _, suffix := range []string{".css", ".scss", ".html", ".tsx", ".jsx", ".vue", ".svelte", ".xaml", ".axaml"} {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return strings.HasPrefix(filepath.Base(path), "tailwind.config.")
}

func collectJSONColors(prefix string, value any, out map[string]string) {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			next := key
			if prefix != "" {
				next = prefix + "-" + key
			}
			collectJSONColors(next, typed[key], out)
		}
	case []any:
		for index, item := range typed {
			collectJSONColors(fmt.Sprintf("%s-%d", prefix, index), item, out)
		}
	case string:
		if colorPattern.MatchString(typed) {
			out[prefix] = typed
		}
	}
}

func normalizeTokenLookup(tokens map[string]string) map[string]string {
	lookup := map[string]string{}
	for key, value := range tokens {
		normalized := normalizeTokenKey(key)
		if _, exists := lookup[normalized]; !exists {
			lookup[normalized] = value
		}
	}
	return lookup
}

func normalizeTokenKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.TrimPrefix(key, "--")
	key = strings.ReplaceAll(key, "_", "-")
	key = strings.ReplaceAll(key, ".", "-")
	key = strings.ReplaceAll(key, " ", "-")
	key = strings.TrimPrefix(key, "color-")
	key = strings.TrimPrefix(key, "colors-")
	return key
}

func findToken(lookup map[string]string, aliases []string) (string, string) {
	for _, alias := range aliases {
		normalized := normalizeTokenKey(alias)
		if value, ok := lookup[normalized]; ok && value != "" {
			return normalized, value
		}
	}
	return "", ""
}

func issue(severity, code, file string, line int, message string) map[string]any {
	out := map[string]any{"severity": severity, "code": code, "message": message}
	if file != "" {
		out["file"] = file
	}
	if line > 0 {
		out["line"] = line
	}
	return out
}

func round2(value float64) float64 {
	return float64(int(value*100+0.5)) / 100
}
