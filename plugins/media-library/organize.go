package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/bits"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// 自动整理（organize）：把「扫描完的候选素材」按画质 + 相似聚类 + AI 标签，
// 一次算成「自动入库 / 待复核 / 跳过」三摞，并把高置信那摞直接沉淀进精选库。
// 原则：只动索引与库目录，不动源文件；自动动作一律可撤销。

type organizePolicy struct {
	AutoIngest   *bool   `json:"auto_ingest,omitempty"`
	QualityFloor float64 `json:"quality_floor"`
	Similarity   int     `json:"similarity"`
	PerCluster   int     `json:"per_cluster"`
	Mode         string  `json:"mode"`
	Cleanup      bool    `json:"cleanup"`
	MaxItems     int     `json:"max_items"`
	MaxReview    int     `json:"max_review"`
	MaxScan      int     `json:"max_scan"`
}

// autoIngestEnabled 未显式传 auto_ingest 时默认开启（少人工干预是默认体验）。
func (policy organizePolicy) autoIngestEnabled() bool {
	return policy.AutoIngest == nil || *policy.AutoIngest
}

func boolPointer(value bool) *bool {
	return &value
}

func defaultOrganizePolicy() organizePolicy {
	return organizePolicy{
		AutoIngest:   boolPointer(true),
		QualityFloor: 45,
		Similarity:   12,
		PerCluster:   1,
		Mode:         "hardlink",
		Cleanup:      true,
		MaxItems:     200,
		MaxReview:    100,
		MaxScan:      120,
	}
}

func (policy organizePolicy) normalized() organizePolicy {
	if policy.QualityFloor <= 0 {
		policy.QualityFloor = 45
	}
	if policy.QualityFloor > 100 {
		policy.QualityFloor = 100
	}
	if policy.Similarity <= 0 || policy.Similarity > 64 {
		policy.Similarity = 12
	}
	if policy.PerCluster <= 0 {
		policy.PerCluster = 1
	}
	if policy.PerCluster > 10 {
		policy.PerCluster = 10
	}
	if policy.Mode != "copy" && policy.Mode != "reference" {
		policy.Mode = "hardlink"
	}
	if policy.MaxItems <= 0 {
		policy.MaxItems = 200
	}
	if policy.MaxItems > 2000 {
		policy.MaxItems = 2000
	}
	if policy.MaxReview <= 0 {
		policy.MaxReview = 100
	}
	if policy.MaxReview > 1000 {
		policy.MaxReview = 1000
	}
	if policy.MaxScan <= 0 {
		policy.MaxScan = 120
	}
	if policy.MaxScan > 2000 {
		policy.MaxScan = 2000
	}
	return policy
}

func ensureOrganizeSchema(db *sql.DB) error {
	var applied int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE name='v4_organize_v1'`).Scan(&applied); err != nil {
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
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS organize_runs (
  id TEXT PRIMARY KEY,
  library_id TEXT NOT NULL,
  source_id TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'planned',
  policy_json TEXT NOT NULL DEFAULT '{}',
  summary_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
)`,
		`CREATE TABLE IF NOT EXISTS organize_decisions (
  run_id TEXT NOT NULL,
  asset_id TEXT NOT NULL,
  bucket TEXT NOT NULL,
  confidence REAL NOT NULL DEFAULT 0,
  quality REAL NOT NULL DEFAULT 0,
  cluster INTEGER NOT NULL DEFAULT 0,
  reason TEXT NOT NULL DEFAULT '',
  item_id TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL,
  PRIMARY KEY(run_id, asset_id)
)`,
		`CREATE INDEX IF NOT EXISTS idx_organize_decisions_bucket ON organize_decisions(run_id, bucket)`,
		`CREATE INDEX IF NOT EXISTS idx_organize_runs_library ON organize_runs(library_id, created_at DESC)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations(name,applied_at) VALUES('v4_organize_v1',?)`, nowRFC3339()); err != nil {
		return err
	}
	return tx.Commit()
}

