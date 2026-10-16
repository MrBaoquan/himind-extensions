// Package runtime 获取并校验插件依赖的外部运行时（whisper 二进制与模型）。
//
// 为什么不把二进制与模型打进插件包：二进制约 21MB、模型 75MB–1.5GB，打进包会让每次安装都
// 拖着几百 MB；且它们是**独立许可**的第三方产物，随包分发会让许可关系变复杂。
// 所以按 runtime-lock.json 锁定的版本与摘要，在首次使用时下载并校验。
//
// 镜像是一等公民：实测 huggingface.co 与 github.com 在部分网络下不可达，而
// hf-mirror.com / ghfast.top 可达。只写一个源会让插件在那些网络里直接不可用。
package runtime

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"himind-plugin/media-ai/internal/media/plugindata"
)

// Lock 运行时的锁定信息。
type Lock struct {
	SchemaVersion int    `json:"schema_version"`
	Runtime       string `json:"runtime"`
	Release       string `json:"release"`
	Note          string `json:"note"`
	Archive       struct {
		Asset   string   `json:"asset"`
		Size    int64    `json:"size"`
		SHA256  string   `json:"sha256"`
		URLs    []string `json:"urls"`
		Entry   string   `json:"entry"`
		License struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		} `json:"license"`
	} `json:"archive"`
	Models            []Model  `json:"models"`
	ModelURLTemplate  string   `json:"model_url_template"`
	ModelURLsFallback []string `json:"model_urls_fallback"`
	LicenseNote       string   `json:"license_note"`
}

