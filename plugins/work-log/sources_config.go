package main

import (
	"context"
	"errors"
	"github.com/MrBaoquan/himind-extensions/sdk/capability"
)

// Source configuration belongs to Agent; these entrypoints preserve the log UI contract.
func sourceListCap(in input) (any, error) {
	var v map[string]any
	err := capability.Invoke(context.Background(), "workspace.session.source.get", map[string]any{}, &v)
	return v, err
}
func sourceSaveCap(in input) (any, error) {
	var current map[string]any
	if err := capability.Invoke(context.Background(), "workspace.session.source.get", map[string]any{}, &current); err != nil {
		return nil, err
	}
	rows := []map[string]any{}
	if in.Mode != "replace" {
		if existing, ok := current["sources"].([]any); ok {
			for _, r := range existing {
				if m, ok := r.(map[string]any); ok {
					rows = append(rows, m)
				}
			}
		}
	}
	for _, spec := range in.Sources {
		id, _ := spec["id"].(string)
		if id == "" {
			return nil, errors.New("每个来源都需要 id")
		}
		found := false
		for i, r := range rows {
			if r["id"] == id {
				rows[i] = spec
				found = true
				break
			}
		}
		if !found {
			rows = append(rows, spec)
		}
	}
	request := map[string]any{"sources": rows}
	if roots, ok := current["native_roots"]; ok {
		request["native_roots"] = roots
	}
	var result map[string]any
	err := capability.Invoke(context.Background(), "workspace.session.source.configure", request, &result)
	return result, err
}
