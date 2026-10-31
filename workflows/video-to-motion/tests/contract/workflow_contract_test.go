package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/MrBaoquan/himind-extensions/tooling/workflowproject"
)

func workflowRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func readJSON[T any](t *testing.T, path string) T {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var value T
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return value
}

type profile struct {
	SchemaVersion   int     `json:"schema_version"`
	ID              string  `json:"platform_profile_id"`
	Plugin          string  `json:"plugin"`
	CapabilityClass string  `json:"capability_class"`
	ActiveCommand   *string `json:"active_command"`
	MotionAxes      []struct {
		Role    string `json:"role"`
		Channel string `json:"channel"`
	} `json:"motion_axes"`
	Effects []struct {
		Role string `json:"role"`
		Mode string `json:"mode"`
	} `json:"effects"`
	Semantics map[string]struct {
		Status           string `json:"status"`
		RequiresApproval bool   `json:"requires_approval"`
	} `json:"semantics"`
	Safety struct {
		TailResetRequired bool `json:"tail_reset_required"`
	} `json:"safety"`
}

func loadProfile(t *testing.T, root, id string) profile {
	t.Helper()
	p := readJSON[profile](t, filepath.Join(root, "profiles", id+".json"))
	if p.SchemaVersion != 1 || p.ID != id {
		t.Fatalf("bad profile identity: %+v", p)
	}
	return p
}

func TestWorkflowPackageValidates(t *testing.T) {
	root := workflowRoot(t)
	if err := workflowproject.Validate(root); err != nil {
		t.Fatal(err)
	}
}

func TestAllJSONAndArtifactSchemasParse(t *testing.T) {
	root := workflowRoot(t)
	paths := []string{
		filepath.Join(root, "workflow.json"),
		filepath.Join(root, "ui", "workflow-view.json"),
		filepath.Join(root, "profiles", "catalog.json"),
	}
	for _, pattern := range []string{"profiles/*.json", "schemas/*.json", "artifacts/*.json", "examples/*.json"} {
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern)))
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, matches...)
	}
	for _, path := range paths {
		var value any
		payload, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if err := json.Unmarshal(payload, &value); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
	}
}

func TestPlatformProfilesCoverActualIOUICapabilities(t *testing.T) {
	root := workflowRoot(t)
	catalog := readJSON[struct {
		Profiles []struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		} `json:"profiles"`
	}](t, filepath.Join(root, "profiles", "catalog.json"))
	if len(catalog.Profiles) != 4 {
		t.Fatalf("expected 4 profiles, got %d", len(catalog.Profiles))
	}
	ids := []string{}
	for _, item := range catalog.Profiles {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(item.Path))); err != nil {
			t.Fatalf("profile path %s: %v", item.Path, err)
		}
		ids = append(ids, item.ID)
	}
	sort.Strings(ids)
	want := []string{"motion-plat-pitch-roll-v1", "mp-njlj-a3t-v1", "mp-njqk-pitch-roll-v1", "mp-ydl-heave-v1"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("profiles=%v", ids)
	}

	ydl := loadProfile(t, root, "mp-ydl-heave-v1")
	if len(ydl.MotionAxes) != 1 || ydl.MotionAxes[0].Role != "heave" {
		t.Fatalf("YDL axes=%v", ydl.MotionAxes)
	}
	if ydl.Semantics["weightless_drop"].Status != "exact" {
		t.Fatal("YDL weightless_drop must be exact")
	}
	foundWind := false
	for _, e := range ydl.Effects {
		if e.Role == "wind" && e.Mode == "continuous_percent" {
			foundWind = true
		}
	}
	if !foundWind {
		t.Fatal("YDL wind must preserve continuous strength capability")
	}

	njlj := loadProfile(t, root, "mp-njlj-a3t-v1")
	if njlj.ActiveCommand == nil || *njlj.ActiveCommand != "A3T" {
		t.Fatalf("NJLJ active command=%v", njlj.ActiveCommand)
	}
	roles := map[string]bool{}
	for _, a := range njlj.MotionAxes {
		roles[a.Role] = true
	}
	if !roles["pitch"] || !roles["roll"] || !roles["heave"] || roles["yaw"] {
		t.Fatalf("NJLJ A3T roles=%v", roles)
	}
	if njlj.Semantics["yaw_turn"].Status != "unsupported" {
		t.Fatal("A3T yaw must be unsupported")
	}

	njqk := loadProfile(t, root, "mp-njqk-pitch-roll-v1")
	for _, a := range njqk.MotionAxes {
		if a.Role == "heave" {
			t.Fatal("NJQK must not declare heave")
		}
	}
	drop := njqk.Semantics["weightless_drop"]
	if drop.Status != "approximate" || !drop.RequiresApproval {
		t.Fatalf("NJQK drop mapping=%+v", drop)
	}

	legacy := loadProfile(t, root, "motion-plat-pitch-roll-v1")
	if legacy.Semantics["yaw_turn"].Status != "unsupported" {
		t.Fatal("legacy yaw must be unsupported")
	}
	for _, item := range []profile{ydl, njlj, njqk, legacy} {
		if !item.Safety.TailResetRequired {
			t.Fatalf("%s must require tail reset", item.ID)
		}
	}
}

