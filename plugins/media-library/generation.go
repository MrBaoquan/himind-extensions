package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Model execution belongs to Agent. This store contains business intent and
// adoption receipts; never provider credentials or a second generation queue.
type generationInput struct {
	editorReference  *editorGenerationReference
	editorRequest    string
	SessionID        string                      `json:"session_id"`
	Capability       string                      `json:"capability"`
	Prompt           string                      `json:"prompt"`
	LibraryID        string                      `json:"library_id"`
	References       []string                    `json:"references"`
	Parameters       map[string]any              `json:"parameters"`
	AutoAdopt        bool                        `json:"auto_adopt"`
	Indexes          []int                       `json:"indexes"`
	AssetID          string                      `json:"asset_id"`
	Index            int                         `json:"index"`
	OutputReferences []generationOutputReference `json:"output_references"`
	ReferenceOrder   []string                    `json:"reference_order"`
}
type generationSession struct {
	ID              string           `json:"id"`
	Capability      string           `json:"capability"`
	Prompt          string           `json:"prompt"`
	LibraryID       string           `json:"library_id"`
	References      []map[string]any `json:"references"`
	Input           map[string]any   `json:"input"`
	AutoAdopt       bool             `json:"auto_adopt"`
	JobID           string           `json:"job_id"`
	CreatedAt       string           `json:"created_at"`
	Job             map[string]any   `json:"job,omitempty"`
	Outputs         []map[string]any `json:"outputs"`
	SubmissionState string           `json:"submission_state,omitempty"`
	QueryState      string           `json:"query_state,omitempty"`
	QueryMessage    string           `json:"query_message,omitempty"`
}

const generationSource = "com.himind.media-library"

