//go:build cgo_libopus
// +build cgo_libopus

// Package cgo tests gopus encode-decode roundtrip consistency.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/rangecoding"
)

// TestGopusRoundtrip tests if gopus decoder can decode gopus encoder output.
func TestGopusRoundtrip(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate 440Hz sine wave
	pcm := make([]float64, frameSize)
	for i := range pcm {
		ti := float64(i) / float64(sampleRate)
		pcm[i] = 0.5 * math.Sin(2*math.Pi*440*ti)
	}

	t.Log("=== Gopus Encode-Decode Roundtrip ===")
	t.Log("")

	// Encode with gopus
	enc := celt.NewEncoder(1)
	enc.SetBitrate(bitrate)

	packet, err := enc.EncodeFrame(pcm, frameSize)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	t.Logf("Encoded %d bytes", len(packet))
	showLen := 10
	if showLen > len(packet) {
		showLen = len(packet)
	}
	t.Logf("First %d bytes: %X", showLen, packet[:showLen])
	t.Log("")

	// Decode header flags to understand what was encoded
	t.Log("=== Decoding Header ===")
	rd := &rangecoding.Decoder{}
	rd.Init(packet)

	silence := rd.DecodeBit(15)
	t.Logf("silence: %d", silence)

	if silence == 1 {
		t.Log("Silence frame, no further decoding needed")
		return
	}

	postfilter := rd.DecodeBit(1)
	t.Logf("postfilter: %d", postfilter)

	mode := celt.GetModeConfig(frameSize)
	var transient int
	if mode.LM > 0 {
		transient = rd.DecodeBit(3)
	}
	t.Logf("transient: %d", transient)

	intra := rd.DecodeBit(3)
	t.Logf("intra: %d", intra)

	t.Log("")

	// Decode coarse energy
	t.Log("=== Decoding Coarse Energy ===")
	dec := celt.NewDecoder(1)
	dec.SetRangeDecoder(rd)

	coarseEnergies := dec.DecodeCoarseEnergy(mode.EffBands, intra == 1, mode.LM)

	t.Logf("First 5 coarse energies:")
	for i := 0; i < 5 && i < len(coarseEnergies); i++ {
		t.Logf("  Band %d: %.4f", i, coarseEnergies[i])
	}
	t.Log("")

	// Compare with original energies
	t.Log("=== Comparing Encoded vs Decoded Energies ===")

	// Compute original energies using the encoder
	enc2 := celt.NewEncoder(1)
	enc2.SetBitrate(bitrate)
	preemph := enc2.ApplyPreemphasisWithScaling(pcm)
	history := make([]float64, celt.Overlap)
	mdct := celt.ComputeMDCTWithHistory(preemph, history, 1)
	origEnergies := enc2.ComputeBandEnergies(mdct, mode.EffBands, frameSize)

	t.Logf("First 5 original energies:")
	for i := 0; i < 5 && i < len(origEnergies); i++ {
		t.Logf("  Band %d: %.4f", i, origEnergies[i])
	}

	t.Log("")
	t.Logf("Comparison (difference should be <= 6dB per coarse step):")
	for i := 0; i < 5 && i < len(origEnergies) && i < len(coarseEnergies); i++ {
		diff := math.Abs(origEnergies[i] - coarseEnergies[i])
		status := "OK"
		if diff > 6 {
			status = "LARGE DIFF"
		}
		t.Logf("  Band %d: orig=%.4f, decoded=%.4f, diff=%.4f [%s]",
			i, origEnergies[i], coarseEnergies[i], diff, status)
	}
}

// TestLibopusDecodeGopus tests if libopus can decode gopus-encoded packets.
func TestLibopusDecodeGopus(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate 440Hz sine wave
	pcm := make([]float64, frameSize)
	for i := range pcm {
		ti := float64(i) / float64(sampleRate)
		pcm[i] = 0.5 * math.Sin(2*math.Pi*440*ti)
	}

	t.Log("=== Libopus Decoding Gopus Output ===")
	t.Log("")

	// Encode with gopus CELT encoder
	enc := celt.NewEncoder(1)
	enc.SetBitrate(bitrate)
	celtPacket, err := enc.EncodeFrame(pcm, frameSize)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}

	// Add TOC byte for Opus packet format
	// 0xF8 = CELT-only, 20ms frame, mono
	tocByte := byte(0xF8)
	opusPacket := append([]byte{tocByte}, celtPacket...)

	t.Logf("Gopus CELT packet: %d bytes", len(celtPacket))
	t.Logf("With TOC: %d bytes", len(opusPacket))
	t.Log("")

	// Decode with libopus
	libDec, err := NewLibopusDecoder(sampleRate, 1)
	if err != nil {
		t.Fatalf("libopus decoder creation failed: %v", err)
	}
	defer libDec.Destroy()

	decoded, decLen := libDec.DecodeFloat(opusPacket, frameSize)
	if decLen <= 0 {
		t.Fatalf("libopus decode failed: %d", decLen)
	}

	t.Logf("Decoded: buffer=%d samples, actual=%d samples", len(decoded), decLen)
	t.Log("")

	// Compare original vs decoded
	t.Log("=== Sample Comparison ===")
	indices := []int{0, 100, 200, 300, 400, 500}
	for _, i := range indices {
		if i < len(pcm) && i < decLen {
			t.Logf("  [%3d] orig=%.4f, decoded=%.4f", i, pcm[i], decoded[i])
		}
	}
	t.Log("")

	// Compute correlation
	sampleCount := decLen
	if sampleCount > len(pcm) {
		sampleCount = len(pcm)
	}
	var sumOrig, sumDec, sumOrigSq, sumDecSq, sumOrigDec float64
	n := float64(sampleCount)
	for i := 0; i < sampleCount; i++ {
		sumOrig += pcm[i]
		sumDec += float64(decoded[i])
		sumOrigSq += pcm[i] * pcm[i]
		sumDecSq += float64(decoded[i]) * float64(decoded[i])
		sumOrigDec += pcm[i] * float64(decoded[i])
	}

	meanOrig := sumOrig / n
	meanDec := sumDec / n
	varOrig := sumOrigSq/n - meanOrig*meanOrig
	varDec := sumDecSq/n - meanDec*meanDec
	covar := sumOrigDec/n - meanOrig*meanDec

	correlation := 0.0
	if varOrig > 0 && varDec > 0 {
		correlation = covar / math.Sqrt(varOrig*varDec)
	}

	t.Logf("Correlation: %.4f", correlation)

	// Compute SNR
	var signalPower, noisePower float64
	for i := 100; i < sampleCount-100; i++ {
		signalPower += pcm[i] * pcm[i]
		noise := float64(decoded[i]) - pcm[i]
		noisePower += noise * noise
	}
	snr := 10 * math.Log10(signalPower/(noisePower+1e-10))
	t.Logf("SNR: %.1f dB", snr)

	// Quality assessment
	t.Log("")
	if correlation > 0.9 && snr > 20 {
		t.Log("GOOD: Audio quality is acceptable")
	} else if correlation > 0.5 && snr > 0 {
		t.Log("MARGINAL: Audio quality is poor but signal is recognizable")
	} else {
		t.Log("BAD: Audio quality is very poor, signal is not recognizable")
	}
}
