package main

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

const searchBM25Weights = "0.0,0.0,0.0,0.0,0.0,8.0,5.0,2.0,6.0,6.0,1.0,1.0"

type resourceSearchInput struct {
	AudioPurpose  string   `json:"audio_purpose,omitempty"`
	Query         string   `json:"query"`
	Kinds         []string `json:"kinds"`
	Scope         string   `json:"scope"`
	LibraryID     string   `json:"library_id"`
	Limit         int      `json:"limit"`
	Cursor        string   `json:"cursor"`
	Folder        string   `json:"folder"`
	MinDimension  int      `json:"min_dimension"`
	MinDurationMS int64    `json:"min_duration_ms"`
	MaxDurationMS int64    `json:"max_duration_ms"`
}

type assetFilters struct {
	Folder        string
	MinDimension  int
	MinDurationMS int64
	MaxDurationMS int64
}

func (filters assetFilters) active() bool {
	return strings.TrimSpace(filters.Folder) != "" || filters.MinDimension > 0 ||
		filters.MinDurationMS > 0 || filters.MaxDurationMS > 0
}

func (filters assetFilters) where(prefix string) ([]string, []any) {
	clauses := []string{}
	args := []any{}
	column := func(name string) string {
		if prefix == "" {
			return name
		}
		return prefix + "." + name
	}
	if folder := strings.TrimSpace(filters.Folder); folder != "" {
		clauses = append(clauses, column("folder")+"=?")
		args = append(args, folder)
	}
	if filters.MinDimension > 0 {
		clauses = append(clauses, "max("+column("width")+","+column("height")+")>=?")
		args = append(args, filters.MinDimension)
	}
	if filters.MinDurationMS > 0 {
		clauses = append(clauses, column("duration_ms")+">=?")
		args = append(args, filters.MinDurationMS)
	}
	if filters.MaxDurationMS > 0 {
		clauses = append(clauses, column("duration_ms")+"<=?")
		args = append(args, filters.MaxDurationMS)
	}
	return clauses, args
}

