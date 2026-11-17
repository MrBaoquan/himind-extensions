package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MrBaoquan/himind-extensions/internal/mediadownload"
)

func musicFixture(id string) *mediadownload.MusicInfo {
	return &mediadownload.MusicInfo{Provider: "douyin", ProviderID: id, Title: "同名音乐", Author: "作者", SourceURL: "https://www.douyin.com/music/" + id, Evidence: "platform_metadata"}
}

func TestMusicSampleUsesIdentityAndUniqueVideoCounts(t *testing.T) {
	db, _ := acquisitionTestLibrary(t)
	videos := []remoteResource{{SourceURL: "https://www.douyin.com/video/123456"}, {SourceURL: "https://www.douyin.com/?modal_id=123456"}, {SourceURL: "https://www.douyin.com/video/234567"}, {SourceURL: "https://www.douyin.com/video/345678"}, {SourceURL: "https://www.douyin.com/video/456789"}}
	metadata := []*mediadownload.MusicInfo{musicFixture("7391234567890123456"), musicFixture("7391234567890123456"), musicFixture("7391234567890123456"), musicFixture("7391234567890123457"), nil}
	result, err := recordMusicSample(db, "热门BGM", videos, metadata)
	if err != nil {
		t.Fatal(err)
	}
	groups := result["items"].([]musicResult)
	if result["sample_size"] != 4 || result["identified"] != 3 || result["unidentified"] != 1 || result["basis"] != "search_sample" || len(groups) != 2 || groups[0].Count != 2 || groups[1].Count != 1 {
		t.Fatalf("sample: %+v", result)
	}
	stored, err := musicForVideo(db, "https://www.douyin.com/video/234567?tracking=ignored")
	if err != nil || stored == nil || stored.ProviderID != "7391234567890123456" {
		t.Fatalf("music identity lookup: %+v %v", stored, err)
	}
	// Repeating a page records another observation, without duplicating music or usages.
	if _, err = recordMusicSample(db, "again", videos, metadata); err != nil {
		t.Fatal(err)
	}
	for table, want := range map[string]int{"music_tracks": 2, "video_music_usages": 3, "music_samples": 2} {
		var n int
		if err = db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil || n != want {
			t.Fatalf("%s: %d %v", table, n, err)
		}
	}
}

