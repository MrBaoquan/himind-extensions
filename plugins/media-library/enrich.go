package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 本文件实现 v3 的“媒体理解”第一批能力：
//   - runtime.status：ffmpeg/ffprobe/exiftool 是否就绪
//   - asset.enrich：批量探测元数据、生成缩略图/波形
//   - asset.detail：读取单个资产的完整元数据
//   - asset.thumbnail：批量返回缩略图 data URL
//
// 这一批先把“图片/视频/音频都能被看懂、能出图”打通；作业化、搜索 facets
// 和精选工作台在后续里程碑接入。

type mediaTools struct {
	FFmpeg         string
	FFprobe        string
	ExifTool       string
	FFmpegVersion  string
	FFprobeVersion string
	ExifVersion    string
}

var (
	mediaToolsOnce   sync.Once
	mediaToolsCached mediaTools
)

func resolveMediaTools() mediaTools {
	mediaToolsOnce.Do(func() {
		tools := mediaTools{
			FFmpeg:   findExecutable("HIMIND_MEDIA_FFMPEG", "ffmpeg.exe", "ffmpeg"),
			FFprobe:  findExecutable("HIMIND_MEDIA_FFPROBE", "ffprobe.exe", "ffprobe"),
			ExifTool: findExecutable("HIMIND_MEDIA_EXIFTOOL", "exiftool.exe", "exiftool"),
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		tools.FFmpegVersion = toolVersion(ctx, tools.FFmpeg, "-version")
		tools.FFprobeVersion = toolVersion(ctx, tools.FFprobe, "-version")
		tools.ExifVersion = toolVersion(ctx, tools.ExifTool, "-ver")
		mediaToolsCached = tools
	})
	return mediaToolsCached
}

func findExecutable(envName string, names ...string) string {
	if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
		if info, err := os.Stat(value); err == nil && !info.IsDir() {
			return value
		}
	}
	var bases []string
	if executable, err := os.Executable(); err == nil {
		dir := filepath.Dir(executable)
		bases = append(bases,
			dir,
			filepath.Dir(dir),
			filepath.Join(filepath.Dir(dir), "bin"),
			filepath.Join(filepath.Dir(filepath.Dir(dir)), "bin"),
		)
	}
	for _, base := range bases {
		for _, name := range names {
			candidate := filepath.Join(base, name)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate
			}
		}
	}
	for _, name := range names {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}

func toolVersion(ctx context.Context, path string, args ...string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	command := exec.CommandContext(ctx, path, args...)
	output, err := command.Output()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(strings.SplitN(string(output), "\n", 2)[0])
	if len(line) > 160 {
		line = line[:160]
	}
	return line
}

func runtimeStatus() (any, error) {
	tools := resolveMediaTools()
	missing := []string{}
	if tools.FFmpeg == "" {
		missing = append(missing, "ffmpeg")
	}
	if tools.FFprobe == "" {
		missing = append(missing, "ffprobe")
	}
	entry := func(path, version string) map[string]any {
		return map[string]any{"path": path, "version": version, "available": path != ""}
	}
	return map[string]any{
		"platform":  runtime.GOOS + "/" + runtime.GOARCH,
		"ffmpeg":    entry(tools.FFmpeg, tools.FFmpegVersion),
		"ffprobe":   entry(tools.FFprobe, tools.FFprobeVersion),
		"exiftool":  entry(tools.ExifTool, tools.ExifVersion),
		"available": tools.FFmpeg != "" && tools.FFprobe != "",
		"missing":   missing,
		"message":   firstNonEmpty(runtimeMessage(missing), "媒体运行时已就绪"),
	}, nil
}

func runtimeMessage(missing []string) string {
	if len(missing) == 0 {
		return ""
	}
	return "缺少 " + strings.Join(missing, "、") + "；安装并加入 PATH 后可恢复元数据与缩略图，文件名检索不受影响。"
}

type probeInfo struct {
	Width      int
	Height     int
	DurationMS int64
	FPS        float64
	Codec      string
	BitDepth   int
	HDR        string
	Rotation   int
	SampleRate int
	Channels   int
	Bitrate    int64
	CapturedAt string
	Format     string
	Tags       map[string]string
	ProbedAt   string
}

