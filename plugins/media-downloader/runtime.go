package main

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

//go:embed runtime-lock.json
var runtimeLockFile embed.FS

type runtimeLock struct {
	SchemaVersion int    `json:"schema_version"`
	Runtime       string `json:"runtime"`
	Version       string `json:"version"`
	PypiVersion   string `json:"pypi_version"`
	PypiPackage   string `json:"pypi_package"`
	PypiSHA256    string `json:"pypi_sha256"`
	Asset         string `json:"asset"`
	URL           string `json:"url"`
	SHA256        string `json:"sha256"`
	Size          int64  `json:"size"`
	License       struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"license"`
}

type runtimeLocation struct {
	Path    string
	Source  string
	Version string
}

func readRuntimeLock() (runtimeLock, error) {
	var lock runtimeLock
	payload, err := runtimeLockFile.ReadFile("runtime-lock.json")
	if err != nil {
		return lock, err
	}
	if err := json.Unmarshal(payload, &lock); err != nil {
		return lock, err
	}
	if lock.Runtime == "" || lock.Version == "" || lock.PypiVersion == "" || lock.PypiPackage == "" || lock.PypiSHA256 == "" || lock.Asset == "" || lock.URL == "" || lock.SHA256 == "" {
		return lock, errors.New("runtime-lock.json 缺少必要字段")
	}
	if lock.PypiPackage != "yt-dlp" || len(lock.PypiSHA256) != 64 {
		return lock, errors.New("runtime-lock.json 的 pypi_package 或 pypi_sha256 不合法")
	}
	if lock.Size <= 0 || lock.Size > 64<<20 {
		return lock, errors.New("runtime-lock.json 的文件大小超出允许范围")
	}
	return lock, nil
}

func managedRuntimePathIn(root, version string) string {
	return filepath.Join(root, "yt-dlp", version, "yt-dlp.exe")
}

func managedVenvRuntimePathIn(root, version string) string {
	return filepath.Join(root, "yt-dlp", version, "venv", "Scripts", "yt-dlp.exe")
}

func managedRuntimePath(version string) (string, error) {
	root, err := runtimesRoot()
	if err != nil {
		return "", err
	}
	return managedRuntimePathIn(root, version), nil
}

func managedVenvRuntimePath(version string) (string, error) {
	root, err := runtimesRoot()
	if err != nil {
		return "", err
	}
	return managedVenvRuntimePathIn(root, version), nil
}

func locateYtDlp() (runtimeLocation, error) {
	lock, err := readRuntimeLock()
	if err != nil {
		return runtimeLocation{}, err
	}
	checkedRoots := make([]string, 0)
	if roots, err := runtimesRoots(); err == nil {
		checkedRoots = append(checkedRoots, roots...)
		for _, root := range roots {
			if managed := managedRuntimePathIn(root, lock.Version); fileExists(managed) {
				return runtimeLocation{Path: managed, Source: "managed", Version: lock.Version}, nil
			}
			if venv := managedVenvRuntimePathIn(root, lock.Version); fileExists(venv) {
				return runtimeLocation{Path: venv, Source: "managed-venv", Version: lock.Version}, nil
			}
		}
	} else {
		checkedRoots = append(checkedRoots, "runtimesRoots error: "+err.Error())
	}
	for _, name := range []string{"yt-dlp.exe", "yt-dlp"} {
		if path, err := exec.LookPath(name); err == nil {
			return runtimeLocation{Path: path, Source: "path", Version: lock.Version}, nil
		}
	}
	return runtimeLocation{}, fmt.Errorf("未找到 yt-dlp，请先调用 media.downloader.runtime.install（已检查: %s）", strings.Join(checkedRoots, "; "))
}

func runtimeVersion(ctx context.Context, path string) (string, error) {
	command := exec.CommandContext(ctx, path, "--version")
	configureHiddenProcess(command)
	output, err := command.Output()
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(output))
	if line == "" {
		return "", errors.New("yt-dlp 没有返回版本号")
	}
	if index := strings.IndexByte(line, '\n'); index >= 0 {
		line = line[:index]
	}
	return strings.TrimSpace(line), nil
}

func ffmpegStatus(ctx context.Context) (string, string) {
	path, err := exec.LookPath("ffmpeg.exe")
	if err != nil {
		path, err = exec.LookPath("ffmpeg")
	}
	if err != nil {
		return "", ""
	}
	command := exec.CommandContext(ctx, path, "-version")
	configureHiddenProcess(command)
	output, err := command.Output()
	if err != nil {
		return path, ""
	}
	line := strings.TrimSpace(string(output))
	if index := strings.IndexByte(line, '\n'); index >= 0 {
		line = line[:index]
	}
	return path, strings.TrimSpace(line)
}

func buildRuntimeStatus(ctx context.Context) RuntimeStatus {
	lock, err := readRuntimeLock()
	if err != nil {
		return RuntimeStatus{State: "error", DownloadsDir: defaultDownloadsDir(), Capabilities: []string{}, Message: err.Error()}
	}
	dataRoot, _ := pluginDataRoot()
	downloadsDir := defaultDownloadsDir()
	result := RuntimeStatus{
		State:             "missing",
		ExpectedVersion:   lock.Version,
		DataRoot:          dataRoot,
		DataRootWritable:  dataRoot != "" && canWriteDirectory(dataRoot),
		DownloadsDir:      downloadsDir,
		DownloadsWritable: downloadsDir != "" && canWriteDirectory(downloadsDir),
		Capabilities:      []string{},
	}
	location, err := locateYtDlp()
	if err != nil {
		result.Message = err.Error()
		if path, version := ffmpegStatus(ctx); path != "" {
			result.FFmpegPath = path
			result.FFmpegVersion = version
		}
		return result
	}
	result.Source = location.Source
	result.Path = location.Path
	version, err := runtimeVersion(ctx, location.Path)
	if err != nil {
		result.State = "broken"
		result.Message = "yt-dlp 无法启动: " + err.Error()
		return result
	}
	result.Version = version
	result.State = "ready"
	result.Capabilities = []string{"info", "download", "playlist", "subtitles"}
	if strings.HasPrefix(location.Source, "managed") && !sameVersion(version, lock.Version) {
		result.State = "outdated"
		result.Message = "受管运行时版本与清单固定版本不一致"
	}
	if path, ffmpegVersion := ffmpegStatus(ctx); path != "" {
		result.FFmpegPath = path
		result.FFmpegVersion = ffmpegVersion
		result.Capabilities = append(result.Capabilities, "merge", "audio-extract")
	}
	return result
}

func installRuntime(ctx context.Context, force bool) (RuntimeInstallResult, error) {
	lock, err := readRuntimeLock()
	if err != nil {
		return RuntimeInstallResult{}, err
	}
	target, err := managedRuntimePath(lock.Version)
	if err != nil {
		return RuntimeInstallResult{}, err
	}
	if !force && fileExists(target) {
		digest, _, err := sha256File(target)
		if err == nil && strings.EqualFold(digest, lock.SHA256) {
			return RuntimeInstallResult{State: "ready", Version: lock.Version, Path: target, SHA256: digest, Installed: false, Mode: "binary", Message: "运行时已安装且摘要匹配"}, nil
		}
	}
	if !force {
		if venv, err := managedVenvRuntimePath(lock.Version); err == nil && fileExists(venv) {
			if version, versionErr := runtimeVersion(ctx, venv); versionErr == nil && sameVersion(version, lock.Version) {
				return RuntimeInstallResult{State: "ready", Version: version, Path: venv, Installed: false, Mode: "python-venv", Message: "Python 备用运行时已安装"}, nil
			}
		}
	}
	binaryErr := downloadOfficialRuntime(ctx, lock, target)
	if binaryErr == nil {
		digest, _, err := sha256File(target)
		if err != nil {
			return RuntimeInstallResult{}, err
		}
		return RuntimeInstallResult{State: "ready", Version: lock.Version, Path: target, SHA256: digest, Installed: true, Mode: "binary", Message: "官方运行时已安装并完成摘要校验"}, nil
	}
	venvResult, venvErr := installPythonRuntime(ctx, lock)
	if venvErr == nil {
		return venvResult, nil
	}
	return RuntimeInstallResult{}, fmt.Errorf("官方二进制安装失败: %v；Python 备用安装失败: %v", binaryErr, venvErr)
}

func downloadOfficialRuntime(ctx context.Context, lock runtimeLock, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, lock.URL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "HiMind-Agent-Media-Downloader/"+lock.Version)
	client := &http.Client{Timeout: 75 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", response.StatusCode)
	}
	temp := target + ".download"
	file, err := os.Create(temp)
	if err != nil {
		return err
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hasher), io.LimitReader(response.Body, lock.Size+1))
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(temp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(temp)
		return closeErr
	}
	if written != lock.Size {
		_ = os.Remove(temp)
		return fmt.Errorf("大小不匹配: 期望 %d，实际 %d", lock.Size, written)
	}
	digest := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(digest, lock.SHA256) {
		_ = os.Remove(temp)
		return fmt.Errorf("摘要不匹配: 期望 %s，实际 %s", lock.SHA256, digest)
	}
	if err := replaceFile(temp, target); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

func installPythonRuntime(ctx context.Context, lock runtimeLock) (RuntimeInstallResult, error) {
	pythonPath, err := lookPathAny("python.exe", "python")
	if err != nil {
		return RuntimeInstallResult{}, errors.New("未找到 Python，无法使用备用安装方式")
	}
	target, err := managedVenvRuntimePath(lock.Version)
	if err != nil {
		return RuntimeInstallResult{}, err
	}
	venvDir := filepath.Dir(filepath.Dir(target))
	if err := os.MkdirAll(venvDir, 0o755); err != nil {
		return RuntimeInstallResult{}, err
	}
	createCtx, createCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer createCancel()
	create := exec.CommandContext(createCtx, pythonPath, "-m", "venv", venvDir)
	configureHiddenProcess(create)
	if output, err := create.CombinedOutput(); err != nil {
		return RuntimeInstallResult{}, fmt.Errorf("创建 Python 虚拟环境失败: %v: %s", err, strings.TrimSpace(string(output)))
	}
	venvPython := filepath.Join(venvDir, "Scripts", "python.exe")
	if !fileExists(venvPython) {
		venvPython = filepath.Join(venvDir, "bin", "python")
	}
	if !fileExists(venvPython) {
		return RuntimeInstallResult{}, errors.New("Python 虚拟环境缺少解释器")
	}
	installCtx, installCancel := context.WithTimeout(ctx, 8*time.Minute)
	defer installCancel()
	requirementsPath := filepath.Join(venvDir, "requirements.lock.txt")
	requirements := lock.PypiPackage + "==" + lock.PypiVersion + " --hash=sha256:" + lock.PypiSHA256 + "\n"
	if err := os.WriteFile(requirementsPath, []byte(requirements), 0o644); err != nil {
		return RuntimeInstallResult{}, err
	}
	defer os.Remove(requirementsPath)
	command := exec.CommandContext(installCtx, venvPython, "-m", "pip", "install", "--disable-pip-version-check", "--no-input", "--no-deps", "--require-hashes", "--requirement", requirementsPath)
	configureHiddenProcess(command)
	output, err := command.CombinedOutput()
	if err != nil {
		return RuntimeInstallResult{}, fmt.Errorf("从 PyPI 安装 yt-dlp 失败: %v: %s", err, strings.TrimSpace(string(output)))
	}
	if !fileExists(target) {
		return RuntimeInstallResult{}, errors.New("PyPI 安装后没有找到 yt-dlp 命令")
	}
	version, err := runtimeVersion(ctx, target)
	if err != nil {
		return RuntimeInstallResult{}, err
	}
	if !sameVersion(version, lock.Version) {
		return RuntimeInstallResult{}, fmt.Errorf("PyPI 安装版本不匹配: 期望 %s，实际 %s", lock.Version, version)
	}
	return RuntimeInstallResult{State: "ready", Version: version, Path: target, Installed: true, Mode: "python-venv", Message: "官方二进制不可用，已通过 PyPI 安装固定版本的 Python 备用运行时"}, nil
}

func replaceFile(source, target string) error {
	if err := os.Rename(source, target); err == nil {
		return nil
	}
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(source, target)
}

func lookPathAny(names ...string) (string, error) {
	for _, name := range names {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("未找到可执行文件: %s", strings.Join(names, ", "))
}

func sameVersion(left, right string) bool {
	return normalizeVersion(left) == normalizeVersion(right)
}

func normalizeVersion(value string) string {
	parts := strings.Split(strings.TrimSpace(strings.TrimPrefix(value, "v")), ".")
	for index, part := range parts {
		number, err := strconv.Atoi(part)
		if err == nil {
			parts[index] = strconv.Itoa(number)
		}
	}
	return strings.Join(parts, ".")
}
