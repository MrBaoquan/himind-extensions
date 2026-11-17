package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MrBaoquan/himind-extensions/internal/mediadownload"
)

type audioStartInput struct {
	AssetID        string `json:"asset_id"`
	LibraryID      string `json:"library_id"`
	Title          string `json:"title,omitempty"`
	StartMS        int64  `json:"start_ms"`
	EndMS          int64  `json:"end_ms"`
	Format         string `json:"format"`
	IdempotencyKey string `json:"idempotency_key"`
}

// Audio processing intents contain an indexed identity and a source fingerprint,
// never a caller-controlled path. Only a private copy crosses the toolkit boundary.
type audioProcess struct {
	SourceAssetID   string `json:"source_asset_id"`
	SourceSHA256    string `json:"source_sha256"`
	SourceURL       string `json:"source_url,omitempty"`
	SourceKind      string `json:"source_kind"`
	SourceExtension string `json:"source_extension"`
	MusicID         string `json:"music_id,omitempty"`
	StartMS         int64  `json:"start_ms"`
	EndMS           int64  `json:"end_ms"`
	Format          string `json:"format"`
}

func validateAudioRange(start, end int64, format string) error {
	if start < 0 || end < 0 || start > 86400000 || end > 86400000 || (end > 0 && end-start < 100) {
		return fmt.Errorf("片段结束时间需晚于开始时间至少 0.1 秒，最长 24 小时")
	}
	if end == 0 && start > 0 {
		return fmt.Errorf("请填写片段结束时间")
	}
	if format != "wav" && format != "mp3" && format != "m4a" {
		return fmt.Errorf("音频格式需为 WAV、MP3 或 M4A")
	}
	return nil
}

func startAudio(in audioStartInput) (any, error) {
	if in.Format == "" {
		in.Format = "wav"
	}
	if err := validateAudioRange(in.StartMS, in.EndMS, in.Format); err != nil {
		return nil, err
	}
	if len([]rune(in.Title)) > 300 {
		return nil, fmt.Errorf("素材名称最多 300 字")
	}
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var path, kind, title, sourceURL string
	err = db.QueryRow(`SELECT local_path,kind,title,source_url FROM library_items WHERE id=? AND deleted_at=''
 UNION ALL SELECT path,kind,name,'' FROM candidates WHERE id=? AND missing=0 LIMIT 1`, in.AssetID, in.AssetID).Scan(&path, &kind, &title, &sourceURL)
	if err != nil {
		return nil, fmt.Errorf("请从已关联的本地视频或音频发起提取")
	}
	if kind != "video" && kind != "audio" {
		return nil, fmt.Errorf("请选择视频或音频素材")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("源素材文件不可用")
	}
	if info.Size() > 2<<30 {
		return nil, fmt.Errorf("当前支持提取不超过 2 GB 的源文件")
	}
	hash, err := hashFile(path)
	if err != nil {
		return nil, fmt.Errorf("无法读取源素材")
	}
	root, err := pluginDataRoot()
	if err != nil {
		return nil, err
	}
	a := &audioProcess{SourceAssetID: in.AssetID, SourceSHA256: hash, SourceURL: sourceURL, SourceKind: kind, SourceExtension: strings.ToLower(filepath.Ext(path)), StartMS: in.StartMS, EndMS: in.EndMS, Format: in.Format}
	_ = db.QueryRow(`SELECT music_id FROM audio_derivations WHERE asset_id=? AND music_id<>'' LIMIT 1`, in.AssetID).Scan(&a.MusicID)
	if a.MusicID == "" && sourceURL != "" {
		if m, e := musicForVideo(db, sourceURL); e == nil && m != nil {
			a.MusicID = stableID("music", m.Provider+":"+m.ProviderID)
		}
	}
	intent := acquisitionStartInput{JobStartInput: mediadownload.JobStartInput{Title: firstNonEmpty(strings.TrimSpace(in.Title), title+" · 音频"), DestDir: root}, Mode: "import", LibraryID: in.LibraryID, IdempotencyKey: in.IdempotencyKey, Purpose: "bgm", Audio: a}
	record, created, err := createAcquisition(db, intent)
	if err != nil {
		return nil, err
	}
	if !created {
		return record, nil
	}
	staging := filepath.Join(root, "acquisition-staging", record.ID)
	if err = os.MkdirAll(staging, 0700); err == nil {
		err = commitAcquisitionCopy(context.Background(), path, filepath.Join(staging, "source"+a.SourceExtension), hash)
	}
	if err == nil {
		err = submitAudioRender(db, record, staging)
	}
	if err == nil {
		err = mediadownload.StartLibraryWorker(record.ID, root)
	}
	if err != nil {
		_, _ = db.Exec(`UPDATE acquisition_jobs SET status='failed',error_code='AUDIO_START_FAILED',message='音频任务未启动，请确认媒体处理工具已启用后重试',updated_at=? WHERE id=? AND lease_until=0`, nowRFC3339(), record.ID)
		signalAudioCancel(db, record.ID)
		return nil, fmt.Errorf("音频任务未启动：%w", err)
	}
	return loadAcquisition(db, record.ID)
}

