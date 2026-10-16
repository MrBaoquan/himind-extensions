package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func newRoot(t *testing.T) Root {
	t.Helper()
	root, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestResolveKeepsRelativePathInsideRoot(t *testing.T) {
	root := newRoot(t)
	resolved, err := root.Resolve(filepath.Join("sub", "out.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(resolved) != filepath.Join(root.Abs(), "sub") {
		t.Fatalf("解析结果不在工作区内: %s", resolved)
	}
}

// 越界必须被拒绝——这是插件规范对路径的硬约束，漏一个就是一次越界写入。
func TestResolveRejectsEscapes(t *testing.T) {
	root := newRoot(t)
	for _, attempt := range []string{
		filepath.Join("..", "outside.mp4"),
		filepath.Join("..", "..", "outside.mp4"),
		filepath.Join("sub", "..", "..", "outside.mp4"),
	} {
		if _, err := root.Resolve(attempt); err == nil {
			t.Errorf("越界路径应被拒绝: %s", attempt)
		}
	}
}

func TestResolveRejectsAbsolutePathOutsideRoot(t *testing.T) {
	root := newRoot(t)
	outside := filepath.Join(t.TempDir(), "outside.mp4")
	if _, err := root.Resolve(outside); err == nil {
		t.Fatalf("工作区外的绝对路径应被拒绝: %s", outside)
	}
}

func TestResolveAcceptsAbsolutePathInsideRoot(t *testing.T) {
	root := newRoot(t)
	inside := filepath.Join(root.Abs(), "movie.mp4")
	resolved, err := root.Resolve(inside)
	if err != nil {
		t.Fatalf("工作区内的绝对路径应被接受: %v", err)
	}
	if resolved != inside {
		t.Fatalf("解析结果不一致: %s != %s", resolved, inside)
	}
}

// 输出文件通常还不存在，解析不应因为它不存在而失败。
func TestResolveAllowsNotYetExistingOutput(t *testing.T) {
	root := newRoot(t)
	target := filepath.Join("nested", "deep", "out.mp4")
	if _, err := root.Resolve(target); err != nil {
		t.Fatalf("尚未存在的输出路径应可解析: %v", err)
	}
	abs := filepath.Join(root.Abs(), target)
	if err := EnsureParentDir(abs); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Dir(abs)); err != nil || !info.IsDir() {
		t.Fatalf("父目录未创建: %v", err)
	}
}

func TestResolveRejectsEmpty(t *testing.T) {
	root := newRoot(t)
	if _, err := root.Resolve("   "); err == nil {
		t.Fatal("空路径应被拒绝")
	}
}
