package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	catalogtool "github.com/MrBaoquan/himind-extensions/tooling/catalog"
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

// 能力包记录的是「本仓托管了这些扩展」，只能来自本仓清单。历史实现把官方仓的
// 能力包硬编码进索引构建，任何扩展仓全量重建都会被注入一次，消费侧合并多个来源
// 时就会把同 id 的包判成来源冲突。
func TestSyncUsesFeaturePacksFromRepositoryManifest(t *testing.T) {
	pack := catalogtool.FeaturePack{
		ID:        "com.himind.feature.demo",
		Name:      "演示能力包",
		PluginIDs: []string{"com.himind.plugin.demo"},
		SkillIDs:  []string{"com.himind.skill.demo"},
	}
	synced := runSync(t, []catalogtool.FeaturePack{pack})
	if len(synced.FeaturePacks) != 1 || synced.FeaturePacks[0].ID != pack.ID {
		t.Fatalf("索引能力包 = %+v，期望只有本仓声明的 %s", synced.FeaturePacks, pack.ID)
	}
}

func TestSyncLeavesFeaturePacksEmptyWhenManifestDeclaresNone(t *testing.T) {
	synced := runSync(t, nil)
	if len(synced.FeaturePacks) != 0 {
		t.Fatalf("未声明能力包的仓不应被注入默认能力包，实际: %+v", synced.FeaturePacks)
	}
}

// runSync 起一个假的 GitHub API，跑一次完整索引重建，返回落盘的索引。
func runSync(t *testing.T, declared []catalogtool.FeaturePack) catalogtool.Catalog {
	t.Helper()
	const (
		repository = "MrBaoquan/demo-ext"
		id         = "com.himind.workflow.demo"
		version    = "1.0.0"
		tag        = "workflow/com.himind.workflow.demo@1.0.0"
		commit     = "abc1234"
		artifact   = "com.himind.workflow.demo-1.0.0.hmwf"
		lock       = "com.himind.workflow.demo-1.0.0.extension-lock.json"
		manifest   = "com.himind.workflow.demo@1.0.0.json"
	)
	releaseManifest, err := json.Marshal(map[string]interface{}{
		"schema_version": "himind_extension_release.v1",
		"repository":     repository,
		"tag":            tag,
		"kind":           "workflow",
		"id":             id,
		"version":        version,
		"channel":        "stable",
		"source_commit":  commit,
		"artifact": map[string]interface{}{
			"name": artifact, "size_bytes": 1024, "sha256": strings.Repeat("a", 64),
		},
	})
	if err != nil {
		t.Fatalf("构造发布清单失败: %v", err)
	}
	sourceManifest, err := json.Marshal(map[string]interface{}{
		"id": id, "name": "演示工作流", "version": version, "author": "HiMind",
		"categories": []string{"demo"}, "description": "演示", "release_notes": "首次发布",
	})
	if err != nil {
		t.Fatalf("构造源码清单失败: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(request.URL.Path, "/releases"):
			_ = json.NewEncoder(writer).Encode([]map[string]interface{}{{
				"tag_name": tag, "draft": false, "published_at": "2026-09-28T00:00:00Z",
				"assets": []map[string]interface{}{
					{"name": manifest, "size": len(releaseManifest), "url": "http://" + request.Host + "/assets/manifest"},
					{"name": lock, "size": 2, "url": "http://" + request.Host + "/assets/lock"},
				},
			}})
		case request.URL.Path == "/assets/manifest":
			_, _ = writer.Write(releaseManifest)
		case request.URL.Path == "/assets/lock":
			_, _ = writer.Write([]byte("{}"))
		case strings.Contains(request.URL.Path, "/contents/"):
			_ = json.NewEncoder(writer).Encode(map[string]string{
				"encoding": "base64", "content": base64.StdEncoding.EncodeToString(sourceManifest),
			})
		case strings.Contains(request.URL.Path, "/git/trees/"):
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"truncated": false,
				"tree":      []map[string]string{{"path": "workflows/demo", "type": "tree", "sha": "tree-sha"}},
			})
		default:
			t.Errorf("未预期的 API 请求: %s", request.URL.String())
		}
	}))
	defer server.Close()

	config := map[string]interface{}{
		"schema_version":  1,
		"distribution_id": "mrbaoquan/demo-ext",
		"channel":         "stable",
		"catalog_id":      "public",
		"extensions": []map[string]string{
			{"type": "workflow", "id": id, "path": "workflows/demo"},
		},
	}
	if declared != nil {
		config["feature_packs"] = declared
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("构造仓库清单失败: %v", err)
	}
	root := t.TempDir()
	extensionsPath := filepath.Join(root, "extensions.json")
	if err := os.WriteFile(extensionsPath, data, 0o644); err != nil {
		t.Fatalf("写仓库清单失败: %v", err)
	}
	catalogPath := filepath.Join(root, ".himind", "catalog.json")
	if err := os.MkdirAll(filepath.Dir(catalogPath), 0o755); err != nil {
		t.Fatalf("建索引目录失败: %v", err)
	}
	if _, err := sync(repository, catalogPath, extensionsPath, "test-token", server.URL, false); err != nil {
		t.Fatalf("索引重建失败: %v", err)
	}
	synced, err := catalogtool.Load(catalogPath)
	if err != nil {
		t.Fatalf("读取重建结果失败: %v", err)
	}
	return *synced
}
