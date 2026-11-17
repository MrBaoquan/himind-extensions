package main

import (
	"strings"
	"testing"
)

func TestGeometryDiagnosticRejectsTextAndUnknownSelectors(t *testing.T) {
	valid := `{"viewport":{"width":1280,"height":800,"dpr":2,"scroll_width":1280},"groups":[]}`
	if _, err := decodeGeometry([]byte(valid)); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		strings.Replace(valid, `"groups":[]`, `"groups":[],"url":"https://example.com/private"`, 1),
		strings.Replace(valid, `"groups":[]`, `"groups":[{"name":"private-title"}]`, 1),
		valid + `{}`,
		strings.Replace(valid, `"groups":[]`, `"groups":[{"name":"intake","elements":{"container":{"selector":"input[value]"}}}]`, 1),
	} {
		if _, err := decodeGeometry([]byte(raw)); err == nil {
			t.Fatal("accepted nonnumeric diagnostic data")
		}
	}
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	if _, err := uiGeometry([]byte(valid), true); err != nil {
		t.Fatal(err)
	}
	if _, err := uiGeometry(nil, false); err != nil {
		t.Fatal(err)
	}
}

func TestCommentGeometryKeepsFixedSelectors(t *testing.T) {
	valid := `{"viewport":{"width":1280,"height":800,"dpr":2,"scroll_width":1280},"groups":[{"name":"comments","elements":{"container":{"selector":"#preview-comments-panel"},"header":{"selector":".comments-head"},"body":{"selector":"#comments-body"},"fullWidth":[{"selector":"#comments-list"}]}}],"samples":[{"selector":".comment-text","font_size":13,"text_contrast":7,"border_contrast":1}]}`
	if _, err := decodeGeometry([]byte(valid)); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		strings.Replace(valid, `"selector":"#comments-list"`, `"selector":".private-comment"`, 1),
		strings.Replace(valid, `"selector":".comment-text"`, `"selector":"input[value]"`, 1),
	} {
		if _, err := decodeGeometry([]byte(raw)); err == nil {
			t.Fatal("accepted unknown comment selector")
		}
	}
}
