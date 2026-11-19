package main

import (
	"strings"
	"testing"
	"time"
)

// Existing business tests declare their projects in an Agent broker fixture.
// No production RPC in work-log is allowed to mutate these identities.
func withAgentProjects(t *testing.T, names ...string) {
	t.Helper()
	projects := make([]map[string]any, 0, len(names))
	for _, name := range names {
		projects = append(projects, map[string]any{"id": "agent-" + shortHash(name), "name": name, "status": "active", "created_at": time.Now().Format(time.RFC3339), "updated_at": time.Now().Format(time.RFC3339)})
	}
	materialBroker(t, func(name string, input map[string]any) (any, int) {
		switch name {
		case "workspace.registry.project.list":
			return map[string]any{"revision": "fixture-v1", "projects": projects, "workspaces": []any{}}, 200
		case "workspace.registry.context.get":
			return map[string]any{"matched": false}, 200
		default:
			t.Errorf("unexpected project mutation or broker call: %s", name)
			return map[string]any{}, 400
		}
	})
}

func TestProjectManagementRPCsAreRemoved(t *testing.T) {
	for _, action := range []string{"upsert", "rename", "delete", "resolve", "assign", "path.set", "path.map", "claim", "merge", "export", "import", "sync", "migration.preview", "migration.commit", "migration.recover", "migration.rollback"} {
		message := callError(t, "work-log.project."+action, `{}`)
		if !strings.Contains(message, "未知") && !strings.Contains(message, "支持") && !strings.Contains(message, "method") {
			t.Fatalf("removed RPC still resolved: %s: %s", action, message)
		}
	}
}

func TestDuplicateProjectNamesRequireStableIdentity(t *testing.T) {
	snapshot := agentWorkspaceSnapshot{Projects: []agentWorkspaceProject{{ID: "a", Name: "配置"}, {ID: "b", Name: "配置"}}}
	if _, err := findAgentProject(snapshot, "", "配置"); err == nil {
		t.Fatal("ambiguous name selected a project by list order")
	}
	if project, err := findAgentProject(snapshot, "b", "配置"); err != nil || project.ID != "b" {
		t.Fatal("stable project ID did not disambiguate the name")
	}
}

func TestSameNameProjectsKeepIndependentStatisticsAndReportGroups(t *testing.T) {
	root := t.TempDir()
	entries := []entry{{ProjectID: "a", Project: "配置", Date: "2026-11-19"}, {ProjectID: "b", Project: "配置", Date: "2026-11-19"}}
	if err := writeDay(root, dayFile{Date: "2026-11-19", Entries: entries}); err != nil {
		t.Fatal(err)
	}
	projects := projectsFile{Projects: []project{{ID: "a", Name: "配置"}, {ID: "b", Name: "配置"}}}
	stats, err := collectProjectStats(root, projects)
	if err != nil || len(stats.Stats) != 2 || stats.Stats["a"].count != 1 || stats.Stats["b"].count != 1 {
		t.Fatalf("same-name statistics merged: %+v %v", stats, err)
	}
	view := projectEntriesForView(entries, projects)
	if view[0].Project == view[1].Project {
		t.Fatal("same-name projects share a report group")
	}
}

func TestBusinessProjectReferencesRejectInvalidIDsAndAmbiguousAliases(t *testing.T) {
	pf := projectsFile{Projects: []project{{ID: "a", Name: "甲", Aliases: []string{"共同别名"}}, {ID: "b", Name: "乙", Aliases: []string{"共同别名"}}}}
	if _, err := projectIndexForReference(pf, "missing", "甲"); err == nil {
		t.Fatal("invalid ID fell back to a valid name")
	}
	if _, err := projectIndexForReference(pf, "", "共同别名"); err == nil {
		t.Fatal("ambiguous alias selected a project")
	}
	item := workItem{ProjectID: "missing", Project: "甲"}
	if err := resolveWorkItemProject(&pf, &item); err == nil {
		t.Fatal("work item accepted an invalid project ID")
	}
}
