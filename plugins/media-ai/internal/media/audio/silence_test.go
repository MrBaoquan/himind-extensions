package audio

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func pcmFixture(samples []byte) []byte {
	data := make([]byte, 44+len(samples))
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], 16000)
	binary.LittleEndian.PutUint32(data[28:], 32000)
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], uint32(len(samples)))
	copy(data[44:], samples)
	return data
}

func TestDigitalSilencePreservesEvenLowestNonzeroSamples(t *testing.T) {
	for _, tc := range []struct {
		name    string
		samples []byte
		silent  bool
	}{
		{"zero", make([]byte, 130000), true},
		{"quiet-positive", []byte{0, 0, 1, 0}, false},
		{"quiet-negative", []byte{255, 255, 0, 0}, false},
		{"only-last-sample", append(make([]byte, 130000), 1, 0), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "audio.wav")
			if err := os.WriteFile(path, pcmFixture(tc.samples), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := DigitalSilence(context.Background(), path)
			if err != nil || got != tc.silent {
				t.Fatalf("silent=%v err=%v", got, err)
			}
		})
	}
}

func TestDigitalSilenceValidatesChunksAndHonorsCancellation(t *testing.T) {
	valid := pcmFixture([]byte{0, 0, 0, 0})
	for _, payload := range [][]byte{nil, []byte("not a WAV"), valid[:len(valid)-1], pcmFixture(nil), pcmFixture([]byte{0})} {
		path := filepath.Join(t.TempDir(), "bad.wav")
		_ = os.WriteFile(path, payload, 0600)
		if _, err := DigitalSilence(context.Background(), path); err == nil {
			t.Fatal("invalid or empty WAV accepted")
		}
	}
	// Metadata bytes are not samples. FFmpeg may add a LIST chunk.
	metadata := append([]byte("LIST"), 4, 0, 0, 0, 'I', 'N', 'F', 'O')
	withMetadata := append(append(append([]byte{}, valid[:36]...), metadata...), valid[36:]...)
	binary.LittleEndian.PutUint32(withMetadata[4:], uint32(len(withMetadata)-8))
	path := filepath.Join(t.TempDir(), "metadata.wav")
	_ = os.WriteFile(path, withMetadata, 0600)
	if silent, err := DigitalSilence(context.Background(), path); err != nil || !silent {
		t.Fatalf("metadata altered silence: %v %v", silent, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := DigitalSilence(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
}
