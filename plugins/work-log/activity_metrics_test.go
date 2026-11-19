package main

import (
	"strings"
	"testing"
)

func TestActivityMetricsParallelAndDuplicateEvidence(t *testing.T) {
	intervals := []activityInterval{
		{From: "2026-11-19T10:00:00+08:00", To: "2026-11-19T10:20:00+08:00"},
		{From: "2026-11-19T10:20:00+08:00", To: "2026-11-19T10:40:00+08:00"},
		{From: "2026-11-19T10:40:00+08:00", To: "2026-11-19T11:00:00+08:00"},
	}
	a := entry{Source: "codex", SessionID: "a", Date: "2026-11-19", ActivityIntervals: intervals}
	b := entry{Source: "codex", SessionID: "b", Date: a.Date, ActivityIntervals: intervals}
	c := entry{Source: "claude-code", SessionID: "c", Date: a.Date, ActivityIntervals: intervals}
	for _, items := range [][]entry{{a, b, c, a}, {c, a, b, a}} {
		got := calculateActivityMetrics(items)
		if got.CoverageMinutes != 60 || got.SessionMinutes != 180 || got.UnknownSessions != 0 {
			t.Fatalf("wrong parallel activity: %+v", got)
		}
	}
}

func TestActivityMetricsUnknownManualAndTimezones(t *testing.T) {
	items := []entry{
		{Source: "codex", SessionID: "a", Date: "2026-11-19", ActivityIntervals: []activityInterval{{From: "2026-11-19T10:00:00+08:00", To: "2026-11-19T10:30:00+08:00"}}},
		{Source: "codex", SessionID: "b", Date: "2026-11-19", ActivityIntervals: []activityInterval{{From: "2026-11-19T02:15:00Z", To: "2026-11-19T02:45:00Z"}}},
		{Source: "codex", SessionID: "unknown", Date: "2026-11-19", ActivityIntervals: []activityInterval{{From: "bad", To: "bad"}}},
		{EntryID: "manual", MinutesSource: "manual", Minutes: 90},
	}
	got := calculateActivityMetrics(items)
	if got.CoverageMinutes != 45 || got.SessionMinutes != 60 || got.UnknownSessions != 1 || got.ManualMinutes != 90 {
		t.Fatalf("wrong activity evidence: %+v", got)
	}
}

func TestActivityMetricsAggregatesFractionalMinutesBeforeRounding(t *testing.T) {
	first := entry{Source: "codex", SessionID: "a", Date: "2026-11-19", ActivityIntervals: []activityInterval{{From: "2026-11-19T10:00:00+08:00", To: "2026-11-19T10:00:40+08:00"}}}
	second := entry{Source: "codex", SessionID: "b", Date: first.Date, ActivityIntervals: []activityInterval{{From: "2026-11-19T10:01:00+08:00", To: "2026-11-19T10:01:40+08:00"}}}
	for _, items := range [][]entry{{first, second, first}, {second, first, second}} {
		got := calculateActivityMetrics(items)
		if got.CoverageMinutes != 1 || got.SessionMinutes != 1 {
			t.Fatalf("fractional activity was rounded before aggregation: %+v", got)
		}
	}
}

func TestAllocateAgentMinutesPreservesParallelSessions(t *testing.T) {
	spans := []activityInterval{{From: "2026-11-19T10:00:00+08:00", To: "2026-11-19T11:00:00+08:00"}}
	digests := []materialDigest{
		{Provider: "agent", Source: "codex", SessionID: "a", ActivityIntervals: spans},
		{Provider: "agent", Source: "codex", SessionID: "b", ActivityIntervals: spans},
		{Provider: "agent", Source: "claude-code", SessionID: "c", ActivityIntervals: spans},
	}
	allocateAgentMinutes(digests)
	for _, digest := range digests {
		if digest.Minutes != 60 || digest.OverlapAdjusted {
			t.Fatalf("parallel session lost activity: %+v", digest)
		}
	}
}

