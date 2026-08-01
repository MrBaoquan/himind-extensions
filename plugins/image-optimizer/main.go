package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
)

type request struct {
	ID     any             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}
type input struct {
	InputPath    string `json:"input_path"`
	OutputPath   string `json:"output_path"`
	Quality      int    `json:"quality"`
	MaxDimension int    `json:"max_dimension"`
}
type result struct {
	InputPath   string `json:"input_path"`
	OutputPath  string `json:"output_path"`
	InputBytes  int64  `json:"input_bytes"`
	OutputBytes int64  `json:"output_bytes"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	SavedBytes  int64  `json:"saved_bytes"`
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return
	}
	var req request
	if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
		respond(nil, nil, err)
		return
	}
	if req.Method != "image.optimize" {
		respond(req.ID, nil, fmt.Errorf("unsupported method: %s", req.Method))
		return
	}
	var in input
	if err := json.Unmarshal(req.Params, &in); err != nil {
		respond(req.ID, nil, err)
		return
	}
	value, err := optimize(in)
	respond(req.ID, value, err)
}

func optimize(in input) (result, error) {
	inputPath, err := filepath.Abs(strings.TrimSpace(in.InputPath))
	if err != nil || strings.TrimSpace(in.InputPath) == "" {
		return result{}, errors.New("input_path is required")
	}
	outputPath, err := filepath.Abs(strings.TrimSpace(in.OutputPath))
	if err != nil || strings.TrimSpace(in.OutputPath) == "" {
		return result{}, errors.New("output_path is required")
	}
	if strings.EqualFold(inputPath, outputPath) {
		return result{}, errors.New("output_path must differ from input_path")
	}
	info, err := os.Stat(inputPath)
	if err != nil {
		return result{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > 100*1024*1024 {
		return result{}, errors.New("input must be an image file no larger than 100 MiB")
	}
	source, err := os.Open(inputPath)
	if err != nil {
		return result{}, err
	}
	imageValue, format, err := image.Decode(source)
	_ = source.Close()
	if err != nil || (format != "jpeg" && format != "png") {
		return result{}, errors.New("supported input formats: JPEG and PNG")
	}
	bounds := imageValue.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if in.MaxDimension != 0 {
		if in.MaxDimension < 320 || in.MaxDimension > 8192 {
			return result{}, errors.New("max_dimension must be between 320 and 8192")
		}
		if width > in.MaxDimension || height > in.MaxDimension {
			newWidth, newHeight := scaledSize(width, height, in.MaxDimension)
			imageValue = resizeNearest(imageValue, newWidth, newHeight)
			width, height = newWidth, newHeight
		}
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return result{}, err
	}
	output, err := os.Create(outputPath)
	if err != nil {
		return result{}, err
	}
	ext := strings.ToLower(filepath.Ext(outputPath))
	switch ext {
	case ".jpg", ".jpeg":
		quality := in.Quality
		if quality == 0 {
			quality = 82
		}
		if quality < 40 || quality > 95 {
			_ = output.Close()
			_ = os.Remove(outputPath)
			return result{}, errors.New("quality must be between 40 and 95")
		}
		err = jpeg.Encode(output, imageValue, &jpeg.Options{Quality: quality})
	case ".png":
		encoder := png.Encoder{CompressionLevel: png.BestCompression}
		err = encoder.Encode(output, imageValue)
	default:
		err = errors.New("output_path must end with .jpg, .jpeg or .png")
	}
	closeErr := output.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(outputPath)
		return result{}, err
	}
	outputInfo, err := os.Stat(outputPath)
	if err != nil {
		return result{}, err
	}
	return result{InputPath: inputPath, OutputPath: outputPath, InputBytes: info.Size(), OutputBytes: outputInfo.Size(), Width: width, Height: height, SavedBytes: info.Size() - outputInfo.Size()}, nil
}

func scaledSize(width, height, maximum int) (int, int) {
	if width >= height {
		return maximum, max(1, height*maximum/width)
	}
	return max(1, width*maximum/height), maximum
}

func resizeNearest(source image.Image, width, height int) image.Image {
	bounds := source.Bounds()
	result := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			sourceX := bounds.Min.X + x*bounds.Dx()/width
			sourceY := bounds.Min.Y + y*bounds.Dy()/height
			result.Set(x, y, source.At(sourceX, sourceY))
		}
	}
	return result
}

func respond(id, value any, err error) {
	response := map[string]any{"jsonrpc": "2.0", "id": id}
	if err != nil {
		response["error"] = map[string]any{"code": -32000, "message": err.Error()}
	} else {
		response["result"] = value
	}
	_ = json.NewEncoder(os.Stdout).Encode(response)
}
