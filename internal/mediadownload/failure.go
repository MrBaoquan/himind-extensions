package mediadownload

import (
	"errors"
	"strings"
	"sync"
)

// TransferFailure carries only a fixed code and message. Extractor output may
// contain headers and signed URLs, so it must never become a persisted error.
type TransferFailure struct {
	Code    string
	Message string
}

func (e *TransferFailure) Error() string { return e.Message }

func knownFailure(text string) *TransferFailure {
	text = strings.ToLower(text)
	contains := func(values ...string) bool {
		for _, value := range values {
			if strings.Contains(text, value) {
				return true
			}
		}
		return false
	}
	switch {
	case contains("anti-bot", "cloudflare", "captcha", "confirm you're not a bot", "verify you are human"):
		return &TransferFailure{"PLATFORM_VERIFICATION", "平台要求网页验证，请打开作品页面完成验证后重试；仍失败时请稍后再试"}
	case contains("larger than max-filesize", "max-filesize limit", "file is larger than"):
		return &TransferFailure{"SIZE_LIMIT", "文件超过本次大小上限，请选择较小格式或提高上限后重新创建任务"}
	case contains("requested format is not available", "no video formats found", "does not have any formats"):
		return &TransferFailure{"FORMAT_UNAVAILABLE", "所选格式不可用，请重新解析并选择最佳画质或其他原始格式"}
	case contains("no space left", "not enough space", "disk full"):
		return &TransferFailure{"DISK_FULL", "磁盘空间不足，请释放空间后重试"}
	case contains("permission denied", "access is denied"):
		return &TransferFailure{"DESTINATION_DENIED", "无法写入下载目录，请检查目录权限或更换保存位置"}
	case contains("ffmpeg is not installed", "ffprobe and ffmpeg not found", "ffmpeg not found", "需要 ffmpeg"):
		return &TransferFailure{"FFMPEG_MISSING", "此格式需要 ffmpeg，请安装后重试，或选择包含音轨的原始格式"}
	case contains("http error 429", "too many requests"):
		return &TransferFailure{"RATE_LIMITED", "平台暂时限制请求，请稍后重试"}
	case contains("http error 403", "http error 401", "http error 404", "video unavailable", "private video", "not available in your country"):
		return &TransferFailure{"SOURCE_UNAVAILABLE", "平台未允许读取此作品，请确认作品页面可访问及账号权限后重试"}
	case contains("timed out", "timeout", "connection reset", "connection refused", "name resolution", "getaddrinfo failed", "certificate_verify_failed", "unable to download", "urlopen error"):
		return &TransferFailure{"NETWORK_FAILED", "网络连接失败，请检查网络及平台可访问性后重试"}
	}
	return nil
}

func DownloadFailure(err error) (string, string) {
	var failure *TransferFailure
	if errors.As(err, &failure) {
		return failure.Code, failure.Message
	}
	if err != nil {
		if failure = knownFailure(err.Error()); failure != nil {
			return failure.Code, failure.Message
		}
	}
	return "DOWNLOAD_FAILED", "媒体采集失败，请检查网络和格式选项后重试"
}

type transferDiagnostic struct {
	mu      sync.Mutex
	session sessionDiagnostic
	tail    string
	failure *TransferFailure
}

func (w *transferDiagnostic) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, _ = w.session.Write(p)
	text := w.tail + string(p)
	failure := knownFailure(text)
	if failure != nil && (w.failure == nil || failure.Code == "PLATFORM_VERIFICATION") {
		w.failure = failure
	}
	if len(text) > 256 {
		text = text[len(text)-256:]
	}
	w.tail = text
	return len(p), nil
}
