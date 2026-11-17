package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/MrBaoquan/himind-extensions/internal/mediadownload"
)

const discoveryPageSize = 12

var discoveryCursorPattern = regexp.MustCompile(`^([a-f0-9]{32}):([1-5]?[0-9])$`)

type discoveryInput struct {
	Provider  string `json:"provider"`
	Query     string `json:"query"`
	Cursor    string `json:"cursor"`
	RequestID string `json:"request_id"`
}
type discoverySnapshot struct {
	Provider   string           `json:"provider"`
	Query      string           `json:"query"`
	Generation string           `json:"generation"`
	Expires    int64            `json:"expires"`
	Items      []remoteResource `json:"items"`
}
type discoveryPage struct {
	Items      []remoteResource `json:"items"`
	NextCursor string           `json:"next_cursor,omitempty"`
	Warnings   []string         `json:"warnings"`
	Expires    int64            `json:"expires_at"`
}
type browserSearchResult struct {
	State string           `json:"state"`
	Items []remoteResource `json:"items"`
}

// Extractor support and official-page search are independent capabilities.
func discoveryProviders() any {
	return map[string]any{"items": []map[string]any{
		{"id": "stock", "search": true, "preview": true, "download": true, "message": "使用已配置的素材站。"},
		{"id": "douyin", "search": true, "preview": true, "download": true, "message": "连接抖音后搜索官网作品；需要验证时会打开官网窗口。采集以解析结果为准。"},
		{"id": "bilibili", "search": true, "preview": true, "download": true, "message": "搜索官网公开作品；需要验证时会打开官网窗口。采集以解析结果为准。"},
		{"id": "youtube", "search": true, "preview": true, "download": true, "message": "搜索官网公开作品，需能访问 YouTube。采集以解析结果为准。"},
	}}
}

func normalizeDiscoveryInput(in discoveryInput) (discoveryInput, error) {
	if in.Provider != "douyin" && in.Provider != "bilibili" && in.Provider != "youtube" {
		return in, fmt.Errorf("请选择支持搜索的平台")
	}
	in.Query = strings.Join(strings.Fields(in.Query), " ")
	if n := len([]rune(in.Query)); n == 0 || n > 100 || strings.IndexFunc(in.Query, unicode.IsControl) >= 0 {
		return in, fmt.Errorf("请输入 1–100 字的关键词")
	}
	if in.Cursor != "" && !discoveryCursorPattern.MatchString(in.Cursor) {
		return in, fmt.Errorf("搜索页已失效，请重新搜索")
	}
	if in.RequestID != "" && !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(in.RequestID) {
		return in, fmt.Errorf("搜索请求无效，请重新搜索")
	}
	return in, nil
}

func discoveryGeneration(provider string) (string, error) {
	if provider != "douyin" {
		return "public", nil
	}
	if err := configureDownloadKernel(); err != nil {
		return "", err
	}
	status, err := mediadownload.ConnectionStatus()
	if err != nil {
		return "", fmt.Errorf("无法读取抖音连接，请在设置中重试")
	}
	if status.State != "connected" || status.Generation == "" {
		return "", fmt.Errorf("请先在“平台与采集”中连接抖音，再搜索作品")
	}
	return status.Generation, nil
}

