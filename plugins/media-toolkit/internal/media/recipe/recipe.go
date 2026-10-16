// Package recipe 把「要对媒体做什么」表达成声明式步骤，再编译成 ffmpeg 参数。
//
// 为什么需要这一层，而不是每个能力各自拼 argv：
//
//  1. 需求会持续增加（转码、压缩、图转视频、抽音频、GIF……）。若每来一个就写一个参数构造器，
//     第 N 个需求就要读 N-1 份同类代码，参数校验散落各处、行为不可预测——底座由此腐烂。
//     有了 recipe，新增需求通常只是新增一个预设（十几个字段），而非新增一套参数逻辑。
//  2. 安全。插件规范禁止暴露"任意命令"。一个接受原始 ffmpeg 参数的能力等价于任意命令执行；
//     类型化的 recipe 把这个口子从设计上焊死，而不是依赖调用方自觉。
//  3. 可复核。recipe 是数据，因此可以在真正执行前先编译成参数给用户过目（见 recipe.plan）。
package recipe

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// Recipe 一次媒体处理任务。
type Recipe struct {
	// Inputs 输入文件。多输入用于图片序列、拼贴等。
	Inputs []Input `json:"inputs"`

	Video *VideoChain `json:"video,omitempty"`
	Audio *AudioChain `json:"audio,omitempty"`

	Output OutputSpec `json:"output"`
}

// Input 一路输入。
type Input struct {
	Path string `json:"path"`
	// Framerate 图片序列的输入帧率（如 "24"）。非空时以 -framerate 传入。
	Framerate string `json:"framerate,omitempty"`
	// Loop 循环输入（图片转视频时让单张图循环）。
	Loop bool `json:"loop,omitempty"`
}

// VideoChain 视频侧的步骤。
type VideoChain struct {
	Trim   *Trim        `json:"trim,omitempty"`
	Crop   *Crop        `json:"crop,omitempty"`
	Scale  *Scale       `json:"scale,omitempty"`
	Fps    *Fps         `json:"fps,omitempty"`
	Encode *VideoEncode `json:"encode,omitempty"`
	// Disable 丢弃视频轨（-vn），用于只取音频。
	Disable bool `json:"disable,omitempty"`
}

// AudioChain 音频侧的步骤。
type AudioChain struct {
	Trim     *Trim        `json:"trim,omitempty"`
	Volume   *Volume      `json:"volume,omitempty"`
	Resample *Resample    `json:"resample,omitempty"`
	Encode   *AudioEncode `json:"encode,omitempty"`
	// Disable 丢弃音频轨（-an）。
	Disable bool `json:"disable,omitempty"`
}

// Trim 时间裁剪（毫秒）。
//
// 用滤镜实现而不是 -ss/-t：滤镜是帧精确的，且能与缩放/裁剪等步骤自由组合；
// -ss 放在 -i 前虽快，但落点随关键帧漂移，做内容交付时不可接受。
type Trim struct {
	StartMs    *int64 `json:"start_ms,omitempty"`
	EndMs      *int64 `json:"end_ms,omitempty"`
	DurationMs *int64 `json:"duration_ms,omitempty"`
}

// Crop 画面裁剪。
type Crop struct {
	Width  int `json:"width"`
	Height int `json:"height"`
	X      int `json:"x,omitempty"`
	Y      int `json:"y,omitempty"`
}

// Scale 缩放。
//
// Width / Height 允许为 0，表示"按比例自动"，此时编译成 -2（而不是 -1）。
//
// 为什么强制偶数：实测 scale 用 -1 时高度可能取到奇数，紧接着的 tile（拼图）会**静默**
// 产出一张全黑图——不报错、不退非零，只是结果是黑的。用 -2 恒定取偶数，从源头消除这类
// "看起来成功、其实全错"的失败。
type Scale struct {
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
}

// Fps 重采样帧率。
type Fps struct {
	Value float64 `json:"value"`
}

// Volume 音量调整。
type Volume struct {
	// Factor 倍数（1.0 = 不变）。
	Factor *float64 `json:"factor,omitempty"`
	// GainDb 增益分贝（优先于 Factor）。
	GainDb *float64 `json:"gain_db,omitempty"`
}

// Resample 音频重采样。
type Resample struct {
	SampleRate int `json:"sample_rate"`
	Channels   int `json:"channels,omitempty"`
}

// VideoEncode 视频编码参数。
type VideoEncode struct {
	// Codec 编码器（libx264 / libx265 / libvpx-vp9 / ...）。空 = copy（不重编码）。
	Codec string `json:"codec,omitempty"`
	// Copy 显式要求流拷贝（-c:v copy）。比 Codec 为空更明确。
	Copy bool `json:"copy,omitempty"`
	// CRF 恒定质量（0-51，越小越好）。与 Bitrate 二选一。
	CRF *int `json:"crf,omitempty"`
	// Bitrate 目标码率（如 "2M"）。
	Bitrate string `json:"bitrate,omitempty"`
	// MaxRate / BufSize 峰值约束（与 Bitrate 搭配做 VBR）。
	MaxRate string `json:"max_rate,omitempty"`
	BufSize string `json:"buf_size,omitempty"`
	// Preset 编码预设（编码器相关，如 veryfast / slow）。
	Preset string `json:"preset,omitempty"`
	// PixelFormat 像素格式（如 yuv420p，浏览器/播放器兼容常用）。
	PixelFormat string `json:"pixel_format,omitempty"`
}

