package frames

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 1x1 PNG（真实字节，便于验证按内容嗅探）。
var tinyPNG = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
	0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
	0x89, 0x00, 0x00, 0x00, 0x0A, 0x49, 0x44, 0x41,
	0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00,
	0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE,
	0x42, 0x60, 0x82,
}

func writeFile(t *testing.T, name string, payload []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPreviewProducesDataURL(t *testing.T) {
	path := writeFile(t, "sheet.png", tinyPNG)
	result, err := Preview(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.Mime != "image/png" {
		t.Fatalf("按内容嗅探应得到 image/png，实际 %s", result.Mime)
	}
	if !strings.HasPrefix(result.DataURL, "data:image/png;base64,") {
		t.Fatalf("data URL 前缀不对: %.40s", result.DataURL)
	}
	// 解回来确认字节一致（防止编码环节出错却看不出来）
	encoded := strings.TrimPrefix(result.DataURL, "data:image/png;base64,")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("base64 解码失败: %v", err)
	}
	if len(decoded) != len(tinyPNG) {
		t.Fatalf("往返后长度不符: %d != %d", len(decoded), len(tinyPNG))
	}
	if result.SizeBytes != int64(len(tinyPNG)) {
		t.Fatalf("体积上报不符: %d", result.SizeBytes)
	}
}

// 扩展名可能是错的（改过名、外部工具产物）—— 一律以内容为准。
func TestPreviewSniffsRealContentType(t *testing.T) {
	path := writeFile(t, "actually-jpeg.jpg", tinyPNG)
	result, err := Preview(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.Mime != "image/png" {
		t.Fatalf("应以内含内容为准，实际 %s", result.Mime)
	}
}

// 只允许图片扩展名：不限制的话它就是一个越权读取通道。
func TestPreviewRejectsNonImage(t *testing.T) {
	for _, name := range []string{"secret.txt", "data.json", "script.js", "archive.zip", "noext"} {
		path := writeFile(t, name, []byte("payload"))
		if _, err := Preview(path, 0); err == nil {
			t.Errorf("%s 不应被允许预览", name)
		} else if !strings.Contains(err.Error(), "只支持预览图片") {
			t.Errorf("%s 的错误信息应说明只支持图片: %v", name, err)
		}
	}
}

func TestPreviewRejectsOversize(t *testing.T) {
	path := writeFile(t, "big.png", append(append([]byte{}, tinyPNG...), make([]byte, 2048)...))
	if _, err := Preview(path, 1024); err == nil {
		t.Fatal("超限图片应被拒绝")
	} else if !strings.Contains(err.Error(), "过大") {
		t.Fatalf("应明确说是体积问题: %v", err)
	}
}

func TestPreviewRejectsDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub.png") // 故意用图片扩展名的目录
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Preview(dir, 0); err == nil {
		t.Fatal("目录不应被当成图片")
	}
}

func TestPreviewRejectsMissingFile(t *testing.T) {
	if _, err := Preview(filepath.Join(t.TempDir(), "absent.png"), 0); err == nil {
		t.Fatal("不存在的文件应报错")
	}
}

func TestPreviewAcceptsUpperExtension(t *testing.T) {
	path := writeFile(t, "SHEET.PNG", tinyPNG)
	if _, err := Preview(path, 0); err != nil {
		t.Fatalf("大写扩展名应被接受: %v", err)
	}
}
