package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"himind-plugin/media-toolkit/internal/himindjsonrpc"
	"himind-plugin/media-toolkit/internal/media/analysis"
	"himind-plugin/media-toolkit/internal/media/recipe"
)

func call(t *testing.T, service *service, method string, params any) (any, *himindjsonrpc.Error) {
	t.Helper()
	payload, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return service.handle(himindjsonrpc.Request{Method: method, Params: payload})
}

func TestUnknownMethodIsRejected(t *testing.T) {
	result, rpcError := call(t, &service{}, "media.toolkit.nope", map[string]any{})
	if rpcError == nil {
		t.Fatalf("未知能力应报错，实际返回 %#v", result)
	}
	if rpcError.Code != -32601 {
		t.Fatalf("错误码应为 -32601，实际 %d", rpcError.Code)
	}
}

func TestRecipePlanCompilesWithoutExecuting(t *testing.T) {
	crf := 24
	service := &service{}
	result, rpcError := call(t, service, capRecipePlan, map[string]any{
		"recipe": recipe.Transcode("in.mp4", "out.mp4", recipe.TranscodeOptions{Width: 1280, CRF: &crf}),
	})
	if rpcError != nil {
		t.Fatal(rpcError)
	}
	payload := result.(map[string]any)
	args, ok := payload["args"].([]string)
	if !ok || len(args) == 0 {
		t.Fatalf("plan 应返回参数列表: %#v", payload)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-c:v libx264") {
		t.Fatalf("参数缺少编码器: %s", joined)
	}
	if payload["output"] != "out.mp4" {
		t.Fatalf("plan 应回报输出路径: %#v", payload["output"])
	}
}

// plan 是"开跑前先看一眼"的关口，非法 recipe 必须在这里被拦下。
func TestRecipePlanRejectsInvalidRecipe(t *testing.T) {
	crf := 20
	_, rpcError := call(t, &service{}, capRecipePlan, map[string]any{
		"recipe": recipe.Recipe{
			Inputs: []recipe.Input{{Path: "a.mp4"}},
			Video:  &recipe.VideoChain{Encode: &recipe.VideoEncode{Codec: "libx264", CRF: &crf, Bitrate: "2M"}},
			Output: recipe.OutputSpec{Path: "o.mp4"},
		},
	})
	if rpcError == nil {
		t.Fatal("crf 与 bitrate 互斥的 recipe 应被拒绝")
	}
}

func TestRecipeRunRequiresWorkspaceRoot(t *testing.T) {
	_, rpcError := call(t, &service{}, capRecipeRun, map[string]any{
		"recipe": recipe.Transcode("in.mp4", "out.mp4", recipe.TranscodeOptions{}),
	})
	if rpcError == nil {
		t.Fatal("缺少 workspace_root 时应被拒绝")
	}
	if !strings.Contains(rpcError.Message, "workspace_root") {
		t.Fatalf("错误信息应指明缺少 workspace_root: %s", rpcError.Message)
	}
}

// 工作区约束在能力层统一执行：recipe 里的输入/输出都必须落在工作区内。
func TestResolveRecipePathsRejectsEscape(t *testing.T) {
	root := t.TempDir()
	_, rpcError := resolveRecipePaths(root, recipe.Transcode(
		filepath.Join("..", "outside.mp4"), "out.mp4", recipe.TranscodeOptions{}))
	if rpcError == nil {
		t.Fatal("越界的输入路径应被拒绝")
	}

	_, rpcError = resolveRecipePaths(root, recipe.Transcode(
		"in.mp4", filepath.Join("..", "outside.mp4"), recipe.TranscodeOptions{}))
	if rpcError == nil {
		t.Fatal("越界的输出路径应被拒绝")
	}
}

func TestResolveRecipePathsAcceptsInsideAndResolvesAgainstRoot(t *testing.T) {
	root := t.TempDir()
	resolved, rpcError := resolveRecipePaths(root, recipe.Transcode(
		"in.mp4", filepath.Join("out", "result.mp4"), recipe.TranscodeOptions{}))
	if rpcError != nil {
		t.Fatal(rpcError)
	}
	if resolved.Inputs[0].Path != filepath.Join(root, "in.mp4") {
		t.Fatalf("输入未按工作区解析: %s", resolved.Inputs[0].Path)
	}
	if resolved.Output.Path != filepath.Join(root, "out", "result.mp4") {
		t.Fatalf("输出未按工作区解析: %s", resolved.Output.Path)
	}
}

func TestProbeRejectsMissingFile(t *testing.T) {
	root := t.TempDir()
	_, rpcError := call(t, &service{}, capProbe, map[string]any{
		"workspace_root": root,
		"path":           "does-not-exist.mp4",
	})
	if rpcError == nil {
		t.Fatal("不存在的输入文件应被拒绝")
	}
}

func TestProbeRejectsDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, rpcError := call(t, &service{}, capProbe, map[string]any{
		"workspace_root": root,
		"path":           "sub",
	})
	if rpcError == nil {
		t.Fatal("目录不应被当成媒体输入")
	}
}

