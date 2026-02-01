//go:build trace
// +build trace

// Package cgo traces pre-emphasis state differences
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestTracePreemphState traces pre-emphasis state between gopus full encoding and libopus.
func TestTracePreemphState(t *testing.T) {
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

	mode := celt.GetModeConfig(frameSize)
	lm := mode.LM
	shortBlocks := mode.ShortBlocks
	M := 1 << lm

	// === FULL GOPUS ENCODING PIPELINE ===
	// This mirrors what EncodeFrame does
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	// Step 1: Apply DC rejection (high-pass filter)
	dcRejected := goEnc.ApplyDCReject(pcm64)
	t.Logf("After DC reject - sample 100: %.10f (was %.10f)", dcRejected[100], pcm64[100])

	// Step 2: Apply delay buffer
	// For first frame, delay buffer is zero, so samplesForFrame = dcRejected[:frameSize]
	samplesForFrame := dcRejected[:frameSize]

	// Step 3: Apply pre-emphasis with scaling
	preemph := goEnc.ApplyPreemphasisWithScaling(samplesForFrame)
	t.Logf("After pre-emphasis - sample 100: %.10f", preemph[100])

	// Step 4: Compute MDCT with history
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, goEnc.OverlapBuffer(), shortBlocks)

	// === COMPARE WITH DIRECT PRE-EMPHASIS (no DC reject, no delay buffer) ===
	goEnc2 := celt.NewEncoder(1)
	goEnc2.Reset()
	preemph2 := goEnc2.ApplyPreemphasisWithScaling(pcm64)
	t.Logf("Direct pre-emphasis - sample 100: %.10f", preemph2[100])

	// === COMPARE WITH LIBOPUS PRE-EMPHASIS ===
	libPreemph := ApplyLibopusPreemphasis(pcm32, 0.85)
	t.Logf("Libopus pre-emphasis - sample 100: %.10f", float64(libPreemph[100]))

	// === MDCT from each ===
	mdctCoeffs2 := celt.ComputeMDCTWithHistory(preemph2, goEnc2.OverlapBuffer(), shortBlocks)

	libPreemph64 := make([]float64, len(libPreemph))
	for i, v := range libPreemph {
		libPreemph64[i] = float64(v)
	}
	libMDCT := celt.ComputeMDCTWithHistory(libPreemph64, make([]float64, 120), shortBlocks)

	t.Log("")
	t.Log("=== Band 0 MDCT Comparison ===")
	bandStart := celt.EBands[0] * M
	bandEnd := celt.EBands[1] * M
	t.Logf("Band 0: indices %d to %d", bandStart, bandEnd)

	t.Log("Idx  | Full encoder | Direct preemph | Libopus preemph")
	for i := bandStart; i < bandEnd; i++ {
		t.Logf("%4d | %+12.4f | %+12.4f | %+12.4f", i, mdctCoeffs[i], mdctCoeffs2[i], libMDCT[i])
	}

	// Check pre-emphasis diff
	t.Log("")
	t.Log("=== Pre-emphasis sample comparison ===")
	for i := 0; i < 10; i++ {
		t.Logf("Sample %d: full=%.4f direct=%.4f lib=%.4f", i, preemph[i], preemph2[i], libPreemph64[i])
	}

	// Compute band 0 linear amplitude
	t.Log("")
	t.Log("=== Band 0 linear amplitude ===")
	sum1, sum2, sum3 := 0.0, 0.0, 0.0
	for i := bandStart; i < bandEnd; i++ {
		sum1 += mdctCoeffs[i] * mdctCoeffs[i]
		sum2 += mdctCoeffs2[i] * mdctCoeffs2[i]
		sum3 += libMDCT[i] * libMDCT[i]
	}
	t.Logf("Full encoder: %.4f", math.Sqrt(sum1))
	t.Logf("Direct preemph: %.4f", math.Sqrt(sum2))
	t.Logf("Libopus preemph: %.4f", math.Sqrt(sum3))

	// === Now trace what the ACTUAL encoded packet has vs what we computed ===
	t.Log("")
	t.Log("=== Full Encoding ===")
	packet, _ := goEnc.EncodeFrame(pcm64, frameSize)
	t.Logf("Packet length: %d bytes", len(packet))

	// Encode using libopus for comparison
	libEnc, _ := NewLibopusEncoder(sampleRate, 1, OpusApplicationAudio)
	defer libEnc.Destroy()
	libEnc.SetBitrate(bitrate)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(false)

	libPacket, _ := libEnc.EncodeFloat(pcm32, frameSize)
	t.Logf("Libopus packet length: %d bytes", len(libPacket))

	// Compare first 20 bytes
	t.Log("First 20 bytes:")
	for i := 0; i < 20 && i < len(packet) && i < len(libPacket); i++ {
		marker := ""
		if packet[i] != libPacket[1:][i] {
			marker = " <-- DIFFERS"
		}
		t.Logf("Byte %2d: gopus=0x%02X libopus=0x%02X%s", i, packet[i], libPacket[1:][i], marker)
	}
}
