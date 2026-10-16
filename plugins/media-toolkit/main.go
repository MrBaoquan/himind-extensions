// media-toolkit —— HiMind 媒体工具集插件。
//
// 底座分三层，职责不串：
//
//	recipe   把「要做什么」表达成声明式步骤，再编译成 ffmpeg 参数（新增需求 = 新增预设）
//	ffmpeg   定位/执行外部工具，进度与取消统一（执行外部进程的唯一出口）
//	能力层   校验输入、解析工作区路径、整形输出（本文件）
//
// 这样做而不是"一个需求一个能力各拼各的 argv"：需求会持续增加（转码、压缩、图转视频、
// 抽音频、GIF……），散着写必然腐烂；而类型化的 recipe 也顺带把"接受任意命令"这个
// 安全口子从设计上焊死。
package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"himind-plugin/media-toolkit/internal/himindjsonrpc"
	"himind-plugin/media-toolkit/internal/media/analysis"
	"himind-plugin/media-toolkit/internal/media/ffmpeg"
	"himind-plugin/media-toolkit/internal/media/frames"
	"himind-plugin/media-toolkit/internal/media/job"
	"himind-plugin/media-toolkit/internal/media/probe"
	"himind-plugin/media-toolkit/internal/media/recipe"
	"himind-plugin/media-toolkit/internal/media/workspace"
)

// 能力 ID。集中定义，避免字符串散落各处导致改名时漏改。
const (
	capRuntimeStatus = "media.toolkit.runtime.status"
	capProbe         = "media.toolkit.probe"
	capRecipePlan    = "media.toolkit.recipe.plan"
	capRecipeRun     = "media.toolkit.recipe.run"
	capJobStatus     = "media.toolkit.job.status"
	capJobCancel     = "media.toolkit.job.cancel"
	capJobList       = "media.toolkit.job.list"
	capSceneScore    = "media.toolkit.scene.score"
	capAudioLoudness = "media.toolkit.audio.loudness"
	capContactSheet  = "media.toolkit.frames.contact-sheet"
	capFrameExtract  = "media.toolkit.frames.extract"
	capPreviewImage  = "media.toolkit.preview.image"
	capFilesList     = "media.toolkit.files.list"
)

// service 一次进程内的服务状态。外部工具只定位一次并复用。
type service struct {
	tools    ffmpeg.Tools
	toolsErr error
	store    *job.Store
}

func newService() *service {
	instance := &service{}
	instance.tools, instance.toolsErr = ffmpeg.Locate()
	if instance.toolsErr == nil {
		store, err := job.NewStore(instance.tools)
		if err == nil {
			instance.store = store
		}
	}
	return instance
}

func (s *service) toolsOrError() (ffmpeg.Tools, *himindjsonrpc.Error) {
	if s.toolsErr != nil || s.tools.FFmpeg == "" {
		return ffmpeg.Tools{}, &himindjsonrpc.Error{
			Code:    -32000,
			Message: "未找到 ffmpeg。请在本机安装 ffmpeg 并确保它在 PATH 中，然后重试。",
		}
	}
	return s.tools, nil
}

var defaultService = newService()

func main() {
	_ = himindjsonrpc.Serve(os.Stdin, os.Stdout, defaultService.handle)
}

func (s *service) handle(request himindjsonrpc.Request) (any, *himindjsonrpc.Error) {
	switch request.Method {

	case capRuntimeStatus:
		return s.runtimeStatus()

	case capProbe:
		var input probeInput
		if rpcError := himindjsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		return s.probe(input)

	case capRecipePlan:
		var input planInput
		if rpcError := himindjsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		return s.recipePlan(input)

	case capRecipeRun:
		var input runInput
		if rpcError := himindjsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		return s.recipeRun(input)

	case capJobStatus:
		var input jobIDInput
		if rpcError := himindjsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		return s.jobStatus(input)

	case capJobCancel:
		var input jobIDInput
		if rpcError := himindjsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		return s.jobCancel(input)

	case capJobList:
		var input jobListInput
		if rpcError := himindjsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		return s.jobList(input)

	case capSceneScore:
		var input sceneInput
		if rpcError := himindjsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		return s.sceneScore(input)

	case capAudioLoudness:
		var input loudnessInput
		if rpcError := himindjsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		return s.audioLoudness(input)

	case capContactSheet:
		var input sheetInput
		if rpcError := himindjsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		return s.contactSheet(input)

	case capFrameExtract:
		var input extractInput
		if rpcError := himindjsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		return s.frameExtract(input)

	case capPreviewImage:
		var input previewInput
		if rpcError := himindjsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		return s.previewImage(input)

	case capFilesList:
		var input filesInput
		if rpcError := himindjsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		return s.filesList(input)
	}

	return nil, &himindjsonrpc.Error{
		Code:    -32601,
		Message: fmt.Sprintf("未知能力: %s", request.Method),
	}
}

