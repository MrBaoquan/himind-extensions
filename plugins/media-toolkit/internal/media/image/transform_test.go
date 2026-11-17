package image

import (
	"context"
	"himind-plugin/media-toolkit/internal/media/probe"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImageTransformOrderAndBounds(t *testing.T) {
	o := Options{Crop: &Crop{X: 10, Y: 20, Width: 101, Height: 55}, Rotate: 90, FlipH: true, Width: 99}
	if e := ValidateTransform(o, 320, 180); e != nil {
		t.Fatal(e)
	}
	f := filtersFor(o, "0xFFFFFF")
	if f != "crop=101:55:10:20:exact=1,transpose=1,hflip,scale=99:-1" {
		t.Fatal(f)
	}
	o.Crop.X = 300
	if ValidateTransform(o, 320, 180) == nil {
		t.Fatal("out of bounds crop accepted")
	}
	o.Crop = nil
	o.Rotate = 45
	if ValidateTransform(o, 320, 180) == nil {
		t.Fatal("arbitrary rotation accepted")
	}
}
func TestIntegrationCropRotateOddPixelsAndPreserveSource(t *testing.T) {
	tools := requireImageTools(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "source.png")
	makeImage(t, tools, src, "red")
	before, e := os.ReadFile(src)
	if e != nil {
		t.Fatal(e)
	}
	r, e := ConvertBatch(context.Background(), tools, []string{src}, filepath.Join(dir, "out"), Options{Format: "png", Crop: &Crop{X: 10, Y: 20, Width: 101, Height: 55}, Rotate: 90, FlipV: true, Quality: 100})
	if e != nil || !r.OK || len(r.Outputs) != 1 {
		t.Fatalf("%+v %v", r, e)
	}
	metadata, e := probe.Read(context.Background(), tools, r.Outputs[0].Output)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(src)
	if e != nil || string(raw) != string(before) {
		t.Fatal("source modified")
	}
	if metadata.Video == nil || metadata.Video.Width != 55 || metadata.Video.Height != 101 {
		t.Fatalf("crop/rotation dimensions wrong: %+v", metadata.Video)
	}
	if !strings.HasSuffix(r.Outputs[0].Output, ".png") {
		t.Fatal(r)
	}
}
