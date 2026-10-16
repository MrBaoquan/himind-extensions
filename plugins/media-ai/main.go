// media-ai —— HiMind 媒体智能插件（本版：语音转写）。
//
// 它补上动作编排链路里**唯一缺失的模态**：前面的工具能"看"（抽帧拼图）与"量"
// （画面活动、响度），但听不见。转写把"这段在唱什么/在说什么"从猜测变成可读的文本，
// 而歌词与对白的分句恰好就是编排动作时最需要的音乐/语义分段依据 ——
// 实测一段音乐片用画面与响度都找不到段落结构（曲线平在中位附近），歌词却给出了
// 稳定的 4-5 秒一句的节律。
//
// 两层外部依赖，都按 runtime-lock.json 锁定版本与摘要后获取：
//   whisper.cpp  语音识别（二进制 + ggml 模型）
//   ffmpeg       抽音频（实测 whisper 自己读不了 mp4/AAC）
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"himind-plugin/media-ai/internal/himindjsonrpc"
	"himind-plugin/media-ai/internal/media/audio"
	"himind-plugin/media-ai/internal/media/job"
	"himind-plugin/media-ai/internal/media/runtime"
	"himind-plugin/media-ai/internal/media/whisper"
	"himind-plugin/media-ai/internal/media/workspace"
)

// 能力 ID。首段 `media` 是 ADR 0119 注册域（与 media-toolkit 一致）。
const (
	capRuntimeStatus  = "media.ai.runtime.status"
	capRuntimeInstall = "media.ai.runtime.install"
	capModels         = "media.ai.models"
	capTranscribe     = "media.ai.transcribe"
	capJobStatus      = "media.ai.job.status"
	capJobCancel      = "media.ai.job.cancel"
	capJobList        = "media.ai.job.list"
)

type service struct {
	store *job.Store
}

var defaultService = &service{}

func main() {
	_ = himindjsonrpc.Serve(os.Stdin, os.Stdout, defaultService.handle)
}

func (s *service) handle(request himindjsonrpc.Request) (any, *himindjsonrpc.Error) {
	switch request.Method {

	case capRuntimeStatus:
		return s.runtimeStatus()

	case capRuntimeInstall:
		var input installInput
		if rpcError := himindjsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		return s.runtimeInstall(input)

	case capModels:
		return s.models()

	case capTranscribe:
		var input transcribeInput
		if rpcError := himindjsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		return s.transcribe(input)

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
	}

	return nil, &himindjsonrpc.Error{
		Code:    -32601,
		Message: fmt.Sprintf("未知能力: %s", request.Method),
	}
}

// ── 输入 ──────────────────────────────────────────────

type installInput struct {
	// Models 要安装的模型 id；为空表示只装二进制。
	Models []string `json:"models"`
}

type transcribeInput struct {
	WorkspaceRoot string `json:"workspace_root"`
	Path          string `json:"path"`
	// Language 语言代码（zh / en / ja …）。留空会走 whisper 的自动检测，
	// 而实测它对歌曲不可靠（中文歌被判成 en，结果只剩占位文本）。
	Language string `json:"language"`
	// Model 模型 id，默认 small。
	Model string `json:"model"`
	StartMs *int64 `json:"start_ms"`
	EndMs   *int64 `json:"end_ms"`
}

type jobIDInput struct {
	JobID string `json:"job_id"`
}

type jobListInput struct {
	Limit int `json:"limit"`
}

// ── 能力实现 ──────────────────────────────────────────

func loadContext() (runtime.Lock, runtime.Paths, *himindjsonrpc.Error) {
	lock, err := runtime.ReadLock(runtime.EmbeddedLock())
	if err != nil {
		return runtime.Lock{}, runtime.Paths{}, &himindjsonrpc.Error{
			Code: -32000, Message: "运行时锁定文件不可用: " + err.Error()}
	}
	paths, err := runtime.ResolvePaths()
	if err != nil {
		return runtime.Lock{}, runtime.Paths{}, &himindjsonrpc.Error{
			Code: -32000, Message: "无法确定运行时目录: " + err.Error()}
	}
	return lock, paths, nil
}

