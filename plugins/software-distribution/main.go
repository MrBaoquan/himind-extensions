package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/MrBaoquan/himind-extensions/sdk/jsonrpc"
)

var identifierPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

type input struct {
	WorkspaceRoot  string `json:"workspace_root"`
	ProjectPath    string `json:"project_path"`
	ArtifactPath   string `json:"artifact_path"`
	ProductID      string `json:"product_id"`
	Version        string `json:"version"`
	CurrentVersion string `json:"current_version"`
	Channel        string `json:"channel"`
	Platform       string `json:"platform"`
	Architecture   string `json:"architecture"`
	PackageType    string `json:"package_type"`
}

type resolveResponse struct {
	Update any `json:"update"`
}

func main() {
	if err := jsonrpc.Serve(os.Stdin, os.Stdout, handle); err != nil {
		fmt.Fprintln(os.Stderr, "软件分发插件已停止:", err)
	}
}

func handle(request jsonrpc.Request) (any, *jsonrpc.Error) {
	var in input
	if rpcError := jsonrpc.DecodeParams(request, &in); rpcError != nil {
		return nil, rpcError
	}
	switch request.Method {
	case "software.distribution.project.inspect":
		result, err := inspectProject(in)
		return rpcResult(result, err)
	case "software.distribution.artifact.inspect":
		result, err := inspectArtifact(in)
		return rpcResult(result, err)
	case "software.distribution.release.resolve":
		result, err := resolveRelease(in)
		return rpcResult(result, err)
	default:
		return nil, &jsonrpc.Error{Code: -32602, Message: "不支持的软件分发能力"}
	}
}

func rpcResult(result any, err error) (any, *jsonrpc.Error) {
	if err != nil {
		return nil, &jsonrpc.Error{Code: -32602, Message: err.Error()}
	}
	return result, nil
}

func inspectProject(in input) (any, error) {
	root, target, err := workspacePath(in.WorkspaceRoot, in.ProjectPath)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		return nil, errors.New("项目目录不存在")
	}
	projectType := "unknown"
	recommendation := map[string]any{}
	if matches, _ := filepath.Glob(filepath.Join(target, "*.csproj")); len(matches) > 0 {
		projectType = "dotnet"
		recommendation = map[string]any{
			"protocol_package": "HiMind.Distribution", "windows_adapter": "HiMind.Distribution.Windows",
			"package_type": "directory-zip", "platform": "windows", "architecture": "x64",
		}
	} else if isDirectory(filepath.Join(target, "Assets")) && isDirectory(filepath.Join(target, "ProjectSettings")) {
		projectType = "unity"
		recommendation = map[string]any{
			"windows_package_type": "directory-zip", "android_package_type": "apk",
			"runtime_note": "Unity 仅复用分发协议；Windows 与 Android 安装分别由平台适配器完成。",
		}
	}
	return map[string]any{
		"workspace_root": root, "project_path": target, "project_type": projectType,
		"product_id": strings.TrimSpace(in.ProductID), "recommendation": recommendation,
		"runtime_dependency": "Dashboard public distribution API", "agent_runtime_required": false,
	}, nil
}

func inspectArtifact(in input) (any, error) {
	_, target, err := workspacePath(in.WorkspaceRoot, in.ArtifactPath)
	if err != nil {
		return nil, err
	}
	if err := validateIdentity(in); err != nil {
		return nil, err
	}
	file, err := os.Open(target)
	if err != nil {
		return nil, errors.New("无法读取制品文件")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("制品必须是普通文件")
	}
	if err := validatePackageExtension(in.PackageType, filepath.Ext(target)); err != nil {
		return nil, err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return nil, errors.New("计算制品摘要失败")
	}
	return map[string]any{
		"ready": true, "artifact_path": target, "file_name": filepath.Base(target), "size": info.Size(),
		"sha256": hex.EncodeToString(hash.Sum(nil)), "product_id": in.ProductID, "version": in.Version,
		"channel": defaultValue(in.Channel, "stable"), "platform": in.Platform,
		"architecture": in.Architecture, "package_type": in.PackageType,
	}, nil
}

func resolveRelease(in input) (any, error) {
	if err := validateIdentity(in); err != nil {
		return nil, err
	}
	base, err := configuredDashboardURL()
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]string{
		"productId": in.ProductID, "currentVersion": defaultValue(in.CurrentVersion, "0.0.0"),
		"channel": defaultValue(in.Channel, "stable"), "platform": in.Platform, "architecture": in.Architecture,
	})
	client := &http.Client{Timeout: 20 * time.Second}
	response, err := client.Post(base+"/api/software-distribution/v1/updates/resolve", "application/json", strings.NewReader(string(body)))
	if err != nil {
		return nil, errors.New("Dashboard resolve 请求失败")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Dashboard resolve 返回 HTTP %d", response.StatusCode)
	}
	var result resolveResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
		return nil, errors.New("Dashboard resolve 响应无效")
	}
	return map[string]any{"dashboard": base, "product_id": in.ProductID, "update": result.Update}, nil
}

func workspacePath(workspaceRoot, target string) (string, string, error) {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	target = strings.TrimSpace(target)
	if workspaceRoot == "" || target == "" {
		return "", "", errors.New("workspace_root 和目标路径不能为空")
	}
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", "", err
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return "", "", err
	}
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || filepath.IsAbs(relative) {
		return "", "", errors.New("目标路径必须位于 workspace_root 内")
	}
	return root, target, nil
}

func validateIdentity(in input) error {
	for name, value := range map[string]string{"product_id": in.ProductID, "platform": in.Platform, "architecture": in.Architecture} {
		if !identifierPattern.MatchString(strings.ToLower(strings.TrimSpace(value))) {
			return fmt.Errorf("%s 不是有效的稳定标识", name)
		}
	}
	if strings.TrimSpace(in.Version) == "" && strings.TrimSpace(in.CurrentVersion) == "" {
		return errors.New("version 或 current_version 至少填写一个")
	}
	switch in.PackageType {
	case "directory-zip", "apk", "unity-addressables", "content":
	case "":
		if in.ArtifactPath != "" {
			return errors.New("package_type 不能为空")
		}
	default:
		return errors.New("不支持的 package_type")
	}
	return nil
}

func validatePackageExtension(packageType, extension string) error {
	extension = strings.ToLower(extension)
	if packageType == "apk" && extension != ".apk" {
		return errors.New("apk 包类型必须使用 .apk 文件")
	}
	if (packageType == "directory-zip" || packageType == "unity-addressables") && extension != ".zip" {
		return errors.New("ZIP 包类型必须使用 .zip 文件")
	}
	return nil
}

func configuredDashboardURL() (string, error) {
	value := strings.TrimSpace(os.Getenv("HIMIND_DASHBOARD_URL"))
	if value == "" {
		value = strings.TrimSpace(os.Getenv("HIMIND_API_BASE"))
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && !(parsed.Scheme == "http" && (parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1"))) {
		return "", errors.New("Agent 未配置可信 Dashboard 地址")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func isDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func defaultValue(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}