type ffprobeDocument struct {
	Streams []struct {
		CodecType        string            `json:"codec_type"`
		CodecName        string            `json:"codec_name"`
		Width            int               `json:"width"`
		Height           int               `json:"height"`
		Duration         string            `json:"duration"`
		RFrameRate       string            `json:"r_frame_rate"`
		AvgFrameRate     string            `json:"avg_frame_rate"`
		SampleRate       string            `json:"sample_rate"`
		Channels         int               `json:"channels"`
		BitRate          string            `json:"bit_rate"`
		BitsPerRawSample string            `json:"bits_per_raw_sample"`
		BitsPerSample    int               `json:"bits_per_sample"`
		ColorTransfer    string            `json:"color_transfer"`
		Tags             map[string]string `json:"tags"`
		SideDataList     []struct {
			Rotation float64 `json:"rotation"`
		} `json:"side_data_list"`
	} `json:"streams"`
	Format struct {
		Duration   string            `json:"duration"`
		BitRate    string            `json:"bit_rate"`
		FormatName string            `json:"format_name"`
		Tags       map[string]string `json:"tags"`
	} `json:"format"`
}

func probeFile(ctx context.Context, path string, kind string) (probeInfo, error) {
	tools := resolveMediaTools()
	if tools.FFprobe == "" {
		return probeInfo{}, fmt.Errorf("未找到 ffprobe，请安装并加入 PATH")
	}
	command := exec.CommandContext(ctx, tools.FFprobe,
		"-v", "error",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		path,
	)
	payload, err := command.Output()
	if err != nil {
		if ctx.Err() != nil {
			return probeInfo{}, fmt.Errorf("探测超时")
		}
		return probeInfo{}, fmt.Errorf("ffprobe 读取失败: %v", err)
	}
	var document ffprobeDocument
	if err := json.Unmarshal(payload, &document); err != nil {
		return probeInfo{}, fmt.Errorf("ffprobe 输出无法解析: %w", err)
	}
	info := probeInfo{
		Format:   document.Format.FormatName,
		Tags:     map[string]string{},
		ProbedAt: nowRFC3339(),
	}
	for key, value := range document.Format.Tags {
		info.Tags[strings.ToLower(key)] = value
	}
	if document.Format.Duration != "" {
		info.DurationMS = durationMilliseconds(document.Format.Duration)
	}
	if document.Format.BitRate != "" {
		info.Bitrate = parseInt64(document.Format.BitRate)
	}
	videoIndex, audioIndex := -1, -1
	for index := range document.Streams {
		if document.Streams[index].CodecType == "video" && videoIndex < 0 {
			videoIndex = index
		}
		if document.Streams[index].CodecType == "audio" && audioIndex < 0 {
			audioIndex = index
		}
	}
	if videoIndex >= 0 {
		video := document.Streams[videoIndex]
		info.Width = video.Width
		info.Height = video.Height
		info.Codec = firstNonEmpty(info.Codec, video.CodecName)
		if video.Duration != "" && info.DurationMS == 0 {
			info.DurationMS = durationMilliseconds(video.Duration)
		}
		if info.FPS == 0 {
			info.FPS = parseFrameRate(firstNonEmpty(video.RFrameRate, video.AvgFrameRate))
		}
		info.BitDepth = firstNonZero(parseInt(video.BitsPerRawSample), video.BitsPerSample)
		info.HDR = hdrLabel(video.ColorTransfer)
		if len(video.SideDataList) > 0 && video.SideDataList[0].Rotation != 0 {
			info.Rotation = int(video.SideDataList[0].Rotation)
		} else if rotate := parseInt(video.Tags["rotate"]); rotate != 0 {
			info.Rotation = rotate
		}
		if info.Bitrate == 0 {
			info.Bitrate = parseInt64(video.BitRate)
		}
		if info.CapturedAt == "" {
			info.CapturedAt = firstNonEmpty(video.Tags["creation_time"], video.Tags["date"])
		}
	}
	if audioIndex >= 0 {
		audio := document.Streams[audioIndex]
		info.SampleRate = parseInt(audio.SampleRate)
		info.Channels = audio.Channels
		if info.Bitrate == 0 {
			info.Bitrate = parseInt64(audio.BitRate)
		}
		if info.Codec == "" {
			info.Codec = audio.CodecName
		}
		if info.CapturedAt == "" {
			info.CapturedAt = firstNonEmpty(audio.Tags["creation_time"], audio.Tags["date"])
		}
	}
	if kind == "audio" && info.Width == 0 {
		info.Height = 0
	}
	if kind == "image" {
		info.DurationMS = 0
	}
	if info.CapturedAt != "" {
		if parsed, ok := normalizeMetadataTime(info.CapturedAt); ok {
			info.CapturedAt = parsed
		}
	}
	if info.CapturedAt == "" {
		info.CapturedAt = captureTimeFromFilename(filepath.Base(path))
	}
	if info.CapturedAt == "" {
		if stat, err := os.Stat(path); err == nil {
			info.CapturedAt = stat.ModTime().Format(time.RFC3339)
		}
	}
	return info, nil
}

