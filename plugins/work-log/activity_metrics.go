package main

import (
	"fmt"
	"sort"
	"time"
)

// activityMetrics describes estimates from recorded message intervals. Manual
// minutes and older entries without interval evidence are deliberately separate.
type activityMetrics struct {
	CoverageMinutes          int `json:"coverage_minutes"`
	SessionMinutes           int `json:"session_minutes"`
	UnknownSessions          int `json:"unknown_sessions"`
	ManualMinutes            int `json:"manual_minutes"`
	ExecutionMinutes         int `json:"execution_minutes"`
	ExecutionCoverageMinutes int `json:"execution_coverage_minutes"`
	ExecutionSessions        int `json:"execution_sessions"`
}

type activitySpan struct{ from, to time.Time }

func intervalSpans(intervals []activityInterval) []activitySpan {
	spans := make([]activitySpan, 0, len(intervals))
	for _, interval := range intervals {
		start, errStart := time.Parse(time.RFC3339Nano, interval.From)
		end, errEnd := time.Parse(time.RFC3339Nano, interval.To)
		if errStart == nil && errEnd == nil && end.After(start) {
			spans = append(spans, activitySpan{start, end})
		}
	}
	return spans
}

// mergedActivityDuration computes a union, including touching spans. It works on
// a copy so callers can reuse evidence for both session and coverage totals.
func mergedActivityDuration(input []activitySpan) time.Duration {
	var total time.Duration
	for _, span := range mergedActivitySpans(input) {
		total += span.to.Sub(span.from)
	}
	return total
}

func compactActivityIntervals(input []activityInterval) []activityInterval {
	var result []activityInterval
	for _, span := range mergedActivitySpans(intervalSpans(input)) {
		result = append(result, activityInterval{span.from.Format(time.RFC3339Nano), span.to.Format(time.RFC3339Nano)})
	}
	return result
}

func mergedActivitySpans(input []activitySpan) []activitySpan {
	if len(input) == 0 {
		return nil
	}
	spans := append([]activitySpan(nil), input...)
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].from.Equal(spans[j].from) {
			return spans[i].to.Before(spans[j].to)
		}
		return spans[i].from.Before(spans[j].from)
	})
	start, end := spans[0].from, spans[0].to
	var merged []activitySpan
	for _, span := range spans[1:] {
		if span.from.After(end) {
			merged = append(merged, activitySpan{start, end})
			start, end = span.from, span.to
		} else if span.to.After(end) {
			end = span.to
		}
	}
	return append(merged, activitySpan{start, end})
}

// calculateActivityMetrics deduplicates repeated or split evidence within the
// same source/session/day while preserving overlap between distinct sessions.
func calculateActivityMetrics(items []entry) activityMetrics {
	bySession := make(map[string][]activitySpan)
	execution := make(map[string][]activitySpan)
	var result activityMetrics
	for i, item := range items {
		key := ""
		if item.Source != "" && item.SessionID != "" {
			key = item.Source + "\x00" + item.SessionID + "\x00" + item.Date
		} else if item.EntryID != "" {
			key = "entry\x00" + item.EntryID
		} else {
			key = fmt.Sprintf("anonymous\x00%d", i)
		}
		if spans := intervalSpans(item.ExecutionIntervals); len(spans) > 0 {
			execution[key] = append(execution[key], spans...)
		}
		if item.MinutesSource == "manual" {
			result.ManualMinutes += entryMinutes(item)
			continue
		}
		bySession[key] = append(bySession[key], intervalSpans(item.ActivityIntervals)...)
	}
	var coverage []activitySpan
	var sessionDuration time.Duration
	for key, spans := range bySession {
		if len(spans) == 0 {
			if len(execution[key]) == 0 {
				result.UnknownSessions++
			}
			continue
		}
		sessionDuration += mergedActivityDuration(spans)
		coverage = append(coverage, spans...)
	}
	result.CoverageMinutes = int(mergedActivityDuration(coverage) / time.Minute)
	// Round only after aggregation, just as coverage does. Rounding each
	// session first loses short activities and can make coverage exceed total.
	result.SessionMinutes = int(sessionDuration / time.Minute)
	coverage = nil
	var executionDuration time.Duration
	for _, spans := range execution {
		merged := mergedActivitySpans(spans)
		for _, span := range merged {
			executionDuration += span.to.Sub(span.from)
		}
		coverage = append(coverage, merged...)
	}
	result.ExecutionMinutes = int(executionDuration / time.Minute)
	result.ExecutionCoverageMinutes = int(mergedActivityDuration(coverage) / time.Minute)
	result.ExecutionSessions = len(execution)
	return result
}
