// Package projection 把全景/等距柱状视频展开成可分析的平面主视图。
//
// 为什么需要它：等距柱状画面里，固定 2D 坐标并不等于观看前向；直接把平铺
// contact sheet 当普通画面看，会把侧方物体/镜头转向误判成平台运动。动作编排
// 应先明确“主前向”，再基于同一个前向视图做画面语义与信号分析。
package projection

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"himind-plugin/media-toolkit/internal/media/ffmpeg"
	"himind-plugin/media-toolkit/internal/media/workspace"
)

const (
	defaultWidth  = 1280
	defaultHeight = 720
	defaultHFov   = 100.0
	defaultVFov   = 65.0
	defaultCRF    = 22
)

// Options 一次主前向投影请求。
type Options struct {
	Source   string
	Output   string
	YawDeg   float64
	PitchDeg float64
	HFovDeg  float64
	VFovDeg  float64
	Width    int
	Height   int
	CRF      int
}

// Result 投影产物摘要。
type Result struct {
	Path     string  `json:"path"`
	YawDeg   float64 `json:"yaw_deg"`
	PitchDeg float64 `json:"pitch_deg"`
	HFovDeg  float64 `json:"h_fov_deg"`
	VFovDeg  float64 `json:"v_fov_deg"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	Size     int64   `json:"size_bytes"`
}

func normalize(options Options) (Options, error) {
	if strings.TrimSpace(options.Source) == "" {
		return options, fmt.Errorf("source 不能为空")
	}
	if strings.TrimSpace(options.Output) == "" {
		return options, fmt.Errorf("output 不能为空")
	}
	if !strings.EqualFold(filepath.Ext(options.Output), ".mp4") {
		return options, fmt.Errorf("output 必须是 .mp4")
	}
	if options.YawDeg < -360 || options.YawDeg > 360 {
		return options, fmt.Errorf("yaw_deg 必须在 -360..360")
	}
	if options.PitchDeg < -89 || options.PitchDeg > 89 {
		return options, fmt.Errorf("pitch_deg 必须在 -89..89")
	}
	if options.HFovDeg <= 0 {
		options.HFovDeg = defaultHFov
	}
	if options.VFovDeg <= 0 {
		options.VFovDeg = defaultVFov
	}
	if options.HFovDeg > 179 {
		return options, fmt.Errorf("h_fov_deg 必须小于 180")
	}
	if options.VFovDeg > 179 {
		return options, fmt.Errorf("v_fov_deg 必须小于 180")
	}
	if options.Width <= 0 {
		options.Width = defaultWidth
	}
	if options.Height <= 0 {
		options.Height = defaultHeight
	}
	if options.Width < 320 || options.Width > 7680 || options.Width%2 != 0 {
		return options, fmt.Errorf("width 必须是 320..7680 之间的偶数")
	}
	if options.Height < 240 || options.Height > 4320 || options.Height%2 != 0 {
		return options, fmt.Errorf("height 必须是 240..4320 之间的偶数")
	}
	if options.CRF <= 0 {
		options.CRF = defaultCRF
	}
	if options.CRF < 12 || options.CRF > 35 {
		return options, fmt.Errorf("crf 必须在 12..35")
	}
	return options, nil
}

// BuildFilter 生成可测试的 v360 滤镜字符串。
func BuildFilter(options Options) (string, error) {
	normalized, err := normalize(options)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"v360=input=equirect:output=flat:yaw=%s:pitch=%s:h_fov=%s:v_fov=%s:w=%d:h=%d",
		formatFloat(normalized.YawDeg), formatFloat(normalized.PitchDeg),
		formatFloat(normalized.HFovDeg), formatFloat(normalized.VFovDeg),
		normalized.Width, normalized.Height,
	), nil
}

// Project 执行等距柱状到主前向平面的转换，并保留音轨。
func Project(ctx context.Context, tools ffmpeg.Tools, options Options) (Result, error) {
	normalized, err := normalize(options)
	if err != nil {
		return Result{}, err
	}
	filter, err := BuildFilter(normalized)
	if err != nil {
		return Result{}, err
	}
	if err := workspace.EnsureParentDir(normalized.Output); err != nil {
		return Result{}, fmt.Errorf("创建输出目录失败: %w", err)
	}
	_, err = tools.Run(ctx, ffmpeg.RunOptions{Args: []string{
		"-i", normalized.Source,
		"-vf", filter,
		"-c:v", "libx264",
		"-crf", fmt.Sprintf("%d", normalized.CRF),
		"-preset", "veryfast",
		"-pix_fmt", "yuv420p",
		"-c:a", "aac",
		"-b:a", "160k",
		"-movflags", "+faststart",
		normalized.Output,
	}})
	if err != nil {
		return Result{}, err
	}
	stat, err := os.Stat(normalized.Output)
	if err != nil || stat.Size() == 0 {
		return Result{}, fmt.Errorf("投影产物缺失或为空")
	}
	return Result{
		Path: normalized.Output, YawDeg: normalized.YawDeg, PitchDeg: normalized.PitchDeg,
		HFovDeg: normalized.HFovDeg, VFovDeg: normalized.VFovDeg,
		Width: normalized.Width, Height: normalized.Height, Size: stat.Size(),
	}, nil
}

func formatFloat(value float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.3f", value), "0"), ".")
}
