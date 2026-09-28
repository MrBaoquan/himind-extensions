package packagedigest

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

// 归档里只有载荷算数：打包账本、签名与安装期状态是「制品怎么被包装的」，
// 换个来源打包就会变，不能进内容摘要。
func TestPayloadDigestIgnoresPackagingAndInstallMetadata(t *testing.T) {
	withMetadata := writeArchive(t, map[string]string{
		"plugin.json":       "{\"id\":\"demo\"}",
		"bin/demo.exe":      "binary",
		"checksums.sha256":  "deadbeef  plugin.json",
		"manifest.sig":      "signature",
		"policy.json":       "{\"source\":\"local\"}",
		"main.go":           "package main",
		"dist/bundle.js":    "generated",
		"ui/node_modules/a": "dependency",
	})
	payloadOnly := writeArchive(t, map[string]string{
		"plugin.json":  "{\"id\":\"demo\"}",
		"bin/demo.exe": "binary",
	})

	actual, err := PayloadDigestOfArchive(withMetadata)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := PayloadDigestOfArchive(payloadOnly)
	if err != nil {
		t.Fatal(err)
	}
	if actual != expected {
		t.Fatalf("packaging metadata leaked into the payload digest: %s != %s", actual, expected)
	}
}

// 摘要的字节形式是跨机器契约（Agent 侧 Rust 实现必须与这里一致），固定一份
// 样本，改口径就会在这里断掉。
func TestPayloadDigestIsAStableContract(t *testing.T) {
	archive := writeArchive(t, map[string]string{
		"plugin.json":       "{\"id\":\"demo\"}",
		"bin/demo.exe":      "binary",
		"checksums.sha256":  "deadbeef  plugin.json",
		"ui/panel/index.js": "console.log(1)",
	})
	actual, err := PayloadDigestOfArchive(archive)
	if err != nil {
		t.Fatal(err)
	}
	// 等价于「bin/demo.exe\0binary\0plugin.json\0{\"id\":\"demo\"}\0ui/panel/index.js\0console.log(1)\0」
	// 路径按“/”分隔排序，与 Agent 侧 Rust 实现逐字节一致。
	const expected = "333bba120c5186d8647ec07a92aa5a43f6d65826f342be657d310fc9293e954a"
	if actual != expected {
		t.Fatalf("payload digest contract changed:\n got: %s\nwant: %s", actual, expected)
	}
}

// 跨仓验证：指向一个真实制品时，摘要必须与 Agent 从该制品安装后算出的值一致。
// 默认跳过，只在显式给出路径时运行，避免测试依赖网络或本机 dist。
func TestPayloadDigestOfRealArtifact(t *testing.T) {
	path := os.Getenv("HIMIND_PAYLOAD_DIGEST_ARTIFACT")
	if path == "" {
		t.Skip("set HIMIND_PAYLOAD_DIGEST_ARTIFACT to an .hmpkg/.hmskill to probe")
	}
	digest, err := PayloadDigestOfArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s payload digest: %s", filepath.Base(path), digest)
}

func writeArchive(t *testing.T, files map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "demo.hmpkg")
	handle, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(handle)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	for _, name := range names {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(files[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := handle.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
