package main

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/MrBaoquan/himind-extensions/sdk/jsonrpc"
)

type input struct {
	Path     string `json:"path"`
	MaxChars int    `json:"max_chars"`
}

type result struct {
	Path       string `json:"path"`
	Format     string `json:"format"`
	Bytes      int64  `json:"bytes"`
	Characters int    `json:"characters"`
	Words      int    `json:"words"`
	Truncated  bool   `json:"truncated"`
	Text       string `json:"text"`
}

func main() {
	_ = jsonrpc.Serve(os.Stdin, os.Stdout, func(request jsonrpc.Request) (any, *jsonrpc.Error) {
		if request.Method != "document.inspect" {
			return nil, jsonrpc.InvalidParams(fmt.Sprintf("unsupported method: %s", request.Method))
		}
		var in input
		if rpcError := jsonrpc.DecodeParams(request, &in); rpcError != nil {
			return nil, rpcError
		}
		value, err := inspect(in)
		if err != nil {
			return nil, jsonrpc.InternalError(err.Error())
		}
		return value, nil
	})
}

func inspect(in input) (result, error) {
	path, err := filepath.Abs(strings.TrimSpace(in.Path))
	if err != nil || strings.TrimSpace(in.Path) == "" {
		return result{}, errors.New("path is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return result{}, err
	}
	if !info.Mode().IsRegular() {
		return result{}, errors.New("path must be a regular file")
	}
	if info.Size() > 50*1024*1024 {
		return result{}, errors.New("document exceeds 50 MiB limit")
	}
	format := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	var text string
	switch format {
	case "txt", "md", "markdown":
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return result{}, readErr
		}
		if !utf8.Valid(content) {
			return result{}, errors.New("text document must use UTF-8")
		}
		text = string(content)
	case "docx":
		text, err = readDocx(path)
		if err != nil {
			return result{}, err
		}
	default:
		return result{}, errors.New("supported formats: docx, txt, md, markdown")
	}
	text = strings.TrimSpace(normalizeText(text))
	characters := utf8.RuneCountInString(text)
	maxChars := in.MaxChars
	if maxChars == 0 {
		maxChars = 20000
	}
	if maxChars < 100 || maxChars > 200000 {
		return result{}, errors.New("max_chars must be between 100 and 200000")
	}
	truncated := characters > maxChars
	if truncated {
		text = string([]rune(text)[:maxChars])
	}
	return result{Path: path, Format: format, Bytes: info.Size(), Characters: characters, Words: len(strings.Fields(text)), Truncated: truncated, Text: text}, nil
}

func readDocx(path string) (string, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return "", errors.New("invalid DOCX archive")
	}
	defer archive.Close()
	for _, file := range archive.File {
		if file.Name != "word/document.xml" {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			return "", err
		}
		defer reader.Close()
		decoder := xml.NewDecoder(io.LimitReader(reader, 20*1024*1024))
		var output strings.Builder
		for {
			token, tokenErr := decoder.Token()
			if tokenErr == io.EOF {
				break
			}
			if tokenErr != nil {
				return "", errors.New("invalid DOCX document XML")
			}
			switch value := token.(type) {
			case xml.CharData:
				output.Write([]byte(value))
			case xml.EndElement:
				if value.Name.Local == "p" || value.Name.Local == "tr" {
					output.WriteByte('\n')
				}
			}
		}
		return output.String(), nil
	}
	return "", errors.New("DOCX is missing word/document.xml")
}

func normalizeText(value string) string {
	lines := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			result = append(result, line)
		}
	}
	return strings.Join(result, "\n")
}
