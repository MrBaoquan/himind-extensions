package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultMaxItems = 50
	maxMaxItems     = 200
	defaultMaxBytes = 2 << 30
	maxMaxBytes     = 16 << 30
)

type limitedBuffer struct {
	limit int
	buf   bytes.Buffer
	err   error
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.err != nil {
		return 0, b.err
	}
	if b.buf.Len()+len(p) > b.limit {
		b.err = errors.New("输出超过限制")
		return 0, b.err
	}
	return b.buf.Write(p)
}

func clampMaxItems(value int) int {
	if value <= 0 {
		return defaultMaxItems
	}
	if value > maxMaxItems {
		return maxMaxItems
	}
	return value
}

func clampMaxBytes(value int64) int64 {
	if value <= 0 {
		return defaultMaxBytes
	}
	if value > maxMaxBytes {
		return maxMaxBytes
	}
	return value
}

func baseProbeArgs(targetURL string, playlist bool, maxItems int) []string {
	args := []string{
		"--no-config", "--no-config-locations", "--no-cookies",
		"--no-warnings", "--skip-download", "--dump-single-json",
		"--socket-timeout", "15", "--retries", "1",
	}
	if playlist {
		args = append(args, "--flat-playlist", "--playlist-end", strconv.Itoa(clampMaxItems(maxItems)))
	} else {
		args = append(args, "--no-playlist")
	}
	return append(args, targetURL)
}

func probeMedia(ctx context.Context, runtimePath string, input InfoInput) (MediaInfo, error) {
	if _, err := validateMediaURL(input.URL); err != nil {
		return MediaInfo{}, err
	}
	args := baseProbeArgs(strings.TrimSpace(input.URL), input.Playlist, input.MaxItems)
	command := exec.CommandContext(ctx, runtimePath, args...)
	configureHiddenProcess(command)
	var output limitedBuffer
	output.limit = 16 << 20
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	if err != nil {
		detail := strings.TrimSpace(output.buf.String())
		if len(detail) > 1000 {
			detail = detail[len(detail)-1000:]
		}
		if detail == "" {
			detail = err.Error()
		}
		return MediaInfo{}, fmt.Errorf("解析媒体链接失败: %s", detail)
	}
	payload := bytes.TrimSpace(output.buf.Bytes())
	if len(payload) == 0 {
		return MediaInfo{}, errors.New("解析媒体链接失败: 没有返回数据")
	}
	var root map[string]any
	if err := json.Unmarshal(payload, &root); err != nil {
		return MediaInfo{}, fmt.Errorf("解析 yt-dlp 输出失败: %w", err)
	}
	return mediaInfoFromMap(root, input.Playlist), nil
}

func mediaInfoFromMap(root map[string]any, requestedPlaylist bool) MediaInfo {
	result := MediaInfo{
		ID:         asString(root["id"]),
		Title:      firstNonEmpty(asString(root["title"]), asString(root["fulltitle"])),
		Uploader:   firstNonEmpty(asString(root["uploader"]), asString(root["channel"])),
		Extractor:  asString(root["extractor_key"]),
		WebpageURL: firstNonEmpty(asString(root["webpage_url"]), asString(root["original_url"])),
		Thumbnail:  asString(root["thumbnail"]),
	}
	result.DurationSeconds = asFloat(root["duration"])
	entries, hasEntries := root["entries"].([]any)
	result.IsPlaylist = requestedPlaylist || hasEntries
	if hasEntries {
		result.EntryCount = len(entries)
		for _, item := range entries {
			entryMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			result.Entries = append(result.Entries, PlaylistEntry{
				ID:       asString(entryMap["id"]),
				Title:    asString(entryMap["title"]),
				URL:      firstNonEmpty(asString(entryMap["webpage_url"]), asString(entryMap["url"])),
				Uploader: firstNonEmpty(asString(entryMap["uploader"]), asString(entryMap["channel"])),
				Duration: asFloat(entryMap["duration"]),
			})
		}
	}
	result.Formats = formatsFromAny(root["formats"])
	result.Subtitles = subtitleKeys(root["subtitles"])
	return result
}

func formatsFromAny(value any) []FormatInfo {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	formats := make([]FormatInfo, 0, len(items))
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		format := FormatInfo{
			FormatID:       asString(entry["format_id"]),
			Ext:            asString(entry["ext"]),
			Resolution:     asString(entry["resolution"]),
			Width:          asInt(entry["width"]),
			Height:         asInt(entry["height"]),
			VideoCodec:     asString(entry["vcodec"]),
			AudioCodec:     asString(entry["acodec"]),
			Filesize:       asInt64(entry["filesize"]),
			FilesizeApprox: asInt64(entry["filesize_approx"]),
			Note:           asString(entry["format_note"]),
			Protocol:       asString(entry["protocol"]),
		}
		if format.FormatID == "" {
			continue
		}
		formats = append(formats, format)
	}
	sort.SliceStable(formats, func(i, j int) bool {
		if formats[i].Height != formats[j].Height {
			return formats[i].Height > formats[j].Height
		}
		return formats[i].FormatID < formats[j].FormatID
	})
	if len(formats) > 40 {
		formats = formats[:40]
	}
	return formats
}