// AudioEncode 音频编码参数。
type AudioEncode struct {
	Codec      string `json:"codec,omitempty"`
	Copy       bool   `json:"copy,omitempty"`
	Bitrate    string `json:"bitrate,omitempty"`
	SampleRate int    `json:"sample_rate,omitempty"`
	Channels   int    `json:"channels,omitempty"`
}

// OutputSpec 产物。
type OutputSpec struct {
	Path   string `json:"path"`
	Format string `json:"format,omitempty"`
	// FastStart 把 moov 前置（网络流式播放必需）。
	FastStart bool `json:"fast_start,omitempty"`
}

// Compile 把 recipe 编译成 ffmpeg 参数（不含可执行文件本身）。
func Compile(r Recipe) ([]string, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}

	args := make([]string, 0, 48)

	// ── 输入 ──
	for _, input := range r.Inputs {
		if strings.TrimSpace(input.Framerate) != "" {
			args = append(args, "-framerate", strings.TrimSpace(input.Framerate))
		}
		if input.Loop {
			args = append(args, "-loop", "1")
		}
		args = append(args, "-i", input.Path)
	}

	// ── 视频滤镜链 ──
	if filters := videoFilters(r.Video); len(filters) > 0 {
		args = append(args, "-vf", strings.Join(filters, ","))
	}
	if filters := audioFilters(r.Audio); len(filters) > 0 {
		args = append(args, "-af", strings.Join(filters, ","))
	}

	// ── 流选择与编码 ──
	args = append(args, streamArgs(r.Video, r.Audio)...)

	// ── 容器 ──
	if format := strings.TrimSpace(r.Output.Format); format != "" {
		args = append(args, "-f", format)
	}
	if r.Output.FastStart {
		args = append(args, "-movflags", "+faststart")
	}

	args = append(args, r.Output.Path)
	return args, nil
}

// Validate 在编译前拦掉必然失败或语义矛盾的组合。
func (r Recipe) Validate() error {
	if len(r.Inputs) == 0 {
		return errors.New("recipe 至少需要一个输入")
	}
	for index, input := range r.Inputs {
		if strings.TrimSpace(input.Path) == "" {
			return fmt.Errorf("第 %d 个输入缺少 path", index+1)
		}
	}
	if strings.TrimSpace(r.Output.Path) == "" {
		return errors.New("recipe 缺少 output.path")
	}
	if r.Video == nil || r.Video.Disable {
		if r.Audio != nil && r.Audio.Disable {
			return errors.New("视频与音频都被丢弃，没有任何内容可产出")
		}
	}
	if r.Video != nil && r.Video.Scale != nil {
		if r.Video.Scale.Width <= 0 && r.Video.Scale.Height <= 0 {
			return errors.New("scale 的 width 与 height 不能同时为 0")
		}
		if r.Video.Scale.Width < 0 || r.Video.Scale.Height < 0 {
			return errors.New("scale 的宽高不能为负数（自动取值请留 0，会编译成 -2）")
		}
	}
	if r.Video != nil && r.Video.Encode != nil {
		encode := r.Video.Encode
		if encode.Copy && (encode.Codec != "" || encode.CRF != nil || encode.Bitrate != "") {
			return errors.New("视频 encode.copy 与 codec/crf/bitrate 互斥")
		}
		if encode.CRF != nil && encode.Bitrate != "" {
			return errors.New("视频 encode 的 crf 与 bitrate 互斥")
		}
		if encode.CRF != nil && (*encode.CRF < 0 || *encode.CRF > 51) {
			return errors.New("视频 encode.crf 应在 0-51")
		}
	}
	if r.Video != nil && r.Video.Trim != nil {
		if err := r.Video.Trim.validate(); err != nil {
			return fmt.Errorf("视频 trim: %w", err)
		}
	}
	if r.Audio != nil && r.Audio.Trim != nil {
		if err := r.Audio.Trim.validate(); err != nil {
			return fmt.Errorf("音频 trim: %w", err)
		}
	}
	// 给静止图配音频等场景需要显式声明输入，这里不做推断；但同一路径重复输入通常是笔误。
	seen := map[string]bool{}
	for _, input := range r.Inputs {
		key := filepath.Clean(input.Path)
		if seen[key] {
			return fmt.Errorf("输入重复: %s", input.Path)
		}
		seen[key] = true
	}
	return nil
}

