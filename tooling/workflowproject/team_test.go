package workflowproject

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func teamManifest(t *testing.T) map[string]any {
	t.Helper()
	return map[string]any{
		"schema_version": "workflow_package.v1", "id": "com.example.workflow.team",
		"version": "1.0.0", "min_agent_version": "0.3.90", "name": "团队交付",
		"release_notes": "测试角色和委派契约。", "supported_runtimes": []any{"himind.builtin"},
		"optional_providers": []any{"personal.codex"},
		"roles":              map[string]any{"reviewer": map[string]any{"optional": true, "model": "himind:service:review"}},
		"steps": []any{map[string]any{
			"id": "TEAM", "title": "团队审查", "kind": "delegate", "execution_mode": "sync",
			"delegate": map[string]any{"branches": []any{
				map[string]any{"id": "A", "title": "审查", "role_id": "reviewer", "runtime": map[string]any{"provider": "himind.builtin", "prompt": "审查", "tool_policy": "none"}},
				map[string]any{"id": "B", "title": "测试", "role_id": "unlisted.role", "runtime": map[string]any{"provider": "auto", "prompt": "测试"}},
			}},
		}},
		"artifacts": []any{}, "ui": map[string]any{"mode": "standard"},
	}
}

func teamStep(manifest map[string]any) map[string]any {
	return manifest["steps"].([]any)[0].(map[string]any)
}

func teamDelegate(manifest map[string]any) map[string]any {
	return teamStep(manifest)["delegate"].(map[string]any)
}

func teamBranch(manifest map[string]any) map[string]any {
	return teamDelegate(manifest)["branches"].([]any)[0].(map[string]any)
}

func teamRuntime(manifest map[string]any) map[string]any {
	return teamBranch(manifest)["runtime"].(map[string]any)
}

