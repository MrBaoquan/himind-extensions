package runtime

import (
	"archive/zip"
	"os"
)

// writeZipWithEntry 造一个只含单个条目的 zip，用于验证路径穿越防护。
func writeZipWithEntry(archivePath, entryName string) error {
	file, err := os.Create(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := zip.NewWriter(file)
	entry, err := writer.Create(entryName)
	if err != nil {
		writer.Close()
		return err
	}
	if _, err := entry.Write([]byte("payload")); err != nil {
		writer.Close()
		return err
	}
	return writer.Close()
}
