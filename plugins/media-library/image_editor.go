package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// The editor owns intent and immutable versions. Toolkit owns pixel processing;
// Agent owns AI execution. No caller-controlled path crosses these boundaries.
type imageEditInput struct {
	AssetID         string                     `json:"asset_id"`
	SessionID       string                     `json:"session_id"`
	VersionID       string                     `json:"version_id"`
	LibraryID       string                     `json:"library_id"`
	Title           string                     `json:"title"`
	Prompt          string                     `json:"prompt"`
	Mode            string                     `json:"mode"`
	RequestKey      string                     `json:"request_key"`
	Options         imageEditOptions           `json:"options"`
	Strokes         []imageEditStroke          `json:"strokes"`
	OutputReference *generationOutputReference `json:"output_reference,omitempty"`
}
type imageEditOptions struct {
	Crop    *imageEditRect `json:"crop,omitempty"`
	Rotate  int            `json:"rotate"`
	FlipH   bool           `json:"flip_h"`
	FlipV   bool           `json:"flip_v"`
	Width   int            `json:"width"`
	Height  int            `json:"height"`
	Format  string         `json:"format"`
	Quality int            `json:"quality"`
}
type imageEditRect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}
type imageEditPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}
type imageEditStroke struct {
	Radius float64          `json:"radius"`
	Points []imageEditPoint `json:"points"`
}
type imageEditSession struct {
	ID               string             `json:"id"`
	SourceID         string             `json:"source_id"`
	SourceSHA256     string             `json:"source_sha256"`
	Title            string             `json:"title"`
	CreatedAt        string             `json:"created_at"`
	Versions         []imageEditVersion `json:"versions"`
	AppliedVersionID string             `json:"applied_version_id,omitempty"`
}
type imageEditVersion struct {
	ID           string           `json:"id"`
	ParentID     string           `json:"parent_id"`
	Path         string           `json:"-"`
	SHA256       string           `json:"sha256"`
	Width        int              `json:"width"`
	Height       int              `json:"height"`
	Format       string           `json:"format"`
	Label        string           `json:"label"`
	CreatedAt    string           `json:"created_at"`
	Options      imageEditOptions `json:"options"`
	GenerationID string           `json:"generation_id,omitempty"`
	OutputIndex  int              `json:"output_index,omitempty"`
}
type editorGenerationReference struct{ SessionID, VersionID, Path, SHA256, MaskPath, MaskSHA256 string }