func TestTeamContractValidation(t *testing.T) {
	cases := []struct {
		name string
		edit func(map[string]any)
		want string
	}{
		{"default parallel and annotation", func(m map[string]any) {}, ""},
		{"sequential", func(m map[string]any) { teamDelegate(m)["strategy"] = "sequential" }, ""},
		{"optional role provider", func(m map[string]any) {
			m["roles"] = map[string]any{"reviewer": map[string]any{"runtime_provider": "personal.codex", "optional": true}}
		}, ""},
		{"optional branch provider", func(m map[string]any) {
			teamRuntime(m)["provider"] = "personal.codex"
			teamRuntime(m)["permission_mode"] = "full_access"
			teamRuntime(m)["model"] = "cli:codex:gpt-6"
		}, ""},
		{"files only", func(m map[string]any) { teamRuntime(m)["tool_policy"] = "files_only" }, ""},
		{"explicit network false", func(m map[string]any) { teamRuntime(m)["allow_network"] = false }, ""},
		{"bad role key", func(m map[string]any) { m["roles"] = map[string]any{"bad role": map[string]any{}} }, "role_id"},
		{"bad step role", func(m map[string]any) { teamStep(m)["role_id"] = "bad role" }, "role_id"},
		{"bad branch role", func(m map[string]any) { teamBranch(m)["role_id"] = "bad role" }, "role_id"},
		{"unknown role provider", func(m map[string]any) {
			m["roles"] = map[string]any{"reviewer": map[string]any{"runtime_provider": "missing"}}
		}, "runtime_provider"},
		{"bad role policy", func(m map[string]any) { m["roles"] = map[string]any{"reviewer": map[string]any{"tool_policy": "all"}} }, "tool_policy"},
		{"bad role model", func(m map[string]any) { m["roles"] = map[string]any{"reviewer": map[string]any{"model": "bare-model"}} }, "model"},
		{"role files only provider", func(m map[string]any) {
			m["roles"] = map[string]any{"reviewer": map[string]any{"runtime_provider": "personal.codex", "tool_policy": "files_only"}}
		}, "files_only"},
		{"missing delegate", func(m map[string]any) { delete(teamStep(m), "delegate") }, "requires delegate"},
		{"missing runtime", func(m map[string]any) { delete(teamBranch(m), "runtime") }, "requires runtime"},
		{"one branch", func(m map[string]any) { teamDelegate(m)["branches"] = teamDelegate(m)["branches"].([]any)[:1] }, "2 and 8"},
		{"nine branches", func(m map[string]any) {
			branches := teamDelegate(m)["branches"].([]any)
			for len(branches) < 9 {
				branches = append(branches, branches[0])
			}
			teamDelegate(m)["branches"] = branches
		}, "2 and 8"},
		{"duplicate branch", func(m map[string]any) { teamDelegate(m)["branches"].([]any)[1].(map[string]any)["id"] = "A" }, "duplicate"},
		{"bad branch id", func(m map[string]any) { teamBranch(m)["id"] = "bad id" }, "branch"},
		{"blank title", func(m map[string]any) { teamBranch(m)["title"] = " " }, "title"},
		{"null input", func(m map[string]any) { teamBranch(m)["input"] = nil }, "input must be an object"},
		{"array input", func(m map[string]any) { teamBranch(m)["input"] = []any{} }, "unmarshal"},
		{"blank prompt", func(m map[string]any) { teamRuntime(m)["prompt"] = " " }, "provider and prompt"},
		{"unknown provider", func(m map[string]any) { teamRuntime(m)["provider"] = "missing" }, "undeclared provider"},
		{"bad strategy", func(m map[string]any) { teamDelegate(m)["strategy"] = "race" }, "strategy"},
		{"empty strategy", func(m map[string]any) { teamDelegate(m)["strategy"] = "" }, "strategy"},
		{"excess success threshold", func(m map[string]any) { teamDelegate(m)["min_succeeded"] = 3 }, "min_succeeded"},
		{"negative success threshold", func(m map[string]any) { teamDelegate(m)["min_succeeded"] = -1 }, "unmarshal"},
		{"conflicting runtime", func(m map[string]any) { teamStep(m)["runtime"] = teamRuntime(m) }, "cannot declare"},
		{"conflicting loop", func(m map[string]any) { teamStep(m)["loop"] = map[string]any{"max_iterations": 1, "steps": []any{}} }, "step scope"},
		{"wrong kind", func(m map[string]any) { teamStep(m)["kind"] = "manual" }, "cannot declare delegate"},
		{"bad tool policy", func(m map[string]any) { teamRuntime(m)["tool_policy"] = "all" }, "tool_policy"},
		{"files only external", func(m map[string]any) {
			teamRuntime(m)["provider"] = "personal.codex"
			teamRuntime(m)["tool_policy"] = "files_only"
		}, "files_only"},
		{"bad permission", func(m map[string]any) { teamRuntime(m)["permission_mode"] = "root" }, "permission_mode"},
		{"bare model", func(m map[string]any) { teamRuntime(m)["model"] = "gpt-6" }, "model"},
		{"model with auto", func(m map[string]any) {
			teamRuntime(m)["provider"] = "auto"
			teamRuntime(m)["model"] = "himind:service:model"
		}, "explicit runtime"},
		{"model wrong namespace", func(m map[string]any) { teamRuntime(m)["model"] = "cli:codex:gpt-6" }, "cannot execute"},
		{"cli target mismatch", func(m map[string]any) {
			teamRuntime(m)["provider"] = "personal.codex"
			teamRuntime(m)["model"] = "cli:github-copilot:gpt-6"
		}, "cannot execute"},
		{"bad fallback", func(m map[string]any) { teamRuntime(m)["model_fallbacks"] = []any{"bare"} }, "model_fallbacks"},
		{"missing schema", func(m map[string]any) { teamRuntime(m)["result_schema"] = "schemas/missing.json" }, "result_schema is missing"},
		{"schema traversal", func(m map[string]any) { teamRuntime(m)["result_schema"] = "../escape.json" }, "result_schema"},
		{"unknown execution field", func(m map[string]any) { teamDelegate(m)["min_success"] = 1 }, "unknown field"},
		{"unknown role field", func(m map[string]any) { m["roles"] = map[string]any{"reviewer": map[string]any{"optionl": true}} }, "unknown field"},
		{"nested branch delegate", func(m map[string]any) { teamBranch(m)["delegate"] = map[string]any{} }, "unknown field"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			manifest := teamManifest(t)
			test.edit(manifest)
			data, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			var decoded Manifest
			if err = json.Unmarshal(data, &decoded); err == nil {
				err = validateManifest(decoded, func(string) bool { return false })
			}
			if test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("want %q, got %v", test.want, err)
			}
		})
	}
}

