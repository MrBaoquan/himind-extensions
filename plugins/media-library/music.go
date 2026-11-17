package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MrBaoquan/himind-extensions/internal/mediadownload"
)

func ensureMusicSchema(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS music_tracks(
 id TEXT PRIMARY KEY,provider TEXT NOT NULL,provider_id TEXT NOT NULL,title TEXT NOT NULL,author TEXT NOT NULL DEFAULT '',
 source_url TEXT NOT NULL,evidence TEXT NOT NULL,updated_at TEXT NOT NULL,UNIQUE(provider,provider_id));
 CREATE TABLE IF NOT EXISTS video_music_usages(
 video_id TEXT PRIMARY KEY,music_id TEXT NOT NULL REFERENCES music_tracks(id),source_url TEXT NOT NULL,title TEXT NOT NULL,
 author TEXT NOT NULL DEFAULT '',evidence TEXT NOT NULL,observed_at TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS idx_music_usages ON video_music_usages(music_id);
 CREATE TABLE IF NOT EXISTS music_samples(
 id TEXT PRIMARY KEY,provider TEXT NOT NULL,query TEXT NOT NULL,basis TEXT NOT NULL,sample_size INTEGER NOT NULL,
 identified INTEGER NOT NULL,observed_at TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS music_observations(
 sample_id TEXT NOT NULL REFERENCES music_samples(id),music_id TEXT NOT NULL REFERENCES music_tracks(id),
 video_count INTEGER NOT NULL,PRIMARY KEY(sample_id,music_id));
 CREATE TABLE IF NOT EXISTS audio_derivations(
 asset_id TEXT NOT NULL REFERENCES library_items(id),job_id TEXT NOT NULL,source_asset_id TEXT NOT NULL DEFAULT '',
 source_sha256 TEXT NOT NULL DEFAULT '',source_url TEXT NOT NULL DEFAULT '',music_id TEXT NOT NULL DEFAULT '',
 role TEXT NOT NULL,purpose TEXT NOT NULL DEFAULT '',parameters_json TEXT NOT NULL,engine TEXT NOT NULL,created_at TEXT NOT NULL,
 PRIMARY KEY(asset_id,job_id));
 CREATE INDEX IF NOT EXISTS idx_audio_purpose ON audio_derivations(purpose,asset_id);
 CREATE TABLE IF NOT EXISTS audio_renders(
 acquisition_id TEXT PRIMARY KEY,child_job_id TEXT NOT NULL DEFAULT '',attempt INTEGER NOT NULL DEFAULT 0);
 INSERT OR IGNORE INTO schema_migrations(name,applied_at) VALUES('music_audio_v1',strftime('%Y-%m-%dT%H:%M:%SZ','now'));`)
	return err
}

func validateMusic(m *mediadownload.MusicInfo) error {
	if m == nil {
		return nil
	}
	if m.Provider != "douyin" || len(m.ProviderID) < 5 || len(m.ProviderID) > 40 {
		return fmt.Errorf("音乐身份无效")
	}
	for _, c := range m.ProviderID {
		if c < '0' || c > '9' {
			return fmt.Errorf("音乐身份无效")
		}
	}
	if m.SourceURL != "https://www.douyin.com/music/"+m.ProviderID || m.Evidence != "platform_metadata" || len([]rune(m.Title)) > 300 || len([]rune(m.Author)) > 200 {
		return fmt.Errorf("音乐元信息无效")
	}
	return nil
}

func storeMusicUsage(tx *sql.Tx, video remoteResource, m *mediadownload.MusicInfo) (string, error) {
	if m == nil {
		return "", nil
	}
	if err := validateMusic(m); err != nil {
		return "", err
	}
	provider, videoID, canonical, err := canonicalRemoteReference(video.SourceURL)
	if err != nil || provider != m.Provider {
		return "", fmt.Errorf("音乐与视频平台不一致")
	}
	id := stableID("music", m.Provider+":"+m.ProviderID)
	now := nowRFC3339()
	_, err = tx.Exec(`INSERT INTO music_tracks(id,provider,provider_id,title,author,source_url,evidence,updated_at) VALUES(?,?,?,?,?,?,?,?)
 ON CONFLICT(provider,provider_id) DO UPDATE SET title=excluded.title,author=excluded.author,updated_at=excluded.updated_at`, id, m.Provider, m.ProviderID, m.Title, m.Author, m.SourceURL, m.Evidence, now)
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(`INSERT INTO video_music_usages(video_id,music_id,source_url,title,author,evidence,observed_at) VALUES(?,?,?,?,?,?,?)
 ON CONFLICT(video_id) DO UPDATE SET music_id=excluded.music_id,title=excluded.title,author=excluded.author,observed_at=excluded.observed_at`, stableID("remote", provider+":"+videoID), id, canonical, clipDiscoveryText(video.Title, 300), clipDiscoveryText(video.Author, 120), m.Evidence, now)
	return id, err
}

func rememberMusicUsage(video remoteResource, m *mediadownload.MusicInfo) error {
	if m == nil {
		return nil
	}
	db, err := openLibraryDB()
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = storeMusicUsage(tx, video, m); err != nil {
		return err
	}
	return tx.Commit()
}

func musicForVideo(db *sql.DB, sourceURL string) (*mediadownload.MusicInfo, error) {
	provider, videoID, _, err := canonicalRemoteReference(sourceURL)
	if err != nil {
		return nil, err
	}
	var m mediadownload.MusicInfo
	err = db.QueryRow(`SELECT m.provider,m.provider_id,m.title,m.author,m.source_url,m.evidence
 FROM video_music_usages u JOIN music_tracks m ON m.id=u.music_id WHERE u.video_id=?`, stableID("remote", provider+":"+videoID)).Scan(&m.Provider, &m.ProviderID, &m.Title, &m.Author, &m.SourceURL, &m.Evidence)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

type musicResult struct {
	ID     string                  `json:"music_id"`
	Music  mediadownload.MusicInfo `json:"music"`
	Count  int                     `json:"video_count"`
	Videos []remoteResource        `json:"videos"`
}

// Counts are scoped to this exact page of sampled videos, never platform heat.
func recordMusicSample(db *sql.DB, query string, videos []remoteResource, metadata []*mediadownload.MusicInfo) (map[string]any, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	groups := map[string]*musicResult{}
	seen := map[string]bool{}
	identified, total := 0, 0
	sampledVideos := []map[string]any{}
	for i, v := range videos {
		_, pid, canonical, e := canonicalRemoteReference(v.SourceURL)
		if e != nil || seen[canonical] {
			continue
		}
		seen[canonical] = true
		total++
		sampledVideos = append(sampledVideos, map[string]any{"video_id": pid, "identified": i < len(metadata) && metadata[i] != nil})
		if i >= len(metadata) || metadata[i] == nil {
			continue
		}
		id, e := storeMusicUsage(tx, v, metadata[i])
		if e != nil {
			return nil, e
		}
		identified++
		if groups[id] == nil {
			groups[id] = &musicResult{ID: id, Music: *metadata[i], Videos: []remoteResource{}}
		}
		v.ProviderID = pid
		v.SourceURL = canonical
		groups[id].Count++
		groups[id].Videos = append(groups[id].Videos, v)
	}
	id, now := randomID("sample"), nowRFC3339()
	if _, err = tx.Exec(`INSERT INTO music_samples(id,provider,query,basis,sample_size,identified,observed_at) VALUES(?,'douyin',?,'search_sample',?,?,?)`, id, query, total, identified, now); err != nil {
		return nil, err
	}
	items := []musicResult{}
	for _, g := range groups {
		if _, err = tx.Exec(`INSERT INTO music_observations(sample_id,music_id,video_count) VALUES(?,?,?)`, id, g.ID, g.Count); err != nil {
			return nil, err
		}
		items = append(items, *g)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count != items[j].Count {
			return items[i].Count > items[j].Count
		}
		return items[i].ID < items[j].ID
	})
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return map[string]any{"items": items, "sample_id": id, "sample_size": total, "identified": identified, "unidentified": total - identified, "sampled_videos": sampledVideos, "basis": "search_sample", "observed_at": now}, nil
}

func discoverMusic(in discoveryInput) (any, error) {
	if in.Provider != "douyin" {
		return nil, fmt.Errorf("音乐发现当前支持抖音")
	}
	if strings.TrimSpace(in.Query) == "" {
		in.Query = "热门BGM"
	}
	in, err := normalizeDiscoveryInput(in)
	if err != nil {
		return nil, err
	}
	generation, err := discoveryGeneration("douyin")
	if err != nil {
		return nil, err
	}
	result, err := searchDiscovery(in)
	if err != nil {
		return nil, err
	}
	page, ok := result.(discoveryPage)
	if !ok {
		return nil, fmt.Errorf("无法读取视频样本")
	}
	metadata := make([]*mediadownload.MusicInfo, len(page.Items))
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Second)
	defer cancel()
	root, err := pluginDataRoot()
	if err != nil {
		return nil, err
	}
	var wg sync.WaitGroup
	slots := make(chan struct{}, 3)
	for i, item := range page.Items {
		wg.Add(1)
		go func(i int, item remoteResource) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-slots }()
			if in.RequestID != "" {
				if _, e := os.Stat(filepath.Join(root, "discovery-cancel", in.RequestID)); e == nil {
					return
				}
			}
			metadata[i], _ = mediadownload.ResolveMusic(ctx, item.SourceURL)
		}(i, item)
	}
	wg.Wait()
	if in.RequestID != "" {
		if _, e := os.Stat(filepath.Join(root, "discovery-cancel", in.RequestID)); e == nil {
			return nil, fmt.Errorf("搜索已取消")
		}
	}
	current, e := discoveryGeneration("douyin")
	if e != nil || current != generation {
		return nil, fmt.Errorf("平台连接已变化，请重新搜索")
	}
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	payload, err := recordMusicSample(db, in.Query, page.Items, metadata)
	if err != nil {
		return nil, err
	}
	payload["next_cursor"] = page.NextCursor
	payload["warnings"] = []string{"使用次数仅反映已加载的视频样本，不代表抖音官方音乐榜单。"}
	return payload, nil
}

func audioMetadata(tx *sql.Tx, record acquisitionRecord, assetID, kind string, durationMS int64) error {
	if kind != "audio" {
		return nil
	}
	if record.Input.Purpose != "bgm" && record.Input.Audio == nil {
		return nil
	}
	musicID := ""
	if record.Input.Music != nil {
		var err error
		musicID, err = storeMusicUsage(tx, remoteResource{SourceURL: record.URL, Title: record.Title}, record.Input.Music)
		if err != nil {
			return err
		}
	}
	sourceID, sourceHash, role, engine := "", "", "video_audio", "yt-dlp"
	parameters := map[string]any{"audio_only": true, "format": "mp3"}
	sourceURL := record.URL
	if a := record.Input.Audio; a != nil {
		sourceID, sourceHash, role, engine = a.SourceAssetID, a.SourceSHA256, "audio_excerpt", "media.toolkit.recipe.run"
		if a.StartMS == 0 && a.EndMS == 0 {
			role = "extracted_audio"
		}
		sourceURL = a.SourceURL
		musicID = a.MusicID
		parameters = map[string]any{"start_ms": a.StartMS, "end_ms": a.EndMS, "format": a.Format, "engine_version": "2.3.3"}
	}
	data, _ := json.Marshal(parameters)
	_, err := tx.Exec(`INSERT OR IGNORE INTO audio_derivations(asset_id,job_id,source_asset_id,source_sha256,source_url,music_id,role,purpose,parameters_json,engine,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, assetID, record.ID, sourceID, sourceHash, sourceURL, musicID, role, record.Input.Purpose, string(data), engine, nowRFC3339())
	if err != nil {
		return err
	}
	var raw string
	if err = tx.QueryRow(`SELECT metadata_json FROM library_items WHERE id=?`, assetID).Scan(&raw); err != nil {
		return err
	}
	meta := map[string]any{}
	_ = json.Unmarshal([]byte(raw), &meta)
	if meta == nil {
		meta = map[string]any{}
	}
	meta["audio_purpose"] = "bgm"
	meta["audio_role"] = role
	if record.Input.Audio != nil && record.Input.Audio.EndMS > 0 {
		meta["duration_ms"] = record.Input.Audio.EndMS - record.Input.Audio.StartMS
	}
	if durationMS > 0 {
		meta["duration_ms"] = durationMS
	}
	encoded, _ := json.Marshal(meta)
	_, err = tx.Exec(`UPDATE library_items SET metadata_json=? WHERE id=?`, string(encoded), assetID)
	return err
}

func audioProvenance(db *sql.DB, itemID string) ([]map[string]any, error) {
	rows, err := db.Query(`SELECT d.source_asset_id,d.source_sha256,d.source_url,d.role,d.purpose,d.parameters_json,d.engine,d.created_at,COALESCE(m.title,''),COALESCE(m.author,''),COALESCE(m.source_url,'') FROM audio_derivations d LEFT JOIN music_tracks m ON m.id=d.music_id WHERE d.asset_id=? ORDER BY d.created_at`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var parent, hash, url, role, purpose, params, engine, at, title, author, musicURL string
		if err = rows.Scan(&parent, &hash, &url, &role, &purpose, &params, &engine, &at, &title, &author, &musicURL); err != nil {
			return nil, err
		}
		p := map[string]any{}
		_ = json.Unmarshal([]byte(params), &p)
		items = append(items, map[string]any{"source_asset_id": parent, "source_sha256": hash, "source_url": url, "role": role, "purpose": purpose, "parameters": p, "engine": engine, "created_at": at, "music_title": title, "music_author": author, "music_url": musicURL})
	}
	return items, rows.Err()
}
