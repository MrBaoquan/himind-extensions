package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type catalog struct {
	SchemaVersion int                      `json:"schema_version"`
	SourceID      string                   `json:"source_id"`
	Generation    string                   `json:"generation"`
	Plugins       []map[string]interface{} `json:"plugins"`
	Skills        []map[string]interface{} `json:"skills"`
	FeaturePacks  []featurePack            `json:"feature_packs"`
}

type featurePack struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	PluginIDs []string `json:"plugin_ids"`
	SkillIDs  []string `json:"skill_ids"`
}

type signatureMetadata struct {
	FileName           string `json:"file_name"`
	FileSize           int64  `json:"file_size"`
	SHA256             string `json:"sha256"`
	Signature          string `json:"signature"`
	SignatureKeyID     string `json:"signature_key_id"`
	SignatureAlgorithm string `json:"signature_algorithm"`
}

type capability struct {
	ID string `json:"id"`
}

type manifest struct {
	ID                 string                   `json:"id"`
	Name               string                   `json:"name"`
	Author             string                   `json:"author"`
	Categories         []string                 `json:"categories"`
	Description        string                   `json:"description"`
	Version            string                   `json:"version"`
	ReleaseNotes       string                   `json:"release_notes"`
	MinAgentVersion    string                   `json:"min_agent_version"`
	SupportedClients   []string                 `json:"supported_clients"`
	Capabilities       []capability             `json:"capabilities"`
	PluginDependencies []map[string]interface{} `json:"plugin_dependencies"`
	RiskSummary        string                   `json:"risk_summary"`
	Permissions        []string                 `json:"permissions"`
	Views              []map[string]interface{} `json:"views"`
}

func main() {
	kind := flag.String("kind", "", "plugin or skill")
	source := flag.String("source", "", "extension source directory")
	artifact := flag.String("artifact", "", "release artifact")
	signature := flag.String("signature", "", "signature metadata JSON")
	repository := flag.String("repository", "", "GitHub owner/repository")
	tag := flag.String("tag", "", "GitHub release tag")
	catalogPath := flag.String("catalog", ".himind/catalog.json", "catalog file")
	generation := flag.String("generation", "", "catalog generation")
	publishedAt := flag.String("published-at", "", "RFC3339 publication time")
	flag.Parse()
	if err := upsert(*kind, *source, *artifact, *signature, *repository, *tag, *catalogPath, *generation, *publishedAt); err != nil {
		fmt.Fprintln(os.Stderr, "catalog update failed:", err)
		os.Exit(1)
	}
}

