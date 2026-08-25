package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type catalog struct {
	Repository    string             `json:"repository"`
	DefaultBranch string             `json:"default_branch"`
	Extensions    []catalogExtension `json:"extensions"`
}

type catalogExtension struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Path string `json:"path"`
}

type manifest struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
}

type projectRecord struct {
	ID                  string `json:"id"`
	Kind                string `json:"kind"`
	ExtensionID         string `json:"extension_id"`
	Name                string `json:"name"`
	Description         string `json:"description"`
	Version             string `json:"version"`
	WorkspacePath       string `json:"workspace_path"`
	Source              string `json:"source"`
	SourceRepository    string `json:"source_repository"`
	SourceDefaultBranch string `json:"source_default_branch"`
	SourceSubdirectory  string `json:"source_subdirectory"`
	SourceCommit        string `json:"source_commit"`
	UpdatedAt           string `json:"updated_at"`
}

func main() {
	repositoryRoot := flag.String("repository-root", ".", "local repository root")
	registry := flag.String("registry", defaultRegistry(), "Agent extension-projects.json path")
	commit := flag.String("commit", "", "source commit SHA")
	flag.Parse()
	if err := syncRegistry(*repositoryRoot, *registry, *commit); err != nil {
		fmt.Fprintln(os.Stderr, "workspace sync failed:", err)
		os.Exit(1)
	}
}

func syncRegistry(repositoryRoot, registry, commit string) error {
	root, err := filepath.Abs(repositoryRoot)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(root, "extensions.json"))
	if err != nil {
		return err
	}
	var value catalog
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	if strings.TrimSpace(value.Repository) == "" || strings.TrimSpace(value.DefaultBranch) == "" {
		return fmt.Errorf("extensions.json must declare repository and default_branch")
	}
	now := fmt.Sprintf("%d", time.Now().UnixMilli())
	records := make([]projectRecord, 0, len(value.Extensions))
	for _, extension := range value.Extensions {
		manifestName := "plugin.json"
		if extension.Type == "skill" {
			manifestName = "skill.json"
		}
		workspace := filepath.Join(root, filepath.FromSlash(extension.Path))
		manifestData, err := os.ReadFile(filepath.Join(workspace, manifestName))
		if err != nil {
			return err
		}
		var item manifest
		if err := json.Unmarshal(manifestData, &item); err != nil {
			return err
		}
		if item.ID != extension.ID {
			return fmt.Errorf("catalog id %s does not match %s", extension.ID, item.ID)
		}
		records = append(records, projectRecord{
			ID: extension.Type + ":" + item.ID, Kind: extension.Type, ExtensionID: item.ID,
			Name: item.Name, Description: item.Description, Version: item.Version,
			WorkspacePath: workspace, Source: "git_workspace", SourceRepository: value.Repository,
			SourceDefaultBranch: value.DefaultBranch, SourceSubdirectory: extension.Path,
			SourceCommit: strings.TrimSpace(commit), UpdatedAt: now,
		})
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	output, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(registry), 0755); err != nil {
		return err
	}
	temporary := registry + ".tmp"
	if err := os.WriteFile(temporary, append(output, '\n'), 0644); err != nil {
		return err
	}
	if err := replaceFile(temporary, registry); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	fmt.Printf("synchronized %d Agent extension projects to %s\n", len(records), registry)
	return nil
}

func replaceFile(temporary, target string) error {
	backup := target + ".bak"
	_ = os.Remove(backup)
	if _, err := os.Stat(target); err == nil {
		if err := os.Rename(target, backup); err != nil {
			return err
		}
	}
	if err := os.Rename(temporary, target); err != nil {
		_ = os.Rename(backup, target)
		return err
	}
	_ = os.Remove(backup)
	return nil
}

func defaultRegistry() string {
	if value := strings.TrimSpace(os.Getenv("HIMIND_EXTENSION_PROJECTS_FILE")); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("HIMIND_AGENT_HOME")); value != "" {
		return filepath.Join(value, "extension-projects.json")
	}
	base := ""
	if value := os.Getenv("LOCALAPPDATA"); value != "" {
		base = filepath.Join(value, "HiMindAgent")
	}
	if base != "" {
		profile := strings.TrimSpace(os.Getenv("HIMIND_AGENT_PROFILE"))
		if profile != "" && profile != "production" && profile != "default" && safeProfileName(profile) {
			return filepath.Join(base, "profiles", profile, "extension-projects.json")
		}
		return filepath.Join(base, "extension-projects.json")
	}
	return "extension-projects.json"
}

func safeProfileName(value string) bool {
	if len(value) == 0 || len(value) > 48 {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z') && !(char >= 'A' && char <= 'Z') && !(char >= '0' && char <= '9') && char != '.' && char != '_' && char != '-' {
			return false
		}
	}
	return true
}
