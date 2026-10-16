package iostudio

import (
	"os"
	"path/filepath"
	"testing"
)

// IOStudio 用 .NET 的 Encoding.UTF8 写状态文件，**会带 UTF-8 BOM**（EF BB BF）。
// Go 的 json.Unmarshal 遇到 BOM 直接报 "invalid character 'ï'"，
// 不清掉的话每一次调用都会失败。这条守住它。
func TestReadStatusStripsUTF8BOM(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "status.json")
	body := `{"phase":"validated","validation":{"hasErrors":false,"errorCount":0}}`
	payload := append([]byte{0xEF, 0xBB, 0xBF}, []byte(body)...)
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatal(err)
	}

	status, err := ReadStatus(path)
	if err != nil {
		t.Fatalf("带 BOM 的状态文件应能解析: %v", err)
	}
	if status == nil || status.Phase != "validated" {
		t.Fatalf("解析结果不符: %+v", status)
	}
	if status.Validation == nil || status.Validation.HasErrors {
		t.Fatalf("预检结果不符: %+v", status.Validation)
	}
}

// 不带 BOM 也要照常工作。
func TestReadStatusWithoutBOM(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "status.json")
	if err := os.WriteFile(path, []byte(`{"phase":"built"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	status, err := ReadStatus(path)
	if err != nil {
		t.Fatal(err)
	}
	if status.Phase != "built" {
		t.Fatalf("解析结果不符: %+v", status)
	}
}

func TestReadStatusRejectsGarbage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "status.json")
	if err := os.WriteFile(path, []byte("<html>不是 JSON</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadStatus(path); err == nil {
		t.Fatal("非 JSON 内容应报错，而不是静默当成空结果")
	}
}