func ensureGenerationSchema(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS generation_sessions(id TEXT PRIMARY KEY, payload_json TEXT NOT NULL, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS generation_outputs(session_id TEXT NOT NULL,output_index INTEGER NOT NULL,library_id TEXT NOT NULL,asset_id TEXT NOT NULL DEFAULT '',state TEXT NOT NULL,error TEXT NOT NULL DEFAULT '',updated_at TEXT NOT NULL,PRIMARY KEY(session_id,output_index,library_id));
CREATE TABLE IF NOT EXISTS asset_derivations(asset_id TEXT NOT NULL,session_id TEXT NOT NULL,output_index INTEGER NOT NULL,manifest_json TEXT NOT NULL,created_at TEXT NOT NULL,PRIMARY KEY(asset_id,session_id,output_index));`)
	return err
}

var generationAgent = invokeGenerationAgent

func invokeGenerationAgent(capability string, input any) (map[string]any, error) {
	allowed := map[string]bool{"media.provider.catalog": true, "media.job.start": true, "media.job.get": true, "media.job.list": true, "media.job.cancel": true, "media.asset.import": true, "media.audio.voice.list": true}
	if !allowed[capability] {
		return nil, fmt.Errorf("不支持的生成能力")
	}
	endpoint, token := os.Getenv("HIMIND_CAPABILITY_BROKER_URL"), os.Getenv("HIMIND_CAPABILITY_BROKER_TOKEN")
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Path != "/invoke" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() || token == "" {
		return nil, fmt.Errorf("AI 调用通道不可用，请从生产 Agent 重新打开媒体库")
	}
	data, err := json.Marshal(map[string]any{"capability": capability, "input": input})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Agent 未响应，请保留此结果组并刷新核对")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	var reply struct {
		Result  map[string]any `json:"result"`
		Error   any            `json:"error"`
		Message string         `json:"message"`
	}
	if json.Unmarshal(body, &reply) != nil {
		return nil, fmt.Errorf("Agent 返回结果无效")
	}
	if response.StatusCode != 200 || reply.Result == nil {
		return nil, classifyGenerationError(response.StatusCode, map[string]any{"error": reply.Error, "message": reply.Message})
	}
	return reply.Result, nil
}

func generationCatalog() (any, error) {
	catalog, err := generationAgent("media.provider.catalog", map[string]any{})
	if err != nil {
		return nil, err
	}
	_, executionErr := generationAgent("media.job.list", map[string]any{"source": generationSource, "limit": 1})
	state, message := generationExecutionAvailability(executionErr)
	return map[string]any{"catalog": catalog, "execution_ready": executionErr == nil, "execution_state": state, "execution_message": message, "auto_adopt_supported": false}, nil
}
func saveGeneration(db *sql.DB, s generationSession) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO generation_sessions(id,payload_json,created_at) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET payload_json=excluded.payload_json`, s.ID, string(data), s.CreatedAt)
	return err
}
func loadGeneration(db *sql.DB, id string) (generationSession, error) {
	var s generationSession
	var data string
	if err := db.QueryRow(`SELECT payload_json FROM generation_sessions WHERE id=?`, id).Scan(&data); err != nil {
		return s, fmt.Errorf("生成记录不存在")
	}
	return s, json.Unmarshal([]byte(data), &s)
}
func prepareGeneration(in generationInput) (any, error) {
	if in.AutoAdopt {
		return nil, fmt.Errorf("当前版本请预览结果后入库；自动入库暂不可用")
	}
	in.Prompt = strings.TrimSpace(in.Prompt)
	if in.Prompt == "" || len([]rune(in.Prompt)) > 8000 {
		return nil, fmt.Errorf("请输入8000字以内的生成内容")
	}
	allowed := map[string]bool{"image.generate": true, "image.edit": true, "video.generate": true, "audio.speech": true}
	if !allowed[in.Capability] {
		return nil, fmt.Errorf("请选择图片、视频或语音生成")
	}
	if in.Capability == "audio.speech" && len([]rune(in.Prompt)) > 4096 {
		return nil, fmt.Errorf("语音文本最多4096字")
	}
	referenceCount := len(in.References) + len(in.OutputReferences)
	if in.editorReference != nil {
		referenceCount++
	}
	if referenceCount > 4 || (in.Capability == "image.edit" && referenceCount == 0) || (in.Capability == "video.generate" && referenceCount > 2) || ((in.Capability == "image.generate" || in.Capability == "audio.speech") && referenceCount > 0) {
		return nil, fmt.Errorf("参考素材数量不符合当前生成类型")
	}
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err = ensureGenerationSchema(db); err != nil {
		return nil, err
	}
	s := generationSession{ID: randomID("generation"), Capability: in.Capability, Prompt: in.Prompt, LibraryID: in.LibraryID, SubmissionState: "prepared", CreatedAt: nowRFC3339(), Input: map[string]any{}, References: []map[string]any{}, Outputs: []map[string]any{}}
	if in.editorReference != nil {
		s.ID = stableID("generation", in.editorReference.SessionID+in.editorRequest)
		if existing, e := loadGeneration(db, s.ID); e == nil {
			return existing, nil
		}
	}
	for k, v := range in.Parameters {
		if !map[string]bool{"size": true, "quality": true, "n": true, "ratio": true, "resolution": true, "duration_seconds": true, "voice": true, "speed": true, "format": true}[k] {
			return nil, fmt.Errorf("不支持的生成参数：%s", k)
		}
		s.Input[k] = v
	}
	if in.Capability == "audio.speech" {
		s.Input["text"] = in.Prompt
	} else {
		s.Input["prompt"] = in.Prompt
	}
	refs := []string{}
	if r := in.editorReference; r != nil {
		ref, e := importEditorReference(r.Path, r.SHA256)
		if e != nil {
			return nil, e
		}
		refs = append(refs, ref)
		entry := map[string]any{"editor_session_id": r.SessionID, "editor_version_id": r.VersionID, "sha256": r.SHA256, "reference": ref}
		if r.MaskPath != "" {
			mask, e := importEditorReference(r.MaskPath, r.MaskSHA256)
			if e != nil {
				return nil, e
			}
			s.Input["mask"] = mask
			entry["mask_sha256"] = r.MaskSHA256
		}
		s.References = append(s.References, entry)
	}
	for _, id := range in.References {
		var path, kind string
		err = db.QueryRow(`SELECT local_path,kind FROM library_items WHERE id=? AND deleted_at='' UNION ALL SELECT path,kind FROM candidates WHERE id=? AND missing=0 LIMIT 1`, id, id).Scan(&path, &kind)
		if err != nil || kind != "image" {
			return nil, fmt.Errorf("参考图片不存在，请重新选择")
		}
		digest, err := hashFile(path)
		if err != nil {
			return nil, fmt.Errorf("参考图片暂不可用")
		}
		reference, err := generationAgent("media.asset.import", map[string]any{"path": path, "sha256": digest})
		if err != nil {
			return nil, err
		}
		refPath, _ := reference["path"].(string)
		if refPath == "" || reference["sha256"] != digest {
			return nil, fmt.Errorf("参考副本校验失败")
		}
		refs = append(refs, refPath)
		s.References = append(s.References, map[string]any{"asset_id": id, "sha256": digest, "reference": refPath})
	}
	for _, ref := range in.OutputReferences {
		path, digest, err := trustedGenerationReference(db, ref)
		if err != nil {
			return nil, err
		}
		refs = append(refs, path)
		s.References = append(s.References, map[string]any{"session_id": ref.SessionID, "index": ref.Index, "sha256": digest, "reference": path})
	}
	if len(in.ReferenceOrder) > 0 {
		if len(in.ReferenceOrder) != len(s.References) {
			return nil, fmt.Errorf("参考图片顺序不完整，请重新选择")
		}
		ordered := make([]map[string]any, 0, len(s.References))
		remaining := map[string]map[string]any{}
		for _, r := range s.References {
			key, _ := r["asset_id"].(string)
			if key == "" {
				key = fmt.Sprintf("%s:%v", r["session_id"], r["index"])
			}
			remaining[key] = r
		}
		refs = nil
		for _, key := range in.ReferenceOrder {
			r, ok := remaining[key]
			if !ok {
				return nil, fmt.Errorf("参考图片顺序无效，请重新选择")
			}
			delete(remaining, key)
			ordered = append(ordered, r)
			refs = append(refs, r["reference"].(string))
		}
		s.References = ordered
	}
	if len(refs) > 0 {
		s.Input["input_images"] = refs
	}
	digest := sha256.Sum256([]byte(generationSource + "\x00" + s.ID))
	s.JobID = "media_exec_" + hex.EncodeToString(digest[:])
	if in.editorReference != nil {
		data, e := json.Marshal(s)
		if e != nil {
			return nil, e
		}
		_, e = db.Exec(`INSERT OR IGNORE INTO generation_sessions(id,payload_json,created_at) VALUES(?,?,?)`, s.ID, string(data), s.CreatedAt)
		if e != nil {
			return nil, e
		}
		return loadGeneration(db, s.ID)
	}
	if err = saveGeneration(db, s); err != nil {
		return nil, err
	}
	return s, nil
}

