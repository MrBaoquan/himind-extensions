package mediadownload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRuntimeSystemProxySelection(t *testing.T) {
	for _, test := range []struct{ name, server, bypass, address, want string }{
		{"single proxy for https", "127.0.0.1:3001", "", "https://github.com/file", "http://127.0.0.1:3001"},
		{"per protocol", "http=127.0.0.1:80;https=127.0.0.1:443", "", "https://github.com/file", "http://127.0.0.1:443"},
		{"unconfigured protocol", "http=127.0.0.1:80", "", "https://github.com/file", ""},
		{"loopback bypass", "127.0.0.1:3001", "", "http://127.0.0.1/file", ""},
		{"wildcard bypass", "127.0.0.1:3001", "*.example.org", "https://cdn.example.org/file", ""},
		{"local bypass", "127.0.0.1:3001", "<local>", "http://intranet/file", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, _ := http.NewRequest(http.MethodGet, test.address, nil)
			proxy, err := configuredRuntimeProxy(test.server, test.bypass, request)
			if err != nil {
				t.Fatal(err)
			}
			actual := ""
			if proxy != nil {
				actual = proxy.String()
			}
			if actual != test.want {
				t.Fatalf("proxy = %q, want %q", actual, test.want)
			}
		})
	}
}

func TestRuntimeDownloadRecoversFromTruncatedBodyAndChecksDigest(t *testing.T) {
	content := []byte("fixed runtime content")
	digest := sha256.Sum256(content)
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			_, _ = w.Write(content[:4])
			return
		}
		_, _ = w.Write(content)
	}))
	defer server.Close()
	lock := runtimeLock{URL: server.URL, Size: int64(len(content)), SHA256: hex.EncodeToString(digest[:])}
	target := filepath.Join(t.TempDir(), "yt-dlp.exe")
	err := retryRuntimeDownload(context.Background(), 3, 0, func() error {
		return downloadRuntimeAttempt(context.Background(), server.Client(), lock, target)
	})
	if err != nil || calls != 2 {
		t.Fatalf("expected recovery after truncated transfer, calls=%d err=%v", calls, err)
	}
	actual, _ := os.ReadFile(target)
	if string(actual) != string(content) {
		t.Fatal("installed content mismatch")
	}
	lock.SHA256 = strings.Repeat("0", 64)
	calls = 1
	err = retryRuntimeDownload(context.Background(), 3, 0, func() error {
		return downloadRuntimeAttempt(context.Background(), server.Client(), lock, target)
	})
	if err == nil || calls != 2 {
		t.Fatalf("checksum mismatch must stop immediately: calls=%d err=%v", calls, err)
	}
	actual, _ = os.ReadFile(target)
	if string(actual) != string(content) {
		t.Fatal("checksum failure overwrote existing runtime")
	}
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(target), "*.download"))
	if len(leftovers) != 0 {
		t.Fatalf("partial downloads left behind: %v", leftovers)
	}
}

func TestRuntimeDownloadRetryPolicyAndCancellation(t *testing.T) {
	for _, test := range []struct {
		err  error
		want int
	}{
		{runtimeHTTPError{503}, 3}, {runtimeHTTPError{429}, 3},
		{runtimeHTTPError{404}, 1}, {errors.New("摘要不匹配"), 1}, {io.ErrUnexpectedEOF, 3},
	} {
		calls := 0
		_ = retryRuntimeDownload(context.Background(), 3, 0, func() error { calls++; return test.err })
		if calls != test.want {
			t.Fatalf("%v: calls=%d want=%d", test.err, calls, test.want)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := retryRuntimeDownload(ctx, 3, time.Hour, func() error { calls++; cancel(); return io.ErrUnexpectedEOF })
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("cancellation ignored: calls=%d err=%v", calls, err)
	}
}

func TestRuntimePythonSkipsStoreAliasAndUsesExistingUVInterpreter(t *testing.T) {
	python := filepath.Join(t.TempDir(), "python.exe")
	if err := os.WriteFile(python, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	lookup := func(name string) (string, error) {
		switch name {
		case "python.exe":
			return "store-alias", nil
		case "uv.exe":
			return "uv", nil
		}
		return "", errors.New("missing")
	}
	var uvArgs string
	result, err := findRuntimePythonWith(context.Background(), lookup, func(_ context.Context, executable string, args ...string) ([]byte, error) {
		switch executable {
		case "store-alias":
			return nil, errors.New("exit status 9009: Python was not found")
		case "uv":
			uvArgs = strings.Join(args, " ")
			return []byte(python + "\r\n"), nil
		case python:
			return json.Marshal(pythonProbe{Executable: python, Major: 3, Minor: 14, Venv: true})
		}
		return nil, fmt.Errorf("unexpected executable %s", executable)
	})
	if err != nil || result != python || uvArgs != "python find --offline --no-python-downloads" {
		t.Fatalf("existing uv runtime not found safely: %q %v, uv=%q", result, err, uvArgs)
	}
}

func TestRuntimePythonRejectsOldOrIncompleteInterpreter(t *testing.T) {
	python := filepath.Join(t.TempDir(), "python.exe")
	_ = os.WriteFile(python, []byte("fixture"), 0o600)
	for _, probe := range []pythonProbe{
		{Executable: python, Major: 3, Minor: 9, Venv: true},
		{Executable: python, Major: 3, Minor: 14, Venv: false},
		{Executable: "relative.exe", Major: 3, Minor: 14, Venv: true},
	} {
		lookup := func(name string) (string, error) {
			if name == "python.exe" {
				return python, nil
			}
			return "", errors.New("missing")
		}
		_, err := findRuntimePythonWith(context.Background(), lookup, func(context.Context, string, ...string) ([]byte, error) { return json.Marshal(probe) })
		if err == nil || !strings.Contains(err.Error(), "Windows Store") {
			t.Fatalf("unusable Python accepted: %+v, %v", probe, err)
		}
	}
}
