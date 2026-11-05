package main

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

// 本文件把前端的关键约定锁进测试：这些行为没法在 Go 侧跑起来（界面由 Agent 的
// WebView 渲染），但一旦有人改坏，用户的手工验收成本很高，所以用静态断言兜住。
func readUIAssetForTest(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", name, err)
	}
	return string(content)
}

func requireContainsForTest(t *testing.T, content, needle, acceptance string) {
	t.Helper()
	if !strings.Contains(content, needle) {
		t.Fatalf("%s: 缺少 %q", acceptance, needle)
	}
}

// 前端资源带 ?v= 版本号，用于让 WebView 在改版后重新拉取。
// 版本号必须跟 plugin.json 一致，否则改了前端却仍加载旧脚本。
func TestUIAssetVersionsMatchPluginVersion(t *testing.T) {
	html := readUIAssetForTest(t, "ui/index.html")
	var manifest struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(readUIAssetForTest(t, "plugin.json")), &manifest); err != nil {
		t.Fatalf("plugin.json 解析失败: %v", err)
	}
	if manifest.Version == "" {
		t.Fatal("plugin.json 缺少 version")
	}
	pattern := regexp.MustCompile(`\?v=([0-9]+\.[0-9]+\.[0-9]+)`)
	matches := pattern.FindAllStringSubmatch(html, -1)
	if len(matches) != 2 {
		t.Fatalf("index.html 应有 app.css 与 app.js 两处版本后缀，实际 %d 处", len(matches))
	}
	for _, match := range matches {
		if match[1] != manifest.Version {
			t.Fatalf("前端资源版本 %s 与 plugin.json 的 %s 不一致", match[1], manifest.Version)
		}
	}
}

// 季度起算月要能在报告页设置，并且首屏先读回口径再渲染。
func TestUIFiscalStartMonthSetting(t *testing.T) {
	html := readUIAssetForTest(t, "ui/index.html")
	js := readUIAssetForTest(t, "ui/app.js")
	css := readUIAssetForTest(t, "ui/app.css")

	requireContainsForTest(t, html, `id="report-fiscal"`, "起算月设置区")
	requireContainsForTest(t, html, `id="fiscal-start-month"`, "起算月下拉框")
	requireContainsForTest(t, html, `id="fiscal-rule"`, "季度规则说明")
	requireContainsForTest(t, css, ".report-fiscal", "起算月设置区样式")

	for _, needle := range []string{
		"function renderFiscalSetting()",
		"function loadPreferences()",
		"function saveFiscalStartMonth(month)",
		"work-log.preferences.get",
		"work-log.preferences.save",
		"fiscal_year_start_month: month",
	} {
		requireContainsForTest(t, js, needle, "起算月前端逻辑")
	}
	// 首屏必须先读口径，否则报告区间会先按自然季度画一遍再跳变。
	boot := js[strings.LastIndex(js, "bind();"):]
	if !strings.Contains(boot, "loadPreferences();") {
		t.Fatal("启动流程缺少 loadPreferences()，首屏可能先按自然季度渲染")
	}
	if strings.Index(boot, "loadPreferences();") > strings.Index(boot, "refresh();") {
		t.Fatal("loadPreferences() 必须在 refresh() 之前调用")
	}
}

// 前端季度计算必须与后端同一口径：起算月 2 时 Q1 = 2-4 月，且 1 月属于上一财年 Q4。
func TestUIQuarterRangeMatchesFiscalRule(t *testing.T) {
	js := readUIAssetForTest(t, "ui/app.js")
	for _, needle := range []string{
		"function fiscalYearStart(date)",
		"function fiscalQuarterStart(date)",
		"month: month,",
	} {
		requireContainsForTest(t, js, needle, "财年区间计算")
	}
	// 季度末必须用起始月推导，历史上曾漏掉 month 字段导致结束日变成 NaN。
	requireContainsForTest(t, js, "const end = new Date(quarter.year, quarter.month, 1);", "季度结束日计算")
}

