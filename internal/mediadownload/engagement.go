package mediadownload

import (
	"context"
	"encoding/json"
	"time"
)

// Optional counters distinguish an unavailable value from a measured zero.
// These public observations contain no account or media delivery information.
type Engagement struct {
	Likes      *int64 `json:"like_count,omitempty"`
	Favorites  *int64 `json:"favorite_count,omitempty"`
	Comments   *int64 `json:"comment_count,omitempty"`
	Shares     *int64 `json:"share_count,omitempty"`
	ObservedAt string `json:"observed_at,omitempty"`
}

func engagementFromDouyinDetail(data []byte, now time.Time) *Engagement {
	var detail struct {
		Statistics map[string]json.RawMessage `json:"statistics"`
	}
	if json.Unmarshal(data, &detail) != nil {
		return nil
	}
	count := func(key string) *int64 {
		raw := detail.Statistics[key]
		var n *int64
		if json.Unmarshal(raw, &n) != nil || n == nil || *n < 0 || *n > 9007199254740991 {
			return nil
		}
		return n
	}
	stats := &Engagement{Likes: count("digg_count"), Favorites: count("collect_count"), Comments: count("comment_count"), Shares: count("share_count")}
	if stats.Likes == nil && stats.Favorites == nil && stats.Comments == nil && stats.Shares == nil {
		return nil
	}
	stats.ObservedAt = now.UTC().Format(time.RFC3339)
	return stats
}

// ResolveEngagement reuses the acquisition adapter and connected session. It
// reads one official detail response and exposes only whitelisted counters.
func ResolveEngagement(ctx context.Context, rawURL string) (*Engagement, error) {
	detail, err := readConnectedDouyinDetail(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	defer clear(detail)
	return engagementFromDouyinDetail(detail, time.Now()), nil
}
