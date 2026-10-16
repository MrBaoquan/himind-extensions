// Package frames 抽帧与拼图——它是"让 AI 看懂视频"的最低成本通道。
//
// 设计要点：拼图（contact sheet）比逐帧喂给模型便宜得多。一张 1920x720 的 24 格拼图约 200KB，
// 就能覆盖近四分钟片子的段落结构（实测：演唱 / 采访 / 红毯一眼可辨）。
package frames

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"himind-plugin/media-toolkit/internal/media/ffmpeg"
	"himind-plugin/media-toolkit/internal/media/workspace"
)

// SheetOptions 拼图参数。
type SheetOptions struct {
	// Columns / Rows 网格。默认 6x4。
	Columns int `json:"columns,omitempty"`
	Rows    int `json:"rows,omitempty"`
	// IntervalMs 采样间隔；为 0 时按"填满整个网格"自动计算。
	IntervalMs int64 `json:"interval_ms,omitempty"`
	// TileWidth 每格宽度（像素）。默认 320。高度按比例自动取偶数。
	TileWidth int `json:"tile_width,omitempty"`
	// Quality JPEG 质量（2-31，越小越好），默认 4。
	Quality int `json:"quality,omitempty"`
	// DurationMs 源时长；用于自动算采样间隔。
	DurationMs int64 `json:"-"`
}

// SheetResult 拼图结果。
type SheetResult struct {
	Path      string   `json:"path"`
	Columns   int      `json:"columns"`
	Rows      int      `json:"rows"`
	TileCount int      `json:"tile_count"`
	Width     int      `json:"width"`
	Height    int      `json:"height"`
	IntervalMs int64   `json:"interval_ms"`
	// MeanLuminance 产物平均亮度。这是"拼图是否真的成功"的判据，见下方说明。
	MeanLuminance float64  `json:"mean_luminance"`
	Warnings      []string `json:"warnings,omitempty"`
}

// blackLuminanceThreshold 判定"整张图接近全黑"的平均亮度门限。
//
// 为什么要判：ffmpeg 的 tile 在某些边长/像素格式组合下会**静默**产出全黑图——
// 退出码 0、文件大小正常、无任何警告，只是内容是黑的。实测就是被这个坑过一次
// （每格高度取到奇数导致）。不做这道校验的话，失败会一路安静地传到使用者眼前。
const blackLuminanceThreshold = 6.0

// ContactSheet 生成拼图。
func ContactSheet(
	ctx context.Context,
	tools ffmpeg.Tools,
	inputPath, outputPath string,
	options SheetOptions,
) (SheetResult, error) {
	columns := options.Columns
	if columns <= 0 {
		columns = 6
	}
	rows := options.Rows
	if rows <= 0 {
		rows = 4
	}
	tileWidth := options.TileWidth
	if tileWidth <= 0 {
		tileWidth = 320
	}
	quality := options.Quality
	if quality <= 0 {
		quality = 4
	}

	intervalMs := options.IntervalMs
	if intervalMs <= 0 {
		if options.DurationMs <= 0 {
			return SheetResult{}, errors.New("未给 interval_ms 时必须提供源时长以自动计算采样间隔")
		}
		intervalMs = options.DurationMs / int64(columns*rows)
		if intervalMs < 1 {
			intervalMs = 1
		}
	}

	// scale 的高度用 -2：保证偶数边长。用 -1 会取到奇数，进而让 tile 静默产出全黑图。
	sampleFps := 1000.0 / float64(intervalMs)
	filter := fmt.Sprintf(
		"fps=%s,scale=%d:-2,tile=%dx%d",
		formatFloat(sampleFps), tileWidth, columns, rows,
	)

	if err := workspace.EnsureParentDir(outputPath); err != nil {
		return SheetResult{}, err
	}

	_, err := tools.Run(ctx, ffmpeg.RunOptions{Args: []string{
		"-i", inputPath,
		"-vf", filter,
		"-frames:v", "1",
		"-q:v", strconv.Itoa(quality),
		outputPath,
	}})
	if err != nil {
		return SheetResult{}, err
	}

	result := SheetResult{
		Path:       outputPath,
		Columns:    columns,
		Rows:       rows,
		TileCount:  columns * rows,
		IntervalMs: intervalMs,
	}

	if stat, statErr := os.Stat(outputPath); statErr == nil && stat.Size() == 0 {
		return result, errors.New("拼图产物为空文件")
	}

	luminance, size, probeErr := inspectImage(ctx, tools, outputPath)
	if probeErr == nil {
		result.MeanLuminance = luminance
		result.Width = size[0]
		result.Height = size[1]
		if luminance >= 0 && luminance < blackLuminanceThreshold {
			result.Warnings = append(result.Warnings, fmt.Sprintf(
				"拼图平均亮度仅 %.1f，疑似全黑（常见原因是缩放边长取到奇数）。"+
					"请把 tile_width 设为偶数，或检查源文件是否可解码", luminance))
		}
	} else {
		result.Warnings = append(result.Warnings, "无法读取拼图亮度，未能校验产物是否有效: "+probeErr.Error())
	}

	return result, nil
}

