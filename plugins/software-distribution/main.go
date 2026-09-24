package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/MrBaoquan/himind-extensions/sdk/jsonrpc"
)

var identifierPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
var sha256Pattern = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

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
	Repository     string `json:"repository"`
	ChannelURL     string `json:"channel_url"`
	Reference      string `json:"reference"`
	AllowUnsigned  bool   `json:"allow_unsigned"`
	AllowDowngrade bool   `json:"allow_downgrade"`
	InstanceID     string `json:"instance_id"`
}

type channelSignature struct {
	KeyID     string `json:"key_id"`
	Algorithm string `json:"algorithm"`
	Value     string `json:"value"`
}

type channelRelease struct {
	Version          string            `json:"version"`
	FileName         string            `json:"file_name"`
	SizeBytes        int64             `json:"size_bytes"`
	SHA256           string            `json:"sha256"`
	DownloadURL      string            `json:"download_url"`
	Signature        *channelSignature `json:"signature"`
	Mandatory        bool              `json:"mandatory"`
	RolloutPercent   int               `json:"rollout_percent"`
	MinClientVersion string            `json:"min_client_version"`
	PublishedAt      string            `json:"published_at"`
	ReleaseNotes     string            `json:"release_notes"`
	Revoked          bool              `json:"revoked"`
}

type softwareDistributionChannel struct {
	SchemaVersion string         `json:"schema_version"`
	ProductID     string         `json:"product_id"`
	ProductName   string         `json:"product_name"`
	Channel       string         `json:"channel"`
	Platform      string         `json:"platform"`
	Architecture  string         `json:"architecture"`
	PackageType   string         `json:"package_type"`
	Release       channelRelease `json:"release"`
}

type softwareUpdateManifest struct {
	ProductID          string `json:"productId"`
	Version            string `json:"version"`
	ReleaseName        string `json:"releaseName"`
	ReleaseNotes       string `json:"releaseNotes"`
	Channel            string `json:"channel"`
	ArtifactURL        string `json:"artifactUrl"`
	FileName           string `json:"fileName"`
	PackageType        string `json:"packageType"`
	SHA256             string `json:"sha256"`
	Size               int64  `json:"size"`
	Mandatory          bool   `json:"mandatory"`
	PublishedAt        string `json:"publishedAt"`
	Signature          string `json:"signature"`
	SignatureKeyID     string `json:"signatureKeyId"`
	SignatureAlgorithm string `json:"signatureAlgorithm"`
}

type resolveResponse struct {
	Update *softwareUpdateManifest `json:"update"`
}

type dotnetProject struct {
	PropertyGroups []struct {
		UseWPF          string `xml:"UseWPF"`
		TargetFramework string `xml:"TargetFramework"`
		OutputType      string `xml:"OutputType"`
	} `xml:"PropertyGroup"`
	ItemGroups []struct {
		PackageReferences []struct {
			Include string `xml:"Include,attr"`
			Update  string `xml:"Update,attr"`
		} `xml:"PackageReference"`
	} `xml:"ItemGroup"`
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
	case "software.distribution.channel.resolve":
		result, err := resolveChannel(in)
		return rpcResult(result, err)
	default:
		return nil, &jsonrpc.Error{Code: -32602, Message: "不支持的软件分发能力"}
	}
}

// resolveChannel 读取 GitHub 渠道描述文件并给出与 Dashboard 渠道同形的更新结果，
// 使调用方无需区分分发源。凭据不参与：公共仓直接读取，私有仓由 Agent 侧代理。
func resolveChannel(in input) (any, error) {
	documentURL, err := channelDocumentURL(in)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequest(http.MethodGet, documentURL, nil)
	if err != nil {
		return nil, errors.New("GitHub channel 文档地址无效")
	}
	request.Header.Set("User-Agent", "HiMind-Agent")
	request.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 20 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("GitHub channel 文档读取失败")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub channel 文档返回 HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, errors.New("GitHub channel 文档读取中断")
	}
	return evaluateChannelDocument(in, body, documentURL)
}

