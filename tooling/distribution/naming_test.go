package distribution

import (
	"strings"
	"testing"
)

func TestReleaseTagAndParseRoundTrip(t *testing.T) {
	tag, err := ReleaseTag("plugin", "com.himind.software-distribution", "1.2.1")
	if err != nil {
		t.Fatalf("ReleaseTag: %v", err)
	}
	if tag != "plugin/com.himind.software-distribution@1.2.1" {
		t.Fatalf("tag = %q", tag)
	}
	kind, id, version, err := ParseReleaseTag(tag)
	if err != nil {
		t.Fatalf("ParseReleaseTag: %v", err)
	}
	if kind != "plugin" || id != "com.himind.software-distribution" || version != "1.2.1" {
		t.Fatalf("parsed = %q %q %q", kind, id, version)
	}
}

func TestParseReleaseTagRejectsLegacyAndMalformed(t *testing.T) {
	// 历史扁平前缀在门禁里必须直接失败，否则两套命名会同时存在。
	legacy := "plugin-com.himind.image-optimizer-v1.0.2"
	if _, _, _, err := ParseReleaseTag(legacy); err == nil {
		t.Fatalf("legacy tag %q should be rejected", legacy)
	}
	for _, value := range []string{"", "plugin", "plugin/", "plugin/@1.0.0", "plugin/x@", "gitlab/x@1.0.0", "plugin/x@@1.0.0"} {
		if _, _, _, err := ParseReleaseTag(value); err == nil {
			t.Fatalf("tag %q should be rejected", value)
		}
	}
}

func TestReleaseTagRejectsUnsafeParts(t *testing.T) {
	if _, err := ReleaseTag("plugin", "com.himind.x y", "1.0.0"); err == nil {
		t.Fatal("空格应被拒绝")
	}
	if _, err := ReleaseTag("plugin", "com.himind.x", "1.0.0.."); err == nil {
		t.Fatal("连续点应被拒绝")
	}
	if _, err := ReleaseTag("plugin", " com.himind.x", "1.0.0"); err == nil {
		t.Fatal("前后空白应被拒绝")
	}
	if _, err := ReleaseTag("unknown", "com.himind.x", "1.0.0"); err == nil {
		t.Fatal("未知类型应被拒绝")
	}
}

func TestArtifactAndManifestNames(t *testing.T) {
	name, err := ArtifactName("skill", "com.himind.skill.develop-himind-skills", "1.8.0")
	if err != nil {
		t.Fatalf("ArtifactName: %v", err)
	}
	if name != "com.himind.skill.develop-himind-skills-1.8.0.hmskill" {
		t.Fatalf("artifact = %q", name)
	}
	if ManifestName("com.himind.x", "1.0.0") != "com.himind.x@1.0.0.json" {
		t.Fatalf("manifest = %q", ManifestName("com.himind.x", "1.0.0"))
	}
}

func TestArtifactNameCoversEveryKind(t *testing.T) {
	want := map[string]string{
		"plugin":   "com.himind.x-1.0.0.hmpkg",
		"skill":    "com.himind.x-1.0.0.hmskill",
		"workflow": "com.himind.x-1.0.0.hmwf",
	}
	for kind, expected := range want {
		name, err := ArtifactName(kind, "com.himind.x", "1.0.0")
		if err != nil {
			t.Fatalf("ArtifactName(%s): %v", kind, err)
		}
		if name != expected {
			t.Fatalf("ArtifactName(%s) = %q, want %q", kind, name, expected)
		}
		// 签名内嵌在发布清单里，任何交付物名都不该再出现分离签名。
		if strings.HasSuffix(name, ".signature.json") {
			t.Fatalf("%s 出现分离签名资产 %q", kind, name)
		}
	}
}

func TestDownloadURLEscapesTag(t *testing.T) {
	got := DownloadURL("MrBaoquan/himind-extensions", "plugin/com.himind.x@1.0.0", "com.himind.x-1.0.0.hmpkg")
	want := "https://github.com/MrBaoquan/himind-extensions/releases/download/plugin%2Fcom.himind.x@1.0.0/com.himind.x-1.0.0.hmpkg"
	if got != want {
		t.Fatalf("download url = %q", got)
	}
}