func ensureImageEditorSchema(db *sql.DB) error {
	_, e := db.Exec(`CREATE TABLE IF NOT EXISTS image_edit_sessions(id TEXT PRIMARY KEY,source_id TEXT NOT NULL,source_sha256 TEXT NOT NULL,title TEXT NOT NULL,created_at TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS image_edit_versions(id TEXT PRIMARY KEY,session_id TEXT NOT NULL,path TEXT NOT NULL,payload_json TEXT NOT NULL,created_at TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS image_edit_version_session ON image_edit_versions(session_id,created_at,id);
 CREATE TABLE IF NOT EXISTS image_edit_generations(session_id TEXT NOT NULL,version_id TEXT NOT NULL,generation_id TEXT NOT NULL UNIQUE,PRIMARY KEY(session_id,generation_id));`)
	return e
}
func editorDB() (*sql.DB, error) {
	db, e := openLibraryDB()
	if e != nil {
		return nil, e
	}
	if e = ensureImageEditorSchema(db); e != nil {
		db.Close()
		return nil, e
	}
	return db, nil
}
func saveImageVersion(db interface {
	Exec(string, ...any) (sql.Result, error)
}, sid string, v imageEditVersion) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	_, e = db.Exec(`INSERT OR IGNORE INTO image_edit_versions(id,session_id,path,payload_json,created_at) VALUES(?,?,?,?,?)`, v.ID, sid, v.Path, string(b), v.CreatedAt)
	return e
}
func loadImageEditor(db *sql.DB, id string) (imageEditSession, error) {
	var s imageEditSession
	e := db.QueryRow(`SELECT id,source_id,source_sha256,title,created_at FROM image_edit_sessions WHERE id=?`, id).Scan(&s.ID, &s.SourceID, &s.SourceSHA256, &s.Title, &s.CreatedAt)
	if e != nil {
		return s, fmt.Errorf("编辑记录不存在，请从图片详情重新打开")
	}
	rows, e := db.Query(`SELECT path,payload_json FROM image_edit_versions WHERE session_id=? ORDER BY created_at,rowid`, id)
	if e != nil {
		return s, e
	}
	defer rows.Close()
	s.Versions = []imageEditVersion{}
	for rows.Next() {
		var path, raw string
		if e = rows.Scan(&path, &raw); e != nil {
			return s, e
		}
		var v imageEditVersion
		if e = json.Unmarshal([]byte(raw), &v); e != nil {
			return s, e
		}
		v.Path = path
		s.Versions = append(s.Versions, v)
	}
	return s, rows.Err()
}
func imageVersion(s imageEditSession, id string) (imageEditVersion, error) {
	for _, v := range s.Versions {
		if v.ID == id {
			h, e := hashFile(v.Path)
			if e != nil || h != v.SHA256 {
				return v, fmt.Errorf("此版本文件已丢失或变化，请选择其他版本")
			}
			return v, nil
		}
	}
	return imageEditVersion{}, fmt.Errorf("请选择有效的图片版本")
}
func editorDimensions(path string) (int, int, error) {
	f, e := os.Open(path)
	if e != nil {
		return 0, 0, e
	}
	defer f.Close()
	c, _, e := image.DecodeConfig(f)
	if e != nil || c.Width < 1 || c.Height < 1 || c.Width > 16384 || c.Height > 16384 || int64(c.Width)*int64(c.Height) > 64e6 {
		return 0, 0, fmt.Errorf("图片尺寸不支持，请使用不超过 6400 万像素的图片")
	}
	return c.Width, c.Height, nil
}

// Private staging is copied before invoking another plugin, preserving source.
var imageEditorToolkit = invokeImageEditorToolkit

