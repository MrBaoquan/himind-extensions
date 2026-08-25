package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceVersionPreservesHistory(t *testing.T) {
	items := []map[string]interface{}{
		{"plugin_id": "com.himind.test", "version": "1.0.0"},
		{"plugin_id": "com.himind.test", "version": "2.0.0", "old": true},
	}
	entry := map[string]interface{}{"plugin_id": "com.himind.test", "version": "2.0.0"}
	result := replaceVersion(items, "plugin_id", "com.himind.test", "2.0.0", entry)
	if len(result) != 2 {
		t.Fatalf("expected two immutable versions, got %d", len(result))
	}
	if result[1]["old"] != nil {
		t.Fatal("same version should be replaced by the newly generated catalog entry")
	}
}

func TestVerifyMetadataMatchesArtifact(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "test.hmskill")
	data := []byte("signed package")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	metadata := signatureMetadata{
		FileName: "test.hmskill", FileSize: int64(len(data)), SHA256: hex.EncodeToString(digest[:]),
		Signature: "base64", SignatureKeyID: "key-1", SignatureAlgorithm: "rsa-pss-sha256",
	}
	if err := verifyMetadata(path, metadata); err != nil {
		t.Fatal(err)
	}
	metadata.SHA256 = "bad"
	if err := verifyMetadata(path, metadata); err == nil {
		t.Fatal("mismatched artifact metadata must be rejected")
	}
}
