package main

import (
	"encoding/binary"
	"testing"
)

func TestWaveformPeaksPreservesSilencePolarityAndTransient(t *testing.T) {
	pcm := make([]byte, 16)
	for i, n := range []int16{0, 0, -32768, 0, 0, 16384, 0, 0} {
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(n))
	}
	p := waveformPeaks(pcm, 4)
	if len(p) != 4 || p[0] != 0 || p[1] != 1 || p[2] != .5 || p[3] != 0 {
		t.Fatalf("incorrect peaks: %v", p)
	}
	if waveformPeaks(nil, 640) != nil || waveformPeaks(pcm, 0) != nil {
		t.Fatal("empty input must not manufacture a waveform")
	}
	if len(waveformPeaks(pcm, 640)) != 8 {
		t.Fatal("bins must not exceed samples")
	}
}
func TestWaveformSinkBoundsMemory(t *testing.T) {
	w := waveformPCM{data: make([]byte, 8000*2*1800+4095)}
	if _, err := w.Write([]byte{0, 1}); err == nil {
		t.Fatal("unbounded PCM sink")
	}
}
