package workflowproject

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
)

func createWorkflow(t *testing.T, template string) (Config, Result) {
	t.Helper()
	workspace := t.TempDir()
	config := Config{
		Slug:            "sample-workflow",
		ID:              "com.example.workflow.sample",
		Name:            "Sample Workflow",
		Description:     "Workflow tooling test.",
		Author:          "Test User",
		Version:         "1.0.0",
		MinAgentVersion: "0.3.47",
		ReleaseNotes:    "Initial version.",
		Template:        template,
		OutputDir:       filepath.Join(workspace, "workflows"),
	}
	result, err := Create(config)
	if err != nil {
		t.Fatal(err)
	}
	return config, result
}

func TestCreateValidateAndPackageWorkflow(t *testing.T) {
	for _, template := range []string{"strict", "segmented", "flexible", "development-loop", "capability-pipeline"} {
		t.Run(template, func(t *testing.T) {
			_, result := createWorkflow(t, template)
			if err := Validate(result.Root); err != nil {
				t.Fatalf("validate scaffold: %v", err)
			}
			packagePath := filepath.Join(filepath.Dir(result.Root), result.WorkflowID+".hmwf")
			if err := Package(result.Root, packagePath); err != nil {
				t.Fatalf("package workflow: %v", err)
			}
			if err := Validate(packagePath); err != nil {
				t.Fatalf("validate package: %v", err)
			}
		})
	}
}

func TestPackageIsDeterministic(t *testing.T) {
	_, result := createWorkflow(t, "strict")
	first := filepath.Join(filepath.Dir(result.Root), "first.hmwf")
	second := filepath.Join(filepath.Dir(result.Root), "second.hmwf")
	if err := Package(result.Root, first); err != nil {
		t.Fatal(err)
	}
	if err := Package(result.Root, second); err != nil {
		t.Fatal(err)
	}
	firstContent, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	secondContent, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(firstContent) != sha256.Sum256(secondContent) {
		t.Fatal("expected deterministic workflow archives")
	}
}

func TestValidateRejectsDuplicateStepAndMissingSchema(t *testing.T) {
	_, result := createWorkflow(t, "strict")
	manifestPath := filepath.Join(result.Root, ManifestFile)
	duplicate := `{
  "schema_version": "workflow_package.v1",
  "id": "com.example.workflow.sample",
  "version": "1.0.0",
  "name": "Bad Workflow",
  "min_agent_version": "0.3.47",
  "dependencies": {},
  "steps": [
    {"id": "START", "title": "Start", "kind": "manual", "execution_mode": "sync"},
    {"id": "START", "title": "Again", "kind": "manual", "execution_mode": "sync"}
  ],
  "artifacts": [],
  "ui": {"mode": "standard"}
}`
	if err := os.WriteFile(manifestPath, []byte(duplicate), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Validate(result.Root); err == nil {
		t.Fatal("expected duplicate step to fail")
	}

	missingSchema := `{
  "schema_version": "workflow_package.v1",
  "id": "com.example.workflow.sample",
  "version": "1.0.0",
  "name": "Bad Workflow",
  "min_agent_version": "0.3.47",
  "dependencies": {},
  "steps": [{"id": "START", "title": "Start", "kind": "manual", "execution_mode": "sync"}],
  "artifacts": [{"id": "result", "artifact_type": "json", "name": "Result", "schema": "artifacts/missing.schema.json"}],
  "ui": {"mode": "standard"}
}`
	if err := os.WriteFile(manifestPath, []byte(missingSchema), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Validate(result.Root); err == nil {
		t.Fatal("expected missing artifact schema to fail")
	}
}
