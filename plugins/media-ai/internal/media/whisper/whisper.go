// Package whisper 执行 whisper-cli 并把结果解析成结构化转写。
package whisper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Segment 一段带时间戳的文本。
type Segment struct {
	StartMs int64  `json:"start_ms"`
	EndMs   int64  `json:"end_ms"`
	Text    string `json:"text"`
}

// Result 一次转写的结果。
type Result struct {
	Language string    `json:"language"`
	Model    string    `json:"model,omitempty"`
	Segments []Segment `json:"segments"`
	// FullText 所有段落拼起来的文本（便于直接读）。
	FullText string `json:"full_text"`
	// VocalRatio 有人声的时间占比 —— 一眼看出"这段片子有没有词"。
	VocalRatio float64 `json:"vocal_ratio"`
	// ElapsedMs 转写耗时。
	ElapsedMs int64 `json:"elapsed_ms"`
}

// ErrNoSpeech 整段没有识别出任何内容。
var ErrNoSpeech = errors.New("没有识别出任何语音或歌词")

// Options 一次转写的参数。
type Options struct {
	// AudioPath 16kHz 单声道 wav（必须先由 ffmpeg 抽出）。
	AudioPath string
	// ModelPath ggml 模型文件。
	ModelPath string
	// Language 语言代码（zh / en / ja…）。**不要留空用自动检测**：
	// 实测自动检测会把中文歌曲判成 en，结果只得到 "(singing in foreign language)" 这类占位文本，
	// 拿不到真正的词。显式指定语言是拿到可用结果的前提。
	Language string
	// OutputDir 中间产物目录（whisper 会在此写 json）。
	OutputDir string
	// Threads 线程数。**保持为 1**：实测多线程在该环境下会段错误崩溃，
	// 单线程可稳定完成（226 秒音频约 95 秒）。
	Threads int
	// Translate 译为英文（默认转写原文）。
	Translate bool
	// OnProgress 进度回调（whisper 把进度写到 stderr，按行回传）。
	OnProgress func(line string)
}

// Run 调用 whisper-cli 转写。
//
// 刻意不解析 whisper 的进度百分比：它的 stderr 是给人看的、格式随版本变。
// 这里只把 stderr 原样回传（供作业日志），结果一律从它写出的 JSON 读取 ——
// 那是稳定的结构化契约。
func Run(ctx context.Context, binaryPath string, options Options) (Result, error) {
	if binaryPath == "" {
		return Result{}, errors.New("whisper 二进制未就绪")
	}
	if options.ModelPath == "" {
		return Result{}, errors.New("模型路径为空")
	}
	if _, err := os.Stat(options.AudioPath); err != nil {
		return Result{}, fmt.Errorf("音频文件不可读: %w", err)
	}

	outBase := filepath.Join(options.OutputDir, "transcript")
	threads := options.Threads
	if threads <= 0 {
		threads = 1
	}
	language := strings.TrimSpace(options.Language)
	if language == "" {
		language = "auto"
	}

	args := []string{
		"-m", options.ModelPath,
		"-f", options.AudioPath,
		"-l", language,
		"-t", strconv.Itoa(threads),
		"-oj",             // 结构化 JSON，程序读它
		"-of", outBase,    // 输出文件名（不含扩展名）
		"--no-prints",     // 抑制计时等信息噪音
	}
	if options.Translate {
		args = append(args, "-tr")
	}

	started := time.Now()
	command := exec.CommandContext(ctx, binaryPath, args...)
	configureHiddenProcess(command)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if options.OnProgress != nil {
		command.Stdout = &lineWriter{forward: options.OnProgress}
	}
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return Result{}, fmt.Errorf("已被取消: %w", ctx.Err())
		}
		return Result{}, fmt.Errorf("whisper 失败 (exit=%d): %s", exitCodeOf(command), tail(stderr.String(), 6))
	}

	payload, err := os.ReadFile(outBase + ".json")
	if err != nil {
		return Result{}, fmt.Errorf("未产出转写结果: %w", err)
	}
	result, err := Parse(payload)
	if err != nil {
		return Result{}, err
	}
	result.ElapsedMs = time.Since(started).Milliseconds()
	return result, nil
}

