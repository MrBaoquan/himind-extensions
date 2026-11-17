package mediadownload

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAuthenticatedOutputCannotPersistSecrets(t *testing.T) {
	var target bytes.Buffer
	writer := &sessionProgressWriter{target: &target}
	for _, chunk := range []string{"Cookie: fixture-secret\n", "download:50.0%|fixture-", "secret|fixture-secret|512|1024\n", "download:NaN|100B/s|00:02|512|1024\n", "download:75.0%|100B/s|00:01|768|1024\n"} {
		_, _ = writer.Write([]byte(chunk))
	}
	if strings.Contains(target.String(), "fixture") || strings.Contains(target.String(), "Cookie") || strings.Contains(target.String(), "NaN") {
		t.Fatal("authenticated output leaked untrusted text")
	}
	if !strings.Contains(target.String(), "75.00|100B/s|00:01|768|1024") {
		t.Fatal("valid progress lost", target.String())
	}
	diagnostic := &sessionDiagnostic{}
	_, _ = diagnostic.Write([]byte("ERROR: Fresh co"))
	_, _ = diagnostic.Write([]byte("okies needed: fixture-secret"))
	if diagnostic.expired || !diagnostic.unsupported {
		t.Fatal("extractor rejection was misclassified as login expiry")
	}
}

func TestDouyinVideoURLNormalization(t *testing.T) {
	for _, raw := range []string{
		"https://www.douyin.com/jingxuan/search/test?modal_id=7702136163577924874&type=general",
		"https://www.douyin.com/user/test?modal_id=7702136163577924874",
		"http://www.douyin.com/video/7702136163577924874?aid=tracking",
		"https://m.douyin.com/share/video/7702136163577924874/",
	} {
		got, err := normalizeSiteURL(context.Background(), raw)
		if err != nil || got != "https://www.douyin.com/video/7702136163577924874" {
			t.Fatalf("normalization: %s %v", got, err)
		}
	}
	for _, raw := range []string{"https://www.douyin.com/jingxuan/search/test", "https://www.douyin.com/?modal_id=x", "https://live.douyin.com/123"} {
		if _, err := normalizeSiteURL(context.Background(), raw); err == nil {
			t.Fatal("non-video accepted", raw)
		}
	}
}

func TestExtractorRejectionDoesNotExpireLogin(t *testing.T) {
	err := sessionFailure(errors.New("raw fixture-secret"), "Fresh cookies (not necessarily logged in) are needed", true, "")
	if err.Error() != douyinExtractorUnavailable || sessionExpiredDetail("Fresh cookies (not necessarily logged in) are needed") {
		t.Fatal("extractor failure requested another login")
	}
	if !sessionExpiredDetail("Login required") {
		t.Fatal("explicit login expiry lost")
	}
}

func TestCookieJarScopesDomainsExpiryAndInjection(t *testing.T) {
	now := time.Now()
	session := siteSession{Cookies: []siteCookie{
		{Name: "guest", Value: "fixture", Domain: ".douyin.com", Path: "/", Expires: now.Unix() + 600},
		{Name: "foreign", Value: "private", Domain: "evil.com", Path: "/"},
		{Name: "suffix", Value: "private", Domain: "douyin.com.evil.com", Path: "/"},
		{Name: "expired", Value: "private", Domain: ".douyin.com", Expires: now.Unix() - 1},
		{Name: "injected", Value: "x\n.evil.com\tTRUE", Domain: ".douyin.com"},
	}}
	data, err := cookieJar(session, now)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), ".douyin.com\tTRUE\t/\tTRUE\t") {
		t.Fatalf("missing secure, scoped cookie: %s", data)
	}
	for _, forbidden := range []string{"foreign", "suffix", "expired", "injected", "private", "evil.com"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("jar contained %s", forbidden)
		}
	}
	if _, err = cookieJar(siteSession{}, now); err == nil {
		t.Fatal("empty cookie jar passed")
	}
}
func TestSessionDoesNotApplyToOtherSites(t *testing.T) {
	for _, raw := range []string{"https://douyin.com.evil.test/video/123", "https://example.com/video", "https://notdouyin.com/video"} {
		args, cleanup, auth, err := applySiteSession(raw, []string{"--no-cookies", raw})
		cleanup()
		if err != nil || auth || args[0] != "--no-cookies" {
			t.Fatalf("unrelated site used session: %s", raw)
		}
	}
}
func TestChildEnvironmentOmitsHostSecrets(t *testing.T) {
	t.Setenv("HIMIND_CREDENTIAL_BROKER_TOKEN", "fixture-token")
	t.Setenv("HIMIND_CREDENTIAL_BROKER_URL", "fixture-url")
	t.Setenv("HIMIND_MEDIA_STREAM_SECRET", "fixture-secret")
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", "fixture-root")
	t.Setenv("WEBVIEW2_USER_DATA_FOLDER", "fixture-agent-browser")
	t.Setenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS", "fixture-debug-port")
	t.Setenv("HIMIND_DOUYIN_DETAIL_FILE", "fixture-stale-detail")
	t.Setenv("YTDLP_NO_PLUGINS", "fixture-disabled-plugins")
	t.Setenv("PYTHONPATH", "fixture-injected-module")
	env := strings.Join(cleanChildEnvironment(), "\n")
	for _, value := range []string{"fixture-token", "fixture-url", "fixture-secret", "fixture-agent-browser", "fixture-debug-port", "fixture-stale-detail", "fixture-disabled-plugins", "fixture-injected-module"} {
		if strings.Contains(env, value) {
			t.Fatal("host secret inherited")
		}
	}
	if !strings.Contains(env, "HIMIND_PLUGIN_DATA_ROOT=fixture-root") {
		t.Fatal("worker data root lost")
	}
}
func TestOnlyOrphanCookieDirectoriesAreCleaned(t *testing.T) {
	root := t.TempDir()
	live := filepath.Join(root, "run-"+strconv.Itoa(os.Getpid())+"-live")
	orphan := filepath.Join(root, "run-2147483647-stale")
	for _, dir := range []string{live, orphan} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	cleanupOrphanJars(root)
	if _, err := os.Stat(live); err != nil {
		t.Fatal("live worker jar removed")
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("orphan jar retained")
	}
}