func normalizeMetadataTime(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	layouts := []string{
		time.RFC3339Nano, time.RFC3339,
		"2006-01-02T15:04:05.000000Z",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006:01:02 15:04:05",
	}
	for _, layout := range layouts {
		if parsed, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return parsed.Format(time.RFC3339), true
		}
	}
	return value, false
}

var filenameDatePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(?:dji|gopro|gopr|img|vid|dsc)[_-]?(\d{8})[_-]?(\d{6})`),
	regexp.MustCompile(`(\d{8})[_-](\d{6})`),
}

func captureTimeFromFilename(name string) string {
	for _, pattern := range filenameDatePatterns {
		match := pattern.FindStringSubmatch(name)
		if len(match) < 3 {
			continue
		}
		if parsed, err := time.ParseInLocation("20060102150405", match[1]+match[2], time.Local); err == nil {
			return parsed.Format(time.RFC3339)
		}
	}
	return ""
}

func durationMilliseconds(value string) int64 {
	seconds, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || seconds <= 0 {
		return 0
	}
	return int64(seconds * 1000)
}

func parseFrameRate(value string) float64 {
	value = strings.TrimSpace(value)
	if value == "" || value == "0/0" {
		return 0
	}
	if index := strings.Index(value, "/"); index > 0 {
		numerator, err1 := strconv.ParseFloat(value[:index], 64)
		denominator, err2 := strconv.ParseFloat(value[index+1:], 64)
		if err1 == nil && err2 == nil && denominator != 0 {
			return numerator / denominator
		}
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func hdrLabel(colorTransfer string) string {
	switch strings.ToLower(strings.TrimSpace(colorTransfer)) {
	case "smpte2084", "smpte-st-2084":
		return "HDR10"
	case "arib-std-b67":
		return "HLG"
	default:
		return ""
	}
}

func parseInt(value string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return parsed
}

func parseInt64(value string) int64 {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func firstNonZero(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

// ---------------------------------------------------------------------------
// 缩略图与波形
// ---------------------------------------------------------------------------

func thumbnailDirectory() (string, error) {
	root, err := pluginDataRoot()
	if err != nil {
		return "", err
	}
	directory := filepath.Join(root, "thumbs")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", err
	}
	return directory, nil
}

func thumbnailSizeWidth(size string) int {
	if size == "preview" {
		return 960
	}
	return 320
}

func thumbnailCacheKey(assetID string, modifiedAt string) string {
	digest := sha256.Sum256([]byte(assetID + "|" + modifiedAt))
	return hex.EncodeToString(digest[:12])
}

func generateThumbnail(ctx context.Context, assetPath string, kind string, durationMS int64, size string, output string) error {
	tools := resolveMediaTools()
	if tools.FFmpeg == "" {
		return fmt.Errorf("未找到 ffmpeg")
	}
	width := thumbnailSizeWidth(size)
	args := []string{"-hide_banner", "-loglevel", "error", "-y"}
	switch kind {
	case "video":
		seek := 1.0
		if durationMS > 0 && durationMS < 2000 {
			seek = float64(durationMS) / 2000.0
		}
		if seek < 0 {
			seek = 0
		}
		args = append(args, "-ss", strconv.FormatFloat(seek, 'f', 3, 64), "-i", assetPath)
	case "audio":
		args = append(args,
			"-i", assetPath,
			"-filter_complex", fmt.Sprintf("showwavespic=s=%dx96:colors=0x7aa2f7", width),
			"-frames:v", "1",
			output,
		)
		return runFFmpeg(ctx, tools.FFmpeg, args)
	default:
		args = append(args, "-i", assetPath)
	}
	args = append(args,
		"-frames:v", "1",
		"-vf", fmt.Sprintf("scale=%d:-2:force_original_aspect_ratio=decrease", width),
		"-q:v", "4",
		output,
	)
	return runFFmpeg(ctx, tools.FFmpeg, args)
}

func runFFmpeg(ctx context.Context, executable string, args []string) error {
	command := exec.CommandContext(ctx, executable, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		if len(message) > 300 {
			message = message[:300]
		}
		return fmt.Errorf("ffmpeg 生成缩略图失败: %s", message)
	}
	return nil
}

func ensureThumbnail(ctx context.Context, db *sql.DB, assetID string, assetPath string, kind string, modifiedAt string, durationMS int64, size string) (string, error) {
	if size != "grid" && size != "preview" {
		size = "grid"
	}
	var cachedPath, cachedMtime string
	err := db.QueryRow(`SELECT path,source_mtime FROM thumb_cache WHERE asset_id=? AND size=?`, assetID, size).Scan(&cachedPath, &cachedMtime)
	if err == nil && cachedMtime == modifiedAt {
		if info, statErr := os.Stat(cachedPath); statErr == nil && info.Size() > 0 {
			_, _ = db.Exec(`UPDATE thumb_cache SET last_used_at=? WHERE asset_id=? AND size=?`, nowRFC3339(), assetID, size)
			return cachedPath, nil
		}
	}
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	directory, err := thumbnailDirectory()
	if err != nil {
		return "", err
	}
	extension := ".jpg"
	if kind == "audio" {
		extension = ".png"
	}
	target := filepath.Join(directory, thumbnailCacheKey(assetID, modifiedAt)+"-"+size+extension)
	timeoutCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if err := generateThumbnail(timeoutCtx, assetPath, kind, durationMS, size, target); err != nil {
		return "", err
	}
	stat, err := os.Stat(target)
	if err != nil || stat.Size() == 0 {
		return "", fmt.Errorf("缩略图为空")
	}
	if _, err := db.Exec(`INSERT INTO thumb_cache(asset_id,size,path,bytes,source_mtime,last_used_at)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(asset_id,size) DO UPDATE SET path=excluded.path,bytes=excluded.bytes,
		source_mtime=excluded.source_mtime,last_used_at=excluded.last_used_at`,
		assetID, size, target, stat.Size(), modifiedAt, nowRFC3339()); err != nil {
		return "", err
	}
	return target, nil
}

// ---------------------------------------------------------------------------
// asset.enrich
// ---------------------------------------------------------------------------

type enrichInput struct {
	SourceID      string   `json:"source_id"`
	AssetIDs      []string `json:"asset_ids"`
	Limit         int      `json:"limit"`
	TimeBudgetMS  int      `json:"time_budget_ms"`
	Thumbnails    bool     `json:"thumbnails"`
	ThumbnailSize string   `json:"thumbnail_size"`
}

type enrichCandidate struct {
	ID           string
	SourceID     string
	Path         string
	Kind         string
	Bytes        int64
	ModifiedAt   string
	VolumeID     string
	FileID       string
	RelativePath string
	ProbeState   string
	ThumbState   string
	DurationMS   int64
}

func enrichAssets(in enrichInput) (any, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	budget := in.TimeBudgetMS
	if budget <= 0 {
		budget = 20_000
	}
	if budget > 120_000 {
		budget = 120_000
	}
	if in.ThumbnailSize != "preview" {
		in.ThumbnailSize = "grid"
	}
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	candidates, err := loadEnrichCandidates(db, in, limit)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	errorsOut := []map[string]any{}
	processedIDs := []string{}
	probed := 0
	thumbed := 0
	processed := 0
	for _, candidate := range candidates {
		if time.Since(started) > time.Duration(budget)*time.Millisecond {
			break
		}
		processed++
		processedIDs = append(processedIDs, candidate.ID)
		if _, statErr := os.Stat(candidate.Path); statErr != nil {
			_, _ = db.Exec(`UPDATE candidates SET missing=1, updated_at=? WHERE id=?`, nowRFC3339(), candidate.ID)
			errorsOut = append(errorsOut, map[string]any{"id": candidate.ID, "name": filepath.Base(candidate.Path), "error": "文件不可用"})
			continue
		}
		if candidate.VolumeID == "" || candidate.FileID == "" {
			identity := identifyFile(candidate.Path)
			candidate.VolumeID = identity.VolumeID
			candidate.FileID = identity.FileID
		}
		if candidate.ProbeState != "ok" {
			probeCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			info, probeErr := probeFile(probeCtx, candidate.Path, candidate.Kind)
			cancel()
			if probeErr != nil {
				_, _ = db.Exec(`UPDATE candidates SET probe_state='failed', meta_json=?, updated_at=? WHERE id=?`,
					errorMetadata(probeErr), nowRFC3339(), candidate.ID)
				errorsOut = append(errorsOut, map[string]any{"id": candidate.ID, "name": filepath.Base(candidate.Path), "error": probeErr.Error()})
			} else {
				if err := saveProbeResult(db, candidate, info); err != nil {
					return nil, err
				}
				candidate.DurationMS = info.DurationMS
				probed++
			}
		}
		if in.Thumbnails {
			thumbPath := ""
			var thumbErr error
			if candidate.Kind == "image" || candidate.Kind == "video" || candidate.Kind == "audio" {
				thumbPath, thumbErr = ensureThumbnail(context.Background(), db, candidate.ID, candidate.Path, candidate.Kind, candidate.ModifiedAt, candidate.DurationMS, in.ThumbnailSize)
			}
			if thumbErr != nil {
				_, _ = db.Exec(`UPDATE candidates SET thumb_state='failed', updated_at=? WHERE id=?`, nowRFC3339(), candidate.ID)
				errorsOut = append(errorsOut, map[string]any{"id": candidate.ID, "name": filepath.Base(candidate.Path), "error": thumbErr.Error()})
			} else if thumbPath != "" {
				_, _ = db.Exec(`UPDATE candidates SET thumb_state='ok', updated_at=? WHERE id=?`, nowRFC3339(), candidate.ID)
				thumbed++
			}
		}
	}
	remaining, _ := countEnrichRemaining(db, in)
	items := []map[string]any{}
	for _, id := range processedIDs {
		summary, summaryErr := loadAssetSummary(db, id)
		if summaryErr == nil {
			items = append(items, summary)
		}
	}
	return map[string]any{
		"processed":  processed,
		"probed":     probed,
		"thumbed":    thumbed,
		"failed":     len(errorsOut),
		"remaining":  remaining,
		"elapsed_ms": time.Since(started).Milliseconds(),
		"errors":     errorsOut,
		"items":      items,
	}, nil
}

func loadAssetSummary(db *sql.DB, id string) (map[string]any, error) {
	var (
		name, kind, folder, codec, hdr, capturedAt, probeState, thumbState string
		bytes, width, height, durationMS                                   int64
		fps                                                                float64
		bitDepth                                                           int64
	)
	err := db.QueryRow(`SELECT name,kind,folder,codec,hdr,captured_at,probe_state,thumb_state,bytes,width,height,duration_ms,fps,bit_depth
		FROM candidates WHERE id=?`, id).Scan(&name, &kind, &folder, &codec, &hdr, &capturedAt, &probeState, &thumbState,
		&bytes, &width, &height, &durationMS, &fps, &bitDepth)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"id": id, "name": name, "kind": kind, "folder": folder, "codec": codec, "hdr": hdr,
		"captured_at": capturedAt, "probe_state": probeState, "thumb_state": thumbState,
		"bytes": bytes, "width": width, "height": height, "duration_ms": durationMS,
		"fps": fps, "bit_depth": bitDepth,
	}, nil
}

func loadEnrichCandidates(db *sql.DB, in enrichInput, limit int) ([]enrichCandidate, error) {
	clause := "missing=0 AND (probe_state='pending' OR thumb_state='pending')"
	if !in.Thumbnails {
		clause = "missing=0 AND probe_state='pending'"
	}
	args := []any{}
	if len(in.AssetIDs) > 0 {
		placeholder, idArgs := placeholders(in.AssetIDs)
		clause = "missing=0 AND id IN (" + placeholder + ")"
		args = append(args, idArgs...)
	} else if strings.TrimSpace(in.SourceID) != "" {
		clause += " AND source_id=?"
		args = append(args, strings.TrimSpace(in.SourceID))
	}
	args = append(args, limit)
	query := `SELECT id,source_id,path,kind,bytes,modified_at,volume_id,file_id,relative_path,probe_state,thumb_state,duration_ms
		FROM candidates WHERE ` + clause + ` ORDER BY modified_at DESC LIMIT ?`
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []enrichCandidate{}
	for rows.Next() {
		var item enrichCandidate
		if err := rows.Scan(&item.ID, &item.SourceID, &item.Path, &item.Kind, &item.Bytes, &item.ModifiedAt,
			&item.VolumeID, &item.FileID, &item.RelativePath, &item.ProbeState, &item.ThumbState, &item.DurationMS); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func countEnrichRemaining(db *sql.DB, in enrichInput) (int, error) {
	clause := "missing=0 AND (probe_state='pending' OR thumb_state='pending')"
	if !in.Thumbnails {
		clause = "missing=0 AND probe_state='pending'"
	}
	args := []any{}
	if len(in.AssetIDs) > 0 {
		placeholder, idArgs := placeholders(in.AssetIDs)
		clause += " AND id IN (" + placeholder + ")"
		args = append(args, idArgs...)
	} else if strings.TrimSpace(in.SourceID) != "" {
		clause += " AND source_id=?"
		args = append(args, strings.TrimSpace(in.SourceID))
	}
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM candidates WHERE `+clause, args...).Scan(&count)
	return count, err
}

