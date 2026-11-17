//go:build !windows

package main

import "os"

func publishAcquisitionFile(source, target string) error {
	if err := os.Link(source, target); err != nil {
		return err
	}
	return os.Remove(source)
}
