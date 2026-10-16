// motion-authoring —— HiMind 动作编排插件。
//
// 职责边界（刻意划死）：**只包装 IOStudio 的命令行，不重写编排逻辑。**
//
// 动作文件的构建与预检在 IOStudio 里已是一等公民，且预检规则会随产品演进。
// 在 Go 里复刻一份会立刻产生第二真相源——两份实现迟早不一致，而这类不一致是静默的：
// 插件说"通过"，真机播放却把输出丢掉。所以这里做的是"受控调用 + 解析结构化结果"。
//
// 另一个必须可见的事：**用哪一份设备表**。设备表按 IOStudio 安装目录解析，
// 它决定了"哪些设备与动作算已声明"。用错表会得出一整套假问题，
// 因此每次调用都把 cli / config_dir / device_table 一并回显，让不匹配暴露出来而不是被忽略。
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"himind-plugin/iostudio/internal/himindjsonrpc"
	studio "himind-plugin/iostudio/internal/iostudio"
	"himind-plugin/iostudio/internal/iostudio/workspace"
)

// 能力 ID。首段 `iostudio` 是插件命名空间（ADR 0119）。
//
// 命名按**产品域**分组，而不是按某一个功能：IOStudio 是设备调试工作台，
// 动作编排只是它四个域之一（另有设备表、协议测试、输出测试）。
// 若把插件命名成某个功能，其它域就无处安放，只能各建一个插件、
// 各自复制一遍"定位 CLI、跑子进程、读结果"的管道。
const (
	capRuntimeStatus = "iostudio.runtime.status"
	capDevices       = "iostudio.devices"

	capMotionPlan     = "iostudio.motion.plan"
	capMotionBuild    = "iostudio.motion.build"
	capMotionValidate = "iostudio.motion.validate"
	capMotionTracks   = "iostudio.motion.tracks"

	capProtocolTest = "iostudio.protocol.test"
)

// service 进程内状态。CLI 只定位一次。
type service struct {
	cli    studio.CLI
	cliErr error
	loaded bool
}

var defaultService = &service{}

func main() {
	_ = himindjsonrpc.Serve(os.Stdin, os.Stdout, defaultService.handle)
}

// ensure 惰性定位 CLI：定位本身要起一次子进程（取版本），
// 放在每次调用里会让只读能力也变慢。
func (s *service) ensure(explicitCLI string) (studio.CLI, *himindjsonrpc.Error) {
	if !s.loaded || strings.TrimSpace(explicitCLI) != "" {
		cli, err := studio.Locate(explicitCLI)
		if err != nil {
			if strings.TrimSpace(explicitCLI) != "" {
				s.cli, s.cliErr, s.loaded = studio.CLI{}, err, true
			} else {
				return studio.CLI{}, &himindjsonrpc.Error{Code: -32000, Message: err.Error()}
			}
		} else {
			s.cli, s.cliErr, s.loaded = cli, nil, true
		}
	}
	if s.cliErr != nil {
		return studio.CLI{}, &himindjsonrpc.Error{Code: -32000, Message: s.cliErr.Error()}
	}
	return s.cli, nil
}

func (s *service) handle(request himindjsonrpc.Request) (any, *himindjsonrpc.Error) {
	switch request.Method {

	case capRuntimeStatus:
		var input statusInput
		if rpcError := himindjsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		return s.runtimeStatus(input)

	case capDevices:
		var input devicesInput
		if rpcError := himindjsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		return s.devices(input)

	case capMotionPlan, capMotionBuild:
		var input buildInput
		if rpcError := himindjsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		return s.build(input, request.Method == capMotionPlan)

	case capMotionValidate:
		var input validateInput
		if rpcError := himindjsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		return s.validate(input)

	case capMotionTracks:
		var input tracksInput
		if rpcError := himindjsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		return s.tracks(input)

	case capProtocolTest:
		var input protocolInput
		if rpcError := himindjsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		return s.protocolTest(input)
	}

	return nil, &himindjsonrpc.Error{
		Code:    -32601,
		Message: fmt.Sprintf("未知能力: %s", request.Method),
	}
}

// ── 输入 ──────────────────────────────────────────────

type statusInput struct {
	CLIPath string `json:"cli_path"`
}

type devicesInput struct {
	CLIPath string `json:"cli_path"`
	// Device 只取指定设备（不填返回全部）。
	Device string `json:"device"`
}

