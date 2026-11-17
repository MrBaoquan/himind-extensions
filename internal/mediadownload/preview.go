package mediadownload

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const PreviewMaxBytes int64 = 96 << 20

// DownloadPreview prepares one bounded, disposable video. It never writes an
// info-json sidecar (which would retain signed CDN URLs or request headers).
func DownloadPreview(ctx context.Context, source, directory string) (string, error) {
	if _, err := validateMediaURL(source); err != nil {
		return "", err
	}
	runtime, err := locateYtDlp()
	if err != nil {
		return "", errors.New("下载组件未安装，请在设置中安装后重试")
	}
	format := "best[ext=mp4][vcodec~='^(avc1|h264)'][height<=720]/best[ext=mp4][vcodec~='^(avc1|h264)']/best[ext=webm][height<=720]"
	ffmpeg, _ := ffmpegStatus(ctx)
	if ffmpeg != "" {
		format = "bestvideo[ext=mp4][vcodec~='^(avc1|h264)'][height<=720]+bestaudio[ext=m4a]/" + format
	}
	args := []string{"--no-config", "--no-config-locations", "--no-cookies", "--no-playlist", "--no-warnings", "--no-progress", "--no-write-info-json", "--socket-timeout", "12", "--retries", "1", "--fragment-retries", "1", "--max-filesize", "100663296", "--format", format, "--output", filepath.Join(directory, "preview.%(ext)s")}
	if ffmpeg != "" {
		args = append(args, "--ffmpeg-location", ffmpeg, "--merge-output-format", "mp4")
	}
	args = append(args, source)
	args, cleanup, authenticated, err := applySiteSession(source, args)
	if err != nil {
		return "", err
	}
	defer cleanup()
	normalized, err := normalizeSiteURL(ctx, source)
	if err != nil {
		return "", err
	}
	args[len(args)-1] = normalized
	detail, err := prepareDouyinDetail(ctx, normalized, args)
	if err != nil {
		return "", err
	}
	run, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(run, runtime.Path, args...)
	cmd.Env = cleanChildEnvironment()
	if detail != "" {
		cmd.Env = append(cmd.Env, "HIMIND_DOUYIN_DETAIL_FILE="+detail)
	}
	configureHiddenProcess(cmd)
	var output limitedBuffer
	output.limit = 64 << 10
	cmd.Stdout, cmd.Stderr = &output, &output
	// Enforce the aggregate bound even for unknown file sizes and split tracks.
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if previewDirectoryBytes(directory) > PreviewMaxBytes {
					cancel()
					return
				}
			}
		}
	}()
	err = cmd.Run()
	close(done)
	if err != nil {
		if ctx.Err() != nil {
			return "", errors.New("预览加载已停止，请重试")
		}
		if previewDirectoryBytes(directory) > PreviewMaxBytes {
			return "", errors.New("此视频超出预览大小限制，请打开原作品或采集后播放")
		}
		failure := sessionFailure(errors.New("预览暂不可用"), output.buf.String(), authenticated, sessionLease(args))
		if strings.Contains(failure.Error(), "site_connection_") {
			return "", failure
		}
		return "", errors.New("暂时无法加载此视频，请检查平台连接后重试，或打开原作品")
	}
	for _, ext := range []string{".mp4", ".webm"} {
		file := filepath.Join(directory, "preview"+ext)
		if info, err := os.Stat(file); err == nil && info.Size() > 1024 && info.Size() <= PreviewMaxBytes {
			return file, nil
		}
	}
	return "", errors.New("此视频没有可用的预览格式，请打开原作品或采集后播放")
}

func previewDirectoryBytes(directory string) int64 {
	var size int64
	entries, _ := os.ReadDir(directory)
	for _, entry := range entries {
		if info, err := entry.Info(); err == nil && info.Mode().IsRegular() {
			size += info.Size()
		}
	}
	return size
}