// ── 输入 ──────────────────────────────────────────────

type probeInput struct {
	WorkspaceRoot string `json:"workspace_root"`
	Path          string `json:"path"`
}

type planInput struct {
	Recipe recipe.Recipe `json:"recipe"`
}

type runInput struct {
	WorkspaceRoot string        `json:"workspace_root"`
	Recipe        recipe.Recipe `json:"recipe"`
	// Kind 作业类型标签，仅用于展示与统计。
	Kind string `json:"kind,omitempty"`
}

type jobIDInput struct {
	JobID string `json:"job_id"`
}

type jobListInput struct {
	Limit int `json:"limit"`
}

type sceneInput struct {
	WorkspaceRoot string  `json:"workspace_root"`
	Path          string  `json:"path"`
	Threshold     float64 `json:"threshold"`
	// MaxPoints 采样点上限；超出时等间隔抽稀，避免长片返回几十万个点。
	MaxPoints int `json:"max_points"`
}

type loudnessInput struct {
	WorkspaceRoot string `json:"workspace_root"`
	Path          string `json:"path"`
	MaxPoints     int    `json:"max_points"`
}

type sheetInput struct {
	WorkspaceRoot string `json:"workspace_root"`
	Path          string `json:"path"`
	Output        string `json:"output"`
	Columns       int    `json:"columns"`
	Rows          int    `json:"rows"`
	IntervalMs    int64  `json:"interval_ms"`
	TileWidth     int    `json:"tile_width"`
	Quality       int    `json:"quality"`
}

type filesInput struct {
	WorkspaceRoot string `json:"workspace_root"`
	Kind          string `json:"kind"`
	MaxDepth      int    `json:"max_depth"`
	Limit         int    `json:"limit"`
}

type previewInput struct {
	WorkspaceRoot string `json:"workspace_root"`
	Path          string `json:"path"`
	MaxBytes      int64  `json:"max_bytes"`
}

type extractInput struct {
	WorkspaceRoot string  `json:"workspace_root"`
	Path          string  `json:"path"`
	OutputDir     string  `json:"output_dir"`
	TimesMs       []int64 `json:"times_ms"`
	Width         int     `json:"width"`
	Format        string  `json:"format"`
}

// ── 能力实现 ──────────────────────────────────────────

func (s *service) runtimeStatus() (any, *himindjsonrpc.Error) {
	status := map[string]any{
		"available": s.toolsErr == nil && s.tools.FFmpeg != "",
	}
	if s.tools.FFmpeg != "" {
		status["ffmpeg_path"] = s.tools.FFmpeg
		status["ffprobe_path"] = s.tools.FFprobe
		status["version"] = s.tools.Version
	}
	pipelines := []string{
		"probe", "recipe.plan", "recipe.run",
		"job.status", "job.cancel", "job.list",
		"scene.score", "audio.loudness",
		"frames.contact-sheet", "frames.extract", "preview.image", "files.list",
	}
	status["capabilities_ready"] = pipelines
	status["presets"] = recipe.PresetNames()
	if !status["available"].(bool) {
		status["message"] = "未找到 ffmpeg，绝大多数能力不可用。安装 ffmpeg 并加入 PATH 后即可恢复。"
		if s.toolsErr != nil {
			status["error"] = s.toolsErr.Error()
		}
	}
	if s.store == nil {
		status["jobs_available"] = false
		status["jobs_message"] = "作业存储不可用，转码类长任务无法提交"
	} else {
		status["jobs_available"] = true
	}
	return status, nil
}

