package workflowproject

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
	"time"

	"github.com/MrBaoquan/himind-extensions/tooling/metaguide"
)

const (
	ManifestFile   = "workflow.json"
	ChecksumsFile  = "checksums.sha256"
	maxFiles       = 100_000
	maxPackageByte = int64(512 * 1024 * 1024)
)

type Config struct {
	Slug            string
	ID              string
	Name            string
	Description     string
	Author          string
	Version         string
	MinAgentVersion string
	ReleaseNotes    string
	Categories      []string
	Template        string
	OutputDir       string
}

type Result struct {
	Root       string `json:"root"`
	WorkflowID string `json:"workflow_id"`
	Version    string `json:"version"`
	Template   string `json:"template"`
}

type Dependencies struct {
	Skills     []DependencyRef `json:"skills"`
	Plugins    []DependencyRef `json:"plugins"`
	Connectors []string        `json:"connectors"`
	Runtimes   []string        `json:"runtimes"`
}

// DependencyRef 是工作流依赖里的一条声明。
//
// 字符串写法表示「本分发内的必需依赖」，发布时必须已在本分发里有规范发布；
// 对象写法可以补 `required` 与 `min_version`，用于跨分发依赖（由别的分发发布，
// 这里解析不到确定制品）或需要声明版本下限的场景。只声明 ID 的依赖写回字符串，
// 简单声明不被扩写成对象。
type DependencyRef struct {
	ID         string
	Required   bool
	MinVersion string
}

func (d *DependencyRef) UnmarshalJSON(data []byte) error {
	var id string
	if err := json.Unmarshal(data, &id); err == nil {
		d.ID = strings.TrimSpace(id)
		d.Required = true
		d.MinVersion = ""
		return nil
	}
	var object struct {
		ID         string `json:"id"`
		PluginID   string `json:"plugin_id"`
		SkillID    string `json:"skill_id"`
		Required   *bool  `json:"required"`
		MinVersion string `json:"min_version"`
	}
	if err := json.Unmarshal(data, &object); err != nil {
		return fmt.Errorf("依赖条目必须是字符串或对象: %s", strings.TrimSpace(string(data)))
	}
	d.ID = firstNonEmpty(object.PluginID, object.SkillID, object.ID)
	d.Required = object.Required == nil || *object.Required
	d.MinVersion = strings.TrimSpace(object.MinVersion)
	return nil
}

func (d DependencyRef) MarshalJSON() ([]byte, error) {
	if d.Required && d.MinVersion == "" {
		return json.Marshal(d.ID)
	}
	return json.Marshal(struct {
		ID         string `json:"id"`
		Required   bool   `json:"required"`
		MinVersion string `json:"min_version,omitempty"`
	}{ID: d.ID, Required: d.Required, MinVersion: d.MinVersion})
}

type Candidate struct {
	Required   bool   `json:"required"`
	ArtifactID string `json:"artifact_id"`
	Source     string `json:"source"`
	AllowDirty bool   `json:"allow_dirty"`
}

type Endpoint struct {
	ID       string   `json:"id"`
	AtStep   string   `json:"at_step"`
	Label    string   `json:"label,omitempty"`
	Requires []string `json:"requires,omitempty"`
	Produces []string `json:"produces,omitempty"`
}

type Runtime struct {
	Provider      string `json:"provider"`
	Prompt        string `json:"prompt"`
	WorkspacePath string `json:"workspace_path,omitempty"`
	ResultSchema  string `json:"result_schema,omitempty"`
	AllowNetwork  bool   `json:"allow_network,omitempty"`
	// ToolPolicy is "default" (provider default) or "none" (mount no model-facing tools).
	ToolPolicy string `json:"tool_policy,omitempty"`
	// InputArtifacts lists upstream artifacts delivered to the step as local file paths.
	InputArtifacts []string `json:"input_artifacts,omitempty"`
	TimeoutSeconds uint64   `json:"timeout_seconds,omitempty"`
}

type Loop struct {
	MaxIterations uint32         `json:"max_iterations"`
	PauseFeedback bool           `json:"pause_for_feedback,omitempty"`
	ContinueWhen  map[string]any `json:"continue_when,omitempty"`
	ExitWhen      map[string]any `json:"exit_when,omitempty"`
	Steps         []Step         `json:"steps"`
}

