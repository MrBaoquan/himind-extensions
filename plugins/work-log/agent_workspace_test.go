package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAgentWorkspaceUsesAuthoritativeSnapshot(t *testing.T) {
	materialBroker(t, func(name string, input map[string]any) (any, int) {
		if name != "workspace.registry.project.list" || len(input) != 0 {
			t.Fatalf("unexpected registry call %s: %v", name, input)
		}
		return map[string]any{
			"revision":   "r1",
			"projects":   []map[string]any{{"id": "parent", "name": "父项目"}, {"id": "child", "name": "子项目", "parent_id": "parent"}},
			"workspaces": []map[string]any{{"id": "w1", "project_id": "child", "path": "F:/source", "role": "source"}, {"id": "w2", "project_id": "child", "path": "F:/worktree", "role": "worktree"}},
		}, 200
	})
	snapshot, err := loadAgentWorkspace(context.Background())
	if err != nil || snapshot.Revision != "r1" || len(snapshot.Projects) != 2 || snapshot.Projects[1].ParentID != "parent" || len(snapshot.Workspaces) != 2 {
		t.Fatalf("bad Agent snapshot: %+v, %v", snapshot, err)
	}
}

func TestLoadAgentWorkspaceRejectsIncompleteOrInvalidSnapshot(t *testing.T) {
	result := map[string]any{"projects": []any{}, "workspaces": []any{}}
	materialBroker(t, func(string, map[string]any) (any, int) { return result, 200 })
	if _, err := loadAgentWorkspace(context.Background()); err == nil {
		t.Fatal("missing revision accepted")
	}
	result = map[string]any{"revision": "r2", "projects": []map[string]any{{"id": "p", "name": "P"}}, "workspaces": []map[string]any{{"project_id": "other", "path": "F:/unknown"}}}
	if _, err := loadAgentWorkspace(context.Background()); err == nil {
		t.Fatal("foreign binding accepted")
	}
}

func TestAgentProjectViewKeepsBusinessStateAcrossRename(t *testing.T) {
	snapshot := agentWorkspaceSnapshot{Projects: []agentWorkspaceProject{
		{ID: "parent", Name: "父项目"}, {ID: "child", Name: "新名称", ParentID: "parent"},
	}, Workspaces: []agentWorkspaceBinding{{ID: "w1", ProjectID: "child", Path: "F:/source", Role: "source"}}}
	business := workspaceBusiness{Projects: map[string]projectWork{"child": {Priority: "P1", Goal: "完成工作"}}}
	view := projectViewFromAgent(snapshot, business)
	if len(view.Projects) != 2 || view.Projects[1].ID != "child" || view.Projects[1].Name != "新名称" || view.Projects[1].ParentID != "parent" || view.Projects[1].Work.Goal != "完成工作" || len(view.Projects[1].Paths) != 1 {
		t.Fatalf("Agent identity and business state diverged: %+v", view)
	}
}

func TestWorkspaceModeStoresOnlyBusinessStateAndRejectsIdentityEdits(t *testing.T) {
	root := t.TempDir()
	materialBroker(t, func(name string, _ map[string]any) (any, int) {
		if name != "workspace.registry.project.list" {
			t.Fatalf("unexpected capability: %s", name)
		}
		return map[string]any{"revision": "r1", "projects": []map[string]any{{"id": "agent", "name": "Agent 项目"}}, "workspaces": []any{}}, 200
	})
	initial := workspaceBusiness{SchemaVersion: workspaceBusinessSchemaVersion, Projects: map[string]projectWork{"orphan": {Goal: "历史目标"}}}
	body, _ := json.Marshal(initial)
	if err := os.WriteFile(workspaceBusinessPath(root), body, 0600); err != nil {
		t.Fatal(err)
	}
	projected, err := readProjects(root)
	if err != nil || len(projected.Projects) != 1 || projected.Projects[0].ID != "agent" {
		t.Fatalf("failed to project Agent projects: %+v, %v", projected, err)
	}
	projected.Projects[0].Work.Goal = "当前目标"
	if err := writeProjects(root, projected); err != nil {
		t.Fatal(err)
	}
	stored, err := readWorkspaceBusiness(root)
	if err != nil || stored.Projects["agent"].Goal != "当前目标" || stored.Projects["orphan"].Goal != "历史目标" {
		t.Fatalf("business state not preserved: %+v, %v", stored, err)
	}
	projected.Projects[0].Name = "私有项目名称"
	if err := writeProjects(root, projected); err == nil {
		t.Fatal("private identity edit was accepted")
	}
	stored, err = readWorkspaceBusiness(root)
	if err != nil || stored.Projects["agent"].Goal != "当前目标" {
		t.Fatal("rejected identity edit changed business state")
	}
}

