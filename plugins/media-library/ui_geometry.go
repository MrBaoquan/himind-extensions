package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// The diagnostic endpoint accepts numeric geometry and fixed selectors only.
// It never captures text, input values, URLs, markup or browser/session state.
type geometryElement struct {
	Selector string `json:"selector"`
	Rect     struct {
		Left   float64 `json:"left"`
		Right  float64 `json:"right"`
		Top    float64 `json:"top"`
		Bottom float64 `json:"bottom"`
		Width  float64 `json:"width"`
		Height float64 `json:"height"`
	} `json:"rect"`
	Padding        geometrySides `json:"padding"`
	Border         geometrySides `json:"border"`
	ScrollbarWidth float64       `json:"scrollbar_width,omitempty"`
}
type geometrySides struct {
	Left  float64 `json:"left"`
	Right float64 `json:"right"`
}
type geometrySnapshot struct {
	Media *struct {
		Present      bool    `json:"present"`
		Paused       bool    `json:"paused"`
		CurrentTime  float64 `json:"current_time"`
		Duration     float64 `json:"duration"`
		ReadyState   int     `json:"ready_state"`
		ErrorCode    int     `json:"error_code"`
		CoverCount   int     `json:"cover_count"`
		LoadedCovers int     `json:"loaded_covers"`
	} `json:"media,omitempty"`
	Samples  []geometrySample `json:"samples,omitempty"`
	Viewport struct {
		Width       float64 `json:"width"`
		Height      float64 `json:"height"`
		DPR         float64 `json:"dpr"`
		ScrollWidth float64 `json:"scroll_width"`
	} `json:"viewport"`
	Groups []struct {
		Name     string `json:"name"`
		Elements struct {
			Container    geometryElement   `json:"container"`
			Header       geometryElement   `json:"header"`
			Body         geometryElement   `json:"body"`
			FullWidth    []geometryElement `json:"fullWidth"`
			AlignedRight []geometryElement `json:"alignedRight"`
		} `json:"elements"`
	} `json:"groups"`
}

type geometrySample struct {
	geometryElement
	FontSize       float64 `json:"font_size"`
	TextContrast   float64 `json:"text_contrast"`
	BorderContrast float64 `json:"border_contrast"`
	Focused        bool    `json:"focused"`
	ScrollWidth    float64 `json:"scroll_width"`
	ClientWidth    float64 `json:"client_width"`
}

