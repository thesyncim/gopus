//go:build trace
// +build trace

// Package cgo traces actual flags used by gopus and libopus encoders.
// Agent 23: Compare actual header flags between encoders.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/celt"
)

// TestTraceFlagsComparison decodes both packets to extract actual flag values
func TestTraceFlagsComparison(t *testing.T) {
	t.Log("=== Agent 23: Flag Comparison ===")
	t.Log("")

	// Generate 440Hz sine wave test signal
	frameSize := 960
	channels := 1
	freq := 440.0
	amp := 0.5

	pcm := make([]float32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		sample := amp * math.Sin(2.0*math.Pi*freq*float64(i)/48000.0)
		pcm[i] = float32(sample)
	}

	// Encode with libopus
	libEnc, err := NewLibopusEncoder(48000, channels, OpusApplicationAudio)
	if err != nil || libEnc == nil {
		t.Fatalf("Failed to create libopus encoder: %v", err)
	}
	defer libEnc.Destroy()

	libEnc.SetBitrate(64000)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(true)
	libEnc.SetSignal(OpusSignalMusic)

	libBytes, libLen := libEnc.EncodeFloat(pcm, frameSize)
	if libLen < 0 {
		t.Fatalf("libopus encode failed: %d", libLen)
	}

	// Encode with gopus
	gopusEnc, err := gopus.NewEncoder(48000, channels, gopus.ApplicationAudio)
	if err != nil {
		t.Fatalf("Failed to create gopus encoder: %v", err)
	}
	_ = gopusEnc.SetBitrate(64000)
	gopusEnc.SetFrameSize(frameSize)

	gopusPacket := make([]byte, 4000)
	gopusLen, err := gopusEnc.Encode(pcm, gopusPacket)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}

	t.Logf("libopus: %d bytes", libLen)
	t.Logf("gopus: %d bytes", gopusLen)
	t.Logf("")

	// Decode header flags from libopus packet using libopus decoder
	t.Log("=== libopus Packet Header Analysis ===")
	libPayload := libBytes[1:] // Skip TOC
	decodePacketFlags(t, libPayload, "libopus")

	t.Log("")
	t.Log("=== gopus Packet Header Analysis ===")
	gopusPayload := gopusPacket[1:gopusLen] // Skip TOC
	decodePacketFlags(t, gopusPayload, "gopus")

	// Also show what flags the gopus internal encoder actually used
	t.Log("")
	t.Log("=== Gopus Internal CELT Encoder Flags ===")
	traceGopusInternalFlags(t, pcm)
}

// decodePacketFlags decodes header flags from a CELT packet using range decoder
func decodePacketFlags(t *testing.T, payload []byte, name string) {
	if len(payload) < 1 {
		t.Logf("%s: Empty payload", name)
		return
	}

	// Use the CELT decoder to extract flags
	dec := celt.NewDecoder(1)

	// Try to decode the frame - this will parse the header
	_, err := dec.DecodeFrame(payload, 960)
	if err != nil {
		t.Logf("%s: Decode error (may still have parsed flags): %v", name, err)
	}

	// Get decoded flags from decoder state (if available)
	t.Logf("%s payload first 10 bytes: %02x", name, payload[:minFlags(10, len(payload))])

	// Manual bit extraction to show flag values
	// In CELT encoding order:
	// 1. Silence flag (logp=15) - nearly 0 bits if 0
	// 2. Postfilter (logp=1) - ~1 bit
	// 3. Transient (logp=3) - if LM>0
	// 4. Intra (logp=3)

	// The flags are range-coded, not raw bits. We need to trace through the decoder.
	// For now, show the raw bytes and let the range decoder extract values.
	t.Logf("Payload bytes 0-3: %02x %02x %02x %02x",
		payload[0], payload[1], payload[2], payload[3])

	// The actual flag values require running through the range decoder
	// which the existing tests do. Let's just compare the raw bytes.
}

// traceGopusInternalFlags traces what flags the gopus encoder actually uses
func traceGopusInternalFlags(t *testing.T, pcm []float32) {
	// Convert to float64 for internal encoder
	pcm64 := make([]float64, len(pcm))
	for i, v := range pcm {
		pcm64[i] = float64(v)
	}

	frameSize := 960

	// Create encoder
	enc := celt.NewEncoder(1)
	enc.Reset()
	enc.SetBitrate(64000)

	// Run through the signal processing to see what transient detection decides
	preemph := enc.ApplyPreemphasisWithScaling(pcm64)

	overlap := celt.Overlap
	transientInput := make([]float64, overlap+frameSize)
	copy(transientInput[overlap:], preemph)

	result := enc.TransientAnalysis(transientInput, frameSize+overlap, false)

	t.Logf("Transient detection result: isTransient=%v, tfEstimate=%.4f",
		result.IsTransient, result.TfEstimate)

	// Get the mode config
	mode := celt.GetModeConfig(frameSize)
	lm := mode.LM

	// First frame is intra
	isIntra := enc.IsIntraFrame()

	t.Logf("Frame 0: LM=%d, isIntra=%v", lm, isIntra)

	// For first frame:
	// - gopus sets transient=true if transient analysis detects it OR if frame 0 and LM>0
	// - libopus may have different transient detection

	t.Logf("")
	t.Logf("gopus flags for first frame:")
	t.Logf("  silence=0 (non-silent signal)")
	t.Logf("  postfilter=0 (not implemented)")

	// Check if first frame forces transient
	forceTransient := (enc.FrameCount() == 0 && lm > 0)
	effectiveTransient := result.IsTransient || forceTransient

	t.Logf("  transient=%v (detected=%v, forced=%v)", effectiveTransient, result.IsTransient, forceTransient)
	t.Logf("  intra=%v", isIntra)
}

func minFlags(a, b int) int {
	if a < b {
		return a
	}
	return b
}
