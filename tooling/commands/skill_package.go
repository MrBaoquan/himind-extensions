package commands

import (
	"flag"
	"fmt"
	"os"

	"github.com/MrBaoquan/himind-extensions/tooling/skillproject"
)

// SkillPackageMain 是 himind-skill-package 的命令行入口。
func SkillPackageMain() {
	flags := flag.NewFlagSet("himind-skill-package", flag.ExitOnError)
	input := flags.String("input", "", "skill project directory")
	output := flags.String("output", "", "output .hmskill path")
	_ = flags.Parse(os.Args[1:])
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
