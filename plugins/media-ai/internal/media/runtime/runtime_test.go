package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadLockAcceptsEmbeddedFile(t *testing.T) {
	lock, err := ReadLock(EmbeddedLock())
	if err != nil {
		t.Fatalf("内嵌锁定文件应可解析: %v", err)
	}
	if lock.Runtime == "" || lock.Release == "" {
		t.Fatalf("缺少运行时信息: %+v", lock)
	}
	if len(lock.Archive.URLs) < 1 {
		t.Fatal("二进制必须有下载地址")
	}
	// 摘要必须是真的（64 位十六进制），否则校验形同虚设
	if len(lock.Archive.SHA256) != 64 {
		t.Fatalf("二进制摘要长度不对: %q", lock.Archive.SHA256)
	}
	for _, model := range lock.Models {
		if len(model.SHA256) != 64 {
			t.Errorf("模型 %s 摘要长度不对: %q", model.ID, model.SHA256)
		}
		if model.Size <= 0 {
			t.Errorf("模型 %s 缺少大小", model.ID)
		}
	}
}

// 镜像必须是一等公民：实测 huggingface.co 与 github.com 在部分网络下不可达。
func TestLockProvidesMirrors(t *testing.T) {
	lock, err := ReadLock(EmbeddedLock())
	if err != nil {
		t.Fatal(err)
	}
	if len(lock.Archive.URLs) < 2 {
		t.Errorf("二进制应提供多个下载地址（镜像），实际 %d", len(lock.Archive.URLs))
	}
	model, ok := lock.FindModel("small")
	if !ok {
		t.Fatal("应能找到 small 模型")
	}
	if urls := lock.ModelURLs(model); len(urls) < 2 {
		t.Errorf("模型应提供多个下载地址（镜像），实际 %d: %v", len(urls), urls)
	}
	for _, url := range lock.ModelURLs(model) {
		if url == "" || !contains(url, model.File) {
			t.Errorf("模型地址未代入文件名: %q", url)
		}
	}
}

func TestFindModelDefaultsToSmall(t *testing.T) {
	lock, err := ReadLock(EmbeddedLock())
	if err != nil {
		t.Fatal(err)
	}
	model, ok := lock.FindModel("")
	if !ok || model.ID != "small" {
		t.Fatalf("空 id 应回落到 small，实际 %+v ok=%v", model, ok)
	}
	if _, ok := lock.FindModel("不存在"); ok {
		t.Fatal("未知 id 不应命中")
	}
}

func TestReadLockRejectsIncomplete(t *testing.T) {
	cases := map[string]string{
		"缺二进制摘要": `{"models":[{"id":"a","file":"a.bin","size":1,"sha256":"x"}]}`,
		"缺模型":    `{"archive":{"asset":"a.zip","sha256":"x","urls":["u"]}}`,
	}
	for name, payload := range cases {
		if _, err := ReadLock([]byte(payload)); err == nil {
			t.Errorf("%s：应被拒绝", name)
		}
	}
}

// 解压必须拒绝路径穿越。
func TestUnzipRejectsEscape(t *testing.T) {
	// 用一个手工构造的 zip 验证：条目名带 ..
	directory := t.TempDir()
	archivePath := filepath.Join(directory, "evil.zip")
	if err := writeZipWithEntry(archivePath, "../escaped.txt"); err != nil {
		t.Skipf("构造测试用 zip 失败: %v", err)
	}
	if err := unzip(archivePath, filepath.Join(directory, "out")); err == nil {
		t.Fatal("带 .. 的条目应被拒绝")
	}
}

func TestInspectReportsNothingOnFreshDir(t *testing.T) {
	lock, err := ReadLock(EmbeddedLock())
	if err != nil {
		t.Fatal(err)
	}
	paths := Paths{
		Root:      t.TempDir(),
		BinaryDir: filepath.Join(t.TempDir(), "whisper"),
		ModelsDir: filepath.Join(t.TempDir(), "models"),
	}
	status := Inspect(lock, paths)
	if status.BinaryInstalled {
		t.Fatal("空目录不应报告已安装二进制")
	}
	if len(status.InstalledModels) != 0 {
		t.Fatalf("空目录不应报告已装模型: %v", status.InstalledModels)
	}
}

// 大小不符的模型文件不算装好（半成品下载会留下这种文件）。
func TestInspectIgnoresWrongSizedModel(t *testing.T) {
	lock, err := ReadLock(EmbeddedLock())
	if err != nil {
		t.Fatal(err)
	}
	modelsDir := t.TempDir()
	lock.Models = lock.Models[:1]
	target := filepath.Join(modelsDir, lock.Models[0].File)
	if err := os.WriteFile(target, []byte("too small"), 0o644); err != nil {
		t.Fatal(err)
	}
	paths := Paths{Root: t.TempDir(), BinaryDir: t.TempDir(), ModelsDir: modelsDir}
	status := Inspect(lock, paths)
	if len(status.InstalledModels) != 0 {
		t.Fatalf("大小不符不应算已安装: %v", status.InstalledModels)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