func TestRuntimeStatusReportsUnavailableWhenFFmpegMissing(t *testing.T) {
	service := &service{toolsErr: errStub{}}
	result, rpcError := call(t, service, capRuntimeStatus, map[string]any{})
	if rpcError != nil {
		t.Fatal(rpcError)
	}
	payload := result.(map[string]any)
	if payload["available"] != false {
		t.Fatalf("ffmpeg 缺失时应报告不可用: %#v", payload)
	}
	if payload["message"] == nil {
		t.Fatal("不可用时必须给出可操作的提示")
	}
}

type errStub struct{}

func (errStub) Error() string { return "stub" }

// 硬切是单帧事件：抽稀必须保住它。
// 实测踩过：等间隔丢帧后 points 里最高只剩 5.9，而 peak_score 仍报 29.47（抽稀前算的），
// 两者自相矛盾 —— 照 points 找切点会一个都找不到。
func TestDownsampleScenePreservesSpikes(t *testing.T) {
	points := make([]analysis.ScenePoint, 0, 5000)
	for index := 0; index < 5000; index++ {
		score := 0.1
		if index == 3777 {
			score = 30.0 // 唯一的硬切
		}
		points = append(points, analysis.ScenePoint{TimeMs: int64(index * 33), Score: score})
	}
	result, note := downsampleScene(points, 500)
	if len(result) > 500 {
		t.Fatalf("抽稀后不应超过上限: %d", len(result))
	}
	var peak float64
	var peakAt int64
	for _, point := range result {
		if point.Score > peak {
			peak = point.Score
			peakAt = point.TimeMs
		}
	}
	if peak != 30.0 {
		t.Fatalf("硬切被抽稀吃掉了：最高只剩 %.2f", peak)
	}
	if peakAt != 3777*33 {
		t.Fatalf("尖峰时刻不对: %d", peakAt)
	}
	if note == "" || !strings.Contains(note, "尖峰") {
		t.Fatalf("应说明保留了尖峰: %q", note)
	}
}

// 少于上限时原样返回，且不产生多余的说明。
func TestDownsampleScenePassthrough(t *testing.T) {
	points := []analysis.ScenePoint{{TimeMs: 0, Score: 1}, {TimeMs: 100, Score: 2}}
	result, note := downsampleScene(points, 500)
	if len(result) != 2 || note != "" {
		t.Fatalf("不应改动: %d %q", len(result), note)
	}
}

// 结果必须按时间有序，否则画出来的曲线会乱。
func TestDownsampleSceneKeepsTimeOrder(t *testing.T) {
	points := make([]analysis.ScenePoint, 0, 3000)
	for index := 0; index < 3000; index++ {
		score := 0.1
		if index%700 == 0 {
			score = 2.0 // 多个尖峰
		}
		points = append(points, analysis.ScenePoint{TimeMs: int64(index * 33), Score: score})
	}
	result, _ := downsampleScene(points, 300)
	for index := 1; index < len(result); index++ {
		if result[index].TimeMs <= result[index-1].TimeMs {
			t.Fatalf("时间序被破坏 @%d: %d <= %d", index, result[index].TimeMs, result[index-1].TimeMs)
		}
	}
}

// 片尾硬切（切黑场）恰好发生在**最后一帧**。
// 实测踩过两次：先是循环只走到 count-1 把尾帧排除；再是超预算时按位置贪婪，
// 尾部的大尖峰被前面的小噪声挤掉。两者都会让整片最强的收束事件消失。
func TestDownsampleScenePreservesSpikeAtTail(t *testing.T) {
	points := make([]analysis.ScenePoint, 0, 5000)
	for index := 0; index < 5000; index++ {
		score := 0.1
		// 大量刚好超过 5×中位的小噪声，占满候选名额（模拟真实片子的常态起伏）
		if index%20 == 0 && index < 4000 {
			score = 0.6
		}
		points = append(points, analysis.ScenePoint{TimeMs: int64(index * 33), Score: score})
	}
	// 最后一帧是真正的硬切
	points[len(points)-1].Score = 30.0

	result, _ := downsampleScene(points, 1000)
	var peak float64
	for _, point := range result {
		if point.Score > peak {
			peak = point.Score
		}
	}
	if peak != 30.0 {
		t.Fatalf("尾部硬切被吃掉了：最高只剩 %.2f", peak)
	}
}

// 首帧尖峰同理（从黑场硬切入画面）。
func TestDownsampleScenePreservesSpikeAtHead(t *testing.T) {
	points := make([]analysis.ScenePoint, 0, 5000)
	for index := 0; index < 5000; index++ {
		score := 0.1
		if index%20 == 0 && index > 500 {
			score = 0.6
		}
		points = append(points, analysis.ScenePoint{TimeMs: int64(index * 33), Score: score})
	}
	points[0].Score = 25.0

	result, _ := downsampleScene(points, 1000)
	var peak float64
	for _, point := range result {
		if point.Score > peak {
			peak = point.Score
		}
	}
	if peak != 25.0 {
		t.Fatalf("首帧硬切被吃掉了：最高只剩 %.2f", peak)
	}
}
