package main

// 媒体资源库插件。
//
// 它只做四件事：把已配置的免费素材来源列出来、按关键词跨来源检索、把选中的
// 素材落到工作区并留下来源与许可记录、给本机已有媒体建索引并按名检索。
//
// 边界：不依赖 HiMind 工作台，不调用 Dashboard 接口，不读取 Agent 凭据；
// 所有写操作只发生在工作区目录和扩展私有数据目录内。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MrBaoquan/himind-extensions/sdk/jsonrpc"
)

const (
	dataNamespace   = "com.himind.media-library"
	indexFileName   = "library-index.json"
	keysFileName    = "provider-keys.json"
	defaultDestDir  = "media"
	maxIndexFiles   = 20000
	minDownloadSize = 1 << 10
	maxDownloadSize = 64 << 20
	maxDownloadCap  = 512 << 20
	searchTimeout   = 25 * time.Second
	downloadTimeout = 5 * time.Minute
)

var downloadClient = &http.Client{Timeout: downloadTimeout}

// mediaExtensions 是会被索引的媒体扩展名，用于把文件归到图片 / 视频 / 音频三类。
var mediaExtensions = map[string]string{
	".png": "image", ".jpg": "image", ".jpeg": "image", ".webp": "image",
	".gif": "image", ".bmp": "image", ".avif": "image", ".svg": "image",
	".mp4": "video", ".mov": "video", ".mkv": "video", ".webm": "video",
	".avi": "video", ".m4v": "video",
	".mp3": "audio", ".wav": "audio", ".flac": "audio", ".m4a": "audio",
	".aac": "audio", ".ogg": "audio", ".opus": "audio",
}

type providerStatus struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Kinds       []string `json:"kinds"`
	Configured  bool     `json:"configured"`
	Requirement string   `json:"requirement"`
}

type searchInput struct {
	Query              string   `json:"query"`
	Kinds              []string `json:"kinds"`
	Providers          []string `json:"providers"`
	PerSource          int      `json:"per_source"`
	Page               int      `json:"page"`
	Orientation        string   `json:"orientation"`
	MinWidth           int      `json:"min_width"`
	MinDurationSeconds float64  `json:"min_duration_seconds"`
}

type providerOutcome struct {
	ID     string      `json:"id"`
	Name   string      `json:"name"`
	Status string      `json:"status"`
	Error  string      `json:"error,omitempty"`
	Items  []assetItem `json:"items"`
}

type fetchItem struct {
	Provider    string `json:"provider"`
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Author      string `json:"author"`
	License     string `json:"license"`
	SourceURL   string `json:"source_url"`
	DownloadURL string `json:"download_url"`
	Filename    string `json:"filename"`
}

type fetchInput struct {
	WorkspaceRoot string      `json:"workspace_root"`
	DestDir       string      `json:"dest_dir"`
	Items         []fetchItem `json:"items"`
	MaxBytes      int64       `json:"max_bytes"`
	Overwrite     bool        `json:"overwrite"`
}

func main() {
	if err := jsonrpc.Serve(os.Stdin, os.Stdout, handle); err != nil {
		fmt.Fprintln(os.Stderr, "媒体资源库插件已停止:", err)
	}
}

