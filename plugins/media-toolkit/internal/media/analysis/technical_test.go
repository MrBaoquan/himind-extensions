package analysis

import (
	"strings"
	"testing"

	"himind-plugin/media-toolkit/internal/media/probe"
)

func TestTechnicalFromInfoNormalizesVideoAndAudio(t *testing.T) {
	info := probe.Info{
		Path: `C:\work\demo.mp4`, DurationMs: 12000, SizeBytes: 1234, BitRate: 2000000,
		FormatName: "mov,mp4,m4a,3gp,3g2,mj2",
		Video:      &probe.Stream{CodecType: "video", CodecName: "h264", Profile: "High", Width: 1920, Height: 1080, Fps: 29.97, BitRate: 1800000, PixelFormat: "yuv420p"},
		Audio:      &probe.Stream{CodecType: "audio", CodecName: "aac", SampleRate: 48000, Channels: 2, BitRate: 192000},
		Streams: []probe.Stream{
			{CodecType: "video", CodecName: "h264", Width: 1920, Height: 1080, BitRate: 1800000, PixelFormat: "yuv420p"},
			{CodecType: "audio", CodecName: "aac", SampleRate: 48000, Channels: 2, BitRate: 192000},
		},
		HasVideo: true, HasAudio: true,
	}
	report := TechnicalFromInfo(info)
	if report.Container != "mp4" || report.MediaType != "video" || report.DurationMs != 12000 {
		t.Fatalf("顶层报告不归一: %+v", report)
	}
	if report.Video == nil || report.Video.Codec != "h264" || report.Video.Width != 1920 || report.Video.PixelFormat != "yuv420p" {
		t.Fatalf("视频参数不归一: %+v", report.Video)
	}
	if report.Audio == nil || report.Audio.SampleRate != 48000 || report.Audio.BitRate != 192000 {
		t.Fatalf("音频参数不归一: %+v", report.Audio)
	}
}

func TestTechnicalFromInfoWarnsAboutMissingAudioAndFPS(t *testing.T) {
	report := TechnicalFromInfo(probe.Info{
		Path: "clip.webm", FormatName: "matroska,webm",
		Video:    &probe.Stream{CodecType: "video", CodecName: "vp9", Width: 640, Height: 360},
		Streams:  []probe.Stream{{CodecType: "video", CodecName: "vp9", Width: 640, Height: 360}},
		HasVideo: true,
	})
	joined := strings.Join(report.Warnings, "|")
	if !strings.Contains(joined, "帧率") || !strings.Contains(joined, "音频") {
		t.Fatalf("缺少可操作告警: %#v", report.Warnings)
	}
}

func TestTechnicalFromInfoNormalizesStillImage(t *testing.T) {
	report := TechnicalFromInfo(probe.Info{
		Path: `C:\work\poster.webp`, FormatName: "webp_pipe",
		Video:    &probe.Stream{CodecType: "video", CodecName: "webp", Width: 320, Height: 180},
		Streams:  []probe.Stream{{CodecType: "video", CodecName: "webp", Width: 320, Height: 180}},
		HasVideo: true,
		Warnings: []string{"时长不可读，进度百分比将不可用"},
	})
	if report.MediaType != "image" || report.Container != "webp" {
		t.Fatalf("静态图片归一化错误: %+v", report)
	}
	joined := strings.Join(report.Warnings, "|")
	if strings.Contains(joined, "进度百分比") || !strings.Contains(joined, "静态图片") {
		t.Fatalf("静态图片警告不准确: %#v", report.Warnings)
	}
}
