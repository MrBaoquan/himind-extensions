//go:build windows

package filepicker

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

const (
	ofnNoChangeDir   = 0x00000008
	ofnAllowMulti    = 0x00000200
	ofnFileMustExist = 0x00001000
)

var (
	comdlg32             = syscall.NewLazyDLL("comdlg32.dll")
	ole32                = syscall.NewLazyDLL("ole32.dll")
	getOpenFileNameW     = comdlg32.NewProc("GetOpenFileNameW")
	commDlgExtendedError = comdlg32.NewProc("CommDlgExtendedError")
	coInitializeEx       = ole32.NewProc("CoInitializeEx")
	coUninitialize       = ole32.NewProc("CoUninitialize")
)

type openFileNameW struct {
	StructSize      uint32
	Owner           uintptr
	Instance        uintptr
	Filter          *uint16
	CustomFilter    *uint16
	MaxCustomFilter uint32
	FilterIndex     uint32
	File            *uint16
	MaxFile         uint32
	FileTitle       *uint16
	MaxFileTitle    uint32
	InitialDir      *uint16
	Title           *uint16
	Flags           uint32
	FileOffset      uint16
	FileExtension   uint16
	DefaultExt      *uint16
	CustomData      uintptr
	Hook            uintptr
	TemplateName    *uint16
	Reserved        unsafe.Pointer
	ReservedUint    uint32
	FlagsEx         uint32
}

func pickPaths(ctx context.Context, request dialogRequest) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Common dialogs are most reliable on one dedicated STA thread. The JSON-RPC
	// process has no GUI thread of its own, and calling from an arbitrary Go
	// goroutine can fail with CDERR_DIALOGFAILURE (0xffff).
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := coInitializeEx.Call(0, 0x2) // COINIT_APARTMENTTHREADED
	if hr == 0 || hr == 1 {
		defer coUninitialize.Call()
	}
	buffer := make([]uint16, 32768)
	filter, err := buildFilter(request.FilterLabel, request.Extensions)
	if err != nil {
		return nil, err
	}
	title := utf16FromString(request.Title)
	initialDir := utf16FromString(request.InitialDir)
	flags := uint32(ofnFileMustExist | ofnNoChangeDir)
	if request.Multiple {
		flags |= ofnAllowMulti
	}
	ofn := openFileNameW{
		StructSize:  uint32(unsafe.Sizeof(openFileNameW{})),
		Owner:       0,
		Filter:      &filter[0],
		FilterIndex: 1,
		File:        &buffer[0],
		MaxFile:     uint32(len(buffer)),
		InitialDir:  &initialDir[0],
		Title:       &title[0],
		Flags:       flags,
	}
	ok, _, _ := getOpenFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
	if ok == 0 {
		code, _, _ := commDlgExtendedError.Call()
		if code == 0 {
			return nil, nil
		}
		return nil, fmt.Errorf("文件选择器失败，错误码 0x%x", uint32(code))
	}
	paths := parseSelection(buffer)
	if len(paths) == 0 {
		return nil, errors.New("文件选择器没有返回路径")
	}
	return paths, nil
}

func buildFilter(label string, extensions []string) ([]uint16, error) {
	if len(extensions) == 0 {
		return nil, errors.New("文件选择器缺少扩展名过滤")
	}
	patterns := make([]string, 0, len(extensions))
	for _, ext := range extensions {
		ext = strings.TrimSpace(ext)
		if ext == "" {
			continue
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		patterns = append(patterns, "*"+ext)
	}
	if len(patterns) == 0 {
		return nil, errors.New("文件选择器缺少扩展名过滤")
	}
	filterText := label + "\x00" + strings.Join(patterns, ";") + ";\x00\x00"
	return utf16.Encode([]rune(filterText)), nil
}

func parseSelection(buffer []uint16) []string {
	parts := make([]string, 0, 8)
	start := 0
	for index, value := range buffer {
		if value != 0 {
			continue
		}
		if index == start {
			break
		}
		parts = append(parts, syscall.UTF16ToString(buffer[start:index]))
		start = index + 1
	}
	if len(parts) == 0 {
		return nil
	}
	if len(parts) == 1 {
		return []string{parts[0]}
	}
	base := parts[0]
	paths := make([]string, 0, len(parts)-1)
	for _, name := range parts[1:] {
		paths = append(paths, filepath.Join(base, name))
	}
	return paths
}

func utf16FromString(value string) []uint16 {
	encoded := utf16.Encode([]rune(value))
	return append(encoded, 0)
}
