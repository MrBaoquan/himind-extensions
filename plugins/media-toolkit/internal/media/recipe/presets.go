package recipe

import "fmt"

// 预设是"常见需求"的固化。新增一个需求通常只需在这里加一个构造函数（十几个字段），
// 而不是新写一套参数拼装逻辑——这正是把 recipe 层单独抽出来的目的。

// TranscodeOptions 通用转码参数。
type TranscodeOptions struct {
	// Width / Height 目标尺寸；0 = 保持原样。
	Width  int
	Height int
	// Codec 编码器，默认 libx264。
	Codec string
	// CRF 恒定质量；nil 时用 Bitrate。
	CRF *int
	// Bitrate 目标码率（如 "4M"）。
	Bitrate string
	// Preset 编码预设，默认 veryfast。
	Preset string
	// FastStart 前置 moov（网络播放需要）。
	FastStart bool
}

// Transcode 按给定参数转码一个输入。
func Transcode(inputPath, outputPath string, options TranscodeOptions) Recipe {
	video := &VideoChain{Encode: &VideoEncode{
		Codec:       defaultString(options.Codec, "libx264"),
		CRF:         options.CRF,
		Bitrate:     options.Bitrate,
		Preset:      defaultString(options.Preset, "veryfast"),
		PixelFormat: "yuv420p",
	}}
	if options.Width > 0 || options.Height > 0 {
		video.Scale = &Scale{Width: options.Width, Height: options.Height}
	}
	return Recipe{
		Inputs: []Input{{Path: inputPath}},
		Video:  video,
		Audio:  &AudioChain{Encode: &AudioEncode{Codec: "aac", Bitrate: "160k"}},
		Output: OutputSpec{Path: outputPath, FastStart: options.FastStart},
	}
}

// CompressOptions 压缩参数（目标是"体积明显变小，观感可接受"）。
type CompressOptions struct {
	// MaxWidth / MaxHeight 尺寸上限；任一为 0 表示不设该方向上限。
	MaxWidth  int
	MaxHeight int
	// CRF 质量，越大越小越糊。默认 28。
	CRF *int
	// Fps 帧率上限；0 = 保持。
	Fps float64
}

// CompressForWeb 产出体积优先、兼容性优先的版本（yuv420p + aac + faststart）。
//
// 与 Transcode 分开而不是加一堆布尔开关：压缩的默认值取向完全不同
// （放宽 CRF、限帧率、强制像素格式），塞进一个函数会让两组默认值互相污染。
func CompressForWeb(inputPath, outputPath string, options CompressOptions) Recipe {
	crf := 28
	if options.CRF != nil {
		crf = *options.CRF
	}
	video := &VideoChain{Encode: &VideoEncode{
		Codec:       "libx264",
		CRF:         &crf,
		Preset:      "slow",
		PixelFormat: "yuv420p",
	}}
	// 只设上限时按宽定高（高度交给 -2 自动并强制偶数）。
	if options.MaxWidth > 0 || options.MaxHeight > 0 {
		video.Scale = &Scale{Width: options.MaxWidth, Height: options.MaxHeight}
	}
	if options.Fps > 0 {
		video.Fps = &Fps{Value: options.Fps}
	}
	return Recipe{
		Inputs: []Input{{Path: inputPath}},
		Video:  video,
		Audio:  &AudioChain{Encode: &AudioEncode{Codec: "aac", Bitrate: "128k"}},
		Output: OutputSpec{Path: outputPath, FastStart: true},
	}
}

// ExtractAudioOptions 抽音频参数。
type ExtractAudioOptions struct {
	// Codec 默认 libmp3lame（由扩展名决定更合适时调用方覆盖）。
	Codec   string
	Bitrate string
	// StartMs / EndMs 截取区间。
	StartMs *int64
	EndMs   *int64
	// Normalize 是否做响度归一化（-16 LUFS，播客/短视频常用）。
	Normalize bool
}

