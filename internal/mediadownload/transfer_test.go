package mediadownload

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestTransferCollectsOnlyFinalMediaAndItsSubtitles(t *testing.T) {
	root := t.TempDir()
	media := filepath.Join(root, "sample [1].mp4")
	subtitle := filepath.Join(root, "sample [1].zh.vtt")
	for _, name := range []string{media, subtitle, filepath.Join(root, "stale.f123.mp4"), filepath.Join(root, "sample [1].mp4.part"), filepath.Join(root, "other.zh.vtt")} {
		if err := os.WriteFile(name, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	encoded, _ := json.Marshal(media)
	var progress float64
	w := &transferProgressWriter{callback: func(p TransferProgress) { progress = p.Percent }}
	_, _ = w.Write([]byte("download:50%|1MiB/s|2|100|200\nhimind-file:" + string(encoded) + "\n"))
	files, err := w.completedFiles(root)
	if err != nil || len(files) != 2 || files[0] != media || files[1] != subtitle || progress != 50 {
		t.Fatalf("completed outputs: %v progress %v err %v", files, progress, err)
	}
	outside := filepath.Join(t.TempDir(), "outside.mp4")
	if err = os.WriteFile(outside, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	w.paths = []string{outside}
	if _, err = w.completedFiles(root); err == nil {
		t.Fatal("accepted output outside private staging")
	}
}
