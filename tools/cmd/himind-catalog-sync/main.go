// Command himind-catalog-sync 从 GitHub Release 全量重建市场索引。
//
// 索引是派生数据，所以「索引和 Release 不一致」这种问题不应该靠人比对，而是
// 直接重建一次。重建只认规范 tag（`<kind>/<id>@<version>`）：历史扁平 tag 不参与，
// 也不会被当成一次有效发布。
package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/MrBaoquan/himind-extensions/tooling/catalog"
	"github.com/MrBaoquan/himind-extensions/tooling/distribution"
)

type extensionEntry struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Path string `json:"path"`
}

type extensionCatalog struct {
	DistributionID string           `json:"distribution_id"`
	Channel        string           `json:"channel"`
	CatalogID      string           `json:"catalog_id"`
	Extensions     []extensionEntry `json:"extensions"`
}

type releaseAsset struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	URL  string `json:"url"`
}

type release struct {
	TagName     string         `json:"tag_name"`
	Draft       bool           `json:"draft"`
	PublishedAt string         `json:"published_at"`
	CreatedAt   string         `json:"created_at"`
	Assets      []releaseAsset `json:"assets"`
}

func main() {
	repository := flag.String("repository", "", "GitHub owner/repo")
	catalogPath := flag.String("catalog", ".himind/catalog.json", "索引文件")
	extensionsPath := flag.String("extensions", "extensions.json", "扩展清单")
	token := flag.String("token", "", "GitHub token，缺省读 GITHUB_TOKEN / GH_TOKEN / gh auth token")
	apiBase := flag.String("api-base", "https://api.github.com", "GitHub API 根地址")
	allowMissing := flag.Bool("allow-missing", false, "允许某些扩展还没有规范发布")
	flag.Parse()
	summary, err := sync(*repository, *catalogPath, *extensionsPath, *token, *apiBase, *allowMissing)
	if err != nil {
		fmt.Fprintln(os.Stderr, "catalog sync failed:", err)
		os.Exit(1)
	}
	fmt.Printf("catalog %s: %d plugins, %d skills, %d workflows (%s)\n",
		*catalogPath, summary.Plugins, summary.Skills, summary.Workflows, summary.Generation)
	if summary.SkippedTags > 0 {
		fmt.Printf("ignored %d non-canonical tags\n", summary.SkippedTags)
	}
}

type summary struct {
	Plugins     int
	Skills      int
	Workflows   int
	Generation  string
	SkippedTags int
}

func sync(repository, catalogPath, extensionsPath, token, apiBase string, allowMissing bool) (summary, error) {
	var config extensionCatalog
	if err := catalog.ReadJSON(extensionsPath, &config); err != nil {
		return summary{}, err
	}
	if strings.TrimSpace(repository) == "" {
		return summary{}, errors.New("-repository 必填")
	}
	client, err := newClient(apiBase, token)
	if err != nil {
		return summary{}, err
	}
	target := catalog.New(config.DistributionID, config.Channel, config.CatalogID)

	releases, err := client.listReleases(repository)
	if err != nil {
		return summary{}, err
	}
	byID := map[string][]extensionEntry{}
	for _, extension := range config.Extensions {
		byID[extension.ID] = append(byID[extension.ID], extension)
	}
	published := map[string]bool{}
	skippedTags := 0
	// 同一扩展可能有多个规范版本，索引保留全部版本，由消费侧选择要用的那个。
	for _, item := range releases {
		kind, id, version, err := distribution.ParseReleaseTag(item.TagName)
		if err != nil {
			skippedTags++
			continue
		}
		if _, ok := byID[id]; !ok {
			return summary{}, fmt.Errorf("Release %s 的扩展没有写进 %s", item.TagName, extensionsPath)
		}
		sourcePath, err := resolveSourcePath(byID[id], kind, id)
		if err != nil {
			return summary{}, err
		}
		manifestName, err := catalog.ManifestFile(kind)
		if err != nil {
			return summary{}, err
		}
		releaseBytes, err := client.asset(repository, item, distribution.ManifestName(id, version))
		if err != nil {
			return summary{}, fmt.Errorf("%s: %w", item.TagName, err)
		}
		var release distribution.ReleaseManifest
		if err := json.Unmarshal(releaseBytes, &release); err != nil {
			return summary{}, fmt.Errorf("%s: 发布清单无法解析: %w", item.TagName, err)
		}
		if err := release.Validate(); err != nil {
			return summary{}, fmt.Errorf("%s: %w", item.TagName, err)
		}
		sourceBytes, err := client.fileAt(repository, item.TagName, path.Join(sourcePath, manifestName))
		if err != nil {
			return summary{}, fmt.Errorf("%s: %w", item.TagName, err)
		}
		var manifest catalog.Manifest
		if err := json.Unmarshal(sourceBytes, &manifest); err != nil {
			return summary{}, fmt.Errorf("%s: 源码清单无法解析: %w", item.TagName, err)
		}
		facts := catalog.ReleaseFacts{
			Release:     release,
			PublishedAt: firstNonEmpty(item.PublishedAt, item.CreatedAt),
			Repository:  repository,
		}
		if release.SourceCommit != "" {
			tree, err := client.sourceTreeOf(repository, release.SourceCommit, sourcePath)
			if err != nil {
				return summary{}, fmt.Errorf("%s: %w", item.TagName, err)
			}
			facts.SourceTree = tree
		}
		if kind == distribution.KindWorkflow {
			lockName := id + "-" + version + ".extension-lock.json"
			lockBytes, err := client.asset(repository, item, lockName)
			if err != nil {
				return summary{}, fmt.Errorf("%s: %w", item.TagName, err)
			}
			if !json.Valid(lockBytes) {
				return summary{}, fmt.Errorf("%s: 扩展锁不是合法 JSON", item.TagName)
			}
			facts.ExtensionLock = lockBytes
		}
		entry, err := catalog.Entry(kind, manifest, facts)
		if err != nil {
			return summary{}, fmt.Errorf("%s: %w", item.TagName, err)
		}
		if err := target.Upsert(kind, entry); err != nil {
			return summary{}, fmt.Errorf("%s: %w", item.TagName, err)
		}
		published[id] = true
	}
	if !allowMissing {
		missing := make([]string, 0, len(byID))
		for id := range byID {
			if !published[id] {
				missing = append(missing, id)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			return summary{}, fmt.Errorf("以下扩展还没有规范发布，先发布再重建索引: %s", strings.Join(missing, ", "))
		}
	}
	if err := target.Save(catalogPath); err != nil {
		return summary{}, err
	}
	return summary{
		Plugins: len(target.Plugins), Skills: len(target.Skills),
		Workflows: len(target.Workflows), Generation: target.Generation,
		SkippedTags: skippedTags,
	}, nil
}

// resolveSourcePath 找到扩展在仓库里的目录。
func resolveSourcePath(entries []extensionEntry, kind, id string) (string, error) {
	for _, entry := range entries {
		if entry.Type == kind && strings.TrimSpace(entry.Path) != "" {
			return entry.Path, nil
		}
	}
	return "", fmt.Errorf("扩展 %s 在仓库清单里没有 %s 目录", id, kind)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

type client struct {
	http  *http.Client
	base  string
	token string
}

func newClient(base, token string) (*client, error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return nil, errors.New("api-base 不能为空")
	}
	resolved := strings.TrimSpace(token)
	if resolved == "" {
		resolved = strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	}
	if resolved == "" {
		resolved = strings.TrimSpace(os.Getenv("GH_TOKEN"))
	}
	if resolved == "" {
		if output, err := exec.Command("gh", "auth", "token").Output(); err == nil {
			resolved = strings.TrimSpace(string(output))
		}
	}
	if resolved == "" {
		return nil, errors.New("需要一个 GitHub token：-token、GITHUB_TOKEN、GH_TOKEN 或 gh auth login")
	}
	return &client{http: &http.Client{Timeout: 120 * time.Second}, base: base, token: resolved}, nil
}

func (c *client) get(requestURL, accept string) ([]byte, error) {
	request, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", accept)
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "himind-catalog-sync")
	request.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API %s 返回 %s", requestURL, response.Status)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	return body, nil
}

