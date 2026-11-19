package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAgentMaterialSurvivesAIEnrichmentRepeatBackfillLocksAndExport(t *testing.T) {
	withDataRoot(t)
	materialBroker(t, func(name string, _ map[string]any) (any, int) {
		if name != "workspace.registry.project.list" {
			t.Fatalf("unexpected capability %s", name)
		}
		return map[string]any{"revision": "r1", "projects": []map[string]any{{"id": "registry-child", "name": "设备配置"}}, "workspaces": []any{}}, 200
	})
	root := t.TempDir()
	evidence := []map[string]any{{"session_id": "claude_code:native-1", "message_id": "m1", "snapshot_revision": "synthetic-revision", "content_sha256": "synthetic-hash"}}
	base := entry{Source: "claude-code", SessionID: "native-1", Date: "2026-11-16", Project: "设备配置", ProjectID: "registry-child", Provider: "agent", Minutes: 10, MinutesSource: "estimated", MinutesMethod: "message_intervals_v1", Evidence: evidence, Title: "素材", Refs: []ref{{Type: "session", Value: "himind-session:claude_code:native-1"}}}
	next := base
	next.Date = "2026-11-17"
	next.Minutes = 5
	ai := entry{Source: "claude-code", SessionID: "native-1", Date: "2026-11-16", Title: "梳理设备配置", Summary: "完成合成配置工作", Minutes: 999, ProjectID: "forged", Evidence: []map[string]any{{"forged": true}}}
	aiNext := entry{Source: next.Source, SessionID: next.SessionID, Date: next.Date, Title: "继续设备配置", Summary: "补记第二天的合成配置活动"}
	params := map[string]any{"log_root": root, "resolve_projects": false, "base_step": "collect", "entries_step": "ai", "workflow_context": map[string]any{"steps": map[string]any{"collect": map[string]any{"entries": []entry{base, next}}, "ai": map[string]any{"entries": []entry{ai, aiNext}}}}}
	raw, _ := json.Marshal(params)
	call(t, "work-log.entry.upsert", string(raw))
	call(t, "work-log.entry.upsert", string(raw))
	read := call(t, "work-log.entry.read", `{"log_root":`+quote(root)+`,"date":"2026-11-16"}`)
	rows := read["entries"].([]any)
	if len(rows) != 1 {
		t.Fatalf("repeat duplicated entry: %v", rows)
	}
	got := rows[0].(map[string]any)
	if got["minutes"] != float64(10) || got["project_id"] != "registry-child" || got["minutes_method"] != "message_intervals_v1" || got["summary"] != "完成合成配置工作" {
		t.Fatalf("AI replaced deterministic facts: %v", got)
	}
	if len(got["evidence"].([]any)) != 1 || got["evidence"].([]any)[0].(map[string]any)["message_id"] != "m1" {
		t.Fatal("lost evidence")
	}
	if got["project_id"] != "registry-child" {
		t.Fatal("log must reference Agent project ID")
	}
	id := got["entry_id"].(string)
	if id != makeEntryID(base) {
		t.Fatal("backend migration changed legacy log key")
	}
	readNext := call(t, "work-log.entry.read", `{"log_root":`+quote(root)+`,"date":"2026-11-17"}`)
	nextRows := readNext["entries"].([]any)
	if len(nextRows) != 1 || nextRows[0].(map[string]any)["minutes"] != float64(5) {
		t.Fatalf("backfill lost or duplicated second-day activity: %v", nextRows)
	}
	call(t, "work-log.entry.lock", `{"log_root":`+quote(root)+`,"date":"2026-11-16","entry_id":`+quote(id)+`,"locked":true}`)
	base.Title = "must not overwrite"
	base.Minutes = 20
	data, _ := json.Marshal(map[string]any{"log_root": root, "resolve_projects": false, "entries": []entry{base}})
	result := call(t, "work-log.entry.upsert", string(data))
	if result["locked_skipped"] != float64(1) {
		t.Fatal("automatic import bypassed lock")
	}
	exported := call(t, "work-log.entry.export", `{"log_root":`+quote(root)+`}`)
	text := exported["json"].(string)
	if !strings.Contains(text, "registry-child") || !strings.Contains(text, "synthetic-hash") || !strings.Contains(text, "2026-11-17") {
		t.Fatal("export lost cross-day provenance")
	}
	target := t.TempDir()
	call(t, "work-log.entry.import", `{"log_root":`+quote(target)+`,"entries_json":`+quote(text)+`}`)
	imported := call(t, "work-log.entry.read", `{"log_root":`+quote(target)+`,"date":"2026-11-16"}`)
	if imported["entries"].([]any)[0].(map[string]any)["project_id"] != "registry-child" {
		t.Fatal("import lost workspace identity")
	}
}
func TestAgentZeroEstimateReplacesOldEstimateWithoutInventingTurnTime(t *testing.T) {
	current := entry{Minutes: 30, MinutesSource: "measured", Evidence: []map[string]any{{"message_id": "old"}}}
	incoming := entry{Provider: "agent", Minutes: 0, MinutesSource: "estimated", MinutesMethod: "message_intervals_v1", Evidence: []map[string]any{{"message_id": "new"}}}
	merged := mergeEntry(current, incoming)
	if merged.Minutes != 0 || merged.MinutesSource != "estimated" || merged.Evidence[0]["message_id"] != "new" {
		t.Fatalf("kept stale fabricated estimate: %+v", merged)
	}
}

func TestManualMinutesClearAutomaticMethodWithoutLosingEvidence(t *testing.T) {
	current := entry{Minutes: 30, MinutesSource: "estimated", MinutesMethod: "message_intervals_v1", Evidence: []map[string]any{{"message_id": "evidence"}}, ActivityIntervals: []activityInterval{{From: "start", To: "end"}}}
	merged := mergeEntry(current, entry{Minutes: 0, MinutesSource: "manual"})
	if merged.Minutes != 0 || merged.MinutesMethod != "" || len(merged.ActivityIntervals) != 0 || len(merged.Evidence) != 1 {
		t.Fatalf("manual correction retained automatic method: %+v", merged)
	}
}
