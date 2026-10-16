// Package ffmpeg 定位并执行 ffmpeg / ffprobe，并把它们的输出收敛成结构化结果。
//
// 为什么不让各能力自己拼 exec.Command：执行外部进程是这个插件里唯一的"危险动作"，
// 参数构造、超时、取消、stderr 采集、进度解析全都必须一致，否则会出现
// "有的能力能取消、有的不能"这类只有现场才暴露的差异。
package ffmpeg

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Tools 一次会话内已定位的可执行文件。
type Tools struct {
	FFmpeg  string
	FFprobe string
	Version string
}

// ErrNotAvailable 表示本机没有可用的 ffmpeg。
var ErrNotAvailable = errors.New("未找到 ffmpeg，请先安装或通过 runtime.install 获取")

// Locate 在 PATH 与常见安装位置里查找 ffmpeg / ffprobe。
func Locate() (Tools, error) {
	var tools Tools
	ffmpegPath, err := lookPath("ffmpeg")
	if err != nil {
		return Tools{}, ErrNotAvailable
	}
	tools.FFmpeg = ffmpegPath
	if probePath, probeErr := lookPath("ffprobe"); probeErr == nil {
		tools.FFprobe = probePath
	}
	tools.Version = probeVersion(ffmpegPath)
	return tools, nil
}

func lookPath(name string) (string, error) {
	if path, err := exec.LookPath(name + ".exe"); err == nil {
		return path, nil
	}
	return exec.LookPath(name)
}

func probeVersion(ffmpegPath string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, ffmpegPath, "-version")
	configureHiddenProcess(command)
	output, err := command.Output()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(output))
	if index := strings.IndexByte(line, '\n'); index >= 0 {
		line = line[:index]
	}
	return strings.TrimSpace(line)
}

// Progress 一次进度回调的载荷（对应 ffmpeg -progress 的键值对）。
type Progress struct {
	Frame      int64
	OutTimeMs  int64
	Speed      string
	TotalSize  int64
	Finished   bool
	RawOutTime string
}

// RunOptions 一次执行的参数。
type RunOptions struct {
	// Args 传给 ffmpeg 的参数（不含可执行文件本身）。
	Args []string
	// Dir 子进程工作目录。置空则继承当前目录。
	// 需要它的地方：滤镜里写相对文件名（如 metadata=print:file=xxx）时会落到该目录；
	// 用绝对路径写在滤镜参数里是行不通的——滤镜参数以 ':' 分隔，Windows 盘符会被当成分隔符，
	// 表现为 "Error initializing a simple filtergraph"。
	Dir string
	// OnProgress 非空时启用 -progress pipe:1 并把解析结果回调出去。
	OnProgress func(Progress)
	// LogWriter 非空时把 stderr 实时写入（调用方通常传日志文件）。
	LogWriter io.Writer
}

// RunResult 一次执行的结果。
type RunResult struct {
	ExitCode int
	Stderr   string
	Elapsed  time.Duration
}

