//go:build trace
// +build trace

// Package cgo traces encoding to find where PVQ bands start.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestTracePVQStartPosition traces what bit position PVQ encoding starts at.
func TestTracePVQStartPosition(t *testing.T) {
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

	// Get libopus reference
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
	libPayload := libPacket[1:]
	t.Logf("libopus packet: %d bytes", len(libPayload))

	// Now do gopus encoding step by step with tracing
	t.Log("\n=== GOPUS Step-by-step encoding trace ===")

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM
	t.Logf("Mode: nbBands=%d, LM=%d", nbBands, lm)

	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	// Note: Debug tracing would need to be enabled at compile time

	goPacket, err := goEnc.EncodeFrame(pcm64, frameSize)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}
	t.Logf("gopus packet: %d bytes", len(goPacket))
	t.Logf("gopus final range: 0x%08X", goEnc.FinalRange())
	t.Logf("libopus final range: 0x%08X", libEnc.GetFinalRange())

	// Compare bytes
	minLen := len(goPacket)
	if len(libPayload) < minLen {
		minLen = len(libPayload)
	}

	firstDiff := -1
	for i := 0; i < minLen; i++ {
		if goPacket[i] != libPayload[i] {
			firstDiff = i
			break
		}
	}

	if firstDiff < 0 {
		t.Log("Packets match completely!")
		return
	}

	t.Logf("\nFirst divergence at byte %d (bit %d)", firstDiff, firstDiff*8)

	// Binary analysis
	gb := goPacket[firstDiff]
	lb := libPayload[firstDiff]
	xor := gb ^ lb
	t.Logf("gopus=0x%02X (%08b), libopus=0x%02X (%08b), XOR=0x%02X", gb, gb, lb, lb, xor)

	// Find exact bit position
	for b := 0; b < 8; b++ {
		if (xor>>b)&1 == 1 {
			exactBit := firstDiff*8 + b
			t.Logf("Exact diverging bit: %d (byte %d, bit %d within byte)", exactBit, firstDiff, b)
			break
		}
	}

	// The divergence at bit 128 could be:
	// 1. Fine energy encoding (if tell < 128 before fine energy but tell > 128 after)
	// 2. PVQ band 0 encoding (if tell >= 128 before PVQ starts)
	// 3. Energy finalise bits
	//
	// To determine this, we need to trace tell position at key points.
	// The debug tracer may provide this info if enabled.

	t.Log("\n=== Analysis ===")
	t.Log("Given byte 16 = bit 128, and typical encoding layout:")
	t.Log("  Header + Coarse + TF + Spread + Dynalloc + Trim ≈ 109 bits")
	t.Log("  Allocation encoding ≈ 0-5 bits (skip/intensity/dual-stereo)")
	t.Log("  Fine energy ≈ 10-20 bits typically")
	t.Log("")
	t.Log("If fine energy uses ~15 bits, PVQ would start around bit 124-129")
	t.Log("This matches the divergence at bit 128!")
	t.Log("")
	t.Log("The single-bit LSB difference suggests a very small numerical")
	t.Log("difference causing different rounding/quantization.")
}

// TestDecodeAndCompare decodes both packets and compares decoded values.
func TestDecodeAndCompare(t *testing.T) {
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

	// Decode libopus packet with gopus decoder
	dec := celt.NewDecoder(1)
	libPayload := libPacket[1:]
	t.Logf("Decoding libopus payload (%d bytes)", len(libPayload))

	libDecoded, err := dec.DecodeFrame(libPayload, frameSize)
	if err != nil {
		t.Logf("gopus decoder error on libopus packet: %v", err)
	} else {
		// Compute SNR against original
		var signalPower, noisePower float64
		for i := 0; i < frameSize && i < len(libDecoded); i++ {
			signal := pcm64[i]
			noise := signal - libDecoded[i]
			signalPower += signal * signal
			noisePower += noise * noise
		}
		if noisePower > 0 {
			snr := 10 * math.Log10(signalPower/noisePower)
			t.Logf("libopus packet decoded by gopus: SNR=%.2f dB", snr)
		}
	}

	// Decode gopus packet with gopus decoder
	dec2 := celt.NewDecoder(1)
	t.Logf("Decoding gopus payload (%d bytes)", len(goPacket))

	goDecoded, err := dec2.DecodeFrame(goPacket, frameSize)
	if err != nil {
		t.Logf("gopus decoder error on gopus packet: %v", err)
	} else {
		var signalPower, noisePower float64
		for i := 0; i < frameSize && i < len(goDecoded); i++ {
			signal := pcm64[i]
			noise := signal - goDecoded[i]
			signalPower += signal * signal
			noisePower += noise * noise
		}
		if noisePower > 0 {
			snr := 10 * math.Log10(signalPower/noisePower)
			t.Logf("gopus packet decoded by gopus: SNR=%.2f dB", snr)
		}
	}

	// Compare decoded outputs
	if len(libDecoded) > 0 && len(goDecoded) > 0 {
		t.Log("\n=== Comparing decoded outputs ===")
		maxDiff := 0.0
		sumDiff := 0.0
		for i := 0; i < len(libDecoded) && i < len(goDecoded); i++ {
			diff := math.Abs(libDecoded[i] - goDecoded[i])
			sumDiff += diff
			if diff > maxDiff {
				maxDiff = diff
			}
		}
		avgDiff := sumDiff / float64(len(libDecoded))
		t.Logf("Max difference: %.6f", maxDiff)
		t.Logf("Avg difference: %.6f", avgDiff)
	}
}