func decodeGeometry(raw []byte) (geometrySnapshot, error) {
	var in geometrySnapshot
	if len(raw) > 32768 {
		return in, fmt.Errorf("几何报告过大")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&in); err != nil {
		return in, fmt.Errorf("几何报告只能包含控件边界数值")
	}
	if d.Decode(new(any)) != io.EOF {
		return in, fmt.Errorf("几何报告格式无效")
	}
	if in.Viewport.Width <= 0 || in.Viewport.Height <= 0 || in.Viewport.DPR <= 0 || len(in.Groups) > 8 {
		return in, fmt.Errorf("几何报告尺寸无效")
	}
	allowedSamples := map[string]bool{}
	for _, selector := range []string{"#image-editor", ".image-editor-head", ".image-editor-body", ".image-editor-footer", ".image-editor-settings", ".image-editor-save", "#image-editor-stage", "#image-editor-canvas", "#image-editor-versions", "#image-editor-apply", "#image-editor-save", "#image-editor-feedback", "#image-editor-close", "#image-editor-ai-availability", "#image-editor-generate", "#image-editor-width", "#image-editor-library", "#image-editor-prompt", "#image-editor-compare", "#image-editor-selection"} {
		allowedSamples[selector] = true
	}
	for _, selector := range []string{".generation-result-body", "#generation-help", "#generation-spec", "#generation-records", "#generation-new", "#generation-reference-list", ".generation-dialog", ".generation-picker-item", ".generation-picker-art img", "#generation-undo", "#generation-savebar", "#generation-selected", "#generation-snapshot", "#generation-record-filter"} {
		allowedSamples[selector] = true
	}
	for _, selector := range []string{"#view-generation", ".generation-head", ".generation-layout", "#generation-form", ".generation-form-fields", ".generation-form-footer", "#generation-results", "#generation-prompt", "#generation-route", "#generation-library", "#generation-history", "#generation-submit", "#generation-feedback", "#generation-output-list", "#generation-collapse", "#generation-availability", "#open-generation"} {
		allowedSamples[selector] = true
	}
	for _, selector := range []string{"#discovery-query", "#discovery-platform", "#discovery-submit", "#discovery-cancel", "#discovery-support", "#discovery-feedback", "#discovery-results", "#discovery-results > .remote-card:first-child", "#discovery-results > .remote-card:first-child h3", "#discovery-results > .remote-card:first-child .remote-card-actions", "#discovery-results > .remote-card:first-child [data-discovery-collect]", "#discovery-more", "#bookmark-results", "#bookmark-query", "#intake-url", "#intake-format", "#start-intake", "#acquisition-jobs", "#acquisition-jobs .job-message", "#legacy-downloads", "#legacy-downloads > summary", "#import-legacy-downloads", "#legacy-feedback", "#legacy-download-list", "#legacy-download-list .legacy-path"} {
		allowedSamples[selector] = true
	}
	for _, selector := range []string{"#audio-editor-summary", "#audio-title", "#audio-start", "#audio-end", "#audio-format", "#audio-library", "#audio-audition", "#audio-submit", "#audio-feedback", "#discovery-modes", "#browse-bgm"} {
		allowedSamples[selector] = true
	}
	for _, selector := range []string{"#preview-media", "#preview-media video", "#close-preview", "#retry-preview", "#remote-preview-actions", "#remote-preview-collect", "#remote-preview-audio", "#discovery-results > .remote-card:first-child .remote-video-cover", "#discovery-results > .remote-card:first-child .remote-title"} {
		allowedSamples[selector] = true
	}
	for _, selector := range []string{"#discovery-results > .remote-card:first-child .remote-card-copy", "#discovery-results > .remote-card:first-child .remote-engagement", "#discovery-results > .remote-card:first-child .remote-engagement-time", "#discovery-results > .music-card:first-child .music-usage-row .remote-engagement"} {
		allowedSamples[selector] = true
	}
	for _, selector := range []string{"#preview", ".detail-head", ".detail-content", ".preview-stage", "#preview-inspector", ".preview-inspector-tabs", ".preview-inspector-scroll", ".detail-footer", "#preview-tools", "#preview-filmstrip", "#preview-meta", "#preview-source-meta", "#asset-note", "#audio-waveform", "#audio-wave-canvas", "#audio-wave-start", "#audio-wave-end", "#preview-operation-slot", "#preview-play", "#preview-toggle-info"} {
		allowedSamples[selector] = true
	}
	for _, selector := range []string{"#preview-media .empty-state", "#preview-media .empty-state strong", "#preview-media .empty-state .meta", "#preview-time", "#audio-wave-status", "#audio-selection-duration"} {
		allowedSamples[selector] = true
	}
	for _, selector := range []string{"#preview-comments-tab", "#preview-comments-panel", ".comments-head", ".comments-actions", "#comments-status", "#comments-body", "#comments-list", ".comment-card", ".comment-text", "#comments-refresh", "#comments-save", "#comments-more"} {
		allowedSamples[selector] = true
	}
	if len(in.Samples) > len(allowedSamples) {
		return in, fmt.Errorf("几何采样过多")
	}
	for _, sample := range in.Samples {
		if !allowedSamples[sample.Selector] || sample.FontSize < 0 || sample.FontSize > 200 || sample.TextContrast < 1 || sample.TextContrast > 21.01 || sample.BorderContrast < 1 || sample.BorderContrast > 21.01 {
			return in, fmt.Errorf("未知几何采样或对比度数值")
		}
	}
	groups := map[string]string{"discovery": "#view-discovery", "bookmarks": "#view-bookmarks", "intake": ".intake-dialog", "acquisitions": ".acquisition-dialog"}
	for _, group := range in.Groups {
		if group.Name == "image-editor" {
			if group.Elements.Container.Selector != "#image-editor" || group.Elements.Header.Selector != ".image-editor-head" || group.Elements.Body.Selector != ".image-editor-footer" || len(group.Elements.FullWidth) != 1 || group.Elements.FullWidth[0].Selector != ".image-editor-save" || len(group.Elements.AlignedRight) != 0 {
				return in, fmt.Errorf("未知图片编辑几何控件")
			}
			continue
		}
		if group.Name == "generation" {
			if group.Elements.Container.Selector != "#generation-results" || group.Elements.Header.Selector != ".generation-result-head" || group.Elements.Body.Selector != "#generation-output-list" || len(group.Elements.FullWidth) != 1 || group.Elements.FullWidth[0].Selector != "#generation-output-list" || len(group.Elements.AlignedRight) != 0 {
				return in, fmt.Errorf("未知生成几何控件")
			}
			continue
		}
		if group.Name == "comments" {
			if group.Elements.Container.Selector != "#preview-comments-panel" || group.Elements.Header.Selector != ".comments-head" || group.Elements.Body.Selector != "#comments-body" || len(group.Elements.FullWidth) != 1 || group.Elements.FullWidth[0].Selector != "#comments-list" || len(group.Elements.AlignedRight) != 0 {
				return in, fmt.Errorf("未知评论几何控件")
			}
			continue
		}
		if group.Name == "preview-source" {
			if group.Elements.Container.Selector != "#preview-source-panel" || group.Elements.Header.Selector != "#preview-source-panel > h3" || group.Elements.Body.Selector != "#preview-source-meta" || len(group.Elements.FullWidth) != 1 || group.Elements.FullWidth[0].Selector != "#preview-source-meta" || len(group.Elements.AlignedRight) != 0 {
				return in, fmt.Errorf("未知来源几何控件")
			}
			continue
		}
		if group.Name == "preview" {
			if group.Elements.Container.Selector != ".detail-info" || group.Elements.Header.Selector != ".detail-info > .detail-info-head" || group.Elements.Body.Selector != "#preview-meta" || len(group.Elements.FullWidth) != 1 || group.Elements.FullWidth[0].Selector != "#preview-meta" || len(group.Elements.AlignedRight) != 0 {
				return in, fmt.Errorf("未知预览几何控件")
			}
			continue
		}
		if group.Name == "music-card" {
			base := "#discovery-results > .music-card:first-child"
			if group.Elements.Container.Selector != base || group.Elements.Header.Selector != base+" > .remote-card-copy" || group.Elements.Body.Selector != base+" > .music-usage-list" || len(group.Elements.FullWidth) != 1 || group.Elements.FullWidth[0].Selector != base+" > .music-usage-list > summary" || len(group.Elements.AlignedRight) != 0 {
				return in, fmt.Errorf("未知音乐卡片几何控件")
			}
			continue
		}
		if group.Name == "audio" {
			if group.Elements.Container.Selector != "#audio-editor" || group.Elements.Header.Selector != "#audio-editor-summary" || group.Elements.Body.Selector != "#audio-extract-form" || len(group.Elements.FullWidth) != 1 || group.Elements.FullWidth[0].Selector != "#audio-title" || len(group.Elements.AlignedRight) != 1 || group.Elements.AlignedRight[0].Selector != "#audio-submit" {
				return in, fmt.Errorf("未知音频几何控件")
			}
			continue
		}
		selector, ok := groups[group.Name]
		if !ok {
			return in, fmt.Errorf("未知几何区域")
		}
		expected := []string{selector, selector + " > .remote-head", selector + " > .remote-body"}
		for i, e := range []geometryElement{group.Elements.Container, group.Elements.Header, group.Elements.Body} {
			if e.Selector != expected[i] {
				return in, fmt.Errorf("未知几何控件")
			}
		}
		fullSelector := selector + " > .remote-body > :first-child"
		if group.Name == "acquisitions" {
			fullSelector = ".acquisition-dialog > .remote-body > .acquisition-jobs"
		}
		if len(group.Elements.FullWidth) != 1 || group.Elements.FullWidth[0].Selector != fullSelector || len(group.Elements.AlignedRight) > 1 {
			return in, fmt.Errorf("未知几何对齐控件")
		}
		for _, e := range group.Elements.AlignedRight {
			if group.Name != "intake" || e.Selector != "#start-intake" {
				return in, fmt.Errorf("未知几何操作列")
			}
		}
	}
	return in, nil
}

func uiGeometry(raw []byte, record bool) (any, error) {
	root, err := pluginDataRoot()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(root, "ui-geometry.json")
	if !record {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("请先在真实界面按 Ctrl+Shift+G 采集几何")
		}
		if err != nil {
			return nil, err
		}
		return decodeGeometry(data)
	}
	in, err := decodeGeometry(raw)
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(in)
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(root, "ui-geometry-*.tmp")
	if err != nil {
		return nil, err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(data); err != nil {
		f.Close()
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	if err = os.Rename(tmp, path); err != nil {
		return nil, err
	}
	return map[string]any{"recorded": true}, nil
}
