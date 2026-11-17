package main

import (
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func editorTestPNG(t *testing.T, path string, w, h int) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	f, e := os.Create(path)
	if e != nil {
		t.Fatal(e)
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), A: 255})
		}
	}
	if e = png.Encode(f, img); e != nil {
		t.Fatal(e)
	}
	f.Close()
}
func TestImageEditorImmutableVersionsReplayAndAdoption(t *testing.T) {
	db, library := acquisitionTestLibrary(t)
	if e := ensureImageEditorSchema(db); e != nil {
		t.Fatal(e)
	}
	root, _ := pluginDataRoot()
	src := filepath.Join(root, "original.png")
	editorTestPNG(t, src, 40, 20)
	hash, _ := hashFile(src)
	s := imageEditSession{ID: "editor-fixture", SourceID: "original-id", SourceSHA256: hash, Title: "原始图片", CreatedAt: nowRFC3339()}
	_, e := db.Exec(`INSERT INTO image_edit_sessions(id,source_id,source_sha256,title,created_at) VALUES(?,?,?,?,?)`, s.ID, s.SourceID, hash, s.Title, s.CreatedAt)
	if e != nil {
		t.Fatal(e)
	}
	v := imageEditVersion{ID: "original-version", Path: src, SHA256: hash, Width: 40, Height: 20, Format: "png", Label: "原图", CreatedAt: nowRFC3339()}
	if e = saveImageVersion(db, s.ID, v); e != nil {
		t.Fatal(e)
	}
	old := imageEditorToolkit
	t.Cleanup(func() { imageEditorToolkit = old })
	calls := 0
	imageEditorToolkit = func(input any) (map[string]any, error) {
		calls++
		args := input.(map[string]any)
		dest := filepath.Join(args["output_dir"].(string), "result.png")
		editorTestPNG(t, dest, 20, 40)
		return map[string]any{"result": map[string]any{"ok": true, "outputs": []any{map[string]any{"output": dest}}}}, nil
	}
	in := imageEditInput{SessionID: s.ID, VersionID: v.ID, Options: imageEditOptions{Rotate: 90, Format: "png", Quality: 90}}
	var next imageEditSession
	for i := 0; i < 2; i++ {
		value, e := applyImageEditor(in)
		if e != nil {
			t.Fatal(e)
		}
		next = value.(imageEditSession)
		if len(next.Versions) != 2 || next.AppliedVersionID != next.Versions[1].ID {
			t.Fatalf("%+v", next)
		}
	}
	if calls != 1 {
		t.Fatal("replay reprocessed", calls)
	}
	if after, _ := hashFile(src); after != hash {
		t.Fatal("original modified")
	}
	for i := 0; i < 2; i++ {
		_, e = adoptImageEditor(imageEditInput{SessionID: s.ID, VersionID: next.Versions[1].ID, LibraryID: library.ID, Title: "旋转后的图片"})
		if e != nil {
			t.Fatal(e)
		}
	}
	var count int
	_ = db.QueryRow(`SELECT COUNT(*) FROM library_items WHERE library_id=?`, library.ID).Scan(&count)
	if count != 1 {
		t.Fatal("duplicate adoption", count)
	}
	listed, e := listLibraryItems(libraryItemsInput{LibraryID: library.ID})
	if e != nil {
		t.Fatal(e)
	}
	items := listed.(map[string]any)["items"].([]map[string]any)
	if len(items) != 1 || items[0]["entry_type"] != "library" || items[0]["library_id"] != library.ID || items[0]["library_kind"] != library.Kind {
		t.Fatalf("saved image lost its library identity: %+v", items)
	}
	var manifest string
	if e = db.QueryRow(`SELECT metadata_json FROM library_items WHERE library_id=?`, library.ID).Scan(&manifest); e != nil {
		t.Fatal(e)
	}
	var m map[string]any
	if e = json.Unmarshal([]byte(manifest), &m); e != nil {
		t.Fatal(e)
	}
	input := m["input"].(map[string]any)
	if m["schema_version"] != "himind.image-edit-manifest.v1" || input["source_sha256"] != hash || input["parent_version_id"] != v.ID {
		t.Fatal(manifest)
	}
	if strings.Contains(manifest, "provider_reported_or_unknown") {
		t.Fatal("deterministic processing must not report AI usage")
	}
	if _, e = adoptImageEditor(imageEditInput{SessionID: s.ID, VersionID: v.ID, LibraryID: library.ID}); e == nil {
		t.Fatal("unmodified original adopted")
	}
	if e = os.WriteFile(next.Versions[1].Path, []byte("changed"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = previewImageEditor(imageEditInput{SessionID: s.ID, VersionID: next.Versions[1].ID}); e == nil {
		t.Fatal("tampered version previewed")
	}
}
func TestImageMaskPixelBindingAndAdapterGate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mask.png")
	strokes := []imageEditStroke{{Radius: .1, Points: []imageEditPoint{{X: .5, Y: .5}, {X: .6, Y: .5}}}}
	if e := writeImageEditMask(path, 100, 60, strokes); e != nil {
		t.Fatal(e)
	}
	f, _ := os.Open(path)
	img, e := png.Decode(f)
	f.Close()
	if e != nil {
		t.Fatal(e)
	}
	_, _, _, center := img.At(50, 30).RGBA()
	_, _, _, edge := img.At(0, 0).RGBA()
	if center != 0 || edge != 65535 || img.Bounds() != image.Rect(0, 0, 100, 60) {
		t.Fatal("mask alpha/dimensions wrong")
	}
	if writeImageEditMask(filepath.Join(t.TempDir(), "bad.png"), 100, 60, []imageEditStroke{{Radius: .1, Points: []imageEditPoint{{X: 2, Y: .5}}}}) == nil {
		t.Fatal("out-of-image stroke accepted")
	}
	c := map[string]any{"execution_ready": true, "catalog": map[string]any{"routes": []any{map[string]any{"capability": "image.edit", "state": "resolved", "selected": map[string]any{"adapter": "ark-images"}}}}}
	if imageEditAIAvailability(c, true) == nil {
		t.Fatal("unsupported adapter allowed mask")
	}
	if e = imageEditAIAvailability(c, false); e != nil {
		t.Fatal(e)
	}
	c["execution_ready"] = false
	if imageEditAIAvailability(c, false) == nil {
		t.Fatal("missing host interface accepted")
	}
}
func TestImageResizeGuardAccountsForAutomaticAspectRatio(t *testing.T) {
	e := validateImageOptions(imageEditOptions{Width: 16000, Format: "png", Quality: 90}, imageEditVersion{Width: 10, Height: 10000})
	if e == nil {
		t.Fatal("aspect ratio expansion escaped pixel limit")
	}
}

func TestEditorAIPrepareKeepsTrustedMaskAndNeverResetsSubmittedRequest(t *testing.T) {
	db, _ := acquisitionTestLibrary(t)
	if e := ensureGenerationSchema(db); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "reference.png")
	editorTestPNG(t, path, 30, 20)
	digest, _ := hashFile(path)
	mask := filepath.Join(t.TempDir(), "mask.png")
	if e := writeImageEditMask(mask, 30, 20, []imageEditStroke{{Radius: .1, Points: []imageEditPoint{{X: .5, Y: .5}}}}); e != nil {
		t.Fatal(e)
	}
	maskHash, _ := hashFile(mask)
	old := generationAgent
	t.Cleanup(func() { generationAgent = old })
	imports := 0
	generationAgent = func(capability string, input any) (map[string]any, error) {
		if capability != "media.asset.import" {
			t.Fatalf("unexpected execution %s", capability)
		}
		imports++
		args := input.(map[string]any)
		return map[string]any{"path": args["path"], "sha256": args["sha256"]}, nil
	}
	in := generationInput{Capability: "image.edit", Prompt: "修改所选区域", editorRequest: "request-fixture", editorReference: &editorGenerationReference{SessionID: "editor-fixture", VersionID: "version-fixture", Path: path, SHA256: digest, MaskPath: mask, MaskSHA256: maskHash}}
	value, e := prepareGeneration(in)
	if e != nil {
		t.Fatal(e)
	}
	s := value.(generationSession)
	if s.Input["mask"] != mask || s.References[0]["sha256"] != digest || s.References[0]["mask_sha256"] != maskHash {
		t.Fatalf("mask/source binding lost: %+v", s)
	}
	s.SubmissionState = "submitting"
	if e = saveGeneration(db, s); e != nil {
		t.Fatal(e)
	}
	value, e = prepareGeneration(in)
	if e != nil {
		t.Fatal(e)
	}
	replay := value.(generationSession)
	if replay.ID != s.ID || replay.SubmissionState != "submitting" || imports != 2 {
		t.Fatalf("replay reset or re-imported: %+v imports=%d", replay, imports)
	}
}
