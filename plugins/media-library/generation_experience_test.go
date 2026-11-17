package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerationUnknownSubmissionNeverReplays(t *testing.T) {
	s, _, _, calls := generationFixture(t, "image.generate")
	generationAgent = func(cap string, input any) (map[string]any, error) {
		if cap == "media.job.start" {
			*calls++
			return nil, fmt.Errorf("timeout")
		}
		return nil, fmt.Errorf("offline")
	}
	if _, err := submitGeneration(generationInput{SessionID: s.ID}); err == nil {
		t.Fatal("expected unknown outcome")
	}
	value, err := submitGeneration(generationInput{SessionID: s.ID})
	if err != nil {
		t.Fatal(err)
	}
	got := value.(generationSession)
	if *calls != 1 || got.SubmissionState != "unknown" || got.QueryState != "unavailable" {
		t.Fatalf("calls=%d snapshot=%#v", *calls, got)
	}
	if got.Prompt != s.Prompt {
		t.Fatal("saved input lost")
	}
}
func TestGenerationSubmittedRecordIsIdempotentAndQueryable(t *testing.T) {
	s, _, _, calls := generationFixture(t, "image.generate")
	for i := 0; i < 2; i++ {
		if _, err := submitGeneration(generationInput{SessionID: s.ID}); err != nil {
			t.Fatal(err)
		}
	}
	if *calls != 1 {
		t.Fatalf("starts=%d", *calls)
	}
}
func TestGenerationTargetProjectionDoesNotChangeSavedTarget(t *testing.T) {
	s, library, _, _ := generationFixture(t, "image.generate")
	if _, err := submitGeneration(generationInput{SessionID: s.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := adoptGeneration(generationInput{SessionID: s.ID, LibraryID: library.ID, Indexes: []int{0}}); err != nil {
		t.Fatal(err)
	}
	value, err := getGeneration(generationInput{SessionID: s.ID, LibraryID: "another-library"})
	if err != nil {
		t.Fatal(err)
	}
	got := value.(generationSession)
	if got.Outputs[0]["adoption_state"] != "pending" {
		t.Fatal("wrong target receipt")
	}
	db, err := openLibraryDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	saved, err := loadGeneration(db, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.LibraryID != library.ID {
		t.Fatal("view changed saved target")
	}
	value, err = getGeneration(generationInput{SessionID: s.ID, LibraryID: library.ID})
	if err != nil {
		t.Fatal(err)
	}
	if value.(generationSession).Outputs[0]["adoption_state"] != "adopted" {
		t.Fatal("original receipt missing")
	}
}
func TestGenerationOutputReferenceIsTrustedAndHashChecked(t *testing.T) {
	s, _, job, calls := generationFixture(t, "image.generate")
	value, err := prepareGeneration(generationInput{Capability: "image.edit", Prompt: "调整背景", OutputReferences: []generationOutputReference{{SessionID: s.ID, Index: 0}}})
	if err != nil {
		t.Fatal(err)
	}
	child := value.(generationSession)
	if child.References[0]["session_id"] != s.ID || len(child.Input["input_images"].([]string)) != 1 {
		t.Fatal("trusted parent missing")
	}
	output := job["outputs"].([]any)[0].(map[string]any)
	_ = os.WriteFile(output["path"].(string), []byte("changed"), 0600)
	if _, err = prepareGeneration(generationInput{Capability: "image.edit", Prompt: "调整背景", OutputReferences: []generationOutputReference{{SessionID: s.ID, Index: 0}}}); err == nil {
		t.Fatal("changed reference accepted")
	}
	if *calls != 0 {
		t.Fatal("preparing a reference must not generate")
	}
}
func TestGenerationAutoAdoptionIsNotPromised(t *testing.T) {
	if _, err := prepareGeneration(generationInput{Capability: "image.generate", Prompt: "图片", AutoAdopt: true}); err == nil || !strings.Contains(err.Error(), "预览结果后入库") {
		t.Fatalf("%v", err)
	}
}
func TestGenerationAvailabilitySeparatesUnsupportedAndTemporaryErrors(t *testing.T) {
	unsupported := classifyGenerationError(500, "capability not found: media.job.list")
	state, _ := generationExecutionAvailability(unsupported)
	if state != "unsupported" {
		t.Fatal(state)
	}
	for _, err := range []error{fmt.Errorf("timeout"), classifyGenerationError(503, "invocation limit reached")} {
		state, msg := generationExecutionAvailability(err)
		if state == "unsupported" || strings.Contains(msg, "更新") {
			t.Fatal("temporary outage misdiagnosed as version problem")
		}
	}
}

func TestGenerationBrokerMessageClassifiesMissingExecution(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(409)
		_, _ = w.Write([]byte(`{"error":"capability_failed","message":"capability not found: media.job.list"}`))
	}))
	defer server.Close()
	t.Setenv("HIMIND_CAPABILITY_BROKER_URL", server.URL+"/invoke")
	t.Setenv("HIMIND_CAPABILITY_BROKER_TOKEN", "test-only")
	_, err := invokeGenerationAgent("media.job.list", map[string]any{})
	state, message := generationExecutionAvailability(err)
	if state != "unsupported" || !strings.Contains(message, "更新") {
		t.Fatalf("%s %s", state, message)
	}
}

func TestReferencePreviewResolvesRegisteredIDWithoutArbitraryPath(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	root := t.TempDir()
	path := filepath.Join(root, "reference.png")
	if err := os.WriteFile(path, []byte("registered image"), 0600); err != nil {
		t.Fatal(err)
	}
	created, rpcError := call(t, "media-library.source.add", map[string]any{"path": root})
	if rpcError != nil {
		t.Fatal(rpcError)
	}
	if _, rpcError = call(t, "media-library.source.scan.batch", map[string]any{"source_id": decode(t, created)["id"], "restart": true, "max_entries": 100}); rpcError != nil {
		t.Fatal(rpcError)
	}
	id := candidateIDByPath(t, path)
	value, err := previewAsset(previewInput{AssetID: id, Kind: "image"})
	if err != nil {
		t.Fatal(err)
	}
	if value.(map[string]any)["path"] != path {
		t.Fatal("resolved wrong file")
	}
	if _, err = previewAsset(previewInput{AssetID: "unregistered"}); err == nil {
		t.Fatal("unknown asset accepted")
	}
	if _, err = previewAsset(previewInput{Path: filepath.Join(t.TempDir(), "unknown.png")}); err == nil {
		t.Fatal("arbitrary file accepted")
	}
}
