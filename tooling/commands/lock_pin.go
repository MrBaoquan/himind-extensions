// himind-lock-pin 把工作流扩展锁里的依赖摘要改写成「制品载荷摘要」。
//
// 背景：锁里的依赖 sha256 描述的是「依赖的哪一份内容」，Agent 在安装后按载荷
// 口径重算（himind-agent/src/workflow/store.rs::package_payload_digest）。而锁是
// 在作者机器上用本地扩展源生成的——那份目录里还留着源码、历史制品和安装期状态，
// 算出来的值与从发布制品安装后算出的值不同。结果是：工作流在作者机器上能用，
// 从工作台/组织分发的制品装到别人机器上就报 “content changed”。
//
// 这里在打包之后、发布之前把每条依赖摘要换成依赖**制品字节**的载荷摘要，于是
// 锁里记的与任何一台机器装完算出来的必然是同一个值。版本与来源也从发布计划取，
// 与发布清单里的依赖 pin 出自同一份事实。
package commands

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MrBaoquan/himind-extensions/tooling/packagedigest"
	"github.com/MrBaoquan/himind-extensions/tooling/releaseplan"
)

// LockPinMain 是 himind-lock-pin 的命令行入口。
//
// 依赖定位参数与 himind-release-plan 完全一致：调用方把同一组参数原样传过来，
// 这里重新算一遍计划，避免「清单里的 pin」和「锁里的 pin」成为两份事实。
func LockPinMain() {
	flags := flag.NewFlagSet("himind-lock-pin", flag.ExitOnError)
	lockPath := flags.String("lock", "", "工作流扩展锁文件")
	kind := flags.String("kind", "workflow", "扩展类型；只有 workflow 有扩展锁")
	source := flags.String("path", "", "扩展源码目录")
	repository := flags.String("repository", "", "GitHub owner/repo，用于拼依赖下载地址")
	channel := flags.String("channel", "stable", "发布通道")
	catalogPath := flags.String("catalog", ".himind/catalog.json", "市场索引")
	dependencyCatalogs := stringListFlag{}
	flags.Var(&dependencyCatalogs, "dependency-catalog", "依赖所在仓的市场索引；可重复指定")
	artifactDirs := stringListFlag{}
	flags.Var(&artifactDirs, "artifact-dir", "本地依赖制品目录；内容与发布清单摘要一致时优先于下载；可重复指定")
	dryRun := flags.Bool("dry-run", false, "只打印将要写入的摘要，不改锁文件")
	_ = flags.Parse(os.Args[1:])

	if err := pinLock(*lockPath, *kind, *source, *repository, *channel, *catalogPath,
		dependencyCatalogs, artifactDirs, *dryRun); err != nil {
		fmt.Fprintln(os.Stderr, "extension lock pin failed:", err)
		os.Exit(1)
	}
}

// lockDocument 是 extension_lock.v1 的可改写投影。
//
// environment 原样保留：它是作者机器上实测到的运行环境事实，本次任务只修正
// 依赖内容摘要，不去重算其它字段。schema_version 与 generated_at 同理。
type lockDocument struct {
	SchemaVersion string           `json:"schema_version"`
	Root          lockRoot         `json:"root"`
	Dependencies  []lockDependency `json:"dependencies"`
	Environment   json.RawMessage  `json:"environment,omitempty"`
	GeneratedAt   string           `json:"generated_at"`
}

type lockRoot struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

type lockDependency struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Version  string `json:"version"`
	SHA256   string `json:"sha256"`
	SourceID string `json:"source_id,omitempty"`
	Required bool   `json:"required"`
}