func TestWorkspaceModeRejectsBrokenStateWithoutLegacyFallback(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(workspaceBusinessPath(root), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readProjects(root); err == nil {
		t.Fatal("invalid workspace state silently used legacy registry")
	}
}

func TestWorkspaceModeEntryUpsertPreservesHistoryAndManualMinutes(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	state, _ := json.Marshal(workspaceBusiness{SchemaVersion: workspaceBusinessSchemaVersion, Projects: map[string]projectWork{}})
	if err := os.WriteFile(workspaceBusinessPath(root), state, 0600); err != nil {
		t.Fatal(err)
	}
	materialBroker(t, func(name string, _ map[string]any) (any, int) {
		switch name {
		case "workspace.registry.project.list":
			return map[string]any{"revision": "r1", "projects": []map[string]any{{"id": "a", "name": "项目 A"}, {"id": "b", "name": "项目 B"}}, "workspaces": []any{}}, 200
		case "workspace.registry.context.get":
			return map[string]any{"matched": false, "snapshot": map[string]any{}}, 200
		default:
			t.Fatalf("unexpected capability: %s", name)
			return nil, 500
		}
	})
	original := entry{Source: "codex", SessionID: "session", Provider: "agent", ProjectID: "a", Project: "原始名称", Title: "工作", Minutes: 25, MinutesSource: "manual"}
	if _, err := upsert(input{LogRoot: root, Date: "2026-11-19", Entry: &original}); err != nil {
		t.Fatal(err)
	}
	rescan := entry{Source: "codex", SessionID: "session", Provider: "agent", ProjectID: "b", Title: "更新", Minutes: 90, MinutesSource: "estimated", MinutesMethod: "message_intervals_v2"}
	if _, err := upsert(input{LogRoot: root, Date: "2026-11-19", Entry: &rescan}); err != nil {
		t.Fatal(err)
	}
	unknown := entry{Source: "codex", SessionID: "unknown", Provider: "agent", RepoPath: "F:/unknown", Project: "未知项目", Title: "未归属"}
	if _, err := upsert(input{LogRoot: root, Date: "2026-11-19", Entry: &unknown}); err != nil {
		t.Fatal(err)
	}
	day, err := readDay(root, "2026-11-19")
	if err != nil || len(day.Entries) != 2 {
		t.Fatalf("unexpected day: %+v, %v", day, err)
	}
	for _, item := range day.Entries {
		switch item.SessionID {
		case "session":
			if item.ProjectID != "a" || item.Project != "项目 A" || item.MinutesSource != "manual" || item.Minutes != 25 {
				t.Fatalf("rescan overwrote history or manual minutes: %+v", item)
			}
		case "unknown":
			if item.ProjectID != "" || item.Project != "" || item.ProjectHint != "未知项目" {
				t.Fatalf("unknown path invented a project: %+v", item)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(root, "projects.json")); !os.IsNotExist(err) {
		t.Fatal("entry upsert created a private project registry")
	}
}

func TestWorkspaceModeEntryPatchRequiresAgentIdentity(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	state, _ := json.Marshal(workspaceBusiness{SchemaVersion: workspaceBusinessSchemaVersion, Projects: map[string]projectWork{}})
	if err := os.WriteFile(workspaceBusinessPath(root), state, 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeDay(root, dayFile{Date: "2026-11-19", Entries: []entry{{EntryID: "log", Date: "2026-11-19", Source: "codex", Title: "工作", Project: "历史名称", ProjectID: "a", MinutesSource: "estimated", MinutesMethod: "message_intervals_v2"}}}); err != nil {
		t.Fatal(err)
	}
	materialBroker(t, func(name string, _ map[string]any) (any, int) {
		if name != "workspace.registry.project.list" {
			t.Fatalf("unexpected capability: %s", name)
		}
		return map[string]any{"revision": "r1", "projects": []map[string]any{{"id": "a", "name": "项目 A"}, {"id": "b", "name": "项目 B"}}, "workspaces": []any{}}, 200
	})
	_, err := entryPatchCap(input{LogRoot: root, Date: "2026-11-19", EntryID: "log", Patch: map[string]any{"feature": "功能"}})
	if err != nil {
		t.Fatal(err)
	}
	day, _ := readDay(root, "2026-11-19")
	if day.Entries[0].Project != "历史名称" {
		t.Fatal("unrelated edit rewrote historical name")
	}
	_, err = entryPatchCap(input{LogRoot: root, Date: "2026-11-19", EntryID: "log", Patch: map[string]any{"project_id": "b", "minutes": 45}})
	if err != nil {
		t.Fatal(err)
	}
	day, _ = readDay(root, "2026-11-19")
	item := day.Entries[0]
	if item.Project != "项目 B" || item.ProjectID != "b" || item.Minutes != 45 || item.MinutesSource != "manual" || item.MinutesMethod != "" {
		t.Fatalf("explicit assignment or manual minutes lost: %+v", item)
	}
	if _, err := entryPatchCap(input{LogRoot: root, Date: "2026-11-19", EntryID: "log", Patch: map[string]any{"project": "不存在"}}); err == nil {
		t.Fatal("unknown name silently created a private project")
	}
	if _, err := os.Stat(filepath.Join(root, "projects.json")); !os.IsNotExist(err) {
		t.Fatal("patch created a private project registry")
	}
}

func TestWorkspaceModeStatsUseIdentityNotHistoricalName(t *testing.T) {
	root := t.TempDir()
	state, _ := json.Marshal(workspaceBusiness{SchemaVersion: workspaceBusinessSchemaVersion, Projects: map[string]projectWork{}})
	if err := os.WriteFile(workspaceBusinessPath(root), state, 0600); err != nil {
		t.Fatal(err)
	}
	items := []entry{
		{EntryID: "owned", Date: "2026-11-19", Project: "项目 B", ProjectID: "a"},
		{EntryID: "unassigned", Date: "2026-11-19", Project: "项目 B"},
	}
	if err := writeDay(root, dayFile{Date: "2026-11-19", Entries: items}); err != nil {
		t.Fatal(err)
	}
	projects := projectsFile{Projects: []project{{ID: "a", Name: "项目 A"}, {ID: "b", Name: "项目 B"}}}
	stats, err := collectProjectStats(root, projects)
	if err != nil || stats.Stats["a"] == nil || stats.Stats["a"].count != 1 || stats.Stats["b"] != nil || len(stats.Pending) != 1 {
		t.Fatalf("historical name changed ownership: %+v, %v", stats, err)
	}
}

func TestWorkspaceModeReportsAndFiltersUseAgentIdentity(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	state, _ := json.Marshal(workspaceBusiness{SchemaVersion: workspaceBusinessSchemaVersion, Projects: map[string]projectWork{}})
	if err := os.WriteFile(workspaceBusinessPath(root), state, 0600); err != nil {
		t.Fatal(err)
	}
	items := []entry{
		{EntryID: "owned", Date: "2026-11-19", Title: "已归属", Project: "项目 B", ProjectID: "a"},
		{EntryID: "unassigned", Date: "2026-11-19", Title: "待归属", Project: "项目 B"},
	}
	if err := writeDay(root, dayFile{Date: "2026-11-19", Entries: items}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(dayPath(root, "2026-11-19"))
	materialBroker(t, func(string, map[string]any) (any, int) {
		return map[string]any{"revision": "r1", "projects": []map[string]any{{"id": "a", "name": "项目 A"}, {"id": "b", "name": "项目 B"}}, "workspaces": []any{}}, 200
	})
	for _, tc := range []struct {
		project string
		count   int
	}{{"项目 A", 1}, {"项目 B", 0}, {"a", 1}} {
		result, err := listEntries(input{LogRoot: root, Date: "2026-11-19", Project: tc.project})
		if err != nil || result.(map[string]any)["total"] != tc.count {
			t.Fatalf("filter %q: %+v, %v", tc.project, result, err)
		}
		calendar, err := archiveCalendar(input{LogRoot: root, Year: 2026, Month: 11, Project: tc.project})
		if err != nil || calendar.(map[string]any)["total_entries"] != tc.count {
			t.Fatalf("calendar filter %q: %+v, %v", tc.project, calendar, err)
		}
	}
	report, err := renderPeriodReport(root, "daily", "2026-11-19", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	markdown := report["markdown"].(string)
	if !strings.Contains(markdown, "### 项目 A") || !strings.Contains(markdown, "### 未归类") || strings.Contains(markdown, "### 项目 B") {
		t.Fatalf("report grouped by historical text: %s", markdown)
	}
	after, _ := os.ReadFile(dayPath(root, "2026-11-19"))
	if string(before) != string(after) {
		t.Fatal("report or filtering rewrote historical log")
	}
}

func TestWorkspaceProjectScopeKeepsMetricsIndependentOfListLimit(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	state, _ := json.Marshal(workspaceBusiness{SchemaVersion: workspaceBusinessSchemaVersion, Projects: map[string]projectWork{}})
	if err := os.WriteFile(workspaceBusinessPath(root), state, 0600); err != nil {
		t.Fatal(err)
	}
	span := []activityInterval{{From: "2026-11-19T10:00:00+08:00", To: "2026-11-19T11:00:00+08:00"}}
	items := []entry{
		{EntryID: "a", Date: "2026-11-19", Source: "codex", SessionID: "a", ProjectID: "parent", ActivityIntervals: span},
		{EntryID: "b", Date: "2026-11-19", Source: "codex", SessionID: "b", ProjectID: "child", ActivityIntervals: span},
		{EntryID: "c", Date: "2026-11-19", Source: "codex", SessionID: "c", ProjectID: "grand", ActivityIntervals: span},
		{EntryID: "other", Date: "2026-11-19", Source: "codex", SessionID: "other", ProjectID: "other", ActivityIntervals: span},
	}
	if err := writeDay(root, dayFile{Date: "2026-11-19", Entries: items}); err != nil {
		t.Fatal(err)
	}
	materialBroker(t, func(string, map[string]any) (any, int) {
		return map[string]any{"revision": "r1", "projects": []map[string]any{{"id": "parent", "name": "父项目"}, {"id": "child", "name": "子项目", "parent_id": "parent"}, {"id": "grand", "name": "孙项目", "parent_id": "child"}, {"id": "other", "name": "其他"}}, "workspaces": []any{}}, 200
	})
	result, err := listEntries(input{LogRoot: root, Date: "2026-11-19", ProjectID: "parent", IncludeChildren: true, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	listed := result.(map[string]any)
	metrics := listed["activity_metrics"].(activityMetrics)
	if listed["total"] != 1 || listed["matched_total"] != 3 || metrics.CoverageMinutes != 60 || metrics.SessionMinutes != 180 {
		t.Fatalf("limited list changed scope metrics: %+v", listed)
	}
	calendar, err := archiveCalendar(input{LogRoot: root, Year: 2026, Month: 11, ProjectID: "parent", IncludeChildren: true})
	if err != nil {
		t.Fatal(err)
	}
	cm := calendar.(map[string]any)["activity_metrics"].(activityMetrics)
	if cm.CoverageMinutes != 60 || cm.SessionMinutes != 180 {
		t.Fatalf("calendar scope differs: %+v", cm)
	}
	scoped, err := renderPeriodReportScoped(root, "daily", "2026-11-19", "", "", nil, "parent", true)
	if err != nil {
		t.Fatal(err)
	}
	rm := scoped["activity_metrics"].(activityMetrics)
	if rm.CoverageMinutes != 60 || rm.SessionMinutes != 180 || scoped["project_id"] != "parent" {
		t.Fatalf("report scope differs: %+v", scoped)
	}
	if _, err := readReport(input{LogRoot: root, Date: "2026-11-19"}); err == nil {
		t.Fatal("unscoped report read a scoped archive")
	}
	if _, err := readReport(input{LogRoot: root, Date: "2026-11-19", ProjectID: "parent", IncludeChildren: true}); err != nil {
		t.Fatalf("scoped report is not readable: %v", err)
	}
	full, err := renderPeriodReport(root, "daily", "2026-11-19", "", "", nil)
	if err != nil || full["key"] == scoped["key"] || full["activity_metrics"].(activityMetrics).SessionMinutes != 240 {
		t.Fatalf("scoped report overwrote full report: %+v, %v", full, err)
	}
	if _, err := listEntries(input{LogRoot: root, Date: "2026-11-19", IncludeChildren: true}); err == nil {
		t.Fatal("child scope without project ID accepted")
	}
}
