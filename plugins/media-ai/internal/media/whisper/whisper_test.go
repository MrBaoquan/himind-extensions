package whisper

import (
	"strings"
	"testing"
)

// 这段样例照抄真实的 whisper-cli -oj 输出结构（含 BOM 以外的字段）。
const sample = `{
  "systeminfo": "...",
  "model": {"type": "small", "multilingual": true},
  "params": {"model": "ggml-small.bin", "language": "zh", "translate": false},
  "result": {"language": "zh"},
  "transcription": [
    {"timestamps": {"from": "00:00:00,000", "to": "00:00:02,600"},
     "offsets": {"from": 0, "to": 2600}, "text": " (upbeat music)"},
    {"timestamps": {"from": "00:00:06,000", "to": "00:00:11,000"},
     "offsets": {"from": 6000, "to": 11000}, "text": " 第一句歌词"},
    {"timestamps": {"from": "00:00:11,000", "to": "00:00:16,000"},
     "offsets": {"from": 11000, "to": 16000}, "text": " 第二句歌词"},
    {"timestamps": {"from": "00:00:16,000", "to": "00:00:21,000"},
     "offsets": {"from": 16000, "to": 21000}, "text": "第三句歌词"},
    {"offsets": {"from": 21700, "to": 22000}, "text": ""}
  ]
}`

func TestParseReadsSegmentsAndLanguage(t *testing.T) {
	result, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if result.Language != "zh" {
		t.Fatalf("语言解析错误: %q", result.Language)
	}
	if result.Model != "small" {
		t.Fatalf("模型解析错误: %q", result.Model)
	}
	// 空文本的段落应被忽略
	if len(result.Segments) != 4 {
		t.Fatalf("应解析出 4 段（空文本跳过），实际 %d", len(result.Segments))
	}
	if result.Segments[1].StartMs != 6000 || result.Segments[1].EndMs != 11000 {
		t.Fatalf("时间戳解析错误: %+v", result.Segments[1])
	}
	// 前后空白要清掉 —— 不清会把 "(upbeat music)" 的占位判断也带偏
	if strings.HasPrefix(result.Segments[1].Text, " ") {
		t.Fatalf("文本未去空白: %q", result.Segments[1].Text)
	}
}

// whisper 用 .NET 风格的输出时可能带 BOM；不清掉 json 直接报错。
func TestParseStripsUTF8BOM(t *testing.T) {
	payload := append([]byte{0xEF, 0xBB, 0xBF}, []byte(sample)...)
	if _, err := Parse(payload); err != nil {
		t.Fatalf("带 BOM 的输出应能解析: %v", err)
	}
}

// 纯音乐片只会得到占位标注。这一类必须能与真正的词区分开，
// 否则编排会把 "(singing in foreign language)" 当成歌词依据。
func TestPlaceholderDetection(t *testing.T) {
	placeholders := []string{
		"(upbeat music)", "(singing in foreign language)", "[Music]",
		"(applause)", "(laughter)", "(inaudible)", "(instrumental)",
	}
	for _, text := range placeholders {
		if !IsPlaceholderText(text) {
			t.Errorf("应判为占位: %q", text)
		}
	}
	real := []string{"第一句歌词", "hello world", "(这是一句带括号的歌词)", "嗯"}
	for _, text := range real {
		if IsPlaceholderText(text) {
			t.Errorf("不应判为占位: %q", text)
		}
	}
}

func TestContentSegmentsFiltersPlaceholders(t *testing.T) {
	result, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	content := result.ContentSegments()
	if len(content) != 3 {
		t.Fatalf("应剩 3 段真实内容，实际 %d: %+v", len(content), content)
	}
	for _, segment := range content {
		if IsPlaceholderText(segment.Text) {
			t.Fatalf("占位文本未被过滤: %q", segment.Text)
		}
	}
}

func TestVocalRatioComputed(t *testing.T) {
	result, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	// 跨度 0..21000，有声 2600+5000+5000+5000 = 17600 → 约 0.838
	if result.VocalRatio < 0.7 || result.VocalRatio > 0.95 {
		t.Fatalf("人声占比不合理: %v", result.VocalRatio)
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := Parse([]byte("<html>not json</html>")); err == nil {
		t.Fatal("非 JSON 应报错")
	}
}

func TestParseReportsNoSpeech(t *testing.T) {
	empty := `{"result":{"language":"en"},"transcription":[]}`
	if _, err := Parse([]byte(empty)); err != ErrNoSpeech {
		t.Fatalf("空转写应返回 ErrNoSpeech，实际 %v", err)
	}
}