type buildInput struct {
	CLIPath       string `json:"cli_path"`
	WorkspaceRoot string `json:"workspace_root"`
	SpecPath      string `json:"spec_path"`
	OutPath       string `json:"out_path"`
	SkipValidate  bool   `json:"skip_validate"`
}

type validateInput struct {
	CLIPath       string `json:"cli_path"`
	WorkspaceRoot string `json:"workspace_root"`
	MotionPath    string `json:"motion_path"`
}

type tracksInput struct {
	CLIPath       string `json:"cli_path"`
	WorkspaceRoot string `json:"workspace_root"`
	MotionPath    string `json:"motion_path"`
}

type protocolInput struct {
	CLIPath       string `json:"cli_path"`
	WorkspaceRoot string `json:"workspace_root"`
	ScenarioPath  string `json:"scenario_path"`
	// ReportPath 报告落点；为空时写在场景同目录。
	ReportPath string `json:"report_path"`
	// KeepReport 为 false 时不保留报告文件（只回传内容）。
	KeepReport *bool `json:"keep_report"`
}

// ── 能力实现 ──────────────────────────────────────────

func (s *service) runtimeStatus(input statusInput) (any, *himindjsonrpc.Error) {
	cli, rpcError := s.ensure(input.CLIPath)
	status := map[string]any{
		"available":    rpcError == nil,
		"capabilities": []string{
			"devices",
			"motion.plan", "motion.build", "motion.validate", "motion.tracks",
			"protocol.test",
		},
	}
	if rpcError != nil {
		status["message"] = rpcError.Message
		if s.cliErr != nil {
			status["error"] = s.cliErr.Error()
		}
		return status, nil
	}
	status["cli_path"] = cli.Path
	status["version"] = cli.Version
	status["config_dir"] = cli.ConfigDir
	status["device_table"] = cli.DeviceTable
	if cli.DeviceTable == "" {
		status["message"] = "未在该安装目录下找到设备表；预检会报告所有设备未声明。"
	}
	return status, nil
}

func (s *service) devices(input devicesInput) (any, *himindjsonrpc.Error) {
	cli, rpcError := s.ensure(input.CLIPath)
	if rpcError != nil {
		return nil, rpcError
	}
	if cli.DeviceTable == "" {
		return map[string]any{
			"config_dir": cli.ConfigDir,
			"error":      "该 IOStudio 安装目录下没有设备表，无法列出设备与动作",
		}, nil
	}
	table, err := studio.ReadDeviceTable(cli.ConfigDir, cli.DeviceTable)
	if err != nil {
		return map[string]any{"config_dir": cli.ConfigDir, "error": err.Error()}, nil
	}
	if filter := strings.TrimSpace(input.Device); filter != "" {
		filtered := table
		filtered.Devices = nil
		for _, device := range table.Devices {
			if strings.EqualFold(device.Name, filter) {
				filtered.Devices = append(filtered.Devices, device)
			}
		}
		if len(filtered.Devices) == 0 {
			return map[string]any{
				"file":       table.File,
				"config_dir": cli.ConfigDir,
				"error":      "设备表中没有名为 " + filter + " 的设备",
				"devices":    deviceNames(table),
			}, nil
		}
		return filtered, nil
	}
	return table, nil
}