func saveProbeResult(db *sql.DB, candidate enrichCandidate, info probeInfo) error {
	metadata, _ := json.Marshal(map[string]any{
		"format": info.Format,
		"tags":   info.Tags,
		"probed": info.ProbedAt,
	})
	_, err := db.Exec(`UPDATE candidates SET
		volume_id=?, file_id=?, width=?, height=?, duration_ms=?, fps=?, codec=?, bit_depth=?, hdr=?, rotation=?,
		sample_rate=?, channels=?, bitrate=?, captured_at=?, probe_state='ok', meta_json=?, updated_at=?
		WHERE id=?`,
		candidate.VolumeID, candidate.FileID, info.Width, info.Height, info.DurationMS, info.FPS,
		info.Codec, info.BitDepth, info.HDR, info.Rotation, info.SampleRate, info.Channels,
		info.Bitrate, info.CapturedAt, string(metadata), nowRFC3339(), candidate.ID)
	return err
}

func errorMetadata(err error) string {
	payload, _ := json.Marshal(map[string]any{"error": err.Error()})
	return string(payload)
}

// ---------------------------------------------------------------------------
// asset.detail / asset.thumbnail
// ---------------------------------------------------------------------------

type assetDetailInput struct {
	AssetID string `json:"asset_id"`
}

