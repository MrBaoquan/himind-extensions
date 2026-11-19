package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// The no-tools runtime inlines JSON in its prompt and allows at most 16,000
// characters. Keep spare capacity for workflow metadata and Windows transport.
const summaryMaterialBytes = 12_000
const summaryMaterialSessions = 32
const summaryRequestRunes = 360
const summaryMinimumRequestRunes = 48

type summarySession struct {
	Source    string `json:"source"`
	SessionID string `json:"session_id"`
	Date      string `json:"date"`
	Project   string `json:"project,omitempty"`
	Title     string `json:"title,omitempty"`
	Request   string `json:"request,omitempty"`
	Result    string `json:"result,omitempty"`
}

func summarySourceDigest(digest materialDigest) materialDigest {
	digest.UserPrompts = append([]string(nil), digest.UserPrompts...)
	digest.AssistantExcerpts = append([]string(nil), digest.AssistantExcerpts...)
	return digest
}

func summaryDigestKey(digest materialDigest) string {
	return digest.Source + "\x00" + digest.SessionID + "\x00" + digest.Date
}

type summaryInput struct {
	Sessions     []summarySession `json:"sessions"`
	OmittedCount int              `json:"omitted_count"`
}

// This artifact is built from privacy-filtered digests. Each included session
// keeps its exact identity; the full evidence stays in the deterministic digest.
func summaryMaterial(digests []materialDigest) ([]byte, error) {
	input := summaryInput{Sessions: make([]summarySession, 0, summaryMaterialSessions)}
	for _, digest := range digests {
		if len(input.Sessions) == summaryMaterialSessions {
			break
		}
		row := summarySession{
			Source: digest.Source, SessionID: digest.SessionID, Date: digest.Date,
			Project: summaryExcerpt(digest.Project, 32), Title: summaryExcerpt(digest.Title, 48),
			Request: summaryRequest(digest.UserPrompts),
		}
		for i := len(digest.AssistantExcerpts) - 1; i >= 0; i-- {
			text := strings.TrimSpace(digest.AssistantExcerpts[i])
			if text != "" && text != "{" && text != "I'" && len([]rune(text)) > 2 {
				row.Result = summaryExcerpt(text, 140)
				break
			}
		}
		input.Sessions = append(input.Sessions, row)
	}
	input.OmittedCount = len(digests) - len(input.Sessions)
	for {
		encoded, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		if len(encoded) <= summaryMaterialBytes {
			return encoded, nil
		}
		trimmed := false
		for i := range input.Sessions {
			row := &input.Sessions[i]
			requestRunes := len([]rune(row.Request))
			if requestRunes > summaryMinimumRequestRunes {
				row.Request = summaryExcerpt(row.Request, max(summaryMinimumRequestRunes, requestRunes-48))
				trimmed = true
			}
			if len([]rune(row.Result)) > 0 {
				row.Result = summaryExcerpt(row.Result, max(0, len([]rune(row.Result))-24))
				trimmed = true
			}
		}
		if !trimmed {
			return nil, fmt.Errorf("session identities exceed the %d-byte AI material limit", summaryMaterialBytes)
		}
	}
}

func meaningfulSummaryPrompt(raw string) string {
	text := strings.Join(strings.Fields(raw), " ")
	if strings.HasPrefix(strings.ToLower(text), "<environment_context") {
		if end := strings.Index(strings.ToLower(text), "</environment_context>"); end >= 0 {
			text = strings.TrimSpace(text[end+len("</environment_context>"):])
		} else {
			return ""
		}
	}
	const subagentPrefix = "You are a subagent spawned by another session."
	if strings.HasPrefix(strings.ToLower(text), strings.ToLower(subagentPrefix)) {
		text = strings.TrimSpace(text[len(subagentPrefix):])
	}
	if strings.HasPrefix(text, "【你的角色】") {
		start := -1
		for _, marker := range []string{"执行协作任务：", "用户目标：", "任务目标：", "任务：", "Task:"} {
			if index := strings.LastIndex(text, marker); index > start {
				start = index
			}
		}
		if start >= 0 {
			text = strings.TrimSpace(text[start:])
		} else {
			return ""
		}
	}
	return text
}

func summaryRequest(prompts []string) string {
	meaningful := make([]string, 0, len(prompts))
	for _, raw := range prompts {
		text := meaningfulSummaryPrompt(raw)
		lower := strings.ToLower(strings.TrimRight(text, " ，。！？、.!?"))
		switch lower {
		case "", "继续", "继续推进", "好的", "好的继续", "好的请继续", "好的请开始推进", "好的，请开始推进",
			"开始", "嗯", "你好", "ok", "okay", "thanks", "谢谢":
			continue
		}
		if strings.HasPrefix(lower, "you are codex") || strings.HasPrefix(lower, "you are an independent verification agent") {
			continue
		}
		meaningful = append(meaningful, text)
	}
	if len(meaningful) > 3 {
		meaningful = append([]string{meaningful[0]}, meaningful[len(meaningful)-3:]...)
	}
	parts := make([]string, 0, len(meaningful))
	remaining := summaryRequestRunes
	for _, text := range meaningful {
		if remaining <= 0 {
			break
		}
		part := summaryExcerpt(text, remaining)
		if part == "" {
			continue
		}
		parts = append(parts, part)
		remaining -= len([]rune(part)) + 1
	}
	return strings.Join(parts, "；")
}

func summaryExcerpt(text string, maxRunes int) string {
	text = strings.Join(strings.Fields(text), " ")
	for byteIndex := range text {
		if maxRunes == 0 {
			return text[:byteIndex]
		}
		maxRunes--
	}
	return text
}
