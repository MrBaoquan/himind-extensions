package workflowproject

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// Role mirrors Agent WorkflowRole. Unknown role IDs remain valid annotations.
type Role struct {
	Optional        bool   `json:"optional,omitempty"`
	Label           string `json:"label,omitempty"`
	Brief           string `json:"brief,omitempty"`
	RuntimeProvider string `json:"runtime_provider,omitempty"`
	Prompt          string `json:"prompt,omitempty"`
	ToolPolicy      string `json:"tool_policy,omitempty"`
	Model           string `json:"model,omitempty"`
}

type Delegate struct {
	Branches     []DelegateBranch `json:"branches"`
	Strategy     string           `json:"strategy"`
	MinSucceeded uint32           `json:"min_succeeded,omitempty"`
}

type DelegateBranch struct {
	ID      string         `json:"id"`
	Title   string         `json:"title"`
	RoleID  string         `json:"role_id,omitempty"`
	Runtime *Runtime       `json:"runtime,omitempty"`
	Input   map[string]any `json:"input,omitempty"`
}

// Agent denies unknown execution fields; silently dropping them can remove gates.
func (manifest *Manifest) UnmarshalJSON(data []byte) error {
	type plain Manifest
	var decoded plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	*manifest = Manifest(decoded)
	return nil
}

func (delegate *Delegate) UnmarshalJSON(data []byte) error {
	type plain Delegate
	decoded := plain{Strategy: "parallel"}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	*delegate = Delegate(decoded)
	return nil
}

func (branch *DelegateBranch) UnmarshalJSON(data []byte) error {
	type plain DelegateBranch
	decoded := plain{Input: map[string]any{}}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	*branch = DelegateBranch(decoded)
	return nil
}

func validateRoleID(id string) error {
	if len(id) == 0 || len(id) > 200 {
		return fmt.Errorf("invalid workflow role_id: %s", id)
	}
	for _, char := range id {
		if !(char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || strings.ContainsRune("._-", char)) {
			return fmt.Errorf("invalid workflow role_id: %s", id)
		}
	}
	return nil
}

func acceptsProvider(manifest Manifest, provider string) bool {
	provider = strings.TrimSpace(provider)
	for _, providers := range [][]string{manifest.SupportedRuntimes, manifest.OptionalProviders} {
		for _, declared := range providers {
			if provider != "" && strings.TrimSpace(declared) == provider {
				return true
			}
		}
	}
	return false
}

func validateRoles(manifest Manifest) error {
	for id, role := range manifest.Roles {
		if err := validateRoleID(id); err != nil {
			return err
		}
		policy, provider := strings.TrimSpace(role.ToolPolicy), strings.TrimSpace(role.RuntimeProvider)
		if !matches(policy, "", "default", "none", "files_only") {
			return fmt.Errorf("workflow role %s tool_policy must be default, none or files_only", id)
		}
		if policy == "files_only" && !matches(provider, "", "auto", "himind.builtin") {
			return fmt.Errorf("workflow role %s provider %s cannot enforce tool_policy=files_only", id, provider)
		}
		if provider != "" && !acceptsProvider(manifest, provider) {
			return fmt.Errorf("workflow role %s runtime_provider is not declared in supported_runtimes or optional_providers: %s", id, provider)
		}
		if err := validateModelAlias(role.Model); err != nil {
			return fmt.Errorf("workflow role %s model: %w", id, err)
		}
	}
	return nil
}

func normalizedKind(step Step) string {
	if strings.TrimSpace(step.Kind) != "" {
		return strings.TrimSpace(step.Kind)
	}
	switch {
	case step.Delegate != nil:
		return "delegate"
	case step.Loop != nil:
		return "loop"
	case step.Runtime != nil:
		return "runtime"
	case strings.TrimSpace(step.CapabilityID) != "":
		return "capability"
	default:
		return "manual"
	}
}