func assetDetail(in assetDetailInput) (any, error) {
	id := strings.TrimSpace(in.AssetID)
	if id == "" {
		return nil, fmt.Errorf("请指定资产")
	}
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var item struct {
		ID, SourceID, Path, RelativePath, Folder, Name, Kind, Extension  string
		ModifiedAt, VolumeID, FileID, CapturedAt, ProbeState, ThumbState string
		Codec, HDR, MetaJSON                                             string
		Bytes, Width, Height, DurationMS, BitDepth, Rotation             int64
		SampleRate, Channels                                             int64
		FPS, Bitrate                                                     float64
	}
	err = db.QueryRow(`SELECT id,source_id,path,relative_path,folder,name,kind,extension,bytes,modified_at,
		volume_id,file_id,captured_at,probe_state,thumb_state,width,height,duration_ms,fps,bit_depth,rotation,
		sample_rate,channels,bitrate,codec,hdr,meta_json FROM candidates WHERE id=?`, id).Scan(
		&item.ID, &item.SourceID, &item.Path, &item.RelativePath, &item.Folder, &item.Name, &item.Kind,
		&item.Extension, &item.Bytes, &item.ModifiedAt, &item.VolumeID, &item.FileID,
		&item.CapturedAt, &item.ProbeState, &item.ThumbState, &item.Width, &item.Height, &item.DurationMS,
		&item.FPS, &item.BitDepth, &item.Rotation, &item.SampleRate, &item.Channels, &item.Bitrate,
		&item.Codec, &item.HDR, &item.MetaJSON)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("资产不存在")
	}
	if err != nil {
		return nil, err
	}
	metadata := map[string]any{}
	_ = json.Unmarshal([]byte(item.MetaJSON), &metadata)
	return map[string]any{
		"id": item.ID, "source_id": item.SourceID, "path": item.Path, "relative_path": item.RelativePath,
		"folder": item.Folder, "name": item.Name, "kind": item.Kind, "extension": item.Extension,
		"bytes": item.Bytes, "modified_at": item.ModifiedAt, "volume_id": item.VolumeID, "file_id": item.FileID,
		"captured_at": item.CapturedAt, "probe_state": item.ProbeState, "thumb_state": item.ThumbState,
		"width": item.Width, "height": item.Height, "duration_ms": item.DurationMS, "fps": item.FPS,
		"bit_depth": item.BitDepth, "rotation": item.Rotation, "sample_rate": item.SampleRate,
		"channels": item.Channels, "bitrate": item.Bitrate, "codec": item.Codec, "hdr": item.HDR,
		"metadata": metadata,
	}, nil
}