type Step struct {
	ID               string         `json:"id"`
	Title            string         `json:"title"`
	Kind             string         `json:"kind,omitempty"`
	CapabilityID     string         `json:"capability_id,omitempty"`
	Runtime          *Runtime       `json:"runtime,omitempty"`
	Loop             *Loop          `json:"loop,omitempty"`
	When             map[string]any `json:"when,omitempty"`
	FailWhen         map[string]any `json:"fail_when,omitempty"`
	CandidateAction  string         `json:"candidate_action,omitempty"`
	Input            map[string]any `json:"input,omitempty"`
	ExecutionMode    string         `json:"execution_mode"`
	RiskLevel        string         `json:"risk_level,omitempty"`
	ApprovalRequired bool           `json:"approval_required,omitempty"`
	// OnFailure is "fail" (default) or "continue". A step that declares
	// "continue" degrades to a tolerated failure so downstream steps still run.
	OnFailure string   `json:"on_failure,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`
}

type Artifact struct {
	ID           string `json:"id"`
	ArtifactType string `json:"artifact_type"`
	Name         string `json:"name"`
	Schema       string `json:"schema,omitempty"`
	Required     bool   `json:"required,omitempty"`
	Validation   string `json:"validation,omitempty"`
	MaxBytes     uint64 `json:"max_bytes,omitempty"`
}

type UI struct {
	Mode     string   `json:"mode"`
	Entry    string   `json:"entry,omitempty"`
	Surfaces []string `json:"surfaces,omitempty"`
}

type Manifest struct {
	SchemaVersion     string         `json:"schema_version"`
	ID                string         `json:"id"`
	Version           string         `json:"version"`
	Name              string         `json:"name"`
	Description       string         `json:"description,omitempty"`
	MinAgentVersion   string         `json:"min_agent_version"`
	LocalRequirements map[string]any `json:"local_requirements,omitempty"`
	OptionalProviders []string       `json:"optional_providers,omitempty"`
	Capabilities      []string       `json:"capabilities,omitempty"`
	Dependencies      Dependencies   `json:"dependencies"`
	Candidate         *Candidate     `json:"candidate,omitempty"`
	ExecutionPolicy   string         `json:"execution_policy,omitempty"`
	Entrypoints       []Endpoint     `json:"entrypoints,omitempty"`
	// DefaultEntrypoint/DefaultExitpoint 允许调用方不选分段就启动；显式传值优先。
	DefaultEntrypoint string     `json:"default_entrypoint,omitempty"`
	Exits             []Endpoint `json:"exits,omitempty"`
	DefaultExitpoint  string     `json:"default_exitpoint,omitempty"`
	Steps             []Step     `json:"steps"`
	Artifacts         []Artifact `json:"artifacts"`
	UI                UI         `json:"ui"`
	SupportedRuntimes []string   `json:"supported_runtimes,omitempty"`
	CreatedAt         string     `json:"created_at,omitempty"`
}

type View struct {
	SchemaVersion string        `json:"schema_version"`
	Title         string        `json:"title"`
	Sections      []ViewSection `json:"sections"`
	Actions       []string      `json:"actions"`
}

type ViewSection struct {
	ID     string           `json:"id"`
	Title  string           `json:"title"`
	Fields []map[string]any `json:"fields,omitempty"`
}

var (
	idPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	slugPattern    = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
)

func Create(config Config) (Result, error) {
	config.Slug = strings.TrimSpace(config.Slug)
	config.ID = strings.TrimSpace(config.ID)
	config.Name = strings.TrimSpace(config.Name)
	config.Description = strings.TrimSpace(config.Description)
	config.Author = strings.TrimSpace(config.Author)
	config.Version = strings.TrimSpace(config.Version)
	config.MinAgentVersion = strings.TrimSpace(config.MinAgentVersion)
	config.ReleaseNotes = strings.TrimSpace(config.ReleaseNotes)
	config.Template = strings.TrimSpace(config.Template)
	if !slugPattern.MatchString(config.Slug) {
		return Result{}, errors.New("slug must use lowercase hyphen-case")
	}
	if config.ID == "" {
		config.ID = "com.himind.workflow." + config.Slug
	}
	if config.Version == "" {
		config.Version = "0.1.0"
	}
	if config.MinAgentVersion == "" {
		config.MinAgentVersion = "0.3.47"
	}
	if config.Name == "" {
		return Result{}, errors.New("name is required")
	}
	if config.Description == "" {
		return Result{}, errors.New("description is required")
	}
	if config.Author == "" {
		return Result{}, errors.New("author is required; use the current Agent authorized user")
	}
	if config.ReleaseNotes == "" {
		return Result{}, errors.New("release_notes is required")
	}
	if config.Template == "" {
		config.Template = "strict"
	}
	for _, item := range []struct {
		field metaguide.Field
		value string
	}{
		{metaguide.StableID, config.ID},
		{metaguide.Slug, config.Slug},
		{metaguide.DisplayName, config.Name},
		{metaguide.Description, config.Description},
		{metaguide.ReleaseNotes, config.ReleaseNotes},
	} {
		if err := metaguide.Check(item.field, item.value); err != nil {
			return Result{}, err
		}
	}
	switch config.Template {
	case "strict", "segmented", "flexible", "development-loop", "capability-pipeline":
	default:
		return Result{}, fmt.Errorf("unsupported template %q; use strict, segmented, flexible, development-loop or capability-pipeline", config.Template)
	}
	root := filepath.Join(config.OutputDir, config.Slug)
	if config.OutputDir == "" {
		root = config.Slug
	}
	if _, err := os.Stat(root); err == nil {
		return Result{}, fmt.Errorf("output directory already exists: %s", root)
	} else if !os.IsNotExist(err) {
		return Result{}, err
	}
	manifest := buildManifest(config)
	if err := validateManifest(manifest, nil); err != nil {
		return Result{}, err
	}
	view := buildView(config.Name, config.Template)
	manifestData, _ := json.MarshalIndent(manifest, "", "  ")
	viewData, _ := json.MarshalIndent(view, "", "  ")
	files := map[string][]byte{
		ManifestFile:               append(manifestData, '\n'),
		"README.md":                []byte(templateReadme(config)),
		"ui/workflow-view.json":    append(viewData, '\n'),
		"examples/input.json":      []byte("{}\n"),
		"tests/contract/README.md": []byte("# Workflow Contract Tests\n\n在此放置契约测试、输入样例和负例说明。\n"),
	}
	if hasSchemaTemplate(config.Template) {
		files["schemas/result.schema.json"] = []byte("{\n  \"type\": \"object\"\n}\n")
	}
	for relative, content := range files {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return Result{}, err
		}
		if err := os.WriteFile(path, content, 0644); err != nil {
			return Result{}, err
		}
	}
	for _, directory := range []string{"artifacts", "connectors"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0755); err != nil {
			return Result{}, err
		}
	}
	return Result{Root: root, WorkflowID: manifest.ID, Version: manifest.Version, Template: config.Template}, nil
}

