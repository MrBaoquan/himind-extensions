package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/MrBaoquan/himind-extensions/internal/mediadownload"
)

var previewRequestPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
var previewCachePattern = regexp.MustCompile(`^[a-f0-9]{64}\.(mp4|webm)$`)

type remotePreviewInput struct {
	URL       string `json:"url"`
	RequestID string `json:"request_id"`
}

func remotePreview(in remotePreviewInput) (any, error) {
	if !previewRequestPattern.MatchString(in.RequestID) {
		return nil, fmt.Errorf("预览请求无效，请重新打开视频")
	}
	provider, _, canonical, err := canonicalRemoteReference(in.URL)
	if err != nil {
		return nil, err
	}
	generation, err := discoveryGeneration(provider)
	if err != nil {
		return nil, err
	}
	if err := configureDownloadKernel(); err != nil {
		return nil, err
	}
	root, err := pluginDataRoot()
	if err != nil {
		return nil, err
	}
	cache := filepath.Join(root, "video-previews")
	if err = os.MkdirAll(cache, 0700); err != nil {
		return nil, err
	}
	cleanupRemotePreviews(cache, time.Now())
	digest := sha256.Sum256([]byte(canonical + "\n" + generation))
	key := hex.EncodeToString(digest[:])
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cancelFile := filepath.Join(cache, "cancel-"+in.RequestID)
	defer os.Remove(cancelFile)
	// Cancellation survives the Agent's per-invocation process boundary.
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := os.Stat(cancelFile); err == nil {
					cancel()
					return
				}
			}
		}
	}()
	if _, err := os.Stat(cancelFile); err == nil {
		return nil, fmt.Errorf("预览已关闭")
	}
	result := func(path string) (any, error) {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("预览已关闭")
		}
		current, err := discoveryGeneration(provider)
		if err != nil || current != generation {
			return nil, fmt.Errorf("平台连接已变化，请重新打开视频")
		}
		stream := signedStreamURL(path)
		if stream == "" {
			return nil, fmt.Errorf("预览服务暂不可用，请重新打开媒体资源库")
		}
		return map[string]any{"stream_url": stream}, nil
	}
	for _, ext := range []string{".mp4", ".webm"} {
		path := filepath.Join(cache, key+ext)
		if info, err := os.Stat(path); err == nil && time.Since(info.ModTime()) < 30*time.Minute && info.Size() > 1024 {
			return result(path)
		}
	}
	work, err := os.MkdirTemp(cache, "loading-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	path, err := mediadownload.DownloadPreview(ctx, canonical, work)
	if err != nil {
		return nil, err
	}
	target := filepath.Join(cache, key+filepath.Ext(path))
	if err = os.Rename(path, target); err != nil {
		return nil, fmt.Errorf("预览缓存保存失败，请重试")
	}
	return result(target)
}

func cancelRemotePreview(id string) (any, error) {
	if !previewRequestPattern.MatchString(id) {
		return nil, fmt.Errorf("预览请求无效")
	}
	root, err := pluginDataRoot()
	if err != nil {
		return nil, err
	}
	cache := filepath.Join(root, "video-previews")
	if err = os.MkdirAll(cache, 0700); err != nil {
		return nil, err
	}
	err = os.WriteFile(filepath.Join(cache, "cancel-"+id), []byte("cancel"), 0600)
	return map[string]bool{"canceled": err == nil}, err
}

// Only known disposable files are removed. Recent playback remains usable;
// cached videos reserve 96 MiB for the next preview within a 288 MiB budget.
func cleanupRemotePreviews(root string, now time.Time) {
	entries, _ := os.ReadDir(root)
	files := []os.FileInfo{}
	var size int64
	for _, e := range entries {
		info, err := e.Info()
		if err == nil && info.IsDir() && regexp.MustCompile(`^loading-[0-9]+$`).MatchString(e.Name()) && now.Sub(info.ModTime()) > 30*time.Minute {
			_ = os.RemoveAll(filepath.Join(root, e.Name()))
		}
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if previewCachePattern.MatchString(e.Name()) {
			files = append(files, info)
			size += info.Size()
		} else if len(e.Name()) == 39 && e.Name()[:7] == "cancel-" && previewRequestPattern.MatchString(e.Name()[7:]) && now.Sub(info.ModTime()) > 2*time.Minute {
			_ = os.Remove(filepath.Join(root, e.Name()))
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].ModTime().Before(files[j].ModTime()) })
	for _, f := range files {
		if now.Sub(f.ModTime()) > 30*time.Minute || size > 192<<20 {
			if os.Remove(filepath.Join(root, f.Name())) == nil {
				size -= f.Size()
			}
		}
	}
}