type assetThumbnailInput struct {
	AssetIDs []string `json:"asset_ids"`
	Size     string   `json:"size"`
}

func assetThumbnails(in assetThumbnailInput) (any, error) {
	if len(in.AssetIDs) == 0 {
		return map[string]any{"items": []any{}}, nil
	}
	if len(in.AssetIDs) > 60 {
		in.AssetIDs = in.AssetIDs[:60]
	}
	if in.Size != "preview" {
		in.Size = "grid"
	}
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	items := []map[string]any{}
	for _, id := range in.AssetIDs {
		var path, kind, modifiedAt string
		var durationMS int64
		if err := db.QueryRow(`SELECT path,kind,modified_at,duration_ms FROM candidates WHERE id=?`, id).Scan(&path, &kind, &modifiedAt, &durationMS); err != nil {
			items = append(items, map[string]any{"id": id, "error": "资产不存在"})
			continue
		}
		cached, err := ensureThumbnail(context.Background(), db, id, path, kind, modifiedAt, durationMS, in.Size)
		if err != nil {
			items = append(items, map[string]any{"id": id, "error": err.Error()})
			continue
		}
		dataURL, err := fileDataURL(cached)
		if err != nil {
			items = append(items, map[string]any{"id": id, "error": err.Error()})
			continue
		}
		items = append(items, map[string]any{"id": id, "data_url": dataURL, "path": cached, "asset_path": path, "kind": kind})
	}
	return map[string]any{"items": items}, nil
}

func fileDataURL(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		return "", fmt.Errorf("缩略图不存在")
	}
	if info.Size() > 2<<20 {
		return "", fmt.Errorf("缩略图过大")
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	mimeType := "image/jpeg"
	if strings.EqualFold(filepath.Ext(path), ".png") {
		mimeType = "image/png"
	}
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(payload), nil
}