func invokeImageEditorToolkit(input any) (map[string]any, error) {
	raw, e := invokeMediaProcessingCapability("media.toolkit.image.convert", input)
	if e != nil {
		return nil, e
	}
	var out map[string]any
	e = json.Unmarshal(raw, &out)
	return out, e
}
func renderImageVersion(source, root string, o imageEditOptions) (string, error) {
	dest := filepath.Join(root, "input"+strings.ToLower(filepath.Ext(source)))
	h, e := hashFile(source)
	if e != nil {
		return "", e
	}
	if e = commitAcquisitionCopy(context.Background(), source, dest, h); e != nil {
		return "", e
	}
	args := map[string]any{"workspace_root": root, "paths": []string{dest}, "output_dir": filepath.Join(root, "out"), "format": o.Format, "quality": o.Quality, "width": o.Width, "height": o.Height, "rotate": o.Rotate, "flip_h": o.FlipH, "flip_v": o.FlipV, "name_template": "result.{ext}"}
	if o.Crop != nil {
		args["crop"] = o.Crop
	}
	reply, e := imageEditorToolkit(args)
	if e != nil {
		return "", e
	}
	result, _ := reply["result"].(map[string]any)
	list, _ := result["outputs"].([]any)
	if result["ok"] != true || len(list) != 1 {
		return "", fmt.Errorf("图片处理失败，请检查图片格式和尺寸后重试")
	}
	out, _ := list[0].(map[string]any)
	path, _ := out["output"].(string)
	expected := filepath.Join(root, "out", "result."+map[string]string{"jpeg": "jpg", "png": "png", "webp": "webp"}[o.Format])
	real, e := filepath.EvalSymlinks(path)
	if e != nil || !strings.EqualFold(real, expected) || !isPathInside(root, real) {
		return "", fmt.Errorf("图片处理产物校验失败")
	}
	return path, nil
}
func openImageEditor(in imageEditInput) (any, error) {
	db, e := editorDB()
	if e != nil {
		return nil, e
	}
	defer db.Close()
	var source, kind, title, digest, id string
	if in.OutputReference != nil {
		source, digest, e = trustedGenerationReference(db, *in.OutputReference)
		kind = "image"
		title = "生成图片"
		id = fmt.Sprintf("generation:%s:%d", in.OutputReference.SessionID, in.OutputReference.Index)
	} else {
		id = in.AssetID
		e = db.QueryRow(`SELECT local_path,kind,title FROM library_items WHERE id=? AND deleted_at='' UNION ALL SELECT path,kind,name FROM candidates WHERE id=? AND missing=0 LIMIT 1`, id, id).Scan(&source, &kind, &title)
		if e == nil {
			digest, e = hashFile(source)
		}
	}
	if e != nil || kind != "image" {
		return nil, fmt.Errorf("图片不存在或已离线，请重新选择")
	}
	ext := strings.ToLower(filepath.Ext(source))
	if !map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".webp": true, ".bmp": true, ".tif": true, ".tiff": true, ".gif": true}[ext] {
		return nil, fmt.Errorf("此格式暂不能编辑，请先转换为 PNG、JPEG 或 WebP")
	}
	var existing string
	if db.QueryRow(`SELECT id FROM image_edit_sessions WHERE source_id=? AND source_sha256=? ORDER BY created_at DESC LIMIT 1`, id, digest).Scan(&existing) == nil {
		return loadImageEditor(db, existing)
	}
	root, e := pluginDataRoot()
	if e != nil {
		return nil, e
	}
	sid := randomID("imageedit")
	staging := filepath.Join(root, "image-editor", sid, "original")
	path, e := renderImageVersion(source, staging, imageEditOptions{Format: "png", Quality: 100})
	if e != nil {
		return nil, e
	}
	// Copy verified source identity; metadata/EXIF normalization is done by toolkit.
	after, e := hashFile(source)
	if e != nil || after != digest {
		return nil, fmt.Errorf("原图在读取时发生变化，请重试")
	}
	w, h, e := editorDimensions(path)
	if e != nil {
		return nil, e
	}
	hash, e := hashFile(path)
	if e != nil {
		return nil, e
	}
	s := imageEditSession{ID: sid, SourceID: id, SourceSHA256: digest, Title: title, CreatedAt: nowRFC3339()}
	tx, e := db.Begin()
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	_, e = tx.Exec(`INSERT INTO image_edit_sessions(id,source_id,source_sha256,title,created_at) VALUES(?,?,?,?,?)`, s.ID, id, digest, title, s.CreatedAt)
	if e != nil {
		return nil, e
	}
	v := imageEditVersion{ID: randomID("imagever"), Path: path, SHA256: hash, Width: w, Height: h, Format: "png", Label: "原图", CreatedAt: nowRFC3339()}
	if e = saveImageVersion(tx, s.ID, v); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return loadImageEditor(db, s.ID)
}
func validateImageOptions(o imageEditOptions, v imageEditVersion) error {
	if !map[string]bool{"png": true, "jpeg": true, "webp": true}[o.Format] || o.Quality < 1 || o.Quality > 100 {
		return fmt.Errorf("请选择 PNG、JPEG 或 WebP，质量为 1–100")
	}
	if o.Width < 0 || o.Height < 0 || o.Width > 16384 || o.Height > 16384 || int64(o.Width)*int64(o.Height) > 64e6 {
		return fmt.Errorf("尺寸应在 1–16384 像素内，留空保留比例，最多 6400 万像素")
	}
	if o.Rotate != 0 && o.Rotate != 90 && o.Rotate != 180 && o.Rotate != 270 {
		return fmt.Errorf("请选择有效的旋转角度")
	}
	if c := o.Crop; c != nil {
		if c.X < 0 || c.Y < 0 || c.Width < 1 || c.Height < 1 || c.X > v.Width-c.Width || c.Y > v.Height-c.Height {
			return fmt.Errorf("裁剪区域必须位于图片内")
		}
	}
	w, h := imageOutputDimensions(o, v)
	if w < 1 || h < 1 || w > 16384 || h > 16384 || int64(w)*int64(h) > 64e6 {
		return fmt.Errorf("输出尺寸超过 16384 像素或 6400 万像素，请缩小尺寸")
	}
	return nil
}
func applyImageEditor(in imageEditInput) (any, error) {
	db, e := editorDB()
	if e != nil {
		return nil, e
	}
	defer db.Close()
	s, e := loadImageEditor(db, in.SessionID)
	if e != nil {
		return nil, e
	}
	v, e := imageVersion(s, in.VersionID)
	if e != nil {
		return nil, e
	}
	if e = validateImageOptions(in.Options, v); e != nil {
		return nil, e
	}
	raw, _ := json.Marshal(in.Options)
	id := stableID("imagever", s.ID+v.ID+string(raw))
	for _, old := range s.Versions {
		if old.ID == id {
			s.AppliedVersionID = id
			return s, nil
		}
	}
	root, e := pluginDataRoot()
	if e != nil {
		return nil, e
	}
	path, e := renderImageVersion(v.Path, filepath.Join(root, "image-editor", s.ID, randomID("render")), in.Options)
	if e != nil {
		return nil, e
	}
	// Decode PNG directly; other export formats use their known decoded geometry.
	width, height := imageOutputDimensions(in.Options, v)
	if in.Options.Format != "webp" {
		if width, height, e = editorDimensions(path); e != nil {
			return nil, e
		}
	}
	digest, e := hashFile(path)
	if e != nil {
		return nil, e
	}
	n := imageEditVersion{ID: id, ParentID: v.ID, Path: path, SHA256: digest, Width: width, Height: height, Format: in.Options.Format, Options: in.Options, Label: "基础编辑", CreatedAt: nowRFC3339()}
	if e = saveImageVersion(db, s.ID, n); e != nil {
		return nil, e
	}
	s, e = loadImageEditor(db, s.ID)
	s.AppliedVersionID = id
	return s, e
}
func imageOutputDimensions(o imageEditOptions, v imageEditVersion) (int, int) {
	w, h := v.Width, v.Height
	if o.Crop != nil {
		w, h = o.Crop.Width, o.Crop.Height
	}
	if o.Rotate == 90 || o.Rotate == 270 {
		w, h = h, w
	}
	if o.Width > 0 && o.Height > 0 {
		return o.Width, o.Height
	}
	if o.Width > 0 {
		return o.Width, max(1, int(math.Round(float64(h)*float64(o.Width)/float64(w))))
	}
	if o.Height > 0 {
		return max(1, int(math.Round(float64(w)*float64(o.Height)/float64(h)))), o.Height
	}
	return w, h
}
func previewImageEditor(in imageEditInput) (any, error) {
	db, e := editorDB()
	if e != nil {
		return nil, e
	}
	defer db.Close()
	s, e := loadImageEditor(db, in.SessionID)
	if e != nil {
		return nil, e
	}
	v, e := imageVersion(s, in.VersionID)
	if e != nil {
		return nil, e
	}
	return map[string]any{"stream_url": streamURLFor(v.Path, v.ID), "width": v.Width, "height": v.Height}, nil
}