func TestMigratedTeamPackagesPreservePayload(t *testing.T) {
	for _, slug := range []string{"software-delivery", "ui-craft-team"} {
		t.Run(slug, func(t *testing.T) {
			root := filepath.Join("..", "..", "workflows", slug)
			manifest, err := Build(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(manifest.Roles) == 0 {
				t.Fatal("missing real role definitions")
			}
			archive := filepath.Join(t.TempDir(), slug+".hmwf")
			if err := Package(root, archive); err != nil {
				t.Fatal(err)
			}
			if err := Validate(archive); err != nil {
				t.Fatal(err)
			}
			reader, err := zip.OpenReader(archive)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			files := map[string][]byte{}
			for _, entry := range reader.File {
				stream, err := entry.Open()
				if err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(stream)
				stream.Close()
				if err != nil {
					t.Fatal(err)
				}
				files[entry.Name] = data
			}
			err = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if entry.IsDir() {
					return nil
				}
				relative, err := filepath.Rel(root, path)
				if err != nil {
					return err
				}
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				if !bytes.Equal(data, files[filepath.ToSlash(relative)]) {
					t.Errorf("archive lost or changed %s", relative)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTeamFieldsRoundTrip(t *testing.T) {
	raw := teamManifest(t)
	teamDelegate(raw)["strategy"] = "sequential"
	teamRuntime(raw)["allow_network"] = false
	teamRuntime(raw)["permission_mode"] = "workspace_write"
	teamRuntime(raw)["model"] = "himind:service:model"
	teamRuntime(raw)["model_fallbacks"] = []any{"himind:service:fallback"}
	raw["capability_alternatives"] = map[string]any{"distribution.release.publish": []any{"distribution.release.publish.github"}}
	data, _ := json.Marshal(raw)
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	role := manifest.Roles["reviewer"]
	delegate := manifest.Steps[0].Delegate
	runtime := delegate.Branches[0].Runtime
	if !role.Optional || role.Model != "himind:service:review" || delegate.Strategy != "sequential" || runtime.AllowNetwork == nil || *runtime.AllowNetwork || runtime.PermissionMode != "workspace_write" || runtime.Model != "himind:service:model" || len(runtime.ModelFallbacks) != 1 || len(manifest.CapabilityAlternatives) != 1 {
		t.Fatalf("team contract lost in round trip: %s", data)
	}
}

func TestInvalidDelegateCannotOverwritePackage(t *testing.T) {
	root := t.TempDir()
	manifest := teamManifest(t)
	teamDelegate(manifest)["min_succeeded"] = 3
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ManifestFile), data, 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "existing.hmwf")
	previous := []byte("previous package")
	if err := os.WriteFile(output, previous, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Validate(root); err == nil || !strings.Contains(err.Error(), "min_succeeded") {
		t.Fatalf("directory validation: %v", err)
	}
	if err := Package(root, output); err == nil || !strings.Contains(err.Error(), "min_succeeded") {
		t.Fatalf("package validation: %v", err)
	}
	actual, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, previous) {
		t.Fatal("invalid source overwrote the previous package")
	}
}

func TestExistingRepositoryWorkflowsStillValidate(t *testing.T) {
	for _, slug := range []string{"daily-work-log", "software-release", "tech-radar", "video-to-motion", "wechat-miniprogram-ci-delivery", "software-delivery", "ui-craft-team"} {
		t.Run(slug, func(t *testing.T) {
			if err := Validate(filepath.Join("..", "..", "workflows", slug)); err != nil {
				t.Fatal(err)
			}
		})
	}
}
