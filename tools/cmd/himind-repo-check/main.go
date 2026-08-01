package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/MrBaoquan/himind-extensions/tooling/skillproject"
	validator "github.com/MrBaoquan/himind-extensions/tools/cmd/himind-plugin-validate"
)

type catalog struct {
	SchemaVersion int                `json:"schema_version"`
	Repository    string             `json:"repository"`
	DefaultBranch string             `json:"default_branch"`
	Extensions    []catalogExtension `json:"extensions"`
}

type catalogExtension struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Path string `json:"path"`
}

type manifestIdentity struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Author       string   `json:"author"`
	Categories   []string `json:"categories"`
	Version      string   `json:"version"`
	ReleaseNotes string   `json:"release_notes"`
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
	if len(value.Extensions) == 0 {
		return errors.New("extensions.json contains no extensions")
	}
	seenIDs := map[string]string{}
	seenPaths := map[string]string{}
	for _, extension := range value.Extensions {
		if extension.Type != "plugin" && extension.Type != "skill" {
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
		if strings.TrimSpace(identity.Name) == "" || strings.TrimSpace(identity.Author) == "" || strings.TrimSpace(identity.Version) == "" || len(identity.Categories) == 0 || strings.TrimSpace(identity.ReleaseNotes) == "" {
			return fmt.Errorf("%s must declare name, author, categories, version and release_notes", extension.Path)
		}
		if extension.Type == "plugin" {
			if err := validator.ValidateDirectory(path); err != nil {
				return err
			}
		} else if err := skillproject.Validate(path); err != nil {
			return err
		}
	}
	if err := ensureCatalogComplete(root, "plugins", "plugin.json", seenPaths); err != nil {
		return err
	}
	if err := ensureCatalogComplete(root, "skills", "skill.json", seenPaths); err != nil {
		return err
	}
	ids := make([]string, 0, len(seenIDs))
	for id := range seenIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		fmt.Printf("valid %s: %s\n", id, seenIDs[id])
	}
	fmt.Printf("validated %d extensions\n", len(ids))
	return nil
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