func submitGeneration(in generationInput) (any, error) {
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err = ensureGenerationSchema(db); err != nil {
		return nil, err
	}
	s, err := loadGeneration(db, in.SessionID)
	if err != nil {
		return nil, err
	}
	if s.SubmissionState != "prepared" {
		return getGeneration(in)
	}
	claimed, err := claimGenerationSubmission(db, &s)
	if err != nil {
		return nil, err
	}
	if !claimed {
		return getGeneration(in)
	}
	result, err := generationAgent("media.job.start", map[string]any{"capability": s.Capability, "source": generationSource, "request_key": s.ID, "input": s.Input})
	if err != nil {
		s.SubmissionState = "unknown"
		_ = saveGeneration(db, s)
		return nil, err
	}
	if result["job_id"] != s.JobID {
		s.SubmissionState = "unknown"
		_ = saveGeneration(db, s)
		return nil, fmt.Errorf("Agent 执行身份不一致，请保留记录后核对")
	}
	s.Job = result
	s.SubmissionState = "submitted"
	if err = saveGeneration(db, s); err != nil {
		return nil, err
	}
	return s, nil
}
func generationList() (any, error) {
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err = ensureGenerationSchema(db); err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT payload_json FROM generation_sessions ORDER BY created_at DESC,id DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []generationSession{}
	for rows.Next() {
		var data string
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		var s generationSession
		if err = json.Unmarshal([]byte(data), &s); err != nil {
			return nil, err
		}
		items = append(items, s)
	}
	return map[string]any{"items": items}, rows.Err()
}
func refreshGeneration(db *sql.DB, s *generationSession) error {
	job, err := generationAgent("media.job.get", map[string]any{"job_id": s.JobID})
	if err != nil {
		return err
	}
	if job["job_id"] != s.JobID || job["source"] != generationSource || job["request_key"] != s.ID {
		return fmt.Errorf("生成执行来源校验失败")
	}
	s.Job = job
	s.Outputs = []map[string]any{}
	outputs, _ := job["outputs"].([]any)
	for index, value := range outputs {
		output, ok := value.(map[string]any)
		if !ok {
			continue
		}
		projected := map[string]any{}
		for k, v := range output {
			projected[k] = v
		}
		projected["index"] = index
		var state, assetID, lastError string
		_ = db.QueryRow(`SELECT state,asset_id,error FROM generation_outputs WHERE session_id=? AND output_index=? AND library_id=?`, s.ID, index, s.LibraryID).Scan(&state, &assetID, &lastError)
		projected["adoption_state"] = firstNonEmpty(state, "pending")
		projected["asset_id"] = assetID
		projected["adoption_error"] = lastError
		s.Outputs = append(s.Outputs, projected)
	}
	return saveGeneration(db, *s)
}
func getGeneration(in generationInput) (any, error) {
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err = ensureGenerationSchema(db); err != nil {
		return nil, err
	}
	s, err := loadGeneration(db, in.SessionID)
	if err != nil {
		return nil, err
	}
	if s.SubmissionState == "prepared" {
		s.QueryState = "prepared"
		return s, nil
	}
	if err = refreshGeneration(db, &s); err != nil {
		s.QueryState = "unavailable"
		s.QueryMessage = "原任务暂不可查询，记录已保留。请稍后核对；不要重复提交。"
		if e, ok := err.(*generationCapabilityError); ok && e.Code == "not_found" {
			s.QueryState = "not_found"
			s.QueryMessage = "Agent 未找到原任务。若重新生成，将创建新的可能收费请求。"
		}
		if in.LibraryID != "" {
			projectGenerationAdoptions(db, &s, in.LibraryID)
		}
		return s, nil
	}
	s.QueryState = "verified"
	if in.LibraryID != "" {
		projectGenerationAdoptions(db, &s, in.LibraryID)
	}
	return s, nil
}
func cancelGeneration(in generationInput) (any, error) {
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	s, err := loadGeneration(db, in.SessionID)
	if err != nil {
		return nil, err
	}
	return generationAgent("media.job.cancel", map[string]any{"job_id": s.JobID})
}