func (s *service) runtimeStatus() (any, *himindjsonrpc.Error) {
	lock, paths, rpcError := loadContext()
	if rpcError != nil {
		return nil, rpcError
	}
	status := runtime.Inspect(lock, paths)

	ffmpegPath, ffmpegErr := audio.Locate()
	payload := map[string]any{
		"runtime":            lock.Runtime,
		"release":            lock.Release,
		"binary_installed":   status.BinaryInstalled,
		"binary_path":        status.BinaryPath,
		"installed_models":   status.InstalledModels,
		"runtime_root":       status.RuntimeRoot,
		"ffmpeg_available":   ffmpegErr == nil,
		"ffmpeg_path":        ffmpegPath,
		"available_models":   modelIDs(lock),
		"capabilities_ready": []string{"transcribe", "job.status", "job.cancel", "job.list"},
	}
	ready := status.BinaryInstalled && ffmpegErr == nil && len(status.InstalledModels) > 0
	payload["ready"] = ready
	if !ready {
		var missing []string
		if !status.BinaryInstalled {
			missing = append(missing, "whisper 二进制（用 runtime.install 获取）")
		}
		if len(status.InstalledModels) == 0 {
			missing = append(missing, "至少一个模型（用 runtime.install 获取）")
		}
		if ffmpegErr != nil {
			missing = append(missing, "ffmpeg（请自行安装并加入 PATH）")
		}
		payload["missing"] = missing
		payload["message"] = "尚缺: " + strings.Join(missing, "、")
	}
	payload["license"] = lock.Archive.License
	payload["license_note"] = lock.LicenseNote
	return payload, nil
}

func (s *service) runtimeInstall(input installInput) (any, *himindjsonrpc.Error) {
	lock, paths, rpcError := loadContext()
	if rpcError != nil {
		return nil, rpcError
	}
	// 下载几十 MB 到几百 MB，给足超时。
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()

	result, err := runtime.Install(ctx, lock, paths, runtime.InstallOptions{Models: input.Models})
	if err != nil {
		return map[string]any{
			"ok":      false,
			"error":   err.Error(),
			"runtime": lock.Runtime,
		}, nil
	}
	return map[string]any{
		"ok":                true,
		"binary_installed":  result.BinaryInstalled,
		"binary_path":       result.BinaryPath,
		"installed_models":  result.InstalledModels,
		"skipped":           result.Skipped,
		"runtime_root":      paths.Root,
		"license":           lock.Archive.License,
		"license_note":      lock.LicenseNote,
	}, nil
}

func (s *service) models() (any, *himindjsonrpc.Error) {
	lock, paths, rpcError := loadContext()
	if rpcError != nil {
		return nil, rpcError
	}
	status := runtime.Inspect(lock, paths)
	installed := map[string]bool{}
	for _, id := range status.InstalledModels {
		installed[id] = true
	}
	items := make([]map[string]any, 0, len(lock.Models))
	for _, model := range lock.Models {
		items = append(items, map[string]any{
			"id":           model.ID,
			"file":         model.File,
			"size_mb":      float64(model.Size) / (1024 * 1024),
			"installed":    installed[model.ID],
			"multilingual": model.Multilingual,
			"note":         model.Note,
		})
	}
	return map[string]any{"models": items, "runtime": lock.Runtime, "release": lock.Release}, nil
}

