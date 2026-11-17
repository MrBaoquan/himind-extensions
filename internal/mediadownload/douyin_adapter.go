package mediadownload

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// The pinned standalone yt-dlp embeds Python and supports extractor plugins;
// no additional Python runtime or user-installed code is required.
//
//go:embed tools/ytdlp-plugins/himind/yt_dlp_plugins/extractor/himind_douyin.py
var douyinAdapter []byte

func installDouyinAdapter(runDirectory string) (string, error) {
	root := filepath.Join(runDirectory, "adapters")
	module := filepath.Join(root, "himind", "yt_dlp_plugins", "extractor", "himind_douyin.py")
	if err := os.MkdirAll(filepath.Dir(module), 0700); err != nil {
		return "", err
	}
	if err := os.WriteFile(module, douyinAdapter, 0600); err != nil {
		return "", err
	}
	return root, nil
}

const douyinUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 Edg/130.0.0.0"

var douyinVideoPath = regexp.MustCompile(`^/video/([0-9]+)/?$`)

func douyinCookieDomain(domain string) bool {
	domain = strings.ToLower(strings.TrimPrefix(domain, "."))
	return domain == "douyin.com" || strings.HasSuffix(domain, ".douyin.com")
}

// Fetch through the desktop client's network stack. The frozen yt-dlp HTTP
// transport is rejected by Douyin even with the same headers and session.
// Only this official API gets the gateway identity; media downloads never do.
func fetchDouyinDetail(ctx context.Context, client *http.Client, session siteSession, videoID string) ([]byte, error) {
	base, _ := url.Parse("https://www.douyin.com/")
	jar, _ := cookiejar.New(nil)
	for _, c := range session.Cookies {
		if !douyinCookieDomain(c.Domain) || c.Expires > 0 && c.Expires <= time.Now().Unix() || c.Name == "" || strings.ContainsAny(c.Name+c.Value+c.Domain+c.Path, "\r\n\t\x00") || strings.ContainsAny(c.Name, ";= ") {
			continue
		}
		cookiePath := c.Path
		if !strings.HasPrefix(cookiePath, "/") {
			cookiePath = "/"
		}
		jar.SetCookies(base, []*http.Cookie{{Name: c.Name, Value: c.Value, Domain: c.Domain, Path: cookiePath, Secure: true}})
	}
	identity := ""
	for _, c := range jar.Cookies(base) {
		if c.Name == "UIFID" || c.Name == "UIFID_TEMP" && identity == "" {
			identity = c.Value
		}
	}
	local := *client
	local.Jar = jar
	local.Timeout = 25 * time.Second
	// Never forward the scoped identity or Cookie to redirects.
	local.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String()+"aweme/v1/web/aweme/detail/?aweme_id="+url.QueryEscape(videoID), nil)
	if err != nil {
		return nil, errors.New("抖音详情请求无效")
	}
	req.Header.Set("User-Agent", douyinUserAgent)
	req.Header.Set("Referer", base.String())
	req.Header.Set("x-tt-argus", "1")
	if identity != "" {
		req.Header.Set("uifid", identity)
	}
	res, err := local.Do(req)
	if err != nil {
		return nil, errors.New("抖音视频详情请求失败，请检查网络后重试。")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, errors.New(douyinExtractorUnavailable)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, (8<<20)+1))
	if err != nil || len(body) > 8<<20 {
		return nil, errors.New("抖音视频详情读取失败，请稍后重试。")
	}
	defer clear(body)
	var response struct {
		Detail json.RawMessage `json:"aweme_detail"`
	}
	if json.Unmarshal(body, &response) != nil || !validDouyinDetail(response.Detail, videoID) {
		return nil, errors.New(douyinExtractorUnavailable)
	}
	return response.Detail, nil
}

func validDouyinDetail(data []byte, videoID string) bool {
	var detail struct {
		ID    string                     `json:"aweme_id"`
		Video map[string]json.RawMessage `json:"video"`
	}
	return json.Unmarshal(data, &detail) == nil && detail.ID == videoID && len(detail.Video) > 0
}

// Only a private file path crosses the process boundary. Signed media URLs stay
// in this ACL-restricted run directory until the invocation/worker cleans it.
func prepareDouyinDetail(ctx context.Context, normalized string, args []string) (string, error) {
	if !douyinURL(normalized) || sessionLease(args) == "" {
		return "", nil
	}
	u, err := url.Parse(normalized)
	if err != nil {
		return "", errors.New("抖音视频链接无效")
	}
	match := douyinVideoPath.FindStringSubmatch(u.Path)
	if len(match) != 2 {
		return "", errors.New("请使用抖音单个视频链接，暂不支持主页列表、图文或合集。")
	}
	var session siteSession
	err = withSessionLock(func(root string) error {
		if readSessionID(root, "active") != sessionLease(args) || sessionMarkedExpired(root) {
			return errors.New(connectionExpired)
		}
		var readErr error
		session, readErr = readSiteSession(root)
		return readErr
	})
	if err != nil {
		return "", err
	}
	detail, err := fetchDouyinDetail(ctx, runtimeHTTPClient(), session, match[1])
	if err != nil {
		return "", err
	}
	defer clear(detail)
	file := filepath.Join(filepath.Dir(args[1]), "detail.json")
	err = withSessionLock(func(root string) error {
		if readSessionID(root, "active") != sessionLease(args) || sessionMarkedExpired(root) {
			return errors.New(connectionExpired)
		}
		return os.WriteFile(file, detail, 0600)
	})
	if err != nil {
		return "", err
	}
	return file, nil
}
