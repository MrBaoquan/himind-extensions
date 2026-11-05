package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestViewHasExactlyFourPrimaryMediaTasks(t *testing.T) {
	payload, err := os.ReadFile("ui/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(payload)
	for _, tab := range []string{"image", "video", "images", "analyze"} {
		if !strings.Contains(html, `data-tab="`+tab+`"`) {
			t.Fatalf("缺少任务页 %s", tab)
		}
	}
	matches := regexp.MustCompile(`data-tab="[^"]+"`).FindAllString(html, -1)
	if len(matches) != 4 {
		t.Fatalf("主任务应恰好为 4 个，实际 %d: %#v", len(matches), matches)
	}
}

func TestViewUsesHighLevelCapabilities(t *testing.T) {
	payload, err := os.ReadFile("ui/app.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(payload)
	for _, capability := range []string{
		"media.toolkit.image.convert",
		"media.toolkit.video.convert",
		"media.toolkit.images.to-video",
		"media.toolkit.media.analyze",
		"media.toolkit.job.status",
		"media.toolkit.job.cancel",
	} {
		if !strings.Contains(script, capability) {
			t.Fatalf("视图没有调用 %s", capability)
		}
	}
}

func TestViewScriptIdsExistInHtml(t *testing.T) {
	htmlPayload, err := os.ReadFile("ui/index.html")
	if err != nil {
		t.Fatal(err)
	}
	scriptPayload, err := os.ReadFile("ui/app.js")
	if err != nil {
		t.Fatal(err)
	}
	html := string(htmlPayload)
	script := string(scriptPayload)
	idPattern := regexp.MustCompile(`id="([^"]+)"`)
	ids := make(map[string]bool)
	for _, match := range idPattern.FindAllStringSubmatch(html, -1) {
		ids[match[1]] = true
	}
	refPattern := regexp.MustCompile(`el\('([^']+)'\)`)
	for _, match := range refPattern.FindAllStringSubmatch(script, -1) {
		if !ids[match[1]] {
			t.Fatalf("脚本引用了不存在的元素 id: %s", match[1])
		}
	}
}

func TestVideoInputOmitsEmptyOptionalNumbers(t *testing.T) {
	payload, err := os.ReadFile("ui/app.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(payload)
	for _, contract := range []string{
		"if (fps > 0) input.fps = fps;",
		"if (videoBitrate > 0) input.video_bitrate_kbps = videoBitrate;",
		"else if (quality.crf > 0) input.crf = quality.crf;",
	} {
		if !strings.Contains(script, contract) {
			t.Fatalf("视频转换必须省略未填写或不适用的可选数字: %s", contract)
		}
	}
	if strings.Contains(script, "fps: positiveNumber('video-fps'),") {
		t.Fatal("视频转换仍在无条件发送 fps")
	}
}

func TestHiddenAttributeAlwaysWinsOverGridLayout(t *testing.T) {
	payload, err := os.ReadFile("ui/style.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), "[hidden] { display: none !important; }") {
		t.Fatal("缺少 hidden 强制隐藏规则，grid 组件会在未勾选时错误显示")
	}
}

func TestCancelButtonHidesAfterTerminalJob(t *testing.T) {
	payload, err := os.ReadFile("ui/app.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), "el('cancel-button').hidden = terminal(job.status);") {
		t.Fatal("终态作业仍可能显示取消按钮")
	}
}

func TestViewCopyIsCompactAndPurposeful(t *testing.T) {
	payload, err := os.ReadFile("ui/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(payload)
	for _, verbose := range []string{
		"先选素材，再选任务",
		"常用选项已经外显",
		"返回归一化的容器",
		"图片转换、视频转换、图片转视频和媒体参数分析。",
	} {
		if strings.Contains(html, verbose) {
			t.Fatalf("界面仍包含冗长说明: %q", verbose)
		}
	}
	if strings.Contains(html, `class="hint"`) {
		t.Fatal("主界面不应继续堆叠 hint 文案")
	}
}

func TestViewUsesFlatTwoColumnWorkspace(t *testing.T) {
	htmlPayload, err := os.ReadFile("ui/index.html")
	if err != nil {
		t.Fatal(err)
	}
	cssPayload, err := os.ReadFile("ui/style.css")
	if err != nil {
		t.Fatal(err)
	}
	html := string(htmlPayload)
	css := string(cssPayload)
	if !strings.Contains(html, `class="layout"`) || !strings.Contains(html, `class="side-column"`) {
		t.Fatal("缺少双栏工作台结构")
	}
	if strings.Contains(css, "linear-gradient") || strings.Contains(css, "box-shadow") {
		t.Fatal("现代化扁平界面不应使用渐变或卡片阴影")
	}
	for _, contract := range []string{
		"grid-template-columns: minmax(0, 1fr) 310px",
		"--subtle: #667085",
		"--line-strong: #8a94a6",
		"outline: 2px solid var(--accent)",
	} {
		if !strings.Contains(css, contract) {
			t.Fatalf("缺少扁平工作台或无障碍约束: %s", contract)
		}
	}
}

func TestRuntimeWarningsStayCompact(t *testing.T) {
	payload, err := os.ReadFile("ui/app.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(payload)
	if !strings.Contains(script, "badge.title = (runtime.runtime_warnings || []).join('\\n');") {
		t.Fatal("运行时风险应放入紧凑提示，不占用主界面横幅")
	}
}

func TestDefaultMediaPresetsPreserveSourceAndBalancedQuality(t *testing.T) {
	payload, err := os.ReadFile("ui/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(payload)
	for _, id := range []string{"image-size-preset", "video-size-preset", "images-size-preset"} {
		block := htmlSection(t, html, `id="`+id+`"`, "</select>")
		if !strings.Contains(block, `<option value="source" selected>原尺寸</option>`) {
			t.Fatalf("%s 默认应为原尺寸，避免自动放大素材", id)
		}
	}
	for _, id := range []string{"image-quality-preset", "video-quality-preset"} {
		block := htmlSection(t, html, `id="`+id+`"`, "</select>")
		if !strings.Contains(block, `<option value="balanced" selected>标准</option>`) {
			t.Fatalf("%s 默认应为标准质量", id)
		}
	}
}

func TestPrimaryFormsKeepProfessionalParametersCollapsed(t *testing.T) {
	payload, err := os.ReadFile("ui/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(payload)
	cases := []struct {
		panel    string
		advanced string
		fields   []string
	}{
		{"image", "image-advanced", []string{"image-width", "image-height", "image-quality", "image-name-template", "image-overwrite"}},
		{"video", "video-advanced", []string{"video-codec", "video-fps", "video-crf", "video-bitrate", "video-preset", "video-out"}},
		{"images", "images-advanced", []string{"images-fps", "images-crf", "images-width", "images-height", "images-out"}},
	}
	for _, tc := range cases {
		panel := panelSection(t, html, tc.panel)
		advancedAt := strings.Index(panel, `id="`+tc.advanced+`"`)
		if advancedAt < 0 {
			t.Fatalf("%s 缺少折叠的高级设置", tc.panel)
		}
		primary := panel[:advancedAt]
		advanced := panel[advancedAt:]
		for _, field := range tc.fields {
			if strings.Contains(primary, `id="`+field+`"`) {
				t.Fatalf("%s 专业参数 %s 不应出现在首屏", tc.panel, field)
			}
			if !strings.Contains(advanced, `id="`+field+`"`) {
				t.Fatalf("%s 专业参数 %s 未放入更多设置", tc.panel, field)
			}
		}
	}
}

func TestPresetChangesDriveBackendInputs(t *testing.T) {
	payload, err := os.ReadFile("ui/app.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(payload)
	for _, contract := range []string{
		"function imageDimensions()",
		"function videoDimensions()",
		"function imagesDimensions()",
		"high: 92, balanced: 82, small: 65",
		"high: { crf: 18, speed: 'slow' }",
		"balanced: { crf: 23, speed: 'medium' }",
		"small: { crf: 28, speed: 'slow' }",
		"const dimensions = requireCustomDimensions('image-size-preset', imageDimensions());",
		"const dimensions = requireCustomDimensions('video-size-preset', videoDimensions());",
		"const dimensions = requireCustomDimensions('images-size-preset', imagesDimensions());",
		"Math.round(positiveNumber('images-duration') * 1000)",
	} {
		if !strings.Contains(script, contract) {
			t.Fatalf("预设未接入真实处理参数: %s", contract)
		}
	}
	for _, contract := range []string{
		"chooseCustom('image-size-preset', 'image-advanced')",
		"chooseCustom('video-quality-preset', 'video-advanced')",
		"chooseCustom('images-size-preset', 'images-advanced')",
	} {
		if !strings.Contains(script, contract) {
			t.Fatalf("手工修改专业参数时应切换自定义并展开高级设置: %s", contract)
		}
	}
}

func panelSection(t *testing.T, html, panel string) string {
	t.Helper()
	start := strings.Index(html, `data-panel="`+panel+`"`)
	if start < 0 {
		t.Fatalf("找不到任务面板: %s", panel)
	}
	end := strings.Index(html[start+1:], `<div class="panel"`)
	if end >= 0 {
		return html[start : start+1+end]
	}
	actions := strings.Index(html[start:], `<div class="actions">`)
	if actions < 0 {
		t.Fatalf("找不到任务面板结束位置: %s", panel)
	}
	return html[start : start+actions]
}

func htmlSection(t *testing.T, html, start, end string) string {
	t.Helper()
	from := strings.Index(html, start)
	if from < 0 {
		t.Fatalf("找不到起始标记: %s", start)
	}
	to := strings.Index(html[from:], end)
	if to < 0 {
		t.Fatalf("找不到结束标记 %s（起始 %s）", end, start)
	}
	return html[from : from+to+len(end)]
}

func TestAdvancedCheckRowsStayAligned(t *testing.T) {
	payload, err := os.ReadFile("ui/style.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(payload)
	for _, contract := range []string{
		".check-row { display: flex; align-items: center;",
		".check-row input { width: 16px; height: 16px;",
	} {
		if !strings.Contains(css, contract) {
			t.Fatalf("高级设置复选框缺少对齐样式: %s", contract)
		}
	}
}

func TestViewUsesNativePickerInsteadOfWorkspaceDropdown(t *testing.T) {
	htmlPayload, err := os.ReadFile("ui/index.html")
	if err != nil {
		t.Fatal(err)
	}
	scriptPayload, err := os.ReadFile("ui/app.js")
	if err != nil {
		t.Fatal(err)
	}
	html := string(htmlPayload)
	script := string(scriptPayload)
	for _, removed := range []string{`id="file-select"`, `id="image-files"`, "从列表选择"} {
		if strings.Contains(html, removed) {
			t.Fatalf("素材区不应再呈现工作区全量列表: %s", removed)
		}
	}
	for _, removed := range []string{"media.toolkit.files.list", "loadFiles()", "renderFileOptions()"} {
		if strings.Contains(script, removed) {
			t.Fatalf("视图不应再加载或渲染工作区全量素材: %s", removed)
		}
	}
	for _, required := range []string{
		`id="pick-media"`,
		`id="selection-list"`,
		`id="pick-media-more"`,
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("缺少原生选择与已选素材结构: %s", required)
		}
	}
	for _, required := range []string{
		"media.toolkit.files.pick",
		"function renderSelection()",
		"data-remove-path",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("缺少素材选择交互: %s", required)
		}
	}
	if strings.Contains(script, "if (currentSelection().length === 1) void probe();") {
		t.Fatal("选择素材后不应自动展开参数表，避免主任务被挤出视野")
	}
	for _, removed := range []string{`id="manual-path"`, `id="media-path"`, `id="probe-button"`, `id="media-info"`} {
		if strings.Contains(html, removed) {
			t.Fatalf("单一拖拽/点击素材区之外不应保留额外选择或手动路径 UI: %s", removed)
		}
	}
	for _, required := range []string{
		"bindSelectionDropTarget()",
		"registerTauriDragEvents()",
		"tauri://drag-drop",
		"paths: paths",
		"kind: 'Window'",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("缺少拖拽素材交互: %s", required)
		}
	}
}

func TestWorkspaceBoundaryIsInternalNotUserFacing(t *testing.T) {
	htmlPayload, err := os.ReadFile("ui/index.html")
	if err != nil {
		t.Fatal(err)
	}
	scriptPayload, err := os.ReadFile("ui/app.js")
	if err != nil {
		t.Fatal(err)
	}
	html := string(htmlPayload)
	script := string(scriptPayload)
	for _, removed := range []string{"workspace-root", "工作区"} {
		if strings.Contains(html, removed) {
			t.Fatalf("工作区是内部安全边界，不应出现在用户界面: %s", removed)
		}
	}
	if strings.Contains(script, "el('workspace-root')") {
		t.Fatal("脚本不应再向用户展示工作区路径")
	}
}
