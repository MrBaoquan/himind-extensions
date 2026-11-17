package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func generationFixture(t *testing.T, capability string) (generationSession, *libraryRecord, map[string]any, *int) {
	t.Helper()
	db, library := acquisitionTestLibrary(t)
	if err := ensureGenerationSchema(db); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "candidate.png")
	if err := os.WriteFile(path, []byte("verified generated content"), 0600); err != nil {
		t.Fatal(err)
	}
	digest, _ := hashFile(path)
	old := generationAgent
	t.Cleanup(func() { generationAgent = old })
	calls := new(int)
	var job map[string]any
	generationAgent = func(cap string, input any) (map[string]any, error) {
		data, _ := json.Marshal(input)
		var args map[string]any
		_ = json.Unmarshal(data, &args)
		switch cap {
		case "media.job.start":
			*calls++
			return job, nil
		case "media.job.get":
			return job, nil
		default:
			return nil, fmt.Errorf("unexpected capability %s", cap)
		}
	}
	value, err := prepareGeneration(generationInput{Capability: capability, Prompt: strings.Repeat("提示词", 100), LibraryID: library.ID})
	if err != nil {
		t.Fatal(err)
	}
	s := value.(generationSession)
	job = map[string]any{"job_id": s.JobID, "source": generationSource, "request_key": s.ID, "status": "succeeded", "result": map[string]any{"model": "fixture-model", "usage": map[string]any{"state": "unknown"}}, "outputs": []any{map[string]any{"path": path, "sha256": digest, "size_bytes": float64(len("verified generated content")), "mime_type": "image/png"}}}
	return s, library, job, calls
}

func TestGenerationAdoptionIsIdempotentAndPreservesCompleteManifest(t *testing.T) {
	s, library, _, calls := generationFixture(t, "image.generate")
	for i := 0; i < 2; i++ {
		value, err := adoptGeneration(generationInput{SessionID: s.ID, LibraryID: library.ID, Indexes: []int{0}})
		if err != nil {
			t.Fatal(err)
		}
		items := value.(map[string]any)["items"].([]map[string]any)
		if items[0]["state"] != "adopted" {
			t.Fatalf("%#v", items)
		}
	}
	if *calls != 0 {
		t.Fatal("adoption must not generate")
	}
	db, err := openLibraryDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	_ = db.QueryRow(`SELECT COUNT(*) FROM library_items WHERE library_id=?`, library.ID).Scan(&count)
	if count != 1 {
		t.Fatalf("assets=%d", count)
	}
	var manifest string
	if err = db.QueryRow(`SELECT manifest_json FROM asset_derivations WHERE session_id=?`, s.ID).Scan(&manifest); err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err = json.Unmarshal([]byte(manifest), &value); err != nil {
		t.Fatal(err)
	}
	if value["input"].(map[string]any)["prompt"] != s.Prompt {
		t.Fatal("prompt was truncated")
	}
	if !strings.Contains(manifest, "fixture-model") {
		t.Fatal("actual model missing")
	}
	_ = db.QueryRow(`SELECT COUNT(*) FROM asset_derivations WHERE session_id=?`, s.ID).Scan(&count)
	if count != 1 {
		t.Fatalf("derivations=%d", count)
	}
}

func TestGenerationFailedAdoptionRetriesExistingResultOnly(t *testing.T) {
	s, library, job, calls := generationFixture(t, "image.generate")
	outputs := job["outputs"].([]any)
	output := outputs[0].(map[string]any)
	path := output["path"].(string)
	if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := adoptGeneration(generationInput{SessionID: s.ID, LibraryID: library.ID, Indexes: []int{0}})
	if err != nil {
		t.Fatal(err)
	}
	if result.(map[string]any)["items"].([]map[string]any)[0]["state"] != "failed" {
		t.Fatal("unverified output was adopted")
	}
	_ = os.WriteFile(path, []byte("verified generated content"), 0600)
	result, err = adoptGeneration(generationInput{SessionID: s.ID, LibraryID: library.ID, Indexes: []int{0}})
	if err != nil {
		t.Fatal(err)
	}
	if result.(map[string]any)["items"].([]map[string]any)[0]["state"] != "adopted" {
		t.Fatalf("retry: %#v", result)
	}
	if *calls != 0 {
		t.Fatal("retry submitted a model request")
	}
}

