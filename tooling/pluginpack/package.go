package pluginpack

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	validator "github.com/MrBaoquan/himind-extensions/tools/cmd/himind-plugin-validate"
)

// Package validates a built plugin directory and creates a deterministic .hmpkg archive.
func Package(input, output string) error {
	if err := validator.ValidateDirectory(input); err != nil {
		return err
	}
	manifest, err := validator.ReadManifest(input)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(input, filepath.FromSlash(manifest.Entry))); err != nil {
		return fmt.Errorf("package input must contain built entry %s", manifest.Entry)
	}
	files, err := packageFiles(input, output)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		return err
	}
	file, err := os.Create(output)
	if err != nil {
		return err
	}
	writer := zip.NewWriter(file)
	checksums := make([]string, 0, len(files))
	for _, relative := range files {
		if relative == "checksums.sha256" {
			continue
		}
		path := filepath.Join(input, filepath.FromSlash(relative))
		source, openErr := os.Open(path)
		if openErr != nil {
			_ = writer.Close()
			_ = file.Close()
			return openErr
		}
		entry, copyErr := writer.Create(relative)
		if copyErr == nil {
			_, copyErr = io.Copy(entry, source)
		}
		_ = source.Close()
		if copyErr != nil {
			_ = writer.Close()
			_ = file.Close()
			return copyErr
		}
		checksum, checksumErr := sha256File(path)
		if checksumErr != nil {
			_ = writer.Close()
			_ = file.Close()
			return checksumErr
		}
		checksums = append(checksums, fmt.Sprintf("%s  %s", checksum, relative))
	}
	checksumEntry, err := writer.Create("checksums.sha256")
	if err == nil {
		_, err = io.WriteString(checksumEntry, strings.Join(checksums, "\n")+"\n")
	}
	if err == nil {
		err = writer.Close()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(output)
	}
	return err
}

func packageFiles(root, output string) ([]string, error) {
	var files []string
	outputPath, err := filepath.Abs(output)
	if err != nil {
		return nil, err
	}
	err = filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			if shouldSkipPackageDirectory(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if shouldSkipPackageFile(info.Name()) {
			return nil
		}
		absolutePath, absoluteErr := filepath.Abs(path)
		if absoluteErr != nil {
			return absoluteErr
		}
		if absolutePath == outputPath {
			return nil
		}
		relative, relativeErr := filepath.Rel(root, path)
		if relativeErr != nil {
			return relativeErr
		}
		files = append(files, filepath.ToSlash(relative))
		return nil
	})
	sort.Strings(files)
	return files, err
}

// Generated dependency trees and local package-manager locks are never part of
// a portable HiMind plugin. Keeping them out makes packaging deterministic and
// prevents large UI plugins from timing out while being archived.
func shouldSkipPackageDirectory(name string) bool {
	switch strings.ToLower(name) {
	case ".git", "node_modules", "dist", "target", "test-output":
		return true
	default:
		return false
	}
}

func shouldSkipPackageFile(name string) bool {
	return strings.EqualFold(name, "package-lock.json") || strings.EqualFold(name, "yarn.lock") || strings.EqualFold(name, "pnpm-lock.yaml")
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
