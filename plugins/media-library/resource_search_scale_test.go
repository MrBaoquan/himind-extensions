package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

type searchBenchScenarioResult struct {
	Name    string   `json:"name"`
	Query   string   `json:"query"`
	Scope   string   `json:"scope"`
	Kinds   []string `json:"kinds,omitempty"`
	Limit   int      `json:"limit"`
	P50MS   float64  `json:"p50_ms"`
	P95MS   float64  `json:"p95_ms"`
	MaxMS   float64  `json:"max_ms"`
	Results int      `json:"results"`
}

type searchBenchDatasetResult struct {
	Rows          int                         `json:"rows"`
	LibraryRows   int                         `json:"library_rows"`
	CandidateRows int                         `json:"candidate_rows"`
	DBBytes       int64                       `json:"db_bytes"`
	SeedMS        float64                     `json:"seed_ms"`
	Iterations    int                         `json:"iterations"`
	Scenarios     []searchBenchScenarioResult `json:"scenarios"`
	Plans         map[string][]string         `json:"plans"`
}

func TestSearchScaleBenchmark(t *testing.T) {
	rawRows := strings.TrimSpace(os.Getenv("HIMIND_SEARCH_BENCH_ROWS"))
	if rawRows == "" {
		t.Skip("set HIMIND_SEARCH_BENCH_ROWS=10000,50000 to run the scale benchmark")
	}
	iterations, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("HIMIND_SEARCH_BENCH_ITERATIONS")))
	if iterations <= 0 {
		iterations = 25
	}
	var sizes []int
	for _, value := range strings.Split(rawRows, ",") {
		size, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || size <= 0 {
			t.Fatalf("invalid HIMIND_SEARCH_BENCH_ROWS value %q", value)
		}
		sizes = append(sizes, size)
	}
	outputPath := strings.TrimSpace(os.Getenv("HIMIND_SEARCH_BENCH_OUTPUT"))
	all := make([]searchBenchDatasetResult, 0, len(sizes))
	for _, size := range sizes {
		size := size
		t.Run(fmt.Sprintf("rows-%d", size), func(t *testing.T) {
			t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
			started := time.Now()
			seedSearchBenchmark(t, size)
			result := runSearchBenchmark(t, size, iterations)
			result.SeedMS = millis(time.Since(started))
			all = append(all, result)
			encoded, _ := json.Marshal(result)
			t.Logf("BENCH_RESULT %s", encoded)
		})
	}
	if outputPath != "" {
		encoded, _ := json.MarshalIndent(all, "", "  ")
		if err := os.WriteFile(outputPath, append(encoded, '\n'), 0o644); err != nil {
			t.Fatalf("write benchmark output: %v", err)
		}
	}
}

