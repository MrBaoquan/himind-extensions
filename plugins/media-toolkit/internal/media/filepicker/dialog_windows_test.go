//go:build windows

package filepicker

import (
	"testing"
	"unicode/utf16"
)

func TestParseSelectionSinglePath(t *testing.T) {
	paths := parseSelection(utf16WithTerminator(`C:\media\photo.png`))
	if len(paths) != 1 || paths[0] != `C:\media\photo.png` {
		t.Fatalf("unexpected single selection: %#v", paths)
	}
}

func TestParseSelectionMultiplePaths(t *testing.T) {
	paths := parseSelection(utf16WithTerminator("C:\\media\\batch\x00one.png\x00two.png"))
	if len(paths) != 2 || paths[0] != `C:\media\batch\one.png` || paths[1] != `C:\media\batch\two.png` {
		t.Fatalf("unexpected multi selection: %#v", paths)
	}
}

func utf16WithTerminator(value string) []uint16 {
	encoded := utf16.Encode([]rune(value))
	return append(encoded, 0, 0)
}
