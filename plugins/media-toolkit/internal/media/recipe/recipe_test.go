package recipe

import (
	"strings"
	"testing"
)

// contains 断言参数序列里出现某个相邻对（如 "-c:v" "libx264"）。
func contains(args []string, first, second string) bool {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == first && args[index+1] == second {
			return true
		}
	}
	return false
}

func has(args []string, value string) bool {
	for _, item := range args {
		if item == value {
			return true
		}
	}
	return false
}

func TestTranscodeCompilesCodecAndQuality(t *testing.T) {
	crf := 23
	compiled, err := Compile(Transcode("in.mp4", "out.mp4", TranscodeOptions{
		Width: 1920, CRF: &crf, Codec: "libx264",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !contains(compiled, "-c:v", "libx264") {
		t.Fatalf("缺少视频编码器: %v", compiled)
	}
	if !contains(compiled, "-crf", "23") {
		t.Fatalf("缺少 CRF: %v", compiled)
	}
	if !contains(compiled, "-vf", "scale=1920:-2") {
		t.Fatalf("缩放应把高度编译成 -2 以保证偶数边长: %v", compiled)
	}
	if compiled[len(compiled)-1] != "out.mp4" {
		t.Fatalf("输出路径应在最后: %v", compiled)
	}
}

// 这条用例守住一个实测踩过的坑：scale 用 -1 时高度可能取到奇数，
// 紧接着的 tile（拼图）会**静默**产出全黑图——退出码 0、文件正常，只是内容是黑的。
func TestScaleNeverProducesOddDimension(t *testing.T) {
	for _, options := range []TranscodeOptions{{Width: 640}, {Height: 480}, {Width: 640, Height: 480}} {
		compiled, err := Compile(Transcode("in.mp4", "out.mp4", options))
		if err != nil {
			t.Fatal(err)
		}
		if has(compiled, "scale=640:-1") || has(compiled, "scale=-1:480") {
			t.Fatalf("出现了 -1 缩放，会导致奇数边长: %v", compiled)
		}
	}
}

func TestExtractAudioDropsVideo(t *testing.T) {
	compiled, err := Compile(ExtractAudio("in.mp4", "out.mp3", ExtractAudioOptions{}))
	if err != nil {
		t.Fatal(err)
	}
	if !has(compiled, "-vn") {
		t.Fatalf("抽音频必须丢弃视频轨: %v", compiled)
	}
	if has(compiled, "-an") {
		t.Fatalf("抽音频不应丢弃音轨: %v", compiled)
	}
}

func TestTrimIsFrameAccurateAndResetsTimestamps(t *testing.T) {
	start := int64(1500)
	end := int64(3000)
	compiled, err := Compile(Recipe{
		Inputs: []Input{{Path: "in.mp4"}},
		Video:  &VideoChain{Trim: &Trim{StartMs: &start, EndMs: &end}},
		Audio:  &AudioChain{Trim: &Trim{StartMs: &start, EndMs: &end}},
		Output: OutputSpec{Path: "out.mp4"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !has(compiled, "trim=start=1.500:end=3.000,setpts=PTS-STARTPTS") {
		t.Fatalf("视频裁剪应用滤镜且重置时间戳: %v", compiled)
	}
	if !has(compiled, "atrim=start=1.500:end=3.000,asetpts=PTS-STARTPTS") {
		t.Fatalf("音频裁剪应同步且重置时间戳: %v", compiled)
	}
}

func TestValidateRejectsContradictions(t *testing.T) {
	crf := 20
	cases := map[string]Recipe{
		"crf 与 bitrate 互斥": {
			Inputs: []Input{{Path: "a.mp4"}},
			Video:  &VideoChain{Encode: &VideoEncode{Codec: "libx264", CRF: &crf, Bitrate: "2M"}},
			Output: OutputSpec{Path: "o.mp4"},
		},
		"copy 与 codec 互斥": {
			Inputs: []Input{{Path: "a.mp4"}},
			Video:  &VideoChain{Encode: &VideoEncode{Copy: true, Codec: "libx264"}},
			Output: OutputSpec{Path: "o.mp4"},
		},
		"scale 宽高同时为 0": {
			Inputs: []Input{{Path: "a.mp4"}},
			Video:  &VideoChain{Scale: &Scale{}},
			Output: OutputSpec{Path: "o.mp4"},
		},
		"音视频都被丢弃": {
			Inputs: []Input{{Path: "a.mp4"}},
			Video:  &VideoChain{Disable: true},
			Audio:  &AudioChain{Disable: true},
			Output: OutputSpec{Path: "o.mp4"},
		},
		"缺少输出": {
			Inputs: []Input{{Path: "a.mp4"}},
			Video:  &VideoChain{},
		},
		"重复输入": {
			Inputs: []Input{{Path: "a.mp4"}, {Path: "a.mp4"}},
			Video:  &VideoChain{},
			Output: OutputSpec{Path: "o.mp4"},
		},
		"trim 区间颠倒": {
			Inputs: []Input{{Path: "a.mp4"}},
			Video: &VideoChain{Trim: &Trim{
				StartMs: int64Ptr(5000), EndMs: int64Ptr(1000)}},
			Output: OutputSpec{Path: "o.mp4"},
		},
	}
	for name, item := range cases {
		if err := item.Validate(); err == nil {
			t.Errorf("%s：应被拒绝但通过了", name)
		}
	}
}

func TestCompressForWebAppliesCompatibleDefaults(t *testing.T) {
	compiled, err := Compile(CompressForWeb("in.mov", "out.mp4", CompressOptions{MaxWidth: 1280, Fps: 30}))
	if err != nil {
		t.Fatal(err)
	}
	if !contains(compiled, "-pix_fmt", "yuv420p") {
		t.Fatalf("网页播放需要 yuv420p: %v", compiled)
	}
	if !contains(compiled, "-movflags", "+faststart") {
		t.Fatalf("网页播放需要 faststart: %v", compiled)
	}
	if !strings.Contains(filtersOf(compiled), "fps=30") {
		t.Fatalf("缺少帧率限制: %v", compiled)
	}
}

// filtersOf 取出 -vf 的值（帧率等滤镜藏在同一条参数里）。
func filtersOf(args []string) string {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == "-vf" {
			return args[index+1]
		}
	}
	return ""
}

func TestPresetNamesAreStable(t *testing.T) {
	names := PresetNames()
	if len(names) < 4 {
		t.Fatalf("预设列表过短: %v", names)
	}
	if !strings.Contains(strings.Join(names, ","), "transcode") {
		t.Fatalf("缺少 transcode 预设: %v", names)
	}
}

func int64Ptr(value int64) *int64 { return &value }