func subtitleKeys(value any) []string {
	root, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(root))
	for key := range root {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > 40 {
		keys = keys[:40]
	}
	return keys
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func asString(value any) string {
	switch item := value.(type) {
	case string:
		return strings.TrimSpace(item)
	case json.Number:
		return item.String()
	case float64:
		return strconv.FormatFloat(item, 'f', -1, 64)
	case int:
		return strconv.Itoa(item)
	case int64:
		return strconv.FormatInt(item, 10)
	default:
		return ""
	}
}

func asInt(value any) int {
	switch item := value.(type) {
	case float64:
		return int(item)
	case int:
		return item
	case int64:
		return int(item)
	case json.Number:
		number, _ := item.Int64()
		return int(number)
	case string:
		number, _ := strconv.Atoi(strings.TrimSpace(item))
		return number
	default:
		return 0
	}
}

func asInt64(value any) int64 {
	switch item := value.(type) {
	case float64:
		return int64(item)
	case int:
		return int64(item)
	case int64:
		return item
	case json.Number:
		number, _ := item.Int64()
		return number
	case string:
		number, _ := strconv.ParseInt(strings.TrimSpace(item), 10, 64)
		return number
	default:
		return 0
	}
}

func asFloat(value any) float64 {
	switch item := value.(type) {
	case float64:
		return item
	case int:
		return float64(item)
	case int64:
		return float64(item)
	case json.Number:
		number, _ := item.Float64()
		return number
	case string:
		number, _ := strconv.ParseFloat(strings.TrimSpace(item), 64)
		return number
	default:
		return 0
	}
}

func resolveFormat(input JobStartInput, ffmpegAvailable bool) (string, error) {
	if formatID := strings.TrimSpace(input.FormatID); formatID != "" {
		if strings.ContainsAny(formatID, " \t\r\n") {
			return "", errors.New("format_id 不能包含空白字符")
		}
		return formatID, nil
	}
	quality := strings.ToLower(strings.TrimSpace(input.Quality))
	if quality == "" {
		quality = "best"
	}
	if input.AudioOnly {
		if !ffmpegAvailable {
			return "", errors.New("音频提取需要 ffmpeg，请先安装 ffmpeg 或改用普通格式")
		}
		return "bestaudio/best", nil
	}
	if !ffmpegAvailable {
		switch quality {
		case "best":
			return "best[acodec!=none][vcodec!=none]/best", nil
		case "1080p":
			return "best[height<=1080][acodec!=none][vcodec!=none]/best[height<=1080]/best", nil
		case "720p":
			return "best[height<=720][acodec!=none][vcodec!=none]/best[height<=720]/best", nil
		case "480p":
			return "best[height<=480][acodec!=none][vcodec!=none]/best[height<=480]/best", nil
		default:
			return "", fmt.Errorf("不支持的清晰度预设: %s", quality)
		}
	}
	switch quality {
	case "best":
		return "bestvideo*+bestaudio/best", nil
	case "1080p":
		return "bestvideo[height<=1080]+bestaudio/best[height<=1080]", nil
	case "720p":
		return "bestvideo[height<=720]+bestaudio/best[height<=720]", nil
	case "480p":
		return "bestvideo[height<=480]+bestaudio/best[height<=480]", nil
	default:
		return "", fmt.Errorf("不支持的清晰度预设: %s", quality)
	}
}

func buildDownloadArgs(input JobStartInput, destination string, ffmpegAvailable bool) ([]string, error) {
	format, err := resolveFormat(input, ffmpegAvailable)
	if err != nil {
		return nil, err
	}
	maxBytes := clampMaxBytes(input.MaxBytes)
	args := []string{
		"--no-config", "--no-config-locations", "--no-cookies",
		"--newline", "--progress",
		"--progress-template", "download:%(progress._percent_str)s|%(progress._speed_str)s|%(progress._eta_str)s|%(progress.downloaded_bytes)s|%(progress.total_bytes_estimate)s",
		"--socket-timeout", "15",
		"--retries", "3",
		"--fragment-retries", "3",
		"--paths", destination,
		"--output", "%(title).150B [%(id)s].%(ext)s",
		"--write-info-json",
		"--windows-filenames",
		"--max-filesize", strconv.FormatInt(maxBytes, 10),
		"--format", format,
	}
	if input.Overwrite {
		args = append(args, "--force-overwrites")
	} else {
		args = append(args, "--no-overwrites")
	}
	if input.Playlist {
		args = append(args, "--yes-playlist", "--playlist-end", strconv.Itoa(clampMaxItems(input.MaxItems)))
	} else {
		args = append(args, "--no-playlist")
	}
	if input.AudioOnly {
		args = append(args, "--extract-audio", "--audio-format", "mp3")
	}
	if len(input.Subtitles) > 0 {
		langs := make([]string, 0, len(input.Subtitles))
		for _, lang := range input.Subtitles {
			value := strings.TrimSpace(lang)
			if value != "" && !strings.ContainsAny(value, " \t\r\n,") {
				langs = append(langs, value)
			}
		}
		if len(langs) > 0 {
			args = append(args, "--write-subs", "--sub-langs", strings.Join(langs, ","))
		}
	}
	return append(args, strings.TrimSpace(input.URL)), nil
}

func parseProgressLine(line string) (progress float64, downloaded int64, total int64, speed string, eta string, ok bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "download:") {
		return 0, 0, 0, "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(line, "download:"), "|")
	if len(parts) < 5 {
		return 0, 0, 0, "", "", false
	}
	percentText := strings.TrimSpace(strings.TrimSuffix(parts[0], "%"))
	progress, _ = strconv.ParseFloat(percentText, 64)
	downloaded, _ = strconv.ParseInt(strings.TrimSpace(parts[3]), 10, 64)
	total, _ = strconv.ParseInt(strings.TrimSpace(parts[4]), 10, 64)
	speed = strings.TrimSpace(parts[1])
	eta = strings.TrimSpace(parts[2])
	return progress, downloaded, total, speed, eta, true
}

func quickRuntimeProbe(ctx context.Context, runtimePath string) error {
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := runtimeVersion(probeCtx, runtimePath)
	return err
}
