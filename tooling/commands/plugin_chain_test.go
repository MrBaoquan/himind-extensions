package commands

import (
	"archive/zip"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/MrBaoquan/himind-extensions/tooling/pluginpack"
	"github.com/MrBaoquan/himind-extensions/tooling/pluginproject"
	validator "github.com/MrBaoquan/himind-extensions/tools/cmd/himind-plugin-validate"
)

// 端到端链路：脚手架 → 测试/构建 → 摊平暂存 → 打包 → 制品复验。
// 这一段此前没有任何自动化覆盖，链路上任何一环漂移都只能在发布、甚至用户安装后才发现。
func TestScaffoldBuildStagePackageChain(t *testing.T) {
	requireGoToolchain(t)
	created, err := pluginproject.Create(pluginproject.Config{
		Name:         "chain-demo",
		DisplayName:  "链路示例",
		Description:  "验证脚手架到制品的完整链路。",
		Author:       "测试用户",
		Categories:   []string{"software-engineering"},
		ReleaseNotes: "首次创建。",
		Template:     "ui-tool",
		OutputDir:    t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join("bin", "chain-demo.exe")
	staging := filepath.Join(t.TempDir(), "staging")
	runGoCommand(t, created.Root, "test", "./...")
	runGoCommand(t, created.Root, "build", "-o", filepath.Join(staging, entry), ".")

	report, err := stagePlugin(created.Root, staging, "")
	if err != nil {
		t.Fatal(err)
	}
	if report.fromSource {
		t.Fatal("staging must keep the freshly built entry instead of the source copy")
	}
	if report.copied == 0 {
		t.Fatal("staging copied nothing; the plugin resources were dropped")
	}

	artifact := filepath.Join(t.TempDir(), "chain-demo.hmpkg")
	if err := packagePlugin(staging, artifact); err != nil {
		t.Fatal(err)
	}
	if err := validator.ValidateArchive(artifact); err != nil {
		t.Fatalf("packaged archive failed validation: %v", err)
	}

	names := archiveNames(t, artifact)
	for _, required := range []string{"plugin.json", "bin/chain-demo.exe", "ui/index.html"} {
		if _, ok := names[required]; !ok {
			t.Fatalf("archive is missing %s: %v", required, sortedNames(names))
		}
	}
	requireNoBuildInputs(t, names)

	// 脚手架产出的清单必须能被 Agent 接受，而 Agent 只认这两种视图落点。
	data, err := os.ReadFile(filepath.Join(staging, "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Contributes struct {
			Views []struct {
				Location string `json:"location"`
			} `json:"views"`
		} `json:"contributes"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Contributes.Views) != 1 || manifest.Contributes.Views[0].Location != "plugin_navigation" {
		t.Fatalf("staged manifest carries a view location the Agent rejects: %+v", manifest.Contributes.Views)
	}
}

// 仓库里真实发布的插件也要走一遍同一条链路：只要作者往插件目录里放运行期文件，
// 就必须出现在制品里，不允许再出现「本地能跑、发布装不上」的静默丢失。
func TestRepositoryPluginsSurviveTheReleasePipeline(t *testing.T) {
	requireGoToolchain(t)
	root := repositoryRoot(t)
	directory := filepath.Join(root, "plugins")
	items, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		t.Skip("this repository has no plugins directory")
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if !item.IsDir() {
			continue
		}
		t.Run(item.Name(), func(t *testing.T) {
			source := filepath.Join(directory, item.Name())
			manifest, err := validator.ReadManifest(source)
			if err != nil {
				t.Fatal(err)
			}
			staging := filepath.Join(t.TempDir(), "staging")
			runGoCommand(t, root, "build", "-o", filepath.Join(staging, filepath.FromSlash(manifest.Entry)), "./plugins/"+item.Name())

			if _, err := stagePlugin(source, staging, ""); err != nil {
				t.Fatal(err)
			}
			artifact := filepath.Join(t.TempDir(), item.Name()+".hmpkg")
			if err := packagePlugin(staging, artifact); err != nil {
				t.Fatal(err)
			}
			if err := validator.ValidateArchive(artifact); err != nil {
				t.Fatalf("packaged archive failed validation: %v", err)
			}

			payload, err := pluginpack.PayloadFiles(source)
			if err != nil {
				t.Fatal(err)
			}
			names := archiveNames(t, artifact)
			for _, relative := range payload {
				if _, ok := names[relative]; !ok {
					t.Fatalf("published payload is missing %s: %v", relative, sortedNames(names))
				}
			}
			if _, ok := names[manifest.Entry]; !ok {
				t.Fatalf("published payload is missing the built entry %s", manifest.Entry)
			}
			requireNoBuildInputs(t, names)
		})
	}
}

func requireGoToolchain(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("the Go toolchain is not available")
	}
}

func runGoCommand(t *testing.T, directory string, args ...string) {
	t.Helper()
	command := exec.Command("go", args...)
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("go %v failed: %v\n%s", args, err, output)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "extensions.json")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Skip("extensions.json was not found above the test directory")
		}
		directory = parent
	}
}

func archiveNames(t *testing.T, path string) map[string]struct{} {
	t.Helper()
	archive, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	names := make(map[string]struct{}, len(archive.File))
	for _, file := range archive.File {
		names[file.Name] = struct{}{}
	}
	return names
}

func sortedNames(names map[string]struct{}) []string {
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func requireNoBuildInputs(t *testing.T, names map[string]struct{}) {
	t.Helper()
	for name := range names {
		// checksums.sha256 是打包时新生成的清单，不算「构建输入进了包」。
		if name == "checksums.sha256" {
			continue
		}
		switch {
		case name == "go.mod", name == "go.sum":
			t.Fatalf("build input leaked into the archive: %s", name)
		case filepath.Ext(name) == ".go":
			t.Fatalf("source file leaked into the archive: %s", name)
		case isPackageArtifact(name):
			// 插件目录里残留的旧发布包曾经被整份打进新包。
			t.Fatalf("a nested release artifact leaked into the archive: %s", name)
		}
	}
}

func isPackageArtifact(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".hmpkg", ".hmskill", ".hmwf", ".pdb", ".log", ".tmp":
		return true
	default:
		return false
	}
}
