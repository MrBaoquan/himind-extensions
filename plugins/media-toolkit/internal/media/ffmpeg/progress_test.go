package ffmpeg

import (
	"strings"
	"testing"
)

func TestParseProgressConvertsMicrosecondsToMilliseconds(t *testing.T) {
	payload := "frame=30\nout_time_us=1500000\nout_time_ms=1500000\nspeed=2.0x\nprogress=end\n"
	var got Progress
	parseProgress(strings.NewReader(payload), func(progress Progress) { got = progress })
	if got.OutTimeMs != 1500 {
		t.Fatalf("out_time 应换算为 1500ms，实际 %d", got.OutTimeMs)
	}
	if !got.Finished {
		t.Fatal("未识别 progress=end")
	}
}
