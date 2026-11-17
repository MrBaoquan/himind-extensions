package mediadownload

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type pythonProbe struct {
	Executable string `json:"executable"`
	Major      int    `json:"major"`
	Minor      int    `json:"minor"`
	Venv       bool   `json:"venv"`
}

const pythonProbeCode = `import sys, json, importlib.util; print(json.dumps(dict(executable=sys.executable, major=sys.version_info.major, minor=sys.version_info.minor, venv=importlib.util.find_spec('venv') is not None)))`

func pythonCommandOutput(ctx context.Context, executable string, args ...string) ([]byte, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(probeCtx, executable, args...)
	configureHiddenProcess(command)
	return command.Output()
}

func findRuntimePython(ctx context.Context) (string, error) {
	return findRuntimePythonWith(ctx, exec.LookPath, pythonCommandOutput)
}

func findRuntimePythonWith(ctx context.Context, lookup func(string) (string, error), output func(context.Context, string, ...string) ([]byte, error)) (string, error) {
	seen := make(map[string]bool)
	probe := func(executable string, prefix ...string) string {
		key := strings.ToLower(executable + " " + strings.Join(prefix, " "))
		if seen[key] || ctx.Err() != nil {
			return ""
		}
		seen[key] = true
		args := append(append([]string{}, prefix...), "-I", "-c", pythonProbeCode)
		payload, err := output(ctx, executable, args...)
		if err != nil {
			return ""
		}
		var result pythonProbe
		if json.Unmarshal(payload, &result) != nil || result.Major != 3 || result.Minor < 10 || !result.Venv || !filepath.IsAbs(result.Executable) || !fileExists(result.Executable) {
			return ""
		}
		return result.Executable
	}
	for _, name := range []string{"python.exe", "python3.exe", "python", "python3", "py.exe", "py"} {
		if executable, err := lookup(name); err == nil {
			var prefix []string
			if name == "py.exe" || name == "py" {
				prefix = []string{"-3"}
			}
			if result := probe(executable, prefix...); result != "" {
				return result, nil
			}
		}
	}
	// uv is optional. Discover only an existing interpreter, never install one
	// or use a client-specific bundled Python as a product dependency.
	if uv, err := lookUpAny(lookup, "uv.exe", "uv"); err == nil && ctx.Err() == nil {
		if payload, err := output(ctx, uv, "python", "find", "--offline", "--no-python-downloads"); err == nil {
			if result := probe(strings.TrimSpace(string(payload))); result != "" {
				return result, nil
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "", errors.New("未找到可用的 Python 3.10+（需要 venv）。Windows Store 占位命令不属于已安装的 Python；可安装 Python，或让 uv 使用已有解释器后重试")
}

func lookUpAny(lookup func(string) (string, error), names ...string) (string, error) {
	for _, name := range names {
		if executable, err := lookup(name); err == nil {
			return executable, nil
		}
	}
	return "", errors.New("未找到命令")
}
