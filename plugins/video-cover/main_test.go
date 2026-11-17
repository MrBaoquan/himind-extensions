package main

import (
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"himind-plugin/video-cover/internal/cover"
	"himind-plugin/video-cover/internal/himindjsonrpc"
)

func TestTemplateList(t *testing.T) {
	result, rpcErr := handle(himindjsonrpc.Request{Method: capTemplateList, Params: []byte(`{}`)})
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	templates := result.(map[string]any)["templates"].([]cover.TemplateInfo)
	if len(templates) != 2 || templates[0].ID != cover.TemplateID || templates[1].ID != cover.NeutralTemplateID || templates[1].SignatureCN != "" {
		t.Fatalf("unexpected templates: %#v", templates)
	}
}

func TestNeutralCoverSupportsLandscapeAndKeepsLegacyAspectConstraint(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "frame.png")
	f, err := os.Create(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, image.NewRGBA(image.Rect(0, 0, 640, 360))); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	input := cover.RenderInput{WorkspaceRoot: root, SourceImage: sourcePath, OutputDir: "out", TemplateID: cover.NeutralTemplateID, Title: "中性封面", Detail: "无固定品牌", Outputs: []cover.OutputSpec{{Width: 640, Height: 360, Format: "png"}}}
	raw, _ := json.Marshal(input)
	result, rpcErr := handle(himindjsonrpc.Request{Method: capLayoutRender, Params: raw})
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	rendered := result.(cover.RenderResult)
	if rendered.TemplateID != cover.NeutralTemplateID || len(rendered.Files) != 1 || rendered.Files[0].Width != 640 || rendered.Files[0].Height != 360 || rendered.Quality.TitleLines < 1 {
		t.Fatalf("invalid neutral cover: %#v", rendered)
	}
	input.TemplateID = cover.TemplateID
	raw, _ = json.Marshal(input)
	if _, rpcErr = handle(himindjsonrpc.Request{Method: capLayoutRender, Params: raw}); rpcErr == nil {
		t.Fatal("branded template must retain its 3:4 constraint")
	}
}

func TestRenderProducesThreeByFourCover(t *testing.T) {
	root := t.TempDir()
	src := image.NewRGBA(image.Rect(0, 0, 900, 1600))
	for y := 0; y < 1600; y++ {
		for x := 0; x < 900; x++ {
			src.Set(x, y, color.RGBA{uint8(x * 255 / 899), uint8(y * 255 / 1599), 90, 255})
		}
	}
	sourcePath := filepath.Join(root, "source.png")
	f, err := os.Create(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if err = png.Encode(f, src); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	input := cover.RenderInput{WorkspaceRoot: root, SourceImage: sourcePath, OutputDir: "out", Filename: "demo", Title: "合肥的太阳照常升起", Detail: "但这座城市的气质，已经不一样了", Outputs: []cover.OutputSpec{{Width: 1080, Height: 1440, Format: "png"}}, Inline: false}
	raw, _ := json.Marshal(input)
	result, rpcErr := handle(himindjsonrpc.Request{Method: capLayoutRender, Params: raw})
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	rendered := result.(cover.RenderResult)
	if len(rendered.Files) != 1 {
		t.Fatalf("unexpected files: %#v", rendered.Files)
	}
	info, err := os.Stat(rendered.Files[0].Path)
	if err != nil || info.Size() == 0 {
		t.Fatalf("missing output: %v", err)
	}
	if rendered.Quality.TitleLines < 1 || rendered.Quality.DetailLines < 1 {
		t.Fatalf("bad quality: %#v", rendered.Quality)
	}
}