func (s *service) probe(input probeInput) (any, *himindjsonrpc.Error) {
	tools, rpcError := s.toolsOrError()
	if rpcError != nil {
		return nil, rpcError
	}
	root, path, rpcError := resolveInputPath(input.WorkspaceRoot, input.Path)
	if rpcError != nil {
		return nil, rpcError
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	info, err := probe.Read(ctx, tools, path)
	if err != nil {
		return map[string]any{"workspace_root": root.Abs(), "path": path, "error": err.Error()}, nil
	}
	return info, nil
}

// recipePlan 只编译不执行 —— 长任务开跑前先让人看一眼将要执行什么。
func (s *service) recipePlan(input planInput) (any, *himindjsonrpc.Error) {
	if err := input.Recipe.Validate(); err != nil {
		return nil, &himindjsonrpc.Error{Code: -32602, Message: "recipe 不合法: " + err.Error()}
	}
	args, err := recipe.Compile(input.Recipe)
	if err != nil {
		return nil, &himindjsonrpc.Error{Code: -32602, Message: err.Error()}
	}
	return map[string]any{
		"ok":          true,
		"description": input.Recipe.Describe(),
		"args":        args,
		"inputs":      len(input.Recipe.Inputs),
		"output":      input.Recipe.Output.Path,
	}, nil
}

func (s *service) recipeRun(input runInput) (any, *himindjsonrpc.Error) {
	// 先校验调用方给的东西，再报基础设施问题：参数缺了是调用方的错，
	// 直接把它埋在"作业存储不可用"后面会让人排查错方向。
	if strings.TrimSpace(input.WorkspaceRoot) == "" {
		return nil, &himindjsonrpc.Error{Code: -32602, Message: "recipe.run 必须提供 workspace_root"}
	}
	if _, rpcError := s.toolsOrError(); rpcError != nil {
		return nil, rpcError
	}
	if s.store == nil {
		return nil, &himindjsonrpc.Error{Code: -32000, Message: "作业存储不可用，无法提交转码任务"}
	}

	// 输入与产物都必须落在工作区内 —— 这是插件规范对路径的硬约束，在这里统一执行。
	resolved, rpcError := resolveRecipePaths(input.WorkspaceRoot, input.Recipe)
	if rpcError != nil {
		return nil, rpcError
	}
	args, err := recipe.Compile(resolved)
	if err != nil {
		return nil, &himindjsonrpc.Error{Code: -32602, Message: err.Error()}
	}
	if err := workspace.EnsureParentDir(resolved.Output.Path); err != nil {
		return nil, &himindjsonrpc.Error{Code: -32000, Message: "无法创建输出目录: " + err.Error()}
	}

	totalMs := int64(0)
	if len(resolved.Inputs) == 1 {
		tools, _ := s.toolsOrError()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		if info, probeErr := probe.Read(ctx, tools, resolved.Inputs[0].Path); probeErr == nil {
			totalMs = info.DurationMs
		}
		cancel()
	}

	kind := strings.TrimSpace(input.Kind)
	if kind == "" {
		kind = "recipe"
	}
	record, err := s.store.Submit(
		kind, resolved.Inputs[0].Path, resolved.Output.Path, resolved.Describe(), args, totalMs)
	if err != nil {
		return nil, &himindjsonrpc.Error{Code: -32000, Message: "提交作业失败: " + err.Error()}
	}
	return map[string]any{"job": record, "description": resolved.Describe()}, nil
}

func (s *service) jobStatus(input jobIDInput) (any, *himindjsonrpc.Error) {
	record, rpcError := s.requireStore(input.JobID)
	if rpcError != nil {
		return nil, rpcError
	}
	return record, nil
}

func (s *service) jobCancel(input jobIDInput) (any, *himindjsonrpc.Error) {
	if s.store == nil {
		return nil, &himindjsonrpc.Error{Code: -32000, Message: "作业存储不可用"}
	}
	if strings.TrimSpace(input.JobID) == "" {
		return nil, &himindjsonrpc.Error{Code: -32602, Message: "job_id 不能为空"}
	}
	if err := s.store.Cancel(input.JobID); err != nil {
		return nil, &himindjsonrpc.Error{Code: -32000, Message: err.Error()}
	}
	record, err := s.store.Get(input.JobID)
	if err != nil {
		return map[string]any{"cancelled": true}, nil
	}
	return map[string]any{"cancelled": true, "job": record}, nil
}

func (s *service) jobList(input jobListInput) (any, *himindjsonrpc.Error) {
	if s.store == nil {
		return nil, &himindjsonrpc.Error{Code: -32000, Message: "作业存储不可用"}
	}
	limit := job.NormalizeLimit(input.Limit, 20, 200)
	records := s.store.List(limit)
	return map[string]any{"jobs": records, "count": len(records)}, nil
}

func (s *service) sceneScore(input sceneInput) (any, *himindjsonrpc.Error) {
	tools, rpcError := s.toolsOrError()
	if rpcError != nil {
		return nil, rpcError
	}
	_, path, rpcError := resolveInputPath(input.WorkspaceRoot, input.Path)
	if rpcError != nil {
		return nil, rpcError
	}
	// 逐帧分析长片开销较大，给足超时但仍有限度。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	result, err := analysis.SceneScore(ctx, tools, path, input.Threshold)
	if err != nil {
		return map[string]any{"path": path, "error": err.Error()}, nil
	}
	points, note := downsampleScene(result.Points, job.NormalizeLimit(input.MaxPoints, 2000, 200000))
	result.Points = points
	if note != "" {
		result.Notes = append(result.Notes, note)
	}
	return result, nil
}

func (s *service) audioLoudness(input loudnessInput) (any, *himindjsonrpc.Error) {
	tools, rpcError := s.toolsOrError()
	if rpcError != nil {
		return nil, rpcError
	}
	_, path, rpcError := resolveInputPath(input.WorkspaceRoot, input.Path)
	if rpcError != nil {
		return nil, rpcError
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	result, err := analysis.Loudness(ctx, tools, path)
	if err != nil {
		return map[string]any{"path": path, "error": err.Error()}, nil
	}
	points, note := downsampleLoudness(result.Points, job.NormalizeLimit(input.MaxPoints, 2000, 200000))
	result.Points = points
	if note != "" {
		result.Notes = append(result.Notes, note)
	}
	return result, nil
}

func (s *service) contactSheet(input sheetInput) (any, *himindjsonrpc.Error) {
	tools, rpcError := s.toolsOrError()
	if rpcError != nil {
		return nil, rpcError
	}
	root, path, rpcError := resolveInputPath(input.WorkspaceRoot, input.Path)
	if rpcError != nil {
		return nil, rpcError
	}
	output, rpcError := resolveOutputPath(root, input.Output)
	if rpcError != nil {
		return nil, rpcError
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	// 自动采样间隔需要源时长。
	durationMs := int64(0)
	if input.IntervalMs <= 0 {
		if info, err := probe.Read(ctx, tools, path); err == nil {
			durationMs = info.DurationMs
		}
	}

	result, err := frames.ContactSheet(ctx, tools, path, output, frames.SheetOptions{
		Columns:    input.Columns,
		Rows:       input.Rows,
		IntervalMs: input.IntervalMs,
		TileWidth:  input.TileWidth,
		Quality:    input.Quality,
		DurationMs: durationMs,
	})
	if err != nil {
		return map[string]any{"path": path, "output": output, "error": err.Error()}, nil
	}
	return result, nil
}

func (s *service) frameExtract(input extractInput) (any, *himindjsonrpc.Error) {
	tools, rpcError := s.toolsOrError()
	if rpcError != nil {
		return nil, rpcError
	}
	root, path, rpcError := resolveInputPath(input.WorkspaceRoot, input.Path)
	if rpcError != nil {
		return nil, rpcError
	}
	if len(input.TimesMs) == 0 {
		return nil, &himindjsonrpc.Error{Code: -32602, Message: "times_ms 不能为空"}
	}
	if len(input.TimesMs) > 200 {
		return nil, &himindjsonrpc.Error{Code: -32602, Message: "times_ms 一次最多 200 个"}
	}
	outputDir, rpcError := resolveOutputPath(root, input.OutputDir)
	if rpcError != nil {
		return nil, rpcError
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	written, err := frames.Extract(ctx, tools, path, outputDir, input.TimesMs, frames.ExtractOptions{
		Width:   input.Width,
		Format:  input.Format,
		Quality: 3,
	})
	if err != nil {
		return map[string]any{"files": written, "error": err.Error()}, nil
	}
	return map[string]any{"files": written, "count": len(written)}, nil
}

// previewImage 把工作区内的图片读成 data URL，供扩展视图显示产物。
//
// 为什么需要它：扩展视图的桥接只暴露**能力调用**（没有"直接读工作区文件"的通道），
// 而拼图与抽帧的价值就在于"看见"。所以补一个受约束的读取口：
// 只读、只允许图片扩展名、有体积上限。
func (s *service) previewImage(input previewInput) (any, *himindjsonrpc.Error) {
	_, path, rpcError := resolveInputPath(input.WorkspaceRoot, input.Path)
	if rpcError != nil {
		return nil, rpcError
	}
	result, err := frames.Preview(path, input.MaxBytes)
	if err != nil {
		return nil, &himindjsonrpc.Error{Code: -32602, Message: err.Error()}
	}
	return result, nil
}

// filesList 列出工作区里的媒体文件。
//
// 存在的理由有两个：视图不该逼用户手打相对路径；AI 也需要一个"这个工作区里
// 有哪些可处理素材"的入口。跳过运行期产物目录（runtime/jobs/dist…），
// 否则真正的素材会被自己的产物淹掉。
func (s *service) filesList(input filesInput) (any, *himindjsonrpc.Error) {
	root, err := workspace.Open(input.WorkspaceRoot)
	if err != nil {
		return nil, &himindjsonrpc.Error{Code: -32602, Message: err.Error()}
	}
	files, truncated := root.ListMedia(workspace.ListOptions{
		Kind:     input.Kind,
		MaxDepth: input.MaxDepth,
		Limit:    input.Limit,
	})
	return map[string]any{
		"workspace_root": root.Abs(),
		"files":          files,
		"count":          len(files),
		"truncated":      truncated,
	}, nil
}

// ── 辅助 ──────────────────────────────────────────────

func (s *service) requireStore(jobID string) (*job.Record, *himindjsonrpc.Error) {
	if s.store == nil {
		return nil, &himindjsonrpc.Error{Code: -32000, Message: "作业存储不可用"}
	}
	if strings.TrimSpace(jobID) == "" {
		return nil, &himindjsonrpc.Error{Code: -32602, Message: "job_id 不能为空"}
	}
	record, err := s.store.Get(jobID)
	if err != nil {
		return nil, &himindjsonrpc.Error{Code: -32000, Message: err.Error()}
	}
	return record, nil
}

func resolveInputPath(rawRoot, rawPath string) (workspace.Root, string, *himindjsonrpc.Error) {
	root, err := workspace.Open(rawRoot)
	if err != nil {
		return workspace.Root{}, "", &himindjsonrpc.Error{Code: -32602, Message: err.Error()}
	}
	path, err := root.Resolve(rawPath)
	if err != nil {
		return workspace.Root{}, "", &himindjsonrpc.Error{Code: -32602, Message: err.Error()}
	}
	if info, statErr := os.Stat(path); statErr != nil || info.IsDir() {
		return workspace.Root{}, "", &himindjsonrpc.Error{
			Code: -32602, Message: "输入文件不存在或不是文件: " + rawPath}
	}
	return root, path, nil
}

func resolveOutputPath(root workspace.Root, rawPath string) (string, *himindjsonrpc.Error) {
	if strings.TrimSpace(rawPath) == "" {
		return "", &himindjsonrpc.Error{Code: -32602, Message: "输出路径不能为空"}
	}
	path, err := root.Resolve(rawPath)
	if err != nil {
		return "", &himindjsonrpc.Error{Code: -32602, Message: err.Error()}
	}
	return path, nil
}

// resolveRecipePaths 把 recipe 里的输入输出都约束到工作区内。
func resolveRecipePaths(rawRoot string, input recipe.Recipe) (recipe.Recipe, *himindjsonrpc.Error) {
	root, err := workspace.Open(rawRoot)
	if err != nil {
		return input, &himindjsonrpc.Error{Code: -32602, Message: err.Error()}
	}
	resolved := input
	resolved.Inputs = make([]recipe.Input, 0, len(input.Inputs))
	for _, item := range input.Inputs {
		path, resolveErr := root.Resolve(item.Path)
		if resolveErr != nil {
			return input, &himindjsonrpc.Error{Code: -32602, Message: resolveErr.Error()}
		}
		copied := item
		copied.Path = path
		resolved.Inputs = append(resolved.Inputs, copied)
	}
	outputPath, resolveErr := root.Resolve(input.Output.Path)
	if resolveErr != nil {
		return input, &himindjsonrpc.Error{Code: -32602, Message: resolveErr.Error()}
	}
	resolved.Output.Path = outputPath
	if err := resolved.Validate(); err != nil {
		return input, &himindjsonrpc.Error{Code: -32602, Message: "recipe 不合法: " + err.Error()}
	}
	return resolved, nil
}

// downsampleScene 等间隔抽稀，避免长片返回天量采样点。
// downsampleScene 抽稀采样点，但**保住显著尖峰**。
//
// 为什么不能用等间隔丢帧：硬切是**单帧**事件，等间隔丢帧会把它整条丢掉 ——
// 实测一条动作片 7528 帧抽到 3000 点后，points 里最高只剩 5.9，
// 而 peak_score 仍报 29.47（它是抽稀前算的），两者自相矛盾；照 points 找切点会一个都找不到。
//
// 做法：找出"明显高于该片自身基线"的局部极大值并全部保留，剩下的名额再等间隔取。
// 判据用**相对自身中位数**（5×中位），与画面活动分数的读法一致 —— 绝对值没有普适阈值。
func downsampleScene(points []analysis.ScenePoint, maxPoints int) ([]analysis.ScenePoint, string) {
	original := len(points)
	if original <= maxPoints {
		return points, ""
	}
	keep := spikeIndices(len(points), maxPoints, func(index int) float64 {
		return points[index].Score
	})
	step := float64(original) / float64(maxPoints-len(keep))
	result := make([]analysis.ScenePoint, 0, maxPoints)
	for index := 0; index < maxPoints-len(keep); index++ {
		position := int(float64(index) * step)
		if position >= original {
			position = original - 1
		}
		result = append(result, points[position])
	}
	for index := range keep {
		result = append(result, points[index])
	}
	sort.Slice(result, func(i, j int) bool { return result[i].TimeMs < result[j].TimeMs })
	// 去重：尖峰位置可能刚好也被步进取到。
	deduped := result[:0]
	for index, point := range result {
		if index == 0 || point.TimeMs != result[index-1].TimeMs {
			deduped = append(deduped, point)
		}
	}
	return deduped, fmt.Sprintf(
		"采样点由 %d 抽稀到 %d（等间隔 + 保留 %d 个显著尖峰，尖峰不会被丢帧吃掉）",
		original, len(deduped), len(keep))
}

// spikeIndices 选出必须保留的尖峰下标：局部极大且 ≥ 5×中位数。
//
// 两个坑都实测踩过：
//   - **首尾也要判**。片尾硬切（切黑场）恰好发生在最后一帧，若循环只走到 count-1，
//     整条片子最强的那个尖峰就正好被排除 —— 而它往往是最重要的收束事件。
//   - **超预算时按分值取前 N，不能按位置贪心**。候选通常远多于名额（本片 0.35 阈值下
//     有 120+ 个），从前往后取会把尾部的大尖峰挤掉。按分值排序才保得住"最显著的"。
func spikeIndices(count, maxPoints int, valueAt func(int) float64) map[int]bool {
	values := make([]float64, count)
	for index := 0; index < count; index++ {
		values[index] = valueAt(index)
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	median := sorted[count/2]
	threshold := median * 5

	type candidate struct {
		index int
		score float64
	}
	candidates := make([]candidate, 0, 64)
	for index := 0; index < count; index++ {
		if values[index] < threshold {
			continue
		}
		left := index == 0 || values[index] >= values[index-1]
		right := index == count-1 || values[index] > values[index+1]
		if left && right {
			candidates = append(candidates, candidate{index: index, score: values[index]})
		}
	}
	// 按分值取前 N —— 名额有限时必须留最显著的，而不是最先出现的。
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})
	limit := maxPoints / 10
	if limit < 10 {
		limit = 10
	}
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	keep := make(map[int]bool, len(candidates))
	for _, item := range candidates {
		keep[item.index] = true
	}
	return keep
}

// downsampleLoudness 与 downsampleScene 同构：等间隔 + 保住响度尖峰。
// 响度尖峰是判断"事件时刻"最可靠的信号，同样不能被丢帧吃掉。
func downsampleLoudness(points []analysis.LoudnessPoint, maxPoints int) ([]analysis.LoudnessPoint, string) {
	original := len(points)
	if original <= maxPoints {
		return points, ""
	}
	keep := spikeIndices(original, maxPoints, func(index int) float64 {
		return points[index].MomentaryLufs
	})
	step := float64(original) / float64(maxPoints-len(keep))
	result := make([]analysis.LoudnessPoint, 0, maxPoints)
	for index := 0; index < maxPoints-len(keep); index++ {
		position := int(float64(index) * step)
		if position >= original {
			position = original - 1
		}
		result = append(result, points[position])
	}
	for index := range keep {
		result = append(result, points[index])
	}
	sort.Slice(result, func(i, j int) bool { return result[i].TimeMs < result[j].TimeMs })
	deduped := result[:0]
	for index, point := range result {
		if index == 0 || point.TimeMs != result[index-1].TimeMs {
			deduped = append(deduped, point)
		}
	}
	return deduped, fmt.Sprintf(
		"采样点由 %d 抽稀到 %d（等间隔 + 保留 %d 个显著尖峰）", original, len(deduped), len(keep))
}
