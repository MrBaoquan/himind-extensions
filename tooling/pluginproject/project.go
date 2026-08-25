package pluginproject

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Config struct {
	Name         string
	DisplayName  string
	Description  string
	Author       string
	Categories   []string
	ReleaseNotes string
	Template     string
	OutputDir    string
}

type Result struct {
	Root       string `json:"root"`
	PluginID   string `json:"plugin_id"`
	Capability string `json:"capability_id"`
}

type Manifest struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Author       string        `json:"author"`
	Categories   []string      `json:"categories"`
	Description  string        `json:"description"`
	ReleaseNotes string        `json:"release_notes"`
	Version      string        `json:"version"`
	Entry        string        `json:"entry"`
	Runtime      string        `json:"runtime"`
	Platforms    []string      `json:"platforms"`
	MinAgent     string        `json:"min_agent_version"`
	Governance   string        `json:"governance"`
	Capabilities []Capability  `json:"capabilities"`
	Permissions  []string      `json:"permissions"`
	Contributes  Contributions `json:"contributes"`
}

type Capability struct {
	ID          string         `json:"id"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
	RiskLevel   string         `json:"risk_level"`
	Availability string        `json:"availability"`
}

type Contributions struct {
	Commands []Command `json:"commands"`
	Views    []View    `json:"views"`
}

type View struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Location string `json:"location"`
	Entry    string `json:"entry"`
}

type Command struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

var namePattern = regexp.MustCompile(`^[a-z0-9]+(?:[-_][a-z0-9]+)*$`)

func Create(config Config) (Result, error) {
	name := strings.TrimSpace(config.Name)
	displayName := strings.TrimSpace(config.DisplayName)
	template := strings.TrimSpace(config.Template)
	if !namePattern.MatchString(name) {
		return Result{}, fmt.Errorf("name must use lowercase words separated by '-' or '_'")
	}
	if template == "" {
		template = "readonly-tool"
	}
	if template != "readonly-tool" && template != "job-worker" && template != "ui-tool" {
		return Result{}, fmt.Errorf("unsupported template %q; use readonly-tool, job-worker or ui-tool", template)
	}
	if displayName == "" {
		return Result{}, fmt.Errorf("display-name is required; use a concise Chinese user-facing name")
	}
	if strings.TrimSpace(config.Author) == "" {
		return Result{}, fmt.Errorf("author is required; use the current Agent authorized user")
	}
	if strings.TrimSpace(config.Description) == "" {
		return Result{}, fmt.Errorf("description is required")
	}
	if strings.TrimSpace(config.ReleaseNotes) == "" {
		return Result{}, fmt.Errorf("release_notes is required")
	}
	if len(config.Categories) == 0 {
		return Result{}, fmt.Errorf("at least one functional category is required")
	}
	root := filepath.Join(config.OutputDir, name)
	if config.OutputDir == "" {
		root = name
	}
	if _, err := os.Stat(root); err == nil {
		return Result{}, fmt.Errorf("output directory already exists: %s", root)
	} else if !os.IsNotExist(err) {
		return Result{}, err
	}
	manifest := buildManifest(name, displayName, config.Description, config.Author, config.Categories, config.ReleaseNotes, template)
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Result{}, err
	}
	files := map[string][]byte{
		"plugin.json":                       data,
		"main.go":                           []byte(templateSource(name, template)),
		"main_test.go":                      []byte(templateTestSource(name, template)),
		"go.mod":                            []byte(moduleSource(name)),
		"internal/himindjsonrpc/jsonrpc.go": []byte(jsonRPCSource),
	}
	if template == "ui-tool" {
		files["ui/index.html"] = []byte(uiTemplate(displayName))
	}
	for relative, content := range files {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return Result{}, err
		}
		if err := os.WriteFile(path, append(content, '\n'), 0644); err != nil {
			return Result{}, err
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0755); err != nil {
		return Result{}, err
	}
	return Result{Root: root, PluginID: manifest.ID, Capability: manifest.Capabilities[0].ID}, nil
}

func buildManifest(name, displayName, description, author string, categories []string, releaseNotes, template string) Manifest {
	id := "com.himind." + strings.ReplaceAll(name, "_", "-")
	capabilityID := strings.ReplaceAll(name, "-", ".") + ".run"
	capability := Capability{ID: capabilityID, Description: "请用中文说明该插件能力的用途、输入和输出。", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}, "additionalProperties": false}, RiskLevel: "read_only", Availability: "local"}
	if template == "job-worker" {
		capability.Description = "请用中文说明该长任务能力的用途、进度和结果。"
		capability.RiskLevel = "local_write"
	}
	contributes := Contributions{Commands: []Command{{ID: capability.ID, Title: "运行" + displayName}}, Views: []View{}}
	if template == "ui-tool" {
		contributes.Views = []View{{ID: strings.ReplaceAll(name, "-", ".") + ".main", Title: displayName, Location: "tools", Entry: "ui/index.html"}}
	}
	return Manifest{ID: id, Name: displayName, Author: strings.TrimSpace(author), Categories: append([]string(nil), categories...), Description: strings.TrimSpace(description), ReleaseNotes: strings.TrimSpace(releaseNotes), Version: "0.1.0", Entry: "bin/" + name + ".exe", Runtime: "process-jsonrpc-stdio", Platforms: []string{"windows-x64"}, MinAgent: "0.3.0", Governance: "optional", Capabilities: []Capability{capability}, Permissions: []string{}, Contributes: contributes}
}

func uiTemplate(displayName string) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="zh-CN">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>%s</title><style>body{font:14px system-ui;margin:0;padding:24px;color:#1f2937;background:#f7f8fa}main{max-width:720px;margin:auto}h1{font-size:22px}button{padding:8px 14px}</style></head>
<body><main><h1>%s</h1><p>请在此实现可独立使用的工具界面。</p><button type="button">开始</button></main></body>
</html>`, displayName, displayName)
}