func searchDiscovery(in discoveryInput) (any, error) {
	in, err := normalizeDiscoveryInput(in)
	if err != nil {
		return nil, err
	}
	generation, err := discoveryGeneration(in.Provider)
	if err != nil {
		return nil, err
	}
	root, err := pluginDataRoot()
	if err != nil {
		return nil, err
	}
	cacheRoot := filepath.Join(root, "discovery-cache")
	if err = os.MkdirAll(cacheRoot, 0700); err != nil {
		return nil, err
	}
	if in.Cursor != "" {
		parts := discoveryCursorPattern.FindStringSubmatch(in.Cursor)
		data, err := os.ReadFile(filepath.Join(cacheRoot, parts[1]+".json"))
		if err != nil || len(data) > 256<<10 {
			return nil, fmt.Errorf("搜索结果已过期，请重新搜索")
		}
		var snapshot discoverySnapshot
		if json.Unmarshal(data, &snapshot) != nil {
			return nil, fmt.Errorf("搜索结果已过期，请重新搜索")
		}
		offset, _ := strconv.Atoi(parts[2])
		return completeDiscoveryPage(snapshot, in, generation, root, parts[1], offset)
	}
	id, err := discoveryID()
	if err != nil {
		return nil, err
	}
	if in.RequestID != "" {
		id = in.RequestID
	}
	if _, err := os.Stat(filepath.Join(root, "discovery-cancel", id)); err == nil {
		return nil, fmt.Errorf("搜索已取消，可以重新搜索")
	}
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err = acquireDiscoveryLease(db, in.Provider, id, time.Now()); err != nil {
		return nil, err
	}
	defer db.Exec(`UPDATE discovery_leases SET until_at=0 WHERE provider=? AND owner=?`, in.Provider, id)
	ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
	defer cancel()
	result, err := browseOfficialSearch(ctx, root, id, in, generation)
	if err != nil {
		return nil, err
	}
	current, err := discoveryGeneration(in.Provider)
	if err != nil || current != generation {
		return nil, fmt.Errorf("平台连接已变化，请重新搜索")
	}
	if result.State != "ok" {
		switch result.State {
		case "empty":
			return discoveryPage{Items: []remoteResource{}, Warnings: []string{}}, nil
		case "canceled":
			return nil, fmt.Errorf("搜索已取消，可以重新搜索")
		default:
			return nil, fmt.Errorf("官网未返回可读取的作品，请检查网络或完成官网验证后重试")
		}
	}
	snapshot := discoverySnapshot{Provider: in.Provider, Query: in.Query, Generation: generation, Expires: time.Now().Add(10 * time.Minute).Unix(), Items: cleanDiscoveryItems(in.Provider, result.Items)}
	cacheDiscoveryCovers(root, in.Provider, result.Items, snapshot.Items)
	if len(snapshot.Items) == 0 {
		return nil, fmt.Errorf("官网结果暂时无法识别，可添加作品链接或稍后重试")
	}
	encoded, _ := json.Marshal(snapshot)
	if err = os.WriteFile(filepath.Join(cacheRoot, id+".json"), encoded, 0600); err != nil {
		return nil, err
	}
	// Remove only our expired numeric metadata snapshots, never session profiles.
	entries, _ := os.ReadDir(cacheRoot)
	for _, entry := range entries {
		if entry.IsDir() || !sessionCacheFileName(entry.Name()) {
			continue
		}
		if info, err := entry.Info(); err == nil && time.Since(info.ModTime()) > 15*time.Minute {
			_ = os.Remove(filepath.Join(cacheRoot, entry.Name()))
		}
	}
	return completeDiscoveryPage(snapshot, in, generation, root, id, 0)
}

var discoveryCacheFilePattern = regexp.MustCompile(`^[a-f0-9]{32}\.json$`)

func sessionCacheFileName(name string) bool { return discoveryCacheFilePattern.MatchString(name) }

func pageDiscovery(snapshot discoverySnapshot, in discoveryInput, generation, id string, offset int, now time.Time) (discoveryPage, error) {
	if snapshot.Provider != in.Provider || snapshot.Query != in.Query || snapshot.Generation != generation || snapshot.Expires <= now.Unix() || len(snapshot.Items) > 60 || offset < 0 || offset >= len(snapshot.Items) || offset%discoveryPageSize != 0 {
		return discoveryPage{}, fmt.Errorf("搜索结果或平台连接已变化，请重新搜索")
	}
	end := min(offset+discoveryPageSize, len(snapshot.Items))
	page := discoveryPage{Items: hydrateDiscoveryCovers(snapshot.Items[offset:end]), Warnings: []string{}, Expires: snapshot.Expires}
	if end < len(snapshot.Items) {
		page.NextCursor = id + ":" + strconv.Itoa(end)
	}
	return page, nil
}

func cleanDiscoveryItems(provider string, items []remoteResource) []remoteResource {
	cleaned := []remoteResource{}
	seen := map[string]bool{}
	for _, item := range items {
		p, pid, canonical, err := canonicalRemoteReference(item.SourceURL)
		if err != nil || p != provider || seen[pid] {
			continue
		}
		seen[pid] = true
		item = remoteResource{ID: stableID("remote", p+":"+pid), Provider: p, ProviderID: pid, SourceURL: canonical, Kind: "video", Title: clipDiscoveryText(item.Title, 300), Author: clipDiscoveryText(item.Author, 120), Tags: []string{}}
		if item.Title == "" {
			item.Title = pid
		}
		cleaned = append(cleaned, item)
		if len(cleaned) == 60 {
			break
		}
	}
	return cleaned
}

