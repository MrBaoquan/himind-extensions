package job

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReceiptCanReplaceHeartbeatWithoutLeakingJobInternals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipt.json")
	record := &Record{ID: "job", Status: StatusRunning, ReceiptPath: path, Input: "private-input", Args: []string{"credential-value"}, Error: "private-log", Output: "output.wav", UpdatedAt: "2026-11-14T00:00:00Z"}
	writeReceipt(record)
	record.Status = StatusCompleted
	record.Progress = 100
	record.OutputDurationMS = 1200
	writeReceipt(record)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got Receipt
	if err = json.Unmarshal(data, &got); err != nil || got.Status != StatusCompleted || got.DurationMS != 1200 {
		t.Fatalf("terminal receipt: %s %v", data, err)
	}
	for _, secret := range []string{"private-input", "credential-value", "private-log", "args", "receipt_path"} {
		if strings.Contains(string(data), secret) {
			t.Fatal("job internals exposed", secret)
		}
	}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".receipt-*.tmp"))
	if len(files) > 0 {
		t.Fatal("temporary receipts retained")
	}
}
