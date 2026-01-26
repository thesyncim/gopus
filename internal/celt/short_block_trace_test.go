package celt

import (
	"fmt"
	"math"
	"testing"
)

func TestShortBlockIMDCT(t *testing.T) {
	// Test short block IMDCT (120 coefficients, 120 overlap)
	n := 120
	overlap := 120
	spectrum := make([]float64, n)
	for i := 0; i < n; i++ {
		spectrum[i] = math.Sin(float64(i)*0.1) * 0.1
	}

	// Test IMDCTDirect first
	fullIMDCT := IMDCTDirect(spectrum)
	fmt.Printf("Short block IMDCT: %d coefficients -> %d output samples\n", n, len(fullIMDCT))

	// Check output range
	var minF, maxF float64
	for _, v := range fullIMDCT {
		if v < minF {
			minF = v
		}
		if v > maxF {
			maxF = v
		}
	}
	fmt.Printf("Full IMDCT range: min=%.6f, max=%.6f\n", minF, maxF)

	// Now test imdctOverlapWithPrev with zero overlap
	prevOverlap := make([]float64, overlap)
	result := imdctOverlapWithPrev(spectrum, prevOverlap, overlap)
	fmt.Printf("imdctOverlapWithPrev: %d samples returned (expected %d)\n", len(result), n+overlap)

	// Check result range
	var minR, maxR float64
	for _, v := range result {
		if v < minR {
			minR = v
		}
		if v > maxR {
			maxR = v
		}
	}
	fmt.Printf("Result range: min=%.6f, max=%.6f\n", minR, maxR)

	// Print first 20 and last 20 samples
	fmt.Printf("\nFirst 20 samples:\n")
	for i := 0; i < 20 && i < len(result); i++ {
		fmt.Printf("  [%3d] result=%.6f full[%d]=%.6f full[%d]=%.6f\n",
			i, result[i], i, fullIMDCT[i], n+i, fullIMDCT[n+i])
	}

	fmt.Printf("\nOutput samples [%d:%d] (should be frameSize output):\n", overlap, n+overlap)
	for i := overlap; i < overlap+20 && i < len(result); i++ {
		fullIdx := i - overlap/2
		fmt.Printf("  [%3d] result=%.6f full[%d]=%.6f\n", i, result[i], fullIdx, fullIMDCT[fullIdx])
	}

	// Check newOverlap region
	fmt.Printf("\nNew overlap region [%d:%d]:\n", n, n+overlap)
	for i := n; i < n+overlap && i < len(result); i++ {
		fmt.Printf("  [%3d] = %.6f\n", i, result[i])
	}

	// Count zeros
	zeroCount := 0
	for i := n; i < len(result); i++ {
		if result[i] == 0.0 {
			zeroCount++
		}
	}
	fmt.Printf("Zeros in new overlap: %d/%d\n", zeroCount, overlap)
}

func TestTransientSynthesisTrace(t *testing.T) {
	// Simulate transient synthesis with 8 short blocks
	frameSize := 960
	shortBlocks := 8
	shortSize := frameSize / shortBlocks // 120
	overlap := 120

	// Create test coefficients
	coeffs := make([]float64, frameSize)
	for i := 0; i < frameSize; i++ {
		coeffs[i] = math.Sin(float64(i)*0.05) * 0.1
	}

	// Simulate synthesizeChannelWithOverlap for transient
	out := make([]float64, frameSize+overlap)
	// Initially, out[0:overlap] is zero (simulating zero prevOverlap)

	fmt.Printf("Transient synthesis: frameSize=%d, shortBlocks=%d, shortSize=%d\n", frameSize, shortBlocks, shortSize)

	for b := 0; b < shortBlocks; b++ {
		shortCoeffs := make([]float64, shortSize)
		for i := 0; i < shortSize; i++ {
			idx := b + i*shortBlocks
			if idx < frameSize {
				shortCoeffs[i] = coeffs[idx]
			}
		}

		// Compute RMS of shortCoeffs
		var coeffRMS float64
		for _, c := range shortCoeffs {
			coeffRMS += c * c
		}
		coeffRMS = math.Sqrt(coeffRMS / float64(len(shortCoeffs)))

		blockPrev := out[b*shortSize : b*shortSize+overlap]
		blockOut := imdctOverlapWithPrev(shortCoeffs, blockPrev, overlap)

		// Compute RMS of blockOut
		var outRMS float64
		for _, v := range blockOut {
			outRMS += v * v
		}
		outRMS = math.Sqrt(outRMS / float64(len(blockOut)))

		fmt.Printf("Block %d: coeffRMS=%.6f, outRMS=%.6f, outLen=%d\n", b, coeffRMS, outRMS, len(blockOut))

		copy(out[b*shortSize:], blockOut)
	}

	// Check final output
	output := out[:frameSize]
	var finalRMS float64
	for _, v := range output {
		finalRMS += v * v
	}
	finalRMS = math.Sqrt(finalRMS / float64(len(output)))
	fmt.Printf("\nFinal output RMS: %.6f\n", finalRMS)

	// Check overlap
	newOverlap := out[frameSize : frameSize+overlap]
	var overlapRMS float64
	for _, v := range newOverlap {
		overlapRMS += v * v
	}
	overlapRMS = math.Sqrt(overlapRMS / float64(len(newOverlap)))
	fmt.Printf("New overlap RMS: %.6f\n", overlapRMS)

	// Count zeros in output
	outputZeros := 0
	for _, v := range output {
		if v == 0.0 {
			outputZeros++
		}
	}
	fmt.Printf("Zeros in output: %d/%d (%.1f%%)\n", outputZeros, len(output), float64(outputZeros)/float64(len(output))*100)
}
