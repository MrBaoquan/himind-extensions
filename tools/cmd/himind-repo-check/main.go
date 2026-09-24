package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/MrBaoquan/himind-extensions/tooling/distribution"
	"github.com/MrBaoquan/himind-extensions/tooling/skillproject"
	"github.com/MrBaoquan/himind-extensions/tooling/workflowproject"
	validator "github.com/MrBaoquan/himind-extensions/tools/cmd/himind-plugin-validate"
)

type catalog struct {
	SchemaVersion              int                `json:"schema_version"`
	Repository                 string             `json:"repository"`
	DistributionID             string             `json:"distribution_id"`
	Channel                    string             `json:"channel"`
	CatalogID                  string             `json:"catalog_id"`
	DefaultBranch              string             `json:"default_branch"`
	DefaultDistributionTargets []string           `json:"default_distribution_targets"`
	Extensions                 []catalogExtension `json:"extensions"`
}

type catalogExtension struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Path string `json:"path"`
}

type manifestIdentity struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	Author              string    `json:"author"`
	Categories          []string  `json:"categories"`
	Version             string    `json:"version"`
	ReleaseNotes        string    `json:"release_notes"`
	DistributionTargets *[]string `json:"distribution_targets"`
}

func main() {
	if err := run("."); err != nil {
		fmt.Fprintln(os.Stderr, "extension repository is invalid:", err)
		os.Exit(1)
	}
}

func run(root string) error {
	data, err := os.ReadFile(filepath.Join(root, "extensions.json"))
	if err != nil {
		return err
	}
	var value catalog
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("extensions.json: %w", err)
	}
	if value.SchemaVersion != 1 || strings.TrimSpace(value.Repository) == "" || strings.TrimSpace(value.DefaultBranch) == "" {
		return errors.New("extensions.json must declare schema_version 1, repository and default_branch")
	}
	if !validDistributionID(value.DistributionID) || !validDistributionPart(value.Channel) || !validDistributionPart(value.CatalogID) {
		return errors.New("extensions.json must declare valid distribution_id, channel and catalog_id")
	}
	repoDefaults, err := distribution.Parse(value.DefaultDistributionTargets)
	if err != nil {
		return fmt.Errorf("extensions.json: %w", err)
	}
	if len(value.Extensions) == 0 {
		return errors.New("extensions.json contains no extensions")
	}
	seenIDs := map[string]string{}
	seenPaths := map[string]string{}
	seenTargets := map[string][]distribution.Target{}
	for _, extension := range value.Extensions {
		if extension.Type != "plugin" && extension.Type != "skill" && extension.Type != "workflow" {
			return fmt.Errorf("unsupported extension type %q", extension.Type)
		}
		path, err := safePath(root, extension.Path)
		if err != nil {
			return err
		}
		identity, err := readManifest(path, extension.Type)
		if err != nil {
			return fmt.Errorf("%s: %w", extension.Path, err)
		}
		if identity.ID != extension.ID {
			return fmt.Errorf("%s declares id %q, catalog expects %q", extension.Path, identity.ID, extension.ID)
		}
		if previous, ok := seenIDs[identity.ID]; ok {
			return fmt.Errorf("duplicate extension id %q in %s and %s", identity.ID, previous, extension.Path)
		}
		if previous, ok := seenPaths[extension.Path]; ok {
			return fmt.Errorf("duplicate extension path %q for %s and %s", extension.Path, previous, identity.ID)
		}
		seenIDs[identity.ID] = extension.Path
		seenPaths[extension.Path] = identity.ID
		targets, err := declaredTargets(identity, repoDefaults)
		if err != nil {
			return fmt.Errorf("%s: %w", extension.Path, err)
		}
		seenTargets[identity.ID] = targets
		if strings.TrimSpace(identity.Name) == "" || strings.TrimSpace(identity.Version) == "" {
			return fmt.Errorf("%s must declare name, author, categories, version and release_notes", extension.Path)
		}
		if extension.Type == "plugin" {
			if strings.TrimSpace(identity.Author) == "" || len(identity.Categories) == 0 || strings.TrimSpace(identity.ReleaseNotes) == "" {
				return fmt.Errorf("%s must declare name, author, categories, version and release_notes", extension.Path)
			}
			if err := validator.ValidateDirectory(path); err != nil {
				return err
			}
		} else if extension.Type == "skill" {
			if strings.TrimSpace(identity.Author) == "" || len(identity.Categories) == 0 || strings.TrimSpace(identity.ReleaseNotes) == "" {
				return fmt.Errorf("%s must declare name, author, categories, version and release_notes", extension.Path)
			}
			if err := skillproject.Validate(path); err != nil {
				return err
			}
		} else if err := workflowproject.Validate(path); err != nil {
			return err
		}
	}
	if err := ensureCatalogComplete(root, "plugins", "plugin.json", seenPaths); err != nil {
		return err
	}
	if err := ensureCatalogComplete(root, "skills", "skill.json", seenPaths); err != nil {
		return err
	}
	if err := ensureCatalogComplete(root, "workflows", "workflow.json", seenPaths); err != nil {
		return err
	}
	ids := make([]string, 0, len(seenIDs))
	for id := range seenIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		fmt.Printf("valid %s: %s [%s]\n", id, seenIDs[id], distribution.Describe(seenTargets[id]))
	}
	fmt.Printf("validated %d extensions\n", len(ids))
	return nil
}

