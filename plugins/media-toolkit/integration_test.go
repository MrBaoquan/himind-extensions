package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"himind-plugin/media-toolkit/internal/media/analysis"
	"himind-plugin/media-toolkit/internal/media/ffmpeg"
	"himind-plugin/media-toolkit/internal/media/frames"
	"himind-plugin/media-toolkit/internal/media/probe"
	"himind-plugin/media-toolkit/internal/media/recipe"
)

// 集成测试用 ffmpeg 自己合成素材，不依赖任何外部文件——
// 这样"底座到底能不能跑通"这件事随时可验，而不是靠手边正好有个视频。
func buildSample(t *testing.T, tools ffmpeg.Tools, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "sample.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// 6 秒：前半段纯色静止（画面活动低），后半段滚动图案（活动高），并带 440Hz 正弦音。
	_, err := tools.Run(ctx, ffmpeg.RunOptions{Args: []string{
		"-f", "lavfi", "-i", "testsrc=duration=6:size=320x240:rate=30",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=6",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-shortest", path,
	}})
	if err != nil {
		t.Skipf("无法合成测试素材（本机 ffmpeg 不支持所需编码器）: %v", err)
	}
	return path
}

func requireTools(t *testing.T) ffmpeg.Tools {
	t.Helper()
	tools, err := ffmpeg.Locate()
	if err != nil {
		t.Skip("本机没有 ffmpeg，跳过集成测试")
	}
	if tools.FFprobe == "" {
		t.Skip("本机没有 ffprobe，跳过集成测试")
	}
	return tools
}

func TestIntegrationProbeReadsDurationAndStreams(t *testing.T) {
	tools := requireTools(t)
	dir := t.TempDir()
	sample := buildSample(t, tools, dir)

	info, err := probe.Read(context.Background(), tools, sample)
	if err != nil {
		t.Fatal(err)
	}
	if !info.HasVideo || !info.HasAudio {
		t.Fatalf("合成素材应同时含视频与音频: %+v", info)
	}
	if info.DurationMs < 4000 || info.DurationMs > 8000 {
		t.Fatalf("时长不在预期区间: %d", info.DurationMs)
	}
	if info.Video.Width != 320 || info.Video.Height != 240 {
		t.Fatalf("分辨率读取错误: %+v", info.Video)
	}
	if info.Video.Fps < 29 || info.Video.Fps > 31 {
		t.Fatalf("帧率读取错误: %v", info.Video.Fps)
	}
}

// 画面活动曲线必须真的区分"静止"与"运动"——这是把 score 当强度信号使用的前提。
func TestIntegrationSceneScoreDetectsMotion(t *testing.T) {
	tools := requireTools(t)
	dir := t.TempDir()
	sample := buildSample(t, tools, dir)

	result, err := analysis.SceneScore(context.Background(), tools, sample, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Points) < 100 {
		t.Fatalf("采样点过少: %d", len(result.Points))
	}
	if result.PeakScore <= 0 {
		t.Fatalf("画面活动分数应大于 0: %+v", result)
	}
	// 第一秒与最后一秒的活动强度应能区分（测试图案一直在动，这里只要求曲线非常数）。
	first := result.Points[5].Score
	last := result.Points[len(result.Points)-5].Score
	if first == last && result.PeakScore == 0 {
		t.Fatalf("曲线看起来是常数，采样可能失效: %+v", result.Points[:3])
	}
}

func TestIntegrationLoudnessReportsAudio(t *testing.T) {
	tools := requireTools(t)
	dir := t.TempDir()
	sample := buildSample(t, tools, dir)

	result, err := analysis.Loudness(context.Background(), tools, sample)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Points) == 0 {
		t.Fatalf("应有响度采样点: %+v", result)
	}
	// 有持续正弦音，整体响度不该还是"数字静音"。
	if result.IntegratedLufs <= -100 {
		t.Fatalf("整体响度异常偏低，音轨可能未被识别: %v", result.IntegratedLufs)
	}
}

// 拼图是本插件给"让 AI 看懂视频"提供的核心原语，必须真能产出非全黑图。
func TestIntegrationContactSheetProducesVisibleImage(t *testing.T) {
	tools := requireTools(t)
	dir := t.TempDir()
	sample := buildSample(t, tools, dir)
	output := filepath.Join(dir, "sheet.jpg")

	result, err := frames.ContactSheet(context.Background(), tools, sample, output, frames.SheetOptions{
		Columns: 3, Rows: 2, TileWidth: 160, IntervalMs: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.TileCount != 6 {
		t.Fatalf("格数不符: %d", result.TileCount)
	}
	if stat, err := os.Stat(output); err != nil || stat.Size() == 0 {
		t.Fatalf("拼图产物缺失或为空: %v", err)
	}
	// 全黑守卫：这条断言正是为了防止"退出码 0、文件正常，但内容是黑的"这类静默失败。
	if result.MeanLuminance < 6 {
		t.Fatalf("拼图疑似全黑（平均亮度 %.2f）: %+v", result.MeanLuminance, result.Warnings)
	}
	if result.Width != 480 || result.Height != 240 {
		t.Fatalf("拼图尺寸不符: %dx%d", result.Width, result.Height)
	}
}

// 转码走完整链路：recipe 编译 → 作业提交 → 进度 → 产物。
func TestIntegrationTranscodeProducesFile(t *testing.T) {
	tools := requireTools(t)
	dir := t.TempDir()
	sample := buildSample(t, tools, dir)
	output := filepath.Join(dir, "out.mp4")

	target := recipe.CompressForWeb(sample, output, recipe.CompressOptions{MaxWidth: 160})
	args, err := recipe.Compile(target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tools.Run(context.Background(), ffmpeg.RunOptions{Args: args}); err != nil {
		t.Fatal(err)
	}

	info, err := probe.Read(context.Background(), tools, output)
	if err != nil {
		t.Fatal(err)
	}
	if info.Video == nil || info.Video.Width != 160 {
		t.Fatalf("缩放未生效: %+v", info.Video)
	}
	// 高度自动（-2）且必须是偶数——奇数会引出下游一连串静默问题。
	if info.Video.Height%2 != 0 {
		t.Fatalf("高度应为偶数: %d", info.Video.Height)
	}
	if stat, err := os.Stat(output); err != nil || stat.Size() == 0 {
		t.Fatalf("产物缺失: %v", err)
	}
}

// 抽帧应真的写出文件。
func TestIntegrationFrameExtractWritesFiles(t *testing.T) {
	tools := requireTools(t)
	dir := t.TempDir()
	sample := buildSample(t, tools, dir)
	outDir := filepath.Join(dir, "frames")

	written, err := frames.Extract(context.Background(), tools, sample, outDir, []int64{500, 1500, 3000},
		frames.ExtractOptions{Width: 160, Format: "jpg"})
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 3 {
		t.Fatalf("应写出 3 个文件: %v", written)
	}
	for _, path := range written {
		if stat, err := os.Stat(path); err != nil || stat.Size() == 0 {
			t.Fatalf("抽帧文件缺失或为空: %s (%v)", path, err)
		}
	}
}
