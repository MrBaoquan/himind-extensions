package iostudio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 协议测试台：IOStudio 的另一个域，与动作编排无关。
//
// 它是一等 CLI 动词（`--protocol-test <场景> --report <输出>`），有结构化报告与退出码，
// 因此能被包装成能力 —— 这也是把本插件按**产品**而不是按**功能**命名的理由：
// 工作台有多少个域，插件就能长出多少个能力组，不必每来一个域就新建一个插件、
// 各自复制一遍"定位 CLI、跑子进程、读结果"的管道。

// ProtocolStep 一个步骤的结果。
type ProtocolStep struct {
	Index     int     `json:"index"`
	Op        string  `json:"op"`
	Via       string  `json:"via,omitempty"`
	OK        bool    `json:"ok"`
	ElapsedMs int64   `json:"elapsedMs,omitempty"`
	Reason    string  `json:"reason,omitempty"`
	// Extra 保留报告里本插件没显式建模的字段，避免上游加字段时丢信息。
	Extra map[string]any `json:"-"`
}

// UnmarshalJSON 抽出已知字段，其余进 Extra。
func (s *ProtocolStep) UnmarshalJSON(payload []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return err
	}
	if v, ok := raw["index"].(float64); ok {
		s.Index = int(v)
	}
	if v, ok := raw["op"].(string); ok {
		s.Op = v
	}
	if v, ok := raw["via"].(string); ok {
		s.Via = v
	}
	if v, ok := raw["ok"].(bool); ok {
		s.OK = v
	}
	if v, ok := raw["elapsedMs"].(float64); ok {
		s.ElapsedMs = int64(v)
	}
	if v, ok := raw["reason"].(string); ok {
		s.Reason = v
	}
	s.Extra = map[string]any{}
	for key, value := range raw {
		switch key {
		case "index", "op", "via", "ok", "elapsedMs", "reason":
		default:
			s.Extra[key] = value
		}
	}
	return nil
}

// MarshalJSON 让 Extra 里的字段回到输出。
func (s ProtocolStep) MarshalJSON() ([]byte, error) {
	merged := map[string]any{}
	for key, value := range s.Extra {
		merged[key] = value
	}
	merged["index"] = s.Index
	if s.Op != "" {
		merged["op"] = s.Op
	}
	if s.Via != "" {
		merged["via"] = s.Via
	}
	merged["ok"] = s.OK
	if s.ElapsedMs != 0 {
		merged["elapsedMs"] = s.ElapsedMs
	}
	if s.Reason != "" {
		merged["reason"] = s.Reason
	}
	return json.Marshal(merged)
}

// ProtocolReport 场景执行报告。
type ProtocolReport struct {
	Name    string         `json:"name"`
	OK      bool           `json:"ok"`
	Steps   []ProtocolStep `json:"steps"`
	Summary struct {
		Passed int `json:"passed"`
		Failed int `json:"failed"`
	} `json:"summary"`
}

// ProtocolResult 一次协议测试的结果。
type ProtocolResult struct {
	OK          bool            `json:"ok"`
	Report      *ProtocolReport `json:"report,omitempty"`
	Scenario    string          `json:"scenario"`
	ReportPath  string          `json:"report_path,omitempty"`
	// FailedSteps 只列失败的步骤 —— 报告可能有几十步，全列出来没人看。
	FailedSteps []ProtocolStep `json:"failed_steps,omitempty"`
	Message     string         `json:"message,omitempty"`
	CLI         string         `json:"cli"`
	ConfigDir   string         `json:"config_dir,omitempty"`
	DeviceTable string         `json:"device_table,omitempty"`
	ExitCode    int            `json:"exit_code"`
	ElapsedMs   int64          `json:"elapsed_ms"`
	Stdout      string         `json:"stdout,omitempty"`
	Stderr      string         `json:"stderr,omitempty"`
}

// ProtocolTestOptions 一次协议测试请求。
type ProtocolTestOptions struct {
	CLI          CLI
	ScenarioPath string
	// ReportPath 报告落点；为空时写到场景同目录的 <name>.protocol-report.json。
	ReportPath string
}

// ProtocolTest 执行协议测试场景。
//
// 退出码语义（IOStudio 侧定的）：0 = 全部通过，2 = 有失败步骤或场景执行失败。
// 本函数把两者都当作"跑完了"（返回 result 而非 error），只有基础设施失败才返回 error ——
// 这样调用方能拿到失败报告本身，而不是只收到一个错误字符串。
func ProtocolTest(ctx context.Context, options ProtocolTestOptions) (ProtocolResult, error) {
	if options.CLI.Path == "" {
		return ProtocolResult{}, ErrCLINotFound
	}
	started := time.Now()
	result := ProtocolResult{
		Scenario:    options.ScenarioPath,
		CLI:         options.CLI.Path,
		ConfigDir:   options.CLI.ConfigDir,
		DeviceTable: options.CLI.DeviceTable,
	}

	reportPath := options.ReportPath
	if strings.TrimSpace(reportPath) == "" {
		reportPath = filepath.Join(
			filepath.Dir(options.ScenarioPath),
			strings.TrimSuffix(filepath.Base(options.ScenarioPath), filepath.Ext(options.ScenarioPath))+".protocol-report.json",
		)
	}
	result.ReportPath = reportPath
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		return result, fmt.Errorf("无法创建报告目录: %w", err)
	}
	// 先删旧报告：否则场景执行失败时我们会读到上一次的残留报告，
	// 把"这次没跑成"读成"上次通过了"。
	_ = os.Remove(reportPath)

	run, err := options.CLI.Run(ctx, options.CLI.ConfigDir, []string{
		"--protocol-test", options.ScenarioPath,
		"--report", reportPath,
	})
	if err != nil {
		return result, err
	}
	result.ExitCode = run.ExitCode
	result.Stdout = strings.TrimSpace(run.Stdout)
	result.Stderr = strings.TrimSpace(run.Stderr)
	result.ElapsedMs = time.Since(started).Milliseconds()

	report, readErr := readProtocolReport(reportPath)
	if readErr != nil {
		// 没有报告通常意味着场景本身没跑起来（路径错、JSON 坏）。
		result.OK = false
		result.Message = "未产出报告：" + readErr.Error()
		if result.ExitCode == 0 {
			// 退出码说成功但没报告，属于矛盾状态，明说而不是含糊放过。
			result.Message = "退出码为 0 但未产出报告（结果不可信）"
		}
		return result, nil
	}

	result.Report = report
	result.OK = report.OK
	for _, step := range report.Steps {
		if !step.OK {
			result.FailedSteps = append(result.FailedSteps, step)
		}
	}
	if !report.OK {
		result.Message = fmt.Sprintf("场景 %q: %d 步通过, %d 步失败",
			report.Name, report.Summary.Passed, report.Summary.Failed)
	}
	return result, nil
}

// readProtocolReport 读报告。报告由 IOStudio 以**不带 BOM** 的 UTF-8 写出
// （它刻意这么做的，好让标准解析器直接读），这里仍容错 BOM ——
// 版本回退或手工改过的报告可能带 BOM，不该因此整次失败。
func readProtocolReport(path string) (*ProtocolReport, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取报告失败: %w", err)
	}
	payload = bytes.TrimPrefix(payload, []byte{0xEF, 0xBB, 0xBF})
	var report ProtocolReport
	if err := json.Unmarshal(payload, &report); err != nil {
		return nil, fmt.Errorf("解析报告失败: %w", err)
	}
	return &report, nil
}
