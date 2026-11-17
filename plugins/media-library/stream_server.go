package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 媒体流服务：插件进程内的小型回环 HTTP 服务，只服务“已索引的资产”，
// 支持 HTTP Range，供 WebView 的 <video>/<audio> 边下边播与拖动。
//
// 这是 M1 的过渡实现：Agent 统一媒体端点（design-v3 的 D1）落地后，
// 只需把 HIMIND_MEDIA_STREAM_BASE/TOKEN 注入进来，UI 不用改。
var (
	streamOnce     sync.Once
	streamBaseURL  string
	streamToken    string
	streamStartErr error
)

func ensureStreamServer() (string, string, error) {
	streamOnce.Do(func() {
		if base := strings.TrimSpace(os.Getenv("HIMIND_MEDIA_STREAM_BASE")); base != "" {
			streamBaseURL = strings.TrimRight(base, "/")
			streamToken = strings.TrimSpace(os.Getenv("HIMIND_MEDIA_STREAM_TOKEN"))
			if streamToken == "" {
				streamStartErr = fmt.Errorf("外部媒体流缺少 token")
			}
			return
		}
		random := make([]byte, 32)
		if _, err := rand.Read(random); err != nil {
			streamStartErr = fmt.Errorf("生成媒体流令牌失败: %w", err)
			return
		}
		streamToken = hex.EncodeToString(random)
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			streamStartErr = fmt.Errorf("启动媒体流服务失败: %w", err)
			return
		}
		mux := http.NewServeMux()
		mux.HandleFunc("/media", handleStreamMedia)
		server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		go func() {
			_ = server.Serve(listener)
		}()
		streamBaseURL = "http://" + listener.Addr().String()
	})
	return streamBaseURL, streamToken, streamStartErr
}

func streamURLFor(path string, assetID string) string {
	if signed := signedStreamURL(path); signed != "" {
		return signed
	}
	assetID = strings.TrimSpace(assetID)
	if assetID == "" {
		return ""
	}
	base, token, err := ensureStreamServer()
	if err != nil || base == "" || token == "" {
		return ""
	}
	query := url.Values{}
	query.Set("token", token)
	query.Set("id", assetID)
	return base + "/media?" + query.Encode()
}

// signedStreamURL 使用 Agent 注入的每机密钥签发短时票据；Agent 端点只读、
// 只监听回环，支持 Range。没有注入时返回空，由调用方回退到本机回环/文件 URL。
func signedStreamURL(path string) string {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("HIMIND_MEDIA_STREAM_BASE")), "/")
	secret := strings.TrimSpace(os.Getenv("HIMIND_MEDIA_STREAM_SECRET"))
	if base == "" || secret == "" || strings.TrimSpace(path) == "" {
		return ""
	}
	payload := struct {
		Path    string `json:"p"`
		Expires int64  `json:"e"`
		Plugin  string `json:"id"`
	}{
		Path: path,
		// 票据按整点对齐：同一小时内重复打开同一素材会得到同一个 URL，
		// WebView 的媒体缓存才能复用；过期仍在 2–3 小时内。
		Expires: (time.Now().Unix()/3600 + 3) * 3600,
		Plugin:  dataNamespace,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(encoded))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return base + "/plugin-media?ticket=" + url.QueryEscape(encoded+"."+signature)
}

func handleStreamMedia(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := r.URL.Query().Get("token")
	if subtle.ConstantTimeCompare([]byte(token), []byte(streamToken)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		http.Error(w, "missing asset id", http.StatusBadRequest)
		return
	}
	path, err := streamableAssetPath(id)
	if err != nil {
		http.Error(w, "asset not available", http.StatusNotFound)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		http.Error(w, "asset not available", http.StatusNotFound)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.Error(w, "asset not available", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", mediaMimeType(path))
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), file)
}

func streamableAssetPath(id string) (string, error) {
	db, err := openLibraryDB()
	if err != nil {
		return "", err
	}
	defer db.Close()
	if strings.HasPrefix(id, "generation:") {
		parts := strings.Split(id, ":")
		var index int
		if len(parts) != 3 {
			return "", fmt.Errorf("生成结果引用无效")
		}
		if _, err := fmt.Sscanf(parts[2], "%d", &index); err != nil {
			return "", err
		}
		s, err := loadGeneration(db, parts[1])
		if err != nil || index < 0 || index >= len(s.Outputs) {
			return "", fmt.Errorf("生成结果不存在")
		}
		path, _ := s.Outputs[index]["path"].(string)
		return path, nil
	}
	var path string
	err = db.QueryRow(`SELECT local_path FROM library_items WHERE id=?
		UNION ALL SELECT path FROM candidates WHERE id=? AND missing=0 LIMIT 1`, id, id).Scan(&path)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("资产不存在")
	}
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("资产路径为空")
	}
	return path, nil
}

func mediaMimeType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4", ".m4v", ".mov":
		return "video/mp4"
	case ".mkv":
		return "video/x-matroska"
	case ".webm":
		return "video/webm"
	case ".avi":
		return "video/x-msvideo"
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".flac":
		return "audio/flac"
	case ".m4a", ".aac":
		return "audio/mp4"
	case ".ogg", ".opus":
		return "audio/ogg"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".bmp":
		return "image/bmp"
	case ".avif":
		return "image/avif"
	case ".svg":
		return "image/svg+xml"
	default:
		return "application/octet-stream"
	}
}