func (s *service) transcribe(input transcribeInput) (any, *himindjsonrpc.Error) {
	lock, paths, rpcError := loadContext()
	if rpcError != nil {
		return nil, rpcError
	}
	root, rpcError := openWorkspace(input.WorkspaceRoot)
	if rpcError != nil {
		return nil, rpcError
	}
	inputPath, rpcError := resolveExistingFile(root, input.Path, "path")
	if rpcError != nil {
		return nil, rpcError
	}

	// 先校验调用方给的参数（模型名），再报基础设施缺失 ——
	// 参数错是调用方的错，直接埋在"未安装"后面会让人排查错方向。
	model, ok := lock.FindModel(input.Model)
	if !ok {
		return nil, &himindjsonrpc.Error{Code: -32602,
			Message: "未知模型: " + input.Model + "（可用: " + strings.Join(modelIDs(lock), ", ") + "）"}
	}

	status := runtime.Inspect(lock, paths)
	if !status.BinaryInstalled {
		return nil, &himindjsonrpc.Error{Code: -32000,
			Message: "whisper 未安装。先调用 media.ai.runtime.install 获取运行时可执行文件。"}
	}
	modelPath := filepath.Join(paths.ModelsDir, model.File)
	if _, err := os.Stat(modelPath); err != nil {
		return nil, &himindjsonrpc.Error{Code: -32000,
			Message: "模型 " + model.ID + " 尚未安装。调用 media.ai.runtime.install 并指定 models: [\"" + model.ID + "\"]。"}
	}
	ffmpegPath, ffmpegErr := audio.Locate()
	if ffmpegErr != nil {
		return nil, &himindjsonrpc.Error{Code: -32000, Message: ffmpegErr.Error()}
	}

	store, err := s.ensureStore()
	if err != nil {
		return nil, &himindjsonrpc.Error{Code: -32000, Message: "作业存储不可用: " + err.Error()}
	}

	language := strings.TrimSpace(input.Language)
	workDir := filepath.Join(paths.Root, "work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return nil, &himindjsonrpc.Error{Code: -32000, Message: "无法创建临时目录: " + err.Error()}
	}

	binaryPath := status.BinaryPath
	record, err := store.Submit("transcribe", inputPath, language, model.ID,
		func(ctx context.Context, reporter *job.Reporter) (any, error) {
			work, err := os.MkdirTemp(workDir, "t-")
			if err != nil {
				return nil, err
			}
			defer os.RemoveAll(work)

			reporter.Stage("extracting-audio")
			reporter.Log("抽取音频: " + inputPath)
			audioPath, err := audio.Extract(ctx, audio.ExtractOptions{
				FFmpegPath: ffmpegPath,
				InputPath:  inputPath,
				OutputPath: filepath.Join(work, "audio.wav"),
				StartMs:    input.StartMs,
				EndMs:      input.EndMs,
			})
			if err != nil {
				return nil, err
			}

			reporter.Stage("transcribing")
			reporter.Log("转写: 模型=" + model.ID + " 语言=" + languageOrDefault(language))
			result, err := whisper.Run(ctx, binaryPath, whisper.Options{
				AudioPath:  audioPath,
				ModelPath:  modelPath,
				Language:   language,
				OutputDir:  work,
				Threads:    1, // 必须单线程：多线程在该环境会段错误
				OnProgress: reporter.Log,
			})
			if err != nil {
				return nil, err
			}

			// 把结果整理成"可直接用"的形状：区分真实内容与占位标注，
			// 并算出歌词节律（相邻段起点差），这正是编排动作时要的时点。
			content := result.ContentSegments()
			payload := map[string]any{
				"language":         result.Language,
				"model":            model.ID,
				"elapsed_ms":       result.ElapsedMs,
				"segments":         result.Segments,
				"content_segments": content,
				"full_text":        result.FullText,
				"vocal_ratio":      result.VocalRatio,
				"placeholder_only": len(content) == 0,
				"content_count":    len(content),
			}
			if len(content) == 0 {
				payload["note"] = "只识别出音乐/人声标注，没有实际词。" +
					"若是歌曲，确认 language 是否指定正确（自动检测对歌曲不可靠）。"
			} else {
				payload["beats_ms"] = beatStarts(content)
			}
			return payload, nil
		})
	if err != nil {
		return nil, &himindjsonrpc.Error{Code: -32000, Message: err.Error()}
	}
	return map[string]any{"job": record}, nil
}

