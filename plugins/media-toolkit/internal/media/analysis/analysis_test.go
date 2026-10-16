package analysis

import "testing"

// 这段样例照抄真实的 ffmpeg metadata=print 输出（含缩进与无空行分隔）。
// 用它做基准而不是自己编格式：解析器的坑恰恰在于"以为有分隔符"。
const sceneSample = `frame:0    pts:0       pts_time:0
lavfi.scd.mafd=0.000
lavfi.scd.score=0.000
frame:1    pts:533     pts_time:0.0333125
lavfi.scd.mafd=0.740
lavfi.scd.score=0.740
frame:2    pts:1067    pts_time:0.0666875
lavfi.scd.mafd=0.697
lavfi.scd.score=0.697
`

const loudnessSample = `frame:0    pts:0       pts_time:0
lavfi.r128.M=-120.691
lavfi.r128.I=-120.691
frame:1    pts:4800    pts_time:0.1
lavfi.r128.M=-18.500
lavfi.r128.I=-19.250
frame:2    pts:9600    pts_time:0.2
lavfi.r128.M=-16.000
lavfi.r128.I=-18.000
lavfi.r128.LRA=1.800
lavfi.r128.Peak=-0.500
`

// 分组之间没有空行——解析必须靠"遇到下一个 frame 行"结算。
func TestParseScenePointsWithoutBlankLines(t *testing.T) {
	points := parseScenePoints(sceneSample)
	if len(points) != 3 {
		t.Fatalf("应解析出 3 个采样点，实际 %d: %#v", len(points), points)
	}
	if points[0].TimeMs != 0 || points[0].Score != 0 {
		t.Fatalf("第 1 点不符: %#v", points[0])
	}
	if points[1].TimeMs != 33 || points[1].Score != 0.740 {
		t.Fatalf("第 2 点不符: %#v", points[1])
	}
	if points[2].TimeMs != 66 || points[2].Mafd != 0.697 {
		t.Fatalf("第 3 点不符: %#v", points[2])
	}
}

func TestParseScenePointsIgnoresEmptyInput(t *testing.T) {
	if points := parseScenePoints(""); len(points) != 0 {
		t.Fatalf("空输入应得到空结果: %#v", points)
	}
}

func TestParseLoudnessPointsCarriesMomentary(t *testing.T) {
	points := parseLoudnessPoints(loudnessSample)
	if len(points) != 3 {
		t.Fatalf("应解析出 3 个采样点，实际 %d: %#v", len(points), points)
	}
	if points[1].TimeMs != 100 || points[1].MomentaryLufs != -18.5 {
		t.Fatalf("第 2 点不符: %#v", points[1])
	}
	// 数字静音是 -120 这类极小值，属于正常取值，不该被裁掉。
	if points[0].MomentaryLufs != -120.691 {
		t.Fatalf("静音采样被改动: %#v", points[0])
	}
}

func TestLastScalarReadsFinalValue(t *testing.T) {
	if value := lastScalar(loudnessSample, "lavfi.r128.I"); value != -18 {
		t.Fatalf("应取最后一次出现的整体响度: %v", value)
	}
	if value := lastScalar(loudnessSample, "lavfi.r128.LRA"); value != 1.8 {
		t.Fatalf("响度范围解析错误: %v", value)
	}
}

func TestParsePtsTimeReadsSeconds(t *testing.T) {
	ms, ok := parsePtsTime("frame:1    pts:533     pts_time:0.0333125")
	if !ok || ms != 33 {
		t.Fatalf("pts_time 解析错误: %d %v", ms, ok)
	}
	if _, ok := parsePtsTime("frame:1 pts:533"); ok {
		t.Fatal("没有 pts_time 时应返回 false")
	}
}
