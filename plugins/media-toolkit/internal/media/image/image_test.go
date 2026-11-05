package image

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"himind-plugin/media-toolkit/internal/media/ffmpeg"
	"himind-plugin/media-toolkit/internal/media/probe"
)

func TestPlanOutputsUsesSafeBatchNames(t *testing.T) {
	outputs, err := PlanOutputs(
		[]string{`C:\work\a.png`, `C:\work\b.jpg`},
		`C:\work\out`,
		Options{Format: "webp", Suffix: "-web"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(outputs) != 2 || !strings.HasSuffix(outputs[0], "a-web.webp") || !strings.HasSuffix(outputs[1], "b-web.webp") {
		t.Fatalf("批量命名不符: %#v", outputs)
	}
}

func TestPlanOutputsSupportsTemplate(t *testing.T) {
	outputs, err := PlanOutputs(
		[]string{`C:\work\hero.png`},
		`C:\work\out`,
		Options{Format: "jpeg", NameTemplate: "{index}-{name}.{ext}"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(outputs[0]) != "1-hero.jpg" {
		t.Fatalf("模板命名不符: %s", outputs[0])
	}
}

func TestPlanOutputsRejectsTraversalAndCollisions(t *testing.T) {
	if _, err := PlanOutputs([]string{"a.png"}, ".", Options{
		Format: "png", NameTemplate: "../{name}.png",
	}); err == nil {
		t.Fatal("路径穿越式命名应被拒绝")
	}
	if _, err := PlanOutputs([]string{"a.png", "b.png"}, ".", Options{
		Format: "png", NameTemplate: "same.png",
	}); err == nil {
		t.Fatal("重复输出名应被拒绝")
	}
	if _, err := PlanOutputs([]string{"a.png"}, ".", Options{
		Format: "png", NameTemplate: "{name}.webp",
	}); err == nil {
		t.Fatal("扩展名与目标格式不一致应被拒绝")
	}
}

func TestFiltersAndQualityAreTyped(t *testing.T) {
	background, err := normalizeBackground("#12abEF")
	if err != nil {
		t.Fatal(err)
	}
	filter := filtersFor(Options{Width: 1280, Height: 720}, background)
	if !strings.Contains(filter, "scale=1280:720:force_original_aspect_ratio=decrease") ||
		!strings.Contains(filter, "pad=1280:720") || !strings.Contains(filter, "color=0x12ABEF") {
		t.Fatalf("缩放/留白滤镜不符: %s", filter)
	}
	if args := qualityArgs("jpeg", 90); len(args) != 2 || args[0] != "-q:v" {
		t.Fatalf("JPEG 质量映射不符: %#v", args)
	}
	if _, err := normalizeBackground("red"); err == nil {
		t.Fatal("非十六进制背景色应被拒绝")
	}
}

func requireImageTools(t *testing.T) ffmpeg.Tools {
	t.Helper()
	tools, err := ffmpeg.Locate()
	if err != nil {
		t.Skipf("本机没有成对 ffmpeg/ffprobe: %v", err)
	}
	return tools
}

func makeImage(t *testing.T, tools ffmpeg.Tools, path, color string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := tools.Run(ctx, ffmpeg.RunOptions{Args: []string{
		"-f", "lavfi", "-i", "color=c=" + color + ":s=320x180",
		"-frames:v", "1", path,
	}})
	if err != nil {
		t.Skipf("无法合成图片测试素材: %v", err)
	}
}

func TestIntegrationBatchConvertsAndResizes(t *testing.T) {
	tools := requireImageTools(t)
	dir := t.TempDir()
	first := filepath.Join(dir, "red.png")
	second := filepath.Join(dir, "blue.png")
	makeImage(t, tools, first, "red")
	makeImage(t, tools, second, "blue")
	outputDir := filepath.Join(dir, "outputs")

	result, err := ConvertBatch(context.Background(), tools, []string{first, second}, outputDir, Options{
		Format: "jpeg", Width: 160, Height: 120, Quality: 88, Background: "#00ff00",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.Count != 2 || len(result.Failed) != 0 {
		t.Fatalf("批量转换失败: %+v", result)
	}
	for _, output := range result.Outputs {
		if output.SizeBytes <= 0 {
			t.Fatalf("产物为空: %+v", output)
		}
		info, err := probe.Read(context.Background(), tools, output.Output)
		if err != nil {
			t.Fatal(err)
		}
		if info.Video == nil || info.Video.Width != 160 || info.Video.Height != 120 {
			t.Fatalf("输出尺寸不符: %+v", info.Video)
		}
		if stat, err := os.Stat(output.Output); err != nil || stat.Size() == 0 {
			t.Fatalf("产物缺失: %v", err)
		}
	}
}
