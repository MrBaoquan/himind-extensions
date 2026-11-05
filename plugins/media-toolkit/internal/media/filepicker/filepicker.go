package filepicker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"himind-plugin/media-toolkit/internal/media/workspace"
)

const maxFiles = 200

var imageExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true,
	".bmp": true, ".tif": true, ".tiff": true, ".gif": true,
}
var videoExtensions = map[string]bool{
	".mp4": true, ".mov": true, ".mkv": true, ".webm": true,
	".avi": true, ".m4v": true, ".wmv": true, ".flv": true,
	".ts": true, ".mts": true, ".m2ts": true,
}
var audioExtensions = map[string]bool{
	".mp3": true, ".wav": true, ".m4a": true, ".aac": true,
	".flac": true, ".ogg": true, ".opus": true, ".wma": true,
}

// Options controls the native picker and the files accepted from it.
type Options struct {
	Mode     string `json:"mode"`
	Multiple bool   `json:"multiple"`
}

// File is a workspace-relative media file ready for a processing capability.
type File struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	SizeBytes int64  `json:"size_bytes"`
	Imported  bool   `json:"imported"`
}

// Result is intentionally explicit about cancellation so the UI never treats a
// closed dialog as an error.
type Result struct {
	Canceled bool   `json:"canceled"`
	Files    []File `json:"files"`
}

type dialogRequest struct {
	Title       string
	InitialDir  string
	FilterLabel string
	Extensions  []string
	Multiple    bool
}

// Pick opens the operating system file picker. Files selected outside the
// workspace are copied into a plugin-owned inbox so all later processing keeps
// the same workspace-only safety boundary.
func Pick(ctx context.Context, root workspace.Root, options Options) (Result, error) {
	request, err := dialogForMode(options)
	if err != nil {
		return Result{}, err
	}
	request.InitialDir = root.Abs()
	paths, err := pickPaths(ctx, request)
	if err != nil {
		return Result{}, err
	}
	if len(paths) == 0 {
		return Result{Canceled: true, Files: []File{}}, nil
	}
	return ImportPaths(ctx, root, paths, options)
}

// ImportPaths validates selected media and imports outside-workspace files.
// It is separate from the native dialog so the copy and validation rules are
// testable without opening UI.
func ImportPaths(ctx context.Context, root workspace.Root, paths []string, options Options) (Result, error) {
	mode, err := normalizeMode(options.Mode)
	if err != nil {
		return Result{}, err
	}
	if len(paths) == 0 {
		return Result{Canceled: true, Files: []File{}}, nil
	}
	if len(paths) > maxFiles {
		return Result{}, fmt.Errorf("一次最多选择 %d 个文件", maxFiles)
	}
	if !options.Multiple && len(paths) > 1 {
		return Result{}, errors.New("当前任务只能选择一个文件")
	}

	seen := make(map[string]bool)
	files := make([]File, 0, len(paths))
	batchDir := ""
	for _, raw := range paths {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		abs, err := filepath.Abs(strings.TrimSpace(raw))
		if err != nil {
			return Result{}, fmt.Errorf("文件路径无效: %w", err)
		}
		info, err := os.Stat(abs)
		if err != nil || !info.Mode().IsRegular() {
			return Result{}, fmt.Errorf("文件不存在或不是普通文件: %s", filepath.Base(abs))
		}
		kind := kindForExtension(filepath.Ext(abs))
		if !accepts(mode, kind) {
			return Result{}, fmt.Errorf("所选文件类型不适合当前任务: %s", filepath.Base(abs))
		}
		canonical, insideErr := root.EnsureInside(abs)
		imported := false
		if insideErr != nil {
			imported = true
			if batchDir == "" {
				batchDir, err = newInboxBatch(root.Abs())
				if err != nil {
					return Result{}, err
				}
			}
			canonical, err = importExternal(ctx, batchDir, abs)
			if err != nil {
				return Result{}, err
			}
		}
		if seen[canonical] {
			continue
		}
		seen[canonical] = true
		relative, err := filepath.Rel(root.Abs(), canonical)
		if err != nil {
			return Result{}, fmt.Errorf("计算工作区相对路径失败: %w", err)
		}
		files = append(files, File{
			Path:      filepath.ToSlash(relative),
			Name:      filepath.Base(canonical),
			Kind:      kind,
			SizeBytes: info.Size(),
			Imported:  imported,
		})
	}
	return Result{Files: files}, nil
}

func dialogForMode(options Options) (dialogRequest, error) {
	mode, err := normalizeMode(options.Mode)
	if err != nil {
		return dialogRequest{}, err
	}
	request := dialogRequest{Multiple: options.Multiple}
	switch mode {
	case "image":
		request.Title = "选择图片"
		request.FilterLabel = "图片"
		request.Extensions = keys(imageExtensions)
	case "video":
		request.Title = "选择视频"
		request.FilterLabel = "视频"
		request.Extensions = keys(videoExtensions)
	case "audio":
		request.Title = "选择音频"
		request.FilterLabel = "音频"
		request.Extensions = keys(audioExtensions)
	default:
		request.Title = "选择媒体文件"
		request.FilterLabel = "媒体文件"
		request.Extensions = append(append(keys(imageExtensions), keys(videoExtensions)...), keys(audioExtensions)...)
	}
	return request, nil
}

func normalizeMode(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "media":
		return "media", nil
	case "image", "images":
		return "image", nil
	case "video":
		return "video", nil
	case "audio":
		return "audio", nil
	default:
		return "", fmt.Errorf("不支持的素材类型: %s", value)
	}
}

func kindForExtension(ext string) string {
	ext = strings.ToLower(ext)
	switch {
	case imageExtensions[ext]:
		return "image"
	case videoExtensions[ext]:
		return "video"
	case audioExtensions[ext]:
		return "audio"
	default:
		return ""
	}
}

func accepts(mode, kind string) bool {
	return kind != "" && (mode == "media" || kind == mode)
}

func keys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func newInboxBatch(root string) (string, error) {
	batch, err := inboxBatchName()
	if err != nil {
		return "", err
	}
	directory := filepath.Join(root, ".himind", "media-toolkit", "inbox", batch)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", fmt.Errorf("创建素材收件目录失败: %w", err)
	}
	return directory, nil
}

func importExternal(ctx context.Context, destinationDir, source string) (string, error) {
	name := filepath.Base(source)
	destination := filepath.Join(destinationDir, name)
	for suffix := 2; ; suffix++ {
		_, statErr := os.Stat(destination)
		if errors.Is(statErr, os.ErrNotExist) {
			break
		}
		if statErr != nil {
			return "", fmt.Errorf("检查导入目标失败: %w", statErr)
		}
		ext := filepath.Ext(name)
		destination = filepath.Join(destinationDir, strings.TrimSuffix(name, ext)+fmt.Sprintf("-%d%s", suffix, ext))
	}
	if err := copyFile(ctx, source, destination); err != nil {
		return "", fmt.Errorf("导入素材失败 %s: %w", name, err)
	}
	return destination, nil
}

func copyFile(ctx context.Context, source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = output.Close()
		if !ok {
			_ = os.Remove(destination)
		}
	}()
	buffer := make([]byte, 1024*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := input.Read(buffer)
		if n > 0 {
			if _, err := output.Write(buffer[:n]); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if err := output.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}

func inboxBatchName() (string, error) {
	random := make([]byte, 4)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return time.Now().Format("20060102-150405") + "-" + hex.EncodeToString(random), nil
}
