package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 本文件实现「通用可配置来源」的持久化：把声明式来源规格写在
// <日志根>/config/sources.json，由采集器读取。写入者仍是本插件，采集器只读。
//
// 规格结构与采集器的 genericSource 对齐（id/label/root/glob/format/...），
// 这里按不透明 JSON 存取，不重复定义字段，避免两侧结构漂移。

const sourcesConfigSchema = "work-log-sources.v1"

type sourcesConfigFile struct {
	SchemaVersion string           `json:"schema_version"`
	UpdatedAt     string           `json:"updated_at"`
	Sources       []map[string]any `json:"sources"`
}

func sourcesConfigPath(root string) string {
	return filepath.Join(root, "config", "sources.json")
}

func readSourcesConfig(root string) (sourcesConfigFile, error) {
	body, err := os.ReadFile(sourcesConfigPath(root))
	if errors.Is(err, os.ErrNotExist) {
		return sourcesConfigFile{SchemaVersion: sourcesConfigSchema, Sources: []map[string]any{}}, nil
	}
	if err != nil {
		return sourcesConfigFile{}, errors.New("来源配置读取失败")
	}
	var cfg sourcesConfigFile
	if err := json.Unmarshal(body, &cfg); err != nil {
		return sourcesConfigFile{}, errors.New("来源配置不是合法 JSON")
	}
	cfg.SchemaVersion = sourcesConfigSchema
	if cfg.Sources == nil {
		cfg.Sources = []map[string]any{}
	}
	return cfg, nil
}

func writeSourcesConfig(root string, cfg sourcesConfigFile) error {
	cfg.SchemaVersion = sourcesConfigSchema
	cfg.UpdatedAt = time.Now().Format(time.RFC3339)
	if cfg.Sources == nil {
		cfg.Sources = []map[string]any{}
	}
	payload, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return errors.New("来源配置序列化失败")
	}
	dir := filepath.Dir(sourcesConfigPath(root))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return errors.New("来源配置目录创建失败")
	}
	return os.WriteFile(sourcesConfigPath(root), append(payload, '\n'), 0o644)
}

func sourceListCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	cfg, err := readSourcesConfig(root)
	if err != nil {
		return nil, err
	}
	path := sourcesConfigPath(root)
	_, statErr := os.Stat(path)
	return map[string]any{
		"log_root":    root,
		"config_path": path,
		"exists":      statErr == nil,
		"sources":     cfg.Sources,
	}, nil
}

// sourceSaveCap 保存来源配置。mode=merge（默认）按 id 合并，replace 整体替换。
func sourceSaveCap(in input) (any, error) {
	root, err := logRoot(in.LogRoot)
	if err != nil {
		return nil, err
	}
	cfg, err := readSourcesConfig(root)
	if err != nil {
		return nil, err
	}
	incoming := make([]map[string]any, 0, len(in.Sources))
	for _, spec := range in.Sources {
		if spec == nil {
			continue
		}
		if strings.TrimSpace(stringValue(spec["id"])) == "" {
			return nil, errors.New("每个来源都需要 id")
		}
		incoming = append(incoming, spec)
	}
	if strings.EqualFold(strings.TrimSpace(in.Mode), "replace") {
		cfg.Sources = incoming
	} else {
		index := map[string]int{}
		for i, spec := range cfg.Sources {
			index[strings.ToLower(strings.TrimSpace(stringValue(spec["id"])))] = i
		}
		for _, spec := range incoming {
			key := strings.ToLower(strings.TrimSpace(stringValue(spec["id"])))
			if at, ok := index[key]; ok {
				cfg.Sources[at] = spec
			} else {
				cfg.Sources = append(cfg.Sources, spec)
				index[key] = len(cfg.Sources) - 1
			}
		}
	}
	if err := writeSourcesConfig(root, cfg); err != nil {
		return nil, err
	}
	return map[string]any{
		"log_root":    root,
		"config_path": sourcesConfigPath(root),
		"count":       len(cfg.Sources),
		"sources":     cfg.Sources,
	}, nil
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}
