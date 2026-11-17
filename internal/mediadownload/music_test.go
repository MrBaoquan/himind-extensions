package mediadownload

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMusicIdentityPreservesLargeIDAndExcludesMediaURL(t *testing.T) {
	music := musicFromDouyinDetail([]byte(`{"music":{"id":7391234567890123456,"title":"曲目","author":"作者","play_url":{"uri":"secret-signed-url"}}}`))
	if music == nil || music.ProviderID != "7391234567890123456" {
		t.Fatalf("identity: %+v", music)
	}
	data, _ := json.Marshal(music)
	if strings.Contains(string(data), "secret") {
		t.Fatal("media URL escaped")
	}
}
func TestMusicIdentityRejectsMissingAndMalformedID(t *testing.T) {
	for _, data := range []string{`{}`, `{"music":{"id":"1e19"}}`, `{"music":{"id":"12345?token=x"}}`, `{"music":{"id":null}}`} {
		if musicFromDouyinDetail([]byte(data)) != nil {
			t.Fatal(data)
		}
	}
}
