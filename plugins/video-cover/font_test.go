package main

import (
	"os"
	"testing"

	"golang.org/x/image/font/opentype"
)

func TestBundledFontParses(t *testing.T) {
	data, err := os.ReadFile("assets/fonts/NotoSerifSC-VF.ttf")
	if err != nil {
		t.Fatal(err)
	}
	f, err := opentype.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: 108, DPI: 72})
	if err != nil {
		t.Fatal(err)
	}
	_ = face.Metrics()
	face.Close()
}