type renderReceipt struct {
	DurationMS int64   `json:"duration_ms,omitempty"`
	ID         string  `json:"id"`
	Status     string  `json:"status"`
	Progress   float64 `json:"progress"`
	Output     string  `json:"output"`
	UpdatedAt  string  `json:"updated_at"`
}

func invokeMediaProcessing(input any) (json.RawMessage, error) {
	return invokeMediaProcessingCapability("media.toolkit.recipe.run", input)
}

func invokeMediaProcessingCapability(capability string, input any) (json.RawMessage, error) {
	if capability != "media.toolkit.recipe.run" && capability != "media.toolkit.image.convert" {
		return nil, fmt.Errorf("不支持的媒体处理能力")
	}
	endpoint, token := os.Getenv("HIMIND_CAPABILITY_BROKER_URL"), os.Getenv("HIMIND_CAPABILITY_BROKER_TOKEN")
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Path != "/invoke" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() || token == "" {
		return nil, fmt.Errorf("媒体处理调用通道不可用，请从生产 Agent 重新打开媒体库")
	}
	data, _ := json.Marshal(map[string]any{"capability": capability, "input": input})
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("媒体处理请求无效")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 90 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("媒体处理工具未响应，请稍后重试")
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("无法读取媒体处理结果")
	}
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("媒体处理工具不可用，请确认已启用且版本不低于 2.3.3")
	}
	var reply struct {
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal(body, &reply) != nil || len(reply.Result) == 0 {
		return nil, fmt.Errorf("媒体处理工具返回无效结果")
	}
	return reply.Result, nil
}

func renderPaths(staging string, attempt int, format string) (string, string) {
	return filepath.Join(staging, fmt.Sprintf("audio-%d.%s", attempt, format)), filepath.Join(staging, fmt.Sprintf("receipt-%d.json", attempt))
}

func readRenderReceipt(path string) (renderReceipt, error) {
	var r renderReceipt
	info, err := os.Stat(path)
	if err != nil {
		return r, err
	}
	if info.Size() > 4096 {
		return r, fmt.Errorf("处理回执无效")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}
	err = json.Unmarshal(data, &r)
	return r, err
}

func submitAudioRender(db *sql.DB, record acquisitionRecord, staging string) error {
	a := record.Input.Audio
	if a == nil {
		return nil
	}
	var attempt int
	_ = db.QueryRow(`SELECT attempt FROM audio_renders WHERE acquisition_id=?`, record.ID).Scan(&attempt)
	if attempt > 0 {
		output, receipt := renderPaths(staging, attempt, a.Format)
		if r, e := readRenderReceipt(receipt); e == nil {
			if r.Status == "completed" && r.Output == output {
				return nil
			}
			at, _ := time.Parse(time.RFC3339, r.UpdatedAt)
			if (r.Status == "queued" || r.Status == "running") && time.Since(at) < 45*time.Second {
				return nil
			}
		} else if stat, e := os.Stat(filepath.Join(staging, fmt.Sprintf("submission-%d", attempt))); e == nil && time.Since(stat.ModTime()) < 2*time.Minute {
			return nil
		}
	}
	source := filepath.Join(staging, "source"+a.SourceExtension)
	digest, err := hashFile(source)
	if err != nil || digest != a.SourceSHA256 {
		return fmt.Errorf("源素材暂存副本缺失或变化，请重新发起提取")
	}
	attempt++
	output, receipt := renderPaths(staging, attempt, a.Format)
	if _, err = db.Exec(`INSERT INTO audio_renders(acquisition_id,attempt) VALUES(?,?) ON CONFLICT(acquisition_id) DO UPDATE SET attempt=excluded.attempt,child_job_id=''`, record.ID, attempt); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(staging, fmt.Sprintf("submission-%d", attempt)), []byte(nowRFC3339()), 0600); err != nil {
		return err
	}
	codec := map[string]string{"wav": "pcm_s16le", "mp3": "libmp3lame", "m4a": "aac"}[a.Format]
	audio := map[string]any{"encode": map[string]any{"codec": codec}}
	if a.EndMS > 0 {
		audio["trim"] = map[string]any{"start_ms": a.StartMS, "end_ms": a.EndMS}
	}
	input := map[string]any{"workspace_root": staging, "kind": "library-audio", "receipt_path": receipt, "recipe": map[string]any{"inputs": []map[string]any{{"path": source}}, "video": map[string]any{"disable": true}, "audio": audio, "output": map[string]any{"path": output}}}
	result, err := invokeMediaProcessing(input)
	if err != nil {
		return err
	}
	var reply struct {
		Job struct {
			ID string `json:"id"`
		} `json:"job"`
	}
	if json.Unmarshal(result, &reply) != nil || reply.Job.ID == "" {
		return fmt.Errorf("媒体处理任务未创建")
	}
	_, err = db.Exec(`UPDATE audio_renders SET child_job_id=? WHERE acquisition_id=? AND attempt=?`, reply.Job.ID, record.ID, attempt)
	return err
}

