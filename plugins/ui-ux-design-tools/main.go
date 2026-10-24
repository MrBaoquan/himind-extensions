package main

import (
	"embed"
	"os"
	"strings"
	"sync"

	"himind-plugin/ui-ux-design-tools/internal/design"
	"himind-plugin/ui-ux-design-tools/internal/himindjsonrpc"
)

//go:embed data/*.csv data/*.json data/stacks/*.csv
var embeddedData embed.FS

var (
	catalogOnce sync.Once
	catalog     *design.Catalog
	catalogErr  error
)

type searchInput struct {
	Query      string `json:"query"`
	Domain     string `json:"domain"`
	Stack      string `json:"stack"`
	MaxResults int    `json:"max_results"`
}

type inspectInput struct {
	WorkspaceRoot string `json:"workspace_root"`
	MaxDepth      int    `json:"max_depth"`
	MaxFiles      int    `json:"max_files"`
}

type persistInput struct {
	WorkspaceRoot string `json:"workspace_root"`
	ProjectSlug   string `json:"project_slug"`
	PageSlug      string `json:"page_slug"`
	Content       string `json:"content"`
	Overwrite     bool   `json:"overwrite"`
}

type tokenValidateInput struct {
	WorkspaceRoot string   `json:"workspace_root"`
	Paths         []string `json:"paths"`
	MaxFiles      int      `json:"max_files"`
}

type generateInput struct {
	Query       string `json:"query"`
	ProjectName string `json:"project_name"`
	Variance    int    `json:"variance"`
	Motion      int    `json:"motion"`
	Density     int    `json:"density"`
}

func main() {
	_ = himindjsonrpc.Serve(os.Stdin, os.Stdout, handle)
}

func handle(request himindjsonrpc.Request) (any, *himindjsonrpc.Error) {
	c, err := getCatalog()
	if err != nil {
		return nil, &himindjsonrpc.Error{Code: -32000, Message: "加载设计数据失败：" + err.Error()}
	}
	switch request.Method {
	case "ui-ux.catalog.describe":
		return c.Describe(), nil
	case "ui-ux.guideline.search":
		var in searchInput
		if rpcError := himindjsonrpc.DecodeParams(request, &in); rpcError != nil {
			return nil, rpcError
		}
		result, err := c.Search(strings.TrimSpace(in.Query), strings.TrimSpace(in.Domain), strings.TrimSpace(in.Stack), in.MaxResults)
		if err != nil {
			return nil, &himindjsonrpc.Error{Code: -32602, Message: err.Error()}
		}
		return result, nil
	case "ui-ux.project.inspect":
		var in inspectInput
		if rpcError := himindjsonrpc.DecodeParams(request, &in); rpcError != nil {
			return nil, rpcError
		}
		result, err := c.InspectProject(design.ProjectInspectInput{WorkspaceRoot: in.WorkspaceRoot, MaxDepth: in.MaxDepth, MaxFiles: in.MaxFiles})
		if err != nil {
			return nil, &himindjsonrpc.Error{Code: -32602, Message: err.Error()}
		}
		return result, nil
	case "ui-ux.design-system.persist":
		var in persistInput
		if rpcError := himindjsonrpc.DecodeParams(request, &in); rpcError != nil {
			return nil, rpcError
		}
		result, err := c.PersistDesignSystem(design.PersistInput{WorkspaceRoot: in.WorkspaceRoot, ProjectSlug: in.ProjectSlug, PageSlug: in.PageSlug, Content: in.Content, Overwrite: in.Overwrite})
		if err != nil {
			return nil, &himindjsonrpc.Error{Code: -32602, Message: err.Error()}
		}
		return result, nil
	case "ui-ux.token.validate":
		var in tokenValidateInput
		if rpcError := himindjsonrpc.DecodeParams(request, &in); rpcError != nil {
			return nil, rpcError
		}
		result, err := c.ValidateTokens(design.TokenValidateInput{WorkspaceRoot: in.WorkspaceRoot, Paths: in.Paths, MaxFiles: in.MaxFiles})
		if err != nil {
			return nil, &himindjsonrpc.Error{Code: -32602, Message: err.Error()}
		}
		return result, nil
	case "ui-ux.design-system.generate":
		var in generateInput
		if rpcError := himindjsonrpc.DecodeParams(request, &in); rpcError != nil {
			return nil, rpcError
		}
		result, err := c.Generate(design.GenerateInput{Query: in.Query, ProjectName: in.ProjectName, Variance: in.Variance, Motion: in.Motion, Density: in.Density})
		if err != nil {
			return nil, &himindjsonrpc.Error{Code: -32602, Message: err.Error()}
		}
		return result, nil
	default:
		return nil, &himindjsonrpc.Error{Code: -32602, Message: "不支持的界面设计工具能力"}
	}
}

func getCatalog() (*design.Catalog, error) {
	catalogOnce.Do(func() {
		catalog = design.NewCatalog(embeddedData)
		_, catalogErr = catalog.Rows("product")
	})
	return catalog, catalogErr
}