func (t Trim) validate() error {
	if t.StartMs != nil && *t.StartMs < 0 {
		return errors.New("start_ms 不能为负")
	}
	if t.EndMs != nil && t.DurationMs != nil {
		return errors.New("end_ms 与 duration_ms 互斥")
	}
	if t.EndMs != nil && t.StartMs != nil && *t.EndMs <= *t.StartMs {
		return errors.New("end_ms 必须大于 start_ms")
	}
	if t.DurationMs != nil && *t.DurationMs <= 0 {
		return errors.New("duration_ms 必须为正")
	}
	return nil
}

func videoFilters(chain *VideoChain) []string {
	if chain == nil || chain.Disable {
		return nil
	}
	var filters []string
	if chain.Trim != nil {
		filters = append(filters, chain.Trim.filterArgs("trim", "setpts")...)
	}
	if chain.Crop != nil {
		filters = append(filters, fmt.Sprintf(
			"crop=%d:%d:%d:%d", chain.Crop.Width, chain.Crop.Height, chain.Crop.X, chain.Crop.Y))
	}
	if chain.Scale != nil {
		filters = append(filters, fmt.Sprintf(
			"scale=%s:%s", dimension(chain.Scale.Width), dimension(chain.Scale.Height)))
	}
	if chain.Fps != nil && chain.Fps.Value > 0 {
		filters = append(filters, "fps="+formatFloat(chain.Fps.Value))
	}
	return filters
}

func audioFilters(chain *AudioChain) []string {
	if chain == nil || chain.Disable {
		return nil
	}
	var filters []string
	if chain.Trim != nil {
		filters = append(filters, chain.Trim.filterArgs("atrim", "asetpts")...)
	}
	if chain.Volume != nil {
		if chain.Volume.GainDb != nil {
			filters = append(filters, "volume="+formatFloat(*chain.Volume.GainDb)+"dB")
		} else if chain.Volume.Factor != nil {
			filters = append(filters, "volume="+formatFloat(*chain.Volume.Factor))
		}
	}
	if chain.Resample != nil && chain.Resample.SampleRate > 0 {
		filters = append(filters, "aresample="+strconv.Itoa(chain.Resample.SampleRate))
	}
	return filters
}

// filterArgs 生成时间裁剪的滤镜片段。
//
// setpts/asetpts 重置时间戳是必须的：不重置的话产物开头会带一段"空时间"，
// 表现为播放器进度条从裁剪点开始而不是从 0 开始。
func (t Trim) filterArgs(trimName, ptsName string) []string {
	parts := make([]string, 0, 2)
	expression := make([]string, 0, 3)
	if t.StartMs != nil {
		expression = append(expression, "start="+formatSeconds(*t.StartMs))
	}
	if t.EndMs != nil {
		expression = append(expression, "end="+formatSeconds(*t.EndMs))
	}
	if t.DurationMs != nil && t.StartMs != nil {
		expression = append(expression, "end="+formatSeconds(*t.StartMs+*t.DurationMs))
	}
	if len(expression) == 0 {
		return nil
	}
	parts = append(parts, trimName+"="+strings.Join(expression, ":"), ptsName+"=PTS-STARTPTS")
	return parts
}

func streamArgs(video *VideoChain, audio *AudioChain) []string {
	args := make([]string, 0, 16)

	switch {
	case video == nil || video.Disable:
		args = append(args, "-vn")
	case video.Encode != nil:
		encode := video.Encode
		if encode.Copy {
			args = append(args, "-c:v", "copy")
		} else if encode.Codec != "" {
			args = append(args, "-c:v", encode.Codec)
			if encode.CRF != nil {
				args = append(args, "-crf", strconv.Itoa(*encode.CRF))
			}
			if encode.Bitrate != "" {
				args = append(args, "-b:v", encode.Bitrate)
			}
			if encode.MaxRate != "" {
				args = append(args, "-maxrate", encode.MaxRate)
			}
			if encode.BufSize != "" {
				args = append(args, "-bufsize", encode.BufSize)
			}
			if encode.Preset != "" {
				args = append(args, "-preset", encode.Preset)
			}
			if encode.PixelFormat != "" {
				args = append(args, "-pix_fmt", encode.PixelFormat)
			}
		}
	}

	switch {
	case audio == nil || audio.Disable:
		args = append(args, "-an")
	case audio.Encode != nil:
		encode := audio.Encode
		if encode.Copy {
			args = append(args, "-c:a", "copy")
		} else if encode.Codec != "" {
			args = append(args, "-c:a", encode.Codec)
			if encode.Bitrate != "" {
				args = append(args, "-b:a", encode.Bitrate)
			}
		}
		if encode.SampleRate > 0 {
			args = append(args, "-ar", strconv.Itoa(encode.SampleRate))
		}
		if encode.Channels > 0 {
			args = append(args, "-ac", strconv.Itoa(encode.Channels))
		}
	}

	return args
}

// dimension 把 0 编译成 -2（偶数自动），保证不产生奇数边长。
func dimension(value int) string {
	if value <= 0 {
		return "-2"
	}
	return strconv.Itoa(value)
}

func formatSeconds(milliseconds int64) string {
	return strconv.FormatFloat(float64(milliseconds)/1000.0, 'f', 3, 64)
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}
