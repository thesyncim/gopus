// Package cgo captures PVQ indices during decode to find the first divergence.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

type pvqCaptureTracer struct {
	celt.NoopTracer
	entries []pvqEntry
}

type pvqEntry struct {
	band  int
	index uint32
	k     int
	n     int
}

func (t *pvqCaptureTracer) TracePVQ(band int, index uint32, k, n int, _ []int) {
	t.entries = append(t.entries, pvqEntry{
		band:  band,
		index: index,
		k:     k,
		n:     n,
	})
}

func decodePVQWithTracer(pkt []byte, frameSize, nbBands int, tracer *pvqCaptureTracer) error {
	orig := celt.DefaultTracer
	celt.DefaultTracer = tracer
	defer func() { celt.DefaultTracer = orig }()

	dec := celt.NewDecoder(1)
	_, err := dec.DecodeFrame(pkt, frameSize)
	return err
}

// TestTracePVQDecodeIndices compares PVQ indices recorded during decode.
func TestTracePVQDecodeIndices(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate 440Hz sine wave
	pcm32 := make([]float32, frameSize)
	pcm64 := make([]float64, frameSize)
	for i := range pcm32 {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm32[i] = float32(val)
		pcm64[i] = val
	}

	// Encode with libopus
	libEnc, err := NewLibopusEncoder(sampleRate, 1, OpusApplicationAudio)
	if err != nil {
		t.Fatalf("libopus encoder creation failed: %v", err)
	}
	defer libEnc.Destroy()
	libEnc.SetBitrate(bitrate)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(false)

	libPacket, _ := libEnc.EncodeFloat(pcm32, frameSize)
	libPayload := libPacket[1:]

	// Encode with gopus
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	goPacket, _ := goEnc.EncodeFrame(pcm64, frameSize)

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands

	libTracer := &pvqCaptureTracer{
		entries: make([]pvqEntry, 0, nbBands),
	}
	goTracer := &pvqCaptureTracer{
		entries: make([]pvqEntry, 0, nbBands),
	}

	if err := decodePVQWithTracer(libPayload, frameSize, nbBands, libTracer); err != nil {
		t.Fatalf("decode libopus failed: %v", err)
	}
	if err := decodePVQWithTracer(goPacket, frameSize, nbBands, goTracer); err != nil {
		t.Fatalf("decode gopus failed: %v", err)
	}

	minLen := len(libTracer.entries)
	if len(goTracer.entries) < minLen {
		minLen = len(goTracer.entries)
	}
	firstDiff := -1
	for i := 0; i < minLen; i++ {
		le := libTracer.entries[i]
		ge := goTracer.entries[i]
		if le.band != ge.band || le.index != ge.index || le.k != ge.k || le.n != ge.n {
			firstDiff = i
			break
		}
	}
	if firstDiff < 0 {
		if len(libTracer.entries) == len(goTracer.entries) {
			t.Log("PVQ decode sequences match")
			return
		}
		t.Fatalf("PVQ decode entry count differs: lib=%d go=%d", len(libTracer.entries), len(goTracer.entries))
	}

	t.Logf("First PVQ decode diff at entry %d", firstDiff)
	for i := firstDiff; i < minLen && i < firstDiff+4; i++ {
		le := libTracer.entries[i]
		ge := goTracer.entries[i]
		t.Logf("Entry %d: lib(band=%d k=%d n=%d idx=%d) go(band=%d k=%d n=%d idx=%d)",
			i, le.band, le.k, le.n, le.index, ge.band, ge.k, ge.n, ge.index)
	}
	t.Fatalf("PVQ decode mismatch at entry %d", firstDiff)
}
