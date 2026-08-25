package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/MrBaoquan/himind-extensions/tooling/skillproject"
)

func main() {
	input := flag.String("input", "", "skill project directory")
	output := flag.String("output", "", "output .hmskill path")
	flag.Parse()
	if *input == "" || *output == "" {
		fmt.Fprintln(os.Stderr, "input and output are required")
		os.Exit(2)
	}
	if err := skillproject.Package(*input, *output); err != nil {
		fmt.Fprintln(os.Stderr, "package failed:", err)
		os.Exit(1)
	}
	fmt.Println("created skill package:", *output)
}
