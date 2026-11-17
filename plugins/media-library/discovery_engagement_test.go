package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MrBaoquan/himind-extensions/internal/mediadownload"
)

func TestDiscoveryEngagementBoundsParallelismAndCachesPartialFailure(t *testing.T) {
	items := make([]remoteResource, discoveryPageSize)
	for i := range items {
		items[i] = remoteResource{Provider: "douyin", SourceURL: "https://www.douyin.com/video/123"}
	}
	items[0].Provider = "youtube"
	items[1].Engagement = &mediadownload.Engagement{}
	var calls, active, peak atomic.Int32
	resolve := func(context.Context, string) (*mediadownload.Engagement, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
		calls.Add(1)
		time.Sleep(time.Millisecond)
		return nil, errors.New("unavailable")
	}
	enrichDiscoveryEngagement(context.Background(), items, resolve)
	if calls.Load() != 10 || peak.Load() > 3 || items[0].Engagement != nil {
		t.Fatalf("unbounded/wrong providers: calls=%d peak=%d", calls.Load(), peak.Load())
	}
	enrichDiscoveryEngagement(context.Background(), items, resolve)
	if calls.Load() != 10 {
		t.Fatal("failed observations retried inside the same snapshot")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	enrichDiscoveryEngagement(ctx, []remoteResource{{Provider: "douyin"}}, resolve)
	if calls.Load() != 10 {
		t.Fatal("canceled search performed more network reads")
	}
}