// TestInspectFineEnergyBits traces fine energy bit usage.
func TestInspectFineEnergyBits(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate test signal
	pcm64 := make([]float64, frameSize)
	for i := range pcm64 {
		pcm64[i] = 0.5 * math.Sin(2*math.Pi*440*float64(i)/float64(sampleRate))
	}

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	// Do full encoding to get allocation result
	_, err := goEnc.EncodeFrame(pcm64, frameSize)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	// Now let's trace what happens with fine energy in detail
	// We can decode the libopus packet and trace the fine energy decoding
	t.Log("=== Fine Energy Analysis ===")
	t.Logf("nbBands=%d, LM=%d", nbBands, lm)

	// Typical fine bits allocation for 64kbps mono:
	// Lower bands get more bits (2-3), higher bands get fewer (0-1)
	t.Log("\nTypical fine bits allocation pattern for 64kbps:")
	t.Log("  Bands 0-4: ~3 bits each = ~15 bits")
	t.Log("  Bands 5-9: ~2 bits each = ~10 bits")
	t.Log("  Bands 10-14: ~1 bit each = ~5 bits")
	t.Log("  Bands 15-20: ~0 bits each = ~0 bits")
	t.Log("  Total fine energy: ~30 bits")
	t.Log("")
	t.Log("If header+coarse+TF+spread+dynalloc+trim+alloc ≈ 115 bits,")
	t.Log("and fine energy uses ~30 bits,")
	t.Log("then PVQ would start around bit 145.")
	t.Log("")
	t.Log("But the divergence is at bit 128, which suggests:")
	t.Log("1. Fine energy uses fewer bits than expected (~13 bits)")
	t.Log("2. Or the divergence is IN the fine energy encoding")
	t.Log("")
	t.Log("Given the single-bit LSB difference, it's likely a rounding")
	t.Log("boundary issue in fine energy quantization.")
}

// TestCompareEncodedBytes does detailed byte comparison.
func TestCompareEncodedBytes(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	pcm32 := make([]float32, frameSize)
	pcm64 := make([]float64, frameSize)
	for i := range pcm32 {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm32[i] = float32(val)
		pcm64[i] = val
	}

	// libopus
	libEnc, _ := NewLibopusEncoder(sampleRate, 1, OpusApplicationAudio)
	defer libEnc.Destroy()
	libEnc.SetBitrate(bitrate)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(false)
	libPacket, _ := libEnc.EncodeFloat(pcm32, frameSize)
	libPayload := libPacket[1:]

	// gopus
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)
	goPacket, _ := goEnc.EncodeFrame(pcm64, frameSize)

	t.Log("=== Detailed Byte Comparison ===")
	t.Log("")
	t.Log("Byte | gopus    | libopus  | Match | XOR")
	t.Log("-----+----------+----------+-------+-----")

	minLen := len(goPacket)
	if len(libPayload) < minLen {
		minLen = len(libPayload)
	}

	for i := 0; i < minLen && i < 25; i++ {
		g := goPacket[i]
		l := libPayload[i]
		match := "  OK "
		if g != l {
			match = "DIFF!"
		}
		t.Logf("%4d | %08b | %08b | %s | %08b", i, g, l, match, g^l)
	}
}

// Helper: simulate range encoder state extraction
func extractRangeEncoderState(packet []byte) (uint32, uint32) {
	if len(packet) < 4 {
		return 0, 0
	}
	// Range encoder state is typically stored in the last bytes
	// This is a simplified extraction
	val := uint32(0)
	for i := len(packet) - 4; i < len(packet); i++ {
		val = (val << 8) | uint32(packet[i])
	}
	return val, 0
}
