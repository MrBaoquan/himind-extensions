package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/MrBaoquan/himind-extensions/tooling/pluginpack"
)

func main() {
	input := flag.String("path", "", "built plugin directory")
	output := flag.String("output", "", "output .hmpkg path")
	flag.Parse()
	if strings.TrimSpace(*input) == "" || strings.TrimSpace(*output) == "" {
		fail("-path and -output are required")
	}
	if err := packagePlugin(*input, *output); err != nil {
		fail(err.Error())
	}
	fmt.Printf("created plugin package: %s\n", *output)
}

func packagePlugin(input, output string) error {
	return pluginpack.Package(input, output)
}

func fail(message string) {
	fmt.Fprintf(os.Stderr, "package failed: %s\n", message)
	os.Exit(1)
}
