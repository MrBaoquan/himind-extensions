package job

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Receipt omits arguments, logs, environment and input contents.
type Receipt struct {
	DurationMS int64   `json:"duration_ms,omitempty"`
	ID         string  `json:"id"`
	Status     string  `json:"status"`
	Progress   float64 `json:"progress"`
	Output     string  `json:"output"`
	UpdatedAt  string  `json:"updated_at"`
}

func writeReceipt(record *Record) {
	if record.ReceiptPath == "" {
		return
	}
	data, err := json.Marshal(Receipt{DurationMS: record.OutputDurationMS, ID: record.ID, Status: record.Status, Progress: record.Progress, Output: record.Output, UpdatedAt: record.UpdatedAt})
	if err != nil {
		return
	}
	f, err := os.CreateTemp(filepath.Dir(record.ReceiptPath), ".receipt-*.tmp")
	if err != nil {
		return
	}
	name := f.Name()
	defer os.Remove(name)
	_, err = f.Write(data)
	if e := f.Close(); err == nil {
		err = e
	}
	if err == nil {
		_ = os.Rename(name, record.ReceiptPath)
	}
}
