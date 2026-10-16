// Package analysis 从画面与声音里抽出**连续信号**，供编排判断"哪里该配动作"。
//
// 设计取向：不产出"切点列表"这类二元判断，而是产出**曲线**。
//
// 原因来自实测：拿一条真实的音乐视频（大量叠化转场）跑场景切分，逐帧变化分数的最高值只有 9.0，
// 按常规阈值 10 得到 **0 个切点**。也就是说阈值式硬切检测对真实内容并不可靠，
// 而同一个信号当成"画面活动强度"曲线使用却始终有效——音乐段落、采访镜头、红毯画面的
// 强度分布一眼可辨。所以这里把分数原样交出去，让调用方（人或 AI）决定怎么用。
package analysis

import (
	"bufio"
	"context"
	"fmt"
	"strconv"
	"strings"

	"himind-plugin/media-toolkit/internal/media/ffmpeg"
)

// ScenePoint 一个采样点的画面活动强度。
type ScenePoint struct {
	TimeMs int64   `json:"time_ms"`
	Score  float64 `json:"score"`
	Mafd   float64 `json:"mafd"`
}

// SceneResult 画面活动曲线的抽取结果。
type SceneResult struct {
	Points []ScenePoint `json:"points"`
	// Threshold 本次使用的检测阈值（仅影响 scdet 内部的"切点"标记，分数本身不受影响）。
	Threshold float64 `json:"threshold"`
	// PeakScore / MeanScore 便于调用方一眼判断"这条片子有没有硬切"。
	PeakScore float64 `json:"peak_score"`
	MeanScore float64 `json:"mean_score"`
	// CutCount 按阈值触发切点的数量。
	CutCount int `json:"cut_count"`
	// Notes 采样过程中的提醒（如分数整体偏低）。
	Notes []string `json:"notes,omitempty"`
}

// SceneScore 抽取逐帧画面活动分数。
//
// 用 metadata=print:file=- 从 stdout 读结构化输出，而不是去解析 stderr：
// stderr 是给人看的，格式随版本变；metadata 是给程序看的键值对，稳定得多。
func SceneScore(ctx context.Context, tools ffmpeg.Tools, path string, threshold float64) (SceneResult, error) {
	if threshold <= 0 {
		threshold = 10
	}
	filter := fmt.Sprintf("scdet=threshold=%s,metadata=print:file=-", formatFloat(threshold))
	stdout, err := tools.Capture(ctx, []string{
		"-i", path,
		"-vf", filter,
		"-an",
		"-f", "null", "-",
	})
	result := SceneResult{Threshold: threshold}
	points := parseScenePoints(stdout)
	result.Points = points
	if err != nil {
		return result, err
	}
	if len(points) == 0 {
		result.Notes = append(result.Notes, "没有采到任何画面分数，请确认文件含视频轨")
		return result, nil
	}

	var sum, peak float64
	for _, point := range points {
		sum += point.Score
		if point.Score > peak {
			peak = point.Score
		}
	}
	result.PeakScore = round3(peak)
	result.MeanScore = round3(sum / float64(len(points)))
	for _, point := range points {
		if point.Score > threshold {
			result.CutCount++
		}
	}
	if result.CutCount == 0 && peak < threshold {
		result.Notes = append(result.Notes, fmt.Sprintf(
			"最强画面变化只有 %.2f（低于阈值 %.2f），该片更可能是叠化/柔切；"+
				"建议改用强度曲线而非切点来判断段落", peak, threshold))
	}
	return result, nil
}

// LoudnessPoint 一个采样点的响度。
type LoudnessPoint struct {
	TimeMs       int64   `json:"time_ms"`
	MomentaryLufs float64 `json:"momentary_lufs"`
	IntegratedLufs float64 `json:"integrated_lufs,omitempty"`
}

// LoudnessResult 响度分析结果。
type LoudnessResult struct {
	Points         []LoudnessPoint `json:"points"`
	IntegratedLufs float64         `json:"integrated_lufs"`
	LoudnessRangeLu float64        `json:"loudness_range_lu"`
	PeakDbtp       float64         `json:"peak_dbtp,omitempty"`
	Notes          []string        `json:"notes,omitempty"`
}

