package mediadownload

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const connectionRequired = "site_connection_required: 请先连接抖音，连接后会继续解析。"
const connectionExpired = "site_connection_expired: 抖音连接已过期，请重新连接。"
const connectionChanged = "site_connection_changed: 平台账号已切换，请确认使用当前账号后重试。"
const douyinExtractorUnavailable = "抖音已连接，但站点未向下载器返回视频详情。登录状态已保留；当前下载器尚不能解析此视频，请稍后重试。"

var sessionIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

type SiteStatus struct {
	Site        string `json:"site"`
	State       string `json:"state"`
	Message     string `json:"message"`
	ConnectedAt string `json:"connected_at,omitempty"`
	Attempt     string `json:"attempt,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
	Generation  string `json:"generation,omitempty"`
}
type siteCookie struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	Domain  string `json:"domain"`
	Path    string `json:"path"`
	Secure  bool   `json:"secure"`
	Expires int64  `json:"expires"`
}
type siteSession struct {
	Site        string       `json:"site"`
	ConnectedAt string       `json:"connected_at"`
	Cookies     []siteCookie `json:"cookies"`
}

// Always use the stable Agent-injected plugin directory. Never silently save
// credentials in an immutable version slot or a workspace fallback.
func sessionRoot() (string, error) {
	root, err := pluginDataRoot()
	if err != nil {
		return "", err
	}
	root = filepath.Join(root, "site-sessions", "douyin")
	if err = privateSessionDirectory(root); err != nil {
		return "", fmt.Errorf("站点会话目录不可用，请检查本机权限: %w", err)
	}
	return root, nil
}
func withSessionLock(action func(string) error) error {
	root, err := sessionRoot()
	if err != nil {
		return err
	}
	release, err := lockSession(filepath.Join(root, ".lock"))
	if err != nil {
		return err
	}
	defer release()
	return action(root)
}
func readSessionID(root, name string) string {
	data, _ := os.ReadFile(filepath.Join(root, name))
	id := strings.TrimSpace(string(data))
	if !sessionIDPattern.MatchString(id) {
		return ""
	}
	return id
}
func readSiteSession(root string) (siteSession, error) {
	var session siteSession
	id := readSessionID(root, "active")
	if id == "" {
		return session, errors.New(connectionRequired)
	}
	data, err := os.ReadFile(filepath.Join(root, "session-"+id+".bin"))
	if err != nil || len(data) > 1<<20 {
		return session, errors.New(connectionExpired)
	}
	decoded, err := unsealSession(data)
	if err != nil {
		return session, errors.New(connectionExpired)
	}
	defer clear(decoded)
	if json.Unmarshal(decoded, &session) != nil || session.Site != "douyin" || len(session.Cookies) == 0 || len(session.Cookies) > 500 {
		return siteSession{}, errors.New(connectionExpired)
	}
	connected, err := time.Parse(time.RFC3339Nano, session.ConnectedAt)
	if err != nil || time.Since(connected) > 7*24*time.Hour {
		return siteSession{}, errors.New(connectionExpired)
	}
	return session, nil
}
func connectionState(root string) SiteStatus {
	result := SiteStatus{Site: "douyin", State: "disconnected", Message: "连接后自动复用，仅保存在本机。"}
	attempt := readSessionID(root, "attempt")
	if data, err := os.ReadFile(filepath.Join(root, "connection.json")); err == nil && len(data) < 8192 {
		var current SiteStatus
		if json.Unmarshal(data, &current) == nil && current.Attempt == attempt && attempt != "" {
			updated, _ := time.Parse(time.RFC3339Nano, current.UpdatedAt)
			if current.State == "connecting" && time.Since(updated) < 11*time.Minute {
				pidBytes, _ := os.ReadFile(filepath.Join(root, "browser.pid"))
				pid, _ := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
				if time.Since(updated) > 4*time.Second && pid > 0 && !sessionProcessAlive(pid) {
					return SiteStatus{Site: "douyin", State: "failed", Message: "连接窗口已关闭，请重新连接。"}
				}
				current.Site = "douyin"
				return current
			}
			if current.State == "connected" {
				if _, err := os.Stat(filepath.Join(root, "session-"+attempt+".bin")); err == nil && readSessionID(root, "active") != attempt {
					_ = os.WriteFile(filepath.Join(root, "active"), []byte(attempt), 0600)
					_ = os.Remove(filepath.Join(root, "expired"))
				}
			} else if current.State == "failed" || current.State == "canceled" {
				current.Site = "douyin"
				return current
			}
		}
	}
	if session, err := readSiteSession(root); err == nil {
		result.State = "connected"
		result.Generation = readSessionID(root, "active")
		result.ConnectedAt = session.ConnectedAt
		result.Message = "已连接，解析和下载自动复用。"
		if sessionMarkedExpired(root) {
			result.State = "expired"
			result.Generation = ""
			result.Message = "连接已过期，请重新连接。"
		}
	} else if strings.Contains(err.Error(), "site_connection_expired") {
		result.State = "expired"
		result.Message = "连接已过期，请重新连接。"
	}
	return result
}
func siteStatus() (SiteStatus, error) {
	var result SiteStatus
	err := withSessionLock(func(root string) error {
		cleanupOrphanJars(root)
		result = connectionState(root)
		cleanupOldSessions(root)
		return nil
	})
	return result, err
}
func connectSite() (SiteStatus, error) {
	var result SiteStatus
	err := withSessionLock(func(root string) error {
		current := connectionState(root)
		if current.State == "connecting" {
			result = current
			return nil
		}
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		helper := filepath.Join(filepath.Dir(executable), "session-browser", "media-session-browser.exe")
		if _, err = os.Stat(helper); err != nil {
			return errors.New("站点连接组件缺失，请重新安装当前媒体扩展")
		}
		id := make([]byte, 16)
		if _, err = rand.Read(id); err != nil {
			return err
		}
		attempt := hex.EncodeToString(id)
		result = SiteStatus{Site: "douyin", State: "connecting", Message: "请在打开的抖音窗口中完成连接。", Attempt: attempt, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		if err = os.WriteFile(filepath.Join(root, "attempt"), []byte(attempt), 0600); err != nil {
			return err
		}
		encoded, _ := json.Marshal(result)
		if err = os.WriteFile(filepath.Join(root, "connection.json"), encoded, 0600); err != nil {
			return err
		}
		cmd := exec.Command(helper, root, attempt)
		cmd.Env = cleanChildEnvironment()
		if err = cmd.Start(); err != nil {
			_ = os.Remove(filepath.Join(root, "attempt"))
			return errors.New("无法打开站点连接窗口，请重试")
		}
		_ = os.WriteFile(filepath.Join(root, "browser.pid"), []byte(strconv.Itoa(cmd.Process.Pid)), 0600)
		_ = cmd.Process.Release()
		return nil
	})
	return result, err
}
func disconnectSite() (SiteStatus, error) {
	err := withSessionLock(func(root string) error {
		// Invalidate first. A late browser completion cannot make a session active.
		for _, name := range []string{"attempt", "active", "expired", "connection.json", "browser.pid"} {
			if err := os.Remove(filepath.Join(root, name)); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		files, _ := filepath.Glob(filepath.Join(root, "session-*.bin"))
		for _, path := range files {
			if err := os.Remove(path); err != nil {
				return err
			}
		}
		// This is the plugin's isolated browser profile, never a system browser.
		// The window observes attempt revocation and clears its transient profile.
		cleanupOrphanJars(root)
		return nil
	})
	return SiteStatus{Site: "douyin", State: "disconnected", Message: "已断开，本机保存的会话已清除。"}, err
}
func expireSite(lease string) {
	_ = withSessionLock(func(root string) error {
		if !sessionIDPattern.MatchString(lease) || readSessionID(root, "active") != lease {
			return nil
		}
		return os.WriteFile(filepath.Join(root, "expired"), []byte("login_required"), 0600)
	})
}

func sessionMarkedExpired(root string) bool {
	data, err := os.ReadFile(filepath.Join(root, "expired"))
	if err != nil {
		return false
	}
	// 0.2.6 also marked generic extractor rejection as expiry. Retry those
	// sessions once without making the user authenticate again. Explicit login
	// errors in this version use a distinct marker and remain expired.
	if strings.TrimSpace(string(data)) == "1" {
		_ = os.Remove(filepath.Join(root, "expired"))
		return false
	}
	return true
}
func douyinURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	return h == "douyin.com" || strings.HasSuffix(h, ".douyin.com")
}

func cookieJar(session siteSession, now time.Time) ([]byte, error) {
	var b strings.Builder
	b.WriteString("# Netscape HTTP Cookie File\n")
	count := 0
	for _, c := range session.Cookies {
		domain := strings.ToLower(strings.TrimPrefix(c.Domain, "."))
		if domain != "douyin.com" && !strings.HasSuffix(domain, ".douyin.com") {
			continue
		}
		if c.Name == "" || strings.ContainsAny(c.Name+c.Value+c.Domain+c.Path, "\r\n\t\x00") || strings.ContainsAny(c.Name, ";= ") {
			continue
		}
		if c.Expires > 0 && c.Expires <= now.Unix() {
			continue
		}
		path := c.Path
		if !strings.HasPrefix(path, "/") {
			path = "/"
		}
		subdomains := "FALSE"
		if strings.HasPrefix(c.Domain, ".") {
			subdomains = "TRUE"
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\tTRUE\t%d\t%s\t%s\n", c.Domain, subdomains, path, c.Expires, c.Name, c.Value)
		count++
	}
	if count == 0 {
		return nil, errors.New(connectionExpired)
	}
	return []byte(b.String()), nil
}

// Cookie values are never arguments or task fields. Each process, including a
// detached worker, decrypts the current active session independently. Short-lived
// jars are held in an ACL-restricted per-process directory and always removed.
func applySiteSession(raw string, args []string) ([]string, func(), bool, error) {
	return applySiteSessionGeneration(raw, args, "")
}

func applySiteSessionGeneration(raw string, args []string, expected string) ([]string, func(), bool, error) {
	cleanup := func() {}
	if !douyinURL(raw) {
		return args, cleanup, false, nil
	}
	var jarPath string
	var adapterRoot string
	err := withSessionLock(func(root string) error {
		_ = connectionState(root)
		if sessionMarkedExpired(root) {
			return errors.New(connectionExpired)
		}
		session, err := readSiteSession(root)
		if err != nil {
			return err
		}
		if expected != "" && readSessionID(root, "active") != expected {
			return errors.New(connectionChanged)
		}
		jar, err := cookieJar(session, time.Now())
		if err != nil {
			return err
		}
		defer clear(jar)
		// Stale jars from crashed processes are cleaned without touching live workers.
		cleanupOrphanJars(root)
		dir, err := os.MkdirTemp(root, "run-"+strconv.Itoa(os.Getpid())+"-")
		if err != nil {
			return err
		}
		cleanup = func() { _ = os.RemoveAll(dir) }
		jarPath = filepath.Join(dir, "cookies-"+readSessionID(root, "active")+".txt")
		adapterRoot, err = installDouyinAdapter(dir)
		if err != nil {
			return err
		}
		return os.WriteFile(jarPath, jar, 0600)
	})
	if err != nil {
		cleanup()
		return nil, func() {}, false, err
	}
	filtered := make([]string, 0, len(args)+2)
	for _, arg := range args {
		if arg != "--no-cookies" && arg != "--write-info-json" {
			filtered = append(filtered, arg)
		}
	}
	// Prevent downloaded metadata from serializing extractor HTTP headers.
	filtered = append([]string{"--cookies", jarPath, "--no-write-info-json", "--no-plugin-dirs", "--plugin-dirs", adapterRoot}, filtered...)
	return filtered, cleanup, true, nil
}
func cleanupOrphanJars(root string) {
	dirs, _ := filepath.Glob(filepath.Join(root, "run-*"))
	profiles, _ := filepath.Glob(filepath.Join(root, "browser-*"))
	dirs = append(dirs, profiles...)
	for _, dir := range dirs {
		parts := strings.Split(filepath.Base(dir), "-")
		if len(parts) < 3 {
			continue
		}
		pid, err := strconv.Atoi(parts[1])
		if err == nil && !sessionProcessAlive(pid) {
			_ = os.RemoveAll(dir)
		}
	}
}
func cleanupOldSessions(root string) {
	active, attempt := readSessionID(root, "active"), readSessionID(root, "attempt")
	paths, _ := filepath.Glob(filepath.Join(root, "session-*.bin"))
	for _, path := range paths {
		id := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "session-"), ".bin")
		if id != active && id != attempt {
			_ = os.Remove(path)
		}
	}
}
func cleanChildEnvironment() []string {
	var result []string
	for _, entry := range os.Environ() {
		name := strings.ToUpper(strings.SplitN(entry, "=", 2)[0])
		if strings.HasPrefix(name, "HIMIND_CREDENTIAL_BROKER_") || strings.HasPrefix(name, "WEBVIEW2_") || name == "HIMIND_MEDIA_STREAM_SECRET" || name == "HIMIND_DOUYIN_DETAIL_FILE" || name == "YTDLP_NO_PLUGINS" || name == "PYTHONPATH" || name == "PYTHONHOME" {
			continue
		}
		result = append(result, entry)
	}
	return result
}
func sessionFailure(err error, detail string, authenticated bool, lease string) error {
	if authenticated {
		if strings.Contains(strings.ToLower(detail), "fresh cookies") {
			return errors.New(douyinExtractorUnavailable)
		}
		if sessionExpiredDetail(detail) {
			expireSite(lease)
			return errors.New(connectionExpired)
		}
		return errors.New("抖音请求失败，请检查网络或稍后重试；连接与下载选项已保留。")
	}
	return err
}
func sessionLease(args []string) string {
	if len(args) < 2 || args[0] != "--cookies" {
		return ""
	}
	return strings.TrimSuffix(strings.TrimPrefix(filepath.Base(args[1]), "cookies-"), ".txt")
}

func sessionExpiredDetail(detail string) bool {
	lower := strings.ToLower(detail)
	return strings.Contains(lower, "login required") || strings.Contains(lower, "sign in")
}

type sessionDiagnostic struct {
	tail        string
	expired     bool
	unsupported bool
}

func (w *sessionDiagnostic) Write(p []byte) (int, error) {
	text := w.tail + string(p)
	if sessionExpiredDetail(text) {
		w.expired = true
	}
	if strings.Contains(strings.ToLower(text), "fresh cookies") {
		w.unsupported = true
	}
	if len(text) > 128 {
		text = text[len(text)-128:]
	}
	w.tail = text
	return len(p), nil
}

// Only numeric progress and known display units may leave an authenticated
// extractor's stdout. Arbitrary lines, headers and signed URLs are discarded.
type sessionProgressWriter struct {
	target io.Writer
	buf    string
}

var speedPattern = regexp.MustCompile(`^[0-9., ]+(?:[KMGTPE]?i?B|bytes)/s$`)
var etaPattern = regexp.MustCompile(`^[0-9: ]{1,24}$`)

func (w *sessionProgressWriter) Write(p []byte) (int, error) {
	w.buf += string(p)
	for {
		index := strings.IndexByte(w.buf, '\n')
		if index < 0 {
			break
		}
		line := w.buf[:index]
		w.buf = w.buf[index+1:]
		percent, downloaded, total, speed, eta, ok := parseProgressLine(line)
		if !ok || math.IsNaN(percent) || math.IsInf(percent, 0) || percent < 0 || percent > 100 || downloaded < 0 || total < 0 {
			continue
		}
		if !speedPattern.MatchString(speed) {
			speed = ""
		}
		if !etaPattern.MatchString(eta) {
			eta = ""
		}
		_, _ = fmt.Fprintf(w.target, "download:%.2f|%s|%s|%d|%d\n", percent, speed, eta, downloaded, total)
	}
	if len(w.buf) > 64<<10 {
		w.buf = ""
	}
	return len(p), nil
}

func watchSiteRevocation(ctx context.Context, cancel context.CancelFunc, revoked chan<- struct{}, active string) {
	root, err := sessionRoot()
	if err != nil {
		cancel()
		return
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if active == "" || readSessionID(root, "active") != active {
				select {
				case revoked <- struct{}{}:
				default:
				}
				cancel()
				return
			}
		}
	}
}

// The public short URL is resolved only within official Douyin hosts. Cookie is
// never sent by this resolver. All other URL policies remain unchanged.
func normalizeSiteURL(ctx context.Context, raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || !douyinURL(raw) {
		return raw, err
	}
	if u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return "", errors.New("抖音会话仅支持官网 HTTPS 地址")
	}
	u.Scheme = "https"
	raw = u.String()
	if strings.ToLower(u.Hostname()) != "v.douyin.com" {
		return canonicalDouyinVideo(u)
	}
	return resolveDouyinShare(ctx, raw)
}

func resolveDouyinShare(ctx context.Context, raw string) (string, error) {
	client := runtimeHTTPClient()
	client.Timeout = 20 * time.Second
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !douyinURL(req.URL.String()) || req.URL.Scheme != "https" || req.URL.User != nil {
			return errors.New("抖音分享链接跳转不受支持")
		}
		_, err := validateMediaURL(req.URL.String())
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(req)
	if err != nil {
		return "", errors.New("无法展开抖音分享链接，请使用官网视频链接或检查网络")
	}
	defer response.Body.Close()
	final := response.Request.URL
	if final.Scheme != "https" || !douyinURL(final.String()) {
		return "", errors.New("抖音分享链接跳转不受支持")
	}
	return canonicalDouyinVideo(final)
}

func canonicalDouyinVideo(u *url.URL) (string, error) {
	host := strings.ToLower(u.Hostname())
	if host != "www.douyin.com" && host != "douyin.com" && host != "m.douyin.com" {
		return "", errors.New("请使用抖音官网视频链接或分享短链接")
	}
	id := regexp.MustCompile(`^/(?:video|share/video)/([0-9]+)(?:/|$)`).FindStringSubmatch(u.Path)
	if len(id) < 2 {
		videoID := u.Query().Get("modal_id")
		if !regexp.MustCompile(`^[0-9]+$`).MatchString(videoID) {
			return "", errors.New("链接未指向具体视频，请在抖音打开视频后复制链接")
		}
		return "https://www.douyin.com/video/" + videoID, nil
	}
	return "https://www.douyin.com/video/" + id[1], nil
}
