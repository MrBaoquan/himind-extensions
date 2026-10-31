package main

// 素材来源适配层。
//
// 每个来源声明凭据需求、支持的媒体类型和统一检索结果。凭据值由 Agent
// Broker 在单次能力调用内提供，来源适配器不直接读取任何长期配置文件。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// httpClient 是来源检索共用的客户端；单次检索另有超时上下文控制总时长。
var httpClient = &http.Client{Timeout: 20 * time.Second}

// assetItem 是跨来源统一的素材形状。视图和落盘都只认这一份字段。
type assetItem struct {
	ID              string   `json:"id"`
	Provider        string   `json:"provider"`
	Kind            string   `json:"kind"`
	Title           string   `json:"title"`
	Author          string   `json:"author"`
	License         string   `json:"license"`
	SourceURL       string   `json:"source_url"`
	PreviewURL      string   `json:"preview_url"`
	DownloadURL     string   `json:"download_url"`
	Width           int      `json:"width,omitempty"`
	Height          int      `json:"height,omitempty"`
	DurationSeconds float64  `json:"duration_seconds,omitempty"`
	Tags            []string `json:"tags,omitempty"`
}

// searchQuery 是一次检索的全部条件。来源不认识的字段自己忽略。
type searchQuery struct {
	Text        string
	Kinds       []string
	PerSource   int
	Page        int
	Orientation string
	MinWidth    int
	MinDuration float64
}

type mediaProvider interface {
	ID() string
	Name() string
	Kinds() []string
	CredentialID() string
	Requirement() string
	Search(ctx context.Context, credential map[string]string, query searchQuery) ([]assetItem, error)
}

// providerRegistry 是全部已知来源。顺序即视图里的展示顺序。
func providerRegistry() []mediaProvider {
	return []mediaProvider{
		newPexelsProvider(),
		newUnsplashProvider(),
		newFreesoundProvider(),
		newFakeProvider(),
	}
}

func providerByID(id string) (mediaProvider, bool) {
	for _, item := range providerRegistry() {
		if item.ID() == id {
			return item, true
		}
	}
	return nil, false
}

func kindRequested(kinds []string, kind string) bool {
	if len(kinds) == 0 {
		return true
	}
	for _, item := range kinds {
		if item == kind {
			return true
		}
	}
	return false
}

func providerSupports(item mediaProvider, kinds []string) bool {
	for _, kind := range item.Kinds() {
		if kindRequested(kinds, kind) {
			return true
		}
	}
	return false
}

func getJSON(ctx context.Context, request *http.Request, target any) error {
	request = request.WithContext(ctx)
	response, err := httpClient.Do(request)
	if err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return fmt.Errorf("来源连接超时，请稍后重试")
		}
		return fmt.Errorf("无法连接来源，请检查网络或来源状态")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return fmt.Errorf("来源拒绝了这次请求（HTTP %d），请检查密钥或配额", response.StatusCode)
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("来源限流（HTTP 429），请稍后重试")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("来源返回 HTTP %d", response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 8<<20))
	return decoder.Decode(target)
}

func clampPerSource(value int) int {
	if value <= 0 {
		return 12
	}
	if value > 40 {
		return 40
	}
	return value
}

func clampPage(value int) int {
	if value <= 0 {
		return 1
	}
	return value
}

