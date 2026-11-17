package mediadownload

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
)

// MusicInfo contains public identity only, never a playable/signed media URL.
type MusicInfo struct {
	Provider   string `json:"provider"`
	ProviderID string `json:"provider_id"`
	Title      string `json:"title"`
	Author     string `json:"author,omitempty"`
	SourceURL  string `json:"source_url"`
	Evidence   string `json:"evidence"`
}

func musicFromDouyinDetail(data []byte) *MusicInfo {
	var detail struct {
		Music struct {
			ID       json.RawMessage `json:"id"`
			IDString string          `json:"id_str"`
			Title    string          `json:"title"`
			Author   string          `json:"author"`
		} `json:"music"`
	}
	if json.Unmarshal(data, &detail) != nil {
		return nil
	}
	id := detail.Music.IDString
	if id == "" {
		id = strings.Trim(string(detail.Music.ID), "\"")
	}
	if len(id) < 5 || len(id) > 40 {
		return nil
	}
	for _, c := range id {
		if c < '0' || c > '9' {
			return nil
		}
	}
	title, author := strings.TrimSpace(detail.Music.Title), strings.TrimSpace(detail.Music.Author)
	if len([]rune(title)) > 300 || len([]rune(author)) > 200 {
		return nil
	}
	return &MusicInfo{Provider: "douyin", ProviderID: id, Title: title, Author: author, SourceURL: "https://www.douyin.com/music/" + id, Evidence: "platform_metadata"}
}

func musicFromDetailFile(path string) *MusicInfo {
	if path == "" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() > 8<<20 {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	defer clear(data)
	return musicFromDouyinDetail(data)
}

// ResolveMusic uses the existing connected session and the same controlled
// video-detail adapter as acquisition. It does not fetch or persist audio URLs.
func ResolveMusic(ctx context.Context, rawURL string) (*MusicInfo, error) {
	detail, err := readConnectedDouyinDetail(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	defer clear(detail)
	return musicFromDouyinDetail(detail), nil
}

// Callers project public fields and clear the private response after use.
func readConnectedDouyinDetail(ctx context.Context, rawURL string) ([]byte, error) {
	if err := ValidateURL(rawURL); err != nil {
		return nil, err
	}
	normalized, err := normalizeSiteURL(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(normalized)
	if err != nil || !douyinURL(normalized) {
		return nil, errors.New("当前仅支持识别抖音视频用曲")
	}
	match := douyinVideoPath.FindStringSubmatch(u.Path)
	if len(match) != 2 {
		return nil, errors.New("请使用抖音单个视频链接")
	}
	var session siteSession
	var generation string
	err = withSessionLock(func(root string) error {
		generation = readSessionID(root, "active")
		if generation == "" || sessionMarkedExpired(root) {
			return errors.New(connectionExpired)
		}
		var e error
		session, e = readSiteSession(root)
		return e
	})
	if err != nil {
		return nil, err
	}
	detail, err := fetchDouyinDetail(ctx, runtimeHTTPClient(), session, match[1])
	if err != nil {
		return nil, err
	}
	err = withSessionLock(func(root string) error {
		if readSessionID(root, "active") != generation || sessionMarkedExpired(root) {
			return errors.New(connectionExpired)
		}
		return nil
	})
	if err != nil {
		clear(detail)
		return nil, err
	}
	return detail, nil
}