func (c *client) listReleases(repository string) ([]release, error) {
	releases := make([]release, 0, 32)
	for page := 1; page <= 20; page++ {
		endpoint := fmt.Sprintf("%s/repos/%s/releases?per_page=100&page=%d", c.base, repository, page)
		data, err := c.get(endpoint, "application/vnd.github+json")
		if err != nil {
			return nil, err
		}
		var batch []release
		if err := json.Unmarshal(data, &batch); err != nil {
			return nil, fmt.Errorf("Release 列表无法解析: %w", err)
		}
		for _, item := range batch {
			if !item.Draft {
				releases = append(releases, item)
			}
		}
		if len(batch) < 100 {
			break
		}
	}
	return releases, nil
}

func (c *client) asset(repository string, item release, name string) ([]byte, error) {
	for _, asset := range item.Assets {
		if asset.Name != name {
			continue
		}
		return c.get(asset.URL, "application/octet-stream")
	}
	return nil, fmt.Errorf("Release 里缺少资产 %s", name)
}

// fileAt 读取某个 tag 上的一个文件。用 contents API 而不是 raw 地址：
// 规范 tag 里带 `/`，contents API 的 ref 参数没有歧义。
func (c *client) fileAt(repository, tag, filePath string) ([]byte, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/contents/%s?ref=%s", c.base, repository, filePath, url.QueryEscape(tag))
	data, err := c.get(endpoint, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	var payload struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("%s 无法解析: %w", filePath, err)
	}
	if payload.Encoding != "base64" {
		return nil, fmt.Errorf("%s 编码不受支持: %s", filePath, payload.Encoding)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(payload.Content, "\n", ""))
	if err != nil {
		return nil, fmt.Errorf("%s 无法解码: %w", filePath, err)
	}
	return decoded, nil
}

// sourceTreeOf 返回某个提交里「扩展源码目录」的 git tree 哈希，等价于
// `git rev-parse <commit>:<sourcePath>`。
//
// 索引里的 source_tree 必须与发布时写进索引的值可比：发布侧（以及消费侧判断
// 「发布后源码改没改」）比的是这个目录的树，所以这里不能取提交的根树。
func (c *client) sourceTreeOf(repository, commit, sourcePath string) (string, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/git/trees/%s?recursive=1", c.base, repository, commit)
	data, err := c.get(endpoint, "application/vnd.github+json")
	if err != nil {
		return "", err
	}
	var payload struct {
		Truncated bool `json:"truncated"`
		Tree      []struct {
			Path string `json:"path"`
			Type string `json:"type"`
			SHA  string `json:"sha"`
		} `json:"tree"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", err
	}
	if payload.Truncated {
		return "", fmt.Errorf("提交 %s 的树太大，无法解析", commit)
	}
	wanted := strings.Trim(strings.ReplaceAll(sourcePath, "\\", "/"), "/")
	if wanted == "" {
		return "", fmt.Errorf("源码目录不能为空")
	}
	for _, entry := range payload.Tree {
		if entry.Type == "tree" && entry.Path == wanted {
			return entry.SHA, nil
		}
	}
	return "", fmt.Errorf("提交 %s 里找不到源码目录 %s", commit, sourcePath)
}
