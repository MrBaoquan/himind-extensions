package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withSearchServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	// 测试不需要遵守 GitHub 的限流节奏。
	t.Setenv("HIMIND_TECH_RADAR_QUERY_INTERVAL_MS", "0")
	server := httptest.NewServer(handler)
	originalBase, originalClient := githubSearchBase, httpClient
	githubSearchBase = server.URL
	httpClient = server.Client()
	t.Cleanup(func() {
		githubSearchBase = originalBase
		httpClient = originalClient
		server.Close()
	})
}

func searchPayload() string {
	return `{"items":[
	  {"full_name":"acme/agent-kit","html_url":"https://github.com/acme/agent-kit","description":"agent toolkit","language":"Go","stargazers_count":900,"topics":["ai-agent"]},
	  {"full_name":"bigcorp/monorepo","html_url":"https://github.com/bigcorp/monorepo","description":"official","language":"Go","stargazers_count":5000,"topics":["ai-agent"]},
	  {"full_name":"acme/render-utils","html_url":"https://github.com/acme/render-utils","description":"render utils","language":"TypeScript","stargazers_count":300,"topics":["ai-agent"]}
	]}`
}

func TestCollectFiltersExcludesAndAnnotates(t *testing.T) {
	withSearchServer(t, func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("x-ratelimit-remaining", "57")
		_, _ = writer.Write([]byte(searchPayload()))
	})
	root := t.TempDir()
	result, err := collect(input{
		Topics:        []string{"ai-agent"},
		Languages:     []string{"Go"},
		ExcludeOwners: []string{"bigcorp"},
		TopN:          10,
		ReportRoot:    root,
		ReportDate:    "2026-09-19",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	payload := result.(map[string]any)
	snap := payload["snapshot"].(snapshot)
	if len(snap.Entries) != 1 || snap.Entries[0].FullName != "acme/agent-kit" {
		t.Fatalf("expected only the Go repo outside excluded owners, got %#v", snap.Entries)
	}
	entry := snap.Entries[0]
	if !entry.IsNew || entry.FirstSeen != "2026-09-19" || entry.Stars != 900 {
		t.Fatalf("unexpected annotation: %#v", entry)
	}
	rateLimit := snap.RateLimit
	if rateLimit["remaining"] != 57 {
		t.Fatalf("rate limit not captured: %#v", rateLimit)
	}
	if _, err := os.Stat(filepath.Join(root, "tech-radar-history.json")); err != nil {
		t.Fatalf("history ledger missing: %v", err)
	}
}

// 每个日期两次查询（active + rising），返回同一份 payload；第二天的星数变化
// 必须由扩展自己的历史算出 stars_gained —— 这是“热度”唯一可靠的来源。
func TestCollectComputesStarDeltasFromHistory(t *testing.T) {
	dayOne := `{"items":[{"full_name":"acme/a","html_url":"https://github.com/acme/a","language":"Go","stargazers_count":100},{"full_name":"acme/b","html_url":"https://github.com/acme/b","language":"Go","stargazers_count":50}]}`
	dayTwo := `{"items":[{"full_name":"acme/b","html_url":"https://github.com/acme/b","language":"Go","stargazers_count":500},{"full_name":"acme/a","html_url":"https://github.com/acme/a","language":"Go","stargazers_count":120},{"full_name":"acme/c","html_url":"https://github.com/acme/c","language":"Go","stargazers_count":80}]}`
	bodies := []string{dayOne, dayOne, dayTwo, dayTwo}
	index := 0
	withSearchServer(t, func(writer http.ResponseWriter, _ *http.Request) {
		position := index
		if position >= len(bodies) {
			position = len(bodies) - 1
		}
		index++
		_, _ = writer.Write([]byte(bodies[position]))
	})
	root := t.TempDir()
	if _, err := collect(input{Topics: []string{"ai"}, ReportRoot: root, ReportDate: "2026-09-18"}); err != nil {
		t.Fatalf("day one failed: %v", err)
	}
	result, err := collect(input{Topics: []string{"ai"}, ReportRoot: root, ReportDate: "2026-09-19"})
	if err != nil {
		t.Fatalf("day two failed: %v", err)
	}
	entries := result.(map[string]any)["snapshot"].(snapshot).Entries
	if len(entries) != 3 {
		t.Fatalf("expected three entries, got %d", len(entries))
	}
	byName := map[string]repoEntry{}
	for _, entry := range entries {
		byName[entry.FullName] = entry
	}
	if byName["acme/b"].StarsGained != 450 {
		t.Fatalf("expected acme/b to gain 450 stars, got %#v", byName["acme/b"])
	}
	if byName["acme/a"].StarsGained != 20 {
		t.Fatalf("expected acme/a to gain 20 stars, got %#v", byName["acme/a"])
	}
	if byName["acme/a"].FirstSeen != "2026-09-18" {
		t.Fatalf("expected first_seen to survive across runs, got %#v", byName["acme/a"])
	}
	if !byName["acme/c"].IsNew || byName["acme/c"].StarsGained != 0 {
		t.Fatalf("expected acme/c to be new without a delta, got %#v", byName["acme/c"])
	}
	// 涨得最多的排最前。
	if entries[0].FullName != "acme/b" {
		t.Fatalf("expected the fastest riser first, got %#v", entries[0])
	}
}

func TestRenderWritesSelfContainedReport(t *testing.T) {
	root := t.TempDir()
	snapshotValue := map[string]any{
		"generated_at": "2026-09-19T00:00:00Z",
		"rule_version": ruleVersion,
		"window_days":  7,
		"topics":       []string{"ai-agent"},
		"entries": []map[string]any{{
			"entry_id": "acme/agent-kit", "full_name": "acme/agent-kit", "html_url": "https://github.com/acme/agent-kit",
			"description": "<script>alert(1)</script>", "language": "Go", "stars": 900, "stars_gained": 0,
			"first_seen": "2026-09-19", "rank_delta": 0, "is_new": true,
		}},
	}
	insight := map[string]any{"insights": []any{map[string]any{"entry_id": "acme/agent-kit", "relevance": "high", "reason": "与 Agent 编排相关"}}}
	result, err := render(input{Snapshot: snapshotValue, Insight: insight, ReportRoot: root, ReportDate: "2026-09-19", Summary: "今日一句总结"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	report := result.(map[string]any)["report"].(map[string]any)
	if report["entry_count"] != 1 || report["new_entry_count"] != 1 || report["degraded"] != false {
		t.Fatalf("unexpected report summary: %#v", report)
	}
	htmlPath := report["html_path"].(string)
	body, err := os.ReadFile(htmlPath)
	if err != nil {
		t.Fatalf("html not written: %v", err)
	}
	content := string(body)
	if strings.Contains(content, "<script>alert(1)</script>") {
		t.Fatal("report must escape untrusted repository descriptions")
	}
	if !strings.Contains(content, "与 Agent 编排相关") || !strings.Contains(content, "今日一句总结") {
		t.Fatal("report must include insight reasons and summary")
	}
	markdown := report["dingtalk_markdown"].(string)
	// Markdown 也必须自证来源，并且带上条目本身（这里没有赛道标记，走“其他收录”）。
	if !strings.Contains(markdown, "acme/agent-kit") ||
		!strings.Contains(markdown, "来源：公开 GitHub 搜索接口") ||
		!strings.Contains(markdown, "与 Agent 编排相关") {
		t.Fatalf("unexpected markdown: %s", markdown)
	}
	var persisted map[string]any
	jsonBody, err := os.ReadFile(report["json_path"].(string))
	if err != nil {
		t.Fatalf("report json not written: %v", err)
	}
	if err := json.Unmarshal(jsonBody, &persisted); err != nil {
		t.Fatalf("report json invalid: %v", err)
	}
}

func TestEmptyInputFallsBackToDefaultDomains(t *testing.T) {
	groups, names, err := queryGroups(input{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groups) == 0 {
		t.Fatal("expected default domains to produce query groups")
	}
	if strings.Join(names, ",") != strings.Join(defaultDomains, ",") {
		t.Fatalf("expected default domains %v, got %v", defaultDomains, names)
	}
}

func TestUnknownDomainIsRejected(t *testing.T) {
	if _, _, err := queryGroups(input{Domains: []string{"graphics"}}); err == nil {
		t.Fatal("expected unknown domain preset to be rejected")
	}
}

func TestExplicitReportRootIsRemembered(t *testing.T) {
	dataRoot := t.TempDir()
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", dataRoot)
	custom := filepath.Join(t.TempDir(), "reports")
	resolved, err := reportRoot(custom)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved == dataRoot {
		t.Fatalf("expected custom root, got default %s", resolved)
	}
	// 后续不带 report_root 的调用（例如自带的归档视图）应该沿用同一个目录。
	again, err := reportRoot("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if again != resolved {
		t.Fatalf("expected remembered root %s, got %s", resolved, again)
	}
}

func TestEmptyReportRootFallsBackToExtensionDataDirectory(t *testing.T) {
	dataRoot := t.TempDir()
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", dataRoot)
	resolved, err := reportRoot("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != dataRoot {
		t.Fatalf("expected extension data root %s, got %s", dataRoot, resolved)
	}
}

func TestArchiveListReturnsRenderedReports(t *testing.T) {
	root := t.TempDir()
	snapshotValue := map[string]any{
		"generated_at": "2026-09-19T00:00:00Z",
		"rule_version": ruleVersion,
		"window_days":  7,
		"topics":       []string{"ai-agent"},
		"entries": []map[string]any{
			{"entry_id": "acme/a", "full_name": "acme/a", "html_url": "https://github.com/acme/a", "language": "Go", "stars": 10, "is_new": true},
			{"entry_id": "acme/b", "full_name": "acme/b", "html_url": "https://github.com/acme/b", "language": "Go", "stars": 5, "is_new": false, "rank_delta": 1},
		},
	}
	for _, date := range []string{"2026-09-18", "2026-09-19"} {
		if _, err := render(input{Snapshot: snapshotValue, ReportRoot: root, ReportDate: date, Summary: "第 " + date + " 期"}); err != nil {
			t.Fatalf("render %s failed: %v", date, err)
		}
	}
	result, err := listArchive(input{ReportRoot: root, TopN: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	payload := result.(map[string]any)
	if payload["total"] != 2 {
		t.Fatalf("expected two archived reports, got %#v", payload["total"])
	}
	records := payload["records"].([]archiveRecord)
	if records[0].ReportDate != "2026-09-19" || records[0].EntryCount != 2 || records[0].NewEntryCount != 1 {
		t.Fatalf("expected newest report first with counts, got %#v", records[0])
	}
	if records[0].HTMLPath == "" || records[0].SnapshotSHA256 == "" {
		t.Fatalf("archive record missing fields: %#v", records[0])
	}
	if !records[0].Degraded {
		t.Fatalf("a render without insight must be archived as degraded: %#v", records[0])
	}
	if _, err := render(input{
		Snapshot:   snapshotValue,
		Insight:    map[string]any{"summary": "有解读"},
		ReportRoot: root,
		ReportDate: "2026-09-19",
		Summary:    "第 2026-09-19 期",
	}); err != nil {
		t.Fatalf("render with insight failed: %v", err)
	}
	if records, err := listArchive(input{ReportRoot: root, ReportDate: "2026-09-19"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	} else if payload := records.(map[string]any); payload["total"] != 1 ||
		payload["records"].([]archiveRecord)[0].Degraded {
		t.Fatalf("expected the re-rendered report to replace the degraded record: %#v", payload)
	}
	filtered, err := listArchive(input{ReportRoot: root, ReportDate: "2026-09-18"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filtered.(map[string]any)["total"] != 1 {
		t.Fatal("expected date filter to narrow the archive to one report")
	}
}

func TestArchiveReadReturnsLatestReportForView(t *testing.T) {
	root := t.TempDir()
	snapshotValue := map[string]any{
		"generated_at": "2026-09-19T00:00:00Z",
		"rule_version": ruleVersion,
		"window_days":  7,
		"topics":       []string{"ai-agent"},
		"entries": []map[string]any{
			{"entry_id": "acme/a", "full_name": "acme/a", "html_url": "https://github.com/acme/a", "language": "Go", "stars": 10, "is_new": true},
		},
	}
	for _, date := range []string{"2026-09-17", "2026-09-19"} {
		if _, err := render(input{
			Snapshot:   snapshotValue,
			Insight:    map[string]any{"summary": "第 " + date + " 期解读"},
			ReportRoot: root,
			ReportDate: date,
			Summary:    "第 " + date + " 期",
		}); err != nil {
			t.Fatalf("render %s failed: %v", date, err)
		}
	}

	latest, err := readArchivedReport(input{ReportRoot: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	payload := latest.(map[string]any)
	if payload["report_date"] != "2026-09-19" {
		t.Fatalf("expected the newest report by default, got %#v", payload["report_date"])
	}
	if htmlBody, _ := payload["html"].(string); !strings.Contains(htmlBody, "acme/a") {
		t.Fatal("archived report HTML must be readable by the view")
	}
	if insight, ok := payload["insight"].(map[string]any); !ok || insight["summary"] != "第 2026-09-19 期解读" {
		t.Fatalf("expected the archived insight to be returned, got %#v", payload["insight"])
	}

	if _, err := readArchivedReport(input{ReportRoot: root, ReportDate: "2026-01-01"}); err == nil {
		t.Fatal("expected a missing report date to be rejected")
	}
	if _, err := readArchivedReport(input{ReportRoot: root, ReportDate: "../../etc"}); err == nil {
		t.Fatal("expected an invalid report date to be rejected")
	}
}

func upstreamContext(includeInsight bool) map[string]any {
	steps := map[string]any{
		"TR-COLLECT": map[string]any{
			"report_date": "2026-09-19",
			"snapshot": map[string]any{
				"generated_at": "2026-09-19T00:00:00Z",
				"rule_version": ruleVersion,
				"window_days":  7,
				"topics":       []string{"ai-agent"},
				"entries": []map[string]any{{
					"entry_id": "acme/agent-kit", "full_name": "acme/agent-kit", "html_url": "https://github.com/acme/agent-kit",
					"description": "agent toolkit", "language": "Go", "stars": 900, "stars_gained": 0, "is_new": true,
				}},
			},
		},
	}
	if includeInsight {
		steps["TR-INSIGHT"] = map[string]any{
			"insights":   []any{map[string]any{"entry_id": "acme/agent-kit", "relevance": "high", "reason": "与 Agent 编排相关"}},
			"highlights": []any{"acme/agent-kit"},
			"summary":    "AI 总结",
		}
	}
	return map[string]any{"steps": steps}
}

// 工作流只把上下文交给能力，扩展按工作流声明的步骤 id 取上游产物。
func TestRenderResolvesUpstreamSnapshotAndInsightFromWorkflowContext(t *testing.T) {
	root := t.TempDir()
	result, err := render(input{
		ReportRoot:      root,
		ReportDate:      "2026-09-19",
		WorkflowContext: upstreamContext(true),
		SnapshotStep:    "TR-COLLECT",
		InsightStep:     "TR-INSIGHT",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	report := result.(map[string]any)["report"].(map[string]any)
	if report["entry_count"] != 1 || report["degraded"] != false {
		t.Fatalf("unexpected report summary: %#v", report)
	}
	if report["summary"] != "AI 总结" {
		t.Fatalf("expected the insight summary to be reused, got %#v", report["summary"])
	}
	body, err := os.ReadFile(report["html_path"].(string))
	if err != nil {
		t.Fatalf("html not written: %v", err)
	}
	if !strings.Contains(string(body), "与 Agent 编排相关") {
		t.Fatal("report must render the upstream insight reason")
	}
}

// 解读步骤被降级（on_failure=continue）时上下文里没有它的输出，报告按规则稿出。
func TestRenderDegradesWhenTheInsightStepWasSkipped(t *testing.T) {
	root := t.TempDir()
	result, err := render(input{
		ReportRoot:      root,
		ReportDate:      "2026-09-19",
		WorkflowContext: upstreamContext(false),
		SnapshotStep:    "TR-COLLECT",
		InsightStep:     "TR-INSIGHT",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	report := result.(map[string]any)["report"].(map[string]any)
	if report["degraded"] != true || report["entry_count"] != 1 {
		t.Fatalf("expected a degraded rule-only report, got %#v", report)
	}
	body, err := os.ReadFile(report["html_path"].(string))
	if err != nil {
		t.Fatalf("html not written: %v", err)
	}
	if !strings.Contains(string(body), "acme/agent-kit") {
		t.Fatal("the degraded report must still list the collected repositories")
	}
	if _, err := os.Stat(filepath.Join(root, "2026-09-19", "insight.json")); err != nil {
		t.Fatalf("degraded report must still archive an insight placeholder: %v", err)
	}
	placeholder, err := os.ReadFile(filepath.Join(root, "2026-09-19", "insight.json"))
	if err != nil {
		t.Fatalf("insight placeholder is unreadable: %v", err)
	}
	if strings.TrimSpace(string(placeholder)) != "{}" {
		t.Fatalf("insight placeholder must be an empty object, got %s", placeholder)
	}
	if summary, _ := report["summary"].(string); !strings.Contains(summary, "规则稿") {
		t.Fatalf("degraded report must explain itself, got %#v", report["summary"])
	}
}

func TestOpenLinkRejectsUnusableInputBeforeTouchingTheSystem(t *testing.T) {
	cases := []struct {
		name  string
		input input
	}{
		{"empty", input{}},
		{"blank", input{URL: "   "}},
		{"too-long", input{URL: "https://github.com/" + strings.Repeat("a", 5000)}},
		{"control-characters", input{URL: "https://github.com/a\nb"}},
		{"missing-scheme", input{URL: "github.com/acme/agent-kit"}},
	}
	for _, testCase := range cases {
		if _, err := openLink(testCase.input); err == nil {
			t.Fatalf("%s: expected the link to be rejected", testCase.name)
		}
	}
}

// 工作流对 Artifact 做严格校验：内容要能通过声明的 schema，sha256 要与文件字节一致。
// 这条回归测试锁住的是“交付内容而不是内部台账/网页”这个契约。
func TestStepArtifactsMatchDeclaredSchemaAndDigest(t *testing.T) {
	withSearchServer(t, func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(searchPayload()))
	})
	root := t.TempDir()
	collected, err := collect(input{Topics: []string{"ai-agent"}, ReportRoot: root, ReportDate: "2026-09-19"})
	if err != nil {
		t.Fatalf("collect failed: %v", err)
	}
	snapshotArtifact := collected.(map[string]any)["artifacts"].([]map[string]any)[0]
	assertArtifactMatchesFile(t, snapshotArtifact, func(content map[string]any) {
		if _, ok := content["entries"].([]any); !ok {
			t.Fatalf("snapshot artifact must carry the entries array: %#v", content)
		}
	})

	snapshotValue := map[string]any{
		"generated_at": "2026-09-19T00:00:00Z",
		"rule_version": ruleVersion,
		"window_days":  7,
		"topics":       []string{"ai-agent"},
		"entries": []map[string]any{{
			"entry_id": "acme/agent-kit", "full_name": "acme/agent-kit", "html_url": "https://github.com/acme/agent-kit",
			"language": "Go", "stars": 900, "stars_gained": 0, "is_new": true,
		}},
	}
	rendered, err := render(input{Snapshot: snapshotValue, ReportRoot: root, ReportDate: "2026-09-19"})
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}
	reportArtifact := rendered.(map[string]any)["artifacts"].([]map[string]any)[0]
	assertArtifactMatchesFile(t, reportArtifact, func(content map[string]any) {
		for _, key := range []string{"report_date", "html_path", "entry_count", "snapshot_sha256"} {
			if _, ok := content[key]; !ok {
				t.Fatalf("report artifact is missing schema field %s: %#v", key, content)
			}
		}
	})
}

func assertArtifactMatchesFile(t *testing.T, artifact map[string]any, assertContent func(map[string]any)) {
	t.Helper()
	uri := strings.TrimPrefix(artifact["uri"].(string), "file:///")
	body, err := os.ReadFile(filepath.FromSlash(uri))
	if err != nil {
		t.Fatalf("artifact file is unavailable: %v", err)
	}
	digest := sha256.Sum256(body)
	if declared := artifact["sha256"].(string); declared != hex.EncodeToString(digest[:]) {
		t.Fatalf("artifact sha256 %s does not match the file content", declared)
	}
	var content map[string]any
	if err := json.Unmarshal(body, &content); err != nil {
		t.Fatalf("artifact content must be a JSON object: %v", err)
	}
	assertContent(content)
}
