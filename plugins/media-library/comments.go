package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const commentPageSize = 10
const commentScope = "official_visible_sample"

type commentInput struct {
	URL       string `json:"url"`
	ItemID    string `json:"item_id"`
	Cursor    string `json:"cursor"`
	RequestID string `json:"request_id"`
	SampleID  string `json:"sample_id"`
}
type publicComment struct {
	ID        string          `json:"id"`
	ParentID  string          `json:"parent_id,omitempty"`
	Author    string          `json:"author"`
	Text      string          `json:"text"`
	Published string          `json:"published,omitempty"`
	Likes     *int64          `json:"like_count,omitempty"`
	IsAuthor  bool            `json:"is_author,omitempty"`
	Avatar    string          `json:"avatar,omitempty"`
	Replies   []publicComment `json:"replies,omitempty"`
}
type commentSample struct {
	SampleID   string          `json:"sample_id,omitempty"`
	SourceURL  string          `json:"source_url"`
	ObservedAt string          `json:"observed_at"`
	Scope      string          `json:"scope"`
	Items      []publicComment `json:"items"`
	Generation string          `json:"-"`
	Expires    int64           `json:"-"`
}
type commentPage struct {
	commentSample
	NextCursor string `json:"next_cursor,omitempty"`
	Total      int    `json:"loaded_count"`
}
type rawComment struct {
	Key       string `json:"key"`
	ParentKey string `json:"parent_key"`
	Author    string `json:"author"`
	Text      string `json:"text"`
	Published string `json:"published"`
	Likes     string `json:"likes"`
	AvatarURL string `json:"avatar_url"`
	IsAuthor  bool   `json:"is_author"`
}
type browserCommentResult struct {
	State   string       `json:"state"`
	Items   []rawComment `json:"items"`
	Markers []string     `json:"markers"`
}

var commentToken = regexp.MustCompile(`^[a-f0-9]{32}$`)
var commentCursor = regexp.MustCompile(`^([a-f0-9]{32}):([0-9]{1,3})$`)
var commentCount = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)\s*([万亿wW]?)$`)

func ensureCommentSchema(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS comment_samples(id TEXT PRIMARY KEY,source_url TEXT NOT NULL,generation TEXT NOT NULL,expires INTEGER NOT NULL,payload TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS comment_snapshots(source_url TEXT PRIMARY KEY,content_id TEXT NOT NULL,saved_at TEXT NOT NULL,payload TEXT NOT NULL);`)
	return err
}

func commentReference(in commentInput) (string, string, error) {
	raw := in.URL
	if raw == "" && in.ItemID != "" {
		db, err := openLibraryDB()
		if err != nil {
			return "", "", err
		}
		defer db.Close()
		_ = db.QueryRow(`SELECT source_url FROM library_items WHERE id=? AND deleted_at=''`, in.ItemID).Scan(&raw)
		if raw == "" {
			_ = db.QueryRow(`SELECT source_url FROM asset_origins WHERE library_item_id=? AND provider='douyin' ORDER BY created_at LIMIT 1`, in.ItemID).Scan(&raw)
		}
	}
	p, id, canonical, err := canonicalRemoteReference(raw)
	if err != nil || p != "douyin" || !regexp.MustCompile(`^[0-9]{10,30}$`).MatchString(id) {
		return "", "", fmt.Errorf("评论暂支持有抖音来源的视频与音频")
	}
	return canonical, id, nil
}

func parseCommentLikes(s string) *int64 {
	s = strings.ReplaceAll(strings.TrimSpace(s), ",", "")
	m := commentCount.FindStringSubmatch(s)
	if m == nil {
		return nil
	}
	n, _ := strconv.ParseFloat(m[1], 64)
	if m[2] == "万" || strings.EqualFold(m[2], "w") {
		n *= 10000
	}
	if m[2] == "亿" {
		n *= 100000000
	}
	if n < 0 || n > 1e12 {
		return nil
	}
	v := int64(n)
	return &v
}

func cleanComments(raw []rawComment) []publicComment {
	ids := map[string]string{}
	seen := map[string]bool{}
	roots := map[string]int{}
	out := []publicComment{}
	for _, r := range raw {
		if r.Key != "" {
			ids[r.Key] = stableID("comment", r.Key)
		}
	}
	convert := func(r rawComment) publicComment {
		return publicComment{ID: ids[r.Key], Author: clipDiscoveryText(r.Author, 120), Text: clipDiscoveryText(r.Text, 2000), Published: clipDiscoveryText(strings.SplitN(r.Published, "·", 2)[0], 100), Likes: parseCommentLikes(r.Likes), IsAuthor: r.IsAuthor}
	}
	for _, r := range raw {
		if r.ParentKey != "" || ids[r.Key] == "" || seen[r.Key] || strings.TrimSpace(r.Text) == "" || strings.TrimSpace(r.Author) == "" {
			continue
		}
		seen[r.Key] = true
		roots[r.Key] = len(out)
		out = append(out, convert(r))
		if len(out) >= 60 {
			break
		}
	}
	for _, r := range raw {
		i, ok := roots[r.ParentKey]
		if !ok || r.Key == r.ParentKey || seen[r.Key] || len(out[i].Replies) >= 20 || strings.TrimSpace(r.Text) == "" {
			continue
		}
		seen[r.Key] = true
		c := convert(r)
		c.ParentID = out[i].ID
		out[i].Replies = append(out[i].Replies, c)
	}
	return out
}

