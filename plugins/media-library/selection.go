package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"math/bits"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// 精选：精选集记录决策，精选库是沉淀目标。第一版先做“当前结果/文件夹 → 精选集 →
// 本机聚类 + 质量排序 → 人工调整 → 沉淀（hardlink/copy/reference）”。

type selectionCreateInput struct {
	Name        string   `json:"name"`
	AssetIDs    []string `json:"asset_ids"`
	SourceID    string   `json:"source_id"`
	Folder      string   `json:"folder"`
	TargetCount int      `json:"target_count"`
	TargetPath  string   `json:"target_path"`
	LibraryID   string   `json:"library_id"`
	PromoteMode string   `json:"promote_mode"`
	Actor       string   `json:"actor"`
}

type selectionDecisionInput struct {
	SelectionID string `json:"selection_id"`
	AssetID     string `json:"asset_id"`
	State       string `json:"state"`
}

type selectionPromoteInput struct {
	SelectionID string `json:"selection_id"`
	Mode        string `json:"mode"`
}

type selectionAutoInput struct {
	SelectionID  string `json:"selection_id"`
	Similarity   int    `json:"similarity"`
	TimeBudgetMS int    `json:"time_budget_ms"`
}

type selectionRecord struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	TargetKind  string `json:"target_kind"`
	TargetPath  string `json:"target_path"`
	PromoteMode string `json:"promote_mode"`
	TargetCount int    `json:"target_count"`
	Status      string `json:"status"`
	LibraryID   string `json:"library_id,omitempty"`
	Total       int    `json:"total"`
	Included    int    `json:"included"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type selectionItemRecord struct {
	AssetID      string  `json:"asset_id"`
	State        string  `json:"state"`
	Rank         int     `json:"rank,omitempty"`
	Score        float64 `json:"score,omitempty"`
	ScoreJSON    string  `json:"score_json,omitempty"`
	Reason       string  `json:"reason,omitempty"`
	Title        string  `json:"title,omitempty"`
	Kind         string  `json:"kind,omitempty"`
	Path         string  `json:"path,omitempty"`
	RelativePath string  `json:"relative_path,omitempty"`
	Folder       string  `json:"folder,omitempty"`
	Width        int     `json:"width,omitempty"`
	Height       int     `json:"height,omitempty"`
	DurationMS   int64   `json:"duration_ms,omitempty"`
	FPS          float64 `json:"fps,omitempty"`
	Codec        string  `json:"codec,omitempty"`
	BitDepth     int     `json:"bit_depth,omitempty"`
	ThumbState   string  `json:"thumb_state,omitempty"`
}

func createSelection(in selectionCreateInput) (any, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "精选"
	}
	targetCount := in.TargetCount
	if targetCount <= 0 {
		targetCount = 10
	}
	targetPath := strings.TrimSpace(in.TargetPath)
	libraryID := strings.TrimSpace(in.LibraryID)
	if libraryID != "" {
		library, err := getLibrary(libraryID)
		if err != nil {
			return nil, err
		}
		targetPath = library.RootURI
	}
	if targetPath == "" {
		return nil, fmt.Errorf("请选择精选库或填写精选库目录")
	}
	absoluteTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return nil, err
	}
	mode := strings.ToLower(strings.TrimSpace(in.PromoteMode))
	if mode == "" {
		mode = "hardlink"
	}
	if mode != "hardlink" && mode != "copy" && mode != "reference" {
		return nil, fmt.Errorf("沉淀方式必须是 hardlink、copy 或 reference")
	}
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	ids := in.AssetIDs
	if len(ids) == 0 {
		ids, err = selectionScopeIDs(db, in.SourceID, in.Folder)
		if err != nil {
			return nil, err
		}
	}
	ids = uniqueStrings(ids)
	if len(ids) == 0 {
		return nil, fmt.Errorf("没有可精选的素材")
	}
	if len(ids) > 1000 {
		ids = ids[:1000]
	}
	status := "draft"
	id := randomID("sel")
	now := nowRFC3339()
	scope, _ := json.Marshal(map[string]any{
		"asset_ids": ids, "source_id": in.SourceID, "folder": in.Folder,
	})
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO selections(id,name,scope_json,target_kind,target_path,promote_mode,target_count,status,library_id,criteria_json,created_by,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,'{}',?,?,?)`,
		id, name, string(scope), "folder", absoluteTarget, mode, targetCount, status, libraryID, safeActor(in.Actor), now, now); err != nil {
		return nil, err
	}
	for _, assetID := range ids {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO selection_items(selection_id,asset_id,state,score_json) VALUES(?,?,'candidate','{}')`, id, assetID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return getSelectionDetail(id)
}

func selectionScopeIDs(db *sql.DB, sourceID string, folder string) ([]string, error) {
	clauses := []string{"missing=0"}
	args := []any{}
	if strings.TrimSpace(sourceID) != "" {
		clauses = append(clauses, "source_id=?")
		args = append(args, strings.TrimSpace(sourceID))
	}
	if strings.TrimSpace(folder) != "" {
		clauses = append(clauses, "folder=?")
		args = append(args, strings.TrimSpace(folder))
	}
	query := `SELECT id FROM candidates WHERE ` + strings.Join(clauses, " AND ") + ` ORDER BY modified_at DESC LIMIT 1000`
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func listSelections() (any, error) {
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT s.id,s.name,s.target_kind,s.target_path,s.promote_mode,s.target_count,s.status,s.created_at,s.updated_at,
		(SELECT COUNT(*) FROM selection_items i WHERE i.selection_id=s.id),
		(SELECT COUNT(*) FROM selection_items i WHERE i.selection_id=s.id AND i.state='included')
		FROM selections s ORDER BY s.updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []selectionRecord{}
	for rows.Next() {
		var item selectionRecord
		if err := rows.Scan(&item.ID, &item.Name, &item.TargetKind, &item.TargetPath, &item.PromoteMode,
			&item.TargetCount, &item.Status, &item.CreatedAt, &item.UpdatedAt, &item.Total, &item.Included); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return map[string]any{"items": items, "total": len(items)}, rows.Err()
}

func getSelectionDetail(id string) (any, error) {
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var selection selectionRecord
	err = db.QueryRow(`SELECT s.id,s.name,s.target_kind,s.target_path,s.promote_mode,s.target_count,s.status,s.created_at,s.updated_at,
		COALESCE(s.library_id,''),
		(SELECT COUNT(*) FROM selection_items i WHERE i.selection_id=s.id),
		(SELECT COUNT(*) FROM selection_items i WHERE i.selection_id=s.id AND i.state='included')
		FROM selections s WHERE s.id=?`, id).Scan(&selection.ID, &selection.Name, &selection.TargetKind, &selection.TargetPath,
		&selection.PromoteMode, &selection.TargetCount, &selection.Status, &selection.CreatedAt, &selection.UpdatedAt,
		&selection.LibraryID, &selection.Total, &selection.Included)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("精选集不存在")
	}
	if err != nil {
		return nil, err
	}
	items, err := loadSelectionItems(db, id)
	if err != nil {
		return nil, err
	}
	library := map[string]any{}
	if selection.LibraryID != "" {
		if record, err := getLibraryWithDB(db, selection.LibraryID); err == nil {
			library = map[string]any{
				"id": record.ID, "name": record.Name, "kind": record.Kind,
				"root_path": record.RootURI, "asset_count": record.AssetCount, "state": record.State,
			}
		}
	}
	return map[string]any{"selection": selection, "items": items, "library": library}, nil
}

func loadSelectionItems(db *sql.DB, id string) ([]selectionItemRecord, error) {
	rows, err := db.Query(`SELECT i.asset_id,i.state,COALESCE(i.rank,0),COALESCE(i.score,0),i.score_json,i.reason,
		COALESCE(c.name,''),COALESCE(c.kind,''),COALESCE(c.path,''),COALESCE(c.relative_path,''),COALESCE(c.folder,''),
		COALESCE(c.width,0),COALESCE(c.height,0),COALESCE(c.duration_ms,0),COALESCE(c.fps,0),
		COALESCE(c.codec,''),COALESCE(c.bit_depth,0),COALESCE(c.thumb_state,'')
		FROM selection_items i LEFT JOIN candidates c ON c.id=i.asset_id
		WHERE i.selection_id=? ORDER BY i.rank ASC, i.asset_id ASC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []selectionItemRecord{}
	for rows.Next() {
		var item selectionItemRecord
		if err := rows.Scan(&item.AssetID, &item.State, &item.Rank, &item.Score, &item.ScoreJSON, &item.Reason,
			&item.Title, &item.Kind, &item.Path, &item.RelativePath, &item.Folder,
			&item.Width, &item.Height, &item.DurationMS, &item.FPS, &item.Codec, &item.BitDepth,
			&item.ThumbState); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

type selectionDecideBatchInput struct {
	SelectionID string `json:"selection_id"`
	Decisions   []struct {
		AssetID string `json:"asset_id"`
		State   string `json:"state"`
	} `json:"decisions"`
}

func decideSelectionBatch(in selectionDecideBatchInput) (any, error) {
	if strings.TrimSpace(in.SelectionID) == "" {
		return nil, fmt.Errorf("请指定精选")
	}
	if len(in.Decisions) == 0 || len(in.Decisions) > 1000 {
		return nil, fmt.Errorf("decisions 数量必须在 1 到 1000 之间")
	}
	for _, decision := range in.Decisions {
		switch strings.ToLower(strings.TrimSpace(decision.State)) {
		case "included", "excluded", "reserve", "candidate":
		default:
			return nil, fmt.Errorf("状态必须是 included、excluded、reserve 或 candidate")
		}
		if strings.TrimSpace(decision.AssetID) == "" {
			return nil, fmt.Errorf("每条决策都要带 asset_id")
		}
	}
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
	now := nowRFC3339()
	updated := int64(0)
	for _, decision := range in.Decisions {
		result, err := tx.Exec(`UPDATE selection_items SET state=?, decided_by=?, decided_at=? WHERE selection_id=? AND asset_id=?`,
			strings.ToLower(strings.TrimSpace(decision.State)), safeActor(""), now, in.SelectionID, decision.AssetID)
		if err != nil {
			return nil, err
		}
		affected, _ := result.RowsAffected()
		updated += affected
	}
	if _, err := tx.Exec(`UPDATE selections SET status='reviewing', updated_at=? WHERE id=?`, now, in.SelectionID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	detail, err := getSelectionDetail(in.SelectionID)
	if err != nil {
		return nil, err
	}
	if payload, ok := detail.(map[string]any); ok {
		payload["updated"] = updated
		return payload, nil
	}
	return detail, nil
}

func decideSelection(in selectionDecisionInput) (any, error) {
	state := strings.ToLower(strings.TrimSpace(in.State))
	switch state {
	case "included", "excluded", "reserve", "candidate":
	default:
		return nil, fmt.Errorf("状态必须是 included、excluded、reserve 或 candidate")
	}
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	result, err := db.Exec(`UPDATE selection_items SET state=?, decided_by=?, decided_at=? WHERE selection_id=? AND asset_id=?`,
		state, safeActor(""), nowRFC3339(), in.SelectionID, in.AssetID)
	if err != nil {
		return nil, err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return nil, fmt.Errorf("精选条目不存在")
	}
	_, _ = db.Exec(`UPDATE selections SET status='reviewing', updated_at=? WHERE id=?`, nowRFC3339(), in.SelectionID)
	return getSelectionDetail(in.SelectionID)
}

// ---------------------------------------------------------------------------
// 自动预选
// ---------------------------------------------------------------------------

type selectionCandidate struct {
	AssetID      string
	Path         string
	Kind         string
	Title        string
	RelativePath string
	Folder       string
	Width        int
	Height       int
	DurationMS   int64
	ModifiedAt   string
	ThumbState   string
	Hash         uint64
	HasHash      bool
	Quality      float64
	Sharpness    float64
	Exposure     float64
	Resolution   float64
	Duration     float64
	Cluster      int
}

func autoSelect(in selectionAutoInput) (any, error) {
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var targetCount int
	var status string
	if err := db.QueryRow(`SELECT target_count,status FROM selections WHERE id=?`, in.SelectionID).Scan(&targetCount, &status); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("精选集不存在")
		}
		return nil, err
	}
	if targetCount <= 0 {
		targetCount = 10
	}
	threshold := in.Similarity
	if threshold <= 0 || threshold > 64 {
		threshold = 12
	}
	started := time.Now()
	candidates, err := loadSelectionCandidates(db, in.SelectionID)
	if err != nil {
		return nil, err
	}
	for index := range candidates {
		candidate := &candidates[index]
		if candidate.Kind == "image" || candidate.Kind == "video" {
			thumb, thumbErr := ensureThumbnail(context.Background(), db, candidate.AssetID, candidate.Path,
				candidate.Kind, candidate.ModifiedAt, candidate.DurationMS, "grid")
			if thumbErr == nil {
				if value, ok := computeVisualHash(thumb); ok {
					candidate.Hash = value.hash
					candidate.HasHash = true
					candidate.Sharpness = value.sharpness
					candidate.Exposure = value.exposure
				}
			}
		} else if candidate.Kind == "audio" {
			candidate.Sharpness = 0.6
			candidate.Exposure = 0.8
		}
		candidate.Resolution = resolutionScore(candidate.Width, candidate.Height)
		candidate.Duration = durationScore(candidate.Kind, candidate.DurationMS)
		candidate.Quality = 100 * (0.5*candidate.Sharpness + 0.2*candidate.Exposure + 0.2*candidate.Resolution + 0.1*candidate.Duration)
	}
	// 相似聚类：按质量从高到低，和已有代表比较 pHash 汉明距离。
	sort.SliceStable(candidates, func(left, right int) bool { return candidates[left].Quality > candidates[right].Quality })
	representatives := []uint64{}
	for index := range candidates {
		candidate := &candidates[index]
		assigned := -1
		if candidate.HasHash {
			for cluster, representative := range representatives {
				if bits.OnesCount64(candidate.Hash^representative) <= threshold {
					assigned = cluster
					break
				}
			}
		}
		if assigned < 0 {
			assigned = len(representatives)
			if candidate.HasHash {
				representatives = append(representatives, candidate.Hash)
			} else {
				representatives = append(representatives, 0)
			}
		}
		candidate.Cluster = assigned
	}
	clusters := map[int][]selectionCandidate{}
	for _, candidate := range candidates {
		clusters[candidate.Cluster] = append(clusters[candidate.Cluster], candidate)
	}
	clusterOrder := make([]int, 0, len(clusters))
	for cluster := range clusters {
		clusterOrder = append(clusterOrder, cluster)
	}
	sort.SliceStable(clusterOrder, func(left, right int) bool {
		return clusters[clusterOrder[left]][0].Quality > clusters[clusterOrder[right]][0].Quality
	})
	selected := []selectionCandidate{}
	seen := map[string]bool{}
	for _, cluster := range clusterOrder {
		if len(selected) >= targetCount {
			break
		}
		best := clusters[cluster][0]
		selected = append(selected, best)
		seen[best.AssetID] = true
	}
	for _, candidate := range candidates {
		if len(selected) >= targetCount {
			break
		}
		if seen[candidate.AssetID] {
			continue
		}
		selected = append(selected, candidate)
		seen[candidate.AssetID] = true
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for index, candidate := range selected {
		score, _ := json.Marshal(map[string]any{
			"quality": candidate.Quality, "sharpness": candidate.Sharpness, "exposure": candidate.Exposure,
			"resolution": candidate.Resolution, "duration": candidate.Duration, "cluster": candidate.Cluster + 1,
		})
		reason := fmt.Sprintf("相似组 %d", candidate.Cluster+1)
		if !candidate.HasHash {
			reason = "独立素材"
		}
		if _, err := tx.Exec(`UPDATE selection_items SET state='included', rank=?, score=?, score_json=?, reason=?,
			decided_by='auto', decided_at=? WHERE selection_id=? AND asset_id=?`,
			index+1, candidate.Quality, string(score), reason, nowRFC3339(), in.SelectionID, candidate.AssetID); err != nil {
			return nil, err
		}
	}
	for _, candidate := range candidates {
		if seen[candidate.AssetID] {
			continue
		}
		score, _ := json.Marshal(map[string]any{
			"quality": candidate.Quality, "sharpness": candidate.Sharpness, "exposure": candidate.Exposure,
			"resolution": candidate.Resolution, "duration": candidate.Duration, "cluster": candidate.Cluster + 1,
		})
		if _, err := tx.Exec(`UPDATE selection_items SET state='reserve', rank=0, score=?, score_json=?, reason=?,
			decided_by='auto', decided_at=? WHERE selection_id=? AND asset_id=?`,
			candidate.Quality, string(score), fmt.Sprintf("备选 · 相似组 %d", candidate.Cluster+1), nowRFC3339(), in.SelectionID, candidate.AssetID); err != nil {
			return nil, err
		}
	}
	runID := randomID("selrun")
	params, _ := json.Marshal(map[string]any{"similarity": threshold, "target_count": targetCount})
	summary, _ := json.Marshal(map[string]any{
		"total": len(candidates), "included": len(selected), "clusters": len(clusters), "elapsed_ms": time.Since(started).Milliseconds(),
	})
	if _, err := tx.Exec(`INSERT INTO selection_runs(id,selection_id,algorithm,version,params_json,result_json,created_at)
		VALUES(?,?,'phash-quality-diversity','1',?,?,?)`, runID, in.SelectionID, string(params), string(summary), nowRFC3339()); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE selections SET status='reviewing', updated_at=? WHERE id=?`, nowRFC3339(), in.SelectionID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return getSelectionDetail(in.SelectionID)
}

func loadSelectionCandidates(db *sql.DB, selectionID string) ([]selectionCandidate, error) {
	rows, err := db.Query(`SELECT i.asset_id,c.path,c.kind,c.name,c.relative_path,c.folder,c.width,c.height,c.duration_ms,c.modified_at,c.thumb_state
		FROM selection_items i JOIN candidates c ON c.id=i.asset_id
		WHERE i.selection_id=? AND c.missing=0`, selectionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []selectionCandidate{}
	for rows.Next() {
		var item selectionCandidate
		if err := rows.Scan(&item.AssetID, &item.Path, &item.Kind, &item.Title, &item.RelativePath, &item.Folder,
			&item.Width, &item.Height, &item.DurationMS, &item.ModifiedAt, &item.ThumbState); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

type visualHash struct {
	hash      uint64
	sharpness float64
	exposure  float64
}

func computeVisualHash(path string) (visualHash, bool) {
	file, err := os.Open(path)
	if err != nil {
		return visualHash{}, false
	}
	defer file.Close()
	source, _, err := image.Decode(file)
	if err != nil {
		return visualHash{}, false
	}
	gray := resizeGray(source, 32, 32)
	coefficients := make([]float64, 64)
	for u := 0; u < 8; u++ {
		for v := 0; v < 8; v++ {
			sum := 0.0
			for y := 0; y < 32; y++ {
				for x := 0; x < 32; x++ {
					sum += gray[y*32+x] *
						math.Cos(float64((2*x+1)*u)*math.Pi/64) *
						math.Cos(float64((2*y+1)*v)*math.Pi/64)
				}
			}
			coefficients[u*8+v] = sum
		}
	}
	values := append([]float64{}, coefficients[1:]...)
	sort.Float64s(values)
	median := values[len(values)/2]
	var hash uint64
	for index := 1; index < 64; index++ {
		if coefficients[index] > median {
			hash |= 1 << uint(index-1)
		}
	}
	large := resizeGray(source, 64, 64)
	sharpness := laplacianVariance(large)
	sharpnessScore := sharpness / (sharpness + 300)
	exposure := exposureScore(large)
	return visualHash{hash: hash, sharpness: sharpnessScore, exposure: exposure}, true
}

func resizeGray(source image.Image, width int, height int) []float64 {
	bounds := source.Bounds()
	out := make([]float64, width*height)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			sx := bounds.Min.X + (x*bounds.Dx())/width
			sy := bounds.Min.Y + (y*bounds.Dy())/height
			r, g, b, _ := source.At(sx, sy).RGBA()
			out[y*width+x] = (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)) / 257.0
		}
	}
	return out
}

func laplacianVariance(gray []float64) float64 {
	// 创建 64x64 的拉普拉斯响应并求方差。
	const size = 64
	values := make([]float64, 0, (size-2)*(size-2))
	for y := 1; y < size-1; y++ {
		for x := 1; x < size-1; x++ {
			value := -4*gray[y*size+x] + gray[(y-1)*size+x] + gray[(y+1)*size+x] + gray[y*size+x-1] + gray[y*size+x+1]
			values = append(values, value)
		}
	}
	if len(values) == 0 {
		return 0
	}
	mean := 0.0
	for _, value := range values {
		mean += value
	}
	mean /= float64(len(values))
	variance := 0.0
	for _, value := range values {
		difference := value - mean
		variance += difference * difference
	}
	return variance / float64(len(values))
}

func exposureScore(gray []float64) float64 {
	if len(gray) == 0 {
		return 0
	}
	dark, bright := 0, 0
	for _, value := range gray {
		if value < 8 {
			dark++
		}
		if value > 247 {
			bright++
		}
	}
	clipped := float64(dark+bright) / float64(len(gray))
	score := 1 - clipped*1.8
	if score < 0 {
		score = 0
	}
	return score
}

func resolutionScore(width int, height int) float64 {
	if width <= 0 || height <= 0 {
		return 0.5
	}
	value := math.Log(float64(width*height)) / math.Log(3840.0*2160.0)
	if value > 1 {
		value = 1
	}
	return value
}

func durationScore(kind string, durationMS int64) float64 {
	if kind != "video" {
		return 0.8
	}
	seconds := float64(durationMS) / 1000.0
	if seconds <= 0 {
		return 0.4
	}
	if seconds >= 5 && seconds <= 120 {
		return 1
	}
	if seconds < 5 {
		return 0.6
	}
	return 0.7
}

// ---------------------------------------------------------------------------
// 沉淀到精选库
// ---------------------------------------------------------------------------

func promoteSelection(in selectionPromoteInput) (any, error) {
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var selection selectionRecord
	err = db.QueryRow(`SELECT id,name,target_kind,target_path,promote_mode,target_count,status,created_at,updated_at,COALESCE(library_id,'')
		FROM selections WHERE id=?`, in.SelectionID).Scan(&selection.ID, &selection.Name, &selection.TargetKind,
		&selection.TargetPath, &selection.PromoteMode, &selection.TargetCount, &selection.Status, &selection.CreatedAt,
		&selection.UpdatedAt, &selection.LibraryID)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("精选集不存在")
	}
	if err != nil {
		return nil, err
	}
	mode := strings.ToLower(strings.TrimSpace(in.Mode))
	if mode == "" {
		mode = selection.PromoteMode
	}
	if mode != "hardlink" && mode != "copy" && mode != "reference" {
		return nil, fmt.Errorf("沉淀方式必须是 hardlink、copy 或 reference")
	}
	// 精选库就是库：先确保这次精选有对应的库，再把文件与条目写进去。
	libraryID, err := curatedLibraryForSelection(selection.ID, selection.Name, selection.TargetPath)
	if err != nil {
		return nil, err
	}
	if library, err := getLibrary(libraryID); err == nil && strings.TrimSpace(library.RootURI) != "" {
		selection.TargetPath = library.RootURI
	}
	target := selection.TargetPath
	if err := os.MkdirAll(target, 0o755); err != nil {
		return nil, fmt.Errorf("创建精选库目录失败: %w", err)
	}
	metaDir := filepath.Join(target, ".himind-media")
	if err := os.MkdirAll(metaDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建精选元数据目录失败: %w", err)
	}
	items, err := loadSelectionItems(db, selection.ID)
	if err != nil {
		return nil, err
	}
	saved := []map[string]any{}
	skipped := []map[string]any{}
	failed := []map[string]any{}
	registered := []curatedRegistration{}
	logPath := filepath.Join(metaDir, "curation.jsonl")
	logFile, logErr := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if logErr == nil {
		defer logFile.Close()
	}
	for _, item := range items {
		if item.State != "included" {
			continue
		}
		source := item.Path
		if source == "" {
			failed = append(failed, map[string]any{"asset_id": item.AssetID, "error": "源路径不可用"})
			continue
		}
		relative := item.RelativePath
		if strings.TrimSpace(relative) == "" {
			relative = filepath.Base(source)
		}
		destination := filepath.Join(target, filepath.FromSlash(relative))
		resolved, resolveErr := resolveWithin(target, destination)
		if resolveErr != nil {
			failed = append(failed, map[string]any{"asset_id": item.AssetID, "error": resolveErr.Error()})
			continue
		}
		destination = resolved
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			failed = append(failed, map[string]any{"asset_id": item.AssetID, "error": err.Error()})
			continue
		}
		if info, statErr := os.Stat(destination); statErr == nil {
			if sourceInfo, sourceErr := os.Stat(source); sourceErr == nil && info.Size() == sourceInfo.Size() {
				skipped = append(skipped, map[string]any{"asset_id": item.AssetID, "path": destination, "reason": "已存在"})
				registered = append(registered, curatedRegistration{
					AssetID: item.AssetID, SelectionID: selection.ID, Destination: destination, Relative: relative,
					Kind: item.Kind, Title: firstNonEmpty(item.Title, filepath.Base(destination)), Bytes: info.Size(),
					Width: item.Width, Height: item.Height, DurationMS: item.DurationMS,
					FPS: item.FPS, Codec: item.Codec, BitDepth: item.BitDepth, SourcePath: source,
				})
				continue
			}
			destination = uniqueDestination(destination)
		}
		actualMode := mode
		location := source
		switch mode {
		case "reference":
			location = source
		case "hardlink", "copy":
			linkErr := os.Link(source, destination)
			if linkErr == nil {
				location = destination
			} else if mode == "copy" || strings.EqualFold(filepath.VolumeName(source), filepath.VolumeName(target)) == false {
				if err := copyFile(source, destination); err != nil {
					failed = append(failed, map[string]any{"asset_id": item.AssetID, "error": err.Error()})
					continue
				}
				location = destination
				if mode == "hardlink" {
					actualMode = "copy"
				}
			} else {
				failed = append(failed, map[string]any{"asset_id": item.AssetID, "error": linkErr.Error()})
				continue
			}
		}
		now := nowRFC3339()
		if _, err := db.Exec(`INSERT INTO curated_entries(selection_id,asset_id,location_path,mode,created_at) VALUES(?,?,?,?,?)
			ON CONFLICT(selection_id,asset_id) DO UPDATE SET location_path=excluded.location_path,mode=excluded.mode`,
			selection.ID, item.AssetID, location, actualMode, now); err != nil {
			failed = append(failed, map[string]any{"asset_id": item.AssetID, "error": err.Error()})
			continue
		}
		_, _ = db.Exec(`INSERT INTO asset_locations(asset_id,path,source_id,role,identity) VALUES(?,?,?,'curated','path')
			ON CONFLICT(asset_id,path) DO NOTHING`, item.AssetID, location, "")
		if logFile != nil {
			event, _ := json.Marshal(map[string]any{
				"selection_id": selection.ID, "asset_id": item.AssetID, "source_path": source,
				"target_path": location, "mode": actualMode, "created_at": now, "actor": safeActor(""),
			})
			_, _ = logFile.Write(append(event, '\n'))
		}
		saved = append(saved, map[string]any{"asset_id": item.AssetID, "path": location, "mode": actualMode})
		size := int64(0)
		if info, statErr := os.Stat(location); statErr == nil {
			size = info.Size()
		}
		registered = append(registered, curatedRegistration{
			AssetID: item.AssetID, SelectionID: selection.ID, Destination: location, Relative: relative,
			Kind: item.Kind, Title: firstNonEmpty(item.Title, filepath.Base(location)), Bytes: size,
			Width: item.Width, Height: item.Height, DurationMS: item.DurationMS,
			FPS: item.FPS, Codec: item.Codec, BitDepth: item.BitDepth, SourcePath: source,
		})
	}
	// 把结果登记成精选库条目：这样精选库会出现在资源库列表里，也能被检索命中。
	registeredItems := 0
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for _, entry := range registered {
		if _, err := upsertCuratedItem(tx, libraryID, entry); err != nil {
			failed = append(failed, map[string]any{"asset_id": entry.AssetID, "error": err.Error()})
			continue
		}
		registeredItems++
	}
	if _, err := tx.Exec(`UPDATE selections SET status='done', library_id=?, updated_at=? WHERE id=?`, libraryID, nowRFC3339(), selection.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	libraryName := ""
	libraryItems := 0
	if library, err := getLibrary(libraryID); err == nil {
		libraryName = library.Name
		libraryItems = library.AssetCount
	}
	return map[string]any{
		"selection_id": selection.ID, "target_path": target, "mode": mode,
		"library_id": libraryID, "library_name": libraryName, "library_kind": libraryKindCurated,
		"library_items": libraryItems, "registered": registeredItems,
		"saved": saved, "skipped": skipped, "failed": failed,
	}, nil
}

func uniqueDestination(path string) string {
	extension := filepath.Ext(path)
	base := strings.TrimSuffix(path, extension)
	for index := 1; index < 1000; index++ {
		candidate := fmt.Sprintf("%s-%d%s", base, index, extension)
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
	return path
}
