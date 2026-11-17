package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRemotePreviewRejectsInvalidAndCanceledRequests(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	for _, in := range []remotePreviewInput{{URL: "http://127.0.0.1/private", RequestID: strings.Repeat("a", 32)}, {URL: "https://www.youtube.com/watch?v=abcdefghijk", RequestID: "../../escape"}} {
		if _, err := remotePreview(in); err == nil {
			t.Fatal("invalid request accepted")
		}
	}
	id := strings.Repeat("b", 32)
	if _, err := cancelRemotePreview(id); err != nil {
		t.Fatal(err)
	}
	if _, err := remotePreview(remotePreviewInput{URL: "https://www.youtube.com/watch?v=abcdefghijk", RequestID: id}); err == nil || !strings.Contains(err.Error(), "关闭") {
		t.Fatal("canceled preview was resolved", err)
	}
}

func TestPreviewCacheRemovesOnlyExpiredDisposableFiles(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	expired := strings.Repeat("a", 64) + ".mp4"
	fresh := strings.Repeat("b", 64) + ".webm"
	for _, n := range []string{expired, fresh, "user-video.mp4", "session.bin"} {
		if err := os.WriteFile(filepath.Join(root, n), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	old := now.Add(-time.Hour)
	_ = os.Chtimes(filepath.Join(root, expired), old, old)
	cleanupRemotePreviews(root, now)
	if _, err := os.Stat(filepath.Join(root, expired)); !os.IsNotExist(err) {
		t.Fatal("expired cache retained")
	}
	for _, n := range []string{fresh, "user-video.mp4", "session.bin"} {
		if _, err := os.Stat(filepath.Join(root, n)); err != nil {
			t.Fatal("non-disposable file removed", n)
		}
	}
}

func TestDiscoveryCoverTargetsArePlatformScoped(t *testing.T) {
	for _, u := range []string{"http://p3.douyinpic.com/a.jpg", "https://p3.douyinpic.com.evil.test/a.jpg", "https://user@p3.douyinpic.com/a.jpg", "https://127.0.0.1/a.jpg", "https://p3.douyinpic.com:444/a.jpg", "https://i.ytimg.com/a.jpg"} {
		if discoveryCoverAllowed("douyin", u) {
			t.Fatal("unsafe cover", u)
		}
	}
	if !discoveryCoverAllowed("douyin", "https://p3.douyinpic.com/a.jpg?signature=fixture") {
		t.Fatal("valid cover rejected")
	}
}
