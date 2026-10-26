package distribution

import (
	"strings"
	"testing"
)

func TestReleaseIconValidation(t *testing.T) {
	valid := ReleaseIcon{
		FileName: "com.example.demo-1.0.0-icon.png", SizeBytes: 128,
		SHA256: strings.Repeat("a", 64), ContentType: "image/png",
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid icon rejected: %v", err)
	}
	cases := []ReleaseIcon{
		{FileName: "../icon.png", SizeBytes: 128, SHA256: strings.Repeat("a", 64), ContentType: "image/png"},
		{FileName: "icon.svg", SizeBytes: 128, SHA256: strings.Repeat("a", 64), ContentType: "image/svg+xml"},
		{FileName: "icon.png", SizeBytes: 0, SHA256: strings.Repeat("a", 64), ContentType: "image/png"},
		{FileName: "icon.webp", SizeBytes: 128, SHA256: strings.Repeat("a", 64), ContentType: "image/png"},
		{FileName: "icon.png", SizeBytes: 128, SHA256: "bad", ContentType: "image/png"},
	}
	for _, value := range cases {
		if err := value.Validate(); err == nil {
			t.Fatalf("invalid icon accepted: %#v", value)
		}
	}
}

func TestReleaseManifestValidatesOptionalIcon(t *testing.T) {
	value := ReleaseManifest{
		SchemaVersion: ReleaseManifestSchema,
		Tag:           "plugin/com.example.demo@1.0.0",
		Kind:          KindPlugin,
		ID:            "com.example.demo",
		Version:       "1.0.0",
		Artifact: ReleaseArtifact{
			Name: "com.example.demo-1.0.0.hmpkg", SizeBytes: 10, SHA256: strings.Repeat("b", 64),
		},
		Icon: &ReleaseIcon{
			FileName: "com.example.demo-1.0.0-icon.webp", SizeBytes: 64,
			SHA256: strings.Repeat("c", 64), ContentType: "image/webp",
		},
	}
	if err := value.Validate(); err != nil {
		t.Fatalf("manifest with valid icon rejected: %v", err)
	}
	value.Icon.ContentType = "image/png"
	if err := value.Validate(); err == nil {
		t.Fatal("manifest with mismatched icon MIME accepted")
	}
}
