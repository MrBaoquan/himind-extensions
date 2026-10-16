// Package motion 把 IOStudio 的命令行包装成可调用的能力。
//
// 一条硬约束决定了这个插件的形态：**绝不重写编排逻辑**。
//
// 动作文件（.motion）的构建与预检在 IOStudio 里已经是一等公民（`--motion-build` / `--validate`），
// 且预检规则（设备是否声明、动作是否存在、量程、安全联锁）会随产品演进而变化。
// 若在 Go 里复刻一份，就会立刻出现第二真相源——两份实现迟早不一致，而这类不一致是静默的：
// 插件说"通过"，真机播放却丢掉输出。所以这里只做"受控地调用 + 解析结构化结果"。
package iostudio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ErrCLINotFound 表示本机找不到 IOStudio 命令行。
var ErrCLINotFound = errors.New(
	"未找到 IOStudio 命令行。请用 cli_path 指定 IOStudio.exe，" +
		"或安装 IOStudio（其 bin 目录会加入 PATH），或设置 IOSTUDIO_CLI 环境变量")

// CLI 一个已定位的 IOStudio 命令行。
type CLI struct {
	// Path 可执行文件全路径。
	Path string
	// Version 形如 "IOStudio 1.0.16"（取不到时为空）。
	Version string
	// ConfigDir 该安装的设备配置目录（设备表与通道量程都在这里）。
	ConfigDir string
	// DeviceTable 生效的设备表文件名（尽力探测；取不到时为空）。
	DeviceTable string
}

// Locate 按优先级定位 IOStudio 命令行。
//
// 顺序：显式路径 → 环境变量 → PATH → 常见安装位置。
// 显式优先是刻意的：验证用的设备表就是该安装目录下的 Config，
// 所以"用哪一份 IOStudio"同时决定了"拿哪份设备宣告做校验"。
func Locate(explicit string) (CLI, error) {
	candidates := make([]string, 0, 8)
	if value := strings.TrimSpace(explicit); value != "" {
		candidates = append(candidates, value)
	}
	if value := strings.TrimSpace(os.Getenv("IOSTUDIO_CLI")); value != "" {
		candidates = append(candidates, value)
	}
	for _, name := range []string{"iostudio", "iostudio.cmd", "IOStudio.exe", "IOStudio"} {
		if path, err := exec.LookPath(name); err == nil {
			candidates = append(candidates, path)
		}
	}
	if localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localAppData != "" {
		candidates = append(candidates,
			filepath.Join(localAppData, "Programs", "IOStudio", "IOStudio.exe"),
			filepath.Join(localAppData, "Programs", "IOStudio", "bin", "iostudio.cmd"),
		)
	}

	for _, candidate := range candidates {
		resolved, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		info, statErr := os.Stat(resolved)
		if statErr != nil || info.IsDir() {
			continue
		}
		cli := CLI{Path: resolved}
		cli.ConfigDir = resolveConfigDir(resolved)
		cli.DeviceTable = resolveDeviceTable(cli.ConfigDir)
		cli.Version = probeVersion(resolved)
		return cli, nil
	}
	return CLI{}, ErrCLINotFound
}

// resolveConfigDir 找出该安装的设备配置目录。
//
// bin\iostudio.cmd 这类 shim 的上层才是应用根；IOStudio.exe 本身所在即应用根。
func resolveConfigDir(executablePath string) string {
	root := filepath.Dir(executablePath)
	if strings.EqualFold(filepath.Base(root), "bin") {
		root = filepath.Dir(root)
	}
	return filepath.Join(root, "Config")
}

// resolveDeviceTable 尽力判断生效的设备表文件名。
//
// 只做"看哪个文件在"的判断，不解析应用设置——真正的权威仍在 IOStudio 内部；
// 这里是为了让调用方能**看见**在用哪份表，而不是遇到不匹配时毫无线索。
func resolveDeviceTable(configDir string) string {
	// 优先取设置里指名的预设，其次缺省表。
	if preset := configuredDeviceTable(configDir); preset != "" {
		candidate := filepath.Join(configDir, preset)
		if _, err := os.Stat(candidate); err == nil {
			return preset
		}
	}
	const defaultTable = "IODevice.xml"
	if _, err := os.Stat(filepath.Join(configDir, defaultTable)); err == nil {
		return defaultTable
	}
	return ""
}

func configuredDeviceTable(configDir string) string {
	payload, err := os.ReadFile(filepath.Join(configDir, "IOStudioSettings.xml"))
	if err != nil {
		return ""
	}
	const marker = `DeviceTableFile="`
	index := strings.Index(string(payload), marker)
	if index < 0 {
		return ""
	}
	rest := string(payload)[index+len(marker):]
	end := strings.IndexByte(rest, '"')
	if end <= 0 {
		return ""
	}
	return filepath.Base(rest[:end])
}

