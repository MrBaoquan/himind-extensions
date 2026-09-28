package pluginpack

import (
	"reflect"
	"testing"
)

// 打包规则是排除法：作者放进插件目录的文件默认进包，只有构建输入与生成物被排除。
// 这份断言刻意固化「旧白名单会丢掉的那些文件」，防止规则被改回白名单。
func TestPayloadFilesIsExclusionBased(t *testing.T) {
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
		".gitignore",
		"ui/node_modules/react/index.js",
		"dist/bundle.js",
		"target/debug/out",
		"test-output/report.json",
		".github/workflows/validate.yml",
		".git/config",
		"package-lock.json",
		"yarn.lock",
		"pnpm-lock.yaml",
		"software-distribution-1.2.0.hmpkg",
		"skills/demo.hmskill",
		"workflows/demo.hmwf",
		"com.himind.demo.release-manifest.json",
		"extension-lock.json",
		"bin/demo.pdb",
		"build.log",
		"scratch.tmp",
	} {
		writeFixture(t, root, path)
	}

	got, err := PayloadFiles(root)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"README.md",
		"assets/data.json",
		"bin/demo.exe",
		"plugin.json",
		"scripts/post.ps1",
		"templates/报告模板.md",
		"ui/app.js",
		"ui/index.html",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("payload mismatch:\n got: %v\nwant: %v", got, want)
	}
}