func cleanText(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func truncate(value string, limit int) string {
	value = cleanText(value)
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

// ---------------------------------------------------------------------------
// Pexels：图片 + 视频，凭据 ID 为 pexels.api。
// ---------------------------------------------------------------------------

type pexelsProvider struct{}

func newPexelsProvider() pexelsProvider { return pexelsProvider{} }

func (pexelsProvider) ID() string   { return "pexels" }
func (pexelsProvider) Name() string { return "Pexels" }
func (pexelsProvider) Kinds() []string {
	return []string{"image", "video"}
}
func (pexelsProvider) CredentialID() string { return "pexels.api" }
func (pexelsProvider) Requirement() string {
	return "Pexels API Key（请在 HiMind Agent 的账号与凭据中配置）"
}

type pexelsPhotoResponse struct {
	Photos []struct {
		ID           int    `json:"id"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		URL          string `json:"url"`
		Photographer string `json:"photographer"`
		Alt          string `json:"alt"`
		Src          struct {
			Original string `json:"original"`
			Large    string `json:"large"`
			Medium   string `json:"medium"`
		} `json:"src"`
	} `json:"photos"`
}

type pexelsVideoResponse struct {
	Videos []struct {
		ID       int    `json:"id"`
		Width    int    `json:"width"`
		Height   int    `json:"height"`
		URL      string `json:"url"`
		Image    string `json:"image"`
		Duration int    `json:"duration"`
		User     struct {
			Name string `json:"name"`
		} `json:"user"`
		VideoFiles []struct {
			Link     string `json:"link"`
			Width    int    `json:"width"`
			Height   int    `json:"height"`
			FileType string `json:"file_type"`
		} `json:"video_files"`
	} `json:"videos"`
}

func (provider pexelsProvider) Search(ctx context.Context, credential map[string]string, query searchQuery) ([]assetItem, error) {
	key := strings.TrimSpace(credential["api_key"])
	if key == "" {
		return nil, fmt.Errorf("Pexels 凭据缺少 api_key 字段")
	}
	items := []assetItem{}
	if kindRequested(query.Kinds, "image") {
		found, err := provider.searchPhotos(ctx, key, query)
		if err != nil {
			return items, err
		}
		items = append(items, found...)
	}
	if kindRequested(query.Kinds, "video") {
		found, err := provider.searchVideos(ctx, key, query)
		if err != nil {
			return items, err
		}
		items = append(items, found...)
	}
	return items, nil
}

func (pexelsProvider) searchPhotos(ctx context.Context, key string, query searchQuery) ([]assetItem, error) {
	params := url.Values{}
	params.Set("query", query.Text)
	params.Set("per_page", strconv.Itoa(clampPerSource(query.PerSource)))
	params.Set("page", strconv.Itoa(clampPage(query.Page)))
	if query.Orientation != "" {
		params.Set("orientation", query.Orientation)
	}
	request, err := http.NewRequest(http.MethodGet, "https://api.pexels.com/v1/search?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", key)
	var payload pexelsPhotoResponse
	if err := getJSON(ctx, request, &payload); err != nil {
		return nil, err
	}
	items := make([]assetItem, 0, len(payload.Photos))
	for _, photo := range payload.Photos {
		if query.MinWidth > 0 && photo.Width < query.MinWidth {
			continue
		}
		download := photo.Src.Original
		if download == "" {
			download = photo.Src.Large
		}
		items = append(items, assetItem{
			ID:          strconv.Itoa(photo.ID),
			Provider:    "pexels",
			Kind:        "image",
			Title:       truncate(photo.Alt, 160),
			Author:      cleanText(photo.Photographer),
			License:     "Pexels License（免费商用，无需署名，不得转售原始素材）",
			SourceURL:   photo.URL,
			PreviewURL:  photo.Src.Medium,
			DownloadURL: download,
			Width:       photo.Width,
			Height:      photo.Height,
		})
	}
	return items, nil
}

func (pexelsProvider) searchVideos(ctx context.Context, key string, query searchQuery) ([]assetItem, error) {
	params := url.Values{}
	params.Set("query", query.Text)
	params.Set("per_page", strconv.Itoa(clampPerSource(query.PerSource)))
	params.Set("page", strconv.Itoa(clampPage(query.Page)))
	if query.Orientation != "" {
		params.Set("orientation", query.Orientation)
	}
	request, err := http.NewRequest(http.MethodGet, "https://api.pexels.com/videos/search?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", key)
	var payload pexelsVideoResponse
	if err := getJSON(ctx, request, &payload); err != nil {
		return nil, err
	}
	items := make([]assetItem, 0, len(payload.Videos))
	for _, video := range payload.Videos {
		if query.MinWidth > 0 && video.Width < query.MinWidth {
			continue
		}
		if query.MinDuration > 0 && float64(video.Duration) < query.MinDuration {
			continue
		}
		download := ""
		best := -1
		for _, file := range video.VideoFiles {
			if !strings.Contains(strings.ToLower(file.FileType), "mp4") {
				continue
			}
			if file.Width > best {
				best = file.Width
				download = file.Link
			}
		}
		if download == "" && len(video.VideoFiles) > 0 {
			download = video.VideoFiles[0].Link
		}
		items = append(items, assetItem{
			ID:              strconv.Itoa(video.ID),
			Provider:        "pexels",
			Kind:            "video",
			Title:           "Pexels 视频 " + strconv.Itoa(video.ID),
			Author:          cleanText(video.User.Name),
			License:         "Pexels License（免费商用，无需署名，不得转售原始素材）",
			SourceURL:       video.URL,
			PreviewURL:      video.Image,
			DownloadURL:     download,
			Width:           video.Width,
			Height:          video.Height,
			DurationSeconds: float64(video.Duration),
		})
	}
	return items, nil
}

// ---------------------------------------------------------------------------
// Unsplash：图片，凭据 ID 为 unsplash.api。
// ---------------------------------------------------------------------------

type unsplashProvider struct{}

func newUnsplashProvider() unsplashProvider { return unsplashProvider{} }

func (unsplashProvider) ID() string   { return "unsplash" }
func (unsplashProvider) Name() string { return "Unsplash" }
func (unsplashProvider) Kinds() []string {
	return []string{"image"}
}
func (unsplashProvider) CredentialID() string { return "unsplash.api" }
func (unsplashProvider) Requirement() string {
	return "Unsplash Access Key（请在 HiMind Agent 的账号与凭据中配置）"
}

func (provider unsplashProvider) Search(ctx context.Context, credential map[string]string, query searchQuery) ([]assetItem, error) {
	if !kindRequested(query.Kinds, "image") {
		return nil, nil
	}
	key := strings.TrimSpace(credential["access_key"])
	if key == "" {
		return nil, fmt.Errorf("Unsplash 凭据缺少 access_key 字段")
	}
	params := url.Values{}
	params.Set("query", query.Text)
	params.Set("per_page", strconv.Itoa(clampPerSource(query.PerSource)))
	params.Set("page", strconv.Itoa(clampPage(query.Page)))
	if query.Orientation != "" {
		params.Set("orientation", query.Orientation)
	}
	request, err := http.NewRequest(http.MethodGet, "https://api.unsplash.com/search/photos?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Client-ID "+key)
	request.Header.Set("Accept-Version", "v1")
	var payload struct {
		Results []struct {
			ID          string `json:"id"`
			Description string `json:"description"`
			Alt         string `json:"alt_description"`
			Width       int    `json:"width"`
			Height      int    `json:"height"`
			Links       struct {
				HTML string `json:"html"`
			} `json:"links"`
			User struct {
				Name string `json:"name"`
			} `json:"user"`
			URLs struct {
				Raw     string `json:"raw"`
				Regular string `json:"regular"`
				Small   string `json:"small"`
			} `json:"urls"`
		} `json:"results"`
	}
	if err := getJSON(ctx, request, &payload); err != nil {
		return nil, err
	}
	items := make([]assetItem, 0, len(payload.Results))
	for _, photo := range payload.Results {
		if query.MinWidth > 0 && photo.Width < query.MinWidth {
			continue
		}
		title := photo.Description
		if strings.TrimSpace(title) == "" {
			title = photo.Alt
		}
		download := photo.URLs.Raw
		if download == "" {
			download = photo.URLs.Regular
		}
		items = append(items, assetItem{
			ID:          photo.ID,
			Provider:    "unsplash",
			Kind:        "image",
			Title:       truncate(title, 160),
			Author:      cleanText(photo.User.Name),
			License:     "Unsplash License（免费商用；按 API 指引需保留来源与作者）",
			SourceURL:   photo.Links.HTML,
			PreviewURL:  photo.URLs.Small,
			DownloadURL: download,
			Width:       photo.Width,
			Height:      photo.Height,
		})
	}
	return items, nil
}

// ---------------------------------------------------------------------------
// Freesound：音频，凭据 ID 为 freesound.api。
// ---------------------------------------------------------------------------

type freesoundProvider struct{}

func newFreesoundProvider() freesoundProvider { return freesoundProvider{} }

func (freesoundProvider) ID() string   { return "freesound" }
func (freesoundProvider) Name() string { return "Freesound" }
func (freesoundProvider) Kinds() []string {
	return []string{"audio"}
}
func (freesoundProvider) CredentialID() string { return "freesound.api" }
func (freesoundProvider) Requirement() string {
	return "Freesound API Token（请在 HiMind Agent 的账号与凭据中配置）"
}

func (provider freesoundProvider) Search(ctx context.Context, credential map[string]string, query searchQuery) ([]assetItem, error) {
	if !kindRequested(query.Kinds, "audio") {
		return nil, nil
	}
	token := strings.TrimSpace(credential["token"])
	if token == "" {
		return nil, fmt.Errorf("Freesound 凭据缺少 token 字段")
	}
	params := url.Values{}
	params.Set("query", query.Text)
	params.Set("page_size", strconv.Itoa(clampPerSource(query.PerSource)))
	params.Set("page", strconv.Itoa(clampPage(query.Page)))
	params.Set("fields", "id,name,username,license,url,duration,previews,images,tags")
	params.Set("token", token)
	request, err := http.NewRequest(http.MethodGet, "https://freesound.org/apiv2/search/text/?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Results []struct {
			ID       int      `json:"id"`
			Name     string   `json:"name"`
			Username string   `json:"username"`
			License  string   `json:"license"`
			URL      string   `json:"url"`
			Duration float64  `json:"duration"`
			Tags     []string `json:"tags"`
			Previews struct {
				HQMP3 string `json:"preview-hq-mp3"`
				LQMP3 string `json:"preview-lq-mp3"`
			} `json:"previews"`
			Images struct {
				Waveform string `json:"waveform_m"`
			} `json:"images"`
		} `json:"results"`
	}
	if err := getJSON(ctx, request, &payload); err != nil {
		return nil, err
	}
	items := make([]assetItem, 0, len(payload.Results))
	for _, sound := range payload.Results {
		if query.MinDuration > 0 && sound.Duration < query.MinDuration {
			continue
		}
		download := sound.Previews.HQMP3
		if download == "" {
			download = sound.Previews.LQMP3
		}
		// 音频用波形图当缩略图，避免视图把 mp3 当图片渲染。
		preview := sound.Images.Waveform
		items = append(items, assetItem{
			ID:              strconv.Itoa(sound.ID),
			Provider:        "freesound",
			Kind:            "audio",
			Title:           truncate(sound.Name, 160),
			Author:          cleanText(sound.Username),
			License:         cleanText(sound.License),
			SourceURL:       sound.URL,
			PreviewURL:      preview,
			DownloadURL:     download,
			DurationSeconds: sound.Duration,
			Tags:            sound.Tags,
		})
	}
	return items, nil
}

// ---------------------------------------------------------------------------
// fake：离线确定性来源，只在 HIMIND_MEDIA_LIBRARY_FAKE=1 时出现。
// 它让测试、演示和视图联调不依赖外部密钥与网络。
// ---------------------------------------------------------------------------

type fakeProvider struct{}

func newFakeProvider() fakeProvider { return fakeProvider{} }

func (fakeProvider) ID() string   { return "fake" }
func (fakeProvider) Name() string { return "样例素材（离线）" }
func (fakeProvider) Kinds() []string {
	return []string{"image", "video", "audio"}
}
func (fakeProvider) CredentialID() string { return "" }
func (fakeProvider) Requirement() string {
	return "内置离线来源，仅用于测试与演示"
}

func (fakeProvider) Search(_ context.Context, _ map[string]string, query searchQuery) ([]assetItem, error) {
	items := []assetItem{}
	catalog := []assetItem{
		{ID: "sample-image", Kind: "image", Title: "样例图片 " + query.Text, PreviewURL: "https://example.invalid/sample.png", DownloadURL: "https://example.invalid/sample.png", Width: 1920, Height: 1080},
		{ID: "sample-video", Kind: "video", Title: "样例视频 " + query.Text, PreviewURL: "https://example.invalid/sample.jpg", DownloadURL: "https://example.invalid/sample.mp4", Width: 1920, Height: 1080, DurationSeconds: 6},
		{ID: "sample-audio", Kind: "audio", Title: "样例音频 " + query.Text, DownloadURL: "https://example.invalid/sample.mp3", DurationSeconds: 3},
	}
	for _, item := range catalog {
		if !kindRequested(query.Kinds, item.Kind) {
			continue
		}
		item.Provider = "fake"
		item.Author = "样例"
		item.License = "样例数据，仅用于联调"
		item.SourceURL = "https://example.invalid/"
		items = append(items, item)
	}
	sort.SliceStable(items, func(left, right int) bool { return items[left].ID < items[right].ID })
	return items, nil
}