// Loudness 抽取响度曲线与整体响度。
//
// 瞬时值静音处会是 -120 LUFS 之类的极小值，那是正常表示（数字静音），不做裁剪——
// 调用方要据此判断"这里有没有声音"，裁掉反而丢信息。
func Loudness(ctx context.Context, tools ffmpeg.Tools, path string) (LoudnessResult, error) {
	stdout, err := tools.Capture(ctx, []string{
		"-i", path,
		"-af", "ebur128=metadata=1,ametadata=print:file=-",
		"-vn",
		"-f", "null", "-",
	})
	result := LoudnessResult{}
	if err != nil {
		return result, err
	}
	result.Points = parseLoudnessPoints(stdout)
	result.IntegratedLufs = round3(lastScalar(stdout, "lavfi.r128.I"))
	result.LoudnessRangeLu = round3(lastScalar(stdout, "lavfi.r128.LRA"))
	result.PeakDbtp = round3(lastScalar(stdout, "lavfi.r128.Peak"))
	if len(result.Points) == 0 {
		result.Notes = append(result.Notes, "没有采到响度采样，请确认文件含音轨")
	}
	return result, nil
}

// ── 解析 ──────────────────────────────────────────────

// parseScenePoints 解析 metadata=print 的成组输出。
//
// 关键：分组之间**没有空行**——实测格式是 frame 行紧跟若干 lavfi.* 行，然后下一个 frame 行。
// 因此必须在遇到新的 frame 行时就结算上一条，而不能等空行（等空行会一条都读不出来）。
func parseScenePoints(text string) []ScenePoint {
	var points []ScenePoint
	var current ScenePoint
	flush := func() {
		points = append(points, current)
		current = ScenePoint{}
	}
	started := false

	reader := bufio.NewScanner(strings.NewReader(text))
	reader.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for reader.Scan() {
		line := strings.TrimSpace(reader.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "frame:") {
			if started {
				flush()
			}
			started = true
			if ms, ok := parsePtsTime(line); ok {
				current.TimeMs = ms
			}
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		switch strings.TrimSpace(key) {
		case "lavfi.scd.score":
			current.Score = parseFloat(value)
		case "lavfi.scd.mafd":
			current.Mafd = parseFloat(value)
		}
	}
	if started {
		flush()
	}
	return points
}

func parseLoudnessPoints(text string) []LoudnessPoint {
	var points []LoudnessPoint
	var current LoudnessPoint
	flush := func() {
		points = append(points, current)
		current = LoudnessPoint{}
	}
	started := false
	hasMomentary := false

	reader := bufio.NewScanner(strings.NewReader(text))
	reader.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for reader.Scan() {
		line := strings.TrimSpace(reader.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "frame:") {
			if started {
				flush()
			}
			started = true
			hasMomentary = false
			if ms, ok := parsePtsTime(line); ok {
				current.TimeMs = ms
			}
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		switch strings.TrimSpace(key) {
		case "lavfi.r128.M":
			current.MomentaryLufs = parseFloat(value)
			hasMomentary = true
		case "lavfi.r128.I":
			current.IntegratedLufs = parseFloat(value)
		}
	}
	if started && hasMomentary {
		flush()
	}
	return points
}

// parsePtsTime 从 "frame:12  pts:400  pts_time:0.4" 里取出毫秒。
func parsePtsTime(line string) (int64, bool) {
	for _, field := range strings.Fields(line) {
		if value, found := strings.CutPrefix(field, "pts_time:"); found {
			seconds, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return 0, false
			}
			return int64(seconds * 1000), true
		}
	}
	return 0, false
}

// lastScalar 取某个键最后一次出现的值（如整体响度、响度范围）。
func lastScalar(text, key string) float64 {
	value := 0.0
	reader := bufio.NewScanner(strings.NewReader(text))
	reader.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	prefix := key + "="
	for reader.Scan() {
		line := strings.TrimSpace(reader.Text())
		if rest, found := strings.CutPrefix(line, prefix); found {
			value = parseFloat(rest)
		}
	}
	return value
}

func parseFloat(value string) float64 {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return 0
	}
	return parsed
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func round3(value float64) float64 {
	scaled := value * 1000
	if scaled >= 0 {
		return float64(int64(scaled+0.5)) / 1000
	}
	return float64(int64(scaled-0.5)) / 1000
}
