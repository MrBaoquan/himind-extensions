package workspace

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// 工作区里的媒体文件发现。
//
// 为什么需要：让人（和 AI）手打完整相对路径既不友好也易错。列出工作区里
// **已经是媒体**的文件，用户从下拉里选，比敲路径快得多。
// 它同时也是给 AI 的入口 —— "这个工作区里有哪些可处理的素材"。

// mediaExtensions 视为媒体的扩展名。
var mediaExtensions = map[string]string{
	".mp4":  "video",
	".mov":  "video",
	".mkv":  "video",
	".avi":  "video",
	".webm": "video",
	".flv":  "video",
	".m4v":  "video",
	".ts":   "video",
	".mp3":  "audio",
	".wav":  "audio",
	".aac":  "audio",
	".flac": "audio",
	".m4a":  "audio",
	".ogg":  "audio",
	".opus": "audio",
	".jpg":  "image",
	".jpeg": "image",
	".png":  "image",
	".webp": "image",
	".bmp":  "image",
	".gif":  "image",
}

// MediaFile 一个候选媒体文件。
type MediaFile struct {
	// Path 相对工作区根的路径（分隔符统一成 /，便于页面直接显示与回传）。
	Path      string `json:"path"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	SizeBytes int64  `json:"size_bytes"`
	Modified  string `json:"modified,omitempty"`
}

// ListOptions 一次列举的参数。
type ListOptions struct {
	// Kind 只取某一类（video / audio / image）；为空表示全部。
	Kind string
	// MaxDepth 最大递归深度（相对工作区根），默认 4，上限 12。
	MaxDepth int
	// Limit 最多返回多少条，默认 200，上限 1000。
	Limit int
	// MaxScan 最多检查多少条目，防超大目录把一次调用拖住，默认 20000。
	MaxScan int
}

// skipDirs 不出现在结果里的目录。
//
// 这些是运行期产物与元数据：把它们列出来只会淹没真正的素材。
var skipDirs = map[string]bool{
	".git": true, ".svn": true, "node_modules": true, ".himind": true,
	"dist": true, "bin": true, "obj": true, "temp": true, "logs": true,
	"runtime": true, "jobs": true,
}

// ListMedia 递归列举工作区里的媒体文件。结果按修改时间倒序（最近处理的在前）。
func (r Root) ListMedia(options ListOptions) ([]MediaFile, bool) {
	kind := strings.ToLower(strings.TrimSpace(options.Kind))
	maxDepth := options.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 4
	}
	if maxDepth > 12 {
		maxDepth = 12
	}
	limit := options.Limit
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	maxScan := options.MaxScan
	if maxScan <= 0 {
		maxScan = 20000
	}

	results := make([]MediaFile, 0, 64)
	scanned := 0
	truncated := false

	_ = filepath.WalkDir(r.abs, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			// 单个条目读不到不该让整次列举失败（权限、占用中都很常见）。
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if scanned >= maxScan {
			truncated = true
			return filepath.SkipAll
		}
		scanned++

		relative, relErr := filepath.Rel(r.abs, path)
		if relErr != nil {
			return nil
		}
		if relative == "." {
			return nil
		}
		depth := len(strings.Split(relative, string(filepath.Separator)))
		name := entry.Name()

		if entry.IsDir() {
			if skipDirs[strings.ToLower(name)] || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			if depth >= maxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") {
			return nil
		}

		fileKind, ok := mediaExtensions[strings.ToLower(filepath.Ext(name))]
		if !ok {
			return nil
		}
		if kind != "" && fileKind != kind {
			return nil
		}
		if len(results) >= limit {
			truncated = true
			return filepath.SkipAll
		}

		info, infoErr := entry.Info()
		file := MediaFile{
			Path: filepath.ToSlash(relative),
			Name: name,
			Kind: fileKind,
		}
		if infoErr == nil {
			file.SizeBytes = info.Size()
			file.Modified = info.ModTime().UTC().Format(time.RFC3339)
		}
		results = append(results, file)
		return nil
	})

	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Modified > results[j].Modified
	})
	return results, truncated
}