func pinLock(lockPath, kind, source, repository, channel, catalogPath string,
	dependencyCatalogs []string, artifactDirs []string, dryRun bool) error {
	if strings.TrimSpace(lockPath) == "" {
		return fmt.Errorf("-lock 必填")
	}
	if strings.TrimSpace(source) == "" {
		return fmt.Errorf("-path 必填")
	}
	plan, err := releaseplan.Build(kind, source, repository, channel, catalogPath, dependencyCatalogs...)
	if err != nil {
		return err
	}
	document, err := readLock(lockPath)
	if err != nil {
		return err
	}
	if document.Root.Kind != plan.Kind || document.Root.ID != plan.ID || document.Root.Version != plan.Version {
		return fmt.Errorf("锁的身份 %s@%s(%s) 与本次发布 %s@%s(%s) 不一致",
			document.Root.ID, document.Root.Version, document.Root.Kind,
			plan.ID, plan.Version, plan.Kind)
	}

	pins := map[string]dependencyPin{}
	for _, dependency := range plan.Dependencies {
		pins[dependency.Kind+"/"+dependency.ID] = dependencyPin{
			Version: dependency.Version, Pinned: dependency.Pinned,
			ArtifactURL: dependency.Source.ArtifactURL, Repository: dependency.Source.Repository,
			SHA256: dependency.SHA256,
		}
	}

	changes := make([]dependencyChange, 0, len(document.Dependencies))
	for index := range document.Dependencies {
		dependency := &document.Dependencies[index]
		pin, ok := pins[dependency.Kind+"/"+dependency.ID]
		if !ok {
			// 锁里有、发布清单里没有：说明这不是本次发布解析到的依赖，保持原样并
			// 明确报出来，而不是悄悄改一个来源不明的摘要。
			fmt.Fprintf(os.Stderr, "warning: 依赖 %s 不在发布计划里，锁保持原样\n", dependency.ID)
			changes = append(changes, dependencyChange{
				ID: dependency.ID, Kind: dependency.Kind, Version: dependency.Version, Skipped: true,
			})
			continue
		}
		if !pin.Pinned || strings.TrimSpace(pin.Version) == "" {
			return fmt.Errorf("依赖 %s 没有确定制品，无法写入锁", dependency.ID)
		}
		if dependency.Version != pin.Version {
			return fmt.Errorf("锁里 %s 是 %s，本次发布 pin 到 %s：先让本地依赖源升到该版本再发布",
				dependency.ID, dependency.Version, pin.Version)
		}
		if strings.TrimSpace(pin.ArtifactURL) == "" {
			return fmt.Errorf("依赖 %s 的制品地址缺失，无法计算载荷摘要", dependency.ID)
		}
		digest, err := dependencyDigest(pin, artifactDirs)
		if err != nil {
			return err
		}
		change := dependencyChange{
			Kind: dependency.Kind, ID: dependency.ID, Version: dependency.Version,
			Previous: dependency.SHA256, SHA256: digest, SourceID: pin.Repository,
			Changed: !strings.EqualFold(dependency.SHA256, digest) || dependency.SourceID != pin.Repository,
		}
		dependency.SHA256 = digest
		dependency.SourceID = pin.Repository
		changes = append(changes, change)
	}

	if !dryRun {
		data, err := json.MarshalIndent(document, "", "  ")
		if err != nil {
			return err
		}
		data = append(data, '\n')
		if err := os.WriteFile(lockPath, data, 0o644); err != nil {
			return err
		}
	}
	return printJSON(struct {
		Lock         string             `json:"lock"`
		Root         lockRoot           `json:"root"`
		DryRun       bool               `json:"dry_run"`
		Dependencies []dependencyChange `json:"dependencies"`
	}{Lock: lockPath, Root: document.Root, DryRun: dryRun, Dependencies: changes})
}

// dependencyPin 是发布计划里一条依赖的定位事实。
type dependencyPin struct {
	Version     string
	Pinned      bool
	ArtifactURL string
	Repository  string
	// SHA256 是发布清单里记的依赖**制品文件**摘要。它用来确认本地缓存的制品
	// 就是清单指向的那一份；对不上就回去下载，避免用陈旧构建算出的锁发出去。
	SHA256 string
}

type dependencyChange struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Version  string `json:"version"`
	SHA256   string `json:"sha256,omitempty"`
	Previous string `json:"previous_sha256,omitempty"`
	SourceID string `json:"source_id,omitempty"`
	Changed  bool   `json:"changed"`
	Skipped  bool   `json:"skipped,omitempty"`
}

func readLock(path string) (lockDocument, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return lockDocument{}, err
	}
	var document lockDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return lockDocument{}, fmt.Errorf("%s: %w", path, err)
	}
	return document, nil
}

