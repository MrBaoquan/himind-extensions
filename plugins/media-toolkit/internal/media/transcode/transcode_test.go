package transcode

import (
	"strings"
	"testing"

	"himind-plugin/media-toolkit/internal/media/recipe"
)

func compiled(t *testing.T, input, output string, options Options) []string {
	t.Helper()
	target, err := Build(input, output, options)
	if err != nil {
		t.Fatal(err)
	}
	args, err := recipe.Compile(target)
	if err != nil {
		t.Fatal(err)
	}
	return args
}

func hasPair(args []string, first, second string) bool {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == first && args[index+1] == second {
			return true
		}
	}
	return false
}

func TestBuildWebMUsesCompatibleDefaults(t *testing.T) {
	args := compiled(t, "in.mov", "out.webm", Options{Format: "webm", Width: 1280})
	if !hasPair(args, "-c:v", "libvpx-vp9") || !hasPair(args, "-c:a", "libopus") {
		t.Fatalf("WebM 编码默认值错误: %v", args)
	}
	if !hasPair(args, "-vf", "scale=1280:-2") {
		t.Fatalf("缩放参数错误: %v", args)
	}
}

func TestBuildINKVSupportsH265AndMP3(t *testing.T) {
	args := compiled(t, "in.mp4", "out.mkv", Options{
		Format: "mkv", VideoCodec: "h265", AudioCodec: "mp3", CRF: 24,
	})
	if !hasPair(args, "-c:v", "libx265") || !hasPair(args, "-c:a", "libmp3lame") || !hasPair(args, "-crf", "24") {
		t.Fatalf("MKV 编码参数错误: %v", args)
	}
}

func TestBuildRejectsIncompatibleCombinations(t *testing.T) {
	cases := []Options{
		{Format: "webm", VideoCodec: "h264"},
		{Format: "mp4", AudioCodec: "opus"},
		{Format: "mp4", VideoCodec: "copy", Width: 1280},
		{Format: "mp4", CRF: 20, VideoBitrateKbps: 4000},
	}
	for _, item := range cases {
		if _, err := Build("in.mp4", "out."+item.Format, item); err == nil {
			t.Fatalf("应拒绝不兼容组合: %+v", item)
		}
	}
	if _, err := Build("in.mp4", "out.webm", Options{Format: "mp4"}); err == nil || !strings.Contains(err.Error(), "扩展名") {
		t.Fatalf("扩展名不匹配应被拒绝: %v", err)
	}
}