func adoptImageEditor(in imageEditInput) (any, error) {
	db, e := editorDB()
	if e != nil {
		return nil, e
	}
	defer db.Close()
	s, e := loadImageEditor(db, in.SessionID)
	if e != nil {
		return nil, e
	}
	v, e := imageVersion(s, in.VersionID)
	if e != nil {
		return nil, e
	}
	if v.ParentID == "" {
		return nil, fmt.Errorf("请先应用编辑，再保存为新素材")
	}
	library, e := getLibraryWithDB(db, in.LibraryID)
	if e != nil {
		return nil, e
	}
	if library.State != "online" || library.BackendType == "smb" || library.AccessMode == "read_only" || library.AccessMode == "readonly" || library.Kind == "curated" {
		return nil, fmt.Errorf("请选择可写的资源库")
	}
	if e = ensureGenerationSchema(db); e != nil {
		return nil, e
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = s.Title + " · 编辑"
	}
	if len([]rune(title)) > 120 {
		return nil, fmt.Errorf("素材名称最多 120 字")
	}
	// Reuse the atomic, hash-deduplicated adoption/outbox pipeline and lineage store.
	gs := generationSession{ID: s.ID + ":" + v.ID, Capability: "image.process", Prompt: title, CreatedAt: v.CreatedAt, Input: map[string]any{"source_asset_id": s.SourceID, "source_sha256": s.SourceSHA256, "version_id": v.ID, "parent_version_id": v.ParentID, "options": v.Options, "width": v.Width, "height": v.Height, "engine": "media-toolkit", "generation_id": v.GenerationID}, Job: map[string]any{}, Outputs: []map[string]any{}}
	chain := []imageEditVersion{}
	for cursor := v.ID; cursor != ""; {
		found := false
		for _, ancestor := range s.Versions {
			if ancestor.ID == cursor {
				chain = append(chain, ancestor)
				cursor = ancestor.ParentID
				found = true
				break
			}
		}
		if !found || len(chain) > len(s.Versions) {
			return nil, fmt.Errorf("版本来源不完整，请重新打开图片")
		}
	}
	gs.Input["version_chain"] = chain
	if v.GenerationID != "" {
		if original, e := loadGeneration(db, v.GenerationID); e == nil {
			gs.Input["ai_generation"] = map[string]any{"session_id": original.ID, "job_id": original.JobID, "prompt": original.Prompt, "input": original.Input, "references": original.References}
		}
	}
	info, e := os.Stat(v.Path)
	if e != nil {
		return nil, e
	}
	gs.Outputs = append(gs.Outputs, map[string]any{"path": v.Path, "sha256": v.SHA256, "size_bytes": info.Size()})
	result, e := adoptGenerationOutput(db, library, gs, 0)
	if e != nil {
		return nil, e
	}
	if e = flushAcquisitionOutbox(db); e != nil {
		return nil, fmt.Errorf("素材已保存，目录同步待重试：%w", e)
	}
	return result, nil
}

