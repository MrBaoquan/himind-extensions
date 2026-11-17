package main

import (
	"encoding/json"
	"testing"

	"github.com/MrBaoquan/himind-extensions/sdk/jsonrpc"
)

func TestRepeatedBookmarkPreservesUserOrganizationAndSupportsExplicitClear(t *testing.T) {
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	save := func(params string) remoteResource {
		t.Helper()
		out, rpcErr := handle(jsonrpc.Request{Method: "media-library.bookmark.save", Params: json.RawMessage(params)})
		if rpcErr != nil {
			t.Fatal(rpcErr)
		}
		return out.(remoteResource)
	}
	first := save(`{"source_url":"https://www.douyin.com/video/123456","title":"初次收藏","note":"用于片头","tags":["航拍"]}`)
	again := save(`{"source_url":"https://www.douyin.com/?modal_id=123456","title":"新标题"}`)
	if again.ID != first.ID || again.Note != "用于片头" || len(again.Tags) != 1 || again.Tags[0] != "航拍" || again.SavedAt != first.SavedAt {
		t.Fatalf("repeated search result erased organization: %+v", again)
	}
	cleared := save(`{"source_url":"https://www.douyin.com/video/123456","note":"","tags":[]}`)
	if cleared.Note != "" || len(cleared.Tags) != 0 {
		t.Fatalf("explicit editor clear ignored: %+v", cleared)
	}
	listed, err := listBookmarks("")
	if err != nil {
		t.Fatal(err)
	}
	items := listed.(map[string]any)["items"].([]remoteResource)
	if len(items) != 1 || items[0].Note != "" || len(items[0].Tags) != 0 {
		t.Fatalf("reopened state differs: %+v", items)
	}
}
