package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/MrBaoquan/himind-extensions/internal/mediadownload"
)

// Only enrich the requested page, at most three simultaneous detail reads.
// An empty observation caches a failed attempt for this ten-minute snapshot.
func enrichDiscoveryEngagement(ctx context.Context, items []remoteResource, resolve func(context.Context, string) (*mediadownload.Engagement, error)) {
	var wg sync.WaitGroup
	slots := make(chan struct{}, 3)
	for i := range items {
		if items[i].Provider != "douyin" || items[i].Engagement != nil {
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			items[i].Engagement = &mediadownload.Engagement{}
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-slots }()
			if ctx.Err() != nil {
				return
			}
			stats, err := resolve(ctx, items[i].SourceURL)
			if err == nil && stats != nil {
				items[i].Engagement = stats
			}
		}(i)
	}
	wg.Wait()
}

func completeDiscoveryPage(snapshot discoverySnapshot, in discoveryInput, generation, root, id string, offset int) (discoveryPage, error) {
	page, err := pageDiscovery(snapshot, in, generation, id, offset, time.Now())
	if err != nil || in.Provider != "douyin" {
		return page, err
	}
	canceled := func() bool {
		if in.RequestID == "" {
			return false
		}
		_, err := os.Stat(filepath.Join(root, "discovery-cancel", in.RequestID))
		return err == nil
	}
	if canceled() {
		return discoveryPage{}, fmt.Errorf("搜索已取消，可以重新搜索")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	resolve := func(ctx context.Context, url string) (*mediadownload.Engagement, error) {
		if canceled() {
			cancel()
			return nil, context.Canceled
		}
		return mediadownload.ResolveEngagement(ctx, url)
	}
	end := min(offset+discoveryPageSize, len(snapshot.Items))
	enrichDiscoveryEngagement(ctx, snapshot.Items[offset:end], resolve)
	if canceled() {
		return discoveryPage{}, fmt.Errorf("搜索已取消，可以重新搜索")
	}
	current, err := discoveryGeneration(in.Provider)
	if err != nil || current != generation {
		return discoveryPage{}, fmt.Errorf("平台连接已变化，请重新搜索")
	}
	// Persist only projected public counters, never the official detail body.
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return discoveryPage{}, err
	}
	if err = os.WriteFile(filepath.Join(root, "discovery-cache", id+".json"), encoded, 0600); err != nil {
		return discoveryPage{}, err
	}
	return pageDiscovery(snapshot, in, generation, id, offset, time.Now())
}