// ExtractOptions 抽帧参数。
type ExtractOptions struct {
	// Width 输出宽度；0 = 原尺寸。
	Width int `json:"width,omitempty"`
	// Format 输出格式（jpg / png），默认 jpg。
	Format string `json:"format,omitempty"`
	// Quality JPEG 质量，默认 3。
	Quality int `json:"quality,omitempty"`
}

// Extract 在指定时刻各抽一帧。返回实际写出的文件路径。
func Extract(
	ctx context.Context,
	tools ffmpeg.Tools,
	inputPath, outputDir string,
	timesMs []int64,
	options ExtractOptions,
) ([]string, error) {
	if len(timesMs) == 0 {
		return nil, errors.New("times_ms 不能为空")
	}
	format := strings.TrimSpace(options.Format)
	if format == "" {
		format = "jpg"
	}
	quality := options.Quality
	if quality <= 0 {
		quality = 3
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, fmt.Errorf("无法创建输出目录: %w", err)
	}

	written := make([]string, 0, len(timesMs))
	for index, timeMs := range timesMs {
		if timeMs < 0 {
			return written, fmt.Errorf("第 %d 个时刻为负数", index+1)
		}
		name := fmt.Sprintf("frame_%04d_%dms.%s", index+1, timeMs, format)
		target := filepath.Join(outputDir, name)

		args := []string{"-ss", formatSeconds(timeMs), "-i", inputPath, "-frames:v", "1"}
		if options.Width > 0 {
			// 同样是 -2：奇数边长会引出下游一系列静默问题。
			args = append(args, "-vf", fmt.Sprintf("scale=%d:-2", options.Width))
		}
		if format == "jpg" || format == "jpeg" {
			args = append(args, "-q:v", strconv.Itoa(quality))
		}
		args = append(args, target)

		if _, err := tools.Run(ctx, ffmpeg.RunOptions{Args: args}); err != nil {
			return written, fmt.Errorf("第 %d 帧 (%dms) 抽取失败: %w", index+1, timeMs, err)
		}
		written = append(written, target)
	}
	return written, nil
}

// inspectImage 读回产物的平均亮度与像素尺寸。
func inspectImage(ctx context.Context, tools ffmpeg.Tools, path string) (float64, [2]int, error) {
	// 用 CSV 而不是 JSON：ffprobe 的 JSON 是带缩进空格的（`"width": 480`），
	// 按标记截取数字会被空格绊住；CSV 的 `480,240` 没有这个歧义。
	payload, err := tools.ProbeJSON(ctx, []string{"-select_streams", "v:0",
		"-show_entries", "stream=width,height", "-of", "csv=p=0", path})
	if err != nil {
		return -1, [2]int{}, err
	}
	size := [2]int{}
	fields := strings.Split(strings.TrimSpace(payload), ",")
	if len(fields) >= 2 {
		size[0] = parseInt(fields[0])
		size[1] = parseInt(fields[1])
	}

	stdout, err := tools.Capture(ctx, []string{
		"-i", path,
		"-vf", "signalstats,metadata=print:file=-",
		"-f", "null", "-",
	})
	if err != nil {
		return -1, size, err
	}
	luminance, ok := numberAfter(stdout, "lavfi.signalstats.YAVG=")
	if !ok {
		return -1, size, errors.New("未取到平均亮度")
	}
	return luminance, size, nil
}

func parseInt(value string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return parsed
}

// numberAfter 从文本里取某个标记后紧跟的数字。
func numberAfter(text, marker string) (float64, bool) {
	index := strings.Index(text, marker)
	if index < 0 {
		return 0, false
	}
	rest := text[index+len(marker):]
	end := 0
	for end < len(rest) {
		char := rest[end]
		if (char >= '0' && char <= '9') || char == '.' || char == '-' {
			end++
			continue
		}
		break
	}
	if end == 0 {
		return 0, false
	}
	value, err := strconv.ParseFloat(rest[:end], 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func formatSeconds(milliseconds int64) string {
	return strconv.FormatFloat(float64(milliseconds)/1000.0, 'f', 3, 64)
}
