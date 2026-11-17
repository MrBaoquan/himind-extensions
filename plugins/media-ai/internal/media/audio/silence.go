package audio

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

// DigitalSilence checks the PCM16 WAV produced by Extract. Only all-zero
// samples qualify; quiet speech, noise and music still go to recognition.
func DigitalSilence(ctx context.Context, path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return false, err
	}
	var header [12]byte
	if _, err = io.ReadFull(file, header[:]); err != nil {
		return false, fmt.Errorf("音频WAV头不完整: %w", err)
	}
	limit := int64(binary.LittleEndian.Uint32(header[4:8])) + 8
	if string(header[:4]) != "RIFF" || string(header[8:]) != "WAVE" || limit > info.Size() || limit < 12 {
		return false, fmt.Errorf("音频WAV结构无效")
	}
	formatted, found, nonzero := false, false, false
	buffer := make([]byte, 64*1024)
	for offset := int64(12); offset < limit; {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		var chunk [8]byte
		if offset+8 > limit {
			return false, fmt.Errorf("音频WAV块头不完整")
		}
		if _, err = io.ReadFull(file, chunk[:]); err != nil {
			return false, err
		}
		size := int64(binary.LittleEndian.Uint32(chunk[4:]))
		end := offset + 8 + size
		if end+(size%2) > limit {
			return false, fmt.Errorf("音频WAV块超出文件范围")
		}
		switch string(chunk[:4]) {
		case "fmt ":
			var format [16]byte
			if size < 16 {
				return false, fmt.Errorf("音频WAV格式不完整")
			}
			if _, err = io.ReadFull(file, format[:]); err != nil {
				return false, err
			}
			if binary.LittleEndian.Uint16(format[:2]) != 1 || binary.LittleEndian.Uint16(format[2:4]) != 1 || binary.LittleEndian.Uint16(format[14:]) != 16 {
				return false, fmt.Errorf("音频检查需要单声道PCM16")
			}
			formatted = true
		case "data":
			if !formatted || size == 0 || size%2 != 0 {
				return false, fmt.Errorf("音频PCM数据无效或为空")
			}
			found = true
			for remaining := size; remaining > 0; {
				if err := ctx.Err(); err != nil {
					return false, err
				}
				length := min(remaining, int64(len(buffer)))
				if _, err = io.ReadFull(file, buffer[:length]); err != nil {
					return false, err
				}
				for _, sampleByte := range buffer[:length] {
					nonzero = nonzero || sampleByte != 0
				}
				remaining -= length
			}
		}
		offset = end + size%2
		if _, err = file.Seek(offset, io.SeekStart); err != nil {
			return false, err
		}
	}
	if !found {
		return false, fmt.Errorf("音频没有PCM数据")
	}
	return !nonzero, nil
}
