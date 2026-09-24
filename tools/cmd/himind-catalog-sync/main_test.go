package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// source_tree 必须指向「扩展源码目录」的树，而不是提交的根树：发布侧用同一个
// 口径判断「Release 已存在但源码变了」，两者不一致会让索引失去比对价值。
func TestSourceTreeOfPicksSourceDirectory(t *testing.T) {
	const subtree = "96b2cdbff3f942fc63aae94594470ea8da2b7b28"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/repos/MrBaoquan/himind-extensions/git/trees/91f6f80" {
			t.Errorf("请求路径不符合预期: %s", request.URL.String())
		}
		if request.URL.Query().Get("recursive") != "1" {
			t.Errorf("递归树缺少 recursive=1: %s", request.URL.String())
		}
		payload := map[string]interface{}{
			"truncated": false,
			"tree": []map[string]string{
				{"path": "workflows", "type": "tree", "sha": "root-workflows"},
				{"path": "workflows/tech-radar", "type": "tree", "sha": subtree},
				{"path": "workflows/tech-radar/workflow.json", "type": "blob", "sha": "blob"},
			},
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(payload)
	}))
	defer server.Close()

	client := &client{http: server.Client(), base: server.URL}
	tree, err := client.sourceTreeOf("MrBaoquan/himind-extensions", "91f6f80", "workflows/tech-radar")
	if err != nil {
		t.Fatalf("sourceTreeOf 失败: %v", err)
	}
	if tree != subtree {
		t.Fatalf("source_tree = %s，期望 %s", tree, subtree)
	}
}

func TestSourceTreeOfRejectsMissingDirectory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{
			"truncated": false,
			"tree":      []map[string]string{{"path": "workflows", "type": "tree", "sha": "root"}},
		})
	}))
	defer server.Close()

	client := &client{http: server.Client(), base: server.URL}
	_, err := client.sourceTreeOf("MrBaoquan/himind-extensions", "91f6f80", "workflows/tech-radar")
	if err == nil || !strings.Contains(err.Error(), "找不到源码目录") {
		t.Fatalf("期望找不到目录的错误，实际: %v", err)
	}
}