func Build(root string) (Manifest, error) {
	return readManifestFromDirectory(root)
}

func Validate(root string) error {
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if info.IsDir() {
		_, err := readManifestFromDirectory(root)
		return err
	}
	files, err := readArchive(root)
	if err != nil {
		return err
	}
	if err := validateChecksums(files); err != nil {
		return err
	}
	data, ok := files[ManifestFile]
	if !ok {
		return errors.New("workflow.json is required")
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("workflow.json: %w", err)
	}
	exists := func(relative string) bool {
		_, ok := files[filepath.ToSlash(relative)]
		return ok
	}
	return validateManifest(manifest, exists)
}

func Package(root, output string) error {
	files, manifest, err := readDirectory(root)
	if err != nil {
		return err
	}
	filesWithoutChecksums := make(map[string][]byte, len(files))
	for name, content := range files {
		if name != ChecksumsFile {
			filesWithoutChecksums[name] = content
		}
	}
	checksums := checksumLines(filesWithoutChecksums)
	filesWithoutChecksums[ChecksumsFile] = []byte(strings.Join(checksums, "\n") + "\n")
	if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		return err
	}
	archive, err := os.Create(output)
	if err != nil {
		return err
	}
	writer := zip.NewWriter(archive)
	names := make([]string, 0, len(filesWithoutChecksums))
	for name := range filesWithoutChecksums {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	})
	for _, name := range names {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.Modified = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
		entry, createErr := writer.CreateHeader(header)
		if createErr == nil {
			_, createErr = entry.Write(filesWithoutChecksums[name])
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
	if err := archive.Close(); err != nil {
		_ = os.Remove(output)
		return err
	}
	if manifest.ID == "" {
		_ = os.Remove(output)
		return errors.New("workflow manifest identity is empty")
	}
	return nil
}

func readManifestFromDirectory(root string) (Manifest, error) {
	files, manifest, err := readDirectory(root)
	if err != nil {
		return Manifest{}, err
	}
	exists := func(relative string) bool {
		_, ok := files[filepath.ToSlash(relative)]
		return ok
	}
	if err := validateManifest(manifest, exists); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func readDirectory(root string) (map[string][]byte, Manifest, error) {
	files := map[string][]byte{}
	var total int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			if shouldPrune(relative) {
				return filepath.SkipDir
			}
			return nil
		}
		if shouldPrune(relative) || relative == ChecksumsFile || strings.HasSuffix(strings.ToLower(relative), ".hmwf") || strings.HasSuffix(strings.ToLower(relative), ".zip") {
			return nil
		}
		if len(files) >= maxFiles {
			return fmt.Errorf("workflow package contains more than %d files", maxFiles)
		}
		if err := validatePackagePath(relative); err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		total += int64(len(content))
		if total > maxPackageByte {
			return errors.New("workflow package content exceeds 512 MiB")
		}
		files[relative] = content
		return nil
	})
	if err != nil {
		return nil, Manifest{}, err
	}
	data, ok := files[ManifestFile]
	if !ok {
		return nil, Manifest{}, errors.New("workflow.json is required")
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, Manifest{}, fmt.Errorf("workflow.json: %w", err)
	}
	return files, manifest, nil
}