type organizeCandidate struct {
	AssetID  string
	Path     string
	Relative string
	Title    string
	Kind     string
	Bytes    int64
	Width    int
	Height   int
	Duration int64
	FPS      float64
	Codec    string
	BitDepth int
	Modified string

	Hash      uint64
	HasHash   bool
	Quality   float64
	Cluster   int
	Tags      []string
	AISummary string
	HasAI     bool
}

type organizeRun struct {
	ID        string
	LibraryID string
	SourceID  string
	Status    string
	Policy    organizePolicy
	Summary   map[string]any
	CreatedAt string
}

type organizePlanInput struct {
	LibraryID string         `json:"library_id"`
	SourceID  string         `json:"source_id"`
	Policy    organizePolicy `json:"policy"`
}

type organizeApplyInput struct {
	RunID string `json:"run_id"`
}

type organizeReviewListInput struct {
	RunID  string `json:"run_id"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
}

type organizeReviewItem struct {
	AssetID string `json:"asset_id"`
	Accept  bool   `json:"accept"`
}

type organizeReviewDecideInput struct {
	RunID string               `json:"run_id"`
	Items []organizeReviewItem `json:"items"`
}

type organizeRunsInput struct {
	LibraryID string `json:"library_id"`
	Limit     int    `json:"limit"`
}

// planOrganize 计算一次整理方案：不落文件，只写运行记录与决策。
func planOrganize(in organizePlanInput) (any, error) {
	libraryID := strings.TrimSpace(in.LibraryID)
	if libraryID == "" {
		return nil, fmt.Errorf("请指定目标库")
	}
	library, err := getLibrary(libraryID)
	if err != nil {
		return nil, err
	}
	if library.AccessMode == "read_only" {
		return nil, fmt.Errorf("这个库是只读的，不能自动入库")
	}
	policy := in.Policy.normalized()
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	candidates, err := loadOrganizeCandidates(db, strings.TrimSpace(in.SourceID), policy.MaxScan)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("没有可整理的素材：先到设置里添加本机文件夹并扫描")
	}
	existing, err := libraryContentKeys(db, libraryID)
	if err != nil {
		return nil, err
	}
	fresh := candidates[:0]
	for _, candidate := range candidates {
		key := contentKeyForRelative(candidate.Relative, candidate.Bytes)
		if existing[key] {
			continue
		}
		fresh = append(fresh, candidate)
	}
	candidates = fresh
	if len(candidates) == 0 {
		return map[string]any{
			"run_id": "", "library_id": libraryID, "counts": map[string]any{
				"total": 0, "auto": 0, "review": 0, "skip": 0, "ai_pending": 0,
			},
			"message": "库内容已是最新，没有需要整理的素材",
			"items":   []map[string]any{},
		}, nil
	}

	// 画质分与相似聚类：图片/视频看首帧，音频只看时长。
	scored := make([]organizeCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		item := candidate
		item.Quality = organizeQuality(db, &item)
		scored = append(scored, item)
	}
	sort.SliceStable(scored, func(left, right int) bool { return scored[left].Quality > scored[right].Quality })
	assignOrganizeClusters(scored, policy.Similarity)
	attachOrganizeAIState(db, scored)

	auto := []organizeCandidate{}
	review := []organizeCandidate{}
	skip := []organizeCandidate{}
	perCluster := map[int]int{}
	for _, item := range scored {
		switch {
		case item.Kind != "image" && item.Kind != "video" && item.Kind != "audio":
			skip = append(skip, item)
		case item.Quality > 0 && item.Quality < policy.QualityFloor:
			skip = append(skip, item)
		case !item.HasHash && item.Kind != "audio":
			item.Cluster = -1
			review = append(review, item)
		case perCluster[item.Cluster] < policy.PerCluster:
			perCluster[item.Cluster]++
			auto = append(auto, item)
		default:
			review = append(review, item)
		}
	}
	if len(auto) > policy.MaxItems {
		overflow := auto[policy.MaxItems:]
		auto = auto[:policy.MaxItems]
		for _, item := range overflow {
			review = append(review, item)
		}
	}
	if len(review) > policy.MaxReview {
		overflow := review[policy.MaxReview:]
		review = review[:policy.MaxReview]
		skip = append(skip, overflow...)
	}
	if !policy.autoIngestEnabled() {
		review = append(review, auto...)
		auto = []organizeCandidate{}
	}

	runID := randomID("org")
	now := nowRFC3339()
	summary := map[string]any{
		"total": len(scored), "auto": len(auto), "review": len(review), "skip": len(skip),
		"ai_pending": 0, "source_id": strings.TrimSpace(in.SourceID),
	}
	aiPending := 0
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	encodedPolicy, _ := json.Marshal(policy)
	for _, group := range []struct {
		bucket string
		items  []organizeCandidate
	}{{"auto", auto}, {"review", review}, {"skip", skip}} {
		for _, item := range group.items {
			if group.bucket != "skip" && !item.HasAI {
				aiPending++
			}
			if _, err := tx.Exec(`INSERT INTO organize_decisions(run_id,asset_id,bucket,confidence,quality,cluster,reason,updated_at)
				VALUES(?,?,?,?,?,?,?,?)`,
				runID, item.AssetID, group.bucket, organizeConfidence(item), item.Quality, item.Cluster,
				organizeReason(item, group.bucket), now); err != nil {
				return nil, err
			}
		}
	}
	summary["ai_pending"] = aiPending
	encodedSummary, _ := json.Marshal(summary)
	if _, err := tx.Exec(`INSERT INTO organize_runs(id,library_id,source_id,status,policy_json,summary_json,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?)`, runID, libraryID, strings.TrimSpace(in.SourceID), "planned",
		string(encodedPolicy), string(encodedSummary), now, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return map[string]any{
		"run_id": runID, "library_id": libraryID, "counts": summary,
		"policy": policy, "items": organizePreviewItems(auto, review),
	}, nil
}

func loadOrganizeCandidates(db *sql.DB, sourceID string, maxScan int) ([]organizeCandidate, error) {
	if maxScan <= 0 {
		maxScan = 120
	}
	query := `SELECT c.id,c.path,c.relative_path,c.name,c.kind,c.bytes,c.width,c.height,c.duration_ms,
		c.fps,c.codec,c.bit_depth,c.modified_at
		FROM candidates c WHERE c.missing=0`
	args := []any{}
	if sourceID != "" {
		query += ` AND c.source_id=?`
		args = append(args, sourceID)
	}
	query += ` ORDER BY c.modified_at DESC LIMIT ?`
	args = append(args, maxScan)
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []organizeCandidate{}
	seen := map[string]bool{}
	for rows.Next() {
		var item organizeCandidate
		if err := rows.Scan(&item.AssetID, &item.Path, &item.Relative, &item.Title, &item.Kind, &item.Bytes,
			&item.Width, &item.Height, &item.Duration, &item.FPS, &item.Codec, &item.BitDepth, &item.Modified); err != nil {
			return nil, err
		}
		if strings.TrimSpace(item.Relative) == "" {
			item.Relative = filepath.Base(item.Path)
		}
		if item.Bytes <= 0 || seen[item.Path] {
			continue
		}
		seen[item.Path] = true
		items = append(items, item)
	}
	return items, rows.Err()
}

func libraryContentKeys(db *sql.DB, libraryID string) (map[string]bool, error) {
	rows, err := db.Query(`SELECT COALESCE(content_key,''),COALESCE(relative_path,''),COALESCE(bytes,0)
		FROM library_items WHERE library_id=? AND deleted_at=''`, libraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := map[string]bool{}
	for rows.Next() {
		var stored, relative string
		var bytes int64
		if err := rows.Scan(&stored, &relative, &bytes); err != nil {
			return nil, err
		}
		if stored == "" {
			stored = contentKeyForRelative(relative, bytes)
		}
		keys[stored] = true
	}
	return keys, rows.Err()
}

func organizeQuality(db *sql.DB, item *organizeCandidate) float64 {
	if item.Kind == "audio" {
		if item.Duration <= 0 {
			return 40
		}
		return 60
	}
	thumb, err := ensureThumbnail(context.Background(), db, item.AssetID, item.Path, item.Kind, item.Modified, item.Duration, "grid")
	if err != nil || thumb == "" {
		return 0
	}
	value, ok := computeVisualHash(thumb)
	if !ok {
		return 0
	}
	item.Hash = value.hash
	item.HasHash = true
	score := 100 * (0.5*value.sharpness + 0.2*value.exposure + 0.2*organizeResolutionScore(item) + 0.1*organizeDurationScore(item))
	return score
}

func organizeResolutionScore(item *organizeCandidate) float64 {
	longest := item.Width
	if item.Height > longest {
		longest = item.Height
	}
	switch {
	case longest >= 3840:
		return 1
	case longest >= 1920:
		return 0.85
	case longest >= 1280:
		return 0.65
	case longest >= 720:
		return 0.45
	default:
		return 0.25
	}
}

func organizeDurationScore(item *organizeCandidate) float64 {
	if item.Kind != "video" {
		return 0.7
	}
	seconds := item.Duration / 1000
	switch {
	case seconds >= 4 && seconds <= 90:
		return 1
	case seconds > 90:
		return 0.7
	default:
		return 0.4
	}
}

func assignOrganizeClusters(items []organizeCandidate, threshold int) {
	representatives := []uint64{}
	for index := range items {
		item := &items[index]
		if !item.HasHash {
			item.Cluster = -1
			continue
		}
		assigned := -1
		for cluster, representative := range representatives {
			if bits.OnesCount64(item.Hash^representative) <= threshold {
				assigned = cluster
				break
			}
		}
		if assigned < 0 {
			representatives = append(representatives, item.Hash)
			assigned = len(representatives) - 1
		}
		item.Cluster = assigned
	}
}

func attachOrganizeAIState(db *sql.DB, items []organizeCandidate) {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.AssetID)
	}
	if len(ids) == 0 {
		return
	}
	byID := map[string]int{}
	for index, item := range items {
		byID[item.AssetID] = index
	}
	placeholders, args := placeholders(ids)
	if rows, err := db.Query(`SELECT asset_id,COALESCE(tags_json,'[]') FROM user_asset_state WHERE asset_id IN (`+placeholders+`)`, args...); err == nil {
		for rows.Next() {
			var assetID, tagsJSON string
			if err := rows.Scan(&assetID, &tagsJSON); err == nil {
				if index, ok := byID[assetID]; ok {
					tags := []string{}
					_ = json.Unmarshal([]byte(tagsJSON), &tags)
					items[index].Tags = tags
					items[index].HasAI = len(tags) > 0
				}
			}
		}
		rows.Close()
	}
	if rows, err := db.Query(`SELECT DISTINCT asset_id FROM ai_tag_cache WHERE asset_id IN (`+placeholders+`)`, args...); err == nil {
		for rows.Next() {
			var assetID string
			if err := rows.Scan(&assetID); err == nil {
				if index, ok := byID[assetID]; ok {
					items[index].HasAI = true
				}
			}
		}
		rows.Close()
	}
}

func organizeConfidence(item organizeCandidate) float64 {
	confidence := item.Quality / 100 * 0.75
	if item.HasAI {
		confidence += 0.15
	}
	if item.Cluster >= 0 {
		confidence += 0.1
	}
	if confidence > 0.99 {
		confidence = 0.99
	}
	return confidence
}

func organizeReason(item organizeCandidate, bucket string) string {
	parts := []string{}
	if item.Quality > 0 {
		parts = append(parts, fmt.Sprintf("画质 %.0f", item.Quality))
	}
	if item.Cluster >= 0 {
		parts = append(parts, fmt.Sprintf("相似组 %d", item.Cluster+1))
	}
	if item.HasAI {
		parts = append(parts, "已有 AI 标签")
	}
	switch bucket {
	case "auto":
		parts = append(parts, "高置信自动入库")
	case "review":
		parts = append(parts, "需要确认")
	case "skip":
		parts = append(parts, "未达画质门槛")
	}
	return strings.Join(parts, " · ")
}

func organizePreviewItems(auto, review []organizeCandidate) []map[string]any {
	items := make([]map[string]any, 0, len(auto)+len(review))
	appendGroup := func(bucket string, group []organizeCandidate) {
		for _, item := range group {
			items = append(items, map[string]any{
				"asset_id": item.AssetID, "bucket": bucket, "title": item.Title,
				"kind": item.Kind, "path": item.Path, "bytes": item.Bytes,
				"width": item.Width, "height": item.Height, "duration_ms": item.Duration,
				"quality": item.Quality, "cluster": item.Cluster, "tags": item.Tags,
				"confidence": organizeConfidence(item), "reason": organizeReason(item, bucket),
				"has_ai": item.HasAI,
			})
		}
	}
	appendGroup("auto", auto)
	appendGroup("review", review)
	return items
}

// applyOrganize 把方案里的高置信条目沉淀进库，并（按策略）清掉本次不再保留的自动条目。
func applyOrganize(in organizeApplyInput) (any, error) {
	runID := strings.TrimSpace(in.RunID)
	if runID == "" {
		return nil, fmt.Errorf("请指定整理方案")
	}
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	run, err := loadOrganizeRun(db, runID)
	if err != nil {
		return nil, err
	}
	library, err := getLibraryWithDB(db, run.LibraryID)
	if err != nil {
		return nil, err
	}
	root := filepath.Clean(library.RootURI)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("准备库目录失败: %w", err)
	}
	rows, err := db.Query(`SELECT d.asset_id,d.bucket,d.quality,d.cluster,d.reason,c.path,c.relative_path,c.name,c.kind,
		c.bytes,c.width,c.height,c.duration_ms,c.fps,c.codec,c.bit_depth
		FROM organize_decisions d JOIN candidates c ON c.id=d.asset_id
		WHERE d.run_id=? AND d.bucket='auto' ORDER BY d.quality DESC`, runID)
	if err != nil {
		return nil, err
	}
	type decision struct {
		assetID, bucket, reason, path, relative, title, kind string
		quality                                              float64
		cluster                                              int
		bytes                                                int64
		width, height                                        int
		duration                                             int64
		fps                                                  float64
		codec                                                string
		bitDepth                                             int
	}
	decisions := []decision{}
	for rows.Next() {
		var item decision
		if err := rows.Scan(&item.assetID, &item.bucket, &item.quality, &item.cluster, &item.reason,
			&item.path, &item.relative, &item.title, &item.kind, &item.bytes, &item.width, &item.height,
			&item.duration, &item.fps, &item.codec, &item.bitDepth); err != nil {
			rows.Close()
			return nil, err
		}
		decisions = append(decisions, item)
	}
	rows.Close()

	ingested := 0
	skipped := 0
	failed := []map[string]any{}
	for _, item := range decisions {
		destination, actualMode, err := placeOrganizeFile(root, item.path, item.relative, run.Policy.Mode)
		if err != nil {
			failed = append(failed, map[string]any{"asset_id": item.assetID, "error": err.Error()})
			continue
		}
		if destination == "" {
			skipped++
			continue
		}
		_ = actualMode
		info, statErr := os.Stat(destination)
		if statErr != nil {
			failed = append(failed, map[string]any{"asset_id": item.assetID, "error": statErr.Error()})
			continue
		}
		tx, err := db.Begin()
		if err != nil {
			return nil, err
		}
		itemID, err := upsertCuratedItem(tx, run.LibraryID, curatedRegistration{
			AssetID: item.assetID, SelectionID: "", Destination: destination, Relative: item.relative,
			Kind: item.kind, Title: firstNonEmpty(item.title, filepath.Base(destination)), Bytes: info.Size(),
			Width: item.width, Height: item.height, DurationMS: item.duration, FPS: item.fps,
			Codec: item.codec, BitDepth: item.bitDepth, SourcePath: item.path,
		})
		if err != nil {
			tx.Rollback()
			failed = append(failed, map[string]any{"asset_id": item.assetID, "error": err.Error()})
			continue
		}
		if _, err := tx.Exec(`UPDATE organize_decisions SET bucket='applied',item_id=?,updated_at=? WHERE run_id=? AND asset_id=?`,
			itemID, nowRFC3339(), runID, item.assetID); err != nil {
			tx.Rollback()
			return nil, err
		}
		logLibraryEvent(tx, run.LibraryID, itemID, "organize.ingest", map[string]any{
			"run_id": runID, "asset_id": item.assetID, "path": destination, "reason": item.reason,
		})
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		ingested++
	}

	removed := 0
	if run.Policy.Cleanup {
		removed = cleanupOrganizeItems(db, run.LibraryID)
	}
	summary := map[string]any{
		"ingested": ingested, "skipped": skipped, "removed": removed, "failed": failed,
	}
	status := "applied"
	if ingested == 0 && len(failed) > 0 {
		status = "failed"
	}
	encoded, _ := json.Marshal(summary)
	if _, err := db.Exec(`UPDATE organize_runs SET status=?,summary_json=?,updated_at=? WHERE id=?`,
		status, string(encoded), nowRFC3339(), runID); err != nil {
		return nil, err
	}
	return map[string]any{"run_id": runID, "status": status, "ingested": ingested,
		"skipped": skipped, "removed": removed, "failed": failed, "counts": summary}, nil
}

func loadOrganizeRun(db *sql.DB, runID string) (*organizeRun, error) {
	run := &organizeRun{}
	var policyJSON, summaryJSON string
	err := db.QueryRow(`SELECT id,library_id,source_id,status,policy_json,summary_json,created_at FROM organize_runs WHERE id=?`, runID).
		Scan(&run.ID, &run.LibraryID, &run.SourceID, &run.Status, &policyJSON, &summaryJSON, &run.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("整理方案不存在或已过期")
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(policyJSON), &run.Policy)
	run.Policy = run.Policy.normalized()
	_ = json.Unmarshal([]byte(summaryJSON), &run.Summary)
	return run, nil
}

func placeOrganizeFile(root, source, relative, mode string) (string, string, error) {
	if strings.TrimSpace(source) == "" {
		return "", "", fmt.Errorf("源路径不可用")
	}
	if strings.TrimSpace(relative) == "" {
		relative = filepath.Base(source)
	}
	destination := filepath.Join(root, filepath.FromSlash(relative))
	resolved, err := resolveWithin(root, destination)
	if err != nil {
		return "", "", err
	}
	destination = resolved
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return "", "", err
	}
	if info, statErr := os.Stat(destination); statErr == nil {
		if sourceInfo, sourceErr := os.Stat(source); sourceErr == nil && info.Size() == sourceInfo.Size() {
			return destination, "exists", nil
		}
		destination = uniqueDestination(destination)
	}
	actual := mode
	switch mode {
	case "reference":
		return source, "reference", nil
	default:
		if err := os.Link(source, destination); err == nil {
			return destination, "hardlink", nil
		}
		if err := copyFile(source, destination); err != nil {
			return "", "", err
		}
		actual = "copy"
	}
	return destination, actual, nil
}

// cleanupOrganizeItems 只清理「源文件已经不在了」的条目（默认只删索引，文件保留）。
// 不清「这次没被选中」的条目：重复整理不应该让库内容来回翻动。
func cleanupOrganizeItems(db *sql.DB, libraryID string) int {
	rows, err := db.Query(`SELECT id,COALESCE(local_path,''),COALESCE(relative_path,'')
		FROM library_items WHERE library_id=? AND deleted_at='' AND COALESCE(origin_selection_id,'')=''`, libraryID)
	if err != nil {
		return 0
	}
	type entry struct {
		id, local, relative string
	}
	stale := []entry{}
	for rows.Next() {
		var item entry
		if err := rows.Scan(&item.id, &item.local, &item.relative); err != nil {
			continue
		}
		info, statErr := os.Stat(item.local)
		if statErr != nil || info.IsDir() {
			stale = append(stale, item)
		}
	}
	rows.Close()
	if len(stale) == 0 {
		return 0
	}
	tx, err := db.Begin()
	if err != nil {
		return 0
	}
	defer tx.Rollback()
	now := nowRFC3339()
	removed := 0
	for _, item := range stale {
		if _, err := tx.Exec(`UPDATE library_items SET deleted_at=? WHERE id=? AND deleted_at=''`, now, item.id); err != nil {
			continue
		}
		_, _ = tx.Exec(`DELETE FROM search_fts WHERE entry_type='library' AND entry_id=?`, item.id)
		_, _ = tx.Exec(`DELETE FROM search_fts_v2 WHERE entry_type='library' AND entry_id=?`, item.id)
		logLibraryEvent(tx, libraryID, item.id, "organize.cleanup", map[string]any{"file": item.local})
		removed++
	}
	if err := tx.Commit(); err != nil {
		return 0
	}
	return removed
}

func listOrganizeReview(in organizeReviewListInput) (any, error) {
	runID := strings.TrimSpace(in.RunID)
	if runID == "" {
		return nil, fmt.Errorf("请指定整理方案")
	}
	limit := in.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT d.asset_id,d.quality,d.cluster,d.reason,
		COALESCE(c.path,''),COALESCE(c.relative_path,''),COALESCE(c.name,''),COALESCE(c.kind,''),
		COALESCE(c.bytes,0),COALESCE(c.width,0),COALESCE(c.height,0),COALESCE(c.duration_ms,0)
		FROM organize_decisions d LEFT JOIN candidates c ON c.id=d.asset_id
		WHERE d.run_id=? AND d.bucket='review'
		ORDER BY d.quality DESC LIMIT ? OFFSET ?`, runID, limit, in.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var assetID, reason, path, relative, name, kind string
		var quality float64
		var cluster, width, height int
		var bytes, duration int64
		if err := rows.Scan(&assetID, &quality, &cluster, &reason, &path, &relative, &name, &kind,
			&bytes, &width, &height, &duration); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{
			"asset_id": assetID, "candidate_id": assetID, "quality": quality, "cluster": cluster,
			"reason": reason, "path": path, "relative_path": relative, "title": name, "kind": kind,
			"bytes": bytes, "width": width, "height": height, "duration_ms": duration,
		})
	}
	return map[string]any{"run_id": runID, "items": items, "total": len(items)}, rows.Err()
}

