package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MrBaoquan/himind-extensions/sdk/capability"
)

// Agent owns project identity and path bindings. Work-log must not invent a
// replacement identity if this service is unavailable or returns bad data.
type agentWorkspaceProject struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Aliases     []string `json:"aliases"`
	ParentID    string   `json:"parent_id"`
	Status      string   `json:"status"`
	Kind        string   `json:"kind"`
	Description string   `json:"description"`
	Color       string   `json:"color"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

type agentWorkspaceBinding struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Path      string `json:"path"`
	Role      string `json:"role"`
}

type agentWorkspaceSnapshot struct {
	Revision   string                  `json:"revision"`
	Projects   []agentWorkspaceProject `json:"projects"`
	Workspaces []agentWorkspaceBinding `json:"workspaces"`
}

func loadAgentWorkspace(ctx context.Context) (agentWorkspaceSnapshot, error) {
	var snapshot agentWorkspaceSnapshot
	if err := capability.Invoke(ctx, "workspace.registry.project.list", map[string]any{}, &snapshot); err != nil {
		return snapshot, err
	}
	if strings.TrimSpace(snapshot.Revision) == "" || snapshot.Projects == nil || snapshot.Workspaces == nil {
		return agentWorkspaceSnapshot{}, errors.New("Agent 工作区登记快照不完整")
	}
	seen := make(map[string]bool, len(snapshot.Projects))
	for _, project := range snapshot.Projects {
		if strings.TrimSpace(project.ID) == "" || strings.TrimSpace(project.Name) == "" || seen[project.ID] {
			return agentWorkspaceSnapshot{}, errors.New("Agent 工作区项目身份无效或重复")
		}
		seen[project.ID] = true
	}
	for _, binding := range snapshot.Workspaces {
		if !seen[binding.ProjectID] || strings.TrimSpace(binding.Path) == "" {
			return agentWorkspaceSnapshot{}, errors.New("Agent 工作区目录绑定无效")
		}
	}
	return snapshot, nil
}

func findAgentProject(snapshot agentWorkspaceSnapshot, id, name string) (agentWorkspaceProject, error) {
	id, name = strings.TrimSpace(id), strings.TrimSpace(name)
	var match *agentWorkspaceProject
	for _, project := range snapshot.Projects {
		if id != "" {
			if project.ID == id {
				return project, nil
			}
			continue
		}
		if name != "" && (strings.EqualFold(project.Name, name) || aliasMatches(project.Aliases, name)) {
			if match != nil {
				return agentWorkspaceProject{}, errors.New("项目名称存在歧义，请使用 Agent 项目 ID")
			}
			candidate := project
			match = &candidate
		}
	}
	if match != nil {
		return *match, nil
	}
	return agentWorkspaceProject{}, errors.New("Agent 工作区中未找到目标项目")
}

// resolveAgentEntries only resolves incoming records. Existing log entries
// retain their historical ownership when the Agent registry later changes.
func resolveAgentEntries(ctx context.Context, entries []entry, snapshot agentWorkspaceSnapshot) error {
	byID := make(map[string]agentWorkspaceProject, len(snapshot.Projects))
	for _, project := range snapshot.Projects {
		byID[project.ID] = project
	}
	for i := range entries {
		item := &entries[i]
		id := strings.TrimSpace(item.ProjectID)
		if id == "" && strings.TrimSpace(item.RepoPath) != "" {
			var result struct {
				Matched  bool `json:"matched"`
				Snapshot struct {
					Project *agentWorkspaceProject `json:"project"`
				} `json:"snapshot"`
			}
			if err := capability.Invoke(ctx, "workspace.registry.context.get", map[string]any{"workspace_root": item.RepoPath}, &result); err != nil {
				return err
			}
			if result.Matched && result.Snapshot.Project != nil {
				id = result.Snapshot.Project.ID
			}
		} else if id == "" && strings.TrimSpace(item.Project) != "" {
			if project, err := findAgentProject(snapshot, "", item.Project); err == nil {
				id = project.ID
			}
		}
		if id == "" {
			item.ProjectHint = firstNonEmpty(item.ProjectHint, item.Project)
			item.Project, item.ProjectID = "", ""
			continue
		}
		project, ok := byID[id]
		if !ok {
			return errors.New("Agent 工作区项目不存在或登记已变化，请重新读取")
		}
		item.ProjectID = id
		item.Project, item.ProjectHint = project.Name, ""
	}
	return nil
}

// workspaceBusiness stores only work-log-specific state, keyed by the Agent's
// stable identity. Project names, hierarchy and paths remain Agent-owned.
const workspaceBusinessSchemaVersion = "work-log-workspace-business.v1"

type workspaceBusiness struct {
	SchemaVersion string                 `json:"schema_version"`
	Projects      map[string]projectWork `json:"projects"`
}

func workspaceBusinessPath(root string) string {
	return filepath.Join(root, "workspace-business.json")
}

// Missing business state is empty; project identity always comes from Agent.
func readWorkspaceBusiness(root string) (*workspaceBusiness, error) {
	body, err := os.ReadFile(workspaceBusinessPath(root))
	if errors.Is(err, os.ErrNotExist) {
		return &workspaceBusiness{SchemaVersion: workspaceBusinessSchemaVersion, Projects: map[string]projectWork{}}, nil
	}
	if err != nil {
		return nil, errors.New("工作区业务状态读取失败")
	}
	var business workspaceBusiness
	if err := json.Unmarshal(body, &business); err != nil || business.SchemaVersion != workspaceBusinessSchemaVersion || business.Projects == nil {
		return nil, errors.New("工作区业务状态格式无效")
	}
	return &business, nil
}

// writeWorkspaceBusiness preserves orphaned historical work states, while
// rejecting identity edits that must go through the Agent registry.
func writeWorkspaceBusiness(root string, business *workspaceBusiness, projected projectsFile) error {
	snapshot, err := loadAgentWorkspace(context.Background())
	if err != nil {
		return err
	}
	canonical := projectViewFromAgent(snapshot, *business)
	identities := make(map[string]project, len(canonical.Projects))
	for _, p := range canonical.Projects {
		identities[p.ID] = p
	}
	if len(projected.Projects) != len(identities) {
		return errors.New("项目身份变更必须通过 Agent 工作区管理")
	}
	seen := make(map[string]bool, len(projected.Projects))
	for _, p := range projected.Projects {
		expected, ok := identities[p.ID]
		if !ok || seen[p.ID] || p.Name != expected.Name || p.ParentID != expected.ParentID || p.Description != expected.Description || p.Status != expected.Status || p.Color != expected.Color || p.Kind != expected.Kind || p.Source != expected.Source || !slices.Equal(p.Aliases, expected.Aliases) || !slices.Equal(p.Paths, expected.Paths) {
			return errors.New("项目身份变更必须通过 Agent 工作区管理")
		}
		seen[p.ID] = true
	}
	for _, p := range projected.Projects {
		business.Projects[p.ID] = p.Work
	}
	payload, err := json.MarshalIndent(business, "", "  ")
	if err != nil {
		return errors.New("工作区业务状态序列化失败")
	}
	return writeFileAtomic(workspaceBusinessPath(root), append(payload, '\n'))
}

func projectViewFromAgent(snapshot agentWorkspaceSnapshot, business workspaceBusiness) projectsFile {
	result := projectsFile{SchemaVersion: projectsSchemaVersion, Projects: make([]project, 0, len(snapshot.Projects))}
	bindings := make(map[string][]projectPath)
	for _, item := range snapshot.Workspaces {
		bindings[item.ProjectID] = append(bindings[item.ProjectID], projectPath{Path: item.Path, Kind: item.Role, Source: "agent"})
	}
	for _, item := range snapshot.Projects {
		result.Projects = append(result.Projects, project{
			ID: item.ID, Name: item.Name, Aliases: item.Aliases, ParentID: item.ParentID,
			Description: item.Description, Status: item.Status, Color: item.Color, Kind: item.Kind,
			Source: "agent", Paths: bindings[item.ID],
			Work: business.Projects[item.ID], CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
		})
	}
	return result
}

// projectEntriesForView changes only copies used for filtering and aggregation;
// saved historical project names remain untouched.
func projectEntriesForView(entries []entry, projects projectsFile) []entry {
	byID := make(map[string]string, len(projects.Projects))
	for _, project := range projects.Projects {
		byID[project.ID] = project.Name
	}
	counts := map[string]int{}
	nodes := map[string]project{}
	for _, p := range projects.Projects {
		counts[p.Name]++
		nodes[p.ID] = p
	}
	for _, p := range projects.Projects {
		if counts[p.Name] < 2 {
			continue
		}
		label := p.Name
		seen := map[string]bool{p.ID: true}
		for parent := p.ParentID; parent != "" && !seen[parent]; {
			node, ok := nodes[parent]
			if !ok {
				break
			}
			seen[parent] = true
			label = node.Name + " / " + label
			parent = node.ParentID
		}
		byID[p.ID] = label + " (" + p.ID + ")"
	}
	view := append([]entry(nil), entries...)
	for i := range view {
		item := &view[i]
		id := strings.TrimSpace(item.ProjectID)
		name := byID[id]
		if name == "" {
			item.ProjectHint = firstNonEmpty(item.ProjectHint, item.Project)
			item.Project, item.ProjectID = "", ""
		} else {
			item.Project = name
		}
	}
	return view
}

func agentProjectView(root string) (*projectsFile, error) {
	projects, err := readProjects(root)
	if err != nil {
		return nil, err
	}
	return &projects, nil
}

func agentProjectScope(projects *projectsFile, id string, includeChildren bool) (map[string]bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		if includeChildren {
			return nil, errors.New("包含子项目时必须指定 Agent 项目 ID")
		}
		return nil, nil
	}
	if projects == nil {
		return nil, errors.New("项目树不可用，请检查 Agent")
	}
	found := false
	for _, project := range projects.Projects {
		if project.ID == id {
			found = true
			break
		}
	}
	if !found {
		return nil, errors.New("Agent 工作区中未找到目标项目")
	}
	scope := map[string]bool{id: true}
	if includeChildren {
		for changed := true; changed; {
			changed = false
			for _, project := range projects.Projects {
				if scope[project.ParentID] && !scope[project.ID] {
					scope[project.ID] = true
					changed = true
				}
			}
		}
	}
	return scope, nil
}

func filterAgentScope(entries []entry, scope map[string]bool) []entry {
	if scope == nil {
		return entries
	}
	result := make([]entry, 0, len(entries))
	for _, item := range entries {
		id := firstNonEmpty(item.ProjectID, item.ProjectID)
		if scope[id] {
			result = append(result, item)
		}
	}
	return result
}
