package main

import (
	"os"
	"testing"
)

func TestLineageAppendAndQueryByFields(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	parent := Lineage{LineageID: "l1", MediaID: "media_parent", Path: `C:\work\a.png`, SHA256: "AAA", Capability: capImageGenerate, CreatedAt: nowText()}
	child := Lineage{LineageID: "l2", MediaID: "media_child", Path: `C:\work\b.png`, SHA256: "BBB", ParentMediaID: "media_parent", Capability: capImageEdit, CreatedAt: nowText()}
	if err := appendLineage([]Lineage{parent, child}); err != nil {
		t.Fatalf("写入血缘失败: %v", err)
	}

	byID, _, err := queryLineage(lineageQuery{MediaID: "media_child"})
	if err != nil {
		t.Fatalf("按 media_id 查询失败: %v", err)
	}
	if len(byID) != 1 || byID[0].LineageID != "l2" {
		t.Fatalf("按 media_id 查询结果不对: %+v", byID)
	}

	byPath, _, err := queryLineage(lineageQuery{Path: `C:\work\a.png`})
	if err != nil || len(byPath) != 1 || byPath[0].MediaID != "media_parent" {
		t.Fatalf("按路径查询结果不对: %+v %v", byPath, err)
	}

	bySHA, _, err := queryLineage(lineageQuery{SHA256: "bbb"})
	if err != nil || len(bySHA) != 1 {
		t.Fatalf("SHA 查询应忽略大小写: %+v %v", bySHA, err)
	}
}

func TestLineageChainWalksParents(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	records := []Lineage{
		{LineageID: "l1", MediaID: "media_a", CreatedAt: nowText()},
		{LineageID: "l2", MediaID: "media_b", ParentMediaID: "media_a", CreatedAt: nowText()},
		{LineageID: "l3", MediaID: "media_c", ParentMediaID: "media_b", CreatedAt: nowText()},
	}
	if err := appendLineage(records); err != nil {
		t.Fatalf("写入血缘失败: %v", err)
	}
	matches, chain, err := queryLineage(lineageQuery{MediaID: "media_c", Chain: true})
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if len(matches) != 1 || matches[0].MediaID != "media_c" {
		t.Fatalf("命中记录不对: %+v", matches)
	}
	if len(chain) != 2 || chain[0].MediaID != "media_b" || chain[1].MediaID != "media_a" {
		t.Fatalf("祖先链顺序不对: %+v", chain)
	}
}

func TestLineageQueryToleratesBrokenLines(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", root)
	path, err := lineagePath()
	if err != nil {
		t.Fatalf("解析路径失败: %v", err)
	}
	content := "{\"lineage_id\":\"l1\",\"media_id\":\"m1\"}\n{broken json\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入测试文件失败: %v", err)
	}
	matches, _, err := queryLineage(lineageQuery{MediaID: "m1"})
	if err != nil {
		t.Fatalf("单行损坏不应让查询失败: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("应跳过损坏行并返回有效记录: %+v", matches)
	}
}
