package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func searchBGMAudio(db *sql.DB, in resourceSearchInput, cursor *searchPageCursor, limit int) (any, error) {
	where := []string{"li.deleted_at=''", "li.kind='audio'", "EXISTS(SELECT 1 FROM audio_derivations d WHERE d.asset_id=li.id AND d.purpose='bgm')"}
	args := []any{}
	if in.Scope == "sources" {
		where = append(where, "0=1")
	}
	if len(in.Kinds) > 0 {
		found := false
		for _, kind := range in.Kinds {
			if kind == "audio" {
				found = true
			}
		}
		if !found {
			where = append(where, "0=1")
		}
	}
	if in.Folder != "" || in.MinDimension > 0 {
		where = append(where, "0=1")
	}
	if in.MinDurationMS > 0 {
		where = append(where, "COALESCE(json_extract(li.metadata_json,'$.duration_ms'),0)>=?")
		args = append(args, in.MinDurationMS)
	}
	if in.MaxDurationMS > 0 {
		where = append(where, "COALESCE(json_extract(li.metadata_json,'$.duration_ms'),0)>0 AND json_extract(li.metadata_json,'$.duration_ms')<=?")
		args = append(args, in.MaxDurationMS)
	}
	if in.LibraryID != "" {
		where = append(where, "li.library_id=?")
		args = append(args, in.LibraryID)
	}
	if q := strings.TrimSpace(in.Query); q != "" {
		where = append(where, "(li.title LIKE ? OR EXISTS(SELECT 1 FROM audio_derivations d JOIN music_tracks m ON m.id=d.music_id WHERE d.asset_id=li.id AND (m.title LIKE ? OR m.author LIKE ?)))")
		for range 3 {
			args = append(args, "%"+q+"%")
		}
	}
	if cursor != nil {
		if cursor.Mode != "recent" {
			return nil, fmt.Errorf("筛选已变化，请重新搜索")
		}
		where = append(where, "(li.updated_at<? OR (li.updated_at=? AND li.id>?))")
		args = append(args, cursor.UpdatedAt, cursor.UpdatedAt, cursor.EntryID)
	}
	args = append(args, limit)
	rows, err := db.Query(`SELECT li.id,li.library_id,li.title,li.local_path,li.relative_path,li.bytes,li.updated_at,li.source_provider,li.source_url,li.license_name,li.metadata_json FROM library_items li WHERE `+strings.Join(where, " AND ")+` ORDER BY li.updated_at DESC,li.id LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	items := []resourceSearchItem{}
	for rows.Next() {
		item := resourceSearchItem{EntryType: "library", Kind: "audio", AudioPurpose: "bgm", CursorMode: "recent", Availability: "missing"}
		var meta string
		if err = rows.Scan(&item.ID, &item.LibraryID, &item.Title, &item.Path, &item.RelativePath, &item.Bytes, &item.UpdatedAt, &item.Source, &item.SourceURL, &item.LicenseName, &meta); err != nil {
			rows.Close()
			return nil, err
		}
		if st, e := os.Stat(item.Path); e == nil && st.Mode().IsRegular() {
			item.Availability = "available"
		}
		applyLibraryMetadata(&item, meta)
		var d struct {
			DurationMS int64 `json:"duration_ms"`
		}
		_ = json.Unmarshal([]byte(meta), &d)
		item.DurationMS = d.DurationMS
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	annotateLibraryKinds(db, items)
	next := ""
	if len(items) == limit {
		next = encodeSearchCursor(items[len(items)-1])
	}
	return map[string]any{"items": items, "total": len(items), "next_cursor": next, "audio_purpose": "bgm", "scope": "library"}, nil
}