func (s *service) jobStatus(input jobIDInput) (any, *himindjsonrpc.Error) {
	store, rpcError := s.requireStore(input.JobID)
	if rpcError != nil {
		return nil, rpcError
	}
	record, err := store.Get(input.JobID)
	if err != nil {
		return nil, &himindjsonrpc.Error{Code: -32000, Message: err.Error()}
	}
	return record, nil
}

func (s *service) jobCancel(input jobIDInput) (any, *himindjsonrpc.Error) {
	store, rpcError := s.requireStore(input.JobID)
	if rpcError != nil {
		return nil, rpcError
	}
	if err := store.Cancel(input.JobID); err != nil {
		return nil, &himindjsonrpc.Error{Code: -32000, Message: err.Error()}
	}
	record, err := store.Get(input.JobID)
	if err != nil {
		return map[string]any{"cancelled": true}, nil
	}
	return map[string]any{"cancelled": true, "job": record}, nil
}

func (s *service) jobList(input jobListInput) (any, *himindjsonrpc.Error) {
	store, err := s.ensureStore()
	if err != nil {
		return nil, &himindjsonrpc.Error{Code: -32000, Message: "作业存储不可用: " + err.Error()}
	}
	limit := job.NormalizeLimit(input.Limit, 20, 200)
	records := store.List(limit)
	return map[string]any{"jobs": records, "count": len(records)}, nil
}

// ── 辅助 ──────────────────────────────────────────────

func (s *service) ensureStore() (*job.Store, error) {
	if s.store != nil {
		return s.store, nil
	}
	store, err := job.NewStore()
	if err != nil {
		return nil, err
	}
	s.store = store
	return store, nil
}

func (s *service) requireStore(jobID string) (*job.Store, *himindjsonrpc.Error) {
	if strings.TrimSpace(jobID) == "" {
		return nil, &himindjsonrpc.Error{Code: -32602, Message: "job_id 不能为空"}
	}
	store, err := s.ensureStore()
	if err != nil {
		return nil, &himindjsonrpc.Error{Code: -32000, Message: "作业存储不可用: " + err.Error()}
	}
	return store, nil
}

func openWorkspace(raw string) (workspace.Root, *himindjsonrpc.Error) {
	root, err := workspace.Open(raw)
	if err != nil {
		return workspace.Root{}, &himindjsonrpc.Error{Code: -32602, Message: err.Error()}
	}
	return root, nil
}

func resolveExistingFile(root workspace.Root, raw, field string) (string, *himindjsonrpc.Error) {
	if strings.TrimSpace(raw) == "" {
		return "", &himindjsonrpc.Error{Code: -32602, Message: field + " 不能为空"}
	}
	path, err := root.Resolve(raw)
	if err != nil {
		return "", &himindjsonrpc.Error{Code: -32602, Message: err.Error()}
	}
	info, statErr := os.Stat(path)
	if statErr != nil || info.IsDir() {
		return "", &himindjsonrpc.Error{Code: -32602,
			Message: field + " 指向的文件不存在: " + raw}
	}
	return path, nil
}

func modelIDs(lock runtime.Lock) []string {
	ids := make([]string, 0, len(lock.Models))
	for _, model := range lock.Models {
		ids = append(ids, model.ID)
	}
	return ids
}

func languageOrDefault(language string) string {
	if strings.TrimSpace(language) == "" {
		return "auto（不推荐：实测自动检测对歌曲不可靠）"
	}
	return language
}

// beatStarts 取相邻内容段的起点序列 —— 音乐/语义分句的节律，编排时的时点依据。
func beatStarts(segments []whisper.Segment) []int64 {
	if len(segments) < 2 {
		return nil
	}
	starts := make([]int64, 0, len(segments))
	for _, segment := range segments {
		starts = append(starts, segment.StartMs)
	}
	return starts
}