// dependencyDigest 取依赖制品的载荷摘要。优先用本地已下载的制品，避免发布时
// 重复下载；本地没有才按发布计划的地址取。
//
// 本地命中必须与发布清单记的制品摘要一致才算命中：作者机器上的 dist 目录里可能
// 躺着同一个版本号的旧构建，用它算出的锁会和真正发出去的那份制品对不上。
func dependencyDigest(pin dependencyPin, artifactDirs []string) (string, error) {
	name := fileNameOfURL(pin.ArtifactURL)
	if name == "" {
		return "", fmt.Errorf("依赖制品地址无法解析: %s", pin.ArtifactURL)
	}
	if local, ok := findLocalArtifact(name, pin.SHA256, artifactDirs); ok {
		return packagedigest.PayloadDigestOfArchive(local)
	}
	artifact, err := os.CreateTemp("", "himind-dependency-*"+filepath.Ext(name))
	if err != nil {
		return "", err
	}
	local := artifact.Name()
	defer os.Remove(local)
	source, err := download(pin.ArtifactURL)
	if err != nil {
		_ = artifact.Close()
		return "", err
	}
	_, copyErr := io.Copy(artifact, source)
	_ = source.Close()
	if closeErr := artifact.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return "", fmt.Errorf("%s: %w", pin.ArtifactURL, copyErr)
	}
	return packagedigest.PayloadDigestOfArchive(local)
}

// findLocalArtifact 在候选目录里找发布清单指向的那一份制品。
//
// 只按文件名找是不够的：同一个版本号在作者机器上可能被反复构建过。因此命中后
// 还要比对发布清单记的制品摘要，对不上就当作没找到，让调用方回到下载路径。
func findLocalArtifact(name, expectedSHA256 string, artifactDirs []string) (string, bool) {
	expected := strings.TrimSpace(expectedSHA256)
	for _, directory := range artifactDirs {
		if strings.TrimSpace(directory) == "" {
			continue
		}
		candidate := filepath.Join(directory, name)
		if info, err := os.Stat(candidate); err != nil || info.IsDir() {
			continue
		}
		if expected == "" {
			return candidate, true
		}
		actual, err := fileDigest(candidate)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: 读取本地依赖制品 %s 失败，改用下载：%v\n", candidate, err)
			continue
		}
		if strings.EqualFold(actual, expected) {
			return candidate, true
		}
		// 版本号一样、字节不一样：本地这份不是发布清单指向的制品，报出来而不是
		// 悄悄换一份来源不明的构建。
		fmt.Fprintf(os.Stderr, "warning: 本地依赖制品 %s 与发布清单摘要不一致，改用下载\n", candidate)
	}
	return "", false
}

func fileDigest(path string) (string, error) {
	handle, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer handle.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, handle); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func download(address string) (io.ReadCloser, error) {
	// 发布资产走 GitHub Release，偶发的连接超时不该让整次发布失败；重试只对
	// 网络层错误有意义，HTTP 状态码已经拿到就说明服务端给了确定答复。
	var lastErr error
	for attempt := 1; attempt <= downloadAttempts; attempt++ {
		body, err := downloadOnce(address)
		if err == nil {
			return body, nil
		}
		var statusError downloadStatusError
		if errors.As(err, &statusError) {
			// 服务端已经明确答复，重试不会有别的结果。
			return nil, err
		}
		lastErr = err
		if attempt < downloadAttempts {
			time.Sleep(time.Duration(attempt) * 3 * time.Second)
		}
	}
	return nil, lastErr
}

const downloadAttempts = 3

// downloadStatusError 表示服务端返回了非 200 状态，属于确定性失败。
type downloadStatusError struct {
	Address string
	Status  string
}

func (err downloadStatusError) Error() string {
	return fmt.Sprintf("下载依赖制品失败 %s: %s", err.Address, err.Status)
}

func downloadOnce(address string) (io.ReadCloser, error) {
	request, err := http.NewRequest(http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "himind-release-pipeline")
	// 私有分发仓的 Release 资产需要凭据；公开仓带上也无害（跨域重定向时
	// net/http 不会把 Authorization 转发给第三方主机）。
	if token := strings.TrimSpace(os.Getenv("GH_TOKEN") + os.Getenv("GITHUB_TOKEN")); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 10 * time.Minute}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		return nil, downloadStatusError{Address: address, Status: response.Status}
	}
	return response.Body, nil
}

func fileNameOfURL(address string) string {
	parsed, err := url.Parse(address)
	if err != nil {
		return ""
	}
	name := filepath.Base(parsed.Path)
	if name == "." || name == "/" || name == "\\" {
		return ""
	}
	return name
}
