package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MrBaoquan/himind-extensions/internal/mediadownload"
)

type remoteResource struct {
	Engagement *mediadownload.Engagement `json:"engagement,omitempty"`
	CoverID    string                    `json:"cover_id,omitempty"`
	ID         string                    `json:"remote_id"`
	Provider   string                    `json:"provider"`
	ProviderID string                    `json:"provider_id"`
	SourceURL  string                    `json:"source_url"`
	Kind       string                    `json:"kind"`
	Title      string                    `json:"title"`
	Author     string                    `json:"author,omitempty"`
	PreviewURL string                    `json:"preview_url,omitempty"`
	License    string                    `json:"license,omitempty"`
	Duration   float64                   `json:"duration_seconds,omitempty"`
	Note       string                    `json:"note,omitempty"`
	Tags       []string                  `json:"tags"`
	SavedAt    string                    `json:"saved_at,omitempty"`
}

func saveBookmark(in remoteResource) (any, error) {
	return saveBookmarkFields(in, in.Note != "", in.Tags != nil)
}

// Saving a search result refreshes work metadata without overwriting the user's
// local organization. Only fields explicitly supplied by an editor are changed.
func saveBookmarkFields(in remoteResource, noteProvided, tagsProvided bool) (any, error) {
	in.CoverID = ""
	in.Engagement = nil
	provider, providerID, canonical, err := canonicalRemoteReference(in.SourceURL)
	if err != nil {
		return nil, err
	}
	if len([]rune(in.Title)) > 300 || len([]rune(in.Note)) > 2000 {
		return nil, fmt.Errorf("标题或备注过长，请缩短后保存")
	}
	in.ID, in.Provider, in.ProviderID, in.SourceURL = stableID("remote", provider+":"+providerID), provider, providerID, canonical
	if in.Kind == "" {
		in.Kind = "video"
	}
	if in.Kind != "video" && in.Kind != "audio" && in.Kind != "image" {
		return nil, fmt.Errorf("素材类型无效")
	}
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" {
		in.Title = providerID
	}
	in.Tags = normalizeAdoptionTags(in.Tags)
	// Platform thumbnails are transient and may contain signed CDN parameters.
	// Durable bookmarks retain only stable work metadata until a private cache
	// is available; they never persist a caller-provided preview URL.
	in.PreviewURL = ""
	in.SavedAt = nowRFC3339()
	metadata, _ := json.Marshal(in)
	tags, _ := json.Marshal(in.Tags)
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO remote_resources(id,provider,provider_id,source_url,metadata_json,updated_at) VALUES(?,?,?,?,?,?)
 ON CONFLICT(provider,provider_id) DO UPDATE SET source_url=excluded.source_url,metadata_json=excluded.metadata_json,updated_at=excluded.updated_at`, in.ID, provider, providerID, canonical, string(metadata), in.SavedAt); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`INSERT INTO remote_bookmarks(remote_id,note,tags_json,created_at,updated_at) VALUES(?,?,?,?,?)
 ON CONFLICT(remote_id) DO UPDATE SET note=CASE WHEN ? THEN excluded.note ELSE remote_bookmarks.note END,
 tags_json=CASE WHEN ? THEN excluded.tags_json ELSE remote_bookmarks.tags_json END,updated_at=excluded.updated_at`, in.ID, in.Note, string(tags), in.SavedAt, in.SavedAt, noteProvided, tagsProvided); err != nil {
		return nil, err
	}
	var storedTags string
	if err = tx.QueryRow(`SELECT note,tags_json,created_at FROM remote_bookmarks WHERE remote_id=?`, in.ID).Scan(&in.Note, &storedTags, &in.SavedAt); err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(storedTags), &in.Tags); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return in, nil
}

func listBookmarks(query string) (any, error) {
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT r.metadata_json,b.note,b.tags_json,b.created_at FROM remote_bookmarks b JOIN remote_resources r ON r.id=b.remote_id
 WHERE ?='' OR instr(lower(r.metadata_json||' '||b.note||' '||b.tags_json),lower(?))>0 ORDER BY b.updated_at DESC LIMIT 200`, strings.TrimSpace(query), strings.TrimSpace(query))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []remoteResource{}
	for rows.Next() {
		var metadata, note, tags, created string
		if err = rows.Scan(&metadata, &note, &tags, &created); err != nil {
			return nil, err
		}
		var resource remoteResource
		if err = json.Unmarshal([]byte(metadata), &resource); err != nil {
			return nil, err
		}
		resource.Note, resource.SavedAt = note, created
		if err = json.Unmarshal([]byte(tags), &resource.Tags); err != nil {
			return nil, err
		}
		items = append(items, resource)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return map[string]any{"items": items, "count": len(items)}, nil
}

func removeBookmark(id string) (any, error) {
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	result, err := db.Exec(`DELETE FROM remote_bookmarks WHERE remote_id=?`, id)
	if err != nil {
		return nil, err
	}
	count, _ := result.RowsAffected()
	return map[string]any{"removed": count, "remote_id": id}, nil
}
