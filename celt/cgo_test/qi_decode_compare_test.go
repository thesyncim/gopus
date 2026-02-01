//go:build cgo_libopus
// +build cgo_libopus

// Package cgo compares decoded qi values between gopus and libopus.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/rangecoding"
)

// TestDecodeQIFromBothPackets decodes qi values from both gopus and libopus packets.
func TestDecodeQIFromBothPackets(t *testing.T) {
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

	t.Log("=== Decode QI Values from Both Packets ===")
	t.Log("")

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	// === LIBOPUS ===
	t.Log("=== LIBOPUS Encoding ===")

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

	libPayload := libPacket[1:] // Skip TOC
	t.Logf("Libopus packet: %d bytes (payload: %d)", len(libPacket), len(libPayload))
	t.Logf("First 10 payload bytes: %02X", libPayload[:minIntQDC(10, len(libPayload))])

	// Decode libopus header
	libRd := &rangecoding.Decoder{}
	libRd.Init(libPayload)

	libSilence := libRd.DecodeBit(15)
	libPostfilter := libRd.DecodeBit(1)
	var libTransient int
	if lm > 0 {
		libTransient = libRd.DecodeBit(3)
	}
	libIntra := libRd.DecodeBit(3)

	t.Logf("Libopus flags: silence=%d, postfilter=%d, transient=%d, intra=%d",
		libSilence, libPostfilter, libTransient, libIntra)

	// Decode libopus qi values
	probModel := celt.GetEProbModel()
	prob := probModel[lm][0] // inter mode
	if libIntra == 1 {
		prob = probModel[lm][1]
	}

	libDec := celt.NewDecoder(1)
	libDec.SetRangeDecoder(libRd)

	libQIs := make([]int, nbBands)
	for band := 0; band < nbBands; band++ {
		pi := 2 * band
		if pi > 40 {
			pi = 40
		}
		fs := int(prob[pi]) << 7
		decay := int(prob[pi+1]) << 6
		libQIs[band] = libDec.DecodeLaplaceTest(fs, decay)
	}

	t.Log("")
	t.Logf("Libopus QI values (first 10):")
	for i := 0; i < 10 && i < nbBands; i++ {
		t.Logf("  Band %d: qi=%d", i, libQIs[i])
	}

	// === GOPUS ===
	t.Log("")
	t.Log("=== GOPUS Encoding ===")

	enc := celt.NewEncoder(1)
	enc.Reset()
	enc.SetBitrate(bitrate)
	enc.SetVBR(false)

	gopusPacket, err := enc.EncodeFrame(pcm64, frameSize)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}

	t.Logf("Gopus packet: %d bytes", len(gopusPacket))
	t.Logf("First 10 bytes: %02X", gopusPacket[:minIntQDC(10, len(gopusPacket))])

	// Decode gopus header
	gopusRd := &rangecoding.Decoder{}
	gopusRd.Init(gopusPacket)

	gopusSilence := gopusRd.DecodeBit(15)
	gopusPostfilter := gopusRd.DecodeBit(1)
	var gopusTransient int
	if lm > 0 {
		gopusTransient = gopusRd.DecodeBit(3)
	}
	gopusIntra := gopusRd.DecodeBit(3)

	t.Logf("Gopus flags: silence=%d, postfilter=%d, transient=%d, intra=%d",
		gopusSilence, gopusPostfilter, gopusTransient, gopusIntra)

	// Decode gopus qi values
	gopusProb := probModel[lm][0] // inter mode
	if gopusIntra == 1 {
		gopusProb = probModel[lm][1]
	}

	gopusDec := celt.NewDecoder(1)
	gopusDec.SetRangeDecoder(gopusRd)

	gopusQIs := make([]int, nbBands)
	for band := 0; band < nbBands; band++ {
		pi := 2 * band
		if pi > 40 {
			pi = 40
		}
		fs := int(gopusProb[pi]) << 7
		decay := int(gopusProb[pi+1]) << 6
		gopusQIs[band] = gopusDec.DecodeLaplaceTest(fs, decay)
	}

	t.Log("")
	t.Logf("Gopus QI values (first 10):")
	for i := 0; i < 10 && i < nbBands; i++ {
		t.Logf("  Band %d: qi=%d", i, gopusQIs[i])
	}

	// === COMPARISON ===
	t.Log("")
	t.Log("=== QI Comparison ===")
	t.Log("Band | Gopus QI | Libopus QI | Match")
	t.Log("-----+----------+------------+------")

	qiDiffs := 0
	for band := 0; band < nbBands; band++ {
		match := "YES"
		if gopusQIs[band] != libQIs[band] {
			match = "NO"
			qiDiffs++
		}
		t.Logf("%4d | %8d | %10d | %s", band, gopusQIs[band], libQIs[band], match)
	}

	t.Log("")
	if qiDiffs > 0 {
		t.Logf("RESULT: %d/%d QI values differ", qiDiffs, nbBands)
	} else {
		t.Log("RESULT: All QI values MATCH!")
	}

	// Sum of absolute differences
	totalDiff := 0
	for band := 0; band < nbBands; band++ {
		diff := gopusQIs[band] - libQIs[band]
		if diff < 0 {
			diff = -diff
		}
		totalDiff += diff
	}
	t.Logf("Sum of absolute QI differences: %d", totalDiff)
}

func minIntQDC(a, b int) int {
	if a < b {
		return a
	}
	return b
}
