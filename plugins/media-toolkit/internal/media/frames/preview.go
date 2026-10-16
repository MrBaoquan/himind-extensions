package frames

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// 视图要显示产物（拼图、抽帧），但扩展视图的桥接只暴露**能力调用** ——
// 没有"直接读工作区文件"的通道。所以补一个受约束的预览能力：
// 把工作区内的图片读成 data URL 交给页面显示。
//
// 约束是刻意的：只读、只允许图片扩展名、有体积上限。
// 若不加限制，"读取任意文件"会变成一个越权读取通道。

// maxPreviewBytes 单次预览的体积上限。
//
// 为什么要有：拼图与抽帧都是几百 KB 量级，但用户可能指向一个几十 MB 的源图。
// 转成 base64 后体积还要涨三分之一，直接塞进 JSON 会把调用链拖垮。
const maxPreviewBytes = 12 * 1024 * 1024

// allowedPreviewExt 允许预览的扩展名与 MIME。
var allowedPreviewExt = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".webp": "image/webp",
	".gif":  "image/gif",
	".bmp":  "image/bmp",
}

// PreviewResult 一次预览的结果。
type PreviewResult struct {
	Path      string `json:"path"`
	Mime      string `json:"mime"`
	SizeBytes int64  `json:"size_bytes"`
	DataURL   string `json:"data_url"`
}

// Preview 把工作区内的图片读成 data URL。
func Preview(path string, maxBytes int64) (PreviewResult, error) {
	if maxBytes <= 0 {
		maxBytes = maxPreviewBytes
	}
	ext := strings.ToLower(filepath.Ext(path))
	mime, ok := allowedPreviewExt[ext]
	if !ok {
		// 明确说"只能预览图片"，而不是含糊地报读取失败 ——
		// 否则调用方会以为是路径或权限问题。
		return PreviewResult{}, fmt.Errorf("只支持预览图片（%s），当前文件是 %s",
			strings.Join(allowedExtensions(), " / "), ext)
	}
	info, err := os.Stat(path)
	if err != nil {
		return PreviewResult{}, fmt.Errorf("读取失败: %w", err)
	}
	if info.IsDir() {
		return PreviewResult{}, errors.New("目标是目录，不是图片文件")
	}
	if info.Size() > maxBytes {
		return PreviewResult{}, fmt.Errorf("图片过大（%.1f MB，上限 %.0f MB），请先缩小再预览",
			float64(info.Size())/(1024*1024), float64(maxBytes)/(1024*1024))
	}

	payload, err := os.ReadFile(path)
	if err != nil {
		return PreviewResult{}, fmt.Errorf("读取失败: %w", err)
	}
	// 按内容嗅探真实类型：扩展名可能是错的（改过名、外部工具产物），
	// 报错时要基于事实。嗅探不出就信扩展名。
	if sniffed := http.DetectContentType(payload); strings.HasPrefix(sniffed, "image/") {
		mime = sniffed
	}

	return PreviewResult{
		Path:      filepath.Base(path),
		Mime:      mime,
		SizeBytes: info.Size(),
		DataURL:   "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(payload),
	}, nil
}

func allowedExtensions() []string {
	extensions := make([]string, 0, len(allowedPreviewExt))
	for ext := range allowedPreviewExt {
		extensions = append(extensions, ext)
	}
	// 固定顺序，避免错误信息每次不一样（也让测试可断言）。
	for i := 0; i < len(extensions); i++ {
		for j := i + 1; j < len(extensions); j++ {
			if extensions[j] < extensions[i] {
				extensions[i], extensions[j] = extensions[j], extensions[i]
			}
		}
	}
	return extensions
}