// Run 执行 ffmpeg。ctx 取消会终止子进程。
func (t Tools) Run(ctx context.Context, options RunOptions) (RunResult, error) {
	if t.FFmpeg == "" {
		return RunResult{}, ErrNotAvailable
	}
	started := time.Now()

	args := make([]string, 0, len(options.Args)+8)
	// -nostdin: 不允许子进程去读我们的 stdin（stdin 是 JSON-RPC 通道，被抢走就断线）。
	args = append(args, "-hide_banner", "-nostdin", "-y")
	args = append(args, options.Args...)
	if options.OnProgress != nil {
		args = append(args, "-nostats", "-progress", "pipe:1")
	}

	command := exec.CommandContext(ctx, t.FFmpeg, args...)
	configureHiddenProcess(command)
	if options.Dir != "" {
		command.Dir = options.Dir
	}

	var stderr bytes.Buffer
	if options.LogWriter != nil {
		command.Stderr = io.MultiWriter(&stderr, options.LogWriter)
	} else {
		command.Stderr = &stderr
	}

	if options.OnProgress == nil {
		err := command.Run()
		result := RunResult{Stderr: stderr.String(), Elapsed: time.Since(started)}
		result.ExitCode = exitCodeOf(command)
		if err != nil {
			if ctx.Err() != nil {
				return result, fmt.Errorf("已被取消: %w", ctx.Err())
			}
			return result, fmt.Errorf("ffmpeg 失败 (exit=%d): %s", result.ExitCode, lastLines(result.Stderr, 6))
		}
		return result, nil
	}

	stdout, err := command.StdoutPipe()
	if err != nil {
		return RunResult{}, fmt.Errorf("无法读取 ffmpeg 进度: %w", err)
	}
	if err := command.Start(); err != nil {
		return RunResult{}, fmt.Errorf("无法启动 ffmpeg: %w", err)
	}

	var waitGroup sync.WaitGroup
	waitGroup.Add(1)
	go func() {
		defer waitGroup.Done()
		parseProgress(stdout, options.OnProgress)
	}()

	waitErr := command.Wait()
	waitGroup.Wait()

	result := RunResult{Stderr: stderr.String(), Elapsed: time.Since(started)}
	result.ExitCode = exitCodeOf(command)
	if waitErr != nil {
		if ctx.Err() != nil {
			return result, fmt.Errorf("已被取消: %w", ctx.Err())
		}
		return result, fmt.Errorf("ffmpeg 失败 (exit=%d): %s", result.ExitCode, lastLines(result.Stderr, 6))
	}
	return result, nil
}

// Capture 执行 ffmpeg 并返回 stdout（用于输出不是文件的场景，如 metadata=print:file=-）。
func (t Tools) Capture(ctx context.Context, args []string) (string, error) {
	if t.FFmpeg == "" {
		return "", ErrNotAvailable
	}
	full := append([]string{"-hide_banner", "-nostdin", "-y"}, args...)
	command := exec.CommandContext(ctx, t.FFmpeg, full...)
	configureHiddenProcess(command)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return stdout.String(), fmt.Errorf(
			"ffmpeg 失败 (exit=%d): %s", exitCodeOf(command), lastLines(stderr.String(), 6))
	}
	return stdout.String(), nil
}

// ProbeJSON 执行 ffprobe 并返回原始 JSON 文本。
func (t Tools) ProbeJSON(ctx context.Context, args []string) (string, error) {
	if t.FFprobe == "" {
		return "", errors.New("未找到 ffprobe")
	}
	full := append([]string{"-v", "error", "-print_format", "json"}, args...)
	command := exec.CommandContext(ctx, t.FFprobe, full...)
	configureHiddenProcess(command)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return "", fmt.Errorf(
			"ffprobe 失败 (exit=%d): %s", exitCodeOf(command), lastLines(stderr.String(), 4))
	}
	return stdout.String(), nil
}

func parseProgress(reader io.Reader, onProgress func(Progress)) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	current := Progress{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		switch key {
		case "frame":
			current.Frame, _ = strconv.ParseInt(value, 10, 64)
		case "out_time_ms":
			current.OutTimeMs, _ = strconv.ParseInt(value, 10, 64)
			current.RawOutTime = value
		case "total_size":
			current.TotalSize, _ = strconv.ParseInt(value, 10, 64)
		case "speed":
			current.Speed = strings.TrimSpace(value)
		case "progress":
			current.Finished = value == "end"
			onProgress(current)
			current = Progress{}
		}
	}
}

func exitCodeOf(command *exec.Cmd) int {
	if command.ProcessState == nil {
		return -1
	}
	return command.ProcessState.ExitCode()
}

func lastLines(text string, count int) string {
	lines := strings.Split(strings.TrimRight(text, "\r\n"), "\n")
	if len(lines) > count {
		lines = lines[len(lines)-count:]
	}
	joined := strings.TrimSpace(strings.Join(lines, " | "))
	if len(joined) > 700 {
		joined = joined[len(joined)-700:]
	}
	return joined
}

// BaseName 返回可执行文件名（用于状态展示）。
func (t Tools) BaseName() string {
	if t.FFmpeg == "" {
		return ""
	}
	return filepath.Base(t.FFmpeg)
}