func TestMusicSampleRejectsUntrustedIdentityAtomically(t *testing.T) {
	db, _ := acquisitionTestLibrary(t)
	invalid := musicFixture("7391234567890123457")
	invalid.Evidence = "guessed_title"
	_, err := recordMusicSample(db, "test", []remoteResource{{SourceURL: "https://www.douyin.com/video/123456"}, {SourceURL: "https://www.douyin.com/video/234567"}}, []*mediadownload.MusicInfo{musicFixture("7391234567890123456"), invalid})
	if err == nil {
		t.Fatal("untrusted identity accepted")
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM music_tracks`).Scan(&n)
	if n != 0 {
		t.Fatal("partial sample survived rollback")
	}
}

func TestAudioCompletedReceiptRecoversIntoLibraryWithProvenance(t *testing.T) {
	db, library := acquisitionTestLibrary(t)
	source := filepath.Join(t.TempDir(), "keep-source.mp4")
	if err := os.WriteFile(source, []byte("source must be preserved"), 0600); err != nil {
		t.Fatal(err)
	}
	hash, _ := hashFile(source)
	input := acquisitionStartInput{Mode: "import", LibraryID: library.ID, IdempotencyKey: "audio-recovery-fixture", Purpose: "bgm", Audio: &audioProcess{SourceAssetID: "source-asset", SourceSHA256: hash, SourceKind: "video", SourceExtension: ".mp4", StartMS: 1000, EndMS: 3000, Format: "wav"}}
	input.Title = "恢复音频"
	record, created, err := createAcquisition(db, input)
	if err != nil || !created {
		t.Fatal(err)
	}
	same, created, err := createAcquisition(db, input)
	if err != nil || created || same.ID != record.ID || same.TaskKind != "audio" || same.Purpose != "bgm" {
		t.Fatalf("intent replay: %+v %v", same, err)
	}
	root, _ := pluginDataRoot()
	staging := filepath.Join(root, "acquisition-staging", record.ID)
	_ = os.MkdirAll(staging, 0700)
	output, receipt := renderPaths(staging, 1, "wav")
	_ = os.WriteFile(output, []byte("verified audio fixture"), 0600)
	r := renderReceipt{ID: "toolkit-job", Status: "completed", Output: output, DurationMS: 2000, UpdatedAt: nowRFC3339()}
	data, _ := json.Marshal(r)
	_ = os.WriteFile(receipt, data, 0600)
	if _, err = db.Exec(`INSERT INTO audio_renders(acquisition_id,attempt,child_job_id) VALUES(?,1,'toolkit-job')`, record.ID); err != nil {
		t.Fatal(err)
	}
	// Resume solely from durable task and toolkit receipt: no broker or network.
	t.Setenv("HIMIND_CAPABILITY_BROKER_URL", "")
	t.Setenv("HIMIND_CAPABILITY_BROKER_TOKEN", "")
	if err = runAcquisitionWorker(record.ID); err != nil {
		t.Fatal(err)
	}
	done, err := loadAcquisition(db, record.ID)
	if err != nil || done.Status != "completed" || len(done.Files) != 1 || done.Files[0].LibraryItemID == "" {
		t.Fatalf("completion: %+v %v", done, err)
	}
	derivations, err := audioProvenance(db, done.Files[0].LibraryItemID)
	if err != nil || len(derivations) != 1 || derivations[0]["source_asset_id"] != "source-asset" || derivations[0]["source_sha256"] != hash || derivations[0]["role"] != "audio_excerpt" {
		t.Fatalf("provenance: %+v %v", derivations, err)
	}
	result, err := searchResources(resourceSearchInput{AudioPurpose: "bgm", Scope: "library", Kinds: []string{"audio"}, MinDurationMS: 1500, MaxDurationMS: 2500})
	if err != nil || len(result.(map[string]any)["items"].([]resourceSearchItem)) != 1 {
		t.Fatalf("BGM search: %+v %v", result, err)
	}
	for _, filter := range []resourceSearchInput{{AudioPurpose: "bgm", Scope: "sources"}, {AudioPurpose: "bgm", Kinds: []string{"video"}}, {AudioPurpose: "bgm", MinDurationMS: 3000}} {
		v, e := searchResources(filter)
		if e != nil || len(v.(map[string]any)["items"].([]resourceSearchItem)) != 0 {
			t.Fatalf("filter ignored: %+v %v", v, e)
		}
	}
	if digest, e := hashFile(source); e != nil || digest != hash {
		t.Fatal("source changed", e)
	}
	// A second worker cannot create another derivation or consume the source.
	if err = runAcquisitionWorker(record.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM audio_derivations`).Scan(&n)
	if n != 1 {
		t.Fatal("duplicate derivation")
	}
}

func TestAudioReceiptRejectsMismatchAndInvalidRange(t *testing.T) {
	for _, r := range [][2]int64{{-1, 200}, {0, -1}, {0, 99}, {200, 100}, {100, 0}, {0, 86400001}} {
		if validateAudioRange(r[0], r[1], "wav") == nil {
			t.Fatalf("range accepted: %v", r)
		}
	}
	if validateAudioRange(0, 0, "flac") == nil {
		t.Fatal("unsupported encoder accepted")
	}
	db, library := acquisitionTestLibrary(t)
	in := acquisitionStartInput{Mode: "import", LibraryID: library.ID, IdempotencyKey: "mismatched-output", Purpose: "bgm", Audio: &audioProcess{SourceAssetID: "source", Format: "wav"}}
	record, _, err := createAcquisition(db, in)
	if err != nil {
		t.Fatal(err)
	}
	if ok, e := claimAcquisition(db, record.ID, "owner", time.Now()); e != nil || !ok {
		t.Fatal(e)
	}
	root, _ := pluginDataRoot()
	staging := filepath.Join(root, "acquisition-staging", record.ID)
	_ = os.MkdirAll(staging, 0700)
	_, receipt := renderPaths(staging, 1, "wav")
	data, _ := json.Marshal(renderReceipt{ID: "toolkit-job", Status: "completed", Output: filepath.Join(t.TempDir(), "outside.wav"), DurationMS: 1000, UpdatedAt: nowRFC3339()})
	_ = os.WriteFile(receipt, data, 0600)
	_, _ = db.Exec(`INSERT INTO audio_renders(acquisition_id,attempt) VALUES(?,1)`, record.ID)
	if _, err = waitAudioRender(context.Background(), db, record, "owner", staging); err == nil {
		t.Fatal("mismatched receipt accepted")
	}
}
