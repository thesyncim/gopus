// Package cgo compares PVQ traces between gopus and libopus packets.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

type pvqEntry struct {
	band   int
	index  uint32
	k      int
	n      int
	pulses []int
}

type pvqTrace struct {
	celt.NoopTracer
	entries []pvqEntry
}

func (t *pvqTrace) TracePVQ(band int, index uint32, k, n int, pulses []int) {
	pCopy := make([]int, len(pulses))
	copy(pCopy, pulses)
	t.entries = append(t.entries, pvqEntry{
		band:   band,
		index:  index,
		k:      k,
		n:      n,
		pulses: pCopy,
	})
}

// TestPVQTraceCompare logs the first PVQ difference between gopus and libopus.
// This is a debugging aid; remove once PVQ mismatch is fixed.
func TestPVQTraceCompare(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate 440Hz sine wave
	pcm64 := make([]float64, frameSize)
	pcm32 := make([]float32, frameSize)
	for i := range pcm64 {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm64[i] = val
		pcm32[i] = float32(val)
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
	libPacket, libLen := libEnc.EncodeFloat(pcm32, frameSize)
	if libLen <= 0 {
		t.Fatalf("libopus encode failed: length=%d", libLen)
	}

	// Encode with gopus
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)
	goPacket, err := goEnc.EncodeFrame(pcm64, frameSize)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}

	// Trace gopus decode
	goTrace := &pvqTrace{}
	origTracer := celt.DefaultTracer
	celt.DefaultTracer = goTrace
	{
		dec := celt.NewDecoder(1)
		if _, err := dec.DecodeFrame(goPacket, frameSize); err != nil {
			t.Fatalf("gopus decode failed: %v", err)
		}
	}
	celt.DefaultTracer = origTracer

	// Trace libopus decode (skip TOC)
	libTrace := &pvqTrace{}
	celt.DefaultTracer = libTrace
	{
		dec := celt.NewDecoder(1)
		if _, err := dec.DecodeFrame(libPacket[1:], frameSize); err != nil {
			t.Fatalf("libopus decode failed: %v", err)
		}
	}
	celt.DefaultTracer = origTracer

	// Compare traces
	n := len(goTrace.entries)
	if len(libTrace.entries) < n {
		n = len(libTrace.entries)
	}
	for i := 0; i < n; i++ {
		g := goTrace.entries[i]
		l := libTrace.entries[i]
		if g.band != l.band || g.index != l.index || g.k != l.k || g.n != l.n {
			t.Fatalf("PVQ diff at entry %d: band %d vs %d, index 0x%X vs 0x%X, k %d vs %d, n %d vs %d",
				i, g.band, l.band, g.index, l.index, g.k, l.k, g.n, l.n)
		}
		// Compare pulses
		if len(g.pulses) != len(l.pulses) {
			t.Fatalf("PVQ diff at entry %d band %d: pulse length %d vs %d", i, g.band, len(g.pulses), len(l.pulses))
		}
		for j := range g.pulses {
			if g.pulses[j] != l.pulses[j] {
				t.Fatalf("PVQ diff at entry %d band %d: pulses[%d]=%d vs %d",
					i, g.band, j, g.pulses[j], l.pulses[j])
			}
		}
	}
	if len(goTrace.entries) != len(libTrace.entries) {
		t.Fatalf("PVQ trace length differs: gopus=%d libopus=%d", len(goTrace.entries), len(libTrace.entries))
	}
	t.Log("PVQ traces match")
}

// TestPVQInputCompare checks whether op_pvq_search matches libopus for the exact
// PVQ input vector produced by the encoder for band 2.
func TestPVQInputCompare(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate 440Hz sine wave
	pcm64 := make([]float64, frameSize)
	for i := range pcm64 {
		ti := float64(i) / float64(sampleRate)
		pcm64[i] = 0.5 * math.Sin(2*math.Pi*440*ti)
	}

	var captured []float64
	capturedK := 0
	capturedN := 0
	celt.SetPVQInputHookForTest(func(band int, x []float64, n, k int) {
		if band != 2 || captured != nil {
			return
		}
		captured = x
		capturedK = k
		capturedN = n
	})
	defer celt.SetPVQInputHookForTest(nil)

	enc := celt.NewEncoder(1)
	enc.Reset()
	enc.SetBitrate(bitrate)
	enc.SetComplexity(10)
	enc.SetVBR(false)
	if _, err := enc.EncodeFrame(pcm64, frameSize); err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	if captured == nil {
		t.Fatalf("no PVQ input captured for band 2")
	}
	t.Logf("Captured PVQ input: band=2 n=%d k=%d", capturedN, capturedK)

	goPulses, _ := celt.OpPVQSearchExport(captured, capturedK)
	libPulses, _ := LibopusPVQSearch(captured, capturedK)

	if len(goPulses) != len(libPulses) {
		t.Fatalf("pulse length mismatch: go=%d lib=%d", len(goPulses), len(libPulses))
	}
	for i := range goPulses {
		if goPulses[i] != libPulses[i] {
			t.Fatalf("PVQ search mismatch at idx %d: go=%d lib=%d", i, goPulses[i], libPulses[i])
		}
	}
	t.Log("PVQ search matches libopus for captured input")

	// Decode libopus packet to capture actual pulses for band 2
	libEnc, err := NewLibopusEncoder(sampleRate, 1, OpusApplicationAudio)
	if err != nil {
		t.Fatalf("libopus encoder creation failed: %v", err)
	}
	defer libEnc.Destroy()
	libEnc.SetBitrate(bitrate)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(false)
	libPacket, libLen := libEnc.EncodeFloat(float32SliceFrom64(pcm64), frameSize)
	if libLen <= 0 {
		t.Fatalf("libopus encode failed: length=%d", libLen)
	}

	libTrace := &pvqTrace{}
	origTracer := celt.DefaultTracer
	celt.DefaultTracer = libTrace
	{
		dec := celt.NewDecoder(1)
		if _, err := dec.DecodeFrame(libPacket[1:], frameSize); err != nil {
			t.Fatalf("libopus decode failed: %v", err)
		}
	}
	celt.DefaultTracer = origTracer

	var libBand2 *pvqEntry
	for i := range libTrace.entries {
		if libTrace.entries[i].band == 2 {
			libBand2 = &libTrace.entries[i]
			break
		}
	}
	if libBand2 == nil {
		t.Fatalf("no libopus PVQ entry for band 2")
	}
	t.Logf("Libopus band 2 pulses: %v", libBand2.pulses)

	// Compare captured input vector to quantized vector from libopus pulses.
	qVec := normalizeResidualTest(libBand2.pulses, 1.0)
	dot := 0.0
	for i := range captured {
		dot += captured[i] * qVec[i]
	}
	t.Logf("Captured input dot quantized vector: %.6f", dot)
}

func float32SliceFrom64(in []float64) []float32 {
	out := make([]float32, len(in))
	for i, v := range in {
		out[i] = float32(v)
	}
	return out
}

func normalizeResidualTest(pulses []int, gain float64) []float64 {
	out := make([]float64, len(pulses))
	if len(pulses) == 0 {
		return out
	}
	energy := 0.0
	for _, v := range pulses {
		energy += float64(v * v)
	}
	if energy <= 0 {
		return out
	}
	scale := gain / math.Sqrt(energy)
	for i, v := range pulses {
		out[i] = float64(v) * scale
	}
	return out
}
