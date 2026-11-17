package main

import (
	"os"
	"strings"
	"testing"
)

func TestUIContract(t *testing.T) {
	data, err := os.ReadFile("ui/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(data)
	for _, id := range []string{"pick-video", "load-frames", "title", "detail", "render-cover", "cover-preview", "frame-grid"} {
		if !strings.Contains(html, `id="`+id+`"`) {
			t.Fatalf("missing UI control %s", id)
		}
	}
	js, err := os.ReadFile("ui/app.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(js)
	if !strings.Contains(strings.ReplaceAll(script, "\r\n", "\n"), "if (!picked || picked.canceled || !file) {\n      setBusy(false);\n      setStatus('未选择视频。');") {
		t.Fatal("missing canceled file-picker reset branch")
	}
	css, err := os.ReadFile("ui/style.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(css), "[hidden]{display:none!important}") {
		t.Fatal("hidden elements must not be overridden by component display rules")
	}
	for _, capability := range []string{"media.toolkit.files.pick", "media.toolkit.probe", "media.toolkit.frames.contact-sheet", "media.toolkit.frames.extract", "media.toolkit.preview.image", "media.cover.layout.render"} {
		if !strings.Contains(script, capability) {
			t.Fatalf("missing capability integration %s", capability)
		}
	}
}
