// Package cgo tests full encoding state progression.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestFullEncodingStateTrace traces the complete encoding state.
func TestFullEncodingStateTrace(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate 440Hz sine wave
	samples := make([]float64, frameSize)
	for i := range samples {
		samples[i] = 0.5 * math.Sin(2*math.Pi*440*float64(i)/float64(sampleRate))
	}

	t.Log("=== Full Encoding State Trace ===")
	t.Log("")

	// Create encoder and compute all the analysis
	enc := celt.NewEncoder(1)
	enc.Reset()
	enc.SetBitrate(bitrate)

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	t.Logf("Mode: frameSize=%d, LM=%d, nbBands=%d", frameSize, lm, nbBands)

	// Step 1: Pre-emphasis
	preemph := enc.ApplyPreemphasisWithScaling(samples)

	// Step 2: Transient detection
	overlap := celt.Overlap
	transientInput := make([]float64, overlap+frameSize)
	copy(transientInput[overlap:], preemph)
	transientResult := enc.TransientAnalysis(transientInput, frameSize+overlap, false)
	transient := transientResult.IsTransient

	shortBlocks := 1
	if transient {
		shortBlocks = mode.ShortBlocks
	}

	t.Logf("Transient: %v (maskMetric=%.2f), shortBlocks=%d", transient, transientResult.MaskMetric, shortBlocks)

	// Step 3: MDCT
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, enc.OverlapBuffer(), shortBlocks)
	t.Logf("MDCT: %d coefficients", len(mdctCoeffs))

	// Step 4: Band energies
	energies := enc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	// Step 5: Initialize range encoder
	targetBits := bitrate * frameSize / sampleRate
	bufSize := (targetBits + 7) / 8
	if bufSize < 256 {
		bufSize = 256
	}
	buf := make([]byte, bufSize)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	t.Log("")
	t.Log("=== Range Encoder State Trace ===")
	t.Logf("Target bits: %d (%d bytes)", targetBits, bufSize)
	t.Logf("Initial: rng=0x%08X, val=0x%08X, tell=%d", re.Range(), re.Val(), re.Tell())

	// Step 6: Encode header flags
	// Silence (logp=15)
	tell := re.Tell()
	if tell == 1 {
		re.EncodeBit(0, 15) // Not silence
	}
	t.Logf("After silence(0): rng=0x%08X, val=0x%08X, tell=%d", re.Range(), re.Val(), re.Tell())

	// Postfilter (logp=1)
	if re.Tell()+16 <= targetBits {
		re.EncodeBit(0, 1)
	}
	t.Logf("After postfilter(0): rng=0x%08X, val=0x%08X, tell=%d", re.Range(), re.Val(), re.Tell())

	// Transient (logp=3)
	transientBit := 0
	if transient {
		transientBit = 1
	}
	if lm > 0 && re.Tell()+3 <= targetBits {
		re.EncodeBit(transientBit, 3)
	}
	t.Logf("After transient(%d): rng=0x%08X, val=0x%08X, tell=%d", transientBit, re.Range(), re.Val(), re.Tell())

	// Intra (logp=3) - first frame
	if re.Tell()+3 <= targetBits {
		re.EncodeBit(1, 3) // Intra = 1 for first frame
	}
	t.Logf("After intra(1): rng=0x%08X, val=0x%08X, tell=%d", re.Range(), re.Val(), re.Tell())

	// Compare with libopus header trace
	bits := []int{0, 0, transientBit, 1}
	logps := []int{15, 1, 3, 3}
	libStates, libHeaderBytes := TraceBitSequence(bits, logps)

	t.Log("")
	t.Log("=== Header State Comparison ===")
	if libStates != nil && len(libStates) > 4 {
		gopusRng := re.Range()
		gopusVal := re.Val()
		gopusTell := re.Tell()
		libRng := libStates[4].Rng
		libVal := libStates[4].Val
		libTell := libStates[4].Tell

		rngMatch := gopusRng == libRng
		valMatch := gopusVal == libVal
		tellMatch := gopusTell == libTell

		t.Logf("rng:  gopus=0x%08X, libopus=0x%08X - %v", gopusRng, libRng, matchStrState(rngMatch))
		t.Logf("val:  gopus=0x%08X, libopus=0x%08X - %v", gopusVal, libVal, matchStrState(valMatch))
		t.Logf("tell: gopus=%d, libopus=%d - %v", gopusTell, libTell, matchStrState(tellMatch))

		if rngMatch && valMatch && tellMatch {
			t.Log("HEADER STATE MATCHES!")
		} else {
			t.Log("HEADER STATE DIFFERS!")
		}
	}
	t.Logf("Libopus header-only bytes: %02X", libHeaderBytes)

	// Step 7: Encode coarse energy
	// Setup encoder for Laplace encoding
	enc.SetRangeEncoder(re)
	enc.SetFrameBitsForTest(targetBits)
	quantizedEnergies := enc.EncodeCoarseEnergy(energies, nbBands, true, lm)

	t.Log("")
	t.Log("=== After Coarse Energy ===")
	t.Logf("After coarse energy: rng=0x%08X, val=0x%08X, tell=%d", re.Range(), re.Val(), re.Tell())

	// Finalize and get bytes
	gopusBytes := re.Done()
	t.Logf("Gopus first 10 bytes: %02X", gopusBytes[:minIntState(10, len(gopusBytes))])

	// Show energy info
	t.Log("")
	t.Log("=== Quantized Energies ===")
	t.Log("Band | Energy | Quantized")
	t.Log("-----+--------+----------")
	for i := 0; i < minIntState(5, len(energies)); i++ {
		q := 0.0
		if i < len(quantizedEnergies) {
			q = quantizedEnergies[i]
		}
		t.Logf("%4d | %6.2f | %9.2f", i, energies[i], q)
	}
}

func matchStrState(match bool) string {
	if match {
		return "MATCH"
	}
	return "DIFFER"
}

func minIntState(a, b int) int {
	if a < b {
		return a
	}
	return b
}
