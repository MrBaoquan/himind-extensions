// motion-composer is the deterministic review gate between motion intent and
// IOStudio. It deliberately does not build .motion files: IOStudio stays the
// authority for expansion, channel declarations, limits and safety validation.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"himind-plugin/motion-composer/internal/analysis"
	"himind-plugin/motion-composer/internal/himindjsonrpc"
)

const (
	capRuntimeStatus   = "motion.composer.runtime.status"
	capAudit           = "motion.composer.audit"
	capCompare         = "motion.composer.compare"
	capPlatformResolve = "motion.composer.platform.resolve"
	version            = "1.2.0"
)

type service struct{}

var defaultService = &service{}

func main() {
	_ = himindjsonrpc.Serve(os.Stdin, os.Stdout, defaultService.handle)
}

func (s *service) handle(request himindjsonrpc.Request) (any, *himindjsonrpc.Error) {
	switch request.Method {
	case capRuntimeStatus:
		return map[string]any{
			"name":         "motion-composer",
			"version":      version,
			"capabilities": []string{capAudit, capCompare, capPlatformResolve},
			"principles": []string{
				"quality audit is deterministic",
				"IOStudio remains the build and safety authority",
				"high-energy flat segments are errors, not style warnings",
			},
		}, nil
	case capAudit:
		var input auditInput
		if rpcError := himindjsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		return s.audit(input)
	case capCompare:
		var input compareInput
		if rpcError := himindjsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		return s.compare(input)
	case capPlatformResolve:
		var input platformResolveInput
		if rpcError := himindjsonrpc.DecodeParams(request, &input); rpcError != nil {
			return nil, rpcError
		}
		return s.resolvePlatform(input)
	default:
		return nil, &himindjsonrpc.Error{Code: -32601, Message: fmt.Sprintf("未知能力: %s", request.Method)}
	}
}

type auditInput struct {
	WorkspaceRoot     string               `json:"workspace_root"`
	SpecPath          string               `json:"spec_path"`
	TimelinePath      string               `json:"behavior_timeline_path"`
	DeviceProfilePath string               `json:"device_profile_path"`
	Thresholds        *analysis.Thresholds `json:"thresholds"`
}

type compareInput struct {
	WorkspaceRoot     string               `json:"workspace_root"`
	SpecPaths         []string             `json:"spec_paths"`
	TimelinePath      string               `json:"behavior_timeline_path"`
	DeviceProfilePath string               `json:"device_profile_path"`
	Thresholds        *analysis.Thresholds `json:"thresholds"`
}

type platformResolveInput struct {
	WorkspaceRoot       string `json:"workspace_root"`
	PlatformProfilePath string `json:"platform_profile_path"`
	MotionIntentPath    string `json:"motion_intent_path"`
	DeviceProfilePath   string `json:"device_profile_path"`
}

func (s *service) audit(input auditInput) (any, *himindjsonrpc.Error) {
	root, rpcError := openWorkspace(input.WorkspaceRoot)
	if rpcError != nil {
		return nil, rpcError
	}
	specPath, rpcError := resolveExisting(root, input.SpecPath, "spec_path")
	if rpcError != nil {
		return nil, rpcError
	}
	_, timeline, profile, rpcError := loadInputs(root, input.TimelinePath, input.DeviceProfilePath)
	if rpcError != nil {
		return nil, rpcError
	}
	payload, err := os.ReadFile(specPath)
	if err != nil {
		return nil, &himindjsonrpc.Error{Code: -32000, Message: "读取 spec 失败: " + err.Error()}
	}
	showSpec, err := analysis.DecodeSpec(trimBOM(payload))
	if err != nil {
		return nil, &himindjsonrpc.Error{Code: -32602, Message: err.Error()}
	}
	thresholds := analysis.DefaultThresholds()
	if input.Thresholds != nil {
		thresholds = *input.Thresholds
	}
	return analysis.Audit(showSpec, specPath, timeline, profile, thresholds), nil
}

func (s *service) compare(input compareInput) (any, *himindjsonrpc.Error) {
	root, rpcError := openWorkspace(input.WorkspaceRoot)
	if rpcError != nil {
		return nil, rpcError
	}
	if len(input.SpecPaths) < 2 {
		return nil, &himindjsonrpc.Error{Code: -32602, Message: "spec_paths 至少需要两套规格"}
	}
	_, timeline, profile, rpcError := loadInputs(root, input.TimelinePath, input.DeviceProfilePath)
	if rpcError != nil {
		return nil, rpcError
	}
	thresholds := analysis.DefaultThresholds()
	if input.Thresholds != nil {
		thresholds = *input.Thresholds
	}

	reports := make([]analysis.Report, 0, len(input.SpecPaths))
	specs := make([]analysis.ShowSpec, 0, len(input.SpecPaths))
	for _, requested := range input.SpecPaths {
		specPath, rpcError := resolveExisting(root, requested, "spec_paths")
		if rpcError != nil {
			return nil, rpcError
		}
		payload, err := os.ReadFile(specPath)
		if err != nil {
			return nil, &himindjsonrpc.Error{Code: -32000, Message: "读取 spec 失败: " + err.Error()}
		}
		showSpec, err := analysis.DecodeSpec(trimBOM(payload))
		if err != nil {
			return nil, &himindjsonrpc.Error{Code: -32602, Message: err.Error()}
		}
		specs = append(specs, showSpec)
		reports = append(reports, analysis.Audit(showSpec, specPath, timeline, profile, thresholds))
	}

	sameTiming := analysis.SameCueTiming(specs)
	ok := sameTiming
	for _, report := range reports {
		ok = ok && report.OK
	}
	return map[string]any{
		"ok":              ok,
		"same_cue_timing": sameTiming,
		"reports":         reports,
	}, nil
}