func upsert(kind, source, artifactPath, signaturePath, repository, tag, catalogPath, generation, publishedAt string) error {
	if kind != "plugin" && kind != "skill" {
		return errors.New("kind must be plugin or skill")
	}
	if len(strings.Split(repository, "/")) != 2 || tag == "" || generation == "" {
		return errors.New("repository, tag and generation are required")
	}
	if publishedAt == "" {
		publishedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if _, err := time.Parse(time.RFC3339, publishedAt); err != nil {
		return fmt.Errorf("published-at: %w", err)
	}
	manifestName := "plugin.json"
	if kind == "skill" {
		manifestName = "skill.json"
	}
	var value manifest
	if err := readJSON(filepath.Join(source, manifestName), &value); err != nil {
		return err
	}
	if value.ID == "" || value.Name == "" || value.Version == "" || value.Author == "" {
		return errors.New("manifest must declare id, name, version and author")
	}
	var signed signatureMetadata
	if err := readJSON(signaturePath, &signed); err != nil {
		return err
	}
	if err := verifyMetadata(artifactPath, signed); err != nil {
		return err
	}

	var target catalog
	if err := readJSON(catalogPath, &target); err != nil {
		return err
	}
	if target.SchemaVersion != 1 {
		return errors.New("catalog schema_version must be 1")
	}
	entry := catalogEntry(kind, value, signed, repository, tag, publishedAt)
	if kind == "plugin" {
		target.Plugins = replaceVersion(target.Plugins, "plugin_id", value.ID, value.Version, entry)
	} else {
		target.Skills = replaceVersion(target.Skills, "skill_id", value.ID, value.Version, entry)
	}
	target.Generation = generation
	if len(target.FeaturePacks) == 0 {
		target.FeaturePacks = defaultFeaturePacks()
	}
	sortEntries(target.Plugins, "plugin_id")
	sortEntries(target.Skills, "skill_id")
	data, err := json.MarshalIndent(target, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(catalogPath, data, 0o644)
}

func catalogEntry(kind string, value manifest, signed signatureMetadata, repository, tag, publishedAt string) map[string]interface{} {
	capabilityIDs := make([]string, 0, len(value.Capabilities))
	for _, item := range value.Capabilities {
		if item.ID != "" {
			capabilityIDs = append(capabilityIDs, item.ID)
		}
	}
	if value.Categories == nil {
		value.Categories = []string{}
	}
	if value.PluginDependencies == nil {
		value.PluginDependencies = []map[string]interface{}{}
	}
	if value.Permissions == nil {
		value.Permissions = []string{}
	}
	if value.SupportedClients == nil {
		value.SupportedClients = []string{}
	}
	base := map[string]interface{}{
		"name": value.Name, "description": value.Description, "author_name": value.Author,
		"categories": value.Categories, "version": value.Version, "release_notes": value.ReleaseNotes,
		"published_at": publishedAt, "min_agent_version": value.MinAgentVersion, "channel": "stable",
		"artifact_id": "github:" + tag + ":" + signed.FileName, "file_name": signed.FileName,
		"file_size": signed.FileSize, "sha256": signed.SHA256, "signature": signed.Signature,
		"signature_key_id": signed.SignatureKeyID, "signature_algorithm": signed.SignatureAlgorithm,
		"download_url": fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repository, url.PathEscape(tag), url.PathEscape(signed.FileName)),
		"source":       "github", "assignment": "optional", "management": "user_managed", "install_mode": "prompt",
		"organization_reason": "", "managed": false, "allow_disable": true, "allow_uninstall": true,
		"capability_ids": capabilityIDs, "plugin_dependencies": value.PluginDependencies,
	}
	if kind == "plugin" {
		base["plugin_id"] = value.ID
		base["review_status"] = "published"
		base["governance"] = "optional"
		base["permissions"] = value.Permissions
		base["view_count"] = len(value.Views)
	} else {
		base["skill_id"] = value.ID
		base["supported_clients"] = value.SupportedClients
		base["risk_summary"] = value.RiskSummary
	}
	return base
}

func verifyMetadata(artifactPath string, metadata signatureMetadata) error {
	data, err := os.ReadFile(artifactPath)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	if metadata.FileName != filepath.Base(artifactPath) || metadata.FileSize != int64(len(data)) || !strings.EqualFold(metadata.SHA256, hex.EncodeToString(digest[:])) {
		return errors.New("signature metadata does not match artifact")
	}
	if metadata.Signature == "" || metadata.SignatureKeyID == "" || metadata.SignatureAlgorithm != "rsa-pss-sha256" {
		return errors.New("signature metadata is incomplete")
	}
	return nil
}

func replaceVersion(items []map[string]interface{}, idField, id, version string, entry map[string]interface{}) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(items)+1)
	for _, item := range items {
		if fmt.Sprint(item[idField]) == id && fmt.Sprint(item["version"]) == version {
			continue
		}
		result = append(result, item)
	}
	return append(result, entry)
}

func sortEntries(items []map[string]interface{}, idField string) {
	sort.SliceStable(items, func(i, j int) bool {
		left, right := fmt.Sprint(items[i][idField]), fmt.Sprint(items[j][idField])
		if left != right {
			return left < right
		}
		return fmt.Sprint(items[i]["version"]) > fmt.Sprint(items[j]["version"])
	})
}

func defaultFeaturePacks() []featurePack {
	return []featurePack{{
		ID: "com.himind.feature.extension-authoring", Name: "扩展创作",
		PluginIDs: []string{"com.himind.extension-development-tools"},
		SkillIDs:  []string{"com.himind.skill.develop-himind-plugins", "com.himind.skill.develop-himind-skills"},
	}}
}

func readJSON(path string, target interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}