func handle(request jsonrpc.Request) (any, *jsonrpc.Error) {
	switch request.Method {
	case "media-library.provider.list":
		// 无参数调用也要能用：模型或视图可能不带 params 直接问有哪些来源。
		if len(request.Params) > 0 {
			var in struct{}
			if rpcError := jsonrpc.DecodeParams(request, &in); rpcError != nil {
				return nil, rpcError
			}
		}
		return rpcResult(listProviders())
	case "media-library.search":
		var in searchInput
		if rpcError := jsonrpc.DecodeParams(request, &in); rpcError != nil {
			return nil, rpcError
		}
		return rpcResult(search(in))
	case "media-library.asset.fetch":
		var in fetchInput
		if rpcError := jsonrpc.DecodeParams(request, &in); rpcError != nil {
			return nil, rpcError
		}
		return rpcResult(fetchAssets(in))
	case "media-library.index.create":
		var in struct {
			Roots    []string `json:"roots"`
			MaxFiles int      `json:"max_files"`
		}
		if rpcError := jsonrpc.DecodeParams(request, &in); rpcError != nil {
			return nil, rpcError
		}
		return rpcResult(indexLibrary(in.Roots, in.MaxFiles))
	case "media-library.index.search":
		var in struct {
			Query string   `json:"query"`
			Kinds []string `json:"kinds"`
			Limit int      `json:"limit"`
		}
		if rpcError := jsonrpc.DecodeParams(request, &in); rpcError != nil {
			return nil, rpcError
		}
		return rpcResult(searchLibrary(in.Query, in.Kinds, in.Limit))
	case "media-library.provider.configure":
		var in struct {
			Provider string `json:"provider"`
			Key      string `json:"key"`
		}
		if rpcError := jsonrpc.DecodeParams(request, &in); rpcError != nil {
			return nil, rpcError
		}
		return rpcResult(configureProvider(in.Provider, in.Key))
	default:
		return nil, &jsonrpc.Error{Code: -32602, Message: "不支持的媒体资源库能力"}
	}
}

