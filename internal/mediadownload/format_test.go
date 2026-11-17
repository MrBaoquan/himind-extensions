package mediadownload

import (
	"strings"
	"testing"
)

func TestExplicitFormatCannotSilentlyOverrideModeOrQuality(t *testing.T) {
	for _, input := range []JobStartInput{
		{FormatID: "137", Quality: "720p"},
		{FormatID: "137", AudioOnly: true},
		{FormatID: "137+140"},
		{FormatID: "best/137"},
		{FormatID: "137[height>720]"},
	} {
		if _, err := resolveFormat(input, true); err == nil {
			t.Fatalf("expected conflicting or expression format to fail: %+v", input)
		}
	}
}

func TestExplicitVideoFormatIncludesAudioOrFails(t *testing.T) {
	withMerge, err := resolveFormat(JobStartInput{FormatID: "137", Quality: "best"}, true)
	if err != nil || withMerge != "137[vcodec!=none][acodec!=none]/137[vcodec!=none]+bestaudio" {
		t.Fatalf("expected muxed or merged stream, got %q, %v", withMerge, err)
	}
	withoutMerge, err := resolveFormat(JobStartInput{FormatID: "18"}, false)
	if err != nil || withoutMerge != "18[vcodec!=none][acodec!=none]" {
		t.Fatalf("no-ffmpeg path must require both tracks: %q, %v", withoutMerge, err)
	}
}

func TestQualityLimitHasNoUnboundedFallbackWithoutFFmpeg(t *testing.T) {
	for _, quality := range []string{"1080p", "720p", "480p"} {
		selector, err := resolveFormat(JobStartInput{Quality: quality}, false)
		if err != nil || strings.Contains(selector, "/") || !strings.Contains(selector, "height<="+strings.TrimSuffix(quality, "p")) || !strings.Contains(selector, "acodec!=none") {
			t.Fatalf("quality or audio guarantee lost for %s: %q, %v", quality, selector, err)
		}
	}
}
