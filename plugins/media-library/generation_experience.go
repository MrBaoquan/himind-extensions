package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

type generationOutputReference struct {
	SessionID string `json:"session_id"`
	Index     int    `json:"index"`
}
type generationCapabilityError struct {
	Code    string
	Message string
}

func (e *generationCapabilityError) Error() string { return e.Message }

func classifyGenerationError(status int, value any) error {
	data, _ := json.Marshal(value)
	raw := strings.ToLower(string(data))
	code := "unavailable"
	message := "Agent 能力暂不可用，请稍后重新检查。"
	switch {
	case strings.Contains(raw, "unknown capability") || strings.Contains(raw, "capability not found") || strings.Contains(raw, "unsupported capability") || strings.Contains(raw, "未注册") || strings.Contains(raw, "未找到能力"):
		code = "unsupported"
		message = "当前 Agent 尚未提供持久生成接口，请更新 Agent 后生成。"
	case status == 401 || status == 403:
		code = "authorization"
		message = "生成调用未获授权，请从 Agent 重新打开媒体库后检查。"
	case strings.Contains(raw, "job not found") || strings.Contains(raw, "任务不存在") || strings.Contains(raw, "执行不存在"):
		code = "not_found"
		message = "Agent 未找到原生成任务。"
	}
	return &generationCapabilityError{code, message}
}
func generationExecutionAvailability(err error) (string, string) {
	if err == nil {
		return "ready", ""
	}
	if typed, ok := err.(*generationCapabilityError); ok {
		return typed.Code, typed.Message
	}
	return "unavailable", "暂时无法检查生成能力，请重试。已有记录与结果仍保留。"
}

func claimGenerationSubmission(db *sql.DB, s *generationSession) (bool, error) {
	s.SubmissionState = "submitting"
	data, err := json.Marshal(s)
	if err != nil {
		return false, err
	}
	result, err := db.Exec(`UPDATE generation_sessions SET payload_json=? WHERE id=? AND json_extract(payload_json,'$.submission_state')='prepared'`, string(data), s.ID)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

// Target-specific projection never changes the saved session's default target.
func projectGenerationAdoptions(db *sql.DB, s *generationSession, libraryID string) {
	for index, output := range s.Outputs {
		var state, assetID, lastError string
		_ = db.QueryRow(`SELECT state,asset_id,error FROM generation_outputs WHERE session_id=? AND output_index=? AND library_id=?`, s.ID, index, libraryID).Scan(&state, &assetID, &lastError)
		output["adoption_state"] = firstNonEmpty(state, "pending")
		output["asset_id"] = assetID
		output["adoption_error"] = lastError
	}
}
func trustedGenerationReference(db *sql.DB, ref generationOutputReference) (string, string, error) {
	s, err := loadGeneration(db, ref.SessionID)
	if err != nil {
		return "", "", err
	}
	if err = refreshGeneration(db, &s); err != nil && len(s.Outputs) == 0 {
		return "", "", fmt.Errorf("参考生成结果暂不可查询，请核对原任务")
	}
	if s.Job["status"] != "succeeded" || ref.Index < 0 || ref.Index >= len(s.Outputs) {
		return "", "", fmt.Errorf("参考生成结果不存在")
	}
	output := s.Outputs[ref.Index]
	path, _ := output["path"].(string)
	digest, _ := output["sha256"].(string)
	if mediaExtensions[strings.ToLower(filepath.Ext(path))] != "image" {
		return "", "", fmt.Errorf("请选择图片生成结果作为参考")
	}
	actual, err := hashFile(path)
	if err != nil || len(digest) != 64 || actual != digest {
		return "", "", fmt.Errorf("参考生成结果已丢失或变化")
	}
	return path, digest, nil
}

func generationVoices() (any, error) {
	catalog, err := generationAgent("media.provider.catalog", map[string]any{})
	if err != nil {
		return nil, err
	}
	// A voice catalogue is optional. Never fabricate names for unsupported adapters.
	routes, _ := catalog["routes"].([]any)
	for _, value := range routes {
		route, _ := value.(map[string]any)
		if route["capability"] != "audio.speech" {
			continue
		}
		selected, _ := route["selected"].(map[string]any)
		if route["state"] != "resolved" {
			return map[string]any{"state": "not_configured", "items": []any{}, "message": "请先在 Agent 的 AI 服务中配置合成语音。"}, nil
		}
		if selected["adapter"] != "fish-audio-tts" {
			return map[string]any{"state": "unsupported", "items": []any{}, "message": "当前服务未提供音色目录，可使用默认音色或填写服务支持的音色 ID。"}, nil
		}
		return generationAgent("media.audio.voice.list", map[string]any{"page_size": 50})
	}
	return map[string]any{"state": "not_configured", "items": []any{}, "message": "尚未配置合成语音。"}, nil
}

func generationReferencePreview(in generationInput) (any, error) {
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	s, err := loadGeneration(db, in.SessionID)
	if err != nil {
		return nil, err
	}
	if in.Index < 0 || in.Index >= len(s.References) {
		return nil, fmt.Errorf("参考图片不存在")
	}
	reference := s.References[in.Index]
	path, _ := reference["reference"].(string)
	digest, _ := reference["sha256"].(string)
	actual, err := hashFile(path)
	if err != nil || len(digest) != 64 || actual != digest {
		return nil, fmt.Errorf("参考副本暂不可用，原生成记录仍保留")
	}
	return map[string]any{"kind": "image", "stream_url": streamURLFor(path, "generation-reference:"+s.ID+fmt.Sprint(in.Index))}, nil
}