// Masks are rendered server-side in original pixel coordinates. Transparent
// pixels are editable; opaque pixels preserve the source (OpenAI edit contract).
func writeImageEditMask(path string, w, h int, strokes []imageEditStroke) error {
	if w < 1 || h < 1 || int64(w)*int64(h) > 32e6 || len(strokes) < 1 || len(strokes) > 128 {
		return fmt.Errorf("请绘制局部选区；局部改图最多支持 3200 万像素")
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255
	}
	total := 0
	work := 0.0
	for _, s := range strokes {
		if math.IsNaN(s.Radius) || math.IsInf(s.Radius, 0) || s.Radius < .001 || s.Radius > .25 || len(s.Points) < 1 || len(s.Points) > 2000 {
			return fmt.Errorf("选区画笔数据无效，请重新绘制")
		}
		total += len(s.Points)
		if total > 12000 {
			return fmt.Errorf("选区过于复杂，请简化后重试")
		}
		radius := max(1, s.Radius*float64(min(w, h)))
		var last imageEditPoint
		for i, p := range s.Points {
			if math.IsNaN(p.X) || math.IsNaN(p.Y) || math.IsInf(p.X, 0) || math.IsInf(p.Y, 0) || p.X < 0 || p.Y < 0 || p.X > 1 || p.Y > 1 {
				return fmt.Errorf("选区超出图片，请重新绘制")
			}
			x, y := p.X*float64(w-1), p.Y*float64(h-1)
			if i == 0 {
				last = p
			}
			lx, ly := last.X*float64(w-1), last.Y*float64(h-1)
			steps := max(1, int(math.Ceil(math.Hypot(x-lx, y-ly)/max(1, radius/2))))
			work += float64(steps+1) * math.Pow(2*radius+3, 2)
			if work > 120e6 {
				return fmt.Errorf("选区过于复杂，请减少笔画或缩小图片后重试")
			}
			for step := 0; step <= steps; step++ {
				cx, cy := lx+(x-lx)*float64(step)/float64(steps), ly+(y-ly)*float64(step)/float64(steps)
				for py := max(0, int(cy-radius)); py <= min(h-1, int(cy+radius)); py++ {
					for px := max(0, int(cx-radius)); px <= min(w-1, int(cx+radius)); px++ {
						if math.Hypot(float64(px)-cx, float64(py)-cy) <= radius {
							img.SetNRGBA(px, py, color.NRGBA{})
						}
					}
				}
			}
			last = p
		}
	}
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	e = png.Encode(f, img)
	ce := f.Close()
	if e == nil {
		e = ce
	}
	return e
}
func imageEditAIAvailability(catalog map[string]any, local bool) error {
	if catalog["execution_ready"] != true {
		return fmt.Errorf("%s", firstNonEmpty(stringValue(catalog["execution_message"]), "当前 Agent 未提供持久生成接口，请更新 Agent 后重试"))
	}
	c, _ := catalog["catalog"].(map[string]any)
	routes, _ := c["routes"].([]any)
	for _, r := range routes {
		route, _ := r.(map[string]any)
		if route["capability"] != "image.edit" {
			continue
		}
		if route["state"] != "resolved" {
			return fmt.Errorf("请在 Agent 中配置图片编辑服务")
		}
		selected, _ := route["selected"].(map[string]any)
		if local && selected["adapter"] != "openai-images-edit" {
			return fmt.Errorf("当前服务不支持局部蒙版，请选择支持局部改图的服务")
		}
		return nil
	}
	return fmt.Errorf("请在 Agent 中配置图片编辑服务")
}
func importEditorReference(path, digest string) (string, error) {
	h, e := hashFile(path)
	if e != nil || h != digest {
		return "", fmt.Errorf("编辑版本或蒙版已变化，请重新选择")
	}
	out, e := generationAgent("media.asset.import", map[string]any{"path": path, "sha256": digest})
	if e != nil {
		return "", e
	}
	ref, _ := out["path"].(string)
	if ref == "" || out["sha256"] != digest {
		return "", fmt.Errorf("编辑参考副本校验失败")
	}
	return ref, nil
}
func generateImageEditor(in imageEditInput) (any, error) {
	if in.Mode != "describe" && in.Mode != "local" && in.Mode != "erase" {
		return nil, fmt.Errorf("请选择描述改图或局部修改")
	}
	if len(in.RequestKey) < 8 || len(in.RequestKey) > 100 {
		return nil, fmt.Errorf("生成请求标识无效，请重新操作")
	}
	db, e := editorDB()
	if e != nil {
		return nil, e
	}
	defer db.Close()
	s, e := loadImageEditor(db, in.SessionID)
	if e != nil {
		return nil, e
	}
	v, e := imageVersion(s, in.VersionID)
	if e != nil {
		return nil, e
	}
	local := in.Mode != "describe"
	cat, e := generationCatalog()
	if e != nil {
		return nil, e
	}
	if e = imageEditAIAvailability(cat.(map[string]any), local); e != nil {
		return nil, e
	}
	ref := &editorGenerationReference{SessionID: s.ID, VersionID: v.ID, Path: v.Path, SHA256: v.SHA256}
	if local {
		root, e := pluginDataRoot()
		if e != nil {
			return nil, e
		}
		dir := filepath.Join(root, "image-editor", s.ID, "masks")
		if e = os.MkdirAll(dir, 0700); e != nil {
			return nil, e
		}
		ref.MaskPath = filepath.Join(dir, randomID("mask")+".png")
		if e = writeImageEditMask(ref.MaskPath, v.Width, v.Height, in.Strokes); e != nil {
			return nil, e
		}
		ref.MaskSHA256, e = hashFile(ref.MaskPath)
		if e != nil {
			return nil, e
		}
	}
	prompt := in.Prompt
	if in.Mode == "erase" {
		prompt = "移除蒙版选中区域内的内容，按周围画面自然补全，保留其他区域。 " + prompt
	}
	value, e := prepareGeneration(generationInput{Capability: "image.edit", Prompt: prompt, editorReference: ref, editorRequest: in.RequestKey})
	if e != nil {
		return nil, e
	}
	gs := value.(generationSession)
	_, e = db.Exec(`INSERT OR IGNORE INTO image_edit_generations(session_id,version_id,generation_id) VALUES(?,?,?)`, s.ID, v.ID, gs.ID)
	if e != nil {
		return nil, e
	}
	return gs, nil
}
func getImageEditor(in imageEditInput) (any, error) {
	db, e := editorDB()
	if e != nil {
		return nil, e
	}
	defer db.Close()
	s, e := loadImageEditor(db, in.SessionID)
	if e != nil {
		return nil, e
	}
	rows, e := db.Query(`SELECT version_id,generation_id FROM image_edit_generations WHERE session_id=?`, s.ID)
	if e != nil {
		return nil, e
	}
	type pair struct{ parent, id string }
	pairs := []pair{}
	for rows.Next() {
		var p pair
		if e = rows.Scan(&p.parent, &p.id); e != nil {
			rows.Close()
			return nil, e
		}
		pairs = append(pairs, p)
	}
	rows.Close()
	jobs := []generationSession{}
	for _, p := range pairs {
		gs, e := loadGeneration(db, p.id)
		if e != nil {
			continue
		}
		if gs.SubmissionState != "prepared" {
			_ = refreshGeneration(db, &gs)
		}
		jobs = append(jobs, gs)
		if gs.Job["status"] != "succeeded" {
			continue
		}
		for i := range gs.Outputs {
			path, digest, e := trustedGenerationReference(db, generationOutputReference{SessionID: gs.ID, Index: i})
			if e != nil {
				continue
			}
			id := stableID("imagever", gs.ID+fmt.Sprint(i))
			exists := false
			for _, v := range s.Versions {
				if v.ID == id {
					exists = true
				}
			}
			if exists {
				continue
			}
			root, e := pluginDataRoot()
			if e != nil {
				return nil, e
			}
			path, e = renderImageVersion(path, filepath.Join(root, "image-editor", s.ID, randomID("candidate")), imageEditOptions{Format: "png", Quality: 100})
			if e != nil {
				return nil, e
			}
			digest, e = hashFile(path)
			if e != nil {
				return nil, e
			}
			w, h, e := editorDimensions(path)
			if e != nil {
				return nil, e
			}
			v := imageEditVersion{ID: id, ParentID: p.parent, Path: path, SHA256: digest, Width: w, Height: h, Format: "png", Label: "AI 改图", CreatedAt: nowRFC3339(), GenerationID: gs.ID, OutputIndex: i}
			if e = saveImageVersion(db, s.ID, v); e != nil {
				return nil, e
			}
		}
	}
	s, e = loadImageEditor(db, s.ID)
	return map[string]any{"session": s, "generations": jobs}, e
}
