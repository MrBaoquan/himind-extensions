package mediadownload

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type TransferProgress struct {
	Percent           float64
	Downloaded, Total int64
}

// ExecuteTransfer runs synchronously inside the product's leased worker. It has
// no queue or job state of its own and never persists raw extractor output.
// destination must be a private, per-task staging directory owned by the caller.
func ExecuteTransfer(ctx context.Context, input JobStartInput, destination, generation string, progress func(TransferProgress)) ([]string, error) {
	if _, err := validateMediaURL(input.URL); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(destination) {
		return nil, fmt.Errorf("采集暂存目录必须是绝对路径")
	}
	if err := os.MkdirAll(destination, 0700); err != nil {
		return nil, fmt.Errorf("采集暂存目录不可写")
	}
	location, err := locateYtDlp()
	if err != nil {
		return nil, err
	}
	ffmpeg, _ := ffmpegStatus(ctx)
	input.Overwrite = false
	args, err := buildDownloadArgs(input, destination, ffmpeg != "")
	if err != nil {
		return nil, err
	}
	// Info JSON may contain signed media URLs and headers. The product persists
	// only its own stable source reference and verified file manifest.
	args = append(args[:len(args)-1], "--no-write-info-json", "--no-simulate", "--print", "after_move:himind-file:%(filepath)j", input.URL)
	args, cleanup, authenticated, err := applySiteSessionGeneration(input.URL, args, generation)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	normalized, err := normalizeSiteURL(ctx, input.URL)
	if err != nil {
		return nil, err
	}
	args[len(args)-1] = normalized
	detail, err := prepareDouyinDetail(ctx, normalized, args)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(ctx, location.Path, args...)
	command.Dir = destination
	command.Env = append(cleanChildEnvironment(), "PYTHONIOENCODING=utf-8")
	if detail != "" {
		command.Env = append(command.Env, "HIMIND_DOUYIN_DETAIL_FILE="+detail)
	}
	configureHiddenProcess(command)
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		return terminateProcessTree(command.Process.Pid)
	}
	command.WaitDelay = 5 * time.Second
	output := &transferProgressWriter{callback: progress}
	diagnostic := &transferDiagnostic{}
	command.Stdout = io.MultiWriter(output, diagnostic)
	command.Stderr = diagnostic
	if authenticated {
		go watchSiteRevocation(ctx, cancel, make(chan struct{}, 1), sessionLease(args))
	}
	if err = command.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if authenticated && diagnostic.session.expired {
			expireSite(sessionLease(args))
			return nil, errors.New(connectionExpired)
		}
		if diagnostic.failure != nil {
			return nil, diagnostic.failure
		}
		return nil, fmt.Errorf("媒体下载失败，请检查网络和平台状态后重试")
	}
	paths, err := output.completedFiles(destination)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		if diagnostic.failure != nil {
			return nil, diagnostic.failure
		}
		return nil, fmt.Errorf("下载器未产生媒体文件，请检查格式选项")
	}
	return paths, nil
}

type transferProgressWriter struct {
	mu       sync.Mutex
	buffer   string
	callback func(TransferProgress)
	paths    []string
}

func (w *transferProgressWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buffer += string(data)
	for {
		index := strings.IndexByte(w.buffer, '\n')
		if index < 0 {
			break
		}
		line := w.buffer[:index]
		w.buffer = w.buffer[index+1:]
		if strings.HasPrefix(line, "himind-file:") {
			var path string
			if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "himind-file:"))), &path) == nil && len(w.paths) < maxMaxItems {
				w.paths = append(w.paths, path)
			}
		}
		if percent, downloaded, total, _, _, ok := parseProgressLine(line); ok && w.callback != nil {
			w.callback(TransferProgress{Percent: percent, Downloaded: downloaded, Total: total})
		}
	}
	if len(w.buffer) > 8192 {
		w.buffer = ""
	}
	return len(data), nil
}

func (w *transferProgressWriter) completedFiles(root string) ([]string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("采集暂存目录不可用")
	}
	seen := map[string]bool{}
	var paths []string
	for _, path := range w.paths {
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil, fmt.Errorf("采集输出不可用")
		}
		rel, err := filepath.Rel(realRoot, real)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return nil, fmt.Errorf("采集输出不在暂存目录中")
		}
		info, err := os.Stat(real)
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
			return nil, fmt.Errorf("采集输出不可用")
		}
		if !seen[real] {
			paths = append(paths, real)
			seen[real] = true
		}
		// yt-dlp reports the final media path after postprocessing. Only subtitle
		// sidecars sharing that basename are included, never stale media fragments.
		entries, err := os.ReadDir(filepath.Dir(real))
		if err != nil {
			return nil, err
		}
		stem := strings.TrimSuffix(filepath.Base(real), filepath.Ext(real)) + "."
		for _, entry := range entries {
			ext := strings.ToLower(filepath.Ext(entry.Name()))
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasPrefix(entry.Name(), stem) || (ext != ".srt" && ext != ".vtt" && ext != ".ass" && ext != ".lrc") {
				continue
			}
			file := filepath.Join(filepath.Dir(real), entry.Name())
			if !seen[file] {
				paths = append(paths, file)
				seen[file] = true
			}
		}
	}
	return paths, nil
}

// StartLibraryWorker launches only the executable's fixed acquisition entry.
// Broker grants and WebView debugging settings cannot leak into detached work.
func StartLibraryWorker(id, dataRoot string) error {
	if !safeJobID(id) || !filepath.IsAbs(dataRoot) {
		return fmt.Errorf("采集执行参数无效")
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	command := exec.Command(executable, "--acquisition-worker", id)
	command.Dir = filepath.Dir(executable)
	for _, entry := range cleanChildEnvironment() {
		if !strings.HasPrefix(strings.ToUpper(entry), "HIMIND_PLUGIN_DATA_ROOT=") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "HIMIND_PLUGIN_DATA_ROOT="+dataRoot)
	configureHiddenProcess(command)
	if err = command.Start(); err != nil {
		return fmt.Errorf("无法启动采集任务")
	}
	return command.Process.Release()
}

func Destination(workspace, destination string) (string, error) {
	if workspace == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		workspace = home
	}
	root, err := resolveWorkspaceRoot(workspace)
	if err != nil {
		return "", err
	}
	return resolveDestination(root, destination)
}
