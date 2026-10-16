// Package probe 读取媒体文件的自述信息。
//
// 这是所有下游步骤的前提：不先知道时长/帧率/有无音轨，就没法算进度百分比、
// 没法决定要不要做音频处理，也没法判断"这张图全黑"是内容如此还是解码出错。
package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"himind-plugin/media-toolkit/internal/media/ffmpeg"
)

// Info 一次探测的结果。
type Info struct {
	Path       string   `json:"path"`
	DurationMs int64    `json:"duration_ms"`
	SizeBytes  int64    `json:"size_bytes"`
	BitRate    int64    `json:"bit_rate,omitempty"`
	FormatName string   `json:"format_name,omitempty"`
	Video      *Stream  `json:"video,omitempty"`
	Audio      *Stream  `json:"audio,omitempty"`
	Streams    []Stream `json:"streams"`
	HasAudio   bool     `json:"has_audio"`
	HasVideo   bool     `json:"has_video"`
	Warnings   []string `json:"warnings,omitempty"`
}

// Stream 一个轨道。
type Stream struct {
	Index      int     `json:"index"`
	CodecType  string  `json:"codec_type"`
	CodecName  string  `json:"codec_name,omitempty"`
	Width      int     `json:"width,omitempty"`
	Height     int     `json:"height,omitempty"`
	Fps        float64 `json:"fps,omitempty"`
	SampleRate int     `json:"sample_rate,omitempty"`
	Channels   int     `json:"channels,omitempty"`
	DurationMs int64   `json:"duration_ms,omitempty"`
}

type rawProbe struct {
	Streams []struct {
		Index     int    `json:"index"`
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		RFrameRate string `json:"r_frame_rate"`
		SampleRate string `json:"sample_rate"`
		Channels   int    `json:"channels"`
		Duration   string `json:"duration"`
	} `json:"streams"`
	Format struct {
		Duration   string `json:"duration"`
		Size       string `json:"size"`
		BitRate    string `json:"bit_rate"`
		FormatName string `json:"format_name"`
	} `json:"format"`
}

// Read 探测一个文件。
func Read(ctx context.Context, tools ffmpeg.Tools, path string) (Info, error) {
	payload, err := tools.ProbeJSON(ctx, []string{"-show_format", "-show_streams", path})
	if err != nil {
		return Info{}, err
	}
	var raw rawProbe
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return Info{}, fmt.Errorf("解析 ffprobe 输出失败: %w", err)
	}

	info := Info{
		Path:       path,
		DurationMs: parseDurationMs(raw.Format.Duration),
		SizeBytes:  parseInt64(raw.Format.Size),
		BitRate:    parseInt64(raw.Format.BitRate),
		FormatName: raw.Format.FormatName,
		Streams:    make([]Stream, 0, len(raw.Streams)),
	}
	for _, item := range raw.Streams {
		stream := Stream{
			Index:      item.Index,
			CodecType:  item.CodecType,
			CodecName:  item.CodecName,
			Width:      item.Width,
			Height:     item.Height,
			Fps:        parseFps(item.RFrameRate),
			SampleRate: parseInt(item.SampleRate),
			Channels:   item.Channels,
			DurationMs: parseDurationMs(item.Duration),
		}
		info.Streams = append(info.Streams, stream)
		switch item.CodecType {
		case "video":
			if info.Video == nil {
				copied := stream
				info.Video = &copied
			}
		case "audio":
			if info.Audio == nil {
				copied := stream
				info.Audio = &copied
			}
		}
	}
	info.HasVideo = info.Video != nil
	info.HasAudio = info.Audio != nil

	if info.DurationMs <= 0 {
		info.Warnings = append(info.Warnings, "时长不可读，进度百分比将不可用")
	}
	if !info.HasVideo && !info.HasAudio {
		return info, fmt.Errorf("该文件既没有视频轨也没有音频轨: %s", path)
	}
	return info, nil
}

func parseDurationMs(value string) int64 {
	seconds, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || seconds <= 0 {
		return 0
	}
	return int64(seconds * 1000)
}

func parseInt64(value string) int64 {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func parseInt(value string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return parsed
}

// parseFps 解析 ffprobe 的分数形式帧率（如 "30/1"、"30000/1001"）。
func parseFps(value string) float64 {
	text := strings.TrimSpace(value)
	if text == "" || text == "0/0" {
		return 0
	}
	if numerator, denominator, found := strings.Cut(text, "/"); found {
		top, errTop := strconv.ParseFloat(numerator, 64)
		bottom, errBottom := strconv.ParseFloat(denominator, 64)
		if errTop != nil || errBottom != nil || bottom == 0 {
			return 0
		}
		return top / bottom
	}
	parsed, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0
	}
	return parsed
}
