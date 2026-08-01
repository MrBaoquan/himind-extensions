package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/MrBaoquan/himind-extensions/tooling/pluginproject"
)

func main() {
	name := flag.String("name", "", "stable ASCII plugin name, for example document-tool")
	displayName := flag.String("display-name", "", "Chinese display name shown to users")
	description := flag.String("description", "", "Chinese user-facing plugin description")
	author := flag.String("author", "", "current authorized HiMind creator name")
	category := flag.String("category", "software-engineering", "functional category id")
	releaseNotes := flag.String("release-notes", "", "Chinese release notes for this version")
	template := flag.String("template", "readonly-tool", "readonly-tool, job-worker or ui-tool")
	output := flag.String("output", "", "parent directory for the generated plugin")
	flag.Parse()
	result, err := pluginproject.Create(pluginproject.Config{Name: strings.TrimSpace(*name), DisplayName: strings.TrimSpace(*displayName), Description: strings.TrimSpace(*description), Author: strings.TrimSpace(*author), Categories: []string{strings.TrimSpace(*category)}, ReleaseNotes: strings.TrimSpace(*releaseNotes), Template: strings.TrimSpace(*template), OutputDir: strings.TrimSpace(*output)})
	if err != nil {
		fmt.Fprintf(os.Stderr, "create failed: %s\n", err)
		os.Exit(1)
	}
	fmt.Printf("created plugin project: %s\n", result.Root)
}