func TestWorkflowDeclaresHardPlatformAwareDependencies(t *testing.T) {
	root := workflowRoot(t)
	var manifest struct {
		Dependencies struct {
			Skills []struct {
				ID         string `json:"id"`
				Required   bool   `json:"required"`
				MinVersion string `json:"min_version"`
			} `json:"skills"`
			Plugins []struct {
				ID         string `json:"id"`
				Required   bool   `json:"required"`
				MinVersion string `json:"min_version"`
			} `json:"plugins"`
		} `json:"dependencies"`
		Capabilities []string `json:"capabilities"`
	}
	manifest = readJSON[struct {
		Dependencies struct {
			Skills []struct {
				ID         string `json:"id"`
				Required   bool   `json:"required"`
				MinVersion string `json:"min_version"`
			} `json:"skills"`
			Plugins []struct {
				ID         string `json:"id"`
				Required   bool   `json:"required"`
				MinVersion string `json:"min_version"`
			} `json:"plugins"`
		} `json:"dependencies"`
		Capabilities []string `json:"capabilities"`
	}](t, filepath.Join(root, "workflow.json"))
	if len(manifest.Dependencies.Skills) != 1 || !manifest.Dependencies.Skills[0].Required || manifest.Dependencies.Skills[0].ID != "com.himind.skill.video-motion-authoring" {
		t.Fatalf("skills=%+v", manifest.Dependencies.Skills)
	}
	foundQA := false
	for _, p := range manifest.Dependencies.Plugins {
		if p.ID == "com.himind.motion-composer" {
			foundQA = p.Required && p.MinVersion == "1.2.0"
		}
	}
	if !foundQA {
		t.Fatal("motion-composer must be required >=1.2.0")
	}
	if !contains(manifest.Capabilities, "motion.composer.platform.resolve") {
		t.Fatal("platform resolve capability missing")
	}
}

func TestUIProfilesAndActionsMatchWorkflow(t *testing.T) {
	root := workflowRoot(t)
	var manifest struct {
		Entrypoints []struct {
			ID string `json:"id"`
		} `json:"entrypoints"`
		Exits []struct {
			ID string `json:"id"`
		} `json:"exits"`
	}
	manifest = readJSON[struct {
		Entrypoints []struct {
			ID string `json:"id"`
		} `json:"entrypoints"`
		Exits []struct {
			ID string `json:"id"`
		} `json:"exits"`
	}](t, filepath.Join(root, "workflow.json"))
	var view struct {
		Sections []struct {
			Fields []struct {
				ID      string   `json:"id"`
				Options []string `json:"options"`
			} `json:"fields"`
		} `json:"sections"`
		Actions []string `json:"actions"`
	}
	view = readJSON[struct {
		Sections []struct {
			Fields []struct {
				ID      string   `json:"id"`
				Options []string `json:"options"`
			} `json:"fields"`
		} `json:"sections"`
		Actions []string `json:"actions"`
	}](t, filepath.Join(root, "ui", "workflow-view.json"))
	wantActions := map[string]bool{}
	for _, ep := range manifest.Entrypoints {
		wantActions[ep.ID] = true
	}
	for _, action := range view.Actions {
		if !wantActions[action] {
			t.Fatalf("UI action %s is not an entrypoint", action)
		}
	}
	foundProfileOptions := false
	for _, section := range view.Sections {
		for _, field := range section.Fields {
			if field.ID == "platform_profile_path" {
				foundProfileOptions = len(field.Options) == 4
				for _, option := range field.Options {
					if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(option))); err != nil {
						t.Fatalf("UI platform option %s: %v", option, err)
					}
				}
			}
		}
	}
	if !foundProfileOptions {
		t.Fatal("UI must expose 4 platform profiles")
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestQualityLoopIsBoundedAndFailClosed(t *testing.T) {
	root := workflowRoot(t)
	type loopStep struct {
		ID               string `json:"id"`
		ApprovalRequired bool   `json:"approval_required"`
	}
	type loopSpec struct {
		MaxIterations int            `json:"max_iterations"`
		ContinueWhen  map[string]any `json:"continue_when"`
		ExitWhen      map[string]any `json:"exit_when"`
		Steps         []loopStep     `json:"steps"`
	}
	type stepSpec struct {
		ID       string         `json:"id"`
		FailWhen map[string]any `json:"fail_when"`
		Loop     *loopSpec      `json:"loop"`
	}
	var manifest struct {
		Steps []stepSpec `json:"steps"`
	}
	manifest = readJSON[struct {
		Steps []stepSpec `json:"steps"`
	}](t, filepath.Join(root, "workflow.json"))

	var qa *loopSpec
	for _, step := range manifest.Steps {
		if step.ID == "V2M-BINDING" && len(step.FailWhen) == 0 {
			t.Fatal("platform binding must fail closed")
		}
		if step.ID == "V2M-QA-LOOP" {
			qa = step.Loop
		}
	}
	if qa == nil {
		t.Fatal("quality loop missing")
	}
	if qa.MaxIterations != 3 || qa.ContinueWhen == nil || qa.ExitWhen == nil {
		t.Fatalf("invalid QA loop: %+v", qa)
	}
	if len(qa.Steps) != 2 {
		t.Fatalf("QA loop steps=%d", len(qa.Steps))
	}
	for _, step := range qa.Steps {
		if step.ApprovalRequired {
			t.Fatal("approval cannot be inside bounded QA loop")
		}
	}
}
