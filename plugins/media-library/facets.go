package main

import (
	"database/sql"
	"strings"
)

type assetFacetInput struct {
	SourceID string   `json:"source_id"`
	Kinds    []string `json:"kinds"`
}

type facetCount struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

func assetFacets(in assetFacetInput) (any, error) {
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	base := []string{"missing=0"}
	args := []any{}
	if strings.TrimSpace(in.SourceID) != "" {
		base = append(base, "source_id=?")
		args = append(args, strings.TrimSpace(in.SourceID))
	}
	if len(in.Kinds) > 0 {
		placeholder, kindArgs := placeholders(in.Kinds)
		base = append(base, "kind IN ("+placeholder+")")
		args = append(args, kindArgs...)
	}
	where := strings.Join(base, " AND ")

	total := 0
	if err := db.QueryRow(`SELECT COUNT(*) FROM candidates WHERE `+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	folders, err := facetRows(db, `SELECT folder, COUNT(*) FROM candidates WHERE `+where+` AND folder<>'' GROUP BY folder ORDER BY COUNT(*) DESC, folder LIMIT 100`, args)
	if err != nil {
		return nil, err
	}
	kinds, err := facetRows(db, `SELECT kind, COUNT(*) FROM candidates WHERE `+where+` GROUP BY kind ORDER BY COUNT(*) DESC`, args)
	if err != nil {
		return nil, err
	}
	resolutions, err := facetRows(db, `SELECT CASE
		WHEN max(width,height)>=5000 THEN '5K+'
		WHEN max(width,height)>=3800 THEN '4K'
		WHEN max(width,height)>=2500 THEN '2K'
		WHEN max(width,height)>=1900 THEN '1080p'
		WHEN max(width,height)>=1200 THEN '720p'
		ELSE '其他' END AS bucket, COUNT(*)
		FROM candidates WHERE `+where+` AND kind IN ('image','video') GROUP BY bucket`, args)
	if err != nil {
		return nil, err
	}
	durations, err := facetRows(db, `SELECT CASE
		WHEN duration_ms<=0 THEN '未知'
		WHEN duration_ms<5000 THEN '5秒内'
		WHEN duration_ms<30000 THEN '5–30秒'
		WHEN duration_ms<120000 THEN '30–120秒'
		ELSE '2分钟以上' END AS bucket, COUNT(*)
		FROM candidates WHERE `+where+` AND kind IN ('video','audio') GROUP BY bucket`, args)
	if err != nil {
		return nil, err
	}
	sortFacets(resolutions, []string{"5K+", "4K", "2K", "1080p", "720p", "其他"})
	sortFacets(durations, []string{"5秒内", "5–30秒", "30–120秒", "2分钟以上", "未知"})
	return map[string]any{
		"total":       total,
		"folders":     folders,
		"kinds":       kinds,
		"resolutions": resolutions,
		"durations":   durations,
	}, nil
}

func facetRows(db *sql.DB, query string, args []any) ([]facetCount, error) {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []facetCount{}
	for rows.Next() {
		var item facetCount
		if err := rows.Scan(&item.Value, &item.Count); err != nil {
			return nil, err
		}
		item.Label = facetLabel(item.Value)
		items = append(items, item)
	}
	return items, rows.Err()
}

func facetLabel(value string) string {
	switch value {
	case "image":
		return "图片"
	case "video":
		return "视频"
	case "audio":
		return "音频"
	case "5K+", "4K", "2K", "1080p", "720p", "其他":
		return value
	case "5秒内", "5–30秒", "30–120秒", "2分钟以上", "未知":
		return value
	default:
		if value == "" {
			return "未分类"
		}
		return value
	}
}

func sortFacets(items []facetCount, order []string) {
	rank := map[string]int{}
	for index, value := range order {
		rank[value] = index
	}
	for left := 0; left < len(items); left++ {
		for right := left + 1; right < len(items); right++ {
			leftRank, leftOK := rank[items[left].Value]
			rightRank, rightOK := rank[items[right].Value]
			if !leftOK {
				leftRank = 999
			}
			if !rightOK {
				rightRank = 999
			}
			if rightRank < leftRank {
				items[left], items[right] = items[right], items[left]
			}
		}
	}
}
