package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/MrBaoquan/himind-extensions/internal/mediadownload"
)

var discoveryCoverPattern = regexp.MustCompile(`^[a-f0-9]{64}\.jpg$`)

func discoveryCoverAllowed(provider, raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" && u.Port() != "443" {
		return false
	}
	domains := map[string][]string{"douyin": {"douyinpic.com", "byteimg.com", "douyincdn.com"}, "bilibili": {"hdslb.com"}, "youtube": {"ytimg.com"}}[provider]
	for _, d := range domains {
		if strings.EqualFold(u.Hostname(), d) || strings.HasSuffix(strings.ToLower(u.Hostname()), "."+d) {
			return true
		}
	}
	return false
}

// Fetch images into disposable storage; signed upstream URLs never enter the
// discovery snapshot, bookmarks or the DOM. Only generated file IDs persist.
func cacheDiscoveryCovers(root, provider string, raw, clean []remoteResource) {
	dir := filepath.Join(root, "discovery-covers")
	if os.MkdirAll(dir, 0700) != nil {
		return
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if discoveryCoverPattern.MatchString(e.Name()) {
			if i, err := e.Info(); err == nil && time.Since(i.ModTime()) > time.Hour {
				_ = os.Remove(filepath.Join(dir, e.Name()))
			}
		}
	}
	urls := map[string]string{}
	for _, r := range raw {
		p, _, canonical, err := canonicalRemoteReference(r.SourceURL)
		if err == nil && p == provider && discoveryCoverAllowed(provider, r.PreviewURL) {
			urls[canonical] = r.PreviewURL
		}
	}
	var wg sync.WaitGroup
	slots := make(chan struct{}, 6)
	for i := range clean {
		if urls[clean[i].SourceURL] == "" {
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			upstream := urls[clean[i].SourceURL]
			if mediadownload.ValidateURL(upstream) != nil {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, "GET", upstream, nil)
			if err != nil {
				return
			}
			req.Header.Set("Referer", clean[i].SourceURL)
			client := http.Client{Timeout: 4 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) > 3 || !discoveryCoverAllowed(provider, req.URL.String()) {
					return http.ErrUseLastResponse
				}
				return mediadownload.ValidateURL(req.URL.String())
			}}
			res, err := client.Do(req)
			if err != nil {
				return
			}
			defer res.Body.Close()
			if res.StatusCode != 200 {
				return
			}
			body, err := io.ReadAll(io.LimitReader(res.Body, (512<<10)+1))
			if err != nil || len(body) > 512<<10 {
				return
			}
			mime := http.DetectContentType(body)
			if mime != "image/jpeg" && mime != "image/png" && mime != "image/webp" {
				return
			}
			hash := sha256.Sum256(body)
			id := hex.EncodeToString(hash[:]) + ".jpg"
			if os.WriteFile(filepath.Join(dir, id), body, 0600) == nil {
				clean[i].CoverID = id
			}
		}(i)
	}
	wg.Wait()
}

func hydrateDiscoveryCovers(items []remoteResource) []remoteResource {
	root, err := pluginDataRoot()
	if err != nil {
		return items
	}
	out := append([]remoteResource(nil), items...)
	for i := range out {
		if discoveryCoverPattern.MatchString(out[i].CoverID) {
			path := filepath.Join(root, "discovery-covers", out[i].CoverID)
			if _, err := os.Stat(path); err == nil {
				out[i].PreviewURL = signedStreamURL(path)
			}
		}
	}
	return out
}