// 工时表按行复制：行头要有复制入口，剪贴板要同时写 text/plain 与 text/html，
// 否则粘到 Excel 只会得到一格或整段文本。
func TestUIMatrixRowCopyForExcel(t *testing.T) {
	html := readUIAssetForTest(t, "ui/index.html")
	js := readUIAssetForTest(t, "ui/app.js")
	css := readUIAssetForTest(t, "ui/app.css")

	requireContainsForTest(t, html, `id="mx-copy-all"`, "复制整表按钮")
	requireContainsForTest(t, js, "function copyTableToClipboard(rows, message)", "表格剪贴板写入")
	requireContainsForTest(t, js, `'text/plain'`, "剪贴板纯文本（制表符分隔）")
	requireContainsForTest(t, js, `'text/html'`, "剪贴板 HTML 表格")
	requireContainsForTest(t, js, "new ClipboardItem(", "多格式剪贴板写入")
	requireContainsForTest(t, js, "function copyMatrixPart(kind, rowIndex)", "按行复制")
	requireContainsForTest(t, js, `data-mx-copy="row"`, "行复制入口")
	requireContainsForTest(t, js, `data-mx-copy="head"`, "表头复制入口")
	requireContainsForTest(t, js, `data-mx-copy="total"`, "合计行复制入口")
	requireContainsForTest(t, css, ".matrix .mx-copy", "复制按钮样式")

	// 单元格点击会跳到日历视图；复制按钮必须 stopPropagation，否则一点就跳走。
	if !strings.Contains(js, "event.stopPropagation();") {
		t.Fatal("复制按钮缺少 stopPropagation，点击会连带触发单元格跳转")
	}
	// 复制内容用制表符分列，Excel 才拆列。
	requireContainsForTest(t, js, `cells.join('\t')`, "TSV 分列")
	requireContainsForTest(t, js, "state.matrixTable = {", "矩阵纯文本副本")

	// 复制内容只含日期列：项目列与合计列常由使用方的工时模板提供（合计往往还是公式），
	// 一起粘过去会和模板已有列错位，或把公式覆盖成死值。
	requireContainsForTest(t, js, "const tableHead = dayCells((day) => day + '日');", "表头只含日期列")
	requireContainsForTest(t, js, "rowNames: matrix.rows.map((row) => row.name)", "项目名单独存放供提示文案")
	requireNotContainsForTest(t, js, "['项目'].concat(", "表头不应再拼「项目」列")
	requireNotContainsForTest(t, js, "['当天合计']", "合计行不应再拼「当天合计」标签")
	requireNotContainsForTest(t, js, ".concat([total > 0 ? hoursShort(total) : ''])", "行尾不应再拼合计列")
	requireNotContainsForTest(t, js, ".concat([hoursShort(grand) || ''])", "合计行尾不应再拼总计列")
}

// requireNotContainsForTest 断言前端源码里不再出现某段构造，防止「只复制日期列」被回退。
func requireNotContainsForTest(t *testing.T, source, forbidden, label string) {
	t.Helper()
	if strings.Contains(source, forbidden) {
		t.Fatalf("%s：源码里仍有 %q", label, forbidden)
	}
}

// 键盘可达性：裁切容器内的聚焦环要用 inset 内环（外侧 outline 会被 overflow:hidden 切掉），
// 页签要 roving tabindex + 方向键，工时表单元格要能 Tab 进入并用 Enter/Space 触发。
// 这几条都是「鼠标能用、键盘不能用」的典型，回归时容易悄悄丢掉。
func TestUIKeyboardAccessibilityContract(t *testing.T) {
	js := readUIAssetForTest(t, "ui/app.js")
	css := readUIAssetForTest(t, "ui/app.css")

	// 裁切容器内的子控件：inset 内环，且不能留着外侧 outline。
	requireContainsForTest(t, css, ".view-switch .switch:focus-visible,\n.seg .seg-btn:focus-visible {", "裁切容器内控件的焦点环")
	requireContainsForTest(t, css, "box-shadow: inset 0 0 0 2px var(--accent);", "inset 焦点环")
	requireContainsForTest(t, css, ".view-switch .switch.active:focus-visible,", "激活项焦点环单独处理")
	requireContainsForTest(t, css, "box-shadow: inset 0 0 0 2px #fff;", "激活项白色内环")

	// 页签：roving tabindex 与方向键。
	requireContainsForTest(t, js, "node.tabIndex = on ? 0 : -1;", "页签 roving tabindex")
	requireContainsForTest(t, js, "'ArrowLeft', 'ArrowRight', 'Home', 'End'", "页签方向键")

	// 工时表单元格：可聚焦、有角色与名称、Enter/Space 等价于点击。
	requireContainsForTest(t, js, `'" tabindex="0" role="button" aria-label="'`, "单元格可聚焦与命名")
	requireContainsForTest(t, js, "if (event.key !== 'Enter' && event.key !== ' ') return;", "单元格键盘触发")

	// 报告分节标题用 h2，避免 h1 直接跳 h3。
	requireContainsForTest(t, js, `<h2>工作类型分布</h2>`, "报告分节标题为 h2")
	requireNotContainsForTest(t, js, `<h3>工作类型分布</h3>`, "报告分节标题不应回退到 h3")
	requireContainsForTest(t, css, ".report-section > h2 {", "报告分节标题样式")
}