func probeVersion(executablePath string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	stdout, _, err := run(ctx, executablePath, "", []string{"--version"})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(stdout)
}

// RunResult 一次命令行调用。
type RunResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Duration time.Duration
}

// Run 执行一次命令。
//
// 需要 shell 吗：不需要。.cmd / .bat 由 cmd.exe 执行，其余（含 .exe）直接 CreateProcess。
// 不做通用 shell 转发——插件规范禁止接受任意命令。
func (c CLI) Run(ctx context.Context, workingDir string, args []string) (RunResult, error) {
	if c.Path == "" {
		return RunResult{}, ErrCLINotFound
	}
	stdout, stderr, err := run(ctx, c.Path, workingDir, args)
	result := RunResult{Stdout: stdout, Stderr: stderr}
	if exitErr, ok := err.(*exec.ExitError); ok {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	if err != nil {
		return result, err
	}
	return result, nil
}

func run(ctx context.Context, executablePath, workingDir string, args []string) (string, string, error) {
	commandPath := executablePath
	commandArgs := args
	if isScript(executablePath) {
		// .cmd/.bat 不能直接 CreateProcess，交给 cmd.exe。
		commandPath = "cmd.exe"
		commandArgs = append([]string{"/c", executablePath}, args...)
	}

	command := exec.CommandContext(ctx, commandPath, commandArgs...)
	configureHiddenProcess(command)
	if workingDir != "" {
		command.Dir = workingDir
	}

	var stdout, stderr strings.Builder
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return stdout.String(), stderr.String(), err
}

func isScript(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".cmd", ".bat":
		return true
	}
	return false
}

// StatusFile 一回合运行写出的状态文件。
//
// IOStudio 把结果同时写 stdout（给人看）与状态文件（给程序看）。这里读后者——
// 解析给人看的文本会随文案调整而失效，而状态文件是稳定的结构化契约。
type StatusFile struct {
	Phase        string          `json:"phase"`
	Motion       string          `json:"motion"`
	MotionExists bool            `json:"motionExists"`
	Tracks       json.RawMessage `json:"tracks"`
	Validation   *Validation     `json:"validation"`
	IsPlaying    *bool           `json:"isPlaying"`
}

// Validation 预检结果。
type Validation struct {
	HasErrors    bool    `json:"hasErrors"`
	ErrorCount   int     `json:"errorCount"`
	WarningCount int     `json:"warningCount"`
	InfoCount    int     `json:"infoCount"`
	Issues       []Issue `json:"issues"`
	Error        string  `json:"error"`
}

// Issue 一条预检问题。
type Issue struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	// 其余字段（轨道、时间点等）按原样带出，避免因为版本新增字段而丢信息。
	Extra map[string]any `json:"-"`
}

// UnmarshalJSON 保留 Issue 的全部字段：已知字段抽出来，未知字段进 Extra。
// 这样 IOStudio 将来给 Issue 加字段时，插件不会把它们丢掉。
func (i *Issue) UnmarshalJSON(payload []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return err
	}
	if value, ok := raw["code"].(string); ok {
		i.Code = value
	}
	if value, ok := raw["severity"].(string); ok {
		i.Severity = value
	}
	if value, ok := raw["message"].(string); ok {
		i.Message = value
	}
	i.Extra = map[string]any{}
	for key, value := range raw {
		switch key {
		case "code", "severity", "message":
		default:
			i.Extra[key] = value
		}
	}
	return nil
}

// MarshalJSON 让 Extra 里的字段回到输出里。
func (i Issue) MarshalJSON() ([]byte, error) {
	merged := map[string]any{}
	for key, value := range i.Extra {
		merged[key] = value
	}
	if i.Code != "" {
		merged["code"] = i.Code
	}
	if i.Severity != "" {
		merged["severity"] = i.Severity
	}
	if i.Message != "" {
		merged["message"] = i.Message
	}
	return json.Marshal(merged)
}

// ReadStatus 读取状态文件。文件不存在时返回 nil（该动词可能没写状态文件）。
func ReadStatus(path string) (*StatusFile, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取状态文件失败: %w", err)
	}
	// 去掉 UTF-8 BOM：写入方用 .NET 的 Encoding.UTF8，它会带 BOM（EF BB BF），
	// 而 Go 的 json.Unmarshal 遇到 BOM 会直接报 "invalid character 'ï'"。
	// 不清掉的话每一次调用都会失败。
	payload = bytes.TrimPrefix(payload, []byte{0xEF, 0xBB, 0xBF})
	var status StatusFile
	if err := json.Unmarshal(payload, &status); err != nil {
		return nil, fmt.Errorf("解析状态文件失败: %w", err)
	}
	return &status, nil
}
