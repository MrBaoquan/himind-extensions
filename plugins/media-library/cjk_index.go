package main

import (
	"database/sql"
	"encoding/json"
	"strings"
	"unicode"
)

type cjkEntry struct {
	entryType, id, kind, updatedAt, content string
} // CJK 逐字索引：解决“2 个汉字查询只能 LIKE 全表”的问题。
// 标题/文件名/相对路径/标签/分类按单字展开写入 FTS，查询编译为连续字短语。
// 例如“城市” → cjk MATCH '"城 市"'。

func ensureCJKIndex(db *sql.DB) error {
	var applied int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE name='v3_cjk_v1'`).Scan(&applied); err != nil {
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
CREATE VIRTUAL TABLE IF NOT EXISTS search_cjk USING fts5(
  entry_type UNINDEXED,
  entry_id UNINDEXED,
  kind UNINDEXED,
  updated_at UNINDEXED,
  cjk,
  tokenize='unicode61 remove_diacritics 2'
);`); err != nil {
		return err
	}
	if err := backfillCJK(tx); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO search_cjk(search_cjk) VALUES('optimize')`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations(name,applied_at) VALUES('v3_cjk_v1',?)`, nowRFC3339()); err != nil {
		return err
	}
	return tx.Commit()
}

func backfillCJK(tx *sql.Tx) error {
	rows, err := tx.Query(`SELECT c.id,c.kind,c.updated_at,c.name,c.relative_path,COALESCE(u.tags_json,'[]')
		FROM candidates c LEFT JOIN user_asset_state u ON u.asset_id=c.id`)
	if err != nil {
		return err
	}
	entries := []cjkEntry{}
	for rows.Next() {
		var id, kind, updatedAt, name, relativePath, tagsJSON string
		if err := rows.Scan(&id, &kind, &updatedAt, &name, &relativePath, &tagsJSON); err != nil {
			rows.Close()
			return err
		}
		entries = append(entries, cjkEntry{
			entryType: "candidate", id: id, kind: kind, updatedAt: updatedAt,
			content: spaceCJK(strings.Join([]string{name, relativePath, tagsText(tagsJSON)}, " ")),
		})
		if len(entries) >= 500 {
			if err := insertCJKEntries(tx, entries); err != nil {
				rows.Close()
				return err
			}
			entries = entries[:0]
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := insertCJKEntries(tx, entries); err != nil {
		return err
	}
	libraryRows, err := tx.Query(`SELECT id,kind,updated_at,title,relative_path,COALESCE(metadata_json,'{}')
		FROM library_items`)
	if err != nil {
		return err
	}
	libraryEntries := []cjkEntry{}
	for libraryRows.Next() {
		var id, kind, updatedAt, title, relativePath, metadata string
		if err := libraryRows.Scan(&id, &kind, &updatedAt, &title, &relativePath, &metadata); err != nil {
			libraryRows.Close()
			return err
		}
		var parsed struct {
			Tags       []string `json:"tags"`
			Collection string   `json:"collection"`
		}
		_ = json.Unmarshal([]byte(metadata), &parsed)
		libraryEntries = append(libraryEntries, cjkEntry{
			entryType: "library", id: id, kind: kind, updatedAt: updatedAt,
			content: spaceCJK(strings.Join([]string{title, relativePath, strings.Join(parsed.Tags, " "), parsed.Collection}, " ")),
		})
		if len(libraryEntries) >= 500 {
			if err := insertCJKEntries(tx, libraryEntries); err != nil {
				libraryRows.Close()
				return err
			}
			libraryEntries = libraryEntries[:0]
		}
	}
	if err := libraryRows.Close(); err != nil {
		return err
	}
	return insertCJKEntries(tx, libraryEntries)
}

func insertCJKEntries(tx *sql.Tx, entries []cjkEntry) error {
	if len(entries) == 0 {
		return nil
	}
	statement, err := tx.Prepare(`INSERT INTO search_cjk(entry_type,entry_id,kind,updated_at,cjk) VALUES(?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer statement.Close()
	for _, entry := range entries {
		if _, err := statement.Exec(entry.entryType, entry.id, entry.kind, entry.updatedAt, entry.content); err != nil {
			return err
		}
	}
	return nil
}

func tagsText(tagsJSON string) string {
	var tags []string
	if err := json.Unmarshal([]byte(tagsJSON), &tags); err != nil {
		return ""
	}
	return strings.Join(tags, " ")
}

func spaceCJK(value string) string {
	var builder strings.Builder
	lastSpace := true
	for _, r := range value {
		if unicode.Is(unicode.Han, r) {
			if !lastSpace {
				builder.WriteRune(' ')
			}
			builder.WriteRune(r)
			builder.WriteRune(' ')
			lastSpace = true
			continue
		}
		if unicode.IsSpace(r) {
			if !lastSpace {
				builder.WriteRune(' ')
				lastSpace = true
			}
			continue
		}
		builder.WriteRune(r)
		lastSpace = false
	}
	return strings.TrimSpace(builder.String())
}

func containsHan(value string) bool {
	for _, r := range value {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

func cjkMatchQuery(query string) string {
	parts := []string{}
	for _, field := range strings.Fields(query) {
		if containsHan(field) {
			spaced := spaceCJK(field)
			if spaced == "" {
				continue
			}
			parts = append(parts, `"`+strings.ReplaceAll(spaced, `"`, `""`)+`"`)
			continue
		}
		escaped := strings.ReplaceAll(field, `"`, `""`)
		parts = append(parts, `"`+escaped+`"*`)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " AND ")
}