func validateTeamScopes(steps []Step, manifest Manifest, depth int, fileExists func(string) bool) error {
	if depth > 8 {
		return fmt.Errorf("workflow loop nesting exceeds 8 levels")
	}
	for _, step := range steps {
		if id := strings.TrimSpace(step.RoleID); id != "" {
			if err := validateRoleID(id); err != nil {
				return fmt.Errorf("workflow step %s: %w", step.ID, err)
			}
		}
		kind := normalizedKind(step)
		if kind == "delegate" {
			if step.Runtime != nil || step.Loop != nil || strings.TrimSpace(step.CapabilityID) != "" {
				return fmt.Errorf("workflow delegate step %s cannot declare capability_id, runtime or loop", step.ID)
			}
			if err := validateDelegate(step, manifest, depth, fileExists); err != nil {
				return err
			}
		} else if step.Delegate != nil {
			return fmt.Errorf("workflow %s step %s cannot declare delegate", kind, step.ID)
		}
		if step.Runtime != nil {
			if err := validateTeamRuntime(*step.Runtime, "step "+step.ID, manifest, false, fileExists); err != nil {
				return err
			}
		}
		if step.Loop != nil {
			if err := validateTeamScopes(step.Loop.Steps, manifest, depth+1, fileExists); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateDelegate(step Step, manifest Manifest, depth int, fileExists func(string) bool) error {
	delegate := step.Delegate
	if delegate == nil {
		return fmt.Errorf("workflow delegate step %s requires delegate", step.ID)
	}
	if depth > 4 {
		return fmt.Errorf("workflow delegate nesting exceeds 4 levels")
	}
	if !matches(strings.TrimSpace(delegate.Strategy), "parallel", "sequential") {
		return fmt.Errorf("workflow delegate step %s strategy must be parallel or sequential", step.ID)
	}
	if len(delegate.Branches) < 2 || len(delegate.Branches) > 8 {
		return fmt.Errorf("workflow delegate step %s requires between 2 and 8 branches", step.ID)
	}
	if int(delegate.MinSucceeded) > len(delegate.Branches) {
		return fmt.Errorf("workflow delegate step %s min_succeeded exceeds branch count", step.ID)
	}
	ids := map[string]bool{}
	for _, branch := range delegate.Branches {
		if err := validateRoleID(branch.ID); err != nil {
			return fmt.Errorf("workflow delegate branch %s id: %w", branch.ID, err)
		}
		if ids[branch.ID] {
			return fmt.Errorf("duplicate workflow delegate branch id: %s", branch.ID)
		}
		ids[branch.ID] = true
		if strings.TrimSpace(branch.Title) == "" {
			return fmt.Errorf("workflow delegate branch %s title is required", branch.ID)
		}
		if id := strings.TrimSpace(branch.RoleID); id != "" {
			if err := validateRoleID(id); err != nil {
				return fmt.Errorf("workflow delegate branch %s: %w", branch.ID, err)
			}
		}
		if branch.Input == nil {
			return fmt.Errorf("workflow delegate branch %s input must be an object", branch.ID)
		}
		if branch.Runtime == nil {
			return fmt.Errorf("workflow delegate branch %s requires runtime", branch.ID)
		}
		if err := validateTeamRuntime(*branch.Runtime, "delegate branch "+branch.ID, manifest, true, fileExists); err != nil {
			return err
		}
	}
	return nil
}

func validateTeamRuntime(runtime Runtime, label string, manifest Manifest, branch bool, fileExists func(string) bool) error {
	provider := strings.TrimSpace(runtime.Provider)
	if provider == "" || strings.TrimSpace(runtime.Prompt) == "" {
		return fmt.Errorf("workflow %s runtime requires provider and prompt", label)
	}
	if provider != "auto" && !acceptsProvider(manifest, provider) {
		return fmt.Errorf("workflow %s uses undeclared provider %s", label, provider)
	}
	if !matches(runtime.PermissionMode, "", "workspace_write", "full_access") {
		return fmt.Errorf("workflow %s unsupported runtime permission_mode: %s", label, runtime.PermissionMode)
	}
	if runtime.PermissionMode == "full_access" && !matches(provider, "auto", "himind.builtin", "personal.codex") && !strings.HasPrefix(provider, "acp.") {
		return fmt.Errorf("workflow %s provider %s cannot enforce permission_mode=full_access", label, provider)
	}
	if !matches(strings.TrimSpace(runtime.ToolPolicy), "", "default", "none", "files_only") {
		return fmt.Errorf("workflow %s tool_policy must be default, none or files_only", label)
	}
	if strings.TrimSpace(runtime.ToolPolicy) == "files_only" && !matches(provider, "auto", "himind.builtin") {
		return fmt.Errorf("workflow %s cannot enforce tool_policy=files_only with %s", label, provider)
	}
	if runtime.ResultSchema != "" {
		if err := validatePackagePath(runtime.ResultSchema); err != nil {
			return fmt.Errorf("workflow %s result_schema: %w", label, err)
		}
		if fileExists != nil && !fileExists(runtime.ResultSchema) {
			return fmt.Errorf("workflow %s result_schema is missing: %s", label, runtime.ResultSchema)
		}
	}
	if err := validateModelAlias(runtime.Model); err != nil {
		return fmt.Errorf("workflow %s model: %w", label, err)
	}
	if err := modelMatchesProvider(provider, runtime.Model); err != nil {
		return fmt.Errorf("workflow %s: %w", label, err)
	}
	seenModels := map[string]bool{}
	for _, alias := range runtime.ModelFallbacks {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			return fmt.Errorf("workflow %s model_fallbacks contains an empty alias", label)
		}
		if err := validateModelAlias(alias); err != nil {
			return fmt.Errorf("workflow %s model_fallbacks: %w", label, err)
		}
		// Rust delegate branches only validate fallback syntax; runtime steps also
		// require unique aliases in the primary model's namespace and provider.
		if !branch {
			if seenModels[alias] {
				return fmt.Errorf("workflow %s model_fallbacks contains a duplicate alias", label)
			}
			if err := modelMatchesProvider(provider, alias); err != nil {
				return fmt.Errorf("workflow %s model_fallbacks: %w", label, err)
			}
			if strings.TrimSpace(runtime.Model) != "" && strings.SplitN(strings.TrimSpace(runtime.Model), ":", 2)[0] != strings.SplitN(alias, ":", 2)[0] {
				return fmt.Errorf("workflow %s model_fallbacks must stay in the same namespace as model", label)
			}
		}
		seenModels[alias] = true
	}
	return nil
}

func validateModelAlias(alias string) error {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return nil
	}
	if len(alias) > 300 || strings.ContainsFunc(alias, func(char rune) bool { return unicode.IsSpace(char) || unicode.IsControl(char) }) {
		return fmt.Errorf("invalid model alias: whitespace, control characters or length over 300")
	}
	parts := strings.SplitN(alias, ":", 3)
	if len(parts) != 3 || !matches(parts[0], "himind", "acp", "cli") || parts[1] == "" || parts[2] == "" || (parts[0] == "acp" && parts[1] == "acp.") {
		return fmt.Errorf("model alias must use himind:<service>:<model>, acp:<provider>:<model> or cli:<provider>:<model>")
	}
	return nil
}

func modelMatchesProvider(provider, alias string) error {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return nil
	}
	if provider == "" || provider == "auto" {
		return fmt.Errorf("declaring model requires an explicit runtime provider")
	}
	parts := strings.SplitN(alias, ":", 3)
	acp, cli := strings.HasPrefix(provider, "acp."), matches(provider, "personal.codex", "personal.github-copilot")
	if parts[0] == "himind" && !acp && !cli || parts[0] == "acp" && acp {
		return nil
	}
	if parts[0] == "cli" && cli && strings.EqualFold(parts[1], strings.TrimPrefix(provider, "personal.")) {
		return nil
	}
	return fmt.Errorf("runtime %s cannot execute model alias %s", provider, alias)
}