// whisperJSON 是 whisper-cli -oj 的输出结构（只取需要的字段）。
type whisperJSON struct {
	Result struct {
		Language string `json:"language"`
	} `json:"result"`
	Model struct {
		Type string `json:"type"`
	} `json:"model"`
	Transcription []struct {
		Offsets struct {
			From int64 `json:"from"`
			To   int64 `json:"to"`
		} `json:"offsets"`
		Text string `json:"text"`
	} `json:"transcription"`
}

// Parse 解析 whisper 的 JSON 输出。
func Parse(payload []byte) (Result, error) {
	// 有的版本会带 BOM；不清掉 json 直接报错。
	payload = bytes.TrimPrefix(payload, []byte{0xEF, 0xBB, 0xBF})

	var raw whisperJSON
	if err := json.Unmarshal(payload, &raw); err != nil {
		return Result{}, fmt.Errorf("解析转写结果失败: %w", err)
	}

	result := Result{
		Language: raw.Result.Language,
		Model:    raw.Model.Type,
		Segments: make([]Segment, 0, len(raw.Transcription)),
	}
	var texts []string
	var vocalMs int64
	for _, item := range raw.Transcription {
		text := strings.TrimSpace(item.Text)
		if text == "" {
			continue
		}
		result.Segments = append(result.Segments, Segment{
			StartMs: item.Offsets.From,
			EndMs:   item.Offsets.To,
			Text:    text,
		})
		vocalMs += item.Offsets.To - item.Offsets.From
		texts = append(texts, text)
	}
	result.FullText = strings.Join(texts, " ")
	if len(result.Segments) > 0 {
		span := result.Segments[len(result.Segments)-1].EndMs - result.Segments[0].StartMs
		if span > 0 {
			result.VocalRatio = float64(vocalMs) / float64(span)
		}
	}
	if len(result.Segments) == 0 {
		return result, ErrNoSpeech
	}
	return result, nil
}

// IsPlaceholderText 判断一段是不是"没有实际词"的占位描述。
//
// whisper 对纯音乐/噪声会给 "(upbeat music)" / "[Music]" / "(inaudible)" 这类标注
// （圆括号与方括号两种写法都用）。它们对"这段有没有人声、在哪一段"有用，但**不是歌词** ——
// 调用方需要能区分，否则会把占位文本当成内容去写编排依据。
func IsPlaceholderText(text string) bool {
	trimmed := strings.TrimSpace(text)
	if len(trimmed) < 3 {
		return false
	}
	// 两类括号都要认；只认圆括号会漏掉 "[Music]" 这种。
	enclosed := (strings.HasPrefix(trimmed, "(") && strings.HasSuffix(trimmed, ")")) ||
		(strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]"))
	if !enclosed {
		return false
	}
	lower := strings.ToLower(trimmed)
	for _, marker := range []string{
		"music", "singing", "instrumental", "applause", "clapping",
		"laughter", "silence", "noise", "foreign language", "sound",
		"inaudible", "unintelligible", "indistinct", "blank_audio",
		"blank audio", "beep", "static", "cough", "sigh", "gasp",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// ContentSegments 只保留有实际内容的段落（去掉占位标注）。
func (r Result) ContentSegments() []Segment {
	out := make([]Segment, 0, len(r.Segments))
	for _, segment := range r.Segments {
		if IsPlaceholderText(segment.Text) {
			continue
		}
		out = append(out, segment)
	}
	return out
}

type lineWriter struct {
	forward func(string)
	buffer  bytes.Buffer
}

func (w *lineWriter) Write(payload []byte) (int, error) {
	w.buffer.Write(payload)
	for {
		line, err := w.buffer.ReadString('\n')
		if err != nil {
			// 未成行，留到下一次
			w.buffer.WriteString(line)
			break
		}
		w.forward(strings.TrimRight(line, "\r\n"))
	}
	return len(payload), nil
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
