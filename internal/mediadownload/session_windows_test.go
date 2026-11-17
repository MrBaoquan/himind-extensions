//go:build windows

package mediadownload

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestDisconnectStopsAuthenticatedWorker(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	root := installFixtureSession(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	revoked := make(chan struct{}, 1)
	go watchSiteRevocation(ctx, cancel, revoked, readSessionID(root, "active"))
	if _, err := disconnectSite(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("revoked worker kept running")
	}
	select {
	case <-revoked:
	default:
		t.Fatal("revocation reason missing")
	}
}

func TestProductSessionIdentityAndGenerationAreIsolated(t *testing.T) {
	original := currentProductConfig()
	t.Cleanup(func() { _ = Configure(original) })
	rootPath := t.TempDir()
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", rootPath)
	if err := Configure(Config{PluginID: "com.himind.media-downloader"}); err != nil {
		t.Fatal(err)
	}
	root := installFixtureSession(t)
	oldData, err := os.ReadFile(filepath.Join(root, "session-0123456789abcdef0123456789abcdef.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err = Configure(Config{PluginID: "com.himind.media-library", DataRoot: rootPath, StrictDataRoot: true}); err != nil {
		t.Fatal(err)
	}
	if data, err := unsealSession(oldData); err == nil {
		clear(data)
		t.Fatal("media-library decrypted legacy product fixture")
	}
	_ = installFixtureSession(t)
	status, err := siteStatus()
	if err != nil || status.State != "connected" || status.Generation == "" {
		t.Fatal("library session not usable", err)
	}
	args, cleanup, auth, err := applySiteSessionGeneration("https://www.douyin.com/video/123", []string{"--no-cookies"}, status.Generation)
	if err != nil || !auth {
		t.Fatal("expected generation rejected", err)
	}
	cleanup()
	if len(args) == 0 {
		t.Fatal("authenticated arguments missing")
	}
	if _, cleanup, _, err = applySiteSessionGeneration("https://www.douyin.com/video/123", []string{"--no-cookies"}, "ffffffffffffffffffffffffffffffff"); err == nil {
		cleanup()
		t.Fatal("switched account reused new session without confirmation")
	}
}

func installFixtureSession(t *testing.T) string {
	t.Helper()
	root, err := sessionRoot()
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(siteSession{Site: "douyin", ConnectedAt: time.Now().UTC().Format(time.RFC3339Nano), Cookies: []siteCookie{{Name: "ttwid", Value: "fixture-cookie-never-real", Domain: ".douyin.com", Path: "/"}}})
	entropy := []byte(currentProductConfig().PluginID + "/douyin/v1")
	in := windows.DataBlob{Size: uint32(len(payload)), Data: &payload[0]}
	ent := windows.DataBlob{Size: uint32(len(entropy)), Data: &entropy[0]}
	var out windows.DataBlob
	if err = windows.CryptProtectData(&in, nil, &ent, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		t.Fatal(err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	id := "0123456789abcdef0123456789abcdef"
	if err = os.WriteFile(filepath.Join(root, "session-"+id+".bin"), unsafe.Slice(out.Data, out.Size), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "active"), []byte(id), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}
func TestEncryptedSessionReuseCleanupAndRevocation(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	root := installFixtureSession(t)
	status, err := siteStatus()
	if err != nil || status.State != "connected" {
		t.Fatalf("status: %+v %v", status, err)
	}
	encoded, _ := json.Marshal(status)
	if strings.Contains(string(encoded), "fixture-cookie") {
		t.Fatal("status leaked cookie")
	}
	if err = os.WriteFile(filepath.Join(root, "expired"), []byte("1"), 0600); err != nil {
		t.Fatal(err)
	}
	status, _ = siteStatus()
	if status.State != "connected" {
		t.Fatal("legacy ambiguous expiry still forces login")
	}
	_ = sessionFailure(errors.New("fixture-secret"), "Fresh cookies are needed", true, readSessionID(root, "active"))
	status, _ = siteStatus()
	if status.State != "connected" {
		t.Fatal("extractor rejection revoked a valid session")
	}
	args, cleanup, auth, err := applySiteSession("https://www.douyin.com/video/123", []string{"--no-cookies", "--write-info-json", "https://www.douyin.com/video/123"})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if !auth || strings.Contains(strings.Join(args, " "), "fixture-cookie") || strings.Contains(strings.Join(args, " "), "--write-info-json") {
		t.Fatal("secret argument or metadata export")
	}
	jar := args[1]
	if _, err = os.Stat(jar); err != nil {
		t.Fatal("temporary jar missing")
	}
	// Independent process proves detached workers need no invocation Broker.
	executable, _ := os.Executable()
	child := exec.Command(executable, "-test.run=^TestSessionIndependentProcess$")
	child.Env = append(cleanChildEnvironment(), "HIMIND_SESSION_FIXTURE_CHILD=1")
	if data, err := child.CombinedOutput(); err != nil {
		t.Fatalf("independent process failed: %v %s", err, data)
	}
	cleanup()
	if _, err = os.Stat(jar); !os.IsNotExist(err) {
		t.Fatal("temporary jar retained")
	}
	files, _ := filepath.Glob(filepath.Join(root, "*.bin"))
	for _, path := range files {
		data, _ := os.ReadFile(path)
		if strings.Contains(string(data), "fixture-cookie") {
			t.Fatal("plaintext session persisted")
		}
	}
	expireSite("ffffffffffffffffffffffffffffffff")
	status, _ = siteStatus()
	if status.State != "connected" {
		t.Fatal("late old-request error expired the new session")
	}
	expireSite(readSessionID(root, "active"))
	status, _ = siteStatus()
	if status.State != "expired" {
		t.Fatal("expired connection still connected")
	}
	if _, _, _, err = applySiteSession("https://www.douyin.com/video/123", nil); err == nil {
		t.Fatal("expired session acquired")
	}
	status, err = disconnectSite()
	if err != nil || status.State != "disconnected" {
		t.Fatal("disconnect failed", err)
	}
	if _, _, _, err = applySiteSession("https://www.douyin.com/video/123", nil); err == nil || !strings.Contains(err.Error(), "site_connection_required") {
		t.Fatal("revoked session acquired", err)
	}
}
func TestSessionIndependentProcess(t *testing.T) {
	if os.Getenv("HIMIND_SESSION_FIXTURE_CHILD") != "1" {
		return
	}
	args, cleanup, auth, err := applySiteSession("https://www.douyin.com/video/123", []string{"--no-cookies"})
	defer cleanup()
	if err != nil || !auth {
		t.Fatal("child unable to reuse session", err)
	}
	jar, err := os.ReadFile(args[1])
	if err != nil || !strings.Contains(string(jar), "fixture-cookie-never-real") {
		t.Fatal("child session mismatch")
	}
}
func TestDisconnectedLateBrowserResultCannotReactivate(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	root := installFixtureSession(t)
	attempt := "0123456789abcdef0123456789abcdef"
	_ = os.WriteFile(filepath.Join(root, "attempt"), []byte(attempt), 0600)
	if _, err := disconnectSite(); err != nil {
		t.Fatal(err)
	}
	// The closing browser writes only its own obsolete slot and status.
	_ = os.WriteFile(filepath.Join(root, "session-"+attempt+".bin"), []byte("obsolete"), 0600)
	payload, _ := json.Marshal(SiteStatus{State: "connected", Attempt: attempt, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)})
	_ = os.WriteFile(filepath.Join(root, "connection.json"), payload, 0600)
	status, _ := siteStatus()
	if status.State != "disconnected" {
		t.Fatal("late completion undid disconnect")
	}
}
