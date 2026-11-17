package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestCommentsProjectionBoundsDeduplicatesAndPreservesUnknownCounts(t *testing.T) {
	raw := []rawComment{{Key: "one", Author: "公开昵称", Text: "<script>正文</script>", Likes: "1.2万", AvatarURL: "https://untrusted.test/?secret=x"}, {Key: "one", Author: "重复", Text: "重复"}, {Key: "reply", ParentKey: "one", Author: "作者", Text: "回复", Likes: "0", IsAuthor: true}, {Key: "orphan", ParentKey: "missing", Text: "孤立回复"}}
	items := cleanComments(raw)
	if len(items) != 1 || len(items[0].Replies) != 1 || *items[0].Likes != 12000 || *items[0].Replies[0].Likes != 0 || items[0].Avatar != "" || !items[0].Replies[0].IsAuthor {
		t.Fatalf("bad projection: %+v", items)
	}
	if items[0].Replies[0].ParentID != items[0].ID {
		t.Fatal("reply lost parent")
	}
	if parseCommentLikes("") != nil || parseCommentLikes("点赞") != nil || parseCommentLikes("-2") != nil {
		t.Fatal("unknown counts invented")
	}
	for i := 0; i < 100; i++ {
		raw = append(raw, rawComment{Key: fmt.Sprint(i), Author: strings.Repeat("字", 200), Text: strings.Repeat("文", 3000)})
	}
	items = cleanComments(raw)
	if len(items) != 60 || len([]rune(items[1].Author)) > 120 || len([]rune(items[1].Text)) > 2000 {
		t.Fatal("unbounded metadata")
	}
	encoded, _ := json.Marshal(items)
	if strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), "avatar_url") {
		t.Fatal("raw URL leaked")
	}
}

func TestCommentPagingAndSourceIsolation(t *testing.T) {
	for _, raw := range []string{"http://127.0.0.1/private", "https://evil.test/video/7624170206072269129", "https://www.youtube.com/watch?v=abcdefghijk"} {
		if _, _, err := commentReference(commentInput{URL: raw}); err == nil {
			t.Fatal("untrusted/non-Douyin URL accepted", raw)
		}
	}
	source, id, err := commentReference(commentInput{URL: "https://www.douyin.com/?modal_id=7624170206072269129"})
	if err != nil || id != "7624170206072269129" || source != "https://www.douyin.com/video/7624170206072269129" {
		t.Fatal(source, id, err)
	}
	sample := commentSample{SampleID: strings.Repeat("a", 32), Items: make([]publicComment, 23)}
	first, err := pageComments(sample, 0)
	if err != nil || len(first.Items) != 10 || first.NextCursor == "" {
		t.Fatal("first page", first, err)
	}
	last, err := pageComments(sample, 20)
	if err != nil || len(last.Items) != 3 || last.NextCursor != "" {
		t.Fatal("last page", last, err)
	}
	for _, offset := range []int{-1, 1, 23, 30} {
		if _, err := pageComments(sample, offset); err == nil {
			t.Fatal("invalid offset", offset)
		}
	}
}

func TestSavedCommentsReadOfflineAndCannotBeInjected(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	source := "https://www.douyin.com/video/7624170206072269129"
	db, err := openLibraryDB()
	if err != nil {
		t.Fatal(err)
	}
	if err = ensureCommentSchema(db); err != nil {
		t.Fatal(err)
	}
	sample := commentSample{SourceURL: source, ObservedAt: "2026-11-15T00:00:00Z", Scope: commentScope, Items: []publicComment{{ID: "stored", Author: "测试昵称", Text: "保存的评论"}}}
	payload, _ := json.Marshal(sample)
	if _, err = db.Exec(`INSERT INTO comment_snapshots VALUES(?,?,?,?)`, source, "hash", "2026-11-15T00:00:00Z", string(payload)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	result, err := commentSnapshot(commentInput{URL: source}, false)
	if err != nil {
		t.Fatal("offline snapshot required login", err)
	}
	if len(result.(map[string]any)["snapshot"].(commentSample).Items) != 1 {
		t.Fatal("lost snapshot")
	}
	missing, err := commentSnapshot(commentInput{URL: "https://www.douyin.com/video/7702136163577924874"}, false)
	if err != nil || missing.(map[string]any)["snapshot"] != nil {
		t.Fatal("cross-source leak", err)
	}
	for _, token := range []string{"../../escape", strings.Repeat("b", 32)} {
		if _, err := commentSnapshot(commentInput{URL: source, SampleID: token}, true); err == nil {
			t.Fatal("untrusted/absent sample saved")
		}
	}
}
