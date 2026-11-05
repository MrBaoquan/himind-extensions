package imagevideo

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"himind-plugin/media-toolkit/internal/media/ffmpeg"
	"himind-plugin/media-toolkit/internal/media/probe"
	"himind-plugin/media-toolkit/internal/media/recipe"
)

func TestInspectSourceFindsSequenceAndEstimatesDuration(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"shot_0001.png", "shot_0002.png", "shot_0003.png"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	source, err := InspectSource(filepath.Join(dir, "shot_%04d.png"), 15)
	if err != nil {
		t.Fatal(err)
	}
	if !source.IsSequence || source.FrameCount != 3 || source.DurationMs != 200 {
		t.Fatalf("序列识别错误: %+v", source)
	}
}

func TestBuildSingleImageLoopsAndAttachAudioAddsShortest(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "still.png")
	if err := os.WriteFile(imagePath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	source, err := InspectSource(imagePath, 30)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Build(source, filepath.Join(dir, "out.mp4"), Options{
		FPS: 30, DurationMs: 2000, Width: 640, Height: 360, CRF: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan = AttachAudio(plan, filepath.Join(dir, "audio.wav"))
	args, err := recipe.Compile(plan.Recipe)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-loop 1") || !strings.Contains(joined, "-shortest") || !strings.Contains(joined, "-c:a aac") {
		t.Fatalf("单图/音轨参数不完整: %s", joined)
	}
	if !strings.Contains(joined, "-t 2.000") {
		t.Fatalf("单图循环必须设置输出时长，否则 ffmpeg 不会自行结束: %s", joined)
	}
}

func TestBuildRejectsUnsafeDimensions(t *testing.T) {
	source := Source{Path: "a.png"}
	for _, options := range []Options{{Width: 641}, {Width: -2}, {FPS: 240}, {CRF: 60}} {
		if _, err := Build(source, "out.mp4", options); err == nil {
			t.Fatalf("应拒绝非法参数: %+v", options)
		}
	}
}

func TestIntegrationSequenceToVideoWithAudio(t *testing.T) {
	tools, err := ffmpeg.Locate()
	if err != nil {
		t.Skipf("本机没有成对 ffmpeg/ffprobe: %v", err)
	}
	dir := t.TempDir()
	for index, color := range []string{"red", "blue"} {
		path := filepath.Join(dir, "shot_"+string(rune('1'+index))+".png")
		_, runErr := tools.Run(context.Background(), ffmpeg.RunOptions{Args: []string{
			"-f", "lavfi", "-i", "color=c=" + color + ":s=160x90", "-frames:v", "1", path,
		}})
		if runErr != nil {
			t.Skipf("无法合成测试图片: %v", runErr)
		}
	}
	audio := filepath.Join(dir, "tone.wav")
	if _, err := tools.Run(context.Background(), ffmpeg.RunOptions{Args: []string{
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2", audio,
	}}); err != nil {
		t.Skipf("无法合成测试音频: %v", err)
	}
	source, err := InspectSource(filepath.Join(dir, "shot_%d.png"), 2)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "from-images.mp4")
	plan, err := Build(source, output, Options{FPS: 2, Width: 160, Height: 90, CRF: 25})
	if err != nil {
		t.Fatal(err)
	}
	plan = AttachAudio(plan, audio)
	args, err := recipe.Compile(plan.Recipe)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := tools.Run(ctx, ffmpeg.RunOptions{Args: args}); err != nil {
		t.Fatal(err)
	}
	info, err := probe.Read(ctx, tools, output)
	if err != nil {
		t.Fatal(err)
	}
	if info.Video == nil || info.Video.Width != 160 || !info.HasAudio {
		t.Fatalf("图片转视频产物异常: %+v", info)
	}
}

func TestBuildMKVDoesNotSetFastStart(t *testing.T) {
	source := Source{Path: "a.png"}
	plan, err := Build(source, "out.mkv", Options{FPS: 30, DurationMs: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Recipe.Output.FastStart {
		t.Fatal("MKV 不应设置 MP4 的 faststart")
	}
	if _, err := InspectSource("not-an-image.txt", 30); err == nil {
		t.Fatal("非图片扩展名应被拒绝")
	}
}
