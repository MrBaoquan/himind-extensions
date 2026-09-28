package commands

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/MrBaoquan/himind-extensions/tooling/pluginpack"
	validator "github.com/MrBaoquan/himind-extensions/tools/cmd/himind-plugin-validate"
)

// PluginStageMain 是 himind-plugin-stage 的命令行入口。
//
// 它把「一个已构建的插件目录」摊平成打包用的暂存目录：入口二进制由调用方先构建，
// 其余随包文件按 pluginpack 的排除法规则整份拷过来。发布脚本曾经自己写白名单
// 拼暂存目录，作者新增的运行期文件会被静默丢掉；现在规则只剩一处。
func PluginStageMain() {
	flags := flag.NewFlagSet("himind-plugin-stage", flag.ExitOnError)
	input := flags.String("path", "", "built plugin directory")
	output := flags.String("output", "", "staging directory to fill")
	entry := flags.String("entry", "", "entry file relative to the plugin directory; defaults to plugin.json entry")
	_ = flags.Parse(os.Args[1:])
	if strings.TrimSpace(*input) == "" || strings.TrimSpace(*output) == "" {
		failStage("-path and -output are required")
	}
	report, err := stagePlugin(*input, *output, *entry)
	if err != nil {
		failStage(err.Error())
	}
	fmt.Printf("staged %d plugin files into %s\n", report.copied, report.output)
	if report.fromSource {
		fmt.Printf("entry was taken from the source directory: %s\n", report.entry)
	}
}

type stageReport struct {
	output     string
	entry      string
	copied     int
	fromSource bool
}

func stagePlugin(input, output, entryOverride string) (stageReport, error) {
	report := stageReport{}
	source, err := filepath.Abs(input)
	if err != nil {
		return report, err
	}
	destination, err := filepath.Abs(output)
	if err != nil {
		return report, err
	}
	if source == destination {
		return report, fmt.Errorf("staging directory must differ from the plugin directory")
	}
	manifest, err := validator.ReadManifest(source)
	if err != nil {
		return report, err
	}
	entry := strings.TrimSpace(entryOverride)
	if entry == "" {
		entry = manifest.Entry
	}
	entry = filepath.ToSlash(entry)
	if entry == "" || filepath.IsAbs(entry) || strings.Contains(entry, "..") || strings.HasSuffix(strings.ToLower(entry), ".go") {
		return report, fmt.Errorf("invalid entry: %q", entry)
	}
	report.entry = entry

	stagedEntry := filepath.Join(destination, filepath.FromSlash(entry))
	// 调用方通常先把入口二进制构建到暂存目录，这里不覆盖它；只有调用方没构建时，
	// 才把源目录里已有的入口当产物一并拷过来。
	if _, err := os.Stat(stagedEntry); err != nil {
		if _, sourceErr := os.Stat(filepath.Join(source, filepath.FromSlash(entry))); sourceErr != nil {
			return report, fmt.Errorf("entry %s is missing: run the plugin build first, or build it into the staging directory", entry)
		}
		report.fromSource = true
	}

	files, err := pluginpack.PayloadFiles(source)
	if err != nil {
		return report, err
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return report, err
	}
	for _, relative := range files {
		if relative == entry && !report.fromSource {
			continue
		}
		sourcePath := filepath.Join(source, filepath.FromSlash(relative))
		if err := copyFile(sourcePath, filepath.Join(destination, filepath.FromSlash(relative))); err != nil {
			return report, err
		}
		report.copied++
	}
	report.output = destination
	return report, nil
}

func copyFile(source, target string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return err
	}
	return output.Close()
}

func failStage(message string) {
	fmt.Fprintf(os.Stderr, "stage failed: %s\n", message)
	os.Exit(1)
}
