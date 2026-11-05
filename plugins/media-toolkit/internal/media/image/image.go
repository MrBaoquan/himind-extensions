// Package image 提供受控的图片格式转换。
//
// 图片任务和视频任务共用 ffmpeg 执行层，但不复用 recipe：图片的“质量”是
// 编码器相关的高层意图，直接映射成 JPEG/WebP/PNG 各自的安全参数，避免把
// ffmpeg 选项暴露给普通用户。
package image

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"himind-plugin/media-toolkit/internal/media/ffmpeg"
)

// Options 一次图片转换的用户意图。
type Options struct {
	Format       string `json:"format"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
	Quality      int    `json:"quality,omitempty"`
	Background   string `json:"background,omitempty"`
	Suffix       string `json:"suffix,omitempty"`
	NameTemplate string `json:"name_template,omitempty"`
	Overwrite    bool   `json:"overwrite,omitempty"`
}

// Output 单个产物的结果。
type Output struct {
	Input     string `json:"input"`
	Output    string `json:"output"`
	Format    string `json:"format"`
	SizeBytes int64  `json:"size_bytes"`
}

// Failure 单个输入失败。批量任务允许其他输入继续处理。
type Failure struct {
	Input string `json:"input"`
	Error string `json:"error"`
}

// BatchResult 批量结果。
type BatchResult struct {
	OK      bool      `json:"ok"`
	Format  string    `json:"format"`
	Outputs []Output  `json:"outputs"`
	Failed  []Failure `json:"failed"`
	Count   int       `json:"count"`
}

type formatSpec struct {
	Name      string
	Extension string
}

var inputExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true,
	".bmp": true, ".tif": true, ".tiff": true, ".gif": true,
}
var formats = map[string]formatSpec{
	"jpeg": {Name: "jpeg", Extension: ".jpg"},
	"jpg":  {Name: "jpeg", Extension: ".jpg"},
	"png":  {Name: "png", Extension: ".png"},
	"webp": {Name: "webp", Extension: ".webp"},
	"bmp":  {Name: "bmp", Extension: ".bmp"},
	"tiff": {Name: "tiff", Extension: ".tiff"},
	"tif":  {Name: "tiff", Extension: ".tiff"},
	"gif":  {Name: "gif", Extension: ".gif"},
}

// SupportedFormats 返回首版可输出的格式。
func SupportedFormats() []string {
	return []string{"jpeg", "png", "webp", "bmp", "tiff", "gif"}
}

// ConvertBatch 转换一组图片。预检失败不处理任何文件；执行期单个失败会记录后继续。
func ConvertBatch(ctx context.Context, tools ffmpeg.Tools, inputs []string, outputDir string, options Options) (BatchResult, error) {
	spec, err := normalizeFormat(options.Format)
	if err != nil {
		return BatchResult{}, err
	}
	if len(inputs) == 0 {
		return BatchResult{}, errors.New("至少需要一个图片输入")
	}
	if len(inputs) > 200 {
		return BatchResult{}, errors.New("单次最多转换 200 张图片")
	}
	for _, input := range inputs {
		info, statErr := os.Stat(input)
		if statErr != nil || !info.Mode().IsRegular() {
			return BatchResult{}, fmt.Errorf("输入图片不存在或不是文件: %s", input)
		}
		if !inputExtensions[strings.ToLower(filepath.Ext(input))] {
			return BatchResult{}, fmt.Errorf("不支持的输入图片扩展名: %s", filepath.Ext(input))
		}
	}
	if strings.TrimSpace(outputDir) == "" {
		return BatchResult{}, errors.New("输出目录不能为空")
	}
	if options.Quality <= 0 {
		options.Quality = 82
	}
	if options.Quality > 100 {
		return BatchResult{}, errors.New("quality 应在 1-100")
	}
	if options.Width < 0 || options.Height < 0 {
		return BatchResult{}, errors.New("宽高不能为负数")
	}
	if options.Width > 16384 || options.Height > 16384 {
		return BatchResult{}, errors.New("宽高上限为 16384")
	}
	if options.Width%2 != 0 || options.Height%2 != 0 {
		return BatchResult{}, errors.New("宽高必须是偶数，避免部分编码器静默取整")
	}
	background, err := normalizeBackground(options.Background)
	if err != nil {
		return BatchResult{}, err
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return BatchResult{}, fmt.Errorf("创建输出目录失败: %w", err)
	}

	outputs, err := PlanOutputs(inputs, outputDir, options, spec)
	if err != nil {
		return BatchResult{}, err
	}
	for _, output := range outputs {
		if sameInput(inputs, output) {
			return BatchResult{}, fmt.Errorf("输出不能覆盖输入: %s", output)
		}
		if !options.Overwrite {
			if _, statErr := os.Stat(output); statErr == nil {
				return BatchResult{}, fmt.Errorf("输出已存在，未开启覆盖: %s", output)
			} else if !errors.Is(statErr, os.ErrNotExist) {
				return BatchResult{}, fmt.Errorf("检查输出文件失败: %w", statErr)
			}
		}
	}

	result := BatchResult{Format: spec.Name, Outputs: make([]Output, 0, len(inputs)), Failed: make([]Failure, 0)}
	for index, input := range inputs {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		output := outputs[index]
		size, convertErr := convertOne(ctx, tools, input, output, spec, options, background)
		if convertErr != nil {
			result.Failed = append(result.Failed, Failure{Input: input, Error: convertErr.Error()})
			continue
		}
		result.Outputs = append(result.Outputs, Output{
			Input: input, Output: output, Format: spec.Name, SizeBytes: size,
		})
	}
	result.Count = len(result.Outputs)
	result.OK = len(result.Failed) == 0
	return result, nil
}

// PlanOutputs 计算并校验全部输出路径。供 UI 预览命名，也供能力执行前预检。
func PlanOutputs(inputs []string, outputDir string, options Options, specs ...formatSpec) ([]string, error) {
	spec := formatSpec{}
	if len(specs) > 0 {
		spec = specs[0]
	} else {
		var err error
		spec, err = normalizeFormat(options.Format)
		if err != nil {
			return nil, err
		}
	}
	template := strings.TrimSpace(options.NameTemplate)
	if template == "" {
		suffix := options.Suffix
		if suffix == "" {
			suffix = "-converted"
		}
		template = "{name}" + suffix + ".{ext}"
	}
	seen := make(map[string]bool, len(inputs))
	outputs := make([]string, 0, len(inputs))
	for index, input := range inputs {
		name := renderName(template, filepath.Base(input), index+1, strings.TrimPrefix(spec.Extension, "."))
		if name == "" || name == "." || name == ".." || filepath.Base(name) != name ||
			strings.ContainsAny(name, `/\`) {
			return nil, fmt.Errorf("输出文件名不合法: %q", name)
		}
		if strings.ToLower(filepath.Ext(name)) != spec.Extension {
			return nil, fmt.Errorf("输出文件名必须以 %s 结尾: %s", spec.Extension, name)
		}
		target := filepath.Join(outputDir, name)
		key := strings.ToLower(filepath.Clean(target))
		if seen[key] {
			return nil, fmt.Errorf("多个输入会写到同一输出文件: %s", target)
		}
		seen[key] = true
		outputs = append(outputs, target)
	}
	return outputs, nil
}

func convertOne(ctx context.Context, tools ffmpeg.Tools, input, output string, spec formatSpec, options Options, background string) (int64, error) {
	temp, err := os.CreateTemp(filepath.Dir(output), "."+filepath.Base(output)+".part-*"+spec.Extension)
	if err != nil {
		return 0, fmt.Errorf("创建临时输出失败: %w", err)
	}
	tempPath := temp.Name()
	_ = temp.Close()
	_ = os.Remove(tempPath)
	defer os.Remove(tempPath)

	args := []string{"-i", input, "-map", "0:v:0", "-frames:v", "1", "-an", "-sn", "-dn", "-map_metadata", "-1"}
	if filters := filtersFor(options, background); filters != "" {
		args = append(args, "-vf", filters)
	}
	args = append(args, qualityArgs(spec.Name, options.Quality)...)
	switch spec.Name {
	case "jpeg":
		args = append(args, "-pix_fmt", "yuvj420p")
	case "tiff":
		args = append(args, "-compression_algo", "deflate")
	case "gif":
		args = append(args, "-loop", "0")
	}
	args = append(args, tempPath)

	if _, err := tools.Run(ctx, ffmpeg.RunOptions{Args: args}); err != nil {
		return 0, err
	}
	info, err := os.Stat(tempPath)
	if err != nil || info.Size() == 0 {
		return 0, fmt.Errorf("转换未产出有效文件")
	}
	if options.Overwrite {
		if removeErr := os.Remove(output); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return 0, fmt.Errorf("替换已有输出失败: %w", removeErr)
		}
	} else if _, statErr := os.Stat(output); statErr == nil {
		return 0, fmt.Errorf("输出在执行期间已被创建: %s", output)
	}
	if err := os.Rename(tempPath, output); err != nil {
		return 0, fmt.Errorf("写入最终输出失败: %w", err)
	}
	if stat, err := os.Stat(output); err == nil {
		return stat.Size(), nil
	}
	return info.Size(), nil
}

func filtersFor(options Options, background string) string {
	filters := make([]string, 0, 2)
	switch {
	case options.Width > 0 && options.Height > 0:
		filters = append(filters,
			fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease", options.Width, options.Height),
			fmt.Sprintf("pad=%d:%d:(ow-iw)/2:(oh-ih)/2:color=%s", options.Width, options.Height, background))
	case options.Width > 0:
		filters = append(filters, fmt.Sprintf("scale=%d:-2", options.Width))
	case options.Height > 0:
		filters = append(filters, fmt.Sprintf("scale=-2:%d", options.Height))
	}
	return strings.Join(filters, ",")
}

func qualityArgs(format string, quality int) []string {
	switch format {
	case "jpeg":
		// ffmpeg 的 JPEG qscale 越小越好，2-31；把用户质量反向映射。
		qscale := 31 - int(float64(quality)*29/100)
		if qscale < 2 {
			qscale = 2
		}
		return []string{"-q:v", strconv.Itoa(qscale)}
	case "webp":
		return []string{"-quality", strconv.Itoa(quality), "-compression_level", "4"}
	case "png":
		level := 9 - quality*6/100
		if level < 1 {
			level = 1
		}
		if level > 9 {
			level = 9
		}
		return []string{"-compression_level", strconv.Itoa(level)}
	default:
		return nil
	}
}

func normalizeFormat(value string) (formatSpec, error) {
	key := strings.ToLower(strings.TrimSpace(value))
	spec, ok := formats[key]
	if !ok {
		return formatSpec{}, fmt.Errorf("不支持的图片格式: %s", value)
	}
	return spec, nil
}

func normalizeBackground(value string) (string, error) {
	text := strings.TrimSpace(value)
	if text == "" {
		text = "#ffffff"
	}
	text = strings.TrimPrefix(text, "#")
	if len(text) != 6 {
		return "", errors.New("背景色必须是 #RRGGBB")
	}
	if _, err := strconv.ParseUint(text, 16, 24); err != nil {
		return "", errors.New("背景色必须是 #RRGGBB")
	}
	return "0x" + strings.ToUpper(text), nil
}

func renderName(template, source string, index int, extension string) string {
	name := strings.TrimSuffix(source, filepath.Ext(source))
	result := strings.ReplaceAll(template, "{name}", name)
	result = strings.ReplaceAll(result, "{index}", strconv.Itoa(index))
	result = strings.ReplaceAll(result, "{ext}", extension)
	return result
}

func sameInput(inputs []string, output string) bool {
	cleanOutput := filepath.Clean(output)
	for _, input := range inputs {
		if strings.EqualFold(filepath.Clean(input), cleanOutput) {
			return true
		}
	}
	return false
}