func decideOrganizeReview(in organizeReviewDecideInput) (any, error) {
	runID := strings.TrimSpace(in.RunID)
	if runID == "" {
		return nil, fmt.Errorf("请指定整理方案")
	}
	if len(in.Items) == 0 {
		return nil, fmt.Errorf("请选择要处理的条目")
	}
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	run, err := loadOrganizeRun(db, runID)
	if err != nil {
		return nil, err
	}
	library, err := getLibraryWithDB(db, run.LibraryID)
	if err != nil {
		return nil, err
	}
	root := filepath.Clean(library.RootURI)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("准备库目录失败: %w", err)
	}
	accepted, rejected := 0, 0
	failed := []map[string]any{}
	for _, decision := range in.Items {
		assetID := strings.TrimSpace(decision.AssetID)
		if assetID == "" {
			continue
		}
		if !decision.Accept {
			if _, err := db.Exec(`UPDATE organize_decisions SET bucket='rejected',updated_at=? WHERE run_id=? AND asset_id=? AND bucket='review'`,
				nowRFC3339(), runID, assetID); err != nil {
				return nil, err
			}
			rejected++
			continue
		}
		var path, relative, title, kind string
		var bytes, duration int64
		var width, height int
		var fps float64
		var codec string
		var bitDepth int
		err := db.QueryRow(`SELECT COALESCE(c.path,''),COALESCE(c.relative_path,''),COALESCE(c.name,''),COALESCE(c.kind,''),
			COALESCE(c.bytes,0),COALESCE(c.width,0),COALESCE(c.height,0),COALESCE(c.duration_ms,0),
			COALESCE(c.fps,0),COALESCE(c.codec,''),COALESCE(c.bit_depth,0)
			FROM organize_decisions d JOIN candidates c ON c.id=d.asset_id
			WHERE d.run_id=? AND d.asset_id=? AND d.bucket='review'`, runID, assetID).
			Scan(&path, &relative, &title, &kind, &bytes, &width, &height, &duration, &fps, &codec, &bitDepth)
		if err == sql.ErrNoRows {
			failed = append(failed, map[string]any{"asset_id": assetID, "error": "条目不在待复核队列"})
			continue
		}
		if err != nil {
			return nil, err
		}
		destination, _, placeErr := placeOrganizeFile(root, path, relative, run.Policy.Mode)
		if placeErr != nil {
			failed = append(failed, map[string]any{"asset_id": assetID, "error": placeErr.Error()})
			continue
		}
		info, statErr := os.Stat(destination)
		if statErr != nil {
			failed = append(failed, map[string]any{"asset_id": assetID, "error": statErr.Error()})
			continue
		}
		tx, err := db.Begin()
		if err != nil {
			return nil, err
		}
		itemID, err := upsertCuratedItem(tx, run.LibraryID, curatedRegistration{
			AssetID: assetID, SelectionID: "", Destination: destination, Relative: relative,
			Kind: kind, Title: firstNonEmpty(title, filepath.Base(destination)), Bytes: info.Size(),
			Width: width, Height: height, DurationMS: duration, FPS: fps, Codec: codec, BitDepth: bitDepth,
			SourcePath: path,
		})
		if err != nil {
			tx.Rollback()
			failed = append(failed, map[string]any{"asset_id": assetID, "error": err.Error()})
			continue
		}
		if _, err := tx.Exec(`UPDATE organize_decisions SET bucket='applied',item_id=?,reason=?,updated_at=? WHERE run_id=? AND asset_id=?`,
			itemID, "复核通过", nowRFC3339(), runID, assetID); err != nil {
			tx.Rollback()
			return nil, err
		}
		logLibraryEvent(tx, run.LibraryID, itemID, "organize.review.accept", map[string]any{"run_id": runID, "asset_id": assetID})
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		accepted++
	}
	return map[string]any{"run_id": runID, "accepted": accepted, "rejected": rejected, "failed": failed}, nil
}

func listOrganizeRuns(in organizeRunsInput) (any, error) {
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	limit := in.Limit
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	query := `SELECT id,library_id,source_id,status,summary_json,created_at FROM organize_runs`
	args := []any{}
	if strings.TrimSpace(in.LibraryID) != "" {
		query += ` WHERE library_id=?`
		args = append(args, strings.TrimSpace(in.LibraryID))
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, libraryID, sourceID, status, summaryJSON, createdAt string
		if err := rows.Scan(&id, &libraryID, &sourceID, &status, &summaryJSON, &createdAt); err != nil {
			return nil, err
		}
		summary := map[string]any{}
		_ = json.Unmarshal([]byte(summaryJSON), &summary)
		items = append(items, map[string]any{
			"run_id": id, "library_id": libraryID, "source_id": sourceID, "status": status,
			"summary": summary, "created_at": createdAt,
		})
	}
	return map[string]any{"runs": items, "total": len(items)}, rows.Err()
}
