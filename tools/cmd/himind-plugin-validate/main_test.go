package validate

import (
	"archive/zip"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseManifestAcceptsCurrentPluginShape(t *testing.T) {
	data := []byte(`{
		"id":"com.himind.example.tool",
		"name":"Example Tool",
		"author":"Test User",
		"categories":["software-engineering"],
		"description":"Example plugin.",
		"release_notes":"Initial release.",
		"version":"1.0.0",
		"entry":"example.exe",
		"runtime":"process-jsonrpc-stdio",
		"min_agent_version":"0.2.0",
		"governance":"optional",
		"capabilities":[{"id":"tool.run","input_schema":{"type":"object"},"risk_level":"read_only"}],
		"permissions":[]
	}`)
	item, err := ParseManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(item.Platforms) != 1 || item.Platforms[0] != "windows-x64" {
		t.Fatalf("unexpected default platforms: %#v", item.Platforms)
	}
}

func TestParseManifestAcceptsCanonicalRiskLevels(t *testing.T) {
	for _, riskLevel := range []string{
		"read_only",
		"local_action",
		"local_write",
		"process",
		"network",
		"network_write",
		"system",
		"admin_action",
		"builtin_policy",
		"R1",
		"R2",
		"R3",
		"R4",
	} {
		t.Run(riskLevel, func(t *testing.T) {
			data := []byte(strings.ReplaceAll(`{
				"id":"com.himind.example.tool",
				"name":"Example Tool",
				"author":"Test User",
				"categories":["software-engineering"],
				"description":"Example plugin.",
				"release_notes":"Initial release.",
				"version":"1.0.0",
				"entry":"example.exe",
				"runtime":"process-jsonrpc-stdio",
				"governance":"optional",
				"capabilities":[{"id":"tool.run","input_schema":{"type":"object"},"risk_level":"__RISK_LEVEL__"}],
				"permissions":[]
			}`, "__RISK_LEVEL__", riskLevel))
			if _, err := ParseManifest(data); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestParseManifestRejectsUnknownRiskLevels(t *testing.T) {
	for _, riskLevel := range []string{"unknown"} {
		t.Run(riskLevel, func(t *testing.T) {
			data := []byte(strings.ReplaceAll(`{
				"id":"com.himind.example.tool",
				"name":"Example Tool",
				"author":"Test User",
				"categories":["software-engineering"],
				"description":"Example plugin.",
				"release_notes":"Initial release.",
				"version":"1.0.0",
				"entry":"example.exe",
				"runtime":"process-jsonrpc-stdio",
				"governance":"optional",
				"capabilities":[{"id":"tool.run","input_schema":{"type":"object"},"risk_level":"__RISK_LEVEL__"}],
				"permissions":[]
			}`, "__RISK_LEVEL__", riskLevel))
			if _, err := ParseManifest(data); err == nil {
				t.Fatalf("expected risk level %q to be rejected", riskLevel)
			}
		})
	}
}

func TestParseManifestRejectsUnsafeEntry(t *testing.T) {
	data := []byte(`{
		"id":"com.himind.example.tool",
		"name":"Example Tool",
		"version":"1.0.0",
		"entry":"../example.exe",
		"runtime":"process-jsonrpc-stdio",
		"governance":"optional"
	}`)
	if _, err := ParseManifest(data); err == nil {
		t.Fatal("expected unsafe entry to be rejected")
	}
}

func TestValidateArchiveRejectsTraversal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unsafe.hmpkg")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	entry, err := writer.Create("../outside.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("unsafe")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ValidateArchive(path); err == nil {
		t.Fatal("expected traversal archive to be rejected")
	}
}

func TestValidateArchiveAcceptsCompleteChecksumsAndRejectsTampering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "valid.hmpkg")
	manifest := []byte(`{"id":"com.himind.example.tool","name":"Example","author":"Test User","categories":["software-engineering"],"description":"Example plugin.","release_notes":"Initial release.","version":"1.0.0","entry":"bin/tool.exe","runtime":"process-jsonrpc-stdio","platforms":["windows-x64"],"governance":"optional"}`)
	entry := []byte("binary")
	writeChecksumArchive(t, path, manifest, entry, entry)
	if err := ValidateArchive(path); err != nil {
		t.Fatal(err)
	}
	writeChecksumArchive(t, path, manifest, entry, []byte("tampered"))
	if err := ValidateArchive(path); err == nil {
		t.Fatal("expected tampered archive to be rejected")
	}
}

func writeChecksumArchive(t *testing.T, path string, manifest, checksumEntry, archiveEntry []byte) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for name, content := range map[string][]byte{"plugin.json": manifest, "bin/tool.exe": archiveEntry} {
		item, createErr := writer.Create(name)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, writeErr := item.Write(content); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	manifestHash := sha256.Sum256(manifest)
	entryHash := sha256.Sum256(checksumEntry)
	checksums, err := writer.Create("checksums.sha256")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fmt.Fprintf(checksums, "%x  plugin.json\n%x  bin/tool.exe\n", manifestHash, entryHash)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}


// 显示名禁用词：长度校验拦不住（「智能」只有 2 字），必须单独有门。
// 实测：给 media-ai 起名「媒体智能」时，长度、结构两道门全过。
func TestParseManifestRejectsBannedDisplayName(t *testing.T) {
	for _, name := range []string{"媒体智能", "一键发布工具", "全能工具箱", "极致管家"} {
		data := []byte(strings.ReplaceAll(`{
			"id":"com.himind.example.tool",
			"name":"__NAME__",
			"author":"Test User",
			"categories":["software-engineering"],
			"description":"Example plugin.",
			"release_notes":"Initial release.",
			"version":"1.0.0",
			"entry":"example.exe",
			"runtime":"process-jsonrpc-stdio",
			"governance":"optional",
			"capabilities":[{"id":"tool.run","input_schema":{"type":"object"},"risk_level":"read_only"}]
		}`, "__NAME__", name))
		if _, err := ParseManifest(data); err == nil {
			t.Errorf("显示名 %q 含禁用词，应被拒绝", name)
		}
	}
}

// 多段前缀未声明命名空间 —— 内核按先到者胜注册，冲突时后装的能力会被静默跳过。
func TestParseManifestRequiresNamespaceForNestedPrefix(t *testing.T) {
	build := func(namespace string) []byte {
		return []byte(strings.ReplaceAll(`{
			"id":"com.himind.media-toolkit",
			"name":"媒体处理工具",
			"author":"Test User",
			"categories":["video-post"],
			"description":"Example plugin.",
			"release_notes":"Initial release.",
			"version":"1.0.0",
			"entry":"example.exe",
			"runtime":"process-jsonrpc-stdio",
			"governance":"optional",
			__NS__
			"capabilities":[{"id":"media.toolkit.probe","input_schema":{"type":"object"},"risk_level":"read_only"}]
		}`, "__NS__", namespace))
	}
	if _, err := ParseManifest(build("")); err == nil {
		t.Error("media.toolkit.* 未声明 capability_namespace 应被拒绝")
	}
	if _, err := ParseManifest(build(`"capability_namespace":"media.toolkit",`)); err != nil {
		t.Errorf("已正确声明应通过: %v", err)
	}
	if _, err := ParseManifest(build(`"capability_namespace":"media.other",`)); err == nil {
		t.Error("声明未覆盖全部能力应被拒绝")
	}
}

// 首段是插件自身 slug 时无需声明（最常见的形态）。
func TestParseManifestAcceptsSlugNamespaceWithoutDeclaration(t *testing.T) {
	data := []byte(`{
		"id":"com.himind.iostudio",
		"name":"IOStudio 工具",
		"author":"Test User",
		"categories":["system-device"],
		"description":"Example plugin.",
		"release_notes":"Initial release.",
		"version":"1.0.0",
		"entry":"example.exe",
		"runtime":"process-jsonrpc-stdio",
		"governance":"optional",
		"capabilities":[{"id":"iostudio.devices","input_schema":{"type":"object"},"risk_level":"read_only"}]
	}`)
	if _, err := ParseManifest(data); err != nil {
		t.Errorf("首段即 slug 应免声明: %v", err)
	}
}
