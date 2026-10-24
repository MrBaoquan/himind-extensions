package design

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
)

type Record map[string]string

type DomainSpec struct {
	File         string
	SearchFields []string
	Identity     string
	Label        string
}

var DomainSpecs = map[string]DomainSpec{
	"product":      {File: "data/products.csv", Identity: "Product Type", Label: "产品类型", SearchFields: []string{"Product Type", "Keywords", "Primary Style Recommendation", "Key Considerations"}},
	"style":        {File: "data/styles.csv", Identity: "Style Category", Label: "界面风格", SearchFields: []string{"Style Category", "Aliases", "Keywords", "Best For", "Type", "AI Prompt Keywords"}},
	"color":        {File: "data/colors.csv", Identity: "Product Type", Label: "配色", SearchFields: []string{"Product Type", "Notes", "Primary", "Secondary", "Accent"}},
	"typography":   {File: "data/typography.csv", Identity: "Font Pairing Name", Label: "字体搭配", SearchFields: []string{"Font Pairing Name", "Category", "Mood/Style Keywords", "Best For", "Heading Font", "Body Font"}},
	"landing":      {File: "data/landing.csv", Identity: "Pattern Name", Label: "落地页结构", SearchFields: []string{"Pattern Name", "Keywords", "Section Order", "Conversion Optimization", "Primary CTA Placement"}},
	"chart":        {File: "data/charts.csv", Identity: "Data Type", Label: "图表", SearchFields: []string{"Data Type", "Keywords", "Best Chart Type", "When to Use", "When NOT to Use", "Accessibility Notes"}},
	"ux":           {File: "data/ux-guidelines.csv", Identity: "Issue", Label: "UX 规范", SearchFields: []string{"Category", "Issue", "Platform", "Description", "Do", "Don't"}},
	"icons":        {File: "data/icons.csv", Identity: "Icon Name", Label: "图标", SearchFields: []string{"Category", "Icon Name", "Keywords", "Best For", "Semantic Role", "Allowed Contexts"}},
	"gsap":         {File: "data/motion.csv", Identity: "Category", Label: "动效", SearchFields: []string{"Category", "Intensity Tier", "Keywords", "Trigger", "Do", "Don't", "Performance Notes"}},
	"react":        {File: "data/react-performance.csv", Identity: "Issue", Label: "React 性能", SearchFields: []string{"Category", "Issue", "Keywords", "Description", "Do", "Don't"}},
	"web":          {File: "data/app-interface.csv", Identity: "Issue", Label: "应用界面", SearchFields: []string{"Category", "Issue", "Keywords", "Description", "Do", "Don't"}},
	"google-fonts": {File: "data/google-fonts.csv", Identity: "Family", Label: "Google Fonts", SearchFields: []string{"Family", "Category", "Stroke", "Classifications", "Keywords", "Subsets", "Designers"}},
	"reasoning":    {File: "data/ui-reasoning.csv", Identity: "UI_Category", Label: "设计推理", SearchFields: []string{"UI_Category", "Recommended_Pattern", "Style_Priority", "Color_Mood", "Typography_Mood", "Key_Effects", "Anti_Patterns"}},
}

const StackDirectory = "data/stacks"

var StackSearchFields = []string{"Category", "Guideline", "Description", "Do", "Don't", "Code Good", "Code Bad"}

type Catalog struct {
	fsys       fs.FS
	mu         sync.RWMutex
	rows       map[string][]Record
	indexes    map[string]*Index
	stackRows  map[string][]Record
	stackIndex map[string]*Index
	meta       map[string]any
	provenance map[string]any
}

func NewCatalog(fsys fs.FS) *Catalog {
	return &Catalog{
		fsys:       fsys,
		rows:       map[string][]Record{},
		indexes:    map[string]*Index{},
		stackRows:  map[string][]Record{},
		stackIndex: map[string]*Index{},
	}
}

func (c *Catalog) Domains() []string {
	out := make([]string, 0, len(DomainSpecs))
	for name := range DomainSpecs {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (c *Catalog) StackNames() []string {
	entries, err := fs.ReadDir(c.fsys, StackDirectory)
	if err != nil {
		return nil
	}
	out := []string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".csv") {
			continue
		}
		out = append(out, strings.TrimSuffix(entry.Name(), ".csv"))
	}
	sort.Strings(out)
	return out
}

func (c *Catalog) Rows(domain string) ([]Record, error) {
	spec, ok := DomainSpecs[domain]
	if !ok {
		return nil, fmt.Errorf("不支持的检索域：%s", domain)
	}
	c.mu.RLock()
	rows, ok := c.rows[domain]
	c.mu.RUnlock()
	if ok {
		return rows, nil
	}
	loaded, err := readCSV(c.fsys, spec.File)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.rows[domain] = loaded
	c.mu.Unlock()
	return loaded, nil
}

