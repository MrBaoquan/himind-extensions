package main

import (
	"fmt"
	"os"
	"path/filepath"

	"himind-plugin/video-cover/internal/cover"
	"himind-plugin/video-cover/internal/himindjsonrpc"
)

const (
	capTemplateList = "media.cover.template.list"
	capLayoutRender = "media.cover.layout.render"
	capLayoutBatch  = "media.cover.layout.batch"
)

type batchInput struct {
	WorkspaceRoot string              `json:"workspace_root"`
	Items         []cover.RenderInput `json:"items"`
}

func main() { _ = himindjsonrpc.Serve(os.Stdin, os.Stdout, handle) }

func handle(request himindjsonrpc.Request) (any, *himindjsonrpc.Error) {
	switch request.Method {
	case capTemplateList:
		return map[string]any{"templates": cover.Templates()}, nil
	case capLayoutRender:
		var in cover.RenderInput
		if e := himindjsonrpc.DecodeParams(request, &in); e != nil {
			return nil, e
		}
		assets, err := loadAssets()
		if err != nil {
			return nil, &himindjsonrpc.Error{Code: -32000, Message: err.Error()}
		}
		result, err := cover.Render(assets, in)
		if err != nil {
			return nil, &himindjsonrpc.Error{Code: -32000, Message: err.Error()}
		}
		return result, nil
	case capLayoutBatch:
		var in batchInput
		if e := himindjsonrpc.DecodeParams(request, &in); e != nil {
			return nil, e
		}
		if len(in.Items) == 0 {
			return nil, &himindjsonrpc.Error{Code: -32602, Message: "items 不能为空"}
		}
		if len(in.Items) > 50 {
			return nil, &himindjsonrpc.Error{Code: -32602, Message: "单批最多 50 项"}
		}
		assets, err := loadAssets()
		if err != nil {
			return nil, &himindjsonrpc.Error{Code: -32000, Message: err.Error()}
		}
		results := make([]any, 0, len(in.Items))
		for i := range in.Items {
			if in.Items[i].WorkspaceRoot == "" {
				in.Items[i].WorkspaceRoot = in.WorkspaceRoot
			}
			result, err := cover.Render(assets, in.Items[i])
			if err != nil {
				results = append(results, map[string]any{"index": i, "ok": false, "error": err.Error()})
				continue
			}
			results = append(results, map[string]any{"index": i, "ok": true, "result": result})
		}
		return map[string]any{"items": results, "count": len(results)}, nil
	default:
		return nil, &himindjsonrpc.Error{Code: -32601, Message: fmt.Sprintf("unknown method: %s", request.Method)}
	}
}

func loadAssets() (cover.Assets, error) {
	root, err := assetRoot()
	if err != nil {
		return cover.Assets{}, err
	}
	serif, err := os.ReadFile(filepath.Join(root, "assets", "fonts", "NotoSerifSC-VF.ttf"))
	if err != nil {
		return cover.Assets{}, fmt.Errorf("读取字体失败：%w", err)
	}
	seal, err := os.ReadFile(filepath.Join(root, "assets", "seal-wanmei.png"))
	if err != nil {
		return cover.Assets{}, fmt.Errorf("读取印章失败：%w", err)
	}
	return cover.Assets{SerifFont: serif, SansFont: serif, SealPNG: seal}, nil
}

func assetRoot() (string, error) {
	candidates := []string{}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates, filepath.Clean(filepath.Join(dir, "..")), dir)
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, cwd)
	}
	for _, root := range candidates {
		if _, err := os.Stat(filepath.Join(root, "assets", "fonts", "NotoSerifSC-VF.ttf")); err == nil {
			return root, nil
		}
	}
	return "", fmt.Errorf("找不到插件资源目录")
}