func pageComments(sample commentSample, offset int) (commentPage, error) {
	if offset < 0 || offset%commentPageSize != 0 || offset > len(sample.Items) || offset == len(sample.Items) && offset != 0 {
		return commentPage{}, fmt.Errorf("评论页已过期，请重新读取")
	}
	all := sample.Items
	sample.Items = append([]publicComment{}, all[offset:min(offset+commentPageSize, len(all))]...)
	page := commentPage{commentSample: sample, Total: len(all)}
	if offset+commentPageSize < len(all) {
		page.NextCursor = sample.SampleID + ":" + strconv.Itoa(offset+commentPageSize)
	}
	return page, nil
}

func listComments(in commentInput) (any, error) {
	source, videoID, err := commentReference(in)
	if err != nil {
		return nil, err
	}
	generation, err := discoveryGeneration("douyin")
	if err != nil {
		return nil, fmt.Errorf("请先在“平台与采集”中连接抖音，再读取评论")
	}
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err = ensureCommentSchema(db); err != nil {
		return nil, err
	}
	var sample commentSample
	if in.Cursor != "" {
		m := commentCursor.FindStringSubmatch(in.Cursor)
		if m == nil {
			return nil, fmt.Errorf("评论页无效，请重新读取")
		}
		var payload string
		err = db.QueryRow(`SELECT payload,generation,expires FROM comment_samples WHERE id=? AND source_url=?`, m[1], source).Scan(&payload, &sample.Generation, &sample.Expires)
		if err != nil || sample.Generation != generation || sample.Expires <= time.Now().Unix() || json.Unmarshal([]byte(payload), &sample) != nil {
			return nil, fmt.Errorf("评论页已过期或连接已变化，请重新读取")
		}
		offset, _ := strconv.Atoi(m[2])
		return pageComments(sample, offset)
	}
	id, err := discoveryID()
	if err != nil {
		return nil, err
	}
	if in.RequestID != "" {
		if !commentToken.MatchString(in.RequestID) {
			return nil, fmt.Errorf("评论请求无效")
		}
		id = in.RequestID
	}
	root, err := pluginDataRoot()
	if err != nil {
		return nil, err
	}
	if _, err = os.Stat(filepath.Join(root, "discovery-cancel", id)); err == nil {
		return nil, fmt.Errorf("读取已取消")
	}
	if err = acquireDiscoveryLease(db, "douyin-comments", id, time.Now()); err != nil {
		return nil, fmt.Errorf("正在读取评论或请求过快，请稍后重试")
	}
	defer db.Exec(`UPDATE discovery_leases SET until_at=0 WHERE provider='douyin-comments' AND owner=?`, id)
	ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
	defer cancel()
	result, err := browseOfficialComments(ctx, root, id, videoID, generation)
	if err != nil {
		return nil, err
	}
	current, err := discoveryGeneration("douyin")
	if err != nil || current != generation {
		return nil, fmt.Errorf("抖音连接已变化，请重新读取评论")
	}
	if _, err = os.Stat(filepath.Join(root, "discovery-cancel", id)); err == nil {
		return nil, fmt.Errorf("读取已取消")
	}
	if result.State != "ok" && result.State != "empty" {
		if result.State == "canceled" {
			return nil, fmt.Errorf("读取已取消")
		}
		return nil, fmt.Errorf("官网未返回可读取的评论，请完成官网验证或稍后重试。已保存快照仍可查看")
	}
	sample = commentSample{SampleID: id, SourceURL: source, ObservedAt: nowRFC3339(), Scope: commentScope, Items: cleanComments(result.Items)}
	if result.State == "ok" && len(sample.Items) == 0 {
		return nil, fmt.Errorf("官网评论结构暂时无法识别，请打开原作品查看")
	}
	// Remote avatar URLs stay inside this invocation. Only bounded image bytes
	// may enter the public sample; failure falls back to the author's initial.
	cacheCommentAvatars(sample.Items, result.Items)
	payload, err := json.Marshal(sample)
	if err != nil {
		return nil, err
	}
	_, _ = db.Exec(`DELETE FROM comment_samples WHERE expires<?`, time.Now().Unix())
	if _, err = db.Exec(`INSERT INTO comment_samples VALUES(?,?,?,?,?)`, id, source, generation, time.Now().Add(10*time.Minute).Unix(), string(payload)); err != nil {
		return nil, err
	}
	return pageComments(sample, 0)
}

