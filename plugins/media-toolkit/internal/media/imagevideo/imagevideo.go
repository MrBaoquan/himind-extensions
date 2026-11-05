// Package imagevideo 把单图或图片序列转换成视频 recipe。
package imagevideo

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"himind-plugin/media-toolkit/internal/media/recipe"
)

var imageExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true,
	".bmp": true, ".tif": true, ".tiff": true, ".gif": true,
}
var sequenceVerb = regexp.MustCompile(`%0?[0-9]*d`)

// Source 一个已经解析到工作区内的图片输入。
type Source struct {
	Path       string
	IsSequence bool
	FrameCount int
	FirstFrame string
	DurationMs int64
}

// Options 图片转视频意图。
type Options struct {
	FPS        float64 `json:"fps,omitempty"`
	DurationMs int64   `json:"duration_ms,omitempty"`
	Width      int     `json:"width,omitempty"`
	Height     int     `json:"height,omitempty"`
	CRF        int     `json:"crf,omitempty"`
}

// Plan 转换计划。
type Plan struct {
	Recipe              recipe.Recipe `json:"recipe"`
	Source              Source        `json:"source"`
	EstimatedDurationMs int64         `json:"estimated_duration_ms"`
}

// InspectSource 判断输入是单图还是 ffmpeg 序列通配符，并验证首帧存在。
func InspectSource(path string, fps float64) (Source, error) {
	if strings.TrimSpace(path) == "" {
		return Source{}, errors.New("图片输入不能为空")
	}
	if sequenceVerb.MatchString(path) {
		if len(sequenceVerb.FindAllString(path, -1)) != 1 {
			return Source{}, errors.New("图片序列只能包含一个 %0Nd 占位符")
		}
		verb := sequenceVerb.FindString(path)
		wildcardPath := strings.Replace(path, verb, "*", 1)
		rawMatches, err := filepath.Glob(wildcardPath)
		if err != nil {
			return Source{}, fmt.Errorf("图片序列模式无效: %w", err)
		}
		// filepath.Glob 不认识 ffmpeg 的 %04d，所以先按 * 找候选，再用数字占位约束筛回。
		parts := strings.SplitN(path, verb, 2)
		framePattern := regexp.MustCompile("^" + regexp.QuoteMeta(parts[0]) + `\d+` + regexp.QuoteMeta(parts[1]) + "$")
		matches := make([]string, 0, len(rawMatches))
		for _, match := range rawMatches {
			if !framePattern.MatchString(match) {
				continue
			}
			info, statErr := os.Stat(match)
			if statErr != nil || !info.Mode().IsRegular() {
				continue
			}
			matches = append(matches, match)
		}
		if len(matches) == 0 {
			return Source{}, errors.New("图片序列没有匹配到文件")
		}
		if len(matches) > 200000 {
			return Source{}, errors.New("图片序列超过 200000 帧，请拆分处理")
		}
		if !imageExtensions[strings.ToLower(filepath.Ext(matches[0]))] {
			return Source{}, fmt.Errorf("不支持的图片序列扩展名: %s", filepath.Ext(matches[0]))
		}
		if fps <= 0 {
			fps = 30
		}
		return Source{
			Path: path, IsSequence: true, FrameCount: len(matches), FirstFrame: matches[0],
			DurationMs: int64(float64(len(matches)) / fps * 1000),
		}, nil
	}
	if !imageExtensions[strings.ToLower(filepath.Ext(path))] {
		return Source{}, fmt.Errorf("不支持的图片扩展名: %s", filepath.Ext(path))
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return Source{}, errors.New("图片输入不存在或不是文件")
	}
	return Source{Path: path, FirstFrame: path}, nil
}

// Build 生成图片转视频 recipe。
func Build(source Source, output string, options Options) (Plan, error) {
	extension := strings.ToLower(filepath.Ext(output))
	if extension != ".mp4" && extension != ".mov" && extension != ".mkv" {
		return Plan{}, errors.New("图片转视频输出必须使用 .mp4 / .mov / .mkv")
	}
	if options.FPS <= 0 {
		options.FPS = 30
	}
	if options.FPS > 120 {
		return Plan{}, errors.New("fps 应在 1-120")
	}
	if options.Width < 0 || options.Height < 0 || options.Width > 7680 || options.Height > 4320 {
		return Plan{}, errors.New("宽高超出允许范围")
	}
	if options.Width%2 != 0 || options.Height%2 != 0 {
		return Plan{}, errors.New("宽高必须是偶数")
	}
	if options.CRF < 0 || options.CRF > 51 {
		return Plan{}, errors.New("CRF 应在 0-51")
	}
	durationMs := options.DurationMs
	if source.IsSequence {
		durationMs = source.DurationMs
	} else {
		if durationMs <= 0 {
			durationMs = 5000
		}
		if durationMs > 24*60*60*1000 {
			return Plan{}, errors.New("单图视频时长不能超过 24 小时")
		}
	}
	input := recipe.Input{Path: source.Path}
	if source.IsSequence {
		input.Framerate = strconv.FormatFloat(options.FPS, 'f', -1, 64)
	} else {
		input.Loop = true
	}
	var crf *int
	if options.CRF > 0 {
		value := options.CRF
		crf = &value
	}
	target := recipe.ImagesToVideo(input, output, recipe.ImagesToVideoOptions{
		Framerate: options.FPS, TotalMs: &durationMs,
		Width: options.Width, Height: options.Height, CRF: crf,
	})
	target.Output.FastStart = extension == ".mp4" || extension == ".mov"
	if input.Loop {
		target.Output.DurationMs = &durationMs
	}
	return Plan{Recipe: target, Source: source, EstimatedDurationMs: durationMs}, nil
}

// AttachAudio 在已有视频计划上附加一条音轨，并让输出以较短流结束。
func AttachAudio(plan Plan, audioPath string) Plan {
	plan.Recipe.Inputs = append(plan.Recipe.Inputs, recipe.Input{Path: audioPath})
	plan.Recipe.Audio = &recipe.AudioChain{Encode: &recipe.AudioEncode{Codec: "aac", Bitrate: "160k"}}
	plan.Recipe.Output.Shortest = true
	return plan
}