func channelDocumentURL(in input) (string, error) {
	if trimmed := strings.TrimSpace(in.ChannelURL); trimmed != "" {
		parsed, err := url.Parse(trimmed)
		if err != nil || parsed.Scheme != "https" ||
			(parsed.Host != "raw.githubusercontent.com" && parsed.Host != "github.com") {
			return "", errors.New("channel_url 必须是受信任的 GitHub HTTPS 地址")
		}
		return trimmed, nil
	}
	if !repositoryPattern.MatchString(strings.TrimSpace(in.Repository)) {
		return "", errors.New("repository 必须是 owner/repo")
	}
	// 渠道地址只取决于产品与渠道，不要求平台、架构与包类型。
	if !identifierPattern.MatchString(strings.TrimSpace(in.ProductID)) {
		return "", errors.New("product_id 不是有效的稳定标识")
	}
	reference := defaultValue(strings.TrimSpace(in.Reference), "HEAD")
	channel := defaultValue(strings.TrimSpace(in.Channel), "stable")
	if !identifierPattern.MatchString(strings.TrimSpace(channel)) {
		return "", errors.New("channel 不是有效的稳定标识")
	}
	return fmt.Sprintf(
		"https://raw.githubusercontent.com/%s/%s/distribution/%s/%s.json",
		strings.TrimSpace(in.Repository), reference, in.ProductID, channel,
	), nil
}

// evaluateChannelDocument 是纯函数：只做一致性与信任校验，便于契约测试。
func evaluateChannelDocument(in input, body []byte, documentURL string) (any, error) {
	var channel softwareDistributionChannel
	if err := json.Unmarshal(body, &channel); err != nil {
		return nil, errors.New("channel 文档不是合法 JSON")
	}
	if channel.SchemaVersion != "software_distribution_channel.v1" {
		return nil, fmt.Errorf("不支持的 channel schema_version: %s", channel.SchemaVersion)
	}
	if channel.ProductID != in.ProductID {
		return nil, errors.New("channel 文档 product_id 与请求不一致")
	}
	if in.Channel != "" && channel.Channel != in.Channel {
		return nil, errors.New("channel 文档 channel 与请求不一致")
	}
	if in.Platform != "" && channel.Platform != in.Platform {
		return nil, errors.New("channel 文档 platform 与请求不一致")
	}
	if in.Architecture != "" && channel.Architecture != in.Architecture {
		return nil, errors.New("channel 文档 architecture 与请求不一致")
	}
	if in.PackageType != "" && channel.PackageType != "" && channel.PackageType != in.PackageType {
		return nil, errors.New("channel 文档 package_type 与请求不一致")
	}
	release := channel.Release
	if !sha256Pattern.MatchString(release.SHA256) {
		return nil, errors.New("channel 文档 sha256 无效")
	}
	if release.SizeBytes <= 0 {
		return nil, errors.New("channel 文档 size_bytes 无效")
	}
	if !strings.HasPrefix(release.DownloadURL, "https://github.com/") {
		return nil, errors.New("channel 文档 download_url 必须指向 github.com")
	}
	signatureRequired := !in.AllowUnsigned
	if release.Signature == nil || strings.TrimSpace(release.Signature.Value) == "" || strings.TrimSpace(release.Signature.KeyID) == "" {
		if signatureRequired {
			return nil, errors.New("channel 文档缺少有效签名，已按 fail-closed 拒绝")
		}
	}

	current := defaultValue(strings.TrimSpace(in.CurrentVersion), "0.0.0")
	var update *softwareUpdateManifest
	reason := ""
	switch comparison := compareVersions(release.Version, current); {
	case release.Revoked:
		reason = "该版本已被撤销"
	case comparison > 0:
		manifest := softwareUpdateManifest{
			ProductID:          channel.ProductID,
			Version:            release.Version,
			ReleaseName:        strings.TrimSpace(channel.ProductName + " " + release.Version),
			ReleaseNotes:       release.ReleaseNotes,
			Channel:            channel.Channel,
			ArtifactURL:        release.DownloadURL,
			FileName:           release.FileName,
			PackageType:        channel.PackageType,
			SHA256:             strings.ToLower(release.SHA256),
			Size:               release.SizeBytes,
			Mandatory:          release.Mandatory,
			PublishedAt:        release.PublishedAt,
			SignatureAlgorithm: "ed25519",
		}
		if release.Signature != nil {
			manifest.Signature = release.Signature.Value
			manifest.SignatureKeyID = release.Signature.KeyID
			if strings.TrimSpace(release.Signature.Algorithm) != "" {
				manifest.SignatureAlgorithm = release.Signature.Algorithm
			}
		}
		update = &manifest
	case comparison == 0:
		reason = "已是最新版本"
	default:
		if in.AllowDowngrade {
			update = &softwareUpdateManifest{
				ProductID:    channel.ProductID,
				Version:      release.Version,
				ReleaseName:  strings.TrimSpace(channel.ProductName + " " + release.Version),
				ReleaseNotes: release.ReleaseNotes,
				Channel:      channel.Channel,
				ArtifactURL:  release.DownloadURL,
				FileName:     release.FileName,
				PackageType:  channel.PackageType,
				SHA256:       strings.ToLower(release.SHA256),
				Size:         release.SizeBytes,
				Mandatory:    release.Mandatory,
				PublishedAt:  release.PublishedAt,
			}
		} else {
			reason = "线上版本低于当前版本，默认拒绝降级"
		}
	}

	result := map[string]any{
		"source":             "github-channel",
		"channel_file":       documentURL,
		"product_id":         channel.ProductID,
		"channel":            channel.Channel,
		"current_version":    current,
		"signature_required": signatureRequired,
		// 显式区分"没有更新"与"有更新"：typed nil 会让消费者把空指针当成有更新。
		"update": nil,
	}
	if update != nil {
		result["update"] = *update
	}
	if reason != "" {
		result["reason"] = reason
	}
	// 灰度：只有声明了稳定实例 ID 时才参与分级，避免用 IP 或随机数代替设备身份。
	if update != nil && release.RolloutPercent > 0 && release.RolloutPercent < 100 {
		if strings.TrimSpace(in.InstanceID) == "" {
			result["update"] = nil
			result["reason"] = "该版本为灰度发布，缺少稳定实例 ID，已按未放量处理"
		} else if !instanceInRollout(in.InstanceID, release.Version, release.RolloutPercent) {
			result["update"] = nil
			result["reason"] = fmt.Sprintf("该版本正在灰度（%d%%），本实例暂未放量", release.RolloutPercent)
		}
	}
	return result, nil
}