func readArchive(path string) (map[string][]byte, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer archive.Close()
	files := map[string][]byte{}
	var total int64
	for _, file := range archive.File {
		if file.FileInfo().IsDir() {
			continue
		}
		name := filepath.ToSlash(file.Name)
		if err := validatePackagePath(name); err != nil {
			return nil, err
		}
		if _, exists := files[name]; exists {
			return nil, fmt.Errorf("duplicate workflow path: %s", name)
		}
		if len(files) >= maxFiles {
			return nil, fmt.Errorf("workflow package contains more than %d files", maxFiles)
		}
		reader, err := file.Open()
		if err != nil {
			return nil, err
		}
		content, err := io.ReadAll(io.LimitReader(reader, maxPackageByte+1))
		_ = reader.Close()
		if err != nil {
			return nil, err
		}
		total += int64(len(content))
		if total > maxPackageByte {
			return nil, errors.New("workflow package content exceeds 512 MiB")
		}
		files[name] = content
	}
	return files, nil
}

func validateManifest(manifest Manifest, fileExists func(string) bool) error {
	if manifest.SchemaVersion != "workflow_package.v1" {
		return fmt.Errorf("unsupported workflow schema_version %q", manifest.SchemaVersion)
	}
	if !idPattern.MatchString(manifest.ID) || strings.Contains(manifest.ID, "..") {
		return fmt.Errorf("invalid workflow id: %s", manifest.ID)
	}
	if !versionPattern.MatchString(manifest.Version) {
		return fmt.Errorf("invalid workflow version: %s", manifest.Version)
	}
	if !versionPattern.MatchString(manifest.MinAgentVersion) {
		return fmt.Errorf("invalid minimum Agent version: %s", manifest.MinAgentVersion)
	}
	if strings.TrimSpace(manifest.Name) == "" {
		return errors.New("workflow name is required")
	}
	if err := metaguide.Check(metaguide.StableID, manifest.ID); err != nil {
		return err
	}
	if err := metaguide.Check(metaguide.DisplayName, manifest.Name); err != nil {
		return err
	}
	if err := metaguide.Check(metaguide.Description, manifest.Description); err != nil {
		return err
	}
	policy := strings.TrimSpace(manifest.ExecutionPolicy)
	if policy == "" {
		policy = "strict"
	}
	if policy != "strict" && policy != "segmented" && policy != "flexible" {
		return fmt.Errorf("workflow execution_policy is invalid: %s", policy)
	}
	if err := validateUniqueStrings("capability", manifest.Capabilities); err != nil {
		return err
	}
	if err := validateUniqueStrings("supported runtime", manifest.SupportedRuntimes); err != nil {
		return err
	}
	for _, runtime := range manifest.Dependencies.Runtimes {
		if !contains(manifest.SupportedRuntimes, runtime) {
			return fmt.Errorf("workflow runtime dependency is not declared in supported_runtimes: %s", runtime)
		}
	}
	if err := validateDependencyRefs("skill", manifest.Dependencies.Skills); err != nil {
		return err
	}
	if err := validateDependencyRefs("plugin", manifest.Dependencies.Plugins); err != nil {
		return err
	}
	stepIDs := map[string]bool{}
	if err := collectStepIDs(manifest.Steps, stepIDs); err != nil {
		return err
	}
	if err := validateSteps(manifest.Steps, stepIDs, manifest); err != nil {
		return err
	}
	if err := validateCandidate(manifest); err != nil {
		return err
	}
	if policy == "strict" && (len(manifest.Entrypoints) > 0 || len(manifest.Exits) > 0) {
		return errors.New("strict workflow cannot declare custom entrypoints or exits")
	}
	if policy != "strict" && len(manifest.Entrypoints) == 0 {
		return fmt.Errorf("%s workflow requires at least one entrypoint", policy)
	}
	if policy != "strict" && len(manifest.Exits) == 0 {
		return fmt.Errorf("%s workflow requires at least one exit", policy)
	}
	if err := validateEndpoints("entrypoint", manifest.Entrypoints, stepIDs); err != nil {
		return err
	}
	if err := validateEndpoints("exit", manifest.Exits, stepIDs); err != nil {
		return err
	}
	if err := validateDefaultEndpoint("default_entrypoint", manifest.DefaultEntrypoint, manifest.Entrypoints); err != nil {
		return err
	}
	if err := validateDefaultEndpoint("default_exitpoint", manifest.DefaultExitpoint, manifest.Exits); err != nil {
		return err
	}
	artifactIDs := map[string]bool{}
	for _, artifact := range manifest.Artifacts {
		if !idPattern.MatchString(artifact.ID) {
			return fmt.Errorf("invalid workflow artifact id: %s", artifact.ID)
		}
		if artifactIDs[artifact.ID] {
			return fmt.Errorf("duplicate workflow artifact id: %s", artifact.ID)
		}
		artifactIDs[artifact.ID] = true
		if strings.TrimSpace(artifact.ArtifactType) == "" || strings.TrimSpace(artifact.Name) == "" {
			return fmt.Errorf("workflow artifact %s identity is required", artifact.ID)
		}
		if artifact.Schema != "" {
			if err := validatePackagePath(artifact.Schema); err != nil {
				return fmt.Errorf("workflow artifact %s schema: %w", artifact.ID, err)
			}
			if fileExists != nil && !fileExists(artifact.Schema) {
				return fmt.Errorf("workflow artifact schema is missing: %s", artifact.Schema)
			}
		}
		if artifact.Validation != "" && artifact.Validation != "strict" && artifact.Validation != "advisory" {
			return fmt.Errorf("workflow artifact %s validation is invalid", artifact.ID)
		}
		if artifact.MaxBytes > 1024*1024*1024 {
			return fmt.Errorf("workflow artifact %s max_bytes is out of range", artifact.ID)
		}
	}
	if manifest.Candidate != nil && !artifactIDs[manifest.Candidate.ArtifactID] {
		return fmt.Errorf("workflow candidate artifact is not declared: %s", manifest.Candidate.ArtifactID)
	}
	if !matches(manifest.UI.Mode, "standard", "declarative", "custom") {
		return fmt.Errorf("workflow ui mode is invalid: %s", manifest.UI.Mode)
	}
	for _, surface := range manifest.UI.Surfaces {
		if !matches(surface, "agent", "dashboard", "mcp") {
			return fmt.Errorf("unsupported workflow ui surface: %s", surface)
		}
	}
	if manifest.UI.Entry != "" {
		if err := validatePackagePath(manifest.UI.Entry); err != nil {
			return fmt.Errorf("workflow ui entry: %w", err)
		}
		if fileExists != nil && !fileExists(manifest.UI.Entry) {
			return fmt.Errorf("workflow ui entry is missing: %s", manifest.UI.Entry)
		}
	}
	return nil
}

