package main

import (
	"encoding/base64"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
)

type previewInput struct {
	AssetID  string `json:"asset_id"`
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	MaxBytes int64  `json:"max_bytes"`
}

func previewAsset(in previewInput) (any, error) {
	path := strings.TrimSpace(in.Path)
	if path == "" && strings.TrimSpace(in.AssetID) != "" {
		db, err := openLibraryDB()
		if err != nil {
			return nil, err
		}
		defer db.Close()
		err = db.QueryRow(`SELECT local_path FROM library_items WHERE id=? AND deleted_at='' UNION ALL SELECT path FROM candidates WHERE id=? AND missing=0 LIMIT 1`, in.AssetID, in.AssetID).Scan(&path)
		if err != nil {
			return nil, fmt.Errorf("参考素材不存在或已离线，请重新选择")
		}
	}
	if path == "" {
		return nil, fmt.Errorf("缺少预览路径")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("素材文件不可用")
	}
	if !previewPathAllowed(absolute) {
		return nil, fmt.Errorf("该路径不属于已关联的资源库或索引来源")
	}
	kind := strings.ToLower(strings.TrimSpace(in.Kind))
	if kind == "" {
		kind = mediaExtensions[strings.ToLower(filepath.Ext(absolute))]
	}
	result := map[string]any{
		"path":  absolute,
		"kind":  kind,
		"bytes": info.Size(),
	}
	streamURL := streamURLFor(absolute, strings.TrimSpace(in.AssetID))
	if streamURL != "" {
		result["stream_url"] = streamURL
	}
	if kind == "image" {
		limit := in.MaxBytes
		if limit <= 0 {
			limit = 8 << 20
		}
		if limit > 32<<20 {
			limit = 32 << 20
		}
		if info.Size() > limit {
			if streamURL == "" {
				return nil, fmt.Errorf("图片超过预览上限，请打开所在位置查看")
			}
			return result, nil
		}
		payload, err := os.ReadFile(absolute)
		if err != nil {
			return nil, err
		}
		mimeType := "image/png"
		switch strings.ToLower(filepath.Ext(absolute)) {
		case ".jpg", ".jpeg":
			mimeType = "image/jpeg"
		case ".webp":
			mimeType = "image/webp"
		case ".gif":
			mimeType = "image/gif"
		case ".bmp":
			mimeType = "image/bmp"
		case ".avif":
			mimeType = "image/avif"
		case ".svg":
			mimeType = "image/svg+xml"
		}
		result["data_url"] = "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(payload)
		return result, nil
	}
	if kind != "video" && kind != "audio" {
		return nil, fmt.Errorf("该媒体类型暂不支持预览")
	}
	// 视频/音频优先走 Range 流：不把整段媒体塞进 data URL，可拖动进度。
	if streamURL != "" {
		return result, nil
	}
	limit := in.MaxBytes
	if limit <= 0 {
		limit = 32 << 20
	}
	if limit > 64<<20 {
		limit = 64 << 20
	}
	if info.Size() <= limit {
		payload, err := os.ReadFile(absolute)
		if err != nil {
			return nil, err
		}
		mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(absolute)))
		if mimeType == "" {
			if kind == "video" {
				mimeType = "video/mp4"
			} else {
				mimeType = "audio/mpeg"
			}
		}
		result["data_url"] = "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(payload)
	}
	result["file_url"] = fileURL(absolute)
	return result, nil
}

func previewPathAllowed(path string) bool {
	db, err := openLibraryDB()
	if err != nil {
		return false
	}
	defer db.Close()
	var found int
	err = db.QueryRow(`SELECT 1 FROM library_items WHERE local_path=?
		UNION ALL SELECT 1 FROM candidates WHERE path=?
		LIMIT 1`, path, path).Scan(&found)
	return err == nil && found == 1
}

func fileURL(path string) string {
	normalized := filepath.ToSlash(path)
	if len(normalized) >= 2 && normalized[1] == ':' {
		return "file:///" + normalized
	}
	return "file://" + normalized
}