func moduleSource(name string) string {
	return fmt.Sprintf("module himind-plugin/%s\n\ngo 1.22\n", name)
}

func templateSource(name, template string) string {
	result := fmt.Sprintf(`package main

import (
    "os"
    "himind-plugin/%s/internal/himindjsonrpc"
)

type input struct {
    Value string `+"`json:\"value\"`"+`
}

func main() {
    _ = himindjsonrpc.Serve(os.Stdin, os.Stdout, handle)
}

func handle(request himindjsonrpc.Request) (any, *himindjsonrpc.Error) {
    var in input
    if rpcError := himindjsonrpc.DecodeParams(request, &in); rpcError != nil {
        return nil, rpcError
    }
    return map[string]any{"value": in.Value}, nil
}
`, name)
	if template == "job-worker" {
		result = strings.Replace(result, `return map[string]any{"value": in.Value}, nil`, `return map[string]any{"status": "completed", "value": in.Value}, nil`, 1)
	}
	return result
}

func templateTestSource(name, template string) string {
	expectedStatus := ""
	if template == "job-worker" {
		expectedStatus = `
	if result["status"] != "completed" {
		t.Fatalf("unexpected status: %#v", result["status"])
	}`
	}
	return fmt.Sprintf(`package main

import (
	"testing"
	"himind-plugin/%s/internal/himindjsonrpc"
)

func TestHandleReturnsInputValue(t *testing.T) {
	resultValue, rpcError := handle(himindjsonrpc.Request{Params: []byte("{\"value\":\"hello\"}")})
	if rpcError != nil {
		t.Fatal(rpcError)
	}
	result := resultValue.(map[string]any)
	if result["value"] != "hello" {
		t.Fatalf("unexpected value: %%#v", result["value"])
	}%s
}
`, name, expectedStatus)
}

const jsonRPCSource = `package himindjsonrpc

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

type Request struct {
	JSONRPC string          ` + "`json:\"jsonrpc\"`" + `
	ID      json.RawMessage ` + "`json:\"id\"`" + `
	Method  string          ` + "`json:\"method\"`" + `
	Params  json.RawMessage ` + "`json:\"params,omitempty\"`" + `
}

type Response struct {
	JSONRPC string          ` + "`json:\"jsonrpc\"`" + `
	ID      json.RawMessage ` + "`json:\"id\"`" + `
	Result  any             ` + "`json:\"result,omitempty\"`" + `
	Error   *Error          ` + "`json:\"error,omitempty\"`" + `
}

type Error struct {
	Code    int    ` + "`json:\"code\"`" + `
	Message string ` + "`json:\"message\"`" + `
}

type Handler func(Request) (any, *Error)

func Serve(r io.Reader, w io.Writer, handler Handler) error {
	if handler == nil { return errors.New("jsonrpc handler is required") }
	scanner := bufio.NewScanner(r)
	encoder := json.NewEncoder(w)
	for scanner.Scan() {
		var request Request
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			_ = encoder.Encode(Response{JSONRPC: "2.0", Error: &Error{Code: -32700, Message: "parse error"}})
			continue
		}
		result, rpcError := handler(request)
		if err := encoder.Encode(Response{JSONRPC: "2.0", ID: request.ID, Result: result, Error: rpcError}); err != nil { return err }
	}
	return scanner.Err()
}

func DecodeParams(request Request, target any) *Error {
	if target == nil { return &Error{Code: -32602, Message: "params target is required"} }
	if err := json.Unmarshal(request.Params, target); err != nil { return &Error{Code: -32602, Message: fmt.Sprintf("invalid params: %v", err)} }
	return nil
}
`
