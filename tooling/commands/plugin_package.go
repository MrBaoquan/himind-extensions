package commands

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/MrBaoquan/himind-extensions/tooling/pluginpack"
)

// PluginPackageMain 是 himind-plugin-package 的命令行入口。
func PluginPackageMain() {
	flags := flag.NewFlagSet("himind-plugin-package", flag.ExitOnError)
	input := flags.String("path", "", "built plugin directory")
	output := flags.String("output", "", "output .hmpkg path")
	_ = flags.Parse(os.Args[1:])
	if strings.TrimSpace(*input) == "" || strings.TrimSpace(*output) == "" {
		failf("-path and -output are required")
	}
	if err := packagePlugin(*input, *output); err != nil {
		failf(err.Error())
	}
	fmt.Printf("created plugin package: %s\n", *output)
}

func packagePlugin(input, output string) error {
	return pluginpack.Package(input, output)
}

func failf(message string) {
	fmt.Fprintf(os.Stderr, "package failed: %s\n", message)
	os.Exit(1)
}
