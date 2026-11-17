package mediadownload

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestEngagementKeepsZeroAndDiscardsMissingOrInvalidCounters(t *testing.T) {
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	stats := engagementFromDouyinDetail([]byte(`{"statistics":{"digg_count":123456,"collect_count":0,"comment_count":null,"share_count":-1},"cookie":"secret","video":{"url":"signed-secret"}}`), now)
	if stats == nil || stats.Likes == nil || *stats.Likes != 123456 || stats.Favorites == nil || *stats.Favorites != 0 || stats.Comments != nil || stats.Shares != nil || stats.ObservedAt != "2026-11-14T10:00:00Z" {
		t.Fatalf("wrong observation: %+v", stats)
	}
	encoded, _ := json.Marshal(stats)
	if strings.Contains(string(encoded), "secret") || !strings.Contains(string(encoded), `"favorite_count":0`) || strings.Contains(string(encoded), "comment_count") {
		t.Fatalf("unsafe or misleading projection: %s", encoded)
	}
	for _, raw := range []string{`{}`, `{"statistics":{}}`, `{"statistics":{"digg_count":"1.2万","comment_count":1.5,"collect_count":9007199254740992}}`, `null`, `invalid`} {
		if got := engagementFromDouyinDetail([]byte(raw), now); got != nil {
			t.Fatalf("unknown counters became data: %s -> %+v", raw, got)
		}
	}
}