func adoptGeneration(in generationInput) (any, error) {
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err = ensureGenerationSchema(db); err != nil {
		return nil, err
	}
	s, err := loadGeneration(db, in.SessionID)
	if err != nil {
		return nil, err
	}
	if err = refreshGeneration(db, &s); err != nil {
		return nil, err
	}
	if s.Job["status"] != "succeeded" {
		return nil, fmt.Errorf("生成尚未完成，请先核对任务状态")
	}
	libraryID := firstNonEmpty(in.LibraryID, s.LibraryID)
	library, err := getLibraryWithDB(db, libraryID)
	if err != nil {
		return nil, err
	}
	if library.State != "online" || library.BackendType == "smb" || library.AccessMode == "read_only" || library.AccessMode == "readonly" {
		return nil, fmt.Errorf("目标资源库当前不可写，请更换目标后重试入库")
	}
	s.LibraryID = libraryID
	if err = saveGeneration(db, s); err != nil {
		return nil, err
	}
	if len(in.Indexes) == 0 || len(in.Indexes) > 4 {
		return nil, fmt.Errorf("请选中要入库的结果")
	}
	results := []map[string]any{}
	for _, index := range in.Indexes {
		if index < 0 || index >= len(s.Outputs) {
			return nil, fmt.Errorf("生成结果序号无效")
		}
		item, adoptErr := adoptGenerationOutput(db, library, s, index)
		if adoptErr != nil {
			_, _ = db.Exec(`INSERT INTO generation_outputs(session_id,output_index,library_id,state,error,updated_at) VALUES(?,?,?,'failed',?,?) ON CONFLICT(session_id,output_index,library_id) DO UPDATE SET state='failed',error=excluded.error,updated_at=excluded.updated_at WHERE state!='adopted'`, s.ID, index, library.ID, adoptErr.Error(), nowRFC3339())
			results = append(results, map[string]any{"index": index, "state": "failed", "error": adoptErr.Error()})
		} else {
			results = append(results, item)
		}
	}
	if err = flushAcquisitionOutbox(db); err != nil {
		return nil, fmt.Errorf("文件已入库，目录同步待重试：%w", err)
	}
	return map[string]any{"items": results}, nil
}
func adoptGenerationOutput(db *sql.DB, library *libraryRecord, s generationSession, index int) (map[string]any, error) {
	output := s.Outputs[index]
	path, _ := output["path"].(string)
	digest, _ := output["sha256"].(string)
	if len(digest) != 64 {
		return nil, fmt.Errorf("生成结果缺少内容校验值")
	}
	actual, err := hashFile(path)
	if err != nil || actual != digest {
		return nil, fmt.Errorf("生成结果已丢失或变化，请核对原任务；无需重新调用模型")
	}
	ext := strings.ToLower(filepath.Ext(path))
	kind := mediaExtensions[ext]
	if kind == "" {
		return nil, fmt.Errorf("此结果格式暂不支持入库")
	}
	relative := filepath.ToSlash(filepath.Join("assets", digest[:2], digest+ext))
	target := filepath.Join(library.RootURI, filepath.FromSlash(relative))
	if err = commitAcquisitionCopy(context.Background(), path, target, digest); err != nil {
		return nil, err
	}
	manifest := map[string]any{"schema_version": "himind.generation-manifest.v1", "session_id": s.ID, "job_id": s.JobID, "capability": s.Capability, "input": s.Input, "references": s.References, "created_at": s.CreatedAt, "output": output, "execution": s.Job["result"], "license_state": "unknown", "usage_state": "provider_reported_or_unknown"}
	sourceProvider, actor := "ai", "generation"
	if s.Capability == "image.process" {
		sourceProvider, actor = "processing", "image-editor"
		manifest["schema_version"] = "himind.image-edit-manifest.v1"
		manifest["width"], manifest["height"] = s.Input["width"], s.Input["height"]
		delete(manifest, "usage_state")
		delete(manifest, "execution")
	}
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Lock before checking hashes so concurrent saves keep every generation event.
	if _, err = tx.Exec(`UPDATE generation_sessions SET created_at=created_at WHERE id=?`, s.ID); err != nil {
		return nil, err
	}
	var id, existingPath, deleted string
	err = tx.QueryRow(`SELECT id,local_path,deleted_at FROM library_items WHERE library_id=? AND sha256=?`, library.ID, digest).Scan(&id, &existingPath, &deleted)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if deleted != "" {
		return nil, fmt.Errorf("相同素材在回收站中，请先恢复")
	}
	dedup := id != ""
	now := nowRFC3339()
	title := string([]rune(s.Prompt)[:min(len([]rune(s.Prompt)), 60)])
	if dedup {
		if h, e := hashFile(existingPath); e != nil || h != digest {
			return nil, fmt.Errorf("已有素材文件不可用，请恢复后重试")
		}
		target = existingPath
		rel, e := filepath.Rel(library.RootURI, target)
		if e == nil {
			relative = filepath.ToSlash(rel)
		}
	} else {
		id = stableID("asset", library.ID+":"+digest)
		_, err = tx.Exec(`INSERT INTO library_items(id,library_id,sha256,relative_path,local_path,kind,title,mime,bytes,source_provider,source_id,adopted_by,adopted_at,updated_at,metadata_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, library.ID, digest, relative, target, kind, title, mime.TypeByExtension(ext), output["size_bytes"], sourceProvider, s.ID, actor, now, now, string(manifestData))
		if err != nil {
			return nil, err
		}
		if err = replaceSearchDocument(tx, searchDocument{EntryType: "library", EntryID: id, LibraryID: library.ID, Kind: kind, Title: title, Filename: filepath.Base(target), Path: relative, UpdatedAt: now}); err != nil {
			return nil, err
		}
	}
	if _, err = tx.Exec(`INSERT OR IGNORE INTO asset_derivations(asset_id,session_id,output_index,manifest_json,created_at) VALUES(?,?,?,?,?)`, id, s.ID, index, string(manifestData), now); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`INSERT INTO generation_outputs(session_id,output_index,library_id,asset_id,state,updated_at) VALUES(?,?,?,?,'adopted',?) ON CONFLICT(session_id,output_index,library_id) DO UPDATE SET state='adopted',asset_id=excluded.asset_id,error='',updated_at=excluded.updated_at`, s.ID, index, library.ID, id, now); err != nil {
		return nil, err
	}
	event := catalogEvent{EventID: stableID("event", s.ID+fmt.Sprint(index)+library.ID), Type: "asset.adopt", LibraryID: library.ID, AssetID: id, RelativePath: relative, Title: title, Kind: kind, SHA256: digest, SourceProvider: sourceProvider, Actor: actor, CreatedAt: now, Deduplicated: dedup, GenerationManifest: manifestData}
	if s.Capability == "audio.speech" {
		event.Role = "voiceover"
		_, err = tx.Exec(`INSERT OR IGNORE INTO audio_derivations(asset_id,job_id,role,purpose,parameters_json,engine,created_at) VALUES(?,?,'voiceover','voiceover',?,'agent',?)`, id, s.JobID, string(manifestData), now)
		if err != nil {
			return nil, err
		}
	}
	eventJSON, _ := json.Marshal(event)
	if _, err = tx.Exec(`INSERT OR IGNORE INTO acquisition_outbox(event_id,library_id,payload_json,created_at) VALUES(?,?,?,?)`, event.EventID, library.ID, string(eventJSON), now); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return map[string]any{"index": index, "state": "adopted", "asset_id": id, "library_id": library.ID, "deduplicated": dedup}, nil
}
func generationPreview(in generationInput) (any, error) {
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	s, err := loadGeneration(db, in.SessionID)
	if err != nil {
		return nil, err
	}
	if err = refreshGeneration(db, &s); err != nil && len(s.Outputs) == 0 {
		return nil, err
	}
	if in.Index < 0 || in.Index >= len(s.Outputs) {
		return nil, fmt.Errorf("生成结果不存在")
	}
	output := s.Outputs[in.Index]
	path, _ := output["path"].(string)
	digest, _ := output["sha256"].(string)
	actual, err := hashFile(path)
	if err != nil || actual != digest {
		return nil, fmt.Errorf("候选文件不可用，请核对原任务")
	}
	kind := mediaExtensions[strings.ToLower(filepath.Ext(path))]
	return map[string]any{"kind": kind, "stream_url": streamURLFor(path, "generation:"+s.ID+fmt.Sprintf(":%d", in.Index))}, nil
}
func generationLineage(in generationInput) (any, error) {
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err = ensureGenerationSchema(db); err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT manifest_json FROM asset_derivations WHERE asset_id=? ORDER BY created_at DESC`, in.AssetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []json.RawMessage{}
	for rows.Next() {
		var data string
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		items = append(items, json.RawMessage(data))
	}
	return map[string]any{"items": items}, rows.Err()
}
