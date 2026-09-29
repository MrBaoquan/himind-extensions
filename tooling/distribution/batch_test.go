package distribution

import (
	"strings"
	"testing"
)

// 64 位十六进制摘要，测试里只用于占位，不校验内容。
const batchTestDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func batchTestMember(kind, id, version string) BatchMember {
	return BatchMember{
		Kind:    kind,
		ID:      id,
		Version: version,
		// 故意留空，由调用方按需覆盖成规范名。
		Artifact: ReleaseArtifact{SizeBytes: 1024, SHA256: batchTestDigest},
	}
}

func batchTestManifest() BatchManifest {
	member := batchTestMember("skill", "com.himind.skill.demo", "2.2.3")
	member.Artifact.Name = "com.himind.skill.demo-2.2.3.hmskill"
	member.Manifest = "com.himind.skill.demo@2.2.3.json"
	return BatchManifest{
		SchemaVersion: BatchManifestSchema,
		Repository:    "MrBaoquan/himind-extensions-ecc",
		Tag:           "batch/2.2.3",
		Version:       "2.2.3",
		Channel:       "stable",
		SourceCommit:  "e4ffaa1e4ffaa1e4ffaa1e4ffaa1e4ffaa1e4ffaa",
		GeneratedAt:   "2026-09-29T01:00:00Z",
		Upstream: BatchUpstream{
			Repository: "affaan-m/ECC",
			Version:    "2.2.2",
			Commit:     "d3b8a3e908904e242ed2dbe66af62cca71131419",
		},
		Members: []BatchMember{member},
	}
}

func TestBatchManifestAcceptsWellFormedBatch(t *testing.T) {
	value := batchTestManifest()
	if err := value.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if value.ManifestName() != "batch@2.2.3.json" {
		t.Fatalf("批次清单名 = %q", value.ManifestName())
	}
}

func TestBatchManifestRequiresUpstreamProvenance(t *testing.T) {
	// 批次 tag 不带上游版本，清单里再不留，这批就说不清是搬自哪里。
	for index, strip := range []func(*BatchManifest){
		func(m *BatchManifest) { m.Upstream.Version = "" },
		func(m *BatchManifest) { m.Upstream.Commit = "" },
		func(m *BatchManifest) { m.SourceCommit = "" },
		func(m *BatchManifest) { m.Repository = "" },
		func(m *BatchManifest) { m.Members = nil },
	} {
		value := batchTestManifest()
		strip(&value)
		if err := value.Validate(); err == nil {
			t.Fatalf("缺字段的批次清单应被拒绝（第 %d 个用例）", index)
		}
	}
}

func TestBatchManifestRejectsMismatchedTagAndVersion(t *testing.T) {
	value := batchTestManifest()
	value.Version = "2.2.4"
	if err := value.Validate(); err == nil {
		t.Fatal("tag 与 version 不一致应被拒绝")
	}
	value = batchTestManifest()
	value.Tag = "skill/com.himind.skill.demo@2.2.3"
	if err := value.Validate(); err == nil {
		t.Fatal("扩展 tag 不该被当成批次 tag")
	}
}

func TestBatchMemberRejectsWrongArtifactOrManifestName(t *testing.T) {
	value := batchTestManifest()
	value.Members[0].Artifact.Name = "com.himind.skill.demo-2.2.2.hmskill"
	if err := value.Validate(); err == nil {
		t.Fatal("制品名与 id/版本不一致应被拒绝")
	}
	value = batchTestManifest()
	value.Members[0].Manifest = "com.himind.skill.demo@2.2.2.json"
	if err := value.Validate(); err == nil {
		t.Fatal("发布清单名与 id/版本不一致应被拒绝")
	}
	value = batchTestManifest()
	value.Members[0].Artifact.SHA256 = "not-a-digest"
	if err := value.Validate(); err == nil {
		t.Fatal("摘要不是 64 位十六进制应被拒绝")
	}
	value = batchTestManifest()
	value.Members[0].Artifact.SizeBytes = 0
	if err := value.Validate(); err == nil {
		t.Fatal("零字节制品应被拒绝")
	}
}

func TestBatchManifestRejectsDuplicateMember(t *testing.T) {
	value := batchTestManifest()
	duplicate := value.Members[0]
	duplicate.Version = "2.2.2"
	duplicate.Artifact.Name = "com.himind.skill.demo-2.2.2.hmskill"
	duplicate.Manifest = "com.himind.skill.demo@2.2.2.json"
	value.Members = append(value.Members, duplicate)
	err := value.Validate()
	if err == nil {
		t.Fatal("同一成员的另一个版本不该出现在同一批里")
	}
	if !strings.Contains(err.Error(), "出现了两次") {
		t.Fatalf("错误信息应指出重复: %v", err)
	}
}

func TestReleaseManifestAcceptsBatchTag(t *testing.T) {
	// 批次发布时成员清单与所在 Release 同一个 tag，Validate 必须认这个形态；
	// 成员身份由 kind/id/version 与规范制品名互相印证。
	member := ReleaseManifest{
		SchemaVersion: ReleaseManifestSchema,
		Repository:    "MrBaoquan/himind-extensions-ecc",
		Tag:           "batch/2.2.3",
		Kind:          "skill",
		ID:            "com.himind.skill.demo",
		Version:       "2.2.3",
		Channel:       "stable",
		Artifact: ReleaseArtifact{
			Name:      "com.himind.skill.demo-2.2.3.hmskill",
			SizeBytes: 1024,
			SHA256:    batchTestDigest,
		},
	}
	if err := member.Validate(); err != nil {
		t.Fatalf("批次 tag 的成员清单应通过: %v", err)
	}
	member.Artifact.Name = "com.himind.skill.demo-2.2.2.hmskill"
	if err := member.Validate(); err == nil {
		t.Fatal("制品名与 kind/id/version 不一致时，批次 tag 不该让校验通过")
	}
	member.Artifact.Name = "com.himind.skill.demo-2.2.3.hmskill"
	member.Kind = "unknown"
	if err := member.Validate(); err == nil {
		t.Fatal("批次 tag 下未受支持的类型应被拒绝")
	}
}