func rpcResult(result any, err error) (any, *jsonrpc.Error) {
	if err != nil {
		return nil, &jsonrpc.Error{Code: -32000, Message: err.Error()}
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// provider.list
// ---------------------------------------------------------------------------
// configurableProviders 是允许在视图里填写密钥的来源。
var configurableProviders = map[string]string{
	"pexels":    "Pexels API Key",
	"unsplash":  "Unsplash Access Key",
	"freesound": "Freesound API Token",
}

// configureProvider 把密钥写进扩展私有数据目录；传空值表示清除。
// 密钥只落在本插件自己的目录里，不回显、不进日志、不写进工作区。
func configureProvider(provider string, key string) (any, error) {
	id := strings.TrimSpace(provider)
	if _, ok := configurableProviders[id]; !ok {
		return nil, fmt.Errorf("不支持配置的来源: %s", provider)
	}
	value := strings.TrimSpace(key)
	if value != "" && len(value) < 8 {
		return nil, fmt.Errorf("密钥长度不足，请确认复制完整")
	}
	if err := saveProviderKey(id, value); err != nil {
		return nil, err
	}
	configured := value != ""
	message := "已保存，可以开始检索"
	if !configured {
		message = "已清除该来源的密钥"
	}
	return map[string]any{"provider": id, "configured": configured, "message": message}, nil
}

func providerKeyFile() (string, error) {
	root, err := pluginDataRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, keysFileName), nil
}

func readProviderKeys() map[string]string {
	keys := map[string]string{}
	path, err := providerKeyFile()
	if err != nil {
		return keys
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return keys
	}
	_ = json.Unmarshal(payload, &keys)
	return keys
}

// providerKey 先看扩展自己的配置，再看环境变量；两条路都不需要工作台。
func providerKey(id string, envName string) string {
	if value := strings.TrimSpace(readProviderKeys()[id]); value != "" {
		return value
	}
	return strings.TrimSpace(os.Getenv(envName))
}

func saveProviderKey(id string, value string) error {
	path, err := providerKeyFile()
	if err != nil {
		return err
	}
	keys := readProviderKeys()
	if value == "" {
		delete(keys, id)
	} else {
		keys[id] = value
	}
	encoded, err := json.MarshalIndent(keys, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		return fmt.Errorf("保存密钥失败: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// provider.list
// ---------------------------------------------------------------------------

func listProviders() (any, error) {
	providers := providerRegistry()
	items := make([]providerStatus, 0, len(providers))
	for _, item := range providers {
		items = append(items, providerStatus{
			ID:          item.ID(),
			Name:        item.Name(),
			Kinds:       item.Kinds(),
			Configured:  item.Configured(),
			Requirement: item.Requirement(),
		})
	}
	return map[string]any{"providers": items}, nil
}

// ---------------------------------------------------------------------------
// search
// ---------------------------------------------------------------------------

func search(in searchInput) (any, error) {
	query := cleanText(in.Query)
	if query == "" {
		return nil, fmt.Errorf("请先给出检索关键词")
	}
	for _, kind := range in.Kinds {
		if kind != "image" && kind != "video" && kind != "audio" {
			return nil, fmt.Errorf("不支持的素材类型: %s", kind)
		}
	}
	requested := map[string]bool{}
	for _, id := range in.Providers {
		requested[strings.TrimSpace(id)] = true
	}
	selected := []mediaProvider{}
	for _, item := range providerRegistry() {
		if len(requested) > 0 && !requested[item.ID()] {
			continue
		}
		if !providerSupports(item, in.Kinds) {
			continue
		}
		selected = append(selected, item)
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("没有匹配的素材来源，请检查来源筛选或类型筛选")
	}

	ctx, cancel := context.WithTimeout(context.Background(), searchTimeout)
	defer cancel()

	outcomes := make([]providerOutcome, len(selected))
	var wait sync.WaitGroup
	for index, item := range selected {
		wait.Add(1)
		go func(slot int, provider mediaProvider) {
			defer wait.Done()
			outcome := providerOutcome{ID: provider.ID(), Name: provider.Name(), Items: []assetItem{}}
			if !provider.Configured() {
				outcome.Status = "skipped"
				outcome.Error = "未配置：" + provider.Requirement()
				outcomes[slot] = outcome
				return
			}
			found, err := provider.Search(ctx, searchQuery{
				Text:        query,
				Kinds:       in.Kinds,
				PerSource:   in.PerSource,
				Page:        in.Page,
				Orientation: in.Orientation,
				MinWidth:    in.MinWidth,
				MinDuration: in.MinDurationSeconds,
			})
			if err != nil {
				outcome.Status = "failed"
				outcome.Error = err.Error()
				outcomes[slot] = outcome
				return
			}
			outcome.Status = "ok"
			outcome.Items = found
			outcomes[slot] = outcome
		}(index, item)
	}
	wait.Wait()

	// 单个来源失败不影响整体结果：每个来源各自带状态返回，模型和视图都能看懂。
	items := []assetItem{}
	failures := []string{}
	for _, outcome := range outcomes {
		items = append(items, outcome.Items...)
		if outcome.Status == "failed" {
			failures = append(failures, outcome.Name+"："+outcome.Error)
		}
	}
	result := map[string]any{
		"query":     query,
		"providers": outcomes,
		"items":     items,
		"total":     len(items),
	}
	if len(failures) > 0 {
		result["warnings"] = failures
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// asset.fetch
// ---------------------------------------------------------------------------

func fetchAssets(in fetchInput) (any, error) {
	if len(in.Items) == 0 {
		return nil, fmt.Errorf("请先选择要取用的素材")
	}
	workspace, err := resolveWorkspace(in.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	destDir := strings.TrimSpace(in.DestDir)
	if destDir == "" {
		destDir = defaultDestDir
	}
	if filepath.IsAbs(destDir) || strings.Contains(destDir, "..") {
		return nil, fmt.Errorf("落盘目录必须是工作区内的相对路径")
	}
	destRoot, err := resolveWithin(workspace, filepath.Join(workspace, destDir))
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(destRoot, 0o755); err != nil {
		return nil, fmt.Errorf("创建落盘目录失败: %w", err)
	}
	limit := in.MaxBytes
	if limit <= 0 {
		limit = maxDownloadSize
	}
	if limit > maxDownloadCap {
		limit = maxDownloadCap
	}

	saved := []map[string]any{}
	skipped := []map[string]any{}
	failed := []map[string]any{}
	for _, item := range in.Items {
		record, err := fetchOne(destRoot, item, limit, in.Overwrite)
		if err != nil {
			failed = append(failed, map[string]any{"id": item.ID, "provider": item.Provider, "error": err.Error()})
			continue
		}
		if record["skipped"] == true {
			skipped = append(skipped, record)
			continue
		}
		saved = append(saved, record)
	}
	return map[string]any{
		"dest_dir": destRoot,
		"saved":    saved,
		"skipped":  skipped,
		"failed":   failed,
	}, nil
}

func fetchOne(destRoot string, item fetchItem, limit int64, overwrite bool) (map[string]any, error) {
	target, err := url.Parse(strings.TrimSpace(item.DownloadURL))
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") {
		return nil, fmt.Errorf("下载地址必须是 http 或 https")
	}
	filename := safeFilename(item.Filename)
	if filename == "" {
		filename = safeFilename(filepath.Base(target.Path))
	}
	if filename == "" {
		filename = fmt.Sprintf("%s-%s", firstNonEmpty(item.Provider, "asset"), firstNonEmpty(item.ID, "item"))
	}
	if ext := filepath.Ext(filename); ext == "" {
		filename += extensionForKind(item.Kind)
	}
	destination, err := resolveWithin(destRoot, filepath.Join(destRoot, filename))
	if err != nil {
		return nil, err
	}

	request, err := http.NewRequest(http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	response, err := downloadClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("下载失败，来源返回 HTTP %d", response.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > limit {
		return nil, fmt.Errorf("素材超过单文件上限 %d 字节，请调大 max_bytes 或改用更小的版本", limit)
	}
	if int64(len(payload)) < minDownloadSize {
		return nil, fmt.Errorf("下载内容过小，可能是来源返回了错误页")
	}
	digest := sha256.Sum256(payload)
	sha := hex.EncodeToString(digest[:])

	if existing, err := os.ReadFile(destination); err == nil {
		total := sha256.Sum256(existing)
		if hex.EncodeToString(total[:]) == sha && !overwrite {
			return map[string]any{"path": destination, "sha256": sha, "bytes": len(payload), "skipped": true}, nil
		}
	}
	if err := os.WriteFile(destination, payload, 0o644); err != nil {
		return nil, fmt.Errorf("写入失败: %w", err)
	}
	sidecar := destination + ".source.json"
	record := map[string]any{
		"path":         destination,
		"sha256":       sha,
		"bytes":        len(payload),
		"provider":     item.Provider,
		"provider_id":  item.ID,
		"kind":         item.Kind,
		"title":        item.Title,
		"author":       item.Author,
		"license":      item.License,
		"source_url":   item.SourceURL,
		"download_url": target.String(),
		"fetched_at":   time.Now().UTC().Format(time.RFC3339),
	}
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(sidecar, append(encoded, '\n'), 0o644); err != nil {
		return nil, fmt.Errorf("写入来源记录失败: %w", err)
	}
	record["sidecar"] = sidecar
	return record, nil
}

// ---------------------------------------------------------------------------
// library.index / library.search
// ---------------------------------------------------------------------------

type libraryEntry struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Extension  string `json:"extension"`
	Bytes      int64  `json:"bytes"`
	ModifiedAt string `json:"modified_at"`
}

type libraryIndex struct {
	GeneratedAt string         `json:"generated_at"`
	Roots       []string       `json:"roots"`
	Entries     []libraryEntry `json:"entries"`
	Truncated   bool           `json:"truncated"`
}

func indexLibrary(roots []string, maxFiles int) (any, error) {
	cleaned := []string{}
	for _, root := range roots {
		value := strings.TrimSpace(root)
		if value == "" {
			continue
		}
		absolute, err := filepath.Abs(value)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(absolute)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("目录不可用: %s", value)
		}
		cleaned = append(cleaned, absolute)
	}
	if len(cleaned) == 0 {
		return nil, fmt.Errorf("请给出至少一个要索引的目录")
	}
	if maxFiles <= 0 {
		maxFiles = maxIndexFiles
	}
	entries := []libraryEntry{}
	truncated := false
	for _, root := range cleaned {
		if truncated {
			break
		}
		walkErr := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if entry.IsDir() {
				name := entry.Name()
				if name == ".git" || name == "node_modules" || name == ".himind" {
					return filepath.SkipDir
				}
				return nil
			}
			kind, ok := mediaExtensions[strings.ToLower(filepath.Ext(path))]
			if !ok {
				return nil
			}
			if len(entries) >= maxFiles {
				truncated = true
				return filepath.SkipAll
			}
			info, err := entry.Info()
			if err != nil {
				return nil
			}
			entries = append(entries, libraryEntry{
				Path:       path,
				Name:       entry.Name(),
				Kind:       kind,
				Extension:  strings.ToLower(filepath.Ext(path)),
				Bytes:      info.Size(),
				ModifiedAt: info.ModTime().UTC().Format(time.RFC3339),
			})
			return nil
		})
		if walkErr != nil {
			return nil, walkErr
		}
	}
	sort.SliceStable(entries, func(left, right int) bool { return entries[left].Path < entries[right].Path })
	index := libraryIndex{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Roots:       cleaned,
		Entries:     entries,
		Truncated:   truncated,
	}
	path, err := writeLibraryIndex(index)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{"image": 0, "video": 0, "audio": 0}
	for _, entry := range entries {
		counts[entry.Kind]++
	}
	return map[string]any{
		"index_path":   path,
		"roots":        cleaned,
		"total":        len(entries),
		"counts":       counts,
		"truncated":    truncated,
		"generated_at": index.GeneratedAt,
	}, nil
}

func searchLibrary(query string, kinds []string, limit int) (any, error) {
	index, err := readLibraryIndex()
	if err != nil {
		return nil, err
	}
	if index == nil {
		return map[string]any{
			"indexed": false,
			"items":   []libraryEntry{},
			"message": "还没有本机媒体索引，先调用 media-library.index.create 指定要索引的目录。",
		}, nil
	}
	if limit <= 0 {
		limit = 60
	}
	needle := strings.ToLower(cleanText(query))
	items := []libraryEntry{}
	for _, entry := range index.Entries {
		if len(kinds) > 0 && !kindRequested(kinds, entry.Kind) {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(entry.Name), needle) {
			continue
		}
		items = append(items, entry)
	}
	sort.SliceStable(items, func(left, right int) bool { return items[left].ModifiedAt > items[right].ModifiedAt })
	total := len(items)
	if total > limit {
		items = items[:limit]
	}
	return map[string]any{
		"indexed":      true,
		"generated_at": index.GeneratedAt,
		"roots":        index.Roots,
		"total":        total,
		"items":        items,
	}, nil
}

func writeLibraryIndex(index libraryIndex) (string, error) {
	root, err := pluginDataRoot()
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, indexFileName)
	encoded, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
		return "", fmt.Errorf("写入索引失败: %w", err)
	}
	return path, nil
}

func readLibraryIndex() (*libraryIndex, error) {
	root, err := pluginDataRoot()
	if err != nil {
		return nil, err
	}
	payload, err := os.ReadFile(filepath.Join(root, indexFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var index libraryIndex
	if err := json.Unmarshal(payload, &index); err != nil {
		return nil, fmt.Errorf("本机媒体索引无法读取，请重新建立: %w", err)
	}
	return &index, nil
}

// ---------------------------------------------------------------------------
// 路径与文件工具
// ---------------------------------------------------------------------------

func pluginDataRoot() (string, error) {
	if value := strings.TrimSpace(os.Getenv("HIMIND_PLUGIN_DATA_ROOT")); value != "" {
		if err := os.MkdirAll(value, 0o755); err != nil {
			return "", err
		}
		return value, nil
	}
	// 视图或命令行单独运行插件时没有宿主注入，退回用户缓存目录，仍然只属于本插件。
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	fallback := filepath.Join(base, "himind-plugin-data", dataNamespace)
	if err := os.MkdirAll(fallback, 0o755); err != nil {
		return "", err
	}
	return fallback, nil
}

func resolveWorkspace(root string) (string, error) {
	value := strings.TrimSpace(root)
	if value == "" {
		return "", fmt.Errorf("请先指定工作区目录")
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("工作区目录不可用: %s", value)
	}
	return absolute, nil
}

func resolveWithin(root string, candidate string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(rootAbs, target)
	if err != nil {
		return "", err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("目标路径越出允许范围")
	}
	return target, nil
}

func safeFilename(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "\\", "/")
	value = filepath.Base(value)
	replaced := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_' || r == '.' || r == ' ':
			return r
		case r > 127:
			return r
		default:
			return '-'
		}
	}, value)
	replaced = strings.Trim(replaced, " .")
	if replaced == "" || replaced == "." || replaced == ".." {
		return ""
	}
	if len([]rune(replaced)) > 120 {
		replaced = string([]rune(replaced)[:120])
	}
	return replaced
}

func extensionForKind(kind string) string {
	switch kind {
	case "image":
		return ".png"
	case "video":
		return ".mp4"
	case "audio":
		return ".mp3"
	default:
		return ".bin"
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
