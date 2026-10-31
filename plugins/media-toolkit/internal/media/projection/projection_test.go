package projection

import (
	"strings"
	"testing"
)

func TestBuildFilterNormalizesDefaults(t *testing.T) {
	filter, err := BuildFilter(Options{Source: "in.mp4", Output: "out.mp4"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"input=equirect", "output=flat", "yaw=0", "pitch=0",
		"h_fov=100", "v_fov=65", "w=1280:h=720",
	} {
		if !strings.Contains(filter, want) {
			t.Fatalf("filter 缺少 %q: %s", want, filter)
		}
	}
}

func TestBuildFilterRejectsOddDimensions(t *testing.T) {
	_, err := BuildFilter(Options{Source: "in.mp4", Output: "out.mp4", Width: 641, Height: 360})
	if err == nil {
		t.Fatal("奇数宽度应被拒绝")
	}
}

func TestBuildFilterRejectsNonMP4(t *testing.T) {
	_, err := BuildFilter(Options{Source: "in.mp4", Output: "out.mov"})
	if err == nil {
		t.Fatal("当前只允许 mp4 输出")
	}
}