// declaredTargets 读取清单里的分发落点。
//
// 清单必须显式声明，不从仓库默认值静默继承：落点决定制品发到哪里，作者
// 每次都要自己确认，避免新增扩展时「没写就等于默认」。仓库默认值只用于
// 脚手架和对照检查，未声明时在这里直接报错。
func declaredTargets(identity manifestIdentity, repoDefaults []distribution.Target) ([]distribution.Target, error) {
	if identity.DistributionTargets == nil {
		return nil, fmt.Errorf("must declare distribution_targets, repository default is [%s]", distribution.Describe(repoDefaults))
	}
	return distribution.Parse(*identity.DistributionTargets)
}

func validDistributionID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 160 {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z') && !(char >= 'A' && char <= 'Z') && !(char >= '0' && char <= '9') && char != '.' && char != '_' && char != '-' && char != '/' {
			return false
		}
	}
	return true
}

func validDistributionPart(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z') && !(char >= 'A' && char <= 'Z') && !(char >= '0' && char <= '9') && char != '.' && char != '_' && char != '-' {
			return false
		}
	}
	return true
}

func safePath(root, relative string) (string, error) {
	if relative == "" || filepath.IsAbs(relative) || strings.Contains(relative, "\\") {
		return "", fmt.Errorf("invalid catalog path %q", relative)
	}
	clean := filepath.Clean(filepath.FromSlash(relative))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("catalog path escapes repository: %q", relative)
	}
	return filepath.Join(root, clean), nil
}

func readManifest(root, kind string) (manifestIdentity, error) {
	name := "plugin.json"
	if kind == "skill" {
		name = "skill.json"
	} else if kind == "workflow" {
		name = "workflow.json"
	}
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		return manifestIdentity{}, err
	}
	var value manifestIdentity
	if err := json.Unmarshal(data, &value); err != nil {
		return value, err
	}
	return value, nil
}

func ensureCatalogComplete(root, directory, manifest string, paths map[string]string) error {
	entries, err := os.ReadDir(filepath.Join(root, directory))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		relative := filepath.ToSlash(filepath.Join(directory, entry.Name()))
		if _, err := os.Stat(filepath.Join(root, relative, manifest)); err == nil {
			if _, ok := paths[relative]; !ok {
				return fmt.Errorf("%s is missing from extensions.json", relative)
			}
		}
	}
	return nil
}
