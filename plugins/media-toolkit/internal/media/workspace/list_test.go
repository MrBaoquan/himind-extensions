package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func seed(t *testing.T, root string, files ...string) {
	t.Helper()
	for _, relative := range files {
		full := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestListMediaFindsMediaByExtension(t *testing.T) {
	dir := t.TempDir()
	seed(t, dir, "a.mp4", "b.MP3", "notes.txt", "sub/c.png", "sub/d.motion")
	root, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	files, truncated := root.ListMedia(ListOptions{})
	if truncated {
		t.Fatal("不该截断")
	}
	got := map[string]string{}
	for _, f := range files {
		got[f.Path] = f.Kind
	}
	want := map[string]string{"a.mp4": "video", "b.MP3": "audio", "sub/c.png": "image"}
	for path, kind := range want {
		if got[path] != kind {
			t.Errorf("%s 应为 %s，实际 %q（结果 %v）", path, kind, got[path], got)
		}
	}
	if _, ok := got["notes.txt"]; ok {
		t.Error("非媒体文件不该出现")
	}
	if _, ok := got["sub/d.motion"]; ok {
		t.Error(".motion 是动作文件，不该当媒体素材")
	}
}

func TestListMediaFiltersByKind(t *testing.T) {
	dir := t.TempDir()
	seed(t, dir, "v.mp4", "a.mp3", "i.png")
	root, _ := Open(dir)
	files, _ := root.ListMedia(ListOptions{Kind: "audio"})
	if len(files) != 1 || files[0].Path != "a.mp3" {
		t.Fatalf("按类型过滤失败: %+v", files)
	}
}

// 运行期产物目录会把真正的素材淹掉 —— 必须跳过。
func TestListMediaSkipsNoiseDirectories(t *testing.T) {
	dir := t.TempDir()
	seed(t, dir, "keep.mp4", ".git/x.mp4", "node_modules/y.mp4", "dist/z.mp4",
		"runtime/r.mp4", "jobs/j.mp4", ".himind/media/h.mp4")
	root, _ := Open(dir)
	files, _ := root.ListMedia(ListOptions{})
	if len(files) != 1 || files[0].Path != "keep.mp4" {
		t.Fatalf("噪声目录未被跳过: %+v", files)
	}
}

func TestListMediaRespectsMaxDepth(t *testing.T) {
	dir := t.TempDir()
	seed(t, dir, "top.mp4", "l1/a.mp4", "l1/l2/b.mp4", "l1/l2/l3/c.mp4")
	root, _ := Open(dir)

	files, _ := root.ListMedia(ListOptions{MaxDepth: 2})
	paths := map[string]bool{}
	for _, f := range files {
		paths[f.Path] = true
	}
	if !paths["top.mp4"] || !paths["l1/a.mp4"] {
		t.Fatalf("浅层文件应被找到: %v", paths)
	}
	if paths["l1/l2/b.mp4"] || paths["l1/l2/l3/c.mp4"] {
		t.Fatalf("超出深度限制的文件不该出现: %v", paths)
	}
}

func TestListMediaRespectsLimitAndReportsTruncation(t *testing.T) {
	dir := t.TempDir()
	for index := 0; index < 12; index++ {
		seed(t, dir, filepath.ToSlash(filepath.Join("f", string(rune('a'+index))+".mp4")))
	}
	root, _ := Open(dir)
	files, truncated := root.ListMedia(ListOptions{Limit: 5})
	if len(files) != 5 {
		t.Fatalf("应受 limit 限制: %d", len(files))
	}
	if !truncated {
		t.Fatal("被限制时应如实上报 truncated —— 否则调用方以为这就是全部")
	}
}

func TestListMediaReportsFileSize(t *testing.T) {
	dir := t.TempDir()
	seed(t, dir, "sized.mp4")
	root, _ := Open(dir)
	files, _ := root.ListMedia(ListOptions{})
	if len(files) != 1 || files[0].SizeBytes <= 0 {
		t.Fatalf("应带出体积: %+v", files)
	}
	if files[0].Name != "sized.mp4" {
		t.Fatalf("name 不符: %+v", files[0])
	}
}