func clipDiscoveryText(text string, limit int) string {
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text)
	runes := []rune(strings.Join(strings.Fields(text), " "))
	if len(runes) > limit {
		runes = runes[:limit]
	}
	return string(runes)
}

func discoveryID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}

func acquireDiscoveryLease(db *sql.DB, provider, owner string, now time.Time) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS discovery_leases(provider TEXT PRIMARY KEY,owner TEXT NOT NULL,until_at INTEGER NOT NULL,next_at INTEGER NOT NULL)`); err != nil {
		return err
	}
	_, err := db.Exec(`INSERT OR IGNORE INTO discovery_leases VALUES(?,'',0,0)`, provider)
	if err != nil {
		return err
	}
	result, err := db.Exec(`UPDATE discovery_leases SET owner=?,until_at=?,next_at=? WHERE provider=? AND until_at<=? AND next_at<=?`, owner, now.Add(75*time.Second).Unix(), now.Add(3*time.Second).Unix(), provider, now.Unix(), now.Unix())
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return fmt.Errorf("此平台正在搜索或请求过快，请稍后重试")
	}
	return nil
}

func browseOfficialSearch(ctx context.Context, root, id string, in discoveryInput, generation string) (browserSearchResult, error) {
	var result browserSearchResult
	executable, err := os.Executable()
	if err != nil {
		return result, err
	}
	helper := filepath.Join(filepath.Dir(executable), "session-browser", "media-session-browser.exe")
	if _, err = os.Stat(helper); err != nil {
		return result, fmt.Errorf("平台搜索组件缺失，请重新安装媒体资源库")
	}
	requestDir := filepath.Join(root, "discovery-requests", id)
	if err = os.MkdirAll(filepath.Dir(requestDir), 0700); err != nil {
		return result, err
	}
	if err = os.Mkdir(requestDir, 0700); err != nil {
		return result, fmt.Errorf("搜索请求已使用，请重新搜索")
	}
	requestPath, resultPath := filepath.Join(requestDir, "request.json"), filepath.Join(requestDir, "result.json")
	defer func() { _ = os.Remove(requestPath); _ = os.Remove(resultPath); _ = os.Remove(requestDir) }()
	data, _ := json.Marshal(map[string]string{"provider": in.Provider, "query": in.Query, "generation": generation})
	if err = os.WriteFile(requestPath, data, 0600); err != nil {
		return result, err
	}
	cmd := exec.CommandContext(ctx, helper, "--search", root, id)
	// Avoid inheriting host credentials or remote debugging/profile overrides.
	for _, env := range os.Environ() {
		key := strings.ToUpper(strings.SplitN(env, "=", 2)[0])
		if key == "SYSTEMROOT" || key == "WINDIR" || key == "TEMP" || key == "TMP" || key == "LOCALAPPDATA" || key == "USERPROFILE" || key == "PATH" {
			cmd.Env = append(cmd.Env, env)
		}
	}
	if err = cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return result, fmt.Errorf("官网搜索超时，请检查网络或完成官网验证后重试")
		}
		return result, fmt.Errorf("平台搜索窗口未能完成，请重试")
	}
	data, err = os.ReadFile(resultPath)
	if err != nil || len(data) > 256<<10 || json.Unmarshal(data, &result) != nil {
		return result, fmt.Errorf("未读取到平台结果，请重试")
	}
	return result, nil
}

func cancelDiscovery(id string) (any, error) {
	if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(id) {
		return nil, fmt.Errorf("搜索请求无效")
	}
	root, err := pluginDataRoot()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, "discovery-cancel")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err = os.WriteFile(filepath.Join(dir, id), []byte("cancel"), 0600); err != nil {
		return nil, err
	}
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if !entry.IsDir() && regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(entry.Name()) {
			if info, err := entry.Info(); err == nil && time.Since(info.ModTime()) > 15*time.Minute {
				_ = os.Remove(filepath.Join(dir, entry.Name()))
			}
		}
	}
	return map[string]bool{"cancel_requested": true}, nil
}
