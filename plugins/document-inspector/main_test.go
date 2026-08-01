package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectTextDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "project.md")
	if err := os.WriteFile(path, []byte("# Project\n\nDelivery checklist"), 0600); err != nil {
		t.Fatal(err)
	}
	value, err := inspect(input{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if value.Format != "md" || value.Words != 4 || value.Text != "# Project\nDelivery checklist" {
		t.Fatalf("unexpected result: %#v", value)
	}
}

func TestInspectDocxDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "project.docx")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	writer, err := archive.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = writer.Write([]byte(`<w:document xmlns:w="urn:test"><w:body><w:p><w:r><w:t>Alpha</w:t></w:r></w:p><w:p><w:r><w:t>Beta</w:t></w:r></w:p></w:body></w:document>`))
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	value, err := inspect(input{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if value.Text != "Alpha\nBeta" {
		t.Fatalf("unexpected text: %q", value.Text)
	}
}