func TestImageEditKeepsParentAndRecordsReferenceHash(t *testing.T) {
	_, library, job, calls := generationFixture(t, "image.generate")
	original := filepath.Join(t.TempDir(), "original.png")
	content := []byte("original image content")
	if err := os.WriteFile(original, content, 0600); err != nil {
		t.Fatal(err)
	}
	digest, _ := hashFile(original)
	db, err := openLibraryDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO library_items(id,library_id,sha256,relative_path,local_path,kind,title,mime,bytes,source_provider,source_id,adopted_by,adopted_at,updated_at,metadata_json) VALUES('parent',?,?,?,?,'image','原图','image/png',?,'local','','test',?,?,'{}')`, library.ID, digest, "original.png", original, len(content), nowRFC3339(), nowRFC3339())
	if err != nil {
		t.Fatal(err)
	}
	invoke := generationAgent
	reference := filepath.Join(t.TempDir(), "reference.png")
	generationAgent = func(cap string, input any) (map[string]any, error) {
		if cap != "media.asset.import" {
			return invoke(cap, input)
		}
		args := input.(map[string]any)
		if args["path"] != original || args["sha256"] != digest {
			t.Fatal("wrong reference submitted")
		}
		if err := os.WriteFile(reference, content, 0600); err != nil {
			t.Fatal(err)
		}
		return map[string]any{"path": reference, "sha256": digest}, nil
	}
	prepared, err := prepareGeneration(generationInput{Capability: "image.edit", Prompt: "将背景调整为白色", References: []string{"parent"}, LibraryID: library.ID})
	if err != nil {
		t.Fatal(err)
	}
	s := prepared.(generationSession)
	job["job_id"], job["request_key"] = s.JobID, s.ID
	result, err := adoptGeneration(generationInput{SessionID: s.ID, LibraryID: library.ID, Indexes: []int{0}})
	if err != nil {
		t.Fatal(err)
	}
	if result.(map[string]any)["items"].([]map[string]any)[0]["state"] != "adopted" {
		t.Fatalf("%#v", result)
	}
	if actual, err := hashFile(original); err != nil || actual != digest {
		t.Fatal("original was changed")
	}
	var manifest string
	if err := db.QueryRow(`SELECT manifest_json FROM asset_derivations WHERE session_id=?`, s.ID).Scan(&manifest); err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(manifest), &record); err != nil {
		t.Fatal(err)
	}
	parent := record["references"].([]any)[0].(map[string]any)
	if parent["asset_id"] != "parent" || parent["sha256"] != digest {
		t.Fatal("parent lineage missing")
	}
	if *calls != 0 {
		t.Fatal("adoption submitted a new model call")
	}
}

func TestGenerationRejectsForeignJobAndUnknownResultIndex(t *testing.T) {
	s, library, job, _ := generationFixture(t, "image.generate")
	job["source"] = "another-plugin"
	if _, err := adoptGeneration(generationInput{SessionID: s.ID, LibraryID: library.ID, Indexes: []int{0}}); err == nil {
		t.Fatal("foreign job accepted")
	}
	job["source"] = generationSource
	if _, err := adoptGeneration(generationInput{SessionID: s.ID, LibraryID: library.ID, Indexes: []int{9}}); err == nil {
		t.Fatal("unknown result accepted")
	}
}

func TestGeneratedSpeechIsVoiceoverAndKeepsOriginalResult(t *testing.T) {
	s, library, job, _ := generationFixture(t, "audio.speech")
	output := job["outputs"].([]any)[0].(map[string]any)
	original := output["path"].(string)
	path := strings.TrimSuffix(original, ".png") + ".mp3"
	if err := os.Rename(original, path); err != nil {
		t.Fatal(err)
	}
	output["path"] = path
	output["mime_type"] = "audio/mpeg"
	result, err := adoptGeneration(generationInput{SessionID: s.ID, LibraryID: library.ID, Indexes: []int{0}})
	if err != nil {
		t.Fatal(err)
	}
	item := result.(map[string]any)["items"].([]map[string]any)[0]
	if item["state"] != "adopted" {
		t.Fatalf("%#v", item)
	}
	db, err := openLibraryDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var role, purpose string
	if err = db.QueryRow(`SELECT role,purpose FROM audio_derivations WHERE asset_id=?`, item["asset_id"]).Scan(&role, &purpose); err != nil {
		t.Fatal(err)
	}
	if role != "voiceover" || purpose == "bgm" {
		t.Fatal("speech entered music workflow")
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("candidate was removed")
	}
}