func waitAudioRender(ctx context.Context, db *sql.DB, record acquisitionRecord, owner, staging string) ([]acquisitionFile, error) {
	var attempt int
	if err := db.QueryRow(`SELECT attempt FROM audio_renders WHERE acquisition_id=?`, record.ID).Scan(&attempt); err != nil {
		return nil, fmt.Errorf("处理任务未提交，请重试")
	}
	output, receipt := renderPaths(staging, attempt, record.Input.Audio.Format)
	deadline := time.Now().Add(2 * time.Hour)
	started := time.Now()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			_ = os.WriteFile(receipt+".cancel", []byte("cancel"), 0600)
			return nil, ctx.Err()
		}
		r, err := readRenderReceipt(receipt)
		if err == nil {
			if r.Output != output {
				return nil, fmt.Errorf("音频处理输出与任务不一致")
			}
			switch r.Status {
			case "completed":
				real, e := filepath.EvalSymlinks(output)
				if e != nil || !isPathInside(staging, real) {
					return nil, fmt.Errorf("音频处理输出不可用")
				}
				if r.DurationMS <= 0 {
					return nil, fmt.Errorf("音频处理产物时长无效，请重试")
				}
				return []acquisitionFile{{Path: output, DurationMS: r.DurationMS}}, nil
			case "failed":
				return nil, fmt.Errorf("音频提取失败，请确认所选文件包含音轨，且片段范围在文件时长内")
			case "cancelled", "interrupted":
				return nil, fmt.Errorf("音频处理已停止，可以重试")
			}
			at, e := time.Parse(time.RFC3339, r.UpdatedAt)
			if e != nil || time.Since(at) > 60*time.Second {
				return nil, fmt.Errorf("音频处理心跳已停止，可以重试")
			}
			progress := r.Progress
			if progress < 0 || progress > 100 {
				progress = 0
			}
			_ = setAcquisitionPhase(db, record.ID, owner, "verifying", progress, nil)
		} else if time.Since(started) > 2*time.Minute {
			return nil, fmt.Errorf("未收到媒体处理回执，可以重试")
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("音频处理超时，可以重试")
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
}

func acquisitionSource(record acquisitionRecord) (string, string, string, error) {
	if a := record.Input.Audio; a != nil {
		return "local", a.SourceAssetID, a.SourceURL, nil
	}
	return canonicalRemoteReference(record.URL)
}

func signalAudioCancel(db *sql.DB, id string) {
	record, err := loadAcquisition(db, id)
	if err != nil || record.Input.Audio == nil {
		return
	}
	var attempt int
	if db.QueryRow(`SELECT attempt FROM audio_renders WHERE acquisition_id=?`, id).Scan(&attempt) != nil {
		return
	}
	root, err := pluginDataRoot()
	if err != nil {
		return
	}
	_, receipt := renderPaths(filepath.Join(root, "acquisition-staging", id), attempt, record.Input.Audio.Format)
	_ = os.WriteFile(receipt+".cancel", []byte("cancel"), 0600)
}
