package pluginpack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackageFilesKeepsRuntimeAssetsAndDropsBuildInputs(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{
		"plugin.json",
		"bin/demo.exe",
		"ui/index.html",
		"ui/app.js",
		"scripts/post.ps1",
		"templates/报告模板.md",
		"assets/data.json",
		"README.md",
		"main.go",
		"go.mod",
		"go.sum",
		"checksums.sha256",
		"ui/node_modules/react/index.js",
		"dist/bundle.js",
	} {
		writeFixture(t, root, path)
	}

	files, err := packageFiles(root, filepath.Join(root, "out.hmpkg"))
	if err != nil {
		t.Fatal(err)
	}

	joined := "\n" + strings.Join(files, "\n") + "\n"
	for _, forbidden := range []string{
		"main.go", "go.mod", "go.sum", "checksums.sha256",
		"node_modules", "dist/bundle.js",
	} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("build input or generated path was packaged: %s (%v)", forbidden, files)
		}
	}
	for _, required := range []string{
		"bin/demo.exe", "plugin.json", "ui/app.js", "ui/index.html",
		"scripts/post.ps1", "templates/报告模板.md", "assets/data.json", "README.md",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("runtime asset was dropped from the package: %s (%v)", required, files)
		}
	}
}

func TestPackageFilesSkipsTheArchiveBeingWritten(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "plugin.json")
	writeFixture(t, root, "bin/demo.exe")
	writeFixture(t, root, "out.hmpkg")

	files, err := packageFiles(root, filepath.Join(root, "out.hmpkg"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains("\n"+strings.Join(files, "\n")+"\n", "out.hmpkg") {
		t.Fatalf("archive under construction leaked into the payload: %v", files)
	}
}

// writeFixture 在 root 下按相对路径建出一个文件，供打包测试搭目录。
func writeFixture(t *testing.T, root, relative string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(relative), 0o644); err != nil {
		t.Fatal(err)
	}
}