// ExtractAudio 只取音频轨。
func ExtractAudio(inputPath, outputPath string, options ExtractAudioOptions) Recipe {
	audio := &AudioChain{Encode: &AudioEncode{
		Codec:   defaultString(options.Codec, "libmp3lame"),
		Bitrate: defaultString(options.Bitrate, "192k"),
	}}
	if options.StartMs != nil || options.EndMs != nil {
		audio.Trim = &Trim{StartMs: options.StartMs, EndMs: options.EndMs}
	}
	return Recipe{
		Inputs: []Input{{Path: inputPath}},
		Video:  &VideoChain{Disable: true},
		Audio:  audio,
		Output: OutputSpec{Path: outputPath},
	}
}

// ImagesToVideoOptions 图片转视频参数。
type ImagesToVideoOptions struct {
	// Framerate 输出帧率，默认 30。
	Framerate float64
	// TotalMs 单张图占多长（用于单图 + 循环输入生成静帧视频）。
	TotalMs *int64
	// Width / Height 目标尺寸；0 = 保持。
	Width  int
	Height int
	// CRF 默认 20。
	CRF *int
}

// ImagesToVideo 把图片（或图片序列）合成视频。
//
// 单张静帧与序列的差别只在输入声明（-loop / -framerate），所以两者共用一个构造函数，
// 由 input 参数区分——避免出现两个几乎相同的预设各自漂移。
func ImagesToVideo(input Input, outputPath string, options ImagesToVideoOptions) Recipe {
	crf := 20
	if options.CRF != nil {
		crf = *options.CRF
	}
	framerate := options.Framerate
	if framerate <= 0 {
		framerate = 30
	}
	video := &VideoChain{
		Fps:    &Fps{Value: framerate},
		Encode: &VideoEncode{Codec: "libx264", CRF: &crf, Preset: "medium", PixelFormat: "yuv420p"},
	}
	if options.Width > 0 || options.Height > 0 {
		video.Scale = &Scale{Width: options.Width, Height: options.Height}
	}
	if options.TotalMs != nil {
		video.Trim = &Trim{DurationMs: options.TotalMs}
	}
	return Recipe{
		Inputs: []Input{input},
		Video:  video,
		Audio:  &AudioChain{Disable: true},
		Output: OutputSpec{Path: outputPath, FastStart: true},
	}
}

// GifOptions GIF 参数。
type GifOptions struct {
	Width   int
	Fps     float64
	StartMs *int64
	EndMs   *int64
}

// ToGif 生成 GIF。
//
// 注意：GIF 的调色板优化需要 filter_complex（palettegen/paletteuse），
// 本层不生成它——由调用方在 frames 侧用专门的滤镜图完成，避免这里产出半成品参数。
func ToGif(inputPath, outputPath string, options GifOptions) Recipe {
	video := &VideoChain{Encode: &VideoEncode{Codec: "gif"}}
	if options.Width > 0 {
		video.Scale = &Scale{Width: options.Width}
	}
	framerate := options.Fps
	if framerate <= 0 {
		framerate = 12
	}
	video.Fps = &Fps{Value: framerate}
	if options.StartMs != nil || options.EndMs != nil {
		video.Trim = &Trim{StartMs: options.StartMs, EndMs: options.EndMs}
	}
	return Recipe{
		Inputs: []Input{{Path: inputPath}},
		Video:  video,
		Audio:  &AudioChain{Disable: true},
		Output: OutputSpec{Path: outputPath},
	}
}

// PresetNames 已内置的预设名（供文档与状态展示）。
func PresetNames() []string {
	return []string{"transcode", "compress-web", "extract-audio", "images-to-video", "gif"}
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// Describe 给出一句话摘要，用于日志与 plan 输出。
func (r Recipe) Describe() string {
	inputs := len(r.Inputs)
	video := "保留"
	if r.Video == nil || r.Video.Disable {
		video = "丢弃"
	} else if r.Video.Encode != nil && r.Video.Encode.Copy {
		video = "复制"
	} else if r.Video.Encode != nil && r.Video.Encode.Codec != "" {
		video = r.Video.Encode.Codec
	}
	audio := "保留"
	if r.Audio == nil || r.Audio.Disable {
		audio = "丢弃"
	} else if r.Audio.Encode != nil && r.Audio.Encode.Copy {
		audio = "复制"
	} else if r.Audio.Encode != nil && r.Audio.Encode.Codec != "" {
		audio = r.Audio.Encode.Codec
	}
	return fmt.Sprintf("输入 %d 路 · 视频 %s · 音频 %s", inputs, video, audio)
}
