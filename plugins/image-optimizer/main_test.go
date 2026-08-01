package main

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestOptimizeImageCreatesSeparateResizedFile(t *testing.T) {
	directory := t.TempDir()
	inputPath := filepath.Join(directory, "source.png")
	outputPath := filepath.Join(directory, "optimized.jpg")
	file, err := os.Create(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	value := image.NewRGBA(image.Rect(0, 0, 640, 320))
	for y := 0; y < 320; y++ {
		for x := 0; x < 640; x++ {
			value.Set(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: 120, A: 255})
		}
	}
	if err := png.Encode(file, value); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := optimize(input{InputPath: inputPath, OutputPath: outputPath, Quality: 75, MaxDimension: 320})
	if err != nil {
		t.Fatal(err)
	}
	if result.Width != 320 || result.Height != 160 || result.OutputBytes == 0 {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestOptimizeImageRejectsOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image.png")
	if _, err := optimize(input{InputPath: path, OutputPath: path}); err == nil {
		t.Fatal("expected overwrite rejection")
	}
}
