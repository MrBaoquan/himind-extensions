// Package audio 用 ffmpeg 从媒体里抽出 whisper 需要的音频格式。
//
// 为什么必须走这一步：实测 whisper-cli 的 miniaudio 解码器读不了 mp4（内含 AAC 时
// 报 "1 input file(s) failed"），所以不能把视频直接丢给它。
// 统一先转成 16kHz 单声道 PCM wav —— 这也是 whisper 的原生输入格式。
package audio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrFFmpegMissing 本机没有 ffmpeg。
var ErrFFmpegMissing = errors.New(
	"未找到 ffmpeg。转写需要它把视频里的音轨抽成音频；请安装 ffmpeg 并确保它在 PATH 中")

// Locate 查找 ffmpeg。
func Locate() (string, error) {
	for _, name := range []string{"ffmpeg.exe", "ffmpeg"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", ErrFFmpegMissing
}

// ExtractOptions 抽音频参数。
type ExtractOptions struct {
	FFmpegPath string
	InputPath  string
	OutputPath string
	// SampleRate 采样率（默认 16000，whisper 要求）。
	SampleRate int
	// StartMs / EndMs 只抽一段（可选）。
	StartMs *int64
	EndMs   *int64
}

// Extract 抽出 16kHz 单声道 wav。返回输出路径。
func Extract(ctx context.Context, options ExtractOptions) (string, error) {
	if options.FFmpegPath == "" {
		return "", ErrFFmpegMissing
	}
	sampleRate := options.SampleRate
	if sampleRate <= 0 {
		sampleRate = 16000
	}
	if err := os.MkdirAll(filepath.Dir(options.OutputPath), 0o755); err != nil {
		return "", err
	}

	args := []string{"-hide_banner", "-nostdin", "-y"}
	// -ss 放在 -i 前：只抽一段时先定位再解码，避免为了取 30 秒而解完整片。
	if options.StartMs != nil && *options.StartMs > 0 {
		args = append(args, "-ss", formatSeconds(*options.StartMs))
	}
	args = append(args, "-i", options.InputPath)
	if options.EndMs != nil && options.StartMs != nil && *options.EndMs > *options.StartMs {
		args = append(args, "-t", formatSeconds(*options.EndMs-*options.StartMs))
	} else if options.EndMs != nil && options.StartMs == nil {
		args = append(args, "-t", formatSeconds(*options.EndMs))
	}
	args = append(args,
		"-vn",                            // 丢弃视频轨
		"-ac", "1",                       // 单声道
		"-ar", strconv.Itoa(sampleRate),  // 采样率
		"-c:a", "pcm_s16le",              // 无压缩 PCM
		options.OutputPath,
	)

	command := exec.CommandContext(ctx, options.FFmpegPath, args...)
	configureHiddenProcess(command)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("已被取消: %w", ctx.Err())
		}
		return "", fmt.Errorf("抽取音频失败 (exit=%d): %s", exitCodeOf(command), tail(stderr.String(), 4))
	}
	if info, err := os.Stat(options.OutputPath); err != nil || info.Size() == 0 {
		// 有些容器没有音轨，ffmpeg 仍会退出 0 但产出空文件 —— 这种情况要明说，
		// 否则后续 whisper 会报"读不到音频"，把原因指错方向。
		return "", errors.New("源文件里没有可用音轨（抽出的音频为空）")
	}
	return options.OutputPath, nil
}

func formatSeconds(milliseconds int64) string {
	return strconv.FormatFloat(float64(milliseconds)/1000.0, 'f', 3, 64)
}

func exitCodeOf(command *exec.Cmd) int {
	if command.ProcessState == nil {
		return -1
	}
	return command.ProcessState.ExitCode()
}

func tail(text string, lines int) string {
	parts := strings.Split(strings.TrimRight(text, "\r\n"), "\n")
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	joined := strings.TrimSpace(strings.Join(parts, " | "))
	if len(joined) > 600 {
		joined = joined[len(joined)-600:]
	}
	return joined
}
