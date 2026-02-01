//go:build cgo_libopus
// +build cgo_libopus

// Package cgo compares band 0 MDCT coefficients between gopus and libopus.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
)

// TestBand0MDCTCoefficients compares the actual MDCT coefficients in band 0
// between gopus and libopus to understand the energy difference.
func TestBand0MDCTCoefficients(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	overlap := celt.Overlap // 120
	shortBlocks := 8

	// Generate 440Hz sine wave
	pcm := make([]float64, frameSize)
	pcmF32 := make([]float32, frameSize)
	for i := range pcm {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm[i] = val
		pcmF32[i] = float32(val)
	}

	t.Log("=== Band 0 MDCT Coefficient Comparison ===")
	t.Log("")

	// Gopus: Apply pre-emphasis
	enc := celt.NewEncoder(1)
	enc.Reset()
	gopusPreemph := enc.ApplyPreemphasisWithScaling(pcm)

	// Build MDCT input: zeros (history) + pre-emphasized signal
	gopusInput := make([]float64, frameSize+overlap)
	copy(gopusInput[overlap:], gopusPreemph)

	// Compute gopus MDCT
	gopusMDCT := celt.ComputeMDCTWithHistory(gopusPreemph, enc.OverlapBuffer(), shortBlocks)

	t.Log("=== Gopus Band 0 MDCT Coefficients (Short Blocks) ===")
	t.Log("Band 0 covers bins 0-7 (interleaved across 8 short blocks)")
	t.Log("")

	// For transient mode with 8 short blocks, band 0 uses:
	// - bin 0 from each short block (8 values total, interleaved)
	// Interleaving: coefficient i from block b is at output[b + i*shortBlocks]
	// So bin 0 from block 0 is at output[0], bin 0 from block 1 is at output[1], etc.

	t.Log("Block | Bin 0 Coeff")
	t.Log("------+-----------")
	var gopusBand0SumSq float64
	for b := 0; b < shortBlocks; b++ {
		// Bin 0 from block b is at index b (since i=0, output index = b + 0*shortBlocks = b)
		coeff := gopusMDCT[b]
		gopusBand0SumSq += coeff * coeff
		t.Logf("%5d | %10.4f", b, coeff)
	}

	t.Log("")
	t.Logf("Gopus band 0 sum of squares: %f", gopusBand0SumSq)
	gopusBand0Energy := 0.5 * math.Log2(gopusBand0SumSq+1e-27)
	t.Logf("Gopus band 0 energy (log2 of sqrt): %f", gopusBand0Energy)

	// Now let's check what libopus produces using our MDCT wrapper
	libPreemph := ApplyLibopusPreemphasis(pcmF32, 0.85)

	// Build libopus input
	libInput := make([]float32, frameSize+overlap)
	for i := 0; i < overlap; i++ {
		libInput[i] = 0 // History zeros
	}
	for i := 0; i < frameSize; i++ {
		libInput[overlap+i] = libPreemph[i]
	}

	mode := GetCELTMode48000_960()
	if mode == nil {
		t.Fatal("Failed to create CELT mode")
	}

	// For short blocks, shift=3
	shift := 3

	t.Log("")
	t.Log("=== Libopus Band 0 MDCT Coefficients (Short Blocks) ===")
	t.Log("Block | Bin 0 Coeff")
	t.Log("------+-----------")

	shortSize := frameSize / shortBlocks // 120
	var libBand0SumSq float64

	for b := 0; b < shortBlocks; b++ {
		blockStart := b * shortSize
		libBlockInput := make([]float32, shortSize+overlap)
		for i := 0; i < shortSize+overlap && blockStart+i < len(libInput); i++ {
			libBlockInput[i] = libInput[blockStart+i]
		}
		libMDCT := mode.MDCTForward(libBlockInput, shift)
		if len(libMDCT) > 0 {
			coeff := float64(libMDCT[0]) // Bin 0
			libBand0SumSq += coeff * coeff
			t.Logf("%5d | %10.4f", b, coeff)
		}
	}

	t.Log("")
	t.Logf("Libopus band 0 sum of squares: %f", libBand0SumSq)
	libBand0Energy := 0.5 * math.Log2(libBand0SumSq+1e-27)
	t.Logf("Libopus band 0 energy (log2 of sqrt): %f", libBand0Energy)

	t.Log("")
	t.Log("=== Comparison ===")
	energyDiff := gopusBand0Energy - libBand0Energy
	t.Logf("Energy difference: %f (%.1f dB)", energyDiff, energyDiff*6)

	// Now let's check what happens with the eMeans subtraction
	eMeansBand0 := celt.GetEMeans()[0]
	t.Logf("eMeans[0] = %f", eMeansBand0)
	t.Logf("Gopus band 0 energy (mean-relative): %f", gopusBand0Energy-eMeansBand0)
	t.Logf("Libopus band 0 energy (mean-relative): %f", libBand0Energy-eMeansBand0)

	t.Log("")
	t.Log("=== Pre-emphasis Input Comparison ===")
	t.Log("First 10 pre-emphasized samples:")
	t.Log("Index | Gopus       | Libopus     | Diff")
	t.Log("------+-------------+-------------+---------")
	for i := 0; i < 10 && i < len(gopusPreemph) && i < len(libPreemph); i++ {
		diff := gopusPreemph[i] - float64(libPreemph[i])
		t.Logf("%5d | %11.4f | %11.4f | %8.4f", i, gopusPreemph[i], libPreemph[i], diff)
	}

	t.Log("")
	t.Log("Last 10 pre-emphasized samples:")
	for i := len(gopusPreemph) - 10; i < len(gopusPreemph) && i >= 0; i++ {
		diff := gopusPreemph[i] - float64(libPreemph[i])
		t.Logf("%5d | %11.4f | %11.4f | %8.4f", i, gopusPreemph[i], libPreemph[i], diff)
	}

	// Check the overlap buffer
	t.Log("")
	t.Log("=== Overlap Buffer (History) ===")
	overlapBuf := enc.OverlapBuffer()
	t.Logf("Gopus overlap buffer length: %d", len(overlapBuf))
	if len(overlapBuf) > 0 {
		nonZero := 0
		for _, v := range overlapBuf {
			if v != 0 {
				nonZero++
			}
		}
		t.Logf("Non-zero values in overlap buffer: %d", nonZero)
	}
}

