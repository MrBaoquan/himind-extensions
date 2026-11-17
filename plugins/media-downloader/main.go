package main

import "github.com/MrBaoquan/himind-extensions/internal/mediadownload"

// The legacy executable keeps its protocol and data identity while both products
// use the same tested transfer implementation.
func main() { mediadownload.RunCLI() }
