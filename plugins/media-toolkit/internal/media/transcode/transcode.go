// Package transcode 把普通用户可理解的视频转换选项映射成安全 recipe。
package transcode

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"himind-plugin/media-toolkit/internal/media/recipe"
)

// Options 高层视频转换意图。
type Options struct {
	Format           string  `json:"format"`
	VideoCodec       string  `json:"video_codec,omitempty"`
	AudioCodec       string  `json:"audio_codec,omitempty"`
	Width            int     `json:"width,omitempty"`
	Height           int     `json:"height,omitempty"`
	FPS              float64 `json:"fps,omitempty"`
	CRF              int     `json:"crf,omitempty"`
	VideoBitrateKbps int     `json:"video_bitrate_kbps,omitempty"`
	AudioBitrateKbps int     `json:"audio_bitrate_kbps,omitempty"`
	Preset           string  `json:"preset,omitempty"`
}

type containerSpec struct {
	Extensions  map[string]bool
	VideoCodecs map[string]string
	AudioCodecs map[string]string
	FastStart   bool
}

var containers = map[string]containerSpec{
	"mp4": {
		Extensions: map[string]bool{".mp4": true, ".m4v": true},
		VideoCodecs: map[string]string{
			"h264": "libx264", "h265": "libx265", "copy": "",
		},
		AudioCodecs: map[string]string{
			"aac": "aac", "copy": "", "none": "",
		},
		FastStart: true,
	},
	"mov": {
		Extensions: map[string]bool{".mov": true},
		VideoCodecs: map[string]string{
			"h264": "libx264", "h265": "libx265", "copy": "",
		},
		AudioCodecs: map[string]string{
			"aac": "aac", "copy": "", "none": "",
		},
		FastStart: true,
	},
	"mkv": {
		Extensions: map[string]bool{".mkv": true},
		VideoCodecs: map[string]string{
			"h264": "libx264", "h265": "libx265", "vp9": "libvpx-vp9", "copy": "",
		},
		AudioCodecs: map[string]string{
			"aac": "aac", "opus": "libopus", "mp3": "libmp3lame", "copy": "", "none": "",
		},
	},
	"webm": {
		Extensions: map[string]bool{".webm": true},
		VideoCodecs: map[string]string{
			"vp9": "libvpx-vp9", "copy": "",
		},
		AudioCodecs: map[string]string{
			"opus": "libopus", "copy": "", "none": "",
		},
	},
}

// Build 生成一个可编译、可预览的 recipe。
func Build(input, output string, options Options) (recipe.Recipe, error) {
	format := strings.ToLower(strings.TrimSpace(options.Format))
	spec, ok := containers[format]
	if !ok {
		return recipe.Recipe{}, fmt.Errorf("不支持的视频格式: %s", options.Format)
	}
	if !spec.Extensions[strings.ToLower(filepath.Ext(output))] {
		return recipe.Recipe{}, fmt.Errorf("%s 输出必须使用 %s 扩展名", format, extensionHint(spec.Extensions))
	}
	videoKey := strings.ToLower(strings.TrimSpace(options.VideoCodec))
	if videoKey == "" {
		if format == "webm" {
			videoKey = "vp9"
		} else {
			videoKey = "h264"
		}
	}
	videoCodec, ok := spec.VideoCodecs[videoKey]
	if !ok {
		return recipe.Recipe{}, fmt.Errorf("%s 不支持视频编码 %s", format, options.VideoCodec)
	}
	audioKey := strings.ToLower(strings.TrimSpace(options.AudioCodec))
	if audioKey == "" {
		if format == "webm" {
			audioKey = "opus"
		} else {
			audioKey = "aac"
		}
	}
	audioCodec, ok := spec.AudioCodecs[audioKey]
	if !ok {
		return recipe.Recipe{}, fmt.Errorf("%s 不支持音频编码 %s", format, options.AudioCodec)
	}
	if options.Width < 0 || options.Height < 0 || options.Width > 7680 || options.Height > 4320 {
		return recipe.Recipe{}, errors.New("宽高超出允许范围")
	}
	if options.Width%2 != 0 || options.Height%2 != 0 {
		return recipe.Recipe{}, errors.New("宽高必须是偶数")
	}
	if options.FPS < 0 || options.FPS > 240 {
		return recipe.Recipe{}, errors.New("fps 应在 1-240")
	}
	if options.CRF < 0 || options.CRF > 51 {
		return recipe.Recipe{}, errors.New("CRF 应在 0-51")
	}
	if options.CRF > 0 && options.VideoBitrateKbps > 0 {
		return recipe.Recipe{}, errors.New("CRF 与目标码率不能同时设置")
	}
	if options.VideoBitrateKbps < 0 || options.VideoBitrateKbps > 200000 {
		return recipe.Recipe{}, errors.New("视频码率超出允许范围")
	}
	if options.AudioBitrateKbps < 0 || options.AudioBitrateKbps > 1024 {
		return recipe.Recipe{}, errors.New("音频码率超出允许范围")
	}
	if videoKey == "copy" && (options.Width > 0 || options.Height > 0 || options.FPS > 0 || options.CRF > 0 || options.VideoBitrateKbps > 0) {
		return recipe.Recipe{}, errors.New("视频流复制时不能缩放、限帧或重设质量")
	}

	video := &recipe.VideoChain{}
	if videoKey == "copy" {
		video.Encode = &recipe.VideoEncode{Copy: true}
	} else {
		encode := &recipe.VideoEncode{Codec: videoCodec, PixelFormat: "yuv420p"}
		if options.CRF > 0 {
			crf := options.CRF
			encode.CRF = &crf
		}
		if options.VideoBitrateKbps > 0 {
			encode.Bitrate = strconv.Itoa(options.VideoBitrateKbps) + "k"
		}
		if videoCodec == "libx264" || videoCodec == "libx265" {
			encode.Preset = normalizePreset(options.Preset)
		}
		video.Encode = encode
	}
	if options.Width > 0 || options.Height > 0 {
		video.Scale = &recipe.Scale{Width: options.Width, Height: options.Height}
	}
	if options.FPS > 0 {
		video.Fps = &recipe.Fps{Value: options.FPS}
	}

	audio := &recipe.AudioChain{}
	if audioKey == "none" {
		audio.Disable = true
	} else if audioKey == "copy" {
		audio.Encode = &recipe.AudioEncode{Copy: true}
	} else {
		bitrate := options.AudioBitrateKbps
		if bitrate <= 0 {
			if audioCodec == "libopus" {
				bitrate = 128
			} else {
				bitrate = 160
			}
		}
		audio.Encode = &recipe.AudioEncode{Codec: audioCodec, Bitrate: strconv.Itoa(bitrate) + "k"}
	}

	return recipe.Recipe{
		Inputs: []recipe.Input{{Path: input}},
		Video:  video,
		Audio:  audio,
		Output: recipe.OutputSpec{Path: output, FastStart: spec.FastStart},
	}, nil
}

// SupportedContainers 返回首版支持的容器。
func SupportedContainers() []string {
	return []string{"mp4", "mov", "mkv", "webm"}
}

func normalizePreset(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "fast", "medium", "slow", "veryfast", "faster", "slower", "veryslow":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "medium"
	}
}

func extensionHint(values map[string]bool) string {
	items := make([]string, 0, len(values))
	for value := range values {
		items = append(items, value)
	}
	return strings.Join(items, " / ")
}