func (s *service) build(input buildInput, dryRun bool) (any, *himindjsonrpc.Error) {
	cli, rpcError := s.ensure(input.CLIPath)
	if rpcError != nil {
		return nil, rpcError
	}
	root, rpcError := openWorkspace(input.WorkspaceRoot)
	if rpcError != nil {
		return nil, rpcError
	}
	specPath, rpcError := resolveExistingFile(root, input.SpecPath, "spec_path")
	if rpcError != nil {
		return nil, rpcError
	}

	outPath := ""
	if !dryRun {
		if strings.TrimSpace(input.OutPath) == "" {
			// 缺省输出：与规格同名、同目录的 .motion。
			outPath = strings.TrimSuffix(specPath, filepath.Ext(specPath)) + ".motion"
		} else {
			resolved, pathError := root.Resolve(input.OutPath)
			if pathError != nil {
				return nil, &himindjsonrpc.Error{Code: -32602, Message: pathError.Error()}
			}
			outPath = resolved
		}
		if err := workspace.EnsureParentDir(outPath); err != nil {
			return nil, &himindjsonrpc.Error{Code: -32000, Message: "无法创建输出目录: " + err.Error()}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	result, err := studio.Build(ctx, studio.BuildRequest{
		CLI:           cli,
		WorkspaceRoot: root.Abs(),
		SpecPath:      specPath,
		OutPath:       outPath,
		DryRun:        dryRun,
		SkipValidate:  input.SkipValidate,
	})
	if err != nil {
		return nil, &himindjsonrpc.Error{Code: -32000, Message: err.Error()}
	}
	return result, nil
}

func (s *service) validate(input validateInput) (any, *himindjsonrpc.Error) {
	cli, rpcError := s.ensure(input.CLIPath)
	if rpcError != nil {
		return nil, rpcError
	}
	root, rpcError := openWorkspace(input.WorkspaceRoot)
	if rpcError != nil {
		return nil, rpcError
	}
	motionPath, rpcError := resolveExistingFile(root, input.MotionPath, "motion_path")
	if rpcError != nil {
		return nil, rpcError
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	validation, err := studio.ValidateFile(ctx, cli, motionPath)
	if err != nil {
		return nil, &himindjsonrpc.Error{Code: -32000, Message: err.Error()}
	}
	return map[string]any{
		"motion_path":  motionPath,
		"ok":           !validation.HasErrors,
		"validation":   validation,
		"cli":          cli.Path,
		"config_dir":   cli.ConfigDir,
		"device_table": cli.DeviceTable,
	}, nil
}

func (s *service) tracks(input tracksInput) (any, *himindjsonrpc.Error) {
	cli, rpcError := s.ensure(input.CLIPath)
	if rpcError != nil {
		return nil, rpcError
	}
	root, rpcError := openWorkspace(input.WorkspaceRoot)
	if rpcError != nil {
		return nil, rpcError
	}
	motionPath, rpcError := resolveExistingFile(root, input.MotionPath, "motion_path")
	if rpcError != nil {
		return nil, rpcError
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	result, err := studio.ReadTracks(ctx, cli, root.Abs(), motionPath)
	if err != nil {
		return nil, &himindjsonrpc.Error{Code: -32000, Message: err.Error()}
	}
	return result, nil
}

// protocolTest 执行协议测试场景。
//
// 与动作编排无关的另一个域 —— 它能被包装，正是因为 IOStudio 的 CLI 把它做成了一等动词
// （有结构化报告与退出码）。这也是本插件按产品命名而非按功能命名的直接好处：
// 工作台长出新的域时，加一个能力组即可，不必新建插件、重写一遍子进程管道。
func (s *service) protocolTest(input protocolInput) (any, *himindjsonrpc.Error) {
	cli, rpcError := s.ensure(input.CLIPath)
	if rpcError != nil {
		return nil, rpcError
	}
	root, rpcError := openWorkspace(input.WorkspaceRoot)
	if rpcError != nil {
		return nil, rpcError
	}
	scenarioPath, rpcError := resolveExistingFile(root, input.ScenarioPath, "scenario_path")
	if rpcError != nil {
		return nil, rpcError
	}

	reportPath := ""
	if strings.TrimSpace(input.ReportPath) != "" {
		resolved, pathError := root.Resolve(input.ReportPath)
		if pathError != nil {
			return nil, &himindjsonrpc.Error{Code: -32602, Message: pathError.Error()}
		}
		reportPath = resolved
	}

	// 协议测试会真的收发网络报文，给足超时。
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	result, err := studio.ProtocolTest(ctx, studio.ProtocolTestOptions{
		CLI:          cli,
		ScenarioPath: scenarioPath,
		ReportPath:   reportPath,
	})
	if err != nil {
		return nil, &himindjsonrpc.Error{Code: -32000, Message: err.Error()}
	}

	// 默认不保留报告文件：报告内容已结构化回传，落盘会把工作区塞满运行产物。
	keep := input.KeepReport != nil && *input.KeepReport
	if !keep && result.ReportPath != "" {
		_ = os.Remove(result.ReportPath)
		result.ReportPath = ""
	}
	return result, nil
}

// ── 辅助 ──────────────────────────────────────────────

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
		return "", &himindjsonrpc.Error{
			Code: -32602, Message: field + " 指向的文件不存在: " + raw}
	}
	return path, nil
}

func deviceNames(table studio.DeviceTable) []string {
	names := make([]string, 0, len(table.Devices))
	for _, device := range table.Devices {
		names = append(names, device.Name)
	}
	return names
}