// instanceInRollout 用稳定实例 ID 与版本号的哈希取模决定是否放量：
// 同一实例对同一版本的结果稳定，不依赖 IP、时间或随机数。
func instanceInRollout(instanceID, version string, percent int) bool {
	if percent >= 100 {
		return true
	}
	if percent <= 0 {
		return false
	}
	digest := sha256.Sum256([]byte(strings.TrimSpace(instanceID) + "|" + strings.TrimSpace(version)))
	bucket := int(binary.BigEndian.Uint32(digest[:4]) % 100)
	return bucket < percent
}

// compareVersions 只比较语义化版本的核心数字段；预发布后缀视为低于同核心版本。
func compareVersions(left, right string) int {
	core := func(value string) ([]int, bool) {
		trimmed := strings.TrimSpace(value)
		prerelease := strings.ContainsAny(trimmed, "-+")
		base := strings.FieldsFunc(strings.SplitN(strings.SplitN(trimmed, "-", 2)[0], "+", 2)[0], func(r rune) bool { return r == '.' })
		numbers := make([]int, 0, len(base))
		for _, part := range base {
			number := 0
			for _, digit := range part {
				if digit < '0' || digit > '9' {
					return nil, prerelease
				}
				number = number*10 + int(digit-'0')
			}
			numbers = append(numbers, number)
		}
		return numbers, prerelease
	}
	leftParts, leftPre := core(left)
	rightParts, rightPre := core(right)
	for index := 0; index < len(leftParts) || index < len(rightParts); index++ {
		leftValue, rightValue := 0, 0
		if index < len(leftParts) {
			leftValue = leftParts[index]
		}
		if index < len(rightParts) {
			rightValue = rightParts[index]
		}
		if leftValue != rightValue {
			if leftValue > rightValue {
				return 1
			}
			return -1
		}
	}
	if leftPre == rightPre {
		return 0
	}
	if leftPre {
		return -1
	}
	return 1
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
	clientStatus := map[string]any{
		"protocol_package_installed": false,
		"windows_adapter_installed":  false,
		"configuration_detected":     false,
		"updater_detected":           false,
	}
	if matches, _ := filepath.Glob(filepath.Join(target, "*.csproj")); len(matches) > 0 {
		project, inspectErr := readDotnetProject(matches[0])
		if inspectErr != nil {
			return nil, inspectErr
		}
		projectType = "dotnet"
		if project.useWPF {
			projectType = "wpf"
		}
		recommendation = map[string]any{
			"protocol_package": "HiMind.Distribution", "windows_adapter": "HiMind.Distribution.Windows",
			"package_type": "directory-zip", "platform": "windows", "architecture": "x64",
		}
		clientStatus["protocol_package_installed"] = project.protocolPackage
		clientStatus["windows_adapter_installed"] = project.windowsAdapter
		clientStatus["configuration_detected"] = hasDistributionConfiguration(target)
		clientStatus["updater_detected"] = isRegularFile(filepath.Join(target, "updater", "HiMind.Distribution.Updater.exe"))
	} else if isDirectory(filepath.Join(target, "Assets")) && isDirectory(filepath.Join(target, "ProjectSettings")) {
		projectType = "unity"
		recommendation = map[string]any{
			"windows_package_type": "directory-zip", "android_package_type": "apk",
			"runtime_note": "Unity 仅复用分发协议；Windows 与 Android 安装分别由平台适配器完成。",
		}
		clientStatus["configuration_detected"] = hasDistributionConfiguration(target)
		clientStatus["updater_detected"] = isRegularFile(filepath.Join(target, "updater", "HiMind.Distribution.Updater.exe"))
	}
	return map[string]any{
		"workspace_root": root, "project_path": target, "project_type": projectType,
		"product_id": strings.TrimSpace(in.ProductID), "recommendation": recommendation,
		"client_status":      clientStatus,
		"runtime_dependency": "应用运行时直接调用分发服务；Agent 仅用于 AI 接入检查和受控发布", "agent_runtime_required": false,
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
	if info.Size() <= 0 {
		return nil, errors.New("制品文件不能为空")
	}
	if err := validateArtifactStructure(in, target); err != nil {
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
	if result.Update != nil {
		if err := validateResolveManifest(in, result.Update); err != nil {
			return nil, err
		}
	}
	return map[string]any{"dashboard": base, "product_id": in.ProductID, "update": result.Update}, nil
}

type dotnetProjectStatus struct {
	useWPF          bool
	protocolPackage bool
	windowsAdapter  bool
}

func readDotnetProject(projectPath string) (dotnetProjectStatus, error) {
	content, err := os.ReadFile(projectPath)
	if err != nil {
		return dotnetProjectStatus{}, errors.New("无法读取 .NET 项目文件")
	}
	var project dotnetProject
	if err := xml.Unmarshal(content, &project); err != nil {
		return dotnetProjectStatus{}, errors.New(".NET 项目文件不是有效的 XML")
	}
	status := dotnetProjectStatus{}
	for _, group := range project.PropertyGroups {
		if strings.EqualFold(strings.TrimSpace(group.UseWPF), "true") {
			status.useWPF = true
		}
	}
	for _, group := range project.ItemGroups {
		for _, reference := range group.PackageReferences {
			name := strings.TrimSpace(reference.Include)
			if name == "" {
				name = strings.TrimSpace(reference.Update)
			}
			if strings.EqualFold(name, "HiMind.Distribution") {
				status.protocolPackage = true
			}
			if strings.EqualFold(name, "HiMind.Distribution.Windows") {
				status.windowsAdapter = true
			}
		}
	}
	return status, nil
}

func hasDistributionConfiguration(projectRoot string) bool {
	for _, name := range []string{"distribution.json", "distribution.sample.json", "appsettings.json", "appsettings.Development.json"} {
		filePath := filepath.Join(projectRoot, name)
		content, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}
		if strings.Contains(strings.ToLower(string(content)), "software-distribution") ||
			strings.Contains(strings.ToLower(string(content)), "productid") {
			return true
		}
	}
	return false
}

func validateArtifactStructure(in input, target string) error {
	switch in.PackageType {
	case "directory-zip", "unity-addressables":
		entries, err := validatedZIPEntries(target)
		if err != nil {
			return err
		}
		if strings.EqualFold(strings.TrimSpace(in.ProductID), "com.himind.media-resolver") {
			for _, required := range []string{
				"mediaresolver.exe",
				"distribution.sample.json",
				"updater/himind.distribution.updater.exe",
			} {
				if _, ok := entries[required]; !ok {
					return fmt.Errorf("MediaResolver 发布包缺少必要文件: %s", required)
				}
			}
		}
	case "apk":
		entries, err := validatedZIPEntries(target)
		if err != nil {
			return fmt.Errorf("APK 文件结构无效: %w", err)
		}
		if _, ok := entries["androidmanifest.xml"]; !ok {
			return errors.New("APK 缺少 AndroidManifest.xml")
		}
	}
	return nil
}

func validatedZIPEntries(target string) (map[string]struct{}, error) {
	archive, err := zip.OpenReader(target)
	if err != nil {
		return nil, errors.New("ZIP 制品结构无效")
	}
	defer archive.Close()
	if len(archive.File) == 0 {
		return nil, errors.New("ZIP 制品不能为空")
	}
	if len(archive.File) > 100000 {
		return nil, errors.New("ZIP 制品文件数量超过限制")
	}
	entries := make(map[string]struct{}, len(archive.File))
	for _, entry := range archive.File {
		name := strings.ReplaceAll(entry.Name, "\\", "/")
		cleaned := path.Clean(name)
		if cleaned == "." || strings.HasPrefix(cleaned, "../") || path.IsAbs(cleaned) || strings.Contains(cleaned, ":") {
			return nil, errors.New("ZIP 制品包含不安全路径")
		}
		if entry.FileInfo().IsDir() {
			continue
		}
		key := strings.ToLower(strings.TrimPrefix(cleaned, "./"))
		if _, duplicate := entries[key]; duplicate {
			return nil, fmt.Errorf("ZIP 制品包含重复路径: %s", cleaned)
		}
		entries[key] = struct{}{}
	}
	if len(entries) == 0 {
		return nil, errors.New("ZIP 制品不包含文件")
	}
	return entries, nil
}

func validateResolveManifest(in input, manifest *softwareUpdateManifest) error {
	if manifest.ProductID != strings.TrimSpace(in.ProductID) {
		return errors.New("Dashboard resolve 返回了不匹配的 productId")
	}
	if strings.TrimSpace(manifest.Version) == "" || strings.TrimSpace(manifest.ReleaseName) == "" {
		return errors.New("Dashboard resolve Manifest 缺少版本或发布名称")
	}
	if manifest.Channel != defaultValue(in.Channel, "stable") {
		return errors.New("Dashboard resolve 返回了不匹配的渠道")
	}
	if filepath.Base(manifest.FileName) != manifest.FileName || strings.TrimSpace(manifest.FileName) == "" {
		return errors.New("Dashboard resolve Manifest 文件名无效")
	}
	if !matchesPackageType(manifest.PackageType) {
		return errors.New("Dashboard resolve Manifest 包类型无效")
	}
	if manifest.Size <= 0 || len(manifest.SHA256) != 64 {
		return errors.New("Dashboard resolve Manifest 大小或 SHA-256 无效")
	}
	if _, err := hex.DecodeString(manifest.SHA256); err != nil {
		return errors.New("Dashboard resolve Manifest SHA-256 无效")
	}
	artifactURL, err := url.Parse(manifest.ArtifactURL)
	if err != nil || artifactURL.Host == "" || (artifactURL.Scheme != "https" && artifactURL.Scheme != "http") {
		return errors.New("Dashboard resolve Manifest 下载地址无效")
	}
	if strings.TrimSpace(manifest.Signature) == "" || strings.TrimSpace(manifest.SignatureKeyID) == "" || manifest.SignatureAlgorithm != "rsa-pss-sha256" {
		return errors.New("Dashboard resolve Manifest 缺少可信 RSA-PSS 签名")
	}
	return nil
}

func workspacePath(workspaceRoot, target string) (string, string, error) {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	target = strings.TrimSpace(target)
	if workspaceRoot == "" || target == "" {
		return "", "", errors.New("workspace_root 和目标路径不能为空")
	}
	lexicalRoot, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", "", err
	}
	if !isDirectory(lexicalRoot) {
		return "", "", errors.New("workspace_root 必须是可访问的本机目录")
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(lexicalRoot, target)
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return "", "", err
	}
	// Reject lexical escapes before resolving the target. This keeps the
	// diagnostic deterministic even when an out-of-root path does not exist.
	relative, err := filepath.Rel(lexicalRoot, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || filepath.IsAbs(relative) {
		return "", "", errors.New("目标路径必须位于显式指定的 workspace_root 内")
	}
	root, err := filepath.EvalSymlinks(lexicalRoot)
	if err != nil {
		return "", "", errors.New("workspace_root 目录不存在或不可访问")
	}
	target, err = filepath.EvalSymlinks(target)
	if err != nil {
		return "", "", errors.New("目标路径不存在或不可访问")
	}
	relative, err = filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || filepath.IsAbs(relative) {
		return "", "", errors.New("目标路径必须位于显式指定的 workspace_root 内")
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

func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func matchesPackageType(value string) bool {
	switch strings.TrimSpace(value) {
	case "directory-zip", "apk", "unity-addressables", "content":
		return true
	default:
		return false
	}
}

func defaultValue(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}
