package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strings"
	"time"
)

// A bounded PCM sink refuses excess output before it can grow in memory.
type waveformPCM struct{ data []byte }

func (w *waveformPCM) Write(p []byte) (int, error) {
	if len(w.data)+len(p) > 8000*2*1800+4096 {
		return 0, fmt.Errorf("波形数据超过上限")
	}
	w.data = append(w.data, p...)
	return len(p), nil
}
func waveformPeaks(pcm []byte, bins int) []float64 {
	count := len(pcm) / 2
	if count == 0 || bins < 1 {
		return nil
	}
	if bins > count {
		bins = count
	}
	peaks := make([]float64, bins)
	for i := 0; i < count; i++ {
		n := math.Abs(float64(int16(binary.LittleEndian.Uint16(pcm[i*2:])))) / 32768
		b := int(int64(i) * int64(bins) / int64(count))
		if n > peaks[b] {
			peaks[b] = n
		}
	}
	return peaks
}
func audioWaveform(in userStateInput) (any, error) {
	if strings.TrimSpace(in.AssetID) == "" {
		return nil, fmt.Errorf("请选择已关联的本机音视频")
	}
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var path, kind string
	err = db.QueryRow(`SELECT local_path,kind FROM library_items WHERE id=? AND deleted_at=''
 UNION ALL SELECT path,kind FROM candidates WHERE id=? AND missing=0 LIMIT 1`, in.AssetID, in.AssetID).Scan(&path, &kind)
	if err != nil || (kind != "video" && kind != "audio") {
		return nil, fmt.Errorf("该资源无法生成音频波形")
	}
	stat, err := os.Stat(path)
	if err != nil || !stat.Mode().IsRegular() || stat.Size() > 2<<30 {
		return nil, fmt.Errorf("源文件不可用或超过2GB，可继续使用时间标记")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	info, err := probeFile(ctx, path, kind)
	if err != nil || info.DurationMS <= 0 || info.DurationMS > 1800000 {
		return nil, fmt.Errorf("波形支持30分钟内的音视频，可继续使用时间标记")
	}
	tools := resolveMediaTools()
	if tools.FFmpeg == "" {
		return nil, fmt.Errorf("缺少ffmpeg，可继续使用时间标记")
	}
	cmd := exec.CommandContext(ctx, tools.FFmpeg, "-hide_banner", "-loglevel", "error", "-nostdin", "-i", path, "-vn", "-map", "0:a:0", "-ac", "1", "-ar", "8000", "-t", "1800", "-f", "s16le", "pipe:1")
	var out waveformPCM
	cmd.Stdout = &out
	if err = cmd.Run(); err != nil {
		return nil, fmt.Errorf("波形暂不可用，可继续使用时间标记与试听")
	}
	peaks := waveformPeaks(out.data, 640)
	if len(peaks) == 0 {
		return nil, fmt.Errorf("未找到可用音轨")
	}
	return map[string]any{"duration_ms": info.DurationMS, "peaks": peaks}, nil
}
