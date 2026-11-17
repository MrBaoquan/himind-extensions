package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// AI 打标缓存：同一素材 + 配置 + prompt 版本只调用一次模型。
// 结果随后写入标签；缓存本身不影响检索。

type aiCacheGetInput struct {
	AssetID       string `json:"asset_id"`
	Profile       string `json:"profile"`
	Model         string `json:"model"`
	PromptVersion string `json:"prompt_version"`
}

type aiCacheSetInput struct {
	AssetID       string         `json:"asset_id"`
	Profile       string         `json:"profile"`
	Model         string         `json:"model"`
	PromptVersion string         `json:"prompt_version"`
	Result        map[string]any `json:"result"`
	Tokens        int            `json:"tokens"`
}

func ensureAITagCache(db *sql.DB) error {
	var applied int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE name='v3_ai_tag_cache_v1'`).Scan(&applied); err != nil {
		return err
	}
	if applied > 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
CREATE TABLE IF NOT EXISTS ai_tag_cache (
  asset_id TEXT NOT NULL,
  profile TEXT NOT NULL,
  model TEXT NOT NULL,
  prompt_version TEXT NOT NULL,
  result_json TEXT NOT NULL,
  tokens INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  PRIMARY KEY(asset_id, profile, model, prompt_version)
);`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations(name,applied_at) VALUES('v3_ai_tag_cache_v1',?)`, nowRFC3339()); err != nil {
		return err
	}
	return tx.Commit()
}

func getAITagCache(in aiCacheGetInput) (any, error) {
	assetID := strings.TrimSpace(in.AssetID)
	if assetID == "" {
		return nil, fmt.Errorf("请指定资产")
	}
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var resultJSON, createdAt string
	var tokens int
	err = db.QueryRow(`SELECT result_json,tokens,created_at FROM ai_tag_cache
		WHERE asset_id=? AND profile=? AND model=? AND prompt_version=?`,
		assetID, strings.TrimSpace(in.Profile), strings.TrimSpace(in.Model), strings.TrimSpace(in.PromptVersion)).
		Scan(&resultJSON, &tokens, &createdAt)
	if err == sql.ErrNoRows {
		return map[string]any{"hit": false}, nil
	}
	if err != nil {
		return nil, err
	}
	var result map[string]any
	_ = json.Unmarshal([]byte(resultJSON), &result)
	return map[string]any{"hit": true, "result": result, "tokens": tokens, "created_at": createdAt}, nil
}

func setAITagCache(in aiCacheSetInput) (any, error) {
	assetID := strings.TrimSpace(in.AssetID)
	if assetID == "" {
		return nil, fmt.Errorf("请指定资产")
	}
	if in.Result == nil {
		return nil, fmt.Errorf("缺少缓存结果")
	}
	encoded, err := json.Marshal(in.Result)
	if err != nil {
		return nil, err
	}
	if len(encoded) > 64*1024 {
		return nil, fmt.Errorf("缓存结果过大")
	}
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO ai_tag_cache(asset_id,profile,model,prompt_version,result_json,tokens,created_at)
		VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(asset_id,profile,model,prompt_version) DO UPDATE SET
			result_json=excluded.result_json,
			tokens=excluded.tokens,
			created_at=CASE
				WHEN ai_tag_cache.result_json=excluded.result_json AND ai_tag_cache.tokens=excluded.tokens
				THEN ai_tag_cache.created_at
				ELSE excluded.created_at
			END`,
		assetID, strings.TrimSpace(in.Profile), strings.TrimSpace(in.Model), strings.TrimSpace(in.PromptVersion),
		string(encoded), in.Tokens, nowRFC3339()); err != nil {
		return nil, err
	}
	return map[string]any{"saved": true}, nil
}
