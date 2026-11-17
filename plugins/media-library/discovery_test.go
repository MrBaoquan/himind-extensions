package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestDiscoverySnapshotCursorBindsPlatformQueryGenerationAndExpiry(t *testing.T) {
	now := time.Now()
	in := discoveryInput{Provider: "douyin", Query: "航拍"}
	snapshot := discoverySnapshot{Provider: in.Provider, Query: in.Query, Generation: "account-a", Expires: now.Add(time.Minute).Unix()}
	for n := 0; n < 30; n++ {
		snapshot.Items = append(snapshot.Items, remoteResource{ProviderID: fmt.Sprint(n)})
	}
	first, err := pageDiscovery(snapshot, in, "account-a", strings.Repeat("a", 32), 0, now)
	if err != nil || len(first.Items) != 12 || !strings.HasSuffix(first.NextCursor, ":12") {
		t.Fatalf("first page: %+v %v", first, err)
	}
	second, err := pageDiscovery(snapshot, in, "account-a", strings.Repeat("a", 32), 12, now)
	if err != nil || second.Items[0].ProviderID != "12" {
		t.Fatalf("next page: %+v %v", second, err)
	}
	for _, check := range []struct {
		input      discoveryInput
		generation string
		offset     int
		at         time.Time
	}{
		{in, "account-b", 12, now}, {discoveryInput{Provider: "youtube", Query: in.Query}, "account-a", 12, now},
		{discoveryInput{Provider: in.Provider, Query: "另一个词"}, "account-a", 12, now},
		{in, "account-a", 13, now}, {in, "account-a", 36, now}, {in, "account-a", 12, now.Add(time.Hour)},
	} {
		if _, err := pageDiscovery(snapshot, check.input, check.generation, "id", check.offset, check.at); err == nil {
			t.Fatalf("unbound cursor accepted: %+v", check)
		}
	}
}

func TestDiscoveryCleansExternalResultsBeforeCaching(t *testing.T) {
	items := cleanDiscoveryItems("douyin", []remoteResource{
		{SourceURL: "https://www.douyin.com/video/123456?utm_source=test", Title: "航拍\n作品", PreviewURL: "https://cdn.example.com/?signature=fixture-secret", Note: "untrusted", Tags: []string{"untrusted"}},
		{SourceURL: "https://www.douyin.com/?modal_id=123456"},
		{SourceURL: "https://www.youtube.com/watch?v=fixture"},
		{SourceURL: "https://evil.example/video/123456"},
	})
	if len(items) != 1 || items[0].SourceURL != "https://www.douyin.com/video/123456" || items[0].Title != "航拍 作品" || items[0].PreviewURL != "" || items[0].Note != "" || len(items[0].Tags) != 0 {
		t.Fatalf("unsafe/duplicate cache: %+v", items)
	}
	for _, input := range []discoveryInput{{Provider: "other", Query: "航拍"}, {Provider: "douyin", Query: "航拍", Cursor: "../../sessions"}, {Provider: "youtube", Query: "drone", RequestID: "../request"}} {
		if _, err := normalizeDiscoveryInput(input); err == nil {
			t.Fatalf("invalid input accepted: %+v", input)
		}
	}
}

func TestDiscoveryLeaseIsCrossConnectionAndPlatformScoped(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	db1, err := openLibraryDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db1.Close()
	db2, err := openLibraryDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	now := time.Now()
	if err = acquireDiscoveryLease(db1, "bilibili", "a", now); err != nil {
		t.Fatal(err)
	}
	if err = acquireDiscoveryLease(db2, "bilibili", "b", now); err == nil {
		t.Fatal("parallel helper admitted")
	}
	if err = acquireDiscoveryLease(db2, "youtube", "c", now); err != nil {
		t.Fatal("independent provider blocked", err)
	}
	if _, err = db1.Exec(`UPDATE discovery_leases SET until_at=0 WHERE provider='bilibili' AND owner='a'`); err != nil {
		t.Fatal(err)
	}
	if err = acquireDiscoveryLease(db2, "bilibili", "b", now); err == nil {
		t.Fatal("rate limit ignored after early completion")
	}
	if err = acquireDiscoveryLease(db2, "bilibili", "b", now.Add(4*time.Second)); err != nil {
		t.Fatal("released lease stuck", err)
	}
}
