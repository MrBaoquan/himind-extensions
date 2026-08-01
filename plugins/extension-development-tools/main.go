package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/MrBaoquan/himind-extensions/sdk/jsonrpc"
	"github.com/MrBaoquan/himind-extensions/tooling/pluginpack"
	"github.com/MrBaoquan/himind-extensions/tooling/pluginproject"
	"github.com/MrBaoquan/himind-extensions/tooling/skillproject"
	validator "github.com/MrBaoquan/himind-extensions/tools/cmd/himind-plugin-validate"
)

type input struct {
	WorkspaceRoot    string   `json:"workspace_root"`
	OutputDir        string   `json:"output_dir"`
	Path             string   `json:"path"`
	Output           string   `json:"output"`
	Name             string   `json:"name"`
	DisplayName      string   `json:"display_name"`
	Template         string   `json:"template"`
	Slug             string   `json:"slug"`
	ID               string   `json:"id"`
	Version          string   `json:"version"`
	MinAgentVersion  string   `json:"min_agent_version"`
	Description      string   `json:"description"`
	Author           string   `json:"author"`
	Categories       []string `json:"categories"`
	ReleaseNotes     string   `json:"release_notes"`
	Kind             string   `json:"kind"`
	SupportedClients []string `json:"supported_clients"`
}

func main() {
	if err := jsonrpc.Serve(os.Stdin, os.Stdout, handle); err != nil {
		fmt.Fprintln(os.Stderr, "extension development tools stopped:", err)
	}
}

func handle(request jsonrpc.Request) (any, *jsonrpc.Error) {
	var in input
	if rpcError := jsonrpc.DecodeParams(request, &in); rpcError != nil {
		return nil, rpcError
	}
	switch request.Method {
	case "extension.environment.preflight":
		return preflight(in.Kind), nil
	case "extension.plugin.scaffold":
		return pluginScaffold(in)
	case "extension.plugin.validate":
		return validatePath(in.WorkspaceRoot, in.Path, validator.ValidatePath)
	case "extension.plugin.build":
		return pluginBuild(in)
	case "extension.plugin.package":
		if err := ensureWithin(in.WorkspaceRoot, in.Path); err != nil {
			return nil, jsonrpc.InvalidParams(err.Error())
		}
		if err := ensureWithin(in.WorkspaceRoot, in.Output); err != nil {
			return nil, jsonrpc.InvalidParams(err.Error())
		}
		if err := pluginpack.Package(in.Path, in.Output); err != nil {
			return nil, jsonrpc.InternalError(err.Error())
		}
		return map[string]any{"ok": true, "output": in.Output}, nil
	case "extension.skill.scaffold":
		return skillScaffold(in)
	case "extension.skill.validate":
		return validatePath(in.WorkspaceRoot, in.Path, skillproject.Validate)
	case "extension.skill.package":
		if err := ensureWithin(in.WorkspaceRoot, in.Path); err != nil {
			return nil, jsonrpc.InvalidParams(err.Error())
		}
		if err := ensureWithin(in.WorkspaceRoot, in.Output); err != nil {
			return nil, jsonrpc.InvalidParams(err.Error())
		}
		if err := skillproject.Package(in.Path, in.Output); err != nil {
			return nil, jsonrpc.InternalError(err.Error())
		}
		return map[string]any{"ok": true, "output": in.Output}, nil
	default:
		return nil, jsonrpc.InvalidParams("unsupported extension development capability")
	}
}

