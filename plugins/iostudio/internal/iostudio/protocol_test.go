package iostudio

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleReport = `{
  "name": "UDP 回环",
  "ok": false,
  "steps": [
    {"index": 0, "op": "open", "ok": true, "elapsedMs": 31},
    {"index": 1, "op": "send", "via": "udp", "ok": true, "elapsedMs": 12},
    {"index": 2, "op": "expect", "via": "udp", "ok": false, "elapsedMs": 500,
     "reason": "超时未收到应答", "expected": "0x01", "actual": ""}
  ],
  "summary": {"passed": 2, "failed": 1}
}`

func TestReadProtocolReportParsesSteps(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")
	if err := os.WriteFile(path, []byte(sampleReport), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := readProtocolReport(path)
	if err != nil {
		t.Fatal(err)
	}
	if report.Name != "UDP 回环" || report.OK {
		t.Fatalf("报告头部解析错误: %+v", report)
	}
	if len(report.Steps) != 3 {
		t.Fatalf("步骤数不符: %d", len(report.Steps))
	}
	if report.Summary.Passed != 2 || report.Summary.Failed != 1 {
		t.Fatalf("汇总解析错误: %+v", report.Summary)
	}
	failed := report.Steps[2]
	if failed.OK || failed.Reason == "" || failed.Via != "udp" {
		t.Fatalf("失败步骤解析错误: %+v", failed)
	}
}

// 上游给步骤加字段时不能丢 —— 编排/排查要靠这些定位信息。
func TestProtocolStepKeepsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")
	if err := os.WriteFile(path, []byte(sampleReport), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := readProtocolReport(path)
	if err != nil {
		t.Fatal(err)
	}
	step := report.Steps[2]
	if step.Extra["expected"] != "0x01" {
		t.Fatalf("未知字段应保留在 Extra: %+v", step.Extra)
	}
	encoded, err := json.Marshal(step)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"expected"`) {
		t.Fatalf("往返后丢失未知字段: %s", encoded)
	}
}

// IOStudio 刻意以不带 BOM 的 UTF-8 写报告；版本回退或手工改过的可能带 BOM，仍要能读。
func TestReadProtocolReportToleratesBOM(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")
	payload := append([]byte{0xEF, 0xBB, 0xBF}, []byte(sampleReport)...)
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readProtocolReport(path); err != nil {
		t.Fatalf("带 BOM 的报告应能解析: %v", err)
	}
}

func TestReadProtocolReportRejectsGarbage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")
	if err := os.WriteFile(path, []byte("<html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readProtocolReport(path); err == nil {
		t.Fatal("非 JSON 应报错")
	}
}

func TestProtocolTestWithoutCLI(t *testing.T) {
	if _, err := ProtocolTest(t.Context(), ProtocolTestOptions{ScenarioPath: "x.json"}); err != ErrCLINotFound {
		t.Fatalf("没有 CLI 时应返回 ErrCLINotFound，实际 %v", err)
	}
}