// TestBand0WithDifferentSignals tests band 0 energy with various signals.
func TestBand0WithDifferentSignals(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	shortBlocks := 8

	signals := []struct {
		name string
		gen  func(int) []float64
	}{
		{"440Hz sine", func(n int) []float64 {
			pcm := make([]float64, n)
			for i := range pcm {
				ti := float64(i) / float64(sampleRate)
				pcm[i] = 0.5 * math.Sin(2*math.Pi*440*ti)
			}
			return pcm
		}},
		{"100Hz sine", func(n int) []float64 {
			pcm := make([]float64, n)
			for i := range pcm {
				ti := float64(i) / float64(sampleRate)
				pcm[i] = 0.5 * math.Sin(2*math.Pi*100*ti)
			}
			return pcm
		}},
		{"1000Hz sine", func(n int) []float64 {
			pcm := make([]float64, n)
			for i := range pcm {
				ti := float64(i) / float64(sampleRate)
				pcm[i] = 0.5 * math.Sin(2*math.Pi*1000*ti)
			}
			return pcm
		}},
		{"silence", func(n int) []float64 {
			return make([]float64, n)
		}},
		{"ramp", func(n int) []float64 {
			pcm := make([]float64, n)
			for i := range pcm {
				pcm[i] = float64(i) / float64(n)
			}
			return pcm
		}},
	}

	t.Log("=== Band 0 Energy for Different Signals ===")
	t.Log("Signal       | Gopus E | Lib E  | Diff")
	t.Log("-------------+---------+--------+------")

	for _, sig := range signals {
		pcm := sig.gen(frameSize)
		pcmF32 := make([]float32, frameSize)
		for i, v := range pcm {
			pcmF32[i] = float32(v)
		}

		// Gopus
		enc := celt.NewEncoder(1)
		enc.Reset()
		gopusPreemph := enc.ApplyPreemphasisWithScaling(pcm)
		gopusMDCT := celt.ComputeMDCTWithHistory(gopusPreemph, enc.OverlapBuffer(), shortBlocks)

		var gopusBand0SumSq float64
		for b := 0; b < shortBlocks; b++ {
			coeff := gopusMDCT[b]
			gopusBand0SumSq += coeff * coeff
		}
		gopusBand0E := 0.5 * math.Log2(gopusBand0SumSq+1e-27)

		// Libopus
		libPreemph := ApplyLibopusPreemphasis(pcmF32, 0.85)
		libInput := make([]float32, frameSize+celt.Overlap)
		for i := 0; i < len(libPreemph); i++ {
			libInput[celt.Overlap+i] = libPreemph[i]
		}

		mode := GetCELTMode48000_960()
		shortSize := frameSize / shortBlocks
		shift := 3

		var libBand0SumSq float64
		for b := 0; b < shortBlocks; b++ {
			blockStart := b * shortSize
			libBlockInput := make([]float32, shortSize+celt.Overlap)
			for i := 0; i < shortSize+celt.Overlap && blockStart+i < len(libInput); i++ {
				libBlockInput[i] = libInput[blockStart+i]
			}
			libMDCT := mode.MDCTForward(libBlockInput, shift)
			if len(libMDCT) > 0 {
				coeff := float64(libMDCT[0])
				libBand0SumSq += coeff * coeff
			}
		}
		libBand0E := 0.5 * math.Log2(libBand0SumSq+1e-27)

		diff := gopusBand0E - libBand0E
		t.Logf("%-12s | %7.2f | %6.2f | %5.2f", sig.name, gopusBand0E, libBand0E, diff)
	}
}
