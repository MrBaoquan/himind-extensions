package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// library.items 浏览某个资源库/精选库的内容，供「库详情」使用。
// 精选库条目本身没有缩略图，返回 candidate_id 让界面复用源素材的缩略图。

type libraryItemsInput struct {
	LibraryID string `json:"library_id"`
	Limit     int    `json:"limit"`
	Offset    int    `json:"offset"`
}

func listLibraryItems(in libraryItemsInput) (any, error) {
	libraryID := strings.TrimSpace(in.LibraryID)
	if libraryID == "" {
		return nil, fmt.Errorf("请指定资源库")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 60
	}
	if limit > 200 {
		limit = 200
	}
	offset := in.Offset
	if offset < 0 {
		offset = 0
	}
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	library, err := getLibraryWithDB(db, libraryID)
	if err != nil {
		return nil, err
	}
	var total int
	if err := db.QueryRow(`SELECT COUNT(*) FROM library_items WHERE library_id=? AND deleted_at=''`, libraryID).Scan(&total); err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT id,COALESCE(relative_path,''),COALESCE(local_path,''),COALESCE(kind,''),COALESCE(title,''),
		COALESCE(bytes,0),COALESCE(metadata_json,'{}'),COALESCE(content_key,''),COALESCE(origin_selection_id,'')
		FROM library_items WHERE library_id=? AND deleted_at='' ORDER BY relative_path,id LIMIT ? OFFSET ?`, libraryID, limit, offset)
	if err != nil {
		return nil, err
	}
	type row struct {
		id, relative, local, kind, title string
		bytes                            int64
		meta, contentKey, origin         string
	}
	collected := []row{}
	for rows.Next() {
		var item row
		if err := rows.Scan(&item.id, &item.relative, &item.local, &item.kind, &item.title, &item.bytes, &item.meta, &item.contentKey, &item.origin); err != nil {
			rows.Close()
			return nil, err
		}
		collected = append(collected, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(collected))
	candidateIDs := []string{}
	for _, item := range collected {
		payload := map[string]any{}
		_ = json.Unmarshal([]byte(item.meta), &payload)
		candidateID := stringValue(payload["selection_asset_id"])
		if candidateID != "" {
			candidateIDs = append(candidateIDs, candidateID)
		}
		items = append(items, map[string]any{
			"id": item.id, "title": item.title, "kind": item.kind,
			"entry_type": "library", "library_id": library.ID, "library_kind": library.Kind,
			"relative_path": item.relative, "path": item.local, "bytes": item.bytes,
			"width": intValue(payload["width"]), "height": intValue(payload["height"]),
			"duration_ms":         int64(intValue(payload["duration_ms"])),
			"codec":               stringValue(payload["codec"]),
			"candidate_id":        candidateID,
			"content_key":         item.contentKey,
			"origin_selection_id": item.origin,
			"tags":                []string{},
			"rating":              0,
		})
	}
	// 标签/评分按源素材的 asset id 存；批量取一次，避免逐行查。
	if len(candidateIDs) > 0 {
		placeholders, args := placeholders(candidateIDs)
		if rows, err := db.Query(`SELECT asset_id,COALESCE(tags_json,'[]'),COALESCE(rating,0)
			FROM user_asset_state WHERE asset_id IN (`+placeholders+`)`, args...); err == nil {
			state := map[string]map[string]any{}
			for rows.Next() {
				var assetID, tagsJSON string
				var rating int
				if err := rows.Scan(&assetID, &tagsJSON, &rating); err == nil {
					tags := []string{}
					_ = json.Unmarshal([]byte(tagsJSON), &tags)
					state[assetID] = map[string]any{"tags": tags, "rating": rating}
				}
			}
			rows.Close()
			for _, item := range items {
				candidateID := item["candidate_id"].(string)
				if value, ok := state[candidateID]; ok {
					item["tags"] = value["tags"]
					item["rating"] = value["rating"]
				}
			}
		}
	}
	return map[string]any{
		"library": map[string]any{
			"id": library.ID, "name": library.Name, "kind": library.Kind,
			"root_path": library.RootURI, "asset_count": library.AssetCount, "state": library.State,
			"origin_selection_id": library.OriginSelectionID,
		},
		"items": items, "total": total, "offset": offset, "limit": limit,
	}, nil
}

// libraryItemsCapability 供 main.go 直接挂到 JSON-RPC 分发。
var _ = sql.ErrNoRows
