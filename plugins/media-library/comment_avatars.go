package main

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/MrBaoquan/himind-extensions/internal/mediadownload"
)

func cacheCommentAvatars(items []publicComment, raw []rawComment) {
	urls := map[string]string{}
	for _, r := range raw {
		if discoveryCoverAllowed("douyin", r.AvatarURL) {
			urls[stableID("comment", r.Key)] = r.AvatarURL
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4)
	client := http.Client{Timeout: 3 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 2 || !discoveryCoverAllowed("douyin", req.URL.String()) {
			return http.ErrUseLastResponse
		}
		return mediadownload.ValidateURL(req.URL.String())
	}}
	for i := range items[:min(len(items), 30)] {
		item := &items[i]
		upstream := urls[item.ID]
		if upstream == "" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-slots }()
			if mediadownload.ValidateURL(upstream) != nil {
				return
			}
			req, err := http.NewRequestWithContext(ctx, "GET", upstream, nil)
			if err != nil {
				return
			}
			res, err := client.Do(req)
			if err != nil {
				return
			}
			defer res.Body.Close()
			if res.StatusCode != 200 {
				return
			}
			body, err := io.ReadAll(io.LimitReader(res.Body, (24<<10)+1))
			if err != nil || len(body) > 24<<10 {
				return
			}
			mime := http.DetectContentType(body)
			if mime != "image/jpeg" && mime != "image/png" && mime != "image/webp" {
				return
			}
			item.Avatar = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(body)
		}()
	}
	wg.Wait()
}