func TestReportShowsParallelActivityMetrics(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	interval := []activityInterval{{From: "2026-11-19T10:00:00+08:00", To: "2026-11-19T11:00:00+08:00"}}
	items := []entry{
		{EntryID: "a", Date: "2026-11-19", Source: "codex", SessionID: "a", Title: "A", Minutes: 60, MinutesSource: "estimated", ActivityIntervals: interval},
		{EntryID: "b", Date: "2026-11-19", Source: "codex", SessionID: "b", Title: "B", Minutes: 60, MinutesSource: "estimated", ActivityIntervals: interval},
		{EntryID: "c", Date: "2026-11-19", Source: "claude-code", SessionID: "c", Title: "C", Minutes: 60, MinutesSource: "estimated", ActivityIntervals: interval},
	}
	if err := writeDay(root, dayFile{Date: "2026-11-19", Entries: items}); err != nil {
		t.Fatal(err)
	}
	report, err := renderPeriodReport(root, "daily", "2026-11-19", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := report["activity_metrics"].(activityMetrics)
	if got.CoverageMinutes != 60 || got.SessionMinutes != 180 {
		t.Fatalf("wrong report metrics: %+v", got)
	}
	markdown := report["markdown"].(string)
	if !strings.Contains(markdown, "活动覆盖：约 1.0 小时") || !strings.Contains(markdown, "会话累计活动：约 3.0 小时") {
		t.Fatalf("report does not explain activity metrics: %s", markdown)
	}
}

func TestCalendarMetricsRespectProjectFilter(t *testing.T) {
	withDataRoot(t)
	withAgentProjects(t, "A", "B")
	root := t.TempDir()
	span := []activityInterval{{From: "2026-11-19T10:00:00+08:00", To: "2026-11-19T11:00:00+08:00"}}
	items := []entry{
		{EntryID: "a", Date: "2026-11-19", Source: "codex", SessionID: "a", Project: "A", ProjectID: "agent-" + shortHash("A"), MinutesSource: "estimated", ActivityIntervals: span},
		{EntryID: "b", Date: "2026-11-19", Source: "codex", SessionID: "b", Project: "B", ProjectID: "agent-" + shortHash("B"), MinutesSource: "estimated", ActivityIntervals: span},
	}
	if err := writeDay(root, dayFile{Date: "2026-11-19", Entries: items}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		project string
		want    int
	}{{"", 120}, {"A", 60}} {
		result, err := archiveCalendar(input{LogRoot: root, Year: 2026, Month: 11, Project: tc.project})
		if err != nil {
			t.Fatal(err)
		}
		got := result.(map[string]any)["activity_metrics"].(activityMetrics)
		if got.CoverageMinutes != 60 || got.SessionMinutes != tc.want {
			t.Fatalf("filter %q: %+v", tc.project, got)
		}
	}
}

func TestTimelineMarksMissingEvidenceAsUnknown(t *testing.T) {
	rows := []entry{{Title: "消息缺少时间", Provider: "agent", MinutesSource: "estimated"}}
	if text := timelineLines(rows); !strings.Contains(text, "活动时长：无法估算") {
		t.Fatalf("missing evidence shown ambiguously: %s", text)
	}
	rows[0].MinutesSource = "manual"
	if text := timelineLines(rows); strings.Contains(text, "活动时长：无法估算") {
		t.Fatalf("manual correction marked as unknown estimate: %s", text)
	}
}

func TestExecutionMetricsPrimarySumParallelSessionsAndDeduplicateWithinSession(t *testing.T) {
	spans := []activityInterval{{From: "2026-11-19T10:00:00+08:00", To: "2026-11-19T11:00:00+08:00"}, {From: "2026-11-19T10:30:00+08:00", To: "2026-11-19T11:30:00+08:00"}}
	items := []entry{
		{Source: "codex", SessionID: "a", Date: "2026-11-19", ExecutionIntervals: spans},
		{Source: "codex", SessionID: "a", Date: "2026-11-19", ExecutionIntervals: spans},
		{Source: "codex", SessionID: "b", Date: "2026-11-19", ExecutionIntervals: []activityInterval{{From: "2026-11-19T10:00:00+08:00", To: "2026-11-19T11:00:00+08:00"}}},
	}
	got := calculateActivityMetrics(items)
	if got.ExecutionMinutes != 150 || got.ExecutionCoverageMinutes != 90 || got.ExecutionSessions != 2 {
		t.Fatalf("execution totals lost parallel sessions or counted a retry twice: %+v", got)
	}
}