func (s *service) resolvePlatform(input platformResolveInput) (any, *himindjsonrpc.Error) {
	root, rpcError := openWorkspace(input.WorkspaceRoot)
	if rpcError != nil {
		return nil, rpcError
	}
	profilePath, rpcError := resolveExisting(root, input.PlatformProfilePath, "platform_profile_path")
	if rpcError != nil {
		return nil, rpcError
	}
	intentPath, rpcError := resolveExisting(root, input.MotionIntentPath, "motion_intent_path")
	if rpcError != nil {
		return nil, rpcError
	}
	devicePath, rpcError := resolveExisting(root, input.DeviceProfilePath, "device_profile_path")
	if rpcError != nil {
		return nil, rpcError
	}
	profilePayload, err := os.ReadFile(profilePath)
	if err != nil {
		return nil, &himindjsonrpc.Error{Code: -32000, Message: "读取平台 profile 失败: " + err.Error()}
	}
	intentPayload, err := os.ReadFile(intentPath)
	if err != nil {
		return nil, &himindjsonrpc.Error{Code: -32000, Message: "读取 motion intent 失败: " + err.Error()}
	}
	devicePayload, err := os.ReadFile(devicePath)
	if err != nil {
		return nil, &himindjsonrpc.Error{Code: -32000, Message: "读取 device profile 失败: " + err.Error()}
	}
	binding, err := analysis.CompilePlatformBindingWithDevice(profilePayload, intentPayload, devicePayload)
	if err != nil {
		return nil, &himindjsonrpc.Error{Code: -32602, Message: err.Error()}
	}
	return binding, nil
}

func loadInputs(root string, timelinePath, profilePath string) (string, *analysis.Timeline, *analysis.DeviceProfile, *himindjsonrpc.Error) {
	var timeline *analysis.Timeline
	if strings.TrimSpace(timelinePath) != "" {
		path, rpcError := resolveExisting(root, timelinePath, "behavior_timeline_path")
		if rpcError != nil {
			return "", nil, nil, rpcError
		}
		payload, err := os.ReadFile(path)
		if err != nil {
			return "", nil, nil, &himindjsonrpc.Error{Code: -32000, Message: "读取行为时间线失败: " + err.Error()}
		}
		timeline, err = analysis.DecodeTimeline(trimBOM(payload))
		if err != nil {
			return "", nil, nil, &himindjsonrpc.Error{Code: -32602, Message: err.Error()}
		}
	}
	var profile *analysis.DeviceProfile
	if strings.TrimSpace(profilePath) != "" {
		path, rpcError := resolveExisting(root, profilePath, "device_profile_path")
		if rpcError != nil {
			return "", nil, nil, rpcError
		}
		payload, err := os.ReadFile(path)
		if err != nil {
			return "", nil, nil, &himindjsonrpc.Error{Code: -32000, Message: "读取设备 profile 失败: " + err.Error()}
		}
		profile, err = analysis.DecodeProfile(trimBOM(payload))
		if err != nil {
			return "", nil, nil, &himindjsonrpc.Error{Code: -32602, Message: err.Error()}
		}
	}
	return "", timeline, profile, nil
}

func openWorkspace(requested string) (string, *himindjsonrpc.Error) {
	if strings.TrimSpace(requested) == "" {
		return "", &himindjsonrpc.Error{Code: -32602, Message: "workspace_root 不能为空"}
	}
	root, err := filepath.Abs(requested)
	if err != nil {
		return "", &himindjsonrpc.Error{Code: -32602, Message: "workspace_root 无效: " + err.Error()}
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", &himindjsonrpc.Error{Code: -32602, Message: "workspace_root 不存在或不是目录"}
	}
	return root, nil
}

func resolveExisting(root, requested, field string) (string, *himindjsonrpc.Error) {
	if strings.TrimSpace(requested) == "" {
		return "", &himindjsonrpc.Error{Code: -32602, Message: field + " 不能为空"}
	}
	path := requested
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", &himindjsonrpc.Error{Code: -32602, Message: field + " 无效: " + err.Error()}
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", &himindjsonrpc.Error{Code: -32602, Message: field + " 必须位于 workspace_root 内"}
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", &himindjsonrpc.Error{Code: -32602, Message: field + " 文件不存在"}
	}
	return path, nil
}

func trimBOM(payload []byte) []byte {
	if len(payload) >= 3 && payload[0] == 0xEF && payload[1] == 0xBB && payload[2] == 0xBF {
		return payload[3:]
	}
	return payload
}

var _ = json.Valid
