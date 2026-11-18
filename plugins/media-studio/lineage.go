// lineage.go —— 生成血缘：每个产物从哪条提示词、哪个风格包、哪个模型来。
//
// 媒体资源以内容哈希存储，一旦离开对话就只剩文件名；「这张图当时怎么生成的」
// 是最容易丢失、也最影响复用的一条信息。血缘以追加式 JSONL 保存，写入不覆盖，
// 查询按 media_id / 路径 / SHA 反查。
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const lineageFile = "lineage.jsonl"

// Lineage 是一条生成记录。
type Lineage struct {
	LineageID     string `json:"lineage_id"`
	MediaID       string `json:"media_id"`
	Path          string `json:"path"`
	SHA256        string `json:"sha256"`
	MimeType      string `json:"mime_type"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	SizeBytes     int    `json:"size_bytes"`
	Capability    string `json:"capability"`
	Prompt        string `json:"prompt"`
	BasePrompt    string `json:"base_prompt"`
	StyleID       string `json:"style_id"`
	ParentMediaID string `json:"parent_media_id,omitempty"`
	ServiceID     string `json:"service_id"`
	Model         string `json:"model"`
	JobID         string `json:"job_id,omitempty"`
	RequestID     string `json:"request_id,omitempty"`
	WorkspaceRoot string `json:"workspace_root,omitempty"`
	CreatedAt     string `json:"created_at"`
}

func lineagePath() (string, error) {
	root, err := dataRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, lineageFile), nil
}

// appendLineage 追加写入若干条记录（单次打开、单次写、逐行 flush）。
func appendLineage(records []Lineage) error {
	if len(records) == 0 {
		return nil
	}
	path, err := lineagePath()
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("打开血缘文件失败: %w", err)
	}
	defer func() { _ = file.Close() }()
	writer := bufio.NewWriter(file)
	for _, record := range records {
		encoded, err := json.Marshal(record)
		if err != nil {
			return fmt.Errorf("序列化血缘失败: %w", err)
		}
		if _, err := writer.Write(append(encoded, '\n')); err != nil {
			return fmt.Errorf("写入血缘失败: %w", err)
		}
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("写入血缘失败: %w", err)
	}
	return nil
}

// loadLineage 读取全部血缘记录；文件不存在视为空。
//
// 血缘文件是追加式日志，条数在单机尺度上远小于媒体索引，一次性读入比维护
// 第二份索引更不容易出错。
func loadLineage() ([]Lineage, error) {
	path, err := lineagePath()
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取血缘失败: %w", err)
	}
	defer func() { _ = file.Close() }()
	records := make([]Lineage, 0, 64)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 2*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var record Lineage
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			// 单行损坏不该让整次查询失败：跳过并继续。
			continue
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取血缘失败: %w", err)
	}
	return records, nil
}

// lineageQuery 描述一次反查条件：media_id、路径或 SHA-256 任一命中即可。
type lineageQuery struct {
	MediaID string
	Path    string
	SHA256  string
	Limit   int
	Chain   bool
}

// queryLineage 返回命中记录（按时间倒序）以及可选的祖先链。
func queryLineage(query lineageQuery) ([]Lineage, []Lineage, error) {
	records, err := loadLineage()
	if err != nil {
		return nil, nil, err
	}
	targetPath := ""
	if strings.TrimSpace(query.Path) != "" {
		targetPath = filepath.Clean(query.Path)
	}
	matches := make([]Lineage, 0, 8)
	for _, record := range records {
		hit := false
		if value := strings.TrimSpace(query.MediaID); value != "" && record.MediaID == value {
			hit = true
		}
		if !hit && targetPath != "" && record.Path != "" && filepath.Clean(record.Path) == targetPath {
			hit = true
		}
		if !hit {
			if value := strings.TrimSpace(query.SHA256); value != "" && strings.EqualFold(record.SHA256, value) {
				hit = true
			}
		}
		if hit {
			matches = append(matches, record)
		}
	}
	// 倒序：最近的生成更有解释力。
	for left, right := 0, len(matches)-1; left < right; left, right = left+1, right-1 {
		matches[left], matches[right] = matches[right], matches[left]
	}
	limit := query.Limit
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	if len(matches) > limit {
		matches = matches[:limit]
	}
	if !query.Chain || len(matches) == 0 {
		return matches, nil, nil
	}
	chain := make([]Lineage, 0, 8)
	seen := map[string]bool{}
	queue := []string{matches[0].ParentMediaID}
	for depth := 0; depth < 5 && len(queue) > 0; depth++ {
		next := make([]string, 0, 4)
		for _, mediaID := range queue {
			if mediaID == "" || seen[mediaID] {
				continue
			}
			seen[mediaID] = true
			for _, record := range records {
				if record.MediaID != mediaID {
					continue
				}
				chain = append(chain, record)
				next = append(next, record.ParentMediaID)
				break
			}
		}
		queue = next
	}
	return matches, chain, nil
}
