package analysis

import (
	"context"
	"path/filepath"
	"strings"

	"himind-plugin/media-toolkit/internal/media/ffmpeg"
	"himind-plugin/media-toolkit/internal/media/probe"
)

// TechnicalReport 是面向用户与 AI 的归一化媒体参数，不暴露原始 ffprobe JSON。
type TechnicalReport struct {
	Path       string            `json:"path"`
	FileName   string            `json:"file_name"`
	MediaType  string            `json:"media_type"`
	Container  string            `json:"container"`
	DurationMs int64             `json:"duration_ms"`
	SizeBytes  int64             `json:"size_bytes"`
	BitRate    int64             `json:"bit_rate,omitempty"`
	Video      *TechnicalVideo   `json:"video,omitempty"`
	Audio      *TechnicalAudio   `json:"audio,omitempty"`
	Warnings   []string          `json:"warnings,omitempty"`
	Streams    []TechnicalStream `json:"streams"`
}

type TechnicalVideo struct {
	Codec       string  `json:"codec"`
	Profile     string  `json:"profile,omitempty"`
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	FPS         float64 `json:"fps"`
	BitRate     int64   `json:"bit_rate,omitempty"`
	PixelFormat string  `json:"pixel_format,omitempty"`
}

type TechnicalAudio struct {
	Codec      string `json:"codec"`
	SampleRate int    `json:"sample_rate"`
	Channels   int    `json:"channels"`
	BitRate    int64  `json:"bit_rate,omitempty"`
}

type TechnicalStream struct {
	Type        string `json:"type"`
	Codec       string `json:"codec"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
	SampleRate  int    `json:"sample_rate,omitempty"`
	Channels    int    `json:"channels,omitempty"`
	BitRate     int64  `json:"bit_rate,omitempty"`
	PixelFormat string `json:"pixel_format,omitempty"`
}

// Technical 探测一个媒体并转换成稳定、简洁的参数报告。
func Technical(ctx context.Context, tools ffmpeg.Tools, path string) (TechnicalReport, error) {
	info, err := probe.Read(ctx, tools, path)
	if err != nil {
		return TechnicalReport{}, err
	}
	return TechnicalFromInfo(info), nil
}

// TechnicalFromInfo 只做归一化，不访问外部进程，便于单独测试。
func TechnicalFromInfo(info probe.Info) TechnicalReport {
	report := TechnicalReport{
		Path:       info.Path,
		FileName:   filepath.Base(info.Path),
		Container:  normalizeContainer(info.Path, info.FormatName),
		DurationMs: info.DurationMs,
		SizeBytes:  info.SizeBytes,
		BitRate:    info.BitRate,
		Warnings:   normalizeWarnings(info.Path, info.Warnings),
		Streams:    make([]TechnicalStream, 0, len(info.Streams)),
	}
	if info.Video != nil {
		if isImagePath(info.Path) {
			report.MediaType = "image"
		} else {
			report.MediaType = "video"
		}
		report.Video = &TechnicalVideo{
			Codec: info.Video.CodecName, Profile: info.Video.Profile,
			Width: info.Video.Width, Height: info.Video.Height, FPS: info.Video.Fps,
			BitRate: info.Video.BitRate, PixelFormat: info.Video.PixelFormat,
		}
		if info.Video.Fps <= 0 {
			report.Warnings = append(report.Warnings, "帧率不可读")
		}
	}
	if info.Audio != nil {
		if report.MediaType == "" {
			report.MediaType = "audio"
		}
		report.Audio = &TechnicalAudio{
			Codec: info.Audio.CodecName, SampleRate: info.Audio.SampleRate,
			Channels: info.Audio.Channels, BitRate: info.Audio.BitRate,
		}
	} else if info.HasVideo {
		report.Warnings = append(report.Warnings, "没有音频轨")
	}
	for _, stream := range info.Streams {
		report.Streams = append(report.Streams, TechnicalStream{
			Type: stream.CodecType, Codec: stream.CodecName,
			Width: stream.Width, Height: stream.Height,
			SampleRate: stream.SampleRate, Channels: stream.Channels,
			BitRate: stream.BitRate, PixelFormat: stream.PixelFormat,
		})
	}
	return report
}

func isImagePath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg", ".png", ".webp", ".bmp", ".tif", ".tiff", ".gif":
		return true
	default:
		return false
	}
}

func normalizeWarnings(path string, warnings []string) []string {
	if !isImagePath(path) {
		return append([]string(nil), warnings...)
	}
	result := make([]string, 0, len(warnings)+1)
	for _, warning := range warnings {
		if strings.Contains(warning, "时长不可读") {
			continue
		}
		result = append(result, warning)
	}
	result = append(result, "静态图片通常没有时长信息")
	return result
}
func normalizeContainer(path, formatName string) string {
	extension := strings.ToLower(filepath.Ext(path))
	switch extension {
	case ".mp4", ".m4v":
		return "mp4"
	case ".mov":
		return "mov"
	case ".mkv":
		return "mkv"
	case ".webm":
		return "webm"
	case ".mp3", ".wav", ".aac", ".flac", ".m4a", ".ogg", ".opus":
		return strings.TrimPrefix(extension, ".")
	case ".jpg", ".jpeg":
		return "jpeg"
	case ".png":
		return "png"
	case ".webp":
		return "webp"
	case ".bmp":
		return "bmp"
	case ".tif", ".tiff":
		return "tiff"
	case ".gif":
		return "gif"
	}
	name := strings.ToLower(strings.TrimSpace(strings.Split(formatName, ",")[0]))
	if name == "matroska" {
		return "mkv"
	}
	return name
}
