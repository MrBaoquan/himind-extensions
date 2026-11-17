package main

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func writeTestPNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for y := 0; y < 48; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 4), G: uint8(y * 5), B: 160, A: 255})
		}
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, img); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeTestWAV(t *testing.T, path string) {
	t.Helper()
	const sampleRate = 8000
	samples := make([]int16, sampleRate)
	for index := range samples {
		samples[index] = int16(math.Sin(float64(index)*0.08) * 3000)
	}
	dataSize := len(samples) * 2
	buffer := bytes.NewBuffer(nil)
	buffer.WriteString("RIFF")
	_ = binary.Write(buffer, binary.LittleEndian, uint32(36+dataSize))
	buffer.WriteString("WAVEfmt ")
	_ = binary.Write(buffer, binary.LittleEndian, uint32(16))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(1))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(1))
	_ = binary.Write(buffer, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(buffer, binary.LittleEndian, uint32(sampleRate*2))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(2))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(16))
	buffer.WriteString("data")
	_ = binary.Write(buffer, binary.LittleEndian, uint32(dataSize))
	_ = binary.Write(buffer, binary.LittleEndian, samples)
	if err := os.WriteFile(path, buffer.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeTestMP4(t *testing.T, path string) bool {
	t.Helper()
	tools := resolveMediaTools()
	if tools.FFmpeg == "" {
		return false
	}
	command := exec.Command(tools.FFmpeg,
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=1:size=320x240:rate=25",
		"-pix_fmt", "yuv420p", "-movflags", "+faststart", path,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Logf("ffmpeg 生成测试视频失败，跳过视频断言: %v %s", err, string(output))
		return false
	}
	return true
}

func candidateIDByPath(t *testing.T, path string) string {
	t.Helper()
	db, err := openLibraryDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var id string
	if err := db.QueryRow(`SELECT id FROM candidates WHERE path=?`, path).Scan(&id); err != nil {
		t.Fatalf("查询候选失败 %s: %v", path, err)
	}
	return id
}

func TestEnrichLocalMediaCreatesMetadataAndThumbnails(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	root := t.TempDir()
	shoot := filepath.Join(root, "2025年跨年夜骆岗")
	if err := os.MkdirAll(shoot, 0o755); err != nil {
		t.Fatal(err)
	}
	pngPath := filepath.Join(shoot, "still.png")
	wavPath := filepath.Join(shoot, "ambience.wav")
	writeTestPNG(t, pngPath)
	writeTestWAV(t, wavPath)
	mp4Path := filepath.Join(shoot, "DJI_20250411222138_0045_D.mp4")
	hasVideo := writeTestMP4(t, mp4Path)

	created, rpcError := call(t, "media-library.source.add", map[string]any{"path": root, "display_name": "测试素材"})
	if rpcError != nil {
		t.Fatalf("add source: %s", rpcError.Message)
	}
	sourceID := decode(t, created)["id"].(string)
	if _, rpcError := call(t, "media-library.source.scan.batch", map[string]any{
		"source_id": sourceID, "restart": true, "max_entries": 200,
	}); rpcError != nil {
		t.Fatalf("scan: %s", rpcError.Message)
	}

	// 扫描必须写入 folder 与 Windows 文件身份。
	db, err := openLibraryDB()
	if err != nil {
		t.Fatal(err)
	}
	var folder, volumeID, fileID string
	if err := db.QueryRow(`SELECT folder,volume_id,file_id FROM candidates WHERE path=?`, pngPath).Scan(&folder, &volumeID, &fileID); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	if folder != "2025年跨年夜骆岗" {
		t.Fatalf("folder = %q", folder)
	}
	if volumeID == "" || fileID == "" {
		t.Fatalf("expected Windows file identity, got volume=%q file=%q", volumeID, fileID)
	}

	enriched, rpcError := call(t, "media-library.asset.enrich", map[string]any{
		"source_id": sourceID, "limit": 20, "thumbnails": true, "thumbnail_size": "grid",
	})
	if rpcError != nil {
		t.Fatalf("enrich: %s", rpcError.Message)
	}
	payload := decode(t, enriched)
	if errorsList, ok := payload["errors"].([]any); ok && len(errorsList) > 0 {
		t.Logf("enrich errors: %#v", errorsList)
	}
	if payload["remaining"].(float64) != 0 {
		t.Fatalf("expected no remaining items: %#v", payload)
	}
	if payload["thumbed"].(float64) < 2 {
		t.Fatalf("expected thumbnails for image/audio: %#v", payload)
	}
	if summaries, ok := payload["items"].([]any); !ok || len(summaries) < 2 {
		t.Fatalf("expected enrich summaries: %#v", payload)
	}

	detailOf := func(path string) map[string]any {
		result, rpcError := call(t, "media-library.asset.detail", map[string]any{"asset_id": candidateIDByPath(t, path)})
		if rpcError != nil {
			t.Fatalf("detail: %s", rpcError.Message)
		}
		return decode(t, result)
	}
	imageDetail := detailOf(pngPath)
	if imageDetail["width"].(float64) != 64 || imageDetail["height"].(float64) != 48 {
		t.Fatalf("unexpected image dimensions: %#v", imageDetail)
	}
	if imageDetail["probe_state"] != "ok" || imageDetail["thumb_state"] != "ok" {
		t.Fatalf("image not enriched: %#v", imageDetail)
	}
	audioDetail := detailOf(wavPath)
	if audioDetail["duration_ms"].(float64) < 900 || audioDetail["duration_ms"].(float64) > 1100 {
		t.Fatalf("unexpected audio duration: %#v", audioDetail)
	}
	if audioDetail["sample_rate"].(float64) != 8000 || audioDetail["channels"].(float64) != 1 {
		t.Fatalf("unexpected audio metadata: %#v", audioDetail)
	}
	if hasVideo {
		videoDetail := detailOf(mp4Path)
		if videoDetail["width"].(float64) != 320 || videoDetail["height"].(float64) != 240 {
			t.Fatalf("unexpected video dimensions: %#v", videoDetail)
		}
		if videoDetail["captured_at"] == "" {
			t.Fatalf("expected captured_at from filename fallback: %#v", videoDetail)
		}
	}

	ids := []string{candidateIDByPath(t, pngPath), candidateIDByPath(t, wavPath)}
	if hasVideo {
		ids = append(ids, candidateIDByPath(t, mp4Path))
	}
	thumbs, rpcError := call(t, "media-library.asset.thumbnail", map[string]any{"asset_ids": ids, "size": "grid"})
	if rpcError != nil {
		t.Fatalf("thumbnail: %s", rpcError.Message)
	}
	items := decode(t, thumbs)["items"].([]any)
	if len(items) != len(ids) {
		t.Fatalf("expected %d thumbnails, got %d", len(ids), len(items))
	}
	for _, entry := range items {
		item := entry.(map[string]any)
		value, _ := item["data_url"].(string)
		if len(value) < 100 {
			t.Fatalf("thumbnail missing: %#v", item)
		}
	}

	// 搜索结果必须带回新元数据，UI 才能显示分辨率/时长/文件夹。
	searchResult, rpcError := call(t, "media-library.asset.search", map[string]any{
		"query": "DJI", "kinds": []string{"video"}, "limit": 10,
	})
	if rpcError != nil {
		t.Fatalf("search: %s", rpcError.Message)
	}
	found := false
	for _, entry := range decode(t, searchResult)["items"].([]any) {
		item := entry.(map[string]any)
		if item["id"] != candidateIDByPath(t, mp4Path) {
			continue
		}
		found = true
		if item["width"].(float64) != 320 || item["height"].(float64) != 240 {
			t.Fatalf("search lost dimensions: %#v", item)
		}
		if item["duration_ms"].(float64) != 1000 {
			t.Fatalf("search lost duration: %#v", item)
		}
		if item["folder"] != "2025年跨年夜骆岗" {
			t.Fatalf("search lost folder: %#v", item)
		}
	}
	if hasVideo && !found {
		t.Fatalf("video not found in search results")
	}

	// Facets 与筛选：文件夹、最小尺寸、最小时长。
	expected := 2
	if hasVideo {
		expected = 3
	}
	facets, rpcError := call(t, "media-library.asset.facets", map[string]any{})
	if rpcError != nil {
		t.Fatalf("facets: %s", rpcError.Message)
	}
	facetPayload := decode(t, facets)
	if int(facetPayload["total"].(float64)) != expected {
		t.Fatalf("facet total = %v, want %d", facetPayload["total"], expected)
	}
	folderFound := false
	for _, entry := range facetPayload["folders"].([]any) {
		item := entry.(map[string]any)
		if item["value"] == "2025年跨年夜骆岗" && int(item["count"].(float64)) == expected {
			folderFound = true
		}
	}
	if !folderFound {
		t.Fatalf("folder facet missing: %#v", facetPayload["folders"])
	}
	byFolder, rpcError := call(t, "media-library.asset.search", map[string]any{
		"query": "", "folder": "2025年跨年夜骆岗", "limit": 20,
	})
	if rpcError != nil {
		t.Fatalf("search folder: %s", rpcError.Message)
	}
	if int(decode(t, byFolder)["total"].(float64)) != expected {
		t.Fatalf("folder filter count = %v", decode(t, byFolder)["total"])
	}
	byWrongFolder, rpcError := call(t, "media-library.asset.search", map[string]any{
		"query": "", "folder": "不存在的目录", "limit": 20,
	})
	if rpcError != nil {
		t.Fatalf("search wrong folder: %s", rpcError.Message)
	}
	if int(decode(t, byWrongFolder)["total"].(float64)) != 0 {
		t.Fatalf("wrong folder should be empty")
	}
	byDimension, rpcError := call(t, "media-library.asset.search", map[string]any{
		"query": "", "min_dimension": 100, "limit": 20,
	})
	if rpcError != nil {
		t.Fatalf("search dimension: %s", rpcError.Message)
	}
	expectedDimension := 1
	if !hasVideo {
		expectedDimension = 0
	}
	if int(decode(t, byDimension)["total"].(float64)) != expectedDimension {
		t.Fatalf("dimension filter count = %v, want %d", decode(t, byDimension)["total"], expectedDimension)
	}
	byDuration, rpcError := call(t, "media-library.asset.search", map[string]any{
		"query": "", "min_duration_ms": 900, "limit": 20,
	})
	if rpcError != nil {
		t.Fatalf("search duration: %s", rpcError.Message)
	}
	expectedDuration := 1
	if hasVideo {
		expectedDuration = 2
	}
	if int(decode(t, byDuration)["total"].(float64)) != expectedDuration {
		t.Fatalf("duration filter count = %v, want %d", decode(t, byDuration)["total"], expectedDuration)
	}

	// 增量重扫：未变化的文件不应重写 FTS，也不应重置探测状态。
	rescan, rpcError := call(t, "media-library.source.scan.batch", map[string]any{
		"source_id": sourceID, "restart": true, "max_entries": 200,
	})
	if rpcError != nil {
		t.Fatalf("rescan: %s", rpcError.Message)
	}
	if int(decode(t, rescan)["indexed_files"].(float64)) != 0 {
		t.Fatalf("rescan should skip unchanged files: %#v", decode(t, rescan))
	}
	checkDB, err := openLibraryDB()
	if err != nil {
		t.Fatal(err)
	}
	var probeState, thumbState string
	if err := checkDB.QueryRow(`SELECT probe_state,thumb_state FROM candidates WHERE path=?`, pngPath).Scan(&probeState, &thumbState); err != nil {
		checkDB.Close()
		t.Fatal(err)
	}
	checkDB.Close()
	if probeState != "ok" || thumbState != "ok" {
		t.Fatalf("rescan reset enrichment: probe=%s thumb=%s", probeState, thumbState)
	}
}

func TestEnsureV3SchemaIsIdempotent(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	for attempt := 0; attempt < 2; attempt++ {
		db, err := openLibraryDB()
		if err != nil {
			t.Fatalf("open %d: %v", attempt, err)
		}
		columns := map[string]bool{}
		rows, err := db.Query(`PRAGMA table_info(candidates)`)
		if err != nil {
			db.Close()
			t.Fatal(err)
		}
		for rows.Next() {
			var cid, notNull, primaryKey int
			var name, kind string
			var defaultValue sql.NullString
			if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
				rows.Close()
				db.Close()
				t.Fatal(err)
			}
			columns[name] = true
		}
		rows.Close()
		for _, column := range []string{"volume_id", "file_id", "folder", "width", "height", "duration_ms", "probe_state", "thumb_state"} {
			if !columns[column] {
				db.Close()
				t.Fatalf("missing column %s on attempt %d", column, attempt)
			}
		}
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE name='v3_assets_v1'`).Scan(&count); err != nil {
			db.Close()
			t.Fatal(err)
		}
		if count != 1 {
			db.Close()
			t.Fatalf("migration marker count = %d", count)
		}
		db.Close()
	}
}

func TestCaptureTimeFromFilename(t *testing.T) {
	value := captureTimeFromFilename("DJI_20250411222138_0045_D.MP4")
	if value == "" {
		t.Fatal("expected DJI timestamp")
	}
	if _, err := time.Parse(time.RFC3339, value); err != nil {
		t.Fatalf("invalid RFC3339: %s", value)
	}
	if captureTimeFromFilename("random.mp4") != "" {
		t.Fatal("unexpected timestamp")
	}
}
