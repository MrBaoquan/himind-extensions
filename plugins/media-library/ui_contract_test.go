package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func readUIAsset(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(content)
}

func between(t *testing.T, content, start, end string) string {
	t.Helper()
	startAt := strings.Index(content, start)
	if startAt < 0 {
		t.Fatalf("missing start marker %q", start)
	}
	endAt := strings.Index(content[startAt:], end)
	if endAt < 0 {
		t.Fatalf("missing end marker %q", end)
	}
	return content[startAt : startAt+endAt+len(end)]
}

func requireContains(t *testing.T, content, needle, acceptance string) {
	t.Helper()
	if !strings.Contains(content, needle) {
		t.Fatalf("%s: missing %q", acceptance, needle)
	}
}

func requireAbsent(t *testing.T, content, needle, acceptance string) {
	t.Helper()
	if strings.Contains(content, needle) {
		t.Fatalf("%s: unexpected %q", acceptance, needle)
	}
}

func countOccurrences(content, needle string) int {
	return strings.Count(content, needle)
}

func TestMediaLibraryUIAcceptanceContract(t *testing.T) {
	html := readUIAsset(t, "ui/index.html")
	css := readUIAsset(t, "ui/app.css")
	js := readUIAsset(t, "ui/app.js")
	adoption := readUIAsset(t, "adoption.go")
	resourceSearch := readUIAsset(t, "resource_search.go")
	manifest := readUIAsset(t, "plugin.json")
	main := between(t, html, `<main class="workspace">`, `</main>`)
	search := between(t, main, `<section class="search-section"`, `</section>`)
	settings := between(t, html, `<div id="settings-modal"`, `<script src="app.js"></script>`)

	t.Run("A1 configuration is absent from the main workspace", func(t *testing.T) {
		for _, forbidden := range []string{`id="library-form"`, `id="library-name"`, `id="library-root"`, `id="source-path"`, `id="providers"`, `id="sources"`, `id="target-library"`, `type="password"`, `凭据`} {
			requireAbsent(t, main, forbidden, "A1")
		}
		requireContains(t, settings, "媒体库设置", "A1")
		requireContains(t, settings, `id="library-form"`, "A1")
		requireContains(t, settings, `id="source-path"`, "A1")
		requireContains(t, settings, `id="target-library"`, "A1")
	})

	t.Run("A2 search surface is condensed", func(t *testing.T) {
		for _, control := range []string{`id="search-form"`, `id="scope-group"`, `id="kind-group"`, `id="filter-toggle"`} {
			requireContains(t, search, control, "A2")
		}
		requireContains(t, search, `id="query"`, "A2")
		requireContains(t, search, `id="search-button"`, "A2")
		requireAbsent(t, main, "provider-chip", "A2")
		requireAbsent(t, main, "常驻", "A2")
		if countOccurrences(search, `id="search-form"`) != 1 || countOccurrences(search, `id="query"`) != 1 {
			t.Fatal("A2: search form and query input must be unique")
		}
	})

	t.Run("A3 settings dialog contains the four configuration areas", func(t *testing.T) {
		if countOccurrences(html, `id="settings-modal"`) != 1 {
			t.Fatal("A3: expected exactly one settings modal")
		}
		for _, tab := range []string{"libraries", "local", "online", "index"} {
			requireContains(t, settings, `data-settings-tab="`+tab+`"`, "A3")
			requireContains(t, settings, `data-settings-panel="`+tab+`"`, "A3")
		}
		requireContains(t, settings, `role="tablist"`, "A3")
		requireContains(t, settings, `aria-controls="settings-panel-libraries"`, "A3")
		requireContains(t, settings, `aria-hidden="true"`, "A3")
	})

	t.Run("A4 results are full width and cards do not repeat primary actions", func(t *testing.T) {
		requireContains(t, main, `id="results"`, "A4")
		requireAbsent(t, main, `class="sidebar"`, "A4")
		requireContains(t, css, `.results { display: grid; grid-template-columns: repeat(auto-fill, minmax(190px, 1fr));`, "A4")
		renderResults := between(t, js, `function renderResults()`, `function toFileUrl`)
		requireContains(t, renderResults, `class="result-card"`, "A4")
		requireAbsent(t, renderResults, `class="primary`, "A4")
	})

	t.Run("A5 preview and settings implement accessible dialog behavior", func(t *testing.T) {
		requireContains(t, html, `id="preview-scrim" class="scrim"`, "A5")
		requireContains(t, html, `id="preview" class="detail-sheet" role="dialog" aria-modal="true"`, "A5")
		requireContains(t, html, `id="close-preview"`, "A5")
		requireContains(t, js, `function trapFocus(event, container)`, "A5")
		requireContains(t, js, `state.lastFocus`, "A5")
		requireContains(t, js, `document.body.classList.add('has-overlay')`, "A5")
		requireContains(t, js, `event.key === 'ArrowRight'`, "A5")
		escapeHandler := between(t, js, `document.addEventListener('keydown'`, `});`)
		settingsAt := strings.Index(escapeHandler, `if (state.settingsOpen) void closeSettings()`)
		previewAt := strings.Index(escapeHandler, `else if (state.previewOpen) closePreview()`)
		if settingsAt < 0 || previewAt < 0 || settingsAt > previewAt {
			t.Fatal("A5: settings dialog must own Escape and focus trapping before preview")
		}
	})

	t.Run("A6 visual tokens are consistent and page effects are restrained", func(t *testing.T) {
		for _, token := range []string{"--color-bg:", "--color-surface:", "--color-text:", "--color-primary:", "--space-4:", "--radius-md:", "--control-height:", "--shadow-float:"} {
			requireContains(t, css, token, "A6")
		}
		for _, forbidden := range []string{"linear-gradient", "radial-gradient", "text-shadow", "drop-shadow"} {
			requireAbsent(t, css, forbidden, "A6")
		}
		shadowPattern := regexp.MustCompile(`box-shadow:\s*([^;]+);`)
		for _, match := range shadowPattern.FindAllStringSubmatch(css, -1) {
			value := strings.TrimSpace(match[1])
			if strings.Contains(value, "var(--shadow-float)") || strings.HasPrefix(value, "0 1px 2px rgba(") {
				continue
			}
			t.Fatalf("A6: unexpected shadow value %q", value)
		}
	})

	t.Run("A7 responsive coverage exists", func(t *testing.T) {
		for _, breakpoint := range []string{"max-width: 980px", "max-width: 680px", "max-width: 420px"} {
			requireContains(t, css, breakpoint, "A7")
		}
		requireContains(t, css, "overflow: hidden", "A7")
		requireContains(t, css, ".detail-sheet { inset: 8px; width: auto; }", "A7")
	})

	t.Run("A8 user-visible lifecycle states exist", func(t *testing.T) {
		for _, state := range []string{"正在搜索", "正在加载预览", "没有匹配结果", "部分来源不可用", "当前离线", "已添加到资源库", "未重复复制", "SMB", "redistribution-notice"} {
			requireContains(t, js+html, state, "A8")
		}
		requireContains(t, js, `class="skeleton"`, "A8")
		requireContains(t, html, `id="settings-feedback"`, "A8")
		requireContains(t, html, `id="preview-feedback"`, "A8")
	})
	t.Run("A11 audio cards support inline playback", func(t *testing.T) {
		requireContains(t, js, "data-play-index=", "A11")
		requireContains(t, js, "function toggleCardAudio(index)", "A11")
		requireContains(t, js, "new Audio()", "A11")
		requireContains(t, js, "card-audio-progress", "A11")
		requireContains(t, js, "aria-pressed", "A11")
		requireContains(t, css, ".card-audio-playback", "A11")
		requireContains(t, css, "pointer-events: none", "A11")
		requireContains(t, js, "item.kind === 'audio' ? (item.download_url || item.preview_url)", "A11")
		requireContains(t, js, "result-waveform", "A11")
	})

	t.Run("A12 preview supports structured details and sequential browsing", func(t *testing.T) {
		for _, id := range []string{"preview-badges", "preview-position", "previous-preview", "next-preview", "detail-info-title"} {
			requireContains(t, html, id, "A12")
		}
		requireContains(t, js, "function navigatePreview(delta)", "A12")
		requireContains(t, js, "event.key === 'ArrowLeft'", "A12")
		requireContains(t, js, "previewLinkMarkup", "A12")
		requireContains(t, css, ".detail-content { display: grid; flex: 1 1 auto;", "A12")
	})

	t.Run("A13 search stays available while scrolling", func(t *testing.T) {
		requireContains(t, css, ".search-section { position: sticky; top: 56px; z-index: 9;", "A13")
	})

	t.Run("A14 SMB adoption uses explicit one-click confirmation", func(t *testing.T) {
		requireAbsent(t, html, "redistribution-confirm", "A14")
		requireContains(t, html, "redistribution-notice", "A14")
		requireContains(t, js, "确认并添加到 SMB 共享库", "A14")
		requireContains(t, js, "许可证不明确时请勿入库", "A14")
	})

	t.Run("A15 adoption supports rename, collection and tags with searchable metadata", func(t *testing.T) {
		for _, id := range []string{"adopt-name", "adopt-collection", "adopt-tags"} {
			requireContains(t, html, id, "A15")
		}
		requireContains(t, js, "parseTagList", "A15")
		requireContains(t, js, "normalizeCollection", "A15")
		requireContains(t, adoption, "Collection            string", "A15")
		requireContains(t, adoption, "\"collection\": collection", "A15")
		requireContains(t, resourceSearch, "Collection   string", "A15")
		requireContains(t, manifest, "\"collection\"", "A15")
	})

	t.Run("Scale A7 cursor pagination UI", func(t *testing.T) {
		requireContains(t, html, "id=\"load-more\"", "A7")
		requireContains(t, js, "function loadMoreResults()", "A7")
		requireContains(t, js, "state.nextCursor", "A7")
		requireContains(t, manifest, "\"cursor\"", "A7")
	})

	t.Run("Native desktop affordances", func(t *testing.T) {
		requireContains(t, html, `id="search-button" class="primary icon-button search-submit" aria-label="搜索"`, "Native")
		requireContains(t, html, `id="open-settings" class="icon-button" aria-label="媒体库设置"`, "Native")
		requireContains(t, html, `id="refresh" class="icon-button" aria-label="刷新"`, "Native")
		requireContains(t, html, `id="adopt-library"`, "Native")
		requireContains(t, js, "document.addEventListener('contextmenu'", "Native")
		requireContains(t, js, "event.preventDefault()", "Native")
	})

	t.Run("Resource management and inline filters", func(t *testing.T) {
		for _, id := range []string{"library-form-title", "source-edit-form", "source-display-name"} {
			requireContains(t, html, id, "Management")
		}
		for _, control := range []string{"data-edit-library", "data-remove-library", "data-edit-source", "data-remove-source"} {
			requireContains(t, js, control, "Management")
		}
		for _, capability := range []string{"media-library.repository.update", "media-library.repository.remove", "media-library.source.update", "media-library.source.remove"} {
			requireContains(t, manifest, capability, "Management")
		}
		requireContains(t, html, "class=\"filter-panel\"", "Filter")
		requireContains(t, css, ".filter-panel { margin-top:", "Filter")
		requireAbsent(t, css, ".filter-popover { position: absolute", "Filter")
	})

	t.Run("Audio playback uses distinct play and pause SVG icons", func(t *testing.T) {
		requireContains(t, js, "class=\"play-icon\"", "Audio")
		requireContains(t, js, "class=\"pause-icon\"", "Audio")
		requireContains(t, css, ".result-play.playing .pause-icon { display: block; }", "Audio")
		requireAbsent(t, js, "play-glyph", "Audio")
	})

	t.Run("Destructive actions use an in-app confirmation dialog", func(t *testing.T) {
		requireContains(t, html, "id=\"confirm-modal\"", "Confirm")
		requireContains(t, html, "role=\"alertdialog\"", "Confirm")
		requireContains(t, js, "function showConfirm(options)", "Confirm")
		requireContains(t, js, "function resolveConfirm(value)", "Confirm")
		requireAbsent(t, js, "window.confirm", "Confirm")
	})

}