func preflight(kind string) map[string]any {
	kind = strings.TrimSpace(strings.ToLower(kind))
	if kind == "" {
		kind = "plugin"
	}
	result := map[string]any{"checked_at": time.Now().UTC().Format(time.RFC3339), "kind": kind}
	if kind == "skill" {
		result["ready"] = true
		result["go"] = map[string]any{"required": false}
		return result
	}
	if kind != "plugin" {
		result["blockers"] = []string{"kind 必须是 plugin 或 skill"}
		return result
	}
	result["go"] = map[string]any{"installed": false, "required": true}
	path, err := exec.LookPath("go")
	if err != nil {
		result["blockers"] = []string{"未在 PATH 中找到 Go 工具链"}
		return result
	}
	output, err := exec.Command(path, "version").CombinedOutput()
	goResult := map[string]any{"installed": true, "path": path, "version": firstLine(string(output))}
	if err != nil {
		goResult["error"] = err.Error()
	}
	result["go"] = goResult
	if err != nil {
		result["blockers"] = []string{"Go 工具链无法执行"}
	} else {
		result["ready"] = true
	}
	return result
}

func pluginScaffold(in input) (any, *jsonrpc.Error) {
	if err := ensureWithin(in.WorkspaceRoot, in.OutputDir); err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	result, err := pluginproject.Create(pluginproject.Config{Name: in.Name, DisplayName: in.DisplayName, Description: in.Description, Author: in.Author, Categories: in.Categories, ReleaseNotes: in.ReleaseNotes, Template: in.Template, OutputDir: in.OutputDir})
	if err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	return result, nil
}

func skillScaffold(in input) (any, *jsonrpc.Error) {
	if err := ensureWithin(in.WorkspaceRoot, in.OutputDir); err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	root, err := skillproject.Create(skillproject.CreateConfig{Slug: in.Slug, ID: in.ID, Name: in.Name, Version: in.Version, Description: in.Description, Author: in.Author, Categories: in.Categories, ReleaseNotes: in.ReleaseNotes, MinAgentVersion: in.MinAgentVersion, Clients: in.SupportedClients, OutputDir: in.OutputDir})
	if err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	return map[string]any{"root": root}, nil
}

func pluginBuild(in input) (any, *jsonrpc.Error) {
	if err := ensureWithin(in.WorkspaceRoot, in.Path); err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	manifest, err := validator.ReadManifest(in.Path)
	if err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	if filepath.IsAbs(manifest.Entry) || strings.Contains(filepath.ToSlash(manifest.Entry), "../") {
		return nil, jsonrpc.InvalidParams("plugin entry must remain inside the project")
	}
	if result, runErr := runGo(in.Path, "test", "./..."); runErr != nil {
		return nil, jsonrpc.InternalError(result)
	}
	entry := filepath.Join(in.Path, filepath.FromSlash(manifest.Entry))
	if err := os.MkdirAll(filepath.Dir(entry), 0755); err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	result, runErr := runGo(in.Path, "build", "-o", entry, ".")
	if runErr != nil {
		return nil, jsonrpc.InternalError(result)
	}
	return map[string]any{"ok": true, "entry": entry, "output": result}, nil
}

func validatePath(workspace, target string, validate func(string) error) (any, *jsonrpc.Error) {
	if err := ensureWithin(workspace, target); err != nil {
		return nil, jsonrpc.InvalidParams(err.Error())
	}
	if err := validate(target); err != nil {
		return nil, jsonrpc.InternalError(err.Error())
	}
	return map[string]any{"ok": true, "path": target}, nil
}

func ensureWithin(workspace, target string) error {
	workspace = strings.TrimSpace(workspace)
	target = strings.TrimSpace(target)
	if workspace == "" || target == "" {
		return fmt.Errorf("workspace_root and target path are required")
	}
	workspaceAbs, err := filepath.Abs(workspace)
	if err != nil {
		return err
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(workspaceAbs, targetAbs)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("path must remain inside workspace_root")
	}
	return nil
}

func runGo(directory string, args ...string) (string, error) {
	goPath, err := exec.LookPath("go")
	if err != nil {
		return "未在 PATH 中找到 Go 工具链", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, goPath, args...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	text := string(output)
	if len(text) > 16000 {
		text = text[len(text)-16000:]
	}
	if ctx.Err() != nil {
		return text + "\n命令超时", ctx.Err()
	}
	return text, err
}

func firstLine(value string) string {
	for _, line := range strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			return strings.TrimSpace(line)
		}
	}
	return ""
}
