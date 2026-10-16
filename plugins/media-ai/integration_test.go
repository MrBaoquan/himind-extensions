package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"himind-plugin/media-ai/internal/media/audio"
	"himind-plugin/media-ai/internal/media/plugindata"
	"himind-plugin/media-ai/internal/media/runtime"
	"himind-plugin/media-ai/internal/media/whisper"
)

// 集成测试会真的下载运行时（二进制约 21MB + 模型 75MB 起）并实际执行转写。
// 默认跳过 —— 单元测试不该顺带下几百 MB。
//
//	MEDIA_AI_INTEGRATION=1 go test -run Integration -v ./...
//
// 它证明的是"这条链路真的能跑"：获取运行时 → 抽音频 → 运行 whisper → 解析结果。
// 这几步各自都可能因为环境问题失败（网络、编解码、线程），而单元测试覆盖不到。
func TestIntegrationInstallAndTranscribe(t *testing.T) {
	if os.Getenv("MEDIA_AI_INTEGRATION") == "" {
		t.Skip("需要 MEDIA_AI_INTEGRATION=1（会下载运行时与模型）")
	}
	ffmpegPath, err := audio.Locate()
	if err != nil {
		t.Skipf("本机没有 ffmpeg，跳过: %v", err)
	}

	lock, err := runtime.ReadLock(runtime.EmbeddedLock())
	if err != nil {
		t.Fatal(err)
	}
	paths, err := runtime.ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}

	// 1) 获取运行时。用最小的模型，缩短验证时间。
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()
	result, err := runtime.Install(ctx, lock, paths, runtime.InstallOptions{Models: []string{"tiny"}})
	if err != nil {
		t.Fatalf("获取运行时失败: %v", err)
	}
	if !result.BinaryInstalled {
		t.Fatal("应装上二进制")
	}
	if _, err := os.Stat(result.BinaryPath); err != nil {
		t.Fatalf("二进制路径不可用: %v", err)
	}
	t.Logf("运行时就绪: %s（模型 %v）", result.BinaryPath, result.InstalledModels)

	// 2) 造一段音频并抽成 whisper 需要的格式。
	workDir := t.TempDir()
	sourcePath := filepath.Join(workDir, "source.mp4")
	buildSyntheticMedia(t, ffmpegPath, sourcePath)

	audioPath, err := audio.Extract(ctx, audio.ExtractOptions{
		FFmpegPath: ffmpegPath,
		InputPath:  sourcePath,
		OutputPath: filepath.Join(workDir, "audio.wav"),
	})
	if err != nil {
		t.Fatalf("抽音频失败: %v", err)
	}
	if info, err := os.Stat(audioPath); err != nil || info.Size() == 0 {
		t.Fatalf("音频产物无效: %v", err)
	}

	// 3) 运行 whisper。单线程 —— 多线程在该环境会段错误。
	modelPath := filepath.Join(paths.ModelsDir, "ggml-tiny.bin")
	transcribeCtx, cancelTranscribe := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancelTranscribe()
	transcription, err := whisper.Run(transcribeCtx, result.BinaryPath, whisper.Options{
		AudioPath: audioPath,
		ModelPath: modelPath,
		Language:  "en",
		OutputDir: workDir,
		Threads:   1,
	})
	if err != nil {
		// 合成音频是纯音，识别不出内容属正常；此时 ErrNoSpeech 是"链路通了"的证据。
		if err == whisper.ErrNoSpeech {
			t.Log("链路跑通（合成音频无语音，得到 ErrNoSpeech，符合预期）")
			return
		}
		t.Fatalf("转写失败: %v", err)
	}
	t.Logf("转写完成: 语言=%s 段落=%d 耗时=%dms", transcription.Language, len(transcription.Segments), transcription.ElapsedMs)
}

// buildSyntheticMedia 用 ffmpeg 造一段带音轨的测试素材（不依赖外部文件）。
func buildSyntheticMedia(t *testing.T, ffmpegPath, target string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, ffmpegPath,
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=4:size=160x120:rate=15",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=4",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-shortest", target)
	if output, err := command.CombinedOutput(); err != nil {
		t.Skipf("无法合成测试素材: %v %s", err, strings.TrimSpace(string(output)))
	}
}

// 插件数据目录必须可解析，否则作业与运行时会无处落盘。
func TestPluginDataRootResolvable(t *testing.T) {
	root, err := plugindata.Sub("jobs")
	if err != nil {
		t.Fatalf("插件数据目录不可用: %v", err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("目录应已创建: %v", err)
	}
}
