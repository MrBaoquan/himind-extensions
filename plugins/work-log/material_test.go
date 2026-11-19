package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func materialBroker(t *testing.T, dispatch func(string, map[string]any) (any, int)) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-material" {
			t.Error("broker authentication missing")
		}
		var in struct {
			Capability string         `json:"capability"`
			Input      map[string]any `json:"input"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			t.Error("bad broker request")
			w.WriteHeader(400)
			return
		}
		result, status := dispatch(in.Capability, in.Input)
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]any{"result": result})
	}))
	t.Cleanup(server.Close)
	t.Setenv("HIMIND_CAPABILITY_BROKER_URL", server.URL+"/invoke")
	t.Setenv("HIMIND_CAPABILITY_BROKER_TOKEN", "synthetic-material")
}
func materialActivity(source, id, role, at, content string) map[string]any {
	return map[string]any{"session": map[string]any{"id": source + ":native-1", "source": source, "native_id": "native-1", "project_id": "registry-project", "project_name": "测试项目", "working_directory": "F:/real-project"}, "kind": "dialogue", "message": map[string]any{"id": id, "role": role, "timestamp": at, "content": content}, "evidence": map[string]any{"session_id": source + ":native-1", "message_id": id, "content_sha256": "source-hash"}}
}
func TestMaterialCollectUsesOnlyAgentHandlesCrossDayAndPrivacy(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	calls := 0
	materialBroker(t, func(cap string, in map[string]any) (any, int) {
		if cap == "workspace.session.sources" {
			return map[string]any{"sources": []map[string]any{{"source": "codex", "availability": "available"}}}, 200
		}
		if cap != "workspace.session.activity" {
			t.Errorf("unexpected direct capability %s", cap)
		}
		calls++
		if in["project_id"] != "parent" || in["include_children"] != true {
			t.Error("lost Agent project scope")
		}
		if in["from"] != "2026-11-17T00:00:00+08:00" || in["to"] != "2026-11-19T00:00:00+08:00" {
			t.Error("wrong natural-day window")
		}
		rows := []map[string]any{materialActivity("codex", "u1", "user", "2026-11-17T23:50:00+08:00", "实现配置"), materialActivity("codex", "a1", "assistant", "2026-11-17T23:59:00+08:00", "配置完成")}
		cursor := "next"
		if in["cursor"] == "next" {
			rows = []map[string]any{rows[1], materialActivity("codex", "u2", "user", "2026-11-18T00:05:00+08:00", "继续修改")}
			cursor = ""
		}
		return map[string]any{"schema_version": "session_history.v1", "activities": rows, "complete": true, "next_cursor": cursor, "sources": []any{}, "skipped": []any{}}, 200
	})
	value, err := materialCollect(materialInput{LogRoot: root, From: "2026-11-17", To: "2026-11-18", Timezone: "Asia/Shanghai", ProjectID: "parent", IncludeChildren: true})
	if err != nil {
		t.Fatal(err)
	}
	result := value.(map[string]any)
	rows := result["entries"].([]map[string]any)
	if len(rows) != 2 || calls != 2 || result["provider"] != "agent" || result["complete"] != true {
		t.Fatalf("bad material counts: %+v", result)
	}
	total := 0
	for _, row := range rows {
		total += row["minutes"].(int)
		want := map[string]int{"2026-11-17": 10, "2026-11-18": 5}[row["date"].(string)]
		if row["minutes"].(int) != want {
			t.Fatalf("cross-midnight interval assigned to wrong day: %v", row)
		}
		if row["project_id"] != "registry-project" || row["source"] != "codex" {
			t.Fatal("lost stable native identity")
		}
	}
	if total != 15 {
		t.Fatalf("wrong estimates: %d", total)
	}
	dir, _ := sessionMaterialDir("")
	if _, err := os.Stat(filepath.Join(dir, "session-digest-2026-11-17_2026-11-18.json")); err != nil {
		t.Fatal("material not stored in work-log data")
	}
	if _, err := os.Stat(filepath.Join(root, "projects.json")); !os.IsNotExist(err) {
		t.Fatal("collection must not create business project registrations")
	}
	filtered, err := materialCollect(materialInput{LogRoot: root, From: "2026-11-17", To: "2026-11-18", Timezone: "Asia/Shanghai", ProjectID: "parent", IncludeChildren: true, ExcludePaths: []string{"F:/real-project"}})
	if err != nil || filtered.(map[string]any)["total"] != 0 {
		t.Fatal("privacy exclusion not applied")
	}
}
func TestMaterialFailsWithoutAgentAndOnReadErrorsInsteadOfLegacyFallback(t *testing.T) {
	t.Setenv("HIMIND_CAPABILITY_BROKER_URL", "")
	t.Setenv("HIMIND_CAPABILITY_BROKER_TOKEN", "")
	if _, err := materialCollect(materialInput{}); err == nil {
		t.Fatal("must require Agent broker")
	}
	withDataRoot(t)
	root := t.TempDir()
	materialBroker(t, func(name string, _ map[string]any) (any, int) {
		if name == "workspace.session.sources" {
			return map[string]any{"sources": []map[string]any{{"source": "codex", "availability": "available"}}}, 200
		}
		return nil, 409
	})
	if _, err := materialCollect(materialInput{LogRoot: root, Date: "2026-11-18"}); err == nil {
		t.Fatal("must not switch to private client storage")
	}
}
func TestMaterialSourcesConfigurationIsSavedThroughAgent(t *testing.T) {
	var config = map[string]any{"sources": []any{}, "native_roots": map[string]any{"codex": "F:/codex"}}
	materialBroker(t, func(name string, input map[string]any) (any, int) {
		switch name {
		case "workspace.session.source.get":
			return config, 200
		case "workspace.session.source.configure":
			config = input
			return map[string]any{"saved": true, "source_count": 1}, 200
		default:
			t.Errorf("unexpected %s", name)
			return nil, 400
		}
	})
	result, err := sourceSaveCap(input{Sources: []map[string]any{{"id": "example", "root": "F:/sessions", "format": "jsonl"}}})
	if err != nil || result.(map[string]any)["saved"] != true {
		t.Fatal(err)
	}
	got, err := sourceListCap(input{})
	if err != nil || len(got.(map[string]any)["sources"].([]any)) != 1 {
		t.Fatal("Agent source configuration roundtrip failed")
	}
}
func TestMaterialOverlapAndUnknownTimeRemainEstimates(t *testing.T) {
	location := time.FixedZone("test", 8*3600)
	rows := []agentActivity{}
	for i, stamp := range []string{"2026-11-18T10:00:00+08:00", "2026-11-18T10:10:00+08:00"} {
		var r agentActivity
		raw, _ := json.Marshal(materialActivity("codex", fmt.Sprint(i), "user", stamp, "work"))
		json.Unmarshal(raw, &r)
		rows = append(rows, r)
	}
	d := agentDigests(rows, location)
	allocateAgentMinutes(d)
	if len(d) != 1 || d[0].Minutes != 10 || d[0].MinutesSource != "estimated" {
		t.Fatal("bad estimate")
	}
	_ = context.Background()
}

func TestMaterialIntervalsPreserveSubsecondPrecision(t *testing.T) {
	var rows []agentActivity
	for i, stamp := range []string{"2026-11-19T10:00:00.900+08:00", "2026-11-19T10:01:00.100+08:00"} {
		var row agentActivity
		raw, _ := json.Marshal(materialActivity("codex", fmt.Sprint(i), "user", stamp, "work"))
		if err := json.Unmarshal(raw, &row); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	digests := agentDigests(rows, time.FixedZone("test", 8*3600))
	allocateAgentMinutes(digests)
	if len(digests) != 1 || digests[0].Minutes != 0 || len(digests[0].ActivityIntervals) != 1 {
		t.Fatalf("subsecond interval rounded up or dropped: %+v", digests)
	}
}

func TestReconcileRequiresCompleteUnfilteredCoverageAndKeepsActivityDays(t *testing.T) {
	withDataRoot(t)
	root, material := t.TempDir(), t.TempDir()
	call(t, "work-log.entry.upsert", `{"log_root":`+quote(root)+`,"entries":[{"date":"2026-11-17","source":"codex","session_id":"same","title":"day1"},{"date":"2026-11-18","source":"codex","session_id":"same","title":"day2"}]}`)
	doc := map[string]any{"from": "2026-11-17", "to": "2026-11-18", "complete": false, "scope": map[string]any{"scope": "all"}, "source_statuses": []map[string]any{{"source": "codex", "availability": "available"}}, "sessions": []map[string]any{{"date": "2026-11-17", "source": "codex", "session_id": "same"}}}
	path := filepath.Join(material, "session-digest-test.json")
	save := func() {
		body, _ := json.Marshal(doc)
		if e := os.WriteFile(path, body, 0600); e != nil {
			t.Fatal(e)
		}
	}
	check := func(stale, unverified float64) {
		r := call(t, "work-log.entry.reconcile", `{"log_root":`+quote(root)+`,"from":"2026-11-17","to":"2026-11-18","material_root":`+quote(material)+`}`)
		if r["stale_count"] != stale || r["unverified_count"] != unverified || r["matched"] != float64(1) {
			t.Fatalf("wrong coverage: %v", r)
		}
	}
	save()
	check(0, 1)
	doc["complete"] = true
	save()
	check(1, 0)
	doc["privacy_filtered"] = true
	save()
	check(0, 1)
}

func TestMaterialSourceTimeoutKeepsOtherSourcesAndRejectsStrict(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	materialBroker(t, func(name string, in map[string]any) (any, int) {
		if name == "workspace.session.sources" {
			return map[string]any{"sources": []map[string]any{
				{"source": "vscode", "availability": "available"},
				{"source": "codex", "availability": "available"},
			}}, 200
		}
		source := in["sources"].([]any)[0].(string)
		if source == "vscode" {
			time.Sleep(400 * time.Millisecond)
		}
		rows := []map[string]any{materialActivity(source, "user1", "user", "2026-11-18T10:00:00+08:00", "测试会话来源超时")}
		return map[string]any{"schema_version": "session_history.v1", "activities": rows, "complete": true}, 200
	})
	in := materialInput{LogRoot: root, Date: "2026-11-18", Timezone: "Asia/Shanghai"}
	value, err := materialCollectWithSourceTimeout(in, 150*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	result := value.(map[string]any)
	warnings := result["warnings"].([]map[string]any)
	rows := result["entries"].([]map[string]any)
	if result["complete"] != false || len(rows) != 1 || rows[0]["source"] != "codex" || len(warnings) != 1 || warnings[0]["source"] != "vscode" || warnings[0]["reason"] != "activity_read_timeout" {
		t.Fatalf("partial source coverage was not preserved: %v", warnings)
	}
	in.Strict = true
	if _, err := materialCollectWithSourceTimeout(in, 150*time.Millisecond); err == nil {
		t.Fatal("strict mode must reject the incomplete collection")
	}
	in.Strict = false
	in.Sources = []string{"vscode"}
	if _, err := materialCollectWithSourceTimeout(in, 150*time.Millisecond); err == nil {
		t.Fatal("an empty collection with a timeout must not pass")
	}
}

func TestTrimMaterialDigestHidesSyntheticCodexIDTitle(t *testing.T) {
	digest := materialDigest{Source: "codex", SessionID: "01a1abcd-1234-5678", Title: "Codex 会话 01a1abcd", UserPrompts: []string{"untrusted task text"}}
	trimMaterialDigest(&digest, 12, 800)
	if digest.Title != "Codex 会话（标题待补）" {
		t.Fatalf("synthetic session ID must not be shown as a human title: %q", digest.Title)
	}
}