func seedSearchBenchmark(t *testing.T, rows int) {
	t.Helper()
	db, err := openLibraryDB()
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	root, err := pluginDataRoot()
	if err != nil {
		t.Fatalf("plugin data root: %v", err)
	}
	dummyFile := filepath.Join(root, "benchmark-asset.bin")
	if err := os.WriteFile(dummyFile, []byte("benchmark"), 0o644); err != nil {
		t.Fatalf("write dummy asset: %v", err)
	}
	now := "2026-10-28T00:00:00Z"
	if _, err := db.Exec(`INSERT INTO libraries(id,name,backend_type,root_uri,access_mode,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, "bench-lib", "Benchmark Library", "local", root, "contribute", now, now); err != nil {
		t.Fatalf("insert benchmark library: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO source_roots(id,path,display_name,created_at) VALUES(?,?,?,?)`, "bench-source", root, "Benchmark Source", now); err != nil {
		t.Fatalf("insert benchmark source: %v", err)
	}
	const batchSize = 1000
	for start := 0; start < rows; start += batchSize {
		end := start + batchSize
		if end > rows {
			end = rows
		}
		tx, err := db.Begin()
		if err != nil {
			t.Fatalf("begin seed batch: %v", err)
		}
		candidateStmt, err := tx.Prepare(`INSERT INTO candidates(id,source_id,path,relative_path,name,kind,extension,bytes,modified_at,seen_generation,missing,indexed_level,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
		if err != nil {
			t.Fatalf("prepare candidate: %v", err)
		}
		libraryStmt, err := tx.Prepare(`INSERT INTO library_items(id,library_id,sha256,relative_path,local_path,kind,title,mime,bytes,author,license_name,license_url,redistribution_allowed,source_provider,source_id,source_url,adopted_by,adopted_at,updated_at,metadata_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
		if err != nil {
			t.Fatalf("prepare library item: %v", err)
		}
		ftsStmt, err := tx.Prepare(`INSERT INTO search_fts(entry_type,entry_id,library_id,content) VALUES(?,?,?,?)`)
		if err != nil {
			t.Fatalf("prepare FTS: %v", err)
		}
		ftsV2Stmt, err := tx.Prepare(`INSERT INTO search_fts_v2(entry_type,entry_id,library_id,kind,updated_at,title,filename,path,tags,collection,license,author) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`)
		if err != nil {
			t.Fatalf("prepare FTS v2: %v", err)
		}
		for i := start; i < end; i++ {
			kind := []string{"image", "video", "audio"}[i%3]
			updated := time.Unix(int64(1761600000+i), 0).UTC().Format(time.RFC3339)
			if i%5 < 3 {
				id := fmt.Sprintf("bench-lib-item-%06d", i)
				title := fmt.Sprintf("benchmark media %06d", i)
				if i%10 == 0 {
					title = fmt.Sprintf("城市夜景 %06d", i)
				} else if i%7 == 0 {
					title = fmt.Sprintf("city skyline %06d", i)
				}
				filename := fmt.Sprintf("clip-%06d.%s", i, map[string]string{"image": "png", "video": "mp4", "audio": "mp3"}[kind])
				relative := filepath.ToSlash(filepath.Join("assets", fmt.Sprintf("%02d", i%100), filename))
				collection := fmt.Sprintf("素材/城市/zone-alpha/group-%02d", i%100)
				metadata, _ := json.Marshal(map[string]any{"tags": []string{"benchmark", "热点", fmt.Sprintf("group-%02d", i%100)}, "collection": collection})
				if _, err := libraryStmt.Exec(id, "bench-lib", fmt.Sprintf("sha-%06d", i), relative, dummyFile, kind, title, "", int64(1024+i), "Benchmark", "CC BY 4.0", "https://creativecommons.org/licenses/by/4.0/", 1, "benchmark", fmt.Sprintf("provider-%06d", i), "https://example.invalid/item", "bench", updated, updated, string(metadata)); err != nil {
					t.Fatalf("insert library item %d: %v", i, err)
				}
				content := strings.Join([]string{title, filename, relative, "benchmark", "热点", collection, "CC BY 4.0", "Benchmark"}, " ")
				if _, err := ftsStmt.Exec("library", id, "bench-lib", content); err != nil {
					t.Fatalf("insert library FTS %d: %v", i, err)
				}
				if _, err := ftsV2Stmt.Exec("library", id, "bench-lib", kind, updated, title, filename, relative, "benchmark 热点", collection, "CC BY 4.0", "Benchmark"); err != nil {
					t.Fatalf("insert library FTS v2 %d: %v", i, err)
				}
			} else {
				id := fmt.Sprintf("bench-candidate-%06d", i)
				name := fmt.Sprintf("city-clip-%06d.%s", i, map[string]string{"image": "png", "video": "mp4", "audio": "mp3"}[kind])
				if i%10 == 0 {
					name = fmt.Sprintf("城市候选-%06d.png", i)
				}
				relative := filepath.ToSlash(filepath.Join("候选", fmt.Sprintf("group-%02d", i%100), name))
				path := filepath.Join(root, "candidates", relative)
				if _, err := candidateStmt.Exec(id, "bench-source", path, relative, name, kind, filepath.Ext(name), int64(2048+i), updated, "generation", 0, 1, updated, updated); err != nil {
					t.Fatalf("insert candidate %d: %v", i, err)
				}
				content := strings.Join([]string{name, relative, "benchmark"}, " ")
				if _, err := ftsStmt.Exec("candidate", id, "", content); err != nil {
					t.Fatalf("insert candidate FTS %d: %v", i, err)
				}
				if _, err := ftsV2Stmt.Exec("candidate", id, "", kind, updated, name, name, relative, "", "", "", ""); err != nil {
					t.Fatalf("insert candidate FTS v2 %d: %v", i, err)
				}
			}
		}
		candidateStmt.Close()
		libraryStmt.Close()
		ftsStmt.Close()
		ftsV2Stmt.Close()
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit seed batch: %v", err)
		}
	}
	if _, err := db.Exec(`INSERT INTO search_fts(search_fts) VALUES('optimize')`); err != nil {
		t.Fatalf("optimize FTS: %v", err)
	}
}

func runSearchBenchmark(t *testing.T, rows int, iterations int) searchBenchDatasetResult {
	t.Helper()
	scenarios := []struct {
		name  string
		input resourceSearchInput
	}{
		{name: "selective_filename", input: resourceSearchInput{Query: "clip-000123", Scope: "all", Limit: 60}},
		{name: "common_tag", input: resourceSearchInput{Query: "benchmark", Scope: "all", Limit: 60}},
		{name: "collection", input: resourceSearchInput{Query: "zone-alpha", Scope: "library", Limit: 60}},
		{name: "short_chinese", input: resourceSearchInput{Query: "城市", Scope: "all", Limit: 60}},
		{name: "combined_filters", input: resourceSearchInput{Query: "benchmark", Scope: "library", LibraryID: "bench-lib", Kinds: []string{"audio"}, Limit: 60}},
		{name: "recent_no_query", input: resourceSearchInput{Scope: "all", Limit: 60}},
	}
	results := make([]searchBenchScenarioResult, 0, len(scenarios))
	for _, scenario := range scenarios {
		for warmup := 0; warmup < 5; warmup++ {
			if _, err := searchResources(scenario.input); err != nil {
				t.Fatalf("%s warmup: %v", scenario.name, err)
			}
		}
		durations := make([]float64, 0, iterations)
		resultCount := 0
		for iteration := 0; iteration < iterations; iteration++ {
			started := time.Now()
			value, err := searchResources(scenario.input)
			elapsed := millis(time.Since(started))
			if err != nil {
				t.Fatalf("%s iteration %d: %v", scenario.name, iteration, err)
			}
			durations = append(durations, elapsed)
			if payload, ok := value.(map[string]any); ok {
				if items, ok := payload["items"].([]resourceSearchItem); ok {
					resultCount = len(items)
				}
			}
		}
		sort.Float64s(durations)
		results = append(results, searchBenchScenarioResult{
			Name: scenario.name, Query: scenario.input.Query, Scope: scenario.input.Scope,
			Kinds: scenario.input.Kinds, Limit: scenario.input.Limit,
			P50MS: percentile(durations, 0.50), P95MS: percentile(durations, 0.95),
			MaxMS: durations[len(durations)-1], Results: resultCount,
		})
	}
	root, _ := pluginDataRoot()
	dbPath := filepath.Join(root, "library.db")
	info, _ := os.Stat(dbPath)
	dbBytes := int64(0)
	if info != nil {
		dbBytes = info.Size()
	}
	libraryRows := (rows*3 + 4) / 5
	return searchBenchDatasetResult{
		Rows: rows, LibraryRows: libraryRows, CandidateRows: rows - libraryRows,
		DBBytes: dbBytes, Iterations: iterations, Scenarios: results, Plans: collectSearchBenchmarkPlans(t),
	}
}

func collectSearchBenchmarkPlans(t *testing.T) map[string][]string {
	t.Helper()
	db, err := openLibraryDB()
	if err != nil {
		t.Fatalf("open db for plans: %v", err)
	}
	defer db.Close()
	queries := map[string]struct {
		sql  string
		args []any
	}{
		"fts":              {sql: `EXPLAIN QUERY PLAN SELECT entry_id FROM search_fts WHERE search_fts MATCH ? LIMIT 500`, args: []any{`"benchmark"`}},
		"fts_v2":           {sql: `EXPLAIN QUERY PLAN SELECT entry_id,bm25(search_fts_v2,0,0,0,0,0,8,5,2,6,6,1,1) FROM search_fts_v2 WHERE search_fts_v2 MATCH ? ORDER BY 2 LIMIT 60`, args: []any{`"benchmark"*`}},
		"library_recent":   {sql: `EXPLAIN QUERY PLAN SELECT id FROM library_items ORDER BY adopted_at DESC,id ASC LIMIT 60`},
		"candidate_recent": {sql: `EXPLAIN QUERY PLAN SELECT id FROM candidates WHERE missing=0 ORDER BY updated_at DESC,id ASC LIMIT 60`},
		"library_like":     {sql: `EXPLAIN QUERY PLAN SELECT id FROM library_items WHERE (title LIKE ? OR relative_path LIKE ? OR license_name LIKE ?) ORDER BY adopted_at DESC LIMIT 60`, args: []any{"%城市%", "%城市%", "%城市%"}},
		"candidate_like":   {sql: `EXPLAIN QUERY PLAN SELECT id FROM candidates WHERE missing=0 AND (name LIKE ? OR relative_path LIKE ?) ORDER BY updated_at DESC LIMIT 60`, args: []any{"%城市%", "%城市%"}},
	}
	plans := map[string][]string{}
	for name, query := range queries {
		rows, err := db.Query(query.sql, query.args...)
		if err != nil {
			t.Fatalf("query plan %s: %v", name, err)
		}
		for rows.Next() {
			var id, parent, notUsed int
			var detail string
			if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
				rows.Close()
				t.Fatalf("scan plan %s: %v", name, err)
			}
			plans[name] = append(plans[name], detail)
		}
		rows.Close()
	}
	return plans
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	index := int(math.Ceil(float64(len(sorted))*p)) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

func millis(duration time.Duration) float64 {
	return math.Round(float64(duration.Microseconds())/10) / 100
}