// Model 一个可获取的模型。
type Model struct {
	ID          string `json:"id"`
	File        string `json:"file"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	Multilingual bool  `json:"multilingual"`
	Note        string `json:"note"`
}

// ReadLock 读取锁定文件（从插件包内嵌的副本，见 lock.go）。
func ReadLock(payload []byte) (Lock, error) {
	var lock Lock
	if err := json.Unmarshal(payload, &lock); err != nil {
		return lock, fmt.Errorf("解析 runtime-lock.json 失败: %w", err)
	}
	if lock.Archive.Asset == "" || lock.Archive.SHA256 == "" || len(lock.Archive.URLs) == 0 {
		return lock, errors.New("runtime-lock.json 的二进制信息不完整")
	}
	if len(lock.Models) == 0 {
		return lock, errors.New("runtime-lock.json 没有可用的模型")
	}
	return lock, nil
}

// FindModel 按 id 找模型。
func (l Lock) FindModel(id string) (Model, bool) {
	wanted := strings.ToLower(strings.TrimSpace(id))
	if wanted == "" {
		wanted = "small"
	}
	for _, model := range l.Models {
		if strings.EqualFold(model.ID, wanted) {
			return model, true
		}
	}
	return Model{}, false
}

// ModelURLs 返回该模型的候选下载地址（模板 + 回退源）。
func (l Lock) ModelURLs(model Model) []string {
	urls := make([]string, 0, 4)
	if l.ModelURLTemplate != "" {
		urls = append(urls, strings.ReplaceAll(l.ModelURLTemplate, "{file}", model.File))
	}
	for _, template := range l.ModelURLsFallback {
		urls = append(urls, strings.ReplaceAll(template, "{file}", model.File))
	}
	return urls
}

// Paths 运行时的落点。
type Paths struct {
	Root      string
	BinaryDir string
	ModelsDir string
}

// ResolvePaths 计算运行时的落点。
func ResolvePaths() (Paths, error) {
	root, err := plugindata.Sub("runtime")
	if err != nil {
		return Paths{}, err
	}
	return Paths{
		Root:      root,
		BinaryDir: filepath.Join(root, "whisper"),
		ModelsDir: filepath.Join(root, "models"),
	}, nil
}

// Status 当前安装状态。
type Status struct {
	BinaryInstalled bool     `json:"binary_installed"`
	BinaryPath      string   `json:"binary_path,omitempty"`
	InstalledModels []string `json:"installed_models"`
	RuntimeRoot     string   `json:"runtime_root"`
}

// Inspect 检查已安装了什么。只做存在性判断，不校验摘要
// （摘要校验只在下载时做一次，每次调用都重算会让状态查询变成几百 MB 的 IO）。
func Inspect(lock Lock, paths Paths) Status {
	status := Status{RuntimeRoot: paths.Root, InstalledModels: []string{}}
	entry := filepath.Join(paths.BinaryDir, filepath.FromSlash(lock.Archive.Entry))
	if info, err := os.Stat(entry); err == nil && !info.IsDir() {
		status.BinaryInstalled = true
		status.BinaryPath = entry
	}
	for _, model := range lock.Models {
		target := filepath.Join(paths.ModelsDir, model.File)
		if info, err := os.Stat(target); err == nil && info.Size() == model.Size {
			status.InstalledModels = append(status.InstalledModels, model.ID)
		}
	}
	return status
}

// InstallOptions 一次安装请求。
type InstallOptions struct {
	// Models 要安装的模型 id 列表；为空表示只装二进制。
	Models []string
	// Progress 下载进度回调（可为空）。
	Progress func(stage string, received, total int64)
}

// InstallResult 安装结果。
type InstallResult struct {
	BinaryInstalled bool     `json:"binary_installed"`
	BinaryPath      string   `json:"binary_path,omitempty"`
	InstalledModels []string `json:"installed_models"`
	Skipped         []string `json:"skipped"`
}

// Install 下载并安装二进制与（可选）模型。已存在的会跳过，不重复下载。
func Install(ctx context.Context, lock Lock, paths Paths, options InstallOptions) (InstallResult, error) {
	result := InstallResult{InstalledModels: []string{}}

	binaryPath := filepath.Join(paths.BinaryDir, filepath.FromSlash(lock.Archive.Entry))
	if info, err := os.Stat(binaryPath); err == nil && !info.IsDir() {
		result.BinaryInstalled = true
		result.BinaryPath = binaryPath
		result.Skipped = append(result.Skipped, "whisper 二进制已存在")
	} else {
		if err := installBinary(ctx, lock, paths, options.Progress); err != nil {
			return result, err
		}
		result.BinaryInstalled = true
		result.BinaryPath = binaryPath
	}

	for _, id := range options.Models {
		model, ok := lock.FindModel(id)
		if !ok {
			return result, fmt.Errorf("未知模型: %s", id)
		}
		target := filepath.Join(paths.ModelsDir, model.File)
		if info, err := os.Stat(target); err == nil && info.Size() == model.Size {
			result.InstalledModels = append(result.InstalledModels, model.ID)
			result.Skipped = append(result.Skipped, model.ID+" 模型已存在")
			continue
		}
		if err := downloadAndVerify(ctx, lock.ModelURLs(model), target, model.Size, model.SHA256,
			func(received, total int64) {
				if options.Progress != nil {
					options.Progress("model:"+model.ID, received, total)
				}
			}); err != nil {
			return result, fmt.Errorf("获取模型 %s 失败: %w", model.ID, err)
		}
		result.InstalledModels = append(result.InstalledModels, model.ID)
	}

	return result, nil
}

func installBinary(ctx context.Context, lock Lock, paths Paths, progress func(string, int64, int64)) error {
	archivePath := filepath.Join(paths.Root, lock.Archive.Asset)
	if err := downloadAndVerify(ctx, lock.Archive.URLs, archivePath, lock.Archive.Size, lock.Archive.SHA256,
		func(received, total int64) {
			if progress != nil {
				progress("binary", received, total)
			}
		}); err != nil {
		return fmt.Errorf("获取 whisper 二进制失败: %w", err)
	}
	defer os.Remove(archivePath)

	if err := os.MkdirAll(paths.BinaryDir, 0o755); err != nil {
		return fmt.Errorf("创建运行时目录失败: %w", err)
	}
	// zip 里是 Release/ 一层；解到 BinaryDir 后入口就是 BinaryDir/Release/whisper-cli.exe，
	// 与 lock 里的 entry 一致（不改动目录结构，避免 entry 与实际路径漂移）。
	if err := unzip(archivePath, paths.BinaryDir); err != nil {
		return fmt.Errorf("解压失败: %w", err)
	}
	entry := filepath.Join(paths.BinaryDir, filepath.FromSlash(lock.Archive.Entry))
	if _, err := os.Stat(entry); err != nil {
		return fmt.Errorf("解压后未找到入口 %s", lock.Archive.Entry)
	}
	return nil
}

// downloadAndVerify 依次尝试候选地址，边下边算摘要，落盘前校验大小与摘要。
func downloadAndVerify(
	ctx context.Context,
	urls []string,
	target string,
	expectedSize int64,
	expectedSHA256 string,
	progress func(int64, int64),
) error {
	if len(urls) == 0 {
		return errors.New("没有可用的下载地址")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}

	var lastErr error
	for _, url := range urls {
		err := downloadOnce(ctx, url, target, expectedSize, expectedSHA256, progress)
		if err == nil {
			return nil
		}
		lastErr = err
	}
	return fmt.Errorf("所有下载地址均失败（最后错误: %w）", lastErr)
}

func downloadOnce(
	ctx context.Context,
	url, target string,
	expectedSize int64,
	expectedSHA256 string,
	progress func(int64, int64),
) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 2 * time.Hour}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s 返回 %d", url, response.StatusCode)
	}
	total := response.ContentLength

	// 先写临时文件：下载或校验失败时不会在目标位置留下半成品
	// （那会被后续的"文件已存在"判断当成装好了）。
	temp := target + ".part"
	file, err := os.Create(temp)
	if err != nil {
		return err
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hasher), &progressReader{
		reader: response.Body,
		total:  total,
		report: progress,
	})
	closeErr := file.Close()
	if copyErr != nil {
		os.Remove(temp)
		return copyErr
	}
	if closeErr != nil {
		os.Remove(temp)
		return closeErr
	}

	actual := hex.EncodeToString(hasher.Sum(nil))
	if expectedSize > 0 && written != expectedSize {
		os.Remove(temp)
		return fmt.Errorf("大小不符：期望 %d，实际 %d", expectedSize, written)
	}
	if !strings.EqualFold(actual, expectedSHA256) {
		os.Remove(temp)
		return fmt.Errorf("摘要不符：期望 %s，实际 %s", expectedSHA256, actual)
	}
	return os.Rename(temp, target)
}

type progressReader struct {
	reader io.Reader
	total  int64
	seen   int64
	report func(int64, int64)
}

func (p *progressReader) Read(buffer []byte) (int, error) {
	count, err := p.reader.Read(buffer)
	if count > 0 {
		p.seen += int64(count)
		if p.report != nil {
			p.report(p.seen, p.total)
		}
	}
	return count, err
}

// unzip 解压到目标目录。刻意不做路径穿越防护之外的花样：
// 只接受相对路径，拒绝 .. 与绝对路径。
func unzip(archivePath, destination string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer reader.Close()

	for _, entry := range reader.File {
		name := filepath.FromSlash(entry.Name)
		if filepath.IsAbs(name) || strings.HasPrefix(name, "..") {
			return fmt.Errorf("压缩包内条目路径非法: %s", entry.Name)
		}
		target := filepath.Join(destination, name)
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		source, err := entry.Open()
		if err != nil {
			return err
		}
		destination, err := os.Create(target)
		if err != nil {
			source.Close()
			return err
		}
		_, copyErr := io.Copy(destination, source)
		source.Close()
		destination.Close()
		if copyErr != nil {
			return copyErr
		}
	}
	return nil
}
