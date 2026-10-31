package iostudio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// BuildRequest 一次构建请求。
type BuildRequest struct {
	CLI           CLI
	WorkspaceRoot string
	SpecPath      string
	OutPath       string
	// DryRun 只验证不保留产物：构建到临时位置、预检、然后删掉。
	// 这是给"AI 先出方案、人复核后再落地"准备的关口。
	DryRun bool
	// SkipValidate 跳过对产物的预检（默认会预检）。
	//
	// 只供包内使用（例如集成测试需要先造出一个未预检的动作文件）。
	// 能力层不转发这个字段：它绕过的是设备声明、动作存在性、量程与安全联锁，
	// 一旦能被调用方传入，预检就形同虚设。
	SkipValidate bool
}

// BuildResult 一次构建的结果。
type BuildResult struct {
	OK         bool        `json:"ok"`
	MotionPath string      `json:"motion_path,omitempty"`
	DryRun     bool        `json:"dry_run,omitempty"`
	Phase      string      `json:"phase,omitempty"`
	Validation *Validation `json:"validation,omitempty"`
	// Stdout / Stderr 仅在失败时带出，便于定位（成功时内容只是常规回显）。
	Stdout string `json:"stdout,omitempty"`
	Stderr string `json:"stderr,omitempty"`
	// CLI / ConfigDir / DeviceTable 回显本次用的工具与设备表 ——
	// 设备表决定"哪些设备与动作算已声明"，用错表会得出一整套假问题，必须可见。
	CLI         string `json:"cli"`
	ConfigDir   string `json:"config_dir,omitempty"`
	DeviceTable string `json:"device_table,omitempty"`
	ExitCode    int    `json:"exit_code"`
	ElapsedMs   int64  `json:"elapsed_ms"`
}

// Build 把演出规格构建成动作文件，并对产物做预检。
//
// 两步是刻意的：`--motion-build` 只保证"规格能被展开成动作文件"，
// 而"这份动作文件在目标设备上播得对不对"要由 `--validate` 回答
// （设备是否声明、动作是否存在、量程是否越界、安全联锁是否满足）。
// 只做前者会让"构建成功"被误读成"可以播"。
func Build(ctx context.Context, request BuildRequest) (BuildResult, error) {
	if request.CLI.Path == "" {
		return BuildResult{}, ErrCLINotFound
	}
	started := time.Now()
	result := BuildResult{
		CLI:         request.CLI.Path,
		ConfigDir:   request.CLI.ConfigDir,
		DeviceTable: request.CLI.DeviceTable,
		DryRun:      request.DryRun,
	}

	outPath := request.OutPath
	tempDir := ""
	if request.DryRun {
		var err error
		tempDir, err = os.MkdirTemp(request.WorkspaceRoot, ".motion-authoring-")
		if err != nil {
			return result, fmt.Errorf("创建临时目录失败: %w", err)
		}
		defer os.RemoveAll(tempDir)
		outPath = filepath.Join(tempDir, "preview.motion")
		// 干跑不保留产物，因此**任何时候**都不回报产物路径 ——
		// 回报了会让调用方以为文件还在（早退路径上尤其容易漏）。
	} else {
		result.MotionPath = outPath
	}

	// 构建。
	statusPath := filepath.Join(filepath.Dir(outPath), ".build-status.json")
	buildArgs := []string{
		"--motion-build", request.SpecPath,
		"--out", outPath,
		"--status-file", statusPath,
	}
	buildRun, err := request.CLI.Run(ctx, request.CLI.ConfigDir, buildArgs)
	if err != nil {
		return result, err
	}
	result.ExitCode = buildRun.ExitCode
	result.Stdout = strings.TrimSpace(buildRun.Stdout)
	result.Stderr = strings.TrimSpace(buildRun.Stderr)
	if status, statusErr := ReadStatus(statusPath); statusErr == nil && status != nil {
		result.Phase = status.Phase
		result.Validation = status.Validation
	}

	if buildRun.ExitCode != 0 {
		result.OK = false
		result.ElapsedMs = time.Since(started).Milliseconds()
		return result, nil
	}
	if _, statErr := os.Stat(outPath); statErr != nil {
		result.OK = false
		result.Stderr = strings.TrimSpace(result.Stderr + "\n构建声称成功但没有产出动作文件")
		result.ElapsedMs = time.Since(started).Milliseconds()
		return result, nil
	}

	// 预检产物。
	if !request.SkipValidate {
		validation, validationErr := ValidateFile(ctx, request.CLI, outPath)
		if validationErr != nil {
			return result, validationErr
		}
		result.Validation = validation
	}

	result.OK = result.Validation == nil || !result.Validation.HasErrors
	result.ElapsedMs = time.Since(started).Milliseconds()
	return result, nil
}

// ValidateFile 对一个已有的动作文件做预检。
func ValidateFile(ctx context.Context, cli CLI, motionPath string) (*Validation, error) {
	if cli.Path == "" {
		return nil, ErrCLINotFound
	}
	statusPath := filepath.Join(filepath.Dir(motionPath), ".validate-status.json")
	run, err := cli.Run(ctx, cli.ConfigDir, []string{
		"--validate", motionPath,
		"--status-file", statusPath,
	})
	if err != nil {
		return nil, err
	}
	status, statusErr := ReadStatus(statusPath)
	if statusErr != nil {
		return nil, statusErr
	}
	if status != nil && status.Validation != nil {
		return status.Validation, nil
	}
	// 没有状态文件时退回退出码判断——至少不能把"校验失败了"说成"通过"。
	if run.ExitCode != 0 {
		return &Validation{
			HasErrors:  true,
			ErrorCount: 1,
			Error:      "预检未产出结果文件且退出码非 0: " + firstNonEmpty(run.Stderr, run.Stdout),
		}, nil
	}
	return &Validation{}, nil
}

// Tracks 读取动作文件的轨道 → 通道映射（无需播放）。
type Tracks struct {
	Path   string          `json:"path"`
	Phase  string          `json:"phase"`
	Tracks json.RawMessage `json:"tracks"`
	CLI    string          `json:"cli"`
}

// ReadTracks 用 --dump-tracks 取出轨道映射。
//
// 这是最快的自动化断言路径：它不建设备、不开窗口，亚秒返回。
func ReadTracks(ctx context.Context, cli CLI, workspaceRoot, motionPath string) (Tracks, error) {
	result := Tracks{Path: motionPath, CLI: cli.Path}
	if cli.Path == "" {
		return result, ErrCLINotFound
	}
	statusPath := filepath.Join(workspaceRoot, ".motion-tracks-status.json")
	run, err := cli.Run(ctx, cli.ConfigDir, []string{
		"--dump-tracks", motionPath,
		"--status-file", statusPath,
	})
	if err != nil {
		return result, err
	}
	status, statusErr := ReadStatus(statusPath)
	if statusErr != nil {
		return result, statusErr
	}
	if status == nil {
		return result, fmt.Errorf("未取出轨道映射: %s", firstNonEmpty(run.Stderr, run.Stdout))
	}
	result.Phase = status.Phase
	result.Tracks = status.Tracks
	return result, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			if len(trimmed) > 400 {
				return trimmed[len(trimmed)-400:]
			}
			return trimmed
		}
	}
	return ""
}

// ErrSpecMissing 规格文件不存在。
var ErrSpecMissing = errors.New("演出规格文件不存在")
