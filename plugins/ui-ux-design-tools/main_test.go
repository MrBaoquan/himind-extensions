package main

import (
	"encoding/json"
	"testing"

	"himind-plugin/ui-ux-design-tools/internal/design"
	"himind-plugin/ui-ux-design-tools/internal/himindjsonrpc"
)

func TestCatalogDescribe(t *testing.T) {
	result, rpcError := handle(himindjsonrpc.Request{Method: "ui-ux.catalog.describe", Params: json.RawMessage(`{}`)})
	if rpcError != nil {
		t.Fatal(rpcError)
	}
	payload := result.(map[string]any)
	if payload["status"] != "ok" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestGuidelineSearch(t *testing.T) {
	result, rpcError := handle(himindjsonrpc.Request{Method: "ui-ux.guideline.search", Params: json.RawMessage(`{"query":"keyboard focus modal","domain":"ux","max_results":3}`)})
	if rpcError != nil {
		t.Fatal(rpcError)
	}
	payload := result.(design.SearchResult)
	if payload.Count == 0 {
		t.Fatalf("expected search matches: %#v", payload)
	}
}

func TestInvalidInput(t *testing.T) {
	_, rpcError := handle(himindjsonrpc.Request{Method: "ui-ux.design-system.generate", Params: json.RawMessage(`{"query":""}`)})
	if rpcError == nil {
		t.Fatal("expected validation error")
	}
}