type resourceSearchItem struct {
	AudioPurpose string   `json:"audio_purpose,omitempty"`
	AudioRole    string   `json:"audio_role,omitempty"`
	EntryType    string   `json:"entry_type"`
	ID           string   `json:"id"`
	CandidateID  string   `json:"candidate_id,omitempty"`
	LibraryID    string   `json:"library_id,omitempty"`
	Title        string   `json:"title"`
	Kind         string   `json:"kind"`
	Path         string   `json:"path,omitempty"`
	RelativePath string   `json:"relative_path,omitempty"`
	Folder       string   `json:"folder,omitempty"`
	Bytes        int64    `json:"bytes,omitempty"`
	Width        int      `json:"width,omitempty"`
	Height       int      `json:"height,omitempty"`
	DurationMS   int64    `json:"duration_ms,omitempty"`
	FPS          float64  `json:"fps,omitempty"`
	BitDepth     int      `json:"bit_depth,omitempty"`
	Codec        string   `json:"codec,omitempty"`
	HDR          string   `json:"hdr,omitempty"`
	CapturedAt   string   `json:"captured_at,omitempty"`
	ProbeState   string   `json:"probe_state,omitempty"`
	ThumbState   string   `json:"thumb_state,omitempty"`
	Source       string   `json:"source"`
	LicenseName  string   `json:"license_name,omitempty"`
	SourceURL    string   `json:"source_url,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	Collection   string   `json:"collection,omitempty"`
	Availability string   `json:"availability"`
	UpdatedAt    string   `json:"updated_at,omitempty"`
	AlsoLocal    bool     `json:"also_local,omitempty"`
	ContentKey   string   `json:"content_key,omitempty"`
	Rank         float64  `json:"-"`
	CursorMode   string   `json:"-"`
	LibraryKind  string   `json:"library_kind,omitempty"`
}

type searchPageCursor struct {
	Mode      string  `json:"mode"`
	Rank      float64 `json:"rank,omitempty"`
	UpdatedAt string  `json:"updated_at"`
	EntryID   string  `json:"entry_id"`
}

func searchResources(in resourceSearchInput) (any, error) {
	query := cleanText(in.Query)
	scope := strings.ToLower(strings.TrimSpace(in.Scope))
	if scope == "" {
		scope = "all"
	}
	if scope != "all" && scope != "library" && scope != "sources" {
		return nil, fmt.Errorf("scope 必须是 all、library 或 sources")
	}
	for _, kind := range in.Kinds {
		if kind != "image" && kind != "video" && kind != "audio" {
			return nil, fmt.Errorf("不支持的素材类型: %s", kind)
		}
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 60
	}
	if limit > 200 {
		limit = 200
	}
	cursor, err := decodeSearchCursor(in.Cursor)
	if err != nil {
		return nil, err
	}
	filters := assetFilters{
		Folder:        in.Folder,
		MinDimension:  in.MinDimension,
		MinDurationMS: in.MinDurationMS,
		MaxDurationMS: in.MaxDurationMS,
	}
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if in.AudioPurpose != "" {
		if in.AudioPurpose != "bgm" {
			return nil, fmt.Errorf("不支持的音频用途")
		}
		return searchBGMAudio(db, in, cursor, limit)
	}

	items := []resourceSearchItem{}
	ftsAvailable := false
	if query != "" && len([]rune(query)) <= 2 && containsHan(query) && (cursor == nil || cursor.Mode == "recent") {
		cjkItems, cjkErr := searchCJKIndex(db, query, scope, in.Kinds, strings.TrimSpace(in.LibraryID), filters, limit, cursor)
		if cjkErr == nil && len(cjkItems) > 0 {
			ftsAvailable = true
			items = cjkItems
		} else {
			items, err = searchLIKEOrRecent(db, query, scope, in.Kinds, strings.TrimSpace(in.LibraryID), filters, limit, cursor)
			if err != nil {
				return nil, err
			}
		}
	} else if query != "" && len([]rune(query)) >= 3 {
		if cursor == nil || cursor.Mode == "window" {
			windowItems, windowErr := searchFTSWindow(db, query, scope, in.Kinds, strings.TrimSpace(in.LibraryID), filters, limit, cursor)
			if windowErr == nil && len(windowItems) >= limit {
				ftsAvailable = true
				items = windowItems
			} else if cursor != nil {
				if windowErr != nil {
					return nil, windowErr
				}
				items = windowItems
			}
		}
		if len(items) == 0 && (cursor == nil || cursor.Mode == "rank") {
			ftsItems, ftsErr := searchFTSV2(db, query, scope, in.Kinds, strings.TrimSpace(in.LibraryID), filters, limit, cursor)
			if ftsErr == nil {
				ftsAvailable = true
				items = ftsItems
			}
			if ftsErr != nil || len(items) == 0 {
				items, err = searchLIKEOrRecent(db, query, scope, in.Kinds, strings.TrimSpace(in.LibraryID), filters, limit, cursor)
				if err != nil {
					return nil, err
				}
			}
		}
	} else {
		items, err = searchLIKEOrRecent(db, query, scope, in.Kinds, strings.TrimSpace(in.LibraryID), filters, limit, cursor)
		if err != nil {
			return nil, err
		}
		ftsAvailable = true
	}
	if len(items) > limit {
		items = items[:limit]
	}
	annotateLibraryKinds(db, items)
	items = dedupeByContent(items)
	nextCursor := ""
	if len(items) == limit {
		nextCursor = encodeSearchCursor(items[len(items)-1])
	}
	return map[string]any{
		"query":         query,
		"scope":         scope,
		"items":         items,
		"total":         len(items),
		"fts_available": ftsAvailable,
		"next_cursor":   nextCursor,
	}, nil
}

func searchFTSV2(db *sql.DB, query string, scope string, kinds []string, libraryID string, filters assetFilters, limit int, cursor *searchPageCursor) ([]resourceSearchItem, error) {
	where := []string{"search_fts_v2 MATCH ?"}
	args := []any{ftsPrefixQuery(query)}
	if scope == "library" {
		where = append(where, "entry_type='library'")
	} else if scope == "sources" {
		where = append(where, "entry_type='candidate'")
	}
	if len(kinds) > 0 {
		placeholder, kindArgs := placeholders(kinds)
		where = append(where, "kind IN ("+placeholder+")")
		args = append(args, kindArgs...)
	}
	if libraryID != "" && scope != "sources" {
		where = append(where, "library_id=?")
		args = append(args, libraryID)
	}
	cursorWhere := []string{"1=1"}
	if cursor != nil {
		cursorWhere = append(cursorWhere, "(rank > ? OR (rank = ? AND updated_at < ?) OR (rank = ? AND updated_at = ? AND entry_id > ?))")
		args = append(args, cursor.Rank, cursor.Rank, cursor.UpdatedAt, cursor.Rank, cursor.UpdatedAt, cursor.EntryID)
	}
	extraWhere, extraArgs := filters.where("c")
	visible := "((h.entry_type='library' AND li.id IS NOT NULL) OR (h.entry_type='candidate' AND c.id IS NOT NULL AND c.missing=0))"
	if len(extraWhere) > 0 {
		visible = "h.entry_type='candidate' AND c.id IS NOT NULL AND c.missing=0 AND " + strings.Join(extraWhere, " AND ")
		args = append(args, extraArgs...)
	}
	args = append(args, limit)
	querySQL := `
WITH ranked AS MATERIALIZED (
  SELECT entry_type,entry_id,library_id,kind,updated_at,
         bm25(search_fts_v2, <BM25>) AS rank
  FROM search_fts_v2
  WHERE <WHERE>
),
hits AS MATERIALIZED (
  SELECT entry_type,entry_id,library_id,kind,updated_at,rank
  FROM ranked
  WHERE <CURSOR>
  ORDER BY rank ASC, updated_at DESC, entry_id ASC
  LIMIT ?
)
SELECT h.entry_type,h.entry_id,h.library_id,h.kind,h.updated_at,h.rank,
       COALESCE(li.title,c.name,''),
       COALESCE(li.relative_path,c.relative_path,''),
       COALESCE(li.local_path,c.path,''),
       COALESCE(li.license_name,''),
       COALESCE(li.source_provider,''),
       COALESCE(li.source_url,''),
       COALESCE(li.bytes,c.bytes,0),
       COALESCE(li.metadata_json,'{}'),
       COALESCE(c.folder,''),COALESCE(c.width,0),COALESCE(c.height,0),COALESCE(c.duration_ms,0),
       COALESCE(c.fps,0),COALESCE(c.bit_depth,0),COALESCE(c.codec,''),COALESCE(c.hdr,''),
       COALESCE(c.captured_at,''),COALESCE(c.probe_state,''),COALESCE(c.thumb_state,'')
FROM hits h
LEFT JOIN library_items li ON h.entry_type='library' AND li.id=h.entry_id
LEFT JOIN candidates c ON h.entry_type='candidate' AND c.id=h.entry_id
WHERE <VISIBLE>
ORDER BY h.rank ASC, h.updated_at DESC, h.entry_id ASC`
	querySQL = strings.Replace(querySQL, "<BM25>", searchBM25Weights, 1)
	querySQL = strings.Replace(querySQL, "<WHERE>", strings.Join(where, " AND "), 1)
	querySQL = strings.Replace(querySQL, "<CURSOR>", strings.Join(cursorWhere, " AND "), 1)
	querySQL = strings.Replace(querySQL, "<VISIBLE>", visible, 1)
	rows, err := db.Query(querySQL, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFTSDetailRows(rows)
}

func searchCJKIndex(db *sql.DB, query string, scope string, kinds []string, libraryID string, filters assetFilters, limit int, cursor *searchPageCursor) ([]resourceSearchItem, error) {
	where := []string{"search_cjk MATCH ?"}
	args := []any{cjkMatchQuery(query)}
	if scope == "library" {
		where = append(where, "entry_type='library'")
	} else if scope == "sources" {
		where = append(where, "entry_type='candidate'")
	}
	if len(kinds) > 0 {
		placeholder, kindArgs := placeholders(kinds)
		where = append(where, "kind IN ("+placeholder+")")
		args = append(args, kindArgs...)
	}
	cursorWhere := []string{"1=1"}
	if cursor != nil {
		cursorWhere = append(cursorWhere, "(updated_at < ? OR (updated_at = ? AND entry_id > ?))")
		args = append(args, cursor.UpdatedAt, cursor.UpdatedAt, cursor.EntryID)
	}
	args = append(args, limit)
	extraWhere, extraArgs := filters.where("c")
	visible := "((h.entry_type='library' AND li.id IS NOT NULL) OR (h.entry_type='candidate' AND c.id IS NOT NULL AND c.missing=0))"
	if libraryID != "" && scope != "sources" {
		visible += " AND (h.entry_type='candidate' OR li.library_id=?)"
		args = append(args, libraryID)
	}
	if len(extraWhere) > 0 {
		visible = "h.entry_type='candidate' AND c.id IS NOT NULL AND c.missing=0 AND " + strings.Join(extraWhere, " AND ")
		args = append(args, extraArgs...)
	}
	// 短中文查询常常命中大量素材（例如“城市”）。先按最近写入取 1000 条样本，
	// 不在全量命中上算 BM25，避免 50 万级出现数百毫秒。
	querySQL := `
WITH sampled AS MATERIALIZED (
  SELECT entry_type,entry_id,kind,updated_at
  FROM search_cjk
  WHERE <WHERE>
  ORDER BY rowid DESC
  LIMIT 1000
),
hits AS MATERIALIZED (
  SELECT * FROM sampled
  WHERE <CURSOR>
  ORDER BY updated_at DESC, entry_id ASC
  LIMIT ?
)
SELECT h.entry_type,h.entry_id,
       COALESCE(li.library_id,''),h.kind,h.updated_at,0 AS rank,
       COALESCE(li.title,c.name,''),
       COALESCE(li.relative_path,c.relative_path,''),
       COALESCE(li.local_path,c.path,''),
       COALESCE(li.license_name,''),
       COALESCE(li.source_provider,''),
       COALESCE(li.source_url,''),
       COALESCE(li.bytes,c.bytes,0),
       COALESCE(li.metadata_json,'{}'),
       COALESCE(c.folder,''),COALESCE(c.width,0),COALESCE(c.height,0),COALESCE(c.duration_ms,0),
       COALESCE(c.fps,0),COALESCE(c.bit_depth,0),COALESCE(c.codec,''),COALESCE(c.hdr,''),
       COALESCE(c.captured_at,''),COALESCE(c.probe_state,''),COALESCE(c.thumb_state,'')
FROM hits h
LEFT JOIN library_items li ON h.entry_type='library' AND li.id=h.entry_id
LEFT JOIN candidates c ON h.entry_type='candidate' AND c.id=h.entry_id
WHERE <VISIBLE>
ORDER BY h.updated_at DESC, h.entry_id ASC`
	querySQL = strings.Replace(querySQL, "<WHERE>", strings.Join(where, " AND "), 1)
	querySQL = strings.Replace(querySQL, "<CURSOR>", strings.Join(cursorWhere, " AND "), 1)
	querySQL = strings.Replace(querySQL, "<VISIBLE>", visible, 1)
	rows, err := db.Query(querySQL, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items, err := scanFTSDetailRows(rows)
	if err != nil {
		return nil, err
	}
	for index := range items {
		items[index].CursorMode = "recent"
	}
	return items, nil
}

func scanFTSDetailRows(rows *sql.Rows) ([]resourceSearchItem, error) {
	items := []resourceSearchItem{}
	for rows.Next() {
		var item resourceSearchItem
		var localPath, metadataJSON string
		var folder, codec, hdr, capturedAt, probeState, thumbState string
		var width, height, bitDepth int
		var durationMS int64
		var fps float64
		if err := rows.Scan(&item.EntryType, &item.ID, &item.LibraryID, &item.Kind, &item.UpdatedAt, &item.Rank,
			&item.Title, &item.RelativePath, &localPath, &item.LicenseName, &item.Source, &item.SourceURL, &item.Bytes, &metadataJSON,
			&folder, &width, &height, &durationMS, &fps, &bitDepth, &codec, &hdr, &capturedAt, &probeState, &thumbState); err != nil {
			return nil, err
		}
		item.Path = localPath
		item.Folder = folder
		item.Width = width
		item.Height = height
		item.DurationMS = durationMS
		item.FPS = fps
		item.BitDepth = bitDepth
		item.Codec = codec
		item.HDR = hdr
		item.CapturedAt = capturedAt
		item.ProbeState = probeState
		item.ThumbState = thumbState
		if item.EntryType == "library" {
			if item.Source == "" {
				item.Source = "library"
			}
			item.Availability = "missing"
			if info, statErr := os.Stat(localPath); statErr == nil && info.Mode().IsRegular() {
				item.Availability = "available"
			}
			applyLibraryMetadata(&item, metadataJSON)
		} else {
			item.Source = "local-folder"
			item.Availability = "available"
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func searchFTSWindow(db *sql.DB, query string, scope string, kinds []string, libraryID string, filters assetFilters, limit int, cursor *searchPageCursor) ([]resourceSearchItem, error) {
	items := []resourceSearchItem{}
	if scope == "all" || scope == "library" {
		found, err := searchLibraryFTSWindow(db, query, kinds, libraryID, filters, limit, cursor)
		if err != nil {
			return nil, err
		}
		items = append(items, found...)
	}
	if scope == "all" || scope == "sources" {
		found, err := searchCandidateFTSWindow(db, query, kinds, filters, limit, cursor)
		if err != nil {
			return nil, err
		}
		items = append(items, found...)
	}
	sort.SliceStable(items, func(left, right int) bool {
		if items[left].Rank != items[right].Rank {
			return items[left].Rank < items[right].Rank
		}
		if items[left].UpdatedAt != items[right].UpdatedAt {
			return items[left].UpdatedAt > items[right].UpdatedAt
		}
		return items[left].ID < items[right].ID
	})
	if len(items) > limit {
		items = items[:limit]
	}
	for index := range items {
		items[index].CursorMode = "window"
	}
	return items, nil
}

func searchLibraryFTSWindow(db *sql.DB, query string, kinds []string, libraryID string, filters assetFilters, limit int, cursor *searchPageCursor) ([]resourceSearchItem, error) {
	if filters.active() {
		// 资源库条目没有分辨率/时长/文件夹元数据，筛选时先不返回，避免误导。
		return []resourceSearchItem{}, nil
	}
	where := []string{"search_fts_v2 MATCH ?", "entry_type='library'"}
	args := []any{ftsPrefixQuery(query)}
	if libraryID != "" {
		where = append(where, "library_id=?")
		args = append(args, libraryID)
	}
	if len(kinds) > 0 {
		placeholder, kindArgs := placeholders(kinds)
		where = append(where, "kind IN ("+placeholder+")")
		args = append(args, kindArgs...)
	}
	args = append(args, 1000)
	cursorWhere := []string{"1=1"}
	if cursor != nil {
		cursorWhere = append(cursorWhere, "(rank > ? OR (rank = ? AND updated_at < ?) OR (rank = ? AND updated_at = ? AND entry_id > ?))")
		args = append(args, cursor.Rank, cursor.Rank, cursor.UpdatedAt, cursor.Rank, cursor.UpdatedAt, cursor.EntryID)
	}
	args = append(args, limit)
	querySQL := `
WITH sampled AS MATERIALIZED (
  SELECT entry_type,entry_id,library_id,kind,updated_at,
         bm25(search_fts_v2, <BM25>) AS rank
  FROM search_fts_v2
  WHERE <WHERE>
  ORDER BY rowid DESC
  LIMIT ?
),
hits AS MATERIALIZED (
  SELECT * FROM sampled
  WHERE <CURSOR>
  ORDER BY rank ASC, updated_at DESC, entry_id ASC
  LIMIT ?
)
SELECT h.entry_type,h.entry_id,li.library_id,h.kind,h.updated_at,h.rank,
       li.title,li.relative_path,li.local_path,li.license_name,li.source_provider,li.source_url,li.bytes,li.metadata_json,
       '',0,0,0,0,0,'','','','',''
FROM hits h
JOIN library_items li ON li.id=h.entry_id
ORDER BY h.rank ASC, h.updated_at DESC, h.entry_id ASC`
	querySQL = strings.Replace(querySQL, "<BM25>", searchBM25Weights, 1)
	querySQL = strings.Replace(querySQL, "<WHERE>", strings.Join(where, " AND "), 1)
	querySQL = strings.Replace(querySQL, "<CURSOR>", strings.Join(cursorWhere, " AND "), 1)
	rows, err := db.Query(querySQL, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFTSDetailRows(rows)
}

func searchCandidateFTSWindow(db *sql.DB, query string, kinds []string, filters assetFilters, limit int, cursor *searchPageCursor) ([]resourceSearchItem, error) {
	where := []string{"search_fts_v2 MATCH ?", "entry_type='candidate'"}
	args := []any{ftsPrefixQuery(query)}
	if len(kinds) > 0 {
		placeholder, kindArgs := placeholders(kinds)
		where = append(where, "kind IN ("+placeholder+")")
		args = append(args, kindArgs...)
	}
	args = append(args, 1000)
	cursorWhere := []string{"1=1"}
	if cursor != nil {
		cursorWhere = append(cursorWhere, "(rank > ? OR (rank = ? AND updated_at < ?) OR (rank = ? AND updated_at = ? AND entry_id > ?))")
		args = append(args, cursor.Rank, cursor.Rank, cursor.UpdatedAt, cursor.Rank, cursor.UpdatedAt, cursor.EntryID)
	}
	args = append(args, limit)
	extraWhere, extraArgs := filters.where("c")
	filterClause := "1=1"
	if len(extraWhere) > 0 {
		filterClause = strings.Join(extraWhere, " AND ")
		args = append(args, extraArgs...)
	}
	querySQL := `
WITH sampled AS MATERIALIZED (
  SELECT entry_type,entry_id,library_id,kind,updated_at,
         bm25(search_fts_v2, <BM25>) AS rank
  FROM search_fts_v2
  WHERE <WHERE>
  ORDER BY rowid DESC
  LIMIT ?
),
hits AS MATERIALIZED (
  SELECT * FROM sampled
  WHERE <CURSOR>
  ORDER BY rank ASC, updated_at DESC, entry_id ASC
  LIMIT ?
)
SELECT h.entry_type,h.entry_id,h.library_id,h.kind,h.updated_at,h.rank,
       c.name,c.relative_path,c.path,'','','',c.bytes,'{}',
       c.folder,c.width,c.height,c.duration_ms,c.fps,c.bit_depth,c.codec,c.hdr,c.captured_at,c.probe_state,c.thumb_state
FROM hits h
JOIN candidates c ON c.id=h.entry_id AND c.missing=0
WHERE <FILTER>
ORDER BY h.rank ASC, h.updated_at DESC, h.entry_id ASC`
	querySQL = strings.Replace(querySQL, "<BM25>", searchBM25Weights, 1)
	querySQL = strings.Replace(querySQL, "<WHERE>", strings.Join(where, " AND "), 1)
	querySQL = strings.Replace(querySQL, "<CURSOR>", strings.Join(cursorWhere, " AND "), 1)
	querySQL = strings.Replace(querySQL, "<FILTER>", filterClause, 1)
	rows, err := db.Query(querySQL, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFTSDetailRows(rows)
}

func searchLIKEOrRecent(db *sql.DB, query string, scope string, kinds []string, libraryID string, filters assetFilters, limit int, cursor *searchPageCursor) ([]resourceSearchItem, error) {
	items := []resourceSearchItem{}
	if scope == "all" || scope == "sources" {
		found, err := searchCandidatesFallback(db, query, kinds, filters, limit, cursor)
		if err != nil {
			return nil, err
		}
		items = append(items, found...)
	}
	if scope == "all" || scope == "library" {
		found, err := searchLibraryFallback(db, query, kinds, libraryID, filters, limit, cursor)
		if err != nil {
			return nil, err
		}
		items = append(items, found...)
	}
	sort.SliceStable(items, func(left, right int) bool {
		if items[left].UpdatedAt != items[right].UpdatedAt {
			return items[left].UpdatedAt > items[right].UpdatedAt
		}
		if items[left].EntryType != items[right].EntryType {
			return items[left].EntryType < items[right].EntryType
		}
		return items[left].ID < items[right].ID
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func searchCandidatesFallback(db *sql.DB, query string, kinds []string, filters assetFilters, limit int, cursor *searchPageCursor) ([]resourceSearchItem, error) {
	where := []string{"missing=0"}
	args := []any{}
	if query != "" {
		where = append(where, "(name LIKE ? OR relative_path LIKE ?)")
		like := "%" + query + "%"
		args = append(args, like, like)
	}
	if len(kinds) > 0 {
		placeholder, kindArgs := placeholders(kinds)
		where = append(where, "kind IN ("+placeholder+")")
		args = append(args, kindArgs...)
	}
	extraWhere, extraArgs := filters.where("")
	if len(extraWhere) > 0 {
		where = append(where, extraWhere...)
		args = append(args, extraArgs...)
	}
	if cursor != nil && cursor.Mode != "rank" {
		where = append(where, "(updated_at < ? OR (updated_at = ? AND id > ?))")
		args = append(args, cursor.UpdatedAt, cursor.UpdatedAt, cursor.EntryID)
	}
	args = append(args, limit)
	querySQL := `SELECT id,relative_path,name,kind,path,bytes,updated_at,
		folder,width,height,duration_ms,fps,bit_depth,codec,hdr,captured_at,probe_state,thumb_state
		FROM candidates WHERE <WHERE>
		ORDER BY updated_at DESC,id ASC LIMIT ?`
	querySQL = strings.Replace(querySQL, "<WHERE>", strings.Join(where, " AND "), 1)
	rows, err := db.Query(querySQL, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCandidateRows(rows)
}

func scanCandidateRows(rows *sql.Rows) ([]resourceSearchItem, error) {
	items := []resourceSearchItem{}
	for rows.Next() {
		var item resourceSearchItem
		var localPath string
		if err := rows.Scan(&item.ID, &item.RelativePath, &item.Title, &item.Kind, &localPath, &item.Bytes, &item.UpdatedAt,
			&item.Folder, &item.Width, &item.Height, &item.DurationMS, &item.FPS, &item.BitDepth, &item.Codec,
			&item.HDR, &item.CapturedAt, &item.ProbeState, &item.ThumbState); err != nil {
			return nil, err
		}
		item.EntryType = "candidate"
		item.Path = localPath
		item.Source = "local-folder"
		item.Availability = "available"
		items = append(items, item)
	}
	return items, rows.Err()
}

func searchLibraryFallback(db *sql.DB, query string, kinds []string, libraryID string, filters assetFilters, limit int, cursor *searchPageCursor) ([]resourceSearchItem, error) {
	if filters.active() {
		return []resourceSearchItem{}, nil
	}
	where := []string{"1=1"}
	args := []any{}
	if query != "" {
		where = append(where, "(title LIKE ? OR relative_path LIKE ? OR license_name LIKE ?)")
		like := "%" + query + "%"
		args = append(args, like, like, like)
	}
	if libraryID != "" {
		where = append(where, "library_id=?")
		args = append(args, libraryID)
	}
	if len(kinds) > 0 {
		placeholder, kindArgs := placeholders(kinds)
		where = append(where, "kind IN ("+placeholder+")")
		args = append(args, kindArgs...)
	}
	if cursor != nil && cursor.Mode != "rank" {
		where = append(where, "(adopted_at < ? OR (adopted_at = ? AND id > ?))")
		args = append(args, cursor.UpdatedAt, cursor.UpdatedAt, cursor.EntryID)
	}
	args = append(args, limit)
	querySQL := `SELECT id,library_id,relative_path,local_path,kind,title,license_name,source_provider,source_url,bytes,adopted_at,metadata_json,
		'',0,0,0,0,0,'','','','',''
		FROM library_items WHERE <WHERE>
		ORDER BY adopted_at DESC,id ASC LIMIT ?`
	querySQL = strings.Replace(querySQL, "<WHERE>", strings.Join(where, " AND "), 1)
	rows, err := db.Query(querySQL, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanLibraryRows(rows)
}

func scanLibraryRows(rows *sql.Rows) ([]resourceSearchItem, error) {
	items := []resourceSearchItem{}
	for rows.Next() {
		var item resourceSearchItem
		var localPath, metadataJSON, folder, codec, hdr, capturedAt, probeState, thumbState string
		var width, height, bitDepth int
		var durationMS int64
		var fps float64
		if err := rows.Scan(&item.ID, &item.LibraryID, &item.RelativePath, &localPath, &item.Kind, &item.Title, &item.LicenseName, &item.Source, &item.SourceURL, &item.Bytes, &item.UpdatedAt, &metadataJSON,
			&folder, &width, &height, &durationMS, &fps, &bitDepth, &codec, &hdr, &capturedAt, &probeState, &thumbState); err != nil {
			return nil, err
		}
		item.Folder = folder
		item.Width = width
		item.Height = height
		item.DurationMS = durationMS
		item.FPS = fps
		item.BitDepth = bitDepth
		item.Codec = codec
		item.HDR = hdr
		item.CapturedAt = capturedAt
		item.ProbeState = probeState
		item.ThumbState = thumbState
		item.EntryType = "library"
		item.Path = localPath
		item.Availability = "missing"
		if info, statErr := os.Stat(localPath); statErr == nil && info.Mode().IsRegular() {
			item.Availability = "available"
		}
		applyLibraryMetadata(&item, metadataJSON)
		items = append(items, item)
	}
	return items, rows.Err()
}

func applyLibraryMetadata(item *resourceSearchItem, metadataJSON string) {
	var metadata struct {
		AudioPurpose string   `json:"audio_purpose"`
		AudioRole    string   `json:"audio_role"`
		DurationMS   int64    `json:"duration_ms"`
		Tags         []string `json:"tags"`
		Collection   string   `json:"collection"`
	}
	if metadataJSON != "" {
		_ = json.Unmarshal([]byte(metadataJSON), &metadata)
	}
	item.Tags = metadata.Tags
	item.Collection = metadata.Collection
	item.AudioPurpose, item.AudioRole = metadata.AudioPurpose, metadata.AudioRole
	if item.DurationMS == 0 {
		item.DurationMS = metadata.DurationMS
	}
}

func placeholders(values []string) (string, []any) {
	parts := make([]string, 0, len(values))
	args := make([]any, 0, len(values))
	for _, value := range values {
		parts = append(parts, "?")
		args = append(args, value)
	}
	return strings.Join(parts, ","), args
}

func ftsPrefixQuery(query string) string {
	parts := []string{}
	for _, field := range strings.Fields(query) {
		escaped := strings.ReplaceAll(field, "\"", "\"\"")
		parts = append(parts, "\""+escaped+"\"*")
	}
	return strings.Join(parts, " AND ")
}

func decodeSearchCursor(value string) (*searchPageCursor, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return nil, fmt.Errorf("无效的搜索分页游标")
	}
	var cursor searchPageCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil || cursor.UpdatedAt == "" || cursor.EntryID == "" {
		return nil, fmt.Errorf("无效的搜索分页游标")
	}
	if cursor.Mode != "rank" && cursor.Mode != "window" && cursor.Mode != "recent" {
		return nil, fmt.Errorf("无效的搜索分页游标")
	}
	return &cursor, nil
}

func encodeSearchCursor(item resourceSearchItem) string {
	mode := item.CursorMode
	if mode == "" {
		mode = "recent"
		if item.Rank != 0 {
			mode = "rank"
		}
	}
	payload, _ := json.Marshal(searchPageCursor{Mode: mode, Rank: item.Rank, UpdatedAt: item.UpdatedAt, EntryID: item.ID})
	return base64.RawURLEncoding.EncodeToString(payload)
}
