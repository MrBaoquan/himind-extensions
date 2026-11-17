package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Walk both durable formats without modifying either. Duplicate event IDs must
// describe identical events; conflicts stop the audit rather than hiding data.
func walkLibraryCatalog(ctx context.Context, library *libraryRecord, visit func(catalogEvent) error) error {
	seen := map[string][32]byte{}
	consume := func(data []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		data = bytes.TrimSpace(data)
		if len(data) == 0 {
			return nil
		}
		var event catalogEvent
		if len(data) > 1<<20 || json.Unmarshal(data, &event) != nil || event.EventID == "" || event.LibraryID != library.ID {
			return fmt.Errorf("目录记录格式无效，请保留原文件并检查")
		}
		canonical, _ := json.Marshal(event)
		digest := sha256.Sum256(canonical)
		if existing, ok := seen[event.EventID]; ok {
			if existing != digest {
				return fmt.Errorf("同一目录事件存在不同内容，请核对后继续")
			}
			return nil
		}
		seen[event.EventID] = digest
		return visit(event)
	}
	root, err := filepath.EvalSymlinks(library.RootURI)
	if err != nil {
		return fmt.Errorf("资源库当前离线")
	}
	for _, format := range []struct {
		directory, extension string
		lines                bool
	}{{"users", ".jsonl", true}, {"events", ".json", false}} {
		directory := filepath.Join(root, ".himind-media", "catalog", format.directory)
		real, err := filepath.EvalSymlinks(directory)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !isPathInside(root, real) {
			return fmt.Errorf("目录记录路径越出资源库")
		}
		entries, err := os.ReadDir(real)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err = ctx.Err(); err != nil {
				return err
			}
			if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), format.extension) {
				continue
			}
			file, err := os.Open(filepath.Join(real, entry.Name()))
			if err != nil {
				return err
			}
			if format.lines {
				scanner := bufio.NewScanner(file)
				scanner.Buffer(make([]byte, 4096), 1<<20)
				for scanner.Scan() {
					if err = consume(scanner.Bytes()); err != nil {
						break
					}
				}
				if err == nil {
					err = scanner.Err()
				}
			} else {
				var data []byte
				data, err = io.ReadAll(io.LimitReader(file, (1<<20)+1))
				if err == nil {
					err = consume(data)
				}
			}
			closeErr := file.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
		}
	}
	return nil
}

func inspectLibraryCatalog(libraryID string, limit int) (any, error) {
	library, err := getLibrary(libraryID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	items := []catalogEvent{}
	count := 0
	err = walkLibraryCatalog(ctx, library, func(event catalogEvent) error {
		count++
		if len(items) < limit {
			items = append(items, event)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"library_id": libraryID, "event_count": count, "items": items, "formats": []string{"users-jsonl", "immutable-events"}}, nil
}
