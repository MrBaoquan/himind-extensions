package design

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

type Index struct {
	Docs []Document
	IDF  map[string]float64
}

type Document struct {
	Record      Record
	Tokens      map[string]int
	FieldTokens map[string]map[string]int
	Text        string
	Identity    string
}

type SearchHit struct {
	Identity string            `json:"identity"`
	Score    float64           `json:"score"`
	Matched  []string          `json:"matched_fields"`
	Fields   map[string]string `json:"fields"`
}

type SearchResult struct {
	Status        string         `json:"status"`
	Domain        string         `json:"domain,omitempty"`
	Stack         string         `json:"stack,omitempty"`
	Query         string         `json:"query"`
	ExpandedQuery string         `json:"expanded_query,omitempty"`
	Count         int            `json:"count"`
	Matches       []SearchHit    `json:"matches"`
	Source        map[string]any `json:"source"`
}

func BuildIndex(rows []Record, fields []string, identityField string) *Index {
	docs := make([]Document, 0, len(rows))
	df := map[string]int{}
	for _, row := range rows {
		fieldTokens := map[string]map[string]int{}
		all := map[string]int{}
		var textParts []string
		for _, field := range fields {
			value := row[field]
			textParts = append(textParts, value)
			tokens := Tokenize(value)
			fieldTokens[field] = tokens
			for token, count := range tokens {
				all[token] += count
			}
		}
		for token := range all {
			df[token]++
		}
		docs = append(docs, Document{
			Record:      row,
			Tokens:      all,
			FieldTokens: fieldTokens,
			Text:        strings.ToLower(strings.Join(textParts, " ")),
			Identity:    row[identityField],
		})
	}
	idf := map[string]float64{}
	n := float64(len(docs))
	for token, count := range df {
		idf[token] = math.Log(1 + (n-float64(count)+0.5)/(float64(count)+0.5))
	}
	return &Index{Docs: docs, IDF: idf}
}

var searchStopWords = map[string]bool{"a": true, "an": true, "and": true, "are": true, "as": true, "at": true, "be": true, "by": true, "do": true, "for": true, "from": true, "in": true, "is": true, "it": true, "no": true, "not": true, "of": true, "on": true, "or": true, "that": true, "the": true, "this": true, "to": true, "use": true, "using": true, "with": true}

func Tokenize(value string) map[string]int {
	out := map[string]int{}
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		token := strings.ToLower(b.String())
		if len([]rune(token)) > 1 || unicode.IsDigit([]rune(token)[0]) {
			out[token]++
		}
		b.Reset()
	}
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

func Search(index *Index, rawQuery string, top int) SearchHitList {
	expanded := ExpandQuery(rawQuery)
	queryTokens := Tokenize(expanded)
	if top <= 0 {
		top = 5
	}
	if top > 20 {
		top = 20
	}
	queryLower := strings.ToLower(expanded)
	hits := []SearchHit{}
	for _, doc := range index.Docs {
		score := 0.0
		matched := map[string]bool{}
		for token := range queryTokens {
			idf, ok := index.IDF[token]
			if !ok {
				continue
			}
			for field, tokens := range doc.FieldTokens {
				tf := tokens[token]
				if tf == 0 {
					continue
				}
				score += idf * (float64(tf) / (float64(tf) + 1.2)) * fieldWeight(field)
				matched[field] = true
			}
		}
		if score == 0 {
			continue
		}
		if queryLower != "" && strings.Contains(doc.Text, queryLower) {
			score += 4
		}
		identityLower := strings.ToLower(doc.Identity)
		for token := range queryTokens {
			if token != "" && strings.Contains(identityLower, token) {
				score += 1.5
				break
			}
		}
		fields := make([]string, 0, len(doc.Record))
		for key, value := range doc.Record {
			fields = append(fields, key)
			_ = value
		}
		sort.Strings(fields)
		fieldsMap := map[string]string{}
		for _, key := range fields {
			fieldsMap[key] = doc.Record[key]
		}
		matchedFields := make([]string, 0, len(matched))
		for field := range matched {
			matchedFields = append(matchedFields, field)
		}
		sort.Strings(matchedFields)
		hits = append(hits, SearchHit{Identity: doc.Identity, Score: math.Round(score*100) / 100, Matched: matchedFields, Fields: fieldsMap})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			return hits[i].Identity < hits[j].Identity
		}
		return hits[i].Score > hits[j].Score
	})
	if len(hits) > top {
		hits = hits[:top]
	}
	return SearchHitList(hits)
}

type SearchHitList []SearchHit

func fieldWeight(field string) float64 {
	lower := strings.ToLower(field)
	switch {
	case strings.Contains(lower, "category") || strings.Contains(lower, "name") || lower == "issue" || lower == "family":
		return 3
	case strings.Contains(lower, "keyword") || strings.Contains(lower, "best for") || strings.Contains(lower, "description"):
		return 2
	default:
		return 1
	}
}

func (c *Catalog) Search(query, domain, stack string, top int) (SearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return SearchResult{}, errInvalid("检索词不能为空")
	}
	if len([]rune(query)) > 500 {
		return SearchResult{}, errInvalid("检索词不能超过 500 个字符")
	}
	if domain != "" && stack != "" {
		return SearchResult{}, errInvalid("domain 与 stack 只能选择一个")
	}
	expanded := ExpandQuery(query)
	if stack == "" && domain == "" {
		domain = DetectDomain(expanded)
	}
	source := c.upstreamMeta()
	if stack != "" {
		stack = strings.ToLower(strings.TrimSpace(stack))
		index, err := c.StackIndex(stack)
		if err != nil {
			return SearchResult{}, err
		}
		hits := Search(index, query, top)
		status := "ok"
		if len(hits) == 0 {
			status = "no_match"
		}
		return SearchResult{Status: status, Stack: stack, Query: query, ExpandedQuery: expanded, Count: len(hits), Matches: hits, Source: source}, nil
	}
	index, err := c.Index(domain)
	if err != nil {
		return SearchResult{}, err
	}
	hits := Search(index, query, top)
	status := "ok"
	if len(hits) == 0 {
		status = "no_match"
	}
	return SearchResult{Status: status, Domain: domain, Query: query, ExpandedQuery: expanded, Count: len(hits), Matches: hits, Source: source}, nil
}

type invalidError string

func (e invalidError) Error() string  { return string(e) }
func errInvalid(message string) error { return invalidError(message) }

func DetectDomain(query string) string {
	lower := strings.ToLower(query)
	switch {
	case containsAny(lower, "chart", "graph", "data visualization", "图表", "可视化"):
		return "chart"
	case containsAny(lower, "font", "typography", "typeface", "字体", "排版"):
		return "typography"
	case containsAny(lower, "color", "palette", "contrast", "配色", "颜色", "对比度"):
		return "color"
	case containsAny(lower, "icon", "图标", "logo"):
		return "icons"
	case containsAny(lower, "react performance", "rerender", "memo", "bundle", "性能"):
		return "react"
	case containsAny(lower, "react native", "ios", "android", "mobile app", "应用界面", "移动端", "原生"):
		return "web"
	case containsAny(lower, "accessibility", "a11y", "keyboard", "focus", "usability", "无障碍", "键盘", "焦点", "可用性"):
		return "ux"
	case containsAny(lower, "animation", "motion", "gsap", "动效", "动画"):
		return "gsap"
	case containsAny(lower, "landing", "hero", "cta", "落地页", "首屏"):
		return "landing"
	case containsAny(lower, "style", "minimal", "brutalism", "glassmorphism", "bento", "风格", "极简", "玻璃", "粗野"):
		return "style"
	default:
		return "product"
	}
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