func browseOfficialComments(ctx context.Context, root, id, videoID, generation string) (browserCommentResult, error) {
	var result browserCommentResult
	executable, err := os.Executable()
	if err != nil {
		return result, err
	}
	helper := filepath.Join(filepath.Dir(executable), "session-browser", "media-session-browser.exe")
	dir := filepath.Join(root, "comment-requests", id)
	if err = os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		return result, err
	}
	if err = os.Mkdir(dir, 0700); err != nil {
		return result, fmt.Errorf("评论请求已使用，请重试")
	}
	defer os.RemoveAll(dir) // server-generated hex ID inside the plugin's request root
	data, _ := json.Marshal(map[string]string{"provider": "douyin", "video_id": videoID, "generation": generation})
	if err = os.WriteFile(filepath.Join(dir, "request.json"), data, 0600); err != nil {
		return result, err
	}
	cmd := exec.CommandContext(ctx, helper, "--comments", root, id)
	for _, env := range os.Environ() {
		key := strings.ToUpper(strings.SplitN(env, "=", 2)[0])
		if key == "SYSTEMROOT" || key == "WINDIR" || key == "TEMP" || key == "TMP" || key == "LOCALAPPDATA" || key == "USERPROFILE" || key == "PATH" {
			cmd.Env = append(cmd.Env, env)
		}
	}
	if err = cmd.Run(); err != nil {
		return result, fmt.Errorf("官网评论读取未完成，请检查网络或重试")
	}
	data, err = os.ReadFile(filepath.Join(dir, "result.json"))
	if err != nil || len(data) > 2<<20 || json.Unmarshal(data, &result) != nil {
		return result, fmt.Errorf("未读取到官网评论，请重试")
	}
	// Selector names only: useful for site layout regressions, never page text,
	// URLs, session contents or request headers.
	diagnostic, _ := json.Marshal(map[string]any{"state": result.State, "markers": result.Markers, "at": nowRFC3339()})
	_ = os.WriteFile(filepath.Join(root, "comment-layout-diagnostic.json"), diagnostic, 0600)
	return result, nil
}

func commentSnapshot(in commentInput, save bool) (any, error) {
	source, _, err := commentReference(in)
	if err != nil {
		return nil, err
	}
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err = ensureCommentSchema(db); err != nil {
		return nil, err
	}
	if save {
		if !commentToken.MatchString(in.SampleID) {
			return nil, fmt.Errorf("请先读取评论，再保存快照")
		}
		var payload, generation string
		var expires int64
		err = db.QueryRow(`SELECT payload,generation,expires FROM comment_samples WHERE id=? AND source_url=?`, in.SampleID, source).Scan(&payload, &generation, &expires)
		current, e := discoveryGeneration("douyin")
		if err != nil || e != nil || generation != current || expires <= time.Now().Unix() {
			return nil, fmt.Errorf("评论采样已过期，请重新读取后保存")
		}
		var sample commentSample
		if json.Unmarshal([]byte(payload), &sample) != nil || len(sample.Items) == 0 {
			return nil, fmt.Errorf("没有可保存的评论")
		}
		sort.SliceStable(sample.Items, func(i, j int) bool {
			a, b := sample.Items[i].Likes, sample.Items[j].Likes
			return a != nil && (b == nil || *a > *b)
		})
		sample.Items = sample.Items[:min(len(sample.Items), 20)]
		sample.SampleID = ""
		payloadBytes, _ := json.Marshal(sample)
		content := stableID("comments", string(payloadBytes))
		_, err = db.Exec(`INSERT INTO comment_snapshots(source_url,content_id,saved_at,payload) VALUES(?,?,?,?) ON CONFLICT(source_url) DO UPDATE SET content_id=excluded.content_id,saved_at=excluded.saved_at,payload=excluded.payload WHERE comment_snapshots.content_id<>excluded.content_id`, source, content, nowRFC3339(), string(payloadBytes))
		if err != nil {
			return nil, err
		}
	}
	var payload, savedAt string
	err = db.QueryRow(`SELECT payload,saved_at FROM comment_snapshots WHERE source_url=?`, source).Scan(&payload, &savedAt)
	if err == sql.ErrNoRows {
		return map[string]any{"snapshot": nil}, nil
	}
	if err != nil {
		return nil, err
	}
	var snapshot commentSample
	if json.Unmarshal([]byte(payload), &snapshot) != nil {
		return nil, fmt.Errorf("评论快照暂时无法读取")
	}
	return map[string]any{"snapshot": snapshot, "saved_at": savedAt}, nil
}
