package skillproject

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type CapabilityDependency struct {
	ID         string `json:"id"`
	Required   bool   `json:"required"`
	MinVersion string `json:"min_version,omitempty"`
	MaxVersion string `json:"max_version,omitempty"`
	Provider   string `json:"provider,omitempty"`
}

type PluginDependency struct {
	PluginID   string `json:"plugin_id"`
	Required   bool   `json:"required"`
	MinVersion string `json:"min_version,omitempty"`
}

type Manifest struct {
	ID                 string                 `json:"id"`
	Name               string                 `json:"name"`
	Author             string                 `json:"author"`
	Categories         []string               `json:"categories"`
	Version            string                 `json:"version"`
	Scope              string                 `json:"scope"`
	Description        string                 `json:"description"`
	ReleaseNotes       string                 `json:"release_notes"`
	MinAgentVersion    string                 `json:"min_agent_version"`
	SupportedClients   []string               `json:"supported_clients"`
	Capabilities       []CapabilityDependency `json:"capabilities"`
	PluginDependencies []PluginDependency     `json:"plugin_dependencies"`
	RiskSummary        string                 `json:"risk_summary"`
	Contents           []string               `json:"contents"`
}

type CreateConfig struct {
	Slug            string
	ID              string
	Name            string
	Version         string
	Description     string
	Author          string
	Categories      []string
	ReleaseNotes    string
	MinAgentVersion string
	Clients         []string
	OutputDir       string
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`)
var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func Create(config CreateConfig) (string, error) {
	if !slugPattern.MatchString(config.Slug) {
		return "", errors.New("slug must use lowercase hyphen-case")
	}
	if strings.TrimSpace(config.ID) == "" {
		config.ID = "com.himind.skill." + config.Slug
	}
	if strings.TrimSpace(config.Version) == "" {
		config.Version = "0.1.0"
	}
	if strings.TrimSpace(config.MinAgentVersion) == "" {
		config.MinAgentVersion = "0.3.0"
	}
	if len(config.Clients) == 0 {
		config.Clients = []string{"codex", "github-copilot", "workbuddy"}
	}
	if strings.TrimSpace(config.Author) == "" {
		return "", errors.New("author is required; use the current Agent authorized user")
	}
	if len(config.Categories) == 0 {
		return "", errors.New("at least one functional category is required")
	}
	if strings.TrimSpace(config.ReleaseNotes) == "" {
		return "", errors.New("release_notes is required")
	}
	root := filepath.Join(config.OutputDir, config.Slug)
	if _, err := os.Stat(root); err == nil {
		return "", fmt.Errorf("output directory already exists: %s", root)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	manifest := Manifest{ID: config.ID, Name: config.Name, Author: strings.TrimSpace(config.Author), Categories: append([]string(nil), config.Categories...), Version: config.Version, Scope: "organization", Description: config.Description, ReleaseNotes: strings.TrimSpace(config.ReleaseNotes), MinAgentVersion: config.MinAgentVersion, SupportedClients: config.Clients, Capabilities: []CapabilityDependency{}, PluginDependencies: []PluginDependency{}, RiskSummary: "read_only", Contents: []string{"skill.json", "SKILL.md", "agents/openai.yaml"}}
	if err := validateManifest(manifest); err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return "", err
	}
	manifestData, _ := json.MarshalIndent(manifest, "", "  ")
	readme := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n# %s\n\n请在此编写可独立使用的操作流程。项目说明仅在目标仓库实际存在时读取。\n", config.Slug, config.Description, config.Name)
	openAI := fmt.Sprintf("interface:\n  display_name: %s\n  short_description: %s\n  default_prompt: %s\n", yamlQuote(config.Name), yamlQuote("使用中文规范创建、校验、测试并提交 HiMind 技能"), yamlQuote(fmt.Sprintf("使用 $%s 完成这项任务。", config.Slug)))
	if err := os.WriteFile(filepath.Join(root, "skill.json"), append(manifestData, '\n'), 0644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte(readme), 0644); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(root, "agents", "openai.yaml"), []byte(openAI), 0644); err != nil {
		return "", err
	}
	return root, nil
}

func yamlQuote(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func Validate(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		_, _, err := readDirectory(path)
		return err
	}
	files, err := readArchive(path)
	if err != nil {
		return err
	}
	manifest, err := parseManifest(files["skill.json"])
	if err != nil {
		return err
	}
	if err := validateDeclaredFiles(manifest, files); err != nil {
		return err
	}
	return validateChecksums(files)
}

func Package(root, output string) error {
	manifest, files, err := readDirectory(root)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		return err
	}
	checksums := make([]string, 0, len(files))
	for name, content := range files {
		digest := sha256.Sum256(content)
		checksums = append(checksums, fmt.Sprintf("%x  %s", digest, name))
	}
	sort.Strings(checksums)
	files["checksums.sha256"] = []byte(strings.Join(checksums, "\n") + "\n")
	archive, err := os.Create(output)
	if err != nil {
		return err
	}
	writer := zip.NewWriter(archive)
	names := append([]string(nil), manifest.Contents...)
	names = append(names, "checksums.sha256")
	sort.Strings(names)
	for _, name := range names {
		entry, createErr := writer.Create(name)
		if createErr == nil {
			_, createErr = entry.Write(files[name])
		}
		if createErr != nil {
			_ = writer.Close()
			_ = archive.Close()
			_ = os.Remove(output)
			return createErr
		}
	}
	if err := writer.Close(); err != nil {
		_ = archive.Close()
		_ = os.Remove(output)
		return err
	}
	return archive.Close()
}

func readDirectory(root string) (Manifest, map[string][]byte, error) {
	manifestData, err := os.ReadFile(filepath.Join(root, "skill.json"))
	if err != nil {
		return Manifest{}, nil, err
	}
	manifest, err := parseManifest(manifestData)
	if err != nil {
		return Manifest{}, nil, err
	}
	files := map[string][]byte{}
	for _, name := range manifest.Contents {
		if err := validatePath(name); err != nil {
			return Manifest{}, nil, err
		}
		content, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if readErr != nil {
			return Manifest{}, nil, fmt.Errorf("skill content is missing: %s", name)
		}
		files[name] = content
	}
	return manifest, files, nil
}

func readArchive(path string) (map[string][]byte, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer archive.Close()
	files := map[string][]byte{}
	for _, file := range archive.File {
		if file.FileInfo().IsDir() {
			continue
		}
		if err := validatePath(file.Name); err != nil {
			return nil, err
		}
		if _, exists := files[file.Name]; exists {
			return nil, fmt.Errorf("duplicate skill path: %s", file.Name)
		}
		reader, err := file.Open()
		if err != nil {
			return nil, err
		}
		content, err := io.ReadAll(io.LimitReader(reader, 64<<20))
		_ = reader.Close()
		if err != nil {
			return nil, err
		}
		files[file.Name] = content
	}
	return files, nil
}

func parseManifest(data []byte) (Manifest, error) {
	var manifest Manifest
	if len(data) == 0 {
		return manifest, errors.New("skill.json is required")
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifest, err
	}
	return manifest, validateManifest(manifest)
}

func validateManifest(manifest Manifest) error {
	if !idPattern.MatchString(manifest.ID) || strings.Contains(manifest.ID, "..") {
		return fmt.Errorf("invalid skill id: %s", manifest.ID)
	}
	if strings.TrimSpace(manifest.Name) == "" {
		return errors.New("skill name is required")
	}
	if strings.TrimSpace(manifest.Author) == "" {
		return errors.New("skill author is required")
	}
	if len(manifest.Categories) == 0 {
		return errors.New("skill categories is required")
	}
	if strings.TrimSpace(manifest.ReleaseNotes) == "" {
		return errors.New("skill release_notes is required")
	}
	if !versionPattern.MatchString(manifest.Version) {
		return fmt.Errorf("invalid skill version: %s", manifest.Version)
	}
	if manifest.Scope != "organization" && manifest.Scope != "builtin" && manifest.Scope != "user" {
		return fmt.Errorf("invalid skill scope: %s", manifest.Scope)
	}
	if len(manifest.SupportedClients) == 0 {
		return errors.New("supported_clients is required")
	}
	declared := map[string]bool{}
	for _, name := range manifest.Contents {
		if err := validatePath(name); err != nil {
			return err
		}
		declared[name] = true
	}
	if !declared["skill.json"] || !declared["SKILL.md"] {
		return errors.New("contents must include skill.json and SKILL.md")
	}
	return nil
}

func validateDeclaredFiles(manifest Manifest, files map[string][]byte) error {
	declared := map[string]bool{}
	for _, name := range manifest.Contents {
		declared[name] = true
		if _, ok := files[name]; !ok {
			return fmt.Errorf("skill content is missing: %s", name)
		}
	}
	for name := range files {
		if name != "checksums.sha256" && !declared[name] {
			return fmt.Errorf("skill package contains undeclared content: %s", name)
		}
	}
	return nil
}

func validateChecksums(files map[string][]byte) error {
	data, ok := files["checksums.sha256"]
	if !ok {
		return errors.New("skill package must contain checksums.sha256")
	}
	expected := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		parts := strings.SplitN(strings.TrimSuffix(line, "\r"), "  ", 2)
		if len(parts) != 2 || len(parts[0]) != 64 {
			return errors.New("invalid checksums.sha256")
		}
		expected[parts[1]] = strings.ToLower(parts[0])
	}
	for name, content := range files {
		if name == "checksums.sha256" {
			continue
		}
		digest := sha256.Sum256(content)
		if expected[name] != hex.EncodeToString(digest[:]) {
			return fmt.Errorf("skill checksum mismatch: %s", name)
		}
		delete(expected, name)
	}
	if len(expected) != 0 {
		return errors.New("checksums.sha256 references missing content")
	}
	return nil
}

func validatePath(name string) error {
	lower := strings.ToLower(name)
	if name == "" || filepath.IsAbs(name) || strings.Contains(name, "\\") || strings.Contains(name, ":") || strings.Contains(name, "../") || strings.HasPrefix(name, "../") {
		return fmt.Errorf("invalid skill path: %s", name)
	}
	if strings.HasSuffix(lower, ".ps1") || strings.HasSuffix(lower, ".sh") || strings.HasSuffix(lower, ".bat") || strings.HasSuffix(lower, ".cmd") || strings.HasSuffix(lower, ".exe") || strings.HasSuffix(lower, ".dll") || strings.Contains(lower, "/scripts/") || strings.Contains(lower, "/bin/") {
		return fmt.Errorf("skill package must not contain executable or script: %s", name)
	}
	return nil
}