func (c *Catalog) Index(domain string) (*Index, error) {
	spec, ok := DomainSpecs[domain]
	if !ok {
		return nil, fmt.Errorf("不支持的检索域：%s", domain)
	}
	c.mu.RLock()
	index, ok := c.indexes[domain]
	c.mu.RUnlock()
	if ok {
		return index, nil
	}
	rows, err := c.Rows(domain)
	if err != nil {
		return nil, err
	}
	index = BuildIndex(rows, spec.SearchFields, spec.Identity)
	c.mu.Lock()
	c.indexes[domain] = index
	c.mu.Unlock()
	return index, nil
}

func (c *Catalog) StackRows(stack string) ([]Record, error) {
	stack = strings.ToLower(strings.TrimSpace(stack))
	if stack == "" {
		return nil, fmt.Errorf("技术栈不能为空")
	}
	c.mu.RLock()
	rows, ok := c.stackRows[stack]
	c.mu.RUnlock()
	if ok {
		return rows, nil
	}
	loaded, err := readCSV(c.fsys, path.Join(StackDirectory, stack+".csv"))
	if err != nil {
		return nil, fmt.Errorf("不支持的技术栈：%s", stack)
	}
	c.mu.Lock()
	c.stackRows[stack] = loaded
	c.mu.Unlock()
	return loaded, nil
}

func (c *Catalog) StackIndex(stack string) (*Index, error) {
	stack = strings.ToLower(strings.TrimSpace(stack))
	if stack == "" {
		return nil, fmt.Errorf("技术栈不能为空")
	}
	c.mu.RLock()
	index, ok := c.stackIndex[stack]
	c.mu.RUnlock()
	if ok {
		return index, nil
	}
	rows, err := c.StackRows(stack)
	if err != nil {
		return nil, err
	}
	index = BuildIndex(rows, StackSearchFields, "Guideline")
	c.mu.Lock()
	c.stackIndex[stack] = index
	c.mu.Unlock()
	return index, nil
}

func (c *Catalog) Describe() map[string]any {
	domains := []map[string]any{}
	for _, name := range c.Domains() {
		spec := DomainSpecs[name]
		count := 0
		if rows, err := c.Rows(name); err == nil {
			count = len(rows)
		}
		domains = append(domains, map[string]any{"name": name, "label": spec.Label, "file": spec.File, "rows": count})
	}
	stacks := c.StackNames()
	provenance := c.provenanceSummary()
	meta := c.upstreamMeta()
	return map[string]any{
		"status":          "ok",
		"source":          meta,
		"domains":         domains,
		"stacks":          stacks,
		"stack_count":     len(stacks),
		"provenance":      provenance,
		"runtime_network": false,
		"notes": []string{
			"设计建议来自固定的离线数据快照，运行时不会访问上游仓库、Google Fonts 或 npm。",
			"数据是启发式设计知识，不替代品牌规范、WCAG 实测或真实界面审查。",
		},
	}
}

func (c *Catalog) upstreamMeta() map[string]any {
	c.mu.RLock()
	if c.meta != nil {
		defer c.mu.RUnlock()
		return c.meta
	}
	c.mu.RUnlock()
	meta := map[string]any{
		"project": "UI UX Pro Max",
		"version": "2.15.0",
		"commit":  "d14efff8ed1faa4187f004f79926f428c8074ec7",
		"license": "MIT",
		"url":     "https://github.com/nextlevelbuilder/ui-ux-pro-max-skill",
	}
	data, err := fs.ReadFile(c.fsys, "data/upstream.json")
	if err == nil {
		_ = json.Unmarshal(data, &meta)
	}
	c.mu.Lock()
	c.meta = meta
	c.mu.Unlock()
	return meta
}

func (c *Catalog) provenanceSummary() map[string]any {
	c.mu.RLock()
	if c.provenance != nil {
		defer c.mu.RUnlock()
		return c.provenance
	}
	c.mu.RUnlock()
	summary := map[string]any{"file": "data/data-provenance.json", "records": 0}
	data, err := fs.ReadFile(c.fsys, "data/data-provenance.json")
	if err == nil {
		var doc struct {
			SchemaVersion int              `json:"schemaVersion"`
			GeneratedAt   string           `json:"generatedAt"`
			Records       []map[string]any `json:"records"`
		}
		if json.Unmarshal(data, &doc) == nil {
			summary = map[string]any{
				"file":           "data/data-provenance.json",
				"schema_version": doc.SchemaVersion,
				"generated_at":   doc.GeneratedAt,
				"records":        len(doc.Records),
			}
		}
	}
	c.mu.Lock()
	c.provenance = summary
	c.mu.Unlock()
	return summary
}

func readCSV(fsys fs.FS, name string) ([]Record, error) {
	file, err := fsys.Open(name)
	if err != nil {
		return nil, fmt.Errorf("读取数据文件 %s 失败：%w", name, err)
	}
	defer file.Close()
	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("读取 %s 表头失败：%w", name, err)
	}
	for i := range header {
		header[i] = strings.TrimSpace(strings.TrimPrefix(header[i], "\ufeff"))
	}
	out := []Record{}
	for {
		values, err := reader.Read()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("读取 %s 失败：%w", name, err)
		}
		row := Record{}
		for i, key := range header {
			if i < len(values) {
				row[key] = values[i]
			} else {
				row[key] = ""
			}
		}
		out = append(out, row)
	}
	return out, nil
}
