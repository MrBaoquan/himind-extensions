package mediadownload

import (
	"fmt"
	"strings"
	"testing"
)

func TestDownloadFailureIsFixedAndDoesNotExposeExtractorSecrets(t *testing.T) {
	for _, item := range []struct{ text, code string }{
		{"HTTP Error 403 caused by Cloudflare anti-bot challenge", "PLATFORM_VERIFICATION"},
		{"File is larger than max-filesize", "SIZE_LIMIT"},
		{"Requested format is not available", "FORMAT_UNAVAILABLE"},
		{"No space left on device", "DISK_FULL"},
		{"Permission denied", "DESTINATION_DENIED"},
		{"ffmpeg is not installed", "FFMPEG_MISSING"},
		{"HTTP Error 429", "RATE_LIMITED"},
		{"HTTP Error 403", "SOURCE_UNAVAILABLE"},
		{"Connection timed out", "NETWORK_FAILED"},
		{"unrecognized failure", "DOWNLOAD_FAILED"},
	} {
		code, message := DownloadFailure(fmt.Errorf("%s https://example.test/?signature=secret-value Cookie: private-value", item.text))
		if code != item.code {
			t.Fatalf("%s: got %s", item.text, code)
		}
		if strings.Contains(message, "secret-value") || strings.Contains(message, "private-value") || strings.Contains(message, "http") {
			t.Fatal("extractor detail exposed")
		}
	}
}

func TestTransferDiagnosticRecognizesChunkedOutputAndVerificationHasPriority(t *testing.T) {
	d := &transferDiagnostic{}
	for _, part := range []string{"ERROR HTTP Error 403", " caused by Cloud", "flare anti-bot challenge", strings.Repeat("x", 10000)} {
		_, _ = d.Write([]byte(part))
	}
	if d.failure == nil || d.failure.Code != "PLATFORM_VERIFICATION" {
		t.Fatal("verification cause lost")
	}
	if len(d.tail) > 256 {
		t.Fatal("unbounded extractor diagnostic")
	}
	code, _ := DownloadFailure(fmt.Errorf("wrapped: %w", d.failure))
	if code != "PLATFORM_VERIFICATION" {
		t.Fatal("typed failure lost")
	}
}