func collectStepIDs(steps []Step, ids map[string]bool) error {
	if len(steps) == 0 {
		return errors.New("workflow step scope must contain at least one step")
	}
	for _, step := range steps {
		if !idPattern.MatchString(step.ID) {
			return fmt.Errorf("invalid workflow step id: %s", step.ID)
		}
		if ids[step.ID] {
			return fmt.Errorf("duplicate workflow step id: %s", step.ID)
		}
		ids[step.ID] = true
		if step.Loop != nil {
			if err := collectStepIDs(step.Loop.Steps, ids); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateSteps(steps []Step, stepIDs map[string]bool, manifest Manifest) error {
	for _, step := range steps {
		if strings.TrimSpace(step.Title) == "" {
			return fmt.Errorf("workflow step %s title is required", step.ID)
		}
		if err := metaguide.Check(metaguide.StepTitle, step.Title); err != nil {
			return err
		}
		if !matches(step.ExecutionMode, "sync", "long_running", "provider_defined") {
			return fmt.Errorf("workflow step %s execution_mode is invalid", step.ID)
		}
		if step.OnFailure != "" && !matches(step.OnFailure, "fail", "continue") {
			return fmt.Errorf("workflow step %s on_failure must be fail or continue", step.ID)
		}
		if step.Kind != "" && !matches(step.Kind, "capability", "runtime", "manual", "loop", "provider_defined") {
			return fmt.Errorf("workflow step %s kind is invalid", step.ID)
		}
		for _, dependency := range step.DependsOn {
			if !stepIDs[dependency] {
				return fmt.Errorf("workflow step %s depends on unknown step %s", step.ID, dependency)
			}
		}
		if step.Kind == "capability" && strings.TrimSpace(step.CapabilityID) == "" {
			return fmt.Errorf("workflow capability step %s requires capability_id", step.ID)
		}
		if step.Kind == "runtime" || step.Runtime != nil {
			if step.Runtime == nil || strings.TrimSpace(step.Runtime.Provider) == "" || strings.TrimSpace(step.Runtime.Prompt) == "" {
				return fmt.Errorf("workflow runtime step %s requires provider and prompt", step.ID)
			}
			if step.Runtime.ResultSchema != "" {
				if err := validatePackagePath(step.Runtime.ResultSchema); err != nil {
					return err
				}
			}
			if step.Runtime.TimeoutSeconds > 86400 {
				return fmt.Errorf("workflow runtime step %s timeout_seconds is out of range", step.ID)
			}
			if step.Runtime.ToolPolicy != "" && !matches(step.Runtime.ToolPolicy, "default", "none") {
				return fmt.Errorf("workflow runtime step %s tool_policy must be default or none", step.ID)
			}
			if len(step.Runtime.InputArtifacts) > 0 && step.Runtime.ToolPolicy == "none" {
				return fmt.Errorf("workflow runtime step %s cannot combine input_artifacts with tool_policy=none", step.ID)
			}
			seenArtifacts := map[string]bool{}
			for _, artifactID := range step.Runtime.InputArtifacts {
				if strings.TrimSpace(artifactID) == "" {
					return fmt.Errorf("workflow runtime step %s input_artifacts contains an empty id", step.ID)
				}
				if seenArtifacts[artifactID] {
					return fmt.Errorf("workflow runtime step %s input_artifacts contains duplicate id %s", step.ID, artifactID)
				}
				seenArtifacts[artifactID] = true
			}
		}
		if step.Kind == "loop" || step.Loop != nil {
			if step.Loop == nil {
				return fmt.Errorf("workflow loop step %s requires loop", step.ID)
			}
			if step.Loop.MaxIterations < 1 || step.Loop.MaxIterations > 20 {
				return fmt.Errorf("workflow loop step %s max_iterations is out of range", step.ID)
			}
			if step.Loop.ContinueWhen == nil && step.Loop.ExitWhen == nil {
				return fmt.Errorf("workflow loop step %s requires continue_when or exit_when", step.ID)
			}
			if scopeHasApproval(step.Loop.Steps) {
				return fmt.Errorf("workflow loop step %s cannot contain approval-required steps", step.ID)
			}
			if scopeHasCandidateAction(step.Loop.Steps) {
				return fmt.Errorf("workflow loop step %s cannot contain candidate actions", step.ID)
			}
			if err := validateSteps(step.Loop.Steps, stepIDs, manifest); err != nil {
				return err
			}
		}
		if step.CandidateAction != "" && !matches(step.CandidateAction, "freeze", "require") {
			return fmt.Errorf("workflow step %s candidate_action is invalid", step.ID)
		}
	}
	return nil
}

func scopeHasApproval(steps []Step) bool {
	for _, step := range steps {
		if step.ApprovalRequired {
			return true
		}
		if step.Loop != nil && scopeHasApproval(step.Loop.Steps) {
			return true
		}
	}
	return false
}

func scopeHasCandidateAction(steps []Step) bool {
	for _, step := range steps {
		if step.CandidateAction != "" {
			return true
		}
		if step.Loop != nil && scopeHasCandidateAction(step.Loop.Steps) {
			return true
		}
	}
	return false
}

func validateCandidate(manifest Manifest) error {
	freezeCount := 0
	var walk func([]Step) error
	walk = func(steps []Step) error {
		for _, step := range steps {
			switch step.CandidateAction {
			case "freeze":
				freezeCount++
			case "require":
				if manifest.Candidate == nil {
					return fmt.Errorf("workflow step %s requires an undeclared candidate", step.ID)
				}
			case "":
			default:
				return fmt.Errorf("workflow step %s candidate_action is invalid", step.ID)
			}
			if step.Loop != nil {
				if err := walk(step.Loop.Steps); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(manifest.Steps); err != nil {
		return err
	}
	if manifest.Candidate != nil && manifest.Candidate.Required && freezeCount != 1 {
		return errors.New("workflow candidate requires exactly one freeze step")
	}
	if manifest.Candidate != nil && manifest.Candidate.Source != "git" {
		return errors.New("workflow candidate source must be git")
	}
	return nil
}

// validateDefaultEndpoint 保证默认入口/出口指向真实声明的端点。
func validateDefaultEndpoint(label string, value string, endpoints []Endpoint) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	for _, endpoint := range endpoints {
		if endpoint.ID == trimmed {
			return nil
		}
	}
	return fmt.Errorf("workflow %s is not a declared endpoint: %s", label, trimmed)
}

func validateEndpoints(name string, endpoints []Endpoint, stepIDs map[string]bool) error {
	seen := map[string]bool{}
	for _, endpoint := range endpoints {
		if !idPattern.MatchString(endpoint.ID) {
			return fmt.Errorf("invalid workflow %s id: %s", name, endpoint.ID)
		}
		if seen[endpoint.ID] {
			return fmt.Errorf("duplicate workflow %s id: %s", name, endpoint.ID)
		}
		seen[endpoint.ID] = true
		if !stepIDs[endpoint.AtStep] {
			return fmt.Errorf("workflow %s %s references unknown step %s", name, endpoint.ID, endpoint.AtStep)
		}
	}
	return nil
}

func validateChecksums(files map[string][]byte) error {
	data, ok := files[ChecksumsFile]
	if !ok {
		return errors.New("workflow package must contain checksums.sha256")
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
		if name == ChecksumsFile {
			continue
		}
		digest := sha256.Sum256(content)
		if expected[name] != hex.EncodeToString(digest[:]) {
			return fmt.Errorf("workflow checksum mismatch: %s", name)
		}
		delete(expected, name)
	}
	if len(expected) != 0 {
		return errors.New("checksums.sha256 references missing content")
	}
	return nil
}

func checksumLines(files map[string][]byte) []string {
	lines := make([]string, 0, len(files))
	for name, content := range files {
		digest := sha256.Sum256(content)
		lines = append(lines, fmt.Sprintf("%x  %s", digest, name))
	}
	sort.Slice(lines, func(i, j int) bool {
		return strings.ToLower(lines[i]) < strings.ToLower(lines[j])
	})
	return lines
}

func validatePackagePath(value string) error {
	value = filepath.ToSlash(value)
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") || strings.Contains(value, ":") || strings.Contains(value, "../") || strings.HasPrefix(value, "../") {
		return fmt.Errorf("invalid workflow package path: %s", value)
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("workflow package path escapes root: %s", value)
	}
	return nil
}

func shouldPrune(relative string) bool {
	for _, component := range strings.Split(filepath.ToSlash(relative), "/") {
		if component == "node_modules" || strings.HasPrefix(component, ".") {
			return true
		}
	}
	return false
}

func validateUniqueStrings(name string, values []string) error {
	seen := map[string]bool{}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("workflow %s must not be empty", name)
		}
		if seen[value] {
			return fmt.Errorf("duplicate workflow %s: %s", name, value)
		}
		seen[value] = true
	}
	return nil
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

// validateDependencyRefs 校验依赖声明：ID 非空且不重复、最低版本是规范版本号。
// ID 与版本在发布时会被拼进 tag 与制品名，非法字符必须在这里拦下。
func validateDependencyRefs(kind string, refs []DependencyRef) error {
	seen := map[string]bool{}
	for _, ref := range refs {
		id := strings.TrimSpace(ref.ID)
		if id == "" {
			return fmt.Errorf("workflow %s dependency must declare an id", kind)
		}
		if !idPattern.MatchString(id) || strings.Contains(id, "..") {
			return fmt.Errorf("invalid workflow %s dependency id: %s", kind, ref.ID)
		}
		if seen[id] {
			return fmt.Errorf("duplicate workflow %s dependency: %s", kind, id)
		}
		seen[id] = true
		if ref.MinVersion != "" && !versionPattern.MatchString(ref.MinVersion) {
			return fmt.Errorf("invalid workflow %s dependency min_version: %s", kind, ref.MinVersion)
		}
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func matches(value string, allowed ...string) bool {
	return contains(allowed, value)
}

func buildManifest(config Config) Manifest {
	manifest := Manifest{
		SchemaVersion:     "workflow_package.v1",
		ID:                config.ID,
		Version:           config.Version,
		Name:              config.Name,
		Description:       config.Description,
		MinAgentVersion:   config.MinAgentVersion,
		LocalRequirements: map[string]any{},
		Dependencies:      Dependencies{Skills: []DependencyRef{}, Plugins: []DependencyRef{}, Connectors: []string{}, Runtimes: []string{}},
		ExecutionPolicy:   "strict",
		Steps: []Step{{
			ID:            "START",
			Title:         "开始",
			Kind:          "manual",
			ExecutionMode: "sync",
			RiskLevel:     "read_only",
			DependsOn:     []string{},
		}},
		Artifacts:         []Artifact{},
		UI:                UI{Mode: "declarative", Entry: "ui/workflow-view.json", Surfaces: []string{"agent"}},
		SupportedRuntimes: []string{},
		CreatedAt:         time.Now().UTC().Format(time.RFC3339),
	}
	switch config.Template {
	case "segmented":
		manifest.ExecutionPolicy = "segmented"
		manifest.Steps = placeholderPipeline()
		manifest.Entrypoints = []Endpoint{{ID: "start", AtStep: "START", Label: "从开始执行"}, {ID: "execute", AtStep: "EXECUTE", Label: "从执行阶段开始"}}
		manifest.Exits = []Endpoint{{ID: "prepared", AtStep: "EXECUTE", Label: "完成执行阶段"}, {ID: "completed", AtStep: "FINISH", Label: "完成全部流程"}}
	case "flexible":
		manifest.ExecutionPolicy = "flexible"
		manifest.Steps = placeholderPipeline()
		manifest.Entrypoints = []Endpoint{{ID: "start", AtStep: "START", Label: "从开始执行"}, {ID: "prepare", AtStep: "PREPARE", Label: "从准备阶段开始"}, {ID: "execute", AtStep: "EXECUTE", Label: "从执行阶段开始"}}
		manifest.Exits = []Endpoint{{ID: "prepared", AtStep: "PREPARE", Label: "完成准备"}, {ID: "executed", AtStep: "EXECUTE", Label: "完成执行"}, {ID: "completed", AtStep: "FINISH", Label: "完成流程"}}
	case "development-loop":
		manifest.Steps = []Step{
			{ID: "START", Title: "准备开发任务", Kind: "manual", ExecutionMode: "sync", RiskLevel: "read_only"},
			{
				ID:            "DEV-LOOP",
				Title:         "开发与反馈循环",
				Kind:          "loop",
				ExecutionMode: "long_running",
				RiskLevel:     "local_write",
				DependsOn:     []string{"START"},
				Loop: &Loop{
					MaxIterations: 3,
					PauseFeedback: true,
					ContinueWhen:  map[string]any{"operator": "not_equals", "path": "loops.DEV-LOOP.latest.steps.DEV-REVIEW.decision", "value": "accepted"},
					ExitWhen:      map[string]any{"operator": "equals", "path": "loops.DEV-LOOP.latest.steps.DEV-REVIEW.decision", "value": "accepted"},
					Steps: []Step{
						{
							ID:            "DEV-CODE",
							Title:         "修改代码",
							Kind:          "runtime",
							ExecutionMode: "long_running",
							RiskLevel:     "local_write",
							Runtime: &Runtime{
								Provider:       "himind.builtin",
								Prompt:         "根据需求、验收标准、当前代码和用户反馈完成开发。完成后返回 summary、changes 和 verification。",
								WorkspacePath:  "input.workspace_root",
								ResultSchema:   "schemas/result.schema.json",
								TimeoutSeconds: 3600,
							},
						},
						{
							ID:            "DEV-REVIEW",
							Title:         "独立检查",
							Kind:          "runtime",
							ExecutionMode: "sync",
							RiskLevel:     "read_only",
							DependsOn:     []string{"DEV-CODE"},
							Runtime: &Runtime{
								Provider:       "himind.builtin",
								Prompt:         "检查需求、代码和验证结果。返回 JSON：{\"decision\":\"accepted|rejected\",\"reason\":\"...\"}。",
								WorkspacePath:  "input.workspace_root",
								ResultSchema:   "schemas/result.schema.json",
								TimeoutSeconds: 900,
							},
						},
					},
				},
			},
		}
		manifest.SupportedRuntimes = []string{"himind.builtin"}
		manifest.Dependencies.Runtimes = []string{"himind.builtin"}
	case "capability-pipeline":
		manifest.Steps = placeholderPipeline()
		manifest.Steps[1].Kind = "capability"
		manifest.Steps[1].CapabilityID = "workflow.example.execute"
		manifest.Capabilities = []string{"workflow.example.execute"}
	}
	return manifest
}

func placeholderPipeline() []Step {
	return []Step{
		{ID: "START", Title: "开始", Kind: "manual", ExecutionMode: "sync", RiskLevel: "read_only"},
		{ID: "PREPARE", Title: "准备输入与依赖", Kind: "manual", ExecutionMode: "sync", RiskLevel: "read_only", DependsOn: []string{"START"}},
		{ID: "EXECUTE", Title: "执行主要工作", Kind: "manual", ExecutionMode: "sync", RiskLevel: "read_only", DependsOn: []string{"PREPARE"}},
		{ID: "FINISH", Title: "完成", Kind: "manual", ExecutionMode: "sync", RiskLevel: "read_only", DependsOn: []string{"EXECUTE"}},
	}
}

func buildView(title, template string) View {
	return View{
		SchemaVersion: "workflow_view.v1",
		Title:         title,
		Sections: []ViewSection{{
			ID:    "target",
			Title: "目标",
			Fields: []map[string]any{{
				"id":          "objective",
				"label":       "任务目标",
				"type":        "textarea",
				"required":    true,
				"placeholder": "填写本次工作流的输入目标。",
			}},
		}},
		Actions: []string{template + "_start"},
	}
}

func templateReadme(config Config) string {
	return fmt.Sprintf("# %s\n\n%s\n\n模板：`%s`\n\nWorkflow 源码使用 `workflow.json` 作为执行契约，`ui/workflow-view.json` 作为声明式 UI。标准目录包括 `schemas/`、`artifacts/`、`connectors/`、`examples/` 和 `tests/contract/`。\n", config.Name, config.Description, config.Template)
}

func hasSchemaTemplate(template string) bool {
	return template == "development-loop" || template == "capability-pipeline"
}
