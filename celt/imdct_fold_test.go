package celt

import (
	"fmt"
	"math"
	"testing"
)

// IMDCTWithFold computes IMDCT using Direct method then folds for overlap-add
func IMDCTWithFold(spectrum []float64, prevOverlap []float64, overlap int) []float64 {
	n2 := len(spectrum)
	if n2 == 0 {
		return nil
	}

	// Get full 2N IMDCT output
	full := IMDCTDirect(spectrum)
	n := len(full) // 2*n2

	// The IMDCT output for MDCT with 50% overlap needs to be folded
	// For CELT, the folding is built into the window
	// Standard IMDCT folding formula for TDAC:
	// out[n] = full[n + N/2] for n in [0, N/2)
	// out[n] = full[n - N/2] for n in [N/2, N)
	// But this needs to be combined with windowing and overlap-add

	// Output buffer with overlap
	out := make([]float64, n2+overlap)

	// Copy prevOverlap for TDAC
	if overlap > 0 && len(prevOverlap) > 0 {
		copy(out, prevOverlap)
	}

	// Get Vorbis window
	window := GetWindowBuffer(overlap)

	// The IMDCT output is:
	// full[0..N/2-1] = "left wing" (gets windowed down)
	// full[N/2..3N/2-1] = "center" (N samples, partly windowed on edges)
	// full[3N/2..2N-1] = "right wing" (gets windowed up)

	// For overlap-add with TDAC, we need:
	// 1. Blend prevOverlap with beginning of current frame (windowed)
	// 2. Output middle portion directly
	// 3. Save end portion for next frame's overlap

	// Actually, the simplest approach is to mimic what imdctOverlapWithPrev does
	// but using the correct IMDCT values.

	// The current imdctOverlapWithPrev writes N2 samples starting at overlap/2
	// Then does TDAC on out[0:overlap]
	// So we need to place the N2 "center" samples of full[] starting at overlap/2

	// In IMDCT, for N2 MDCT coefficients:
	// - full has 2*N2 samples (e.g., 1920)
	// - The "useful" center is full[N2/2 : N2/2 + N2] = full[480:1440]
	// Wait, that's not right either...

	// Let me think about this more carefully.
	// MDCT takes 2N samples with 50% overlap and produces N coefficients
	// IMDCT takes N coefficients and produces 2N samples
	// With overlap-add, consecutive frames overlap by N samples (50%)

	// For CELT with N=960 coefficients and overlap=120:
	// - IMDCT produces 1920 samples
	// - Frames overlap by 120 samples
	// - Output per frame is 960 samples

	// The windowing/folding used in CELT (Vorbis window):
	// - First overlap/2 samples: window down (multiply by 0 to 1)
	// - Middle samples: no windowing
	// - Last overlap/2 samples: window up (multiply by 1 to 0)

	// Let me just compute the center N samples of the 2N IMDCT output
	// and handle overlap manually

	// Center N samples are full[N/2:3N/2] = full[480:1440] for N=960
	start := n / 4 // N/4 = 480
	end := start + n2

	// Place center samples at out[overlap/2:]
	copy(out[overlap/2:], full[start:end])

	// TDAC windowing on first overlap samples
	if overlap > 0 {
		xp1 := overlap - 1
		yp1 := 0
		wp1 := 0
		wp2 := overlap - 1

		for i := 0; i < overlap/2; i++ {
			x1 := out[xp1]
			x2 := out[yp1]
			out[yp1] = x2*window[wp2] - x1*window[wp1]
			out[xp1] = x2*window[wp1] + x1*window[wp2]
			yp1++
			xp1--
			wp1++
			wp2--
		}
	}

	return out
}

func TestIMDCTWithFold(t *testing.T) {
	n2 := 960
	spectrum := make([]float64, n2)
	spectrum[0] = 1.0 // DC impulse

	overlap := 120
	prevOverlap := make([]float64, overlap)

	// Test with folded Direct IMDCT
	result := IMDCTWithFold(spectrum, prevOverlap, overlap)

	fmt.Println("=== IMDCTWithFold (using Direct IMDCT center) ===")
	fmt.Printf("Output length: %d\n", len(result))
	fmt.Printf("First 10: ")
	for i := 0; i < 10; i++ {
		fmt.Printf("%.6f ", result[i])
	}
	fmt.Printf("\n[950:960]: ")
	for i := 950; i < 960; i++ {
		fmt.Printf("%.6f ", result[i])
	}
	fmt.Printf("\nNew overlap [0:10]: ")
	for i := 960; i < 970; i++ {
		fmt.Printf("%.6f ", result[i])
	}
	fmt.Printf("\nNew overlap [50:60]: ")
	for i := 1010; i < 1020 && i < len(result); i++ {
		fmt.Printf("%.6f ", result[i])
	}
	fmt.Println()

	// Compare with current DFT-based
	dftResult := imdctOverlapWithPrev(spectrum, prevOverlap, overlap)
	fmt.Println("\n=== Current DFT-based IMDCT ===")
	fmt.Printf("First 10: ")
	for i := 0; i < 10; i++ {
		fmt.Printf("%.6f ", dftResult[i])
	}
	fmt.Printf("\n[950:960]: ")
	for i := 950; i < 960; i++ {
		fmt.Printf("%.6f ", dftResult[i])
	}
	fmt.Printf("\nNew overlap [0:10]: ")
	for i := 960; i < 970; i++ {
		fmt.Printf("%.6f ", dftResult[i])
	}
	fmt.Printf("\nNew overlap [50:60]: ")
	for i := 1010; i < 1020 && i < len(dftResult); i++ {
		fmt.Printf("%.6f ", dftResult[i])
	}
	fmt.Println()

	// Check energy ratios
	var foldEnergy, dftEnergy float64
	for i := 0; i < n2; i++ {
		foldEnergy += result[i] * result[i]
		dftEnergy += dftResult[i] * dftResult[i]
	}
	fmt.Printf("\nEnergy (first N2): fold=%.6f, dft=%.6f, ratio=%.2f\n",
		foldEnergy, dftEnergy, dftEnergy/foldEnergy)

	// Check overlap region magnitude
	var foldOverlapMax, dftOverlapMax float64
	for i := n2; i < n2+overlap; i++ {
		if math.Abs(result[i]) > foldOverlapMax {
			foldOverlapMax = math.Abs(result[i])
		}
		if math.Abs(dftResult[i]) > dftOverlapMax {
			dftOverlapMax = math.Abs(dftResult[i])
		}
	}
	fmt.Printf("Overlap max magnitude: fold=%.6f, dft=%.6f\n", foldOverlapMax, dftOverlapMax)
}

func TestIMDCTWithFoldRealistic(t *testing.T) {
	n2 := 960
	spectrum := make([]float64, n2)
	// Realistic spectrum (exponential decay)
	for i := 0; i < n2; i++ {
		spectrum[i] = math.Sin(float64(i)*0.3) * math.Exp(-float64(i)/200.0)
	}

	overlap := 120
	prevOverlap := make([]float64, overlap)

	foldResult := IMDCTWithFold(spectrum, prevOverlap, overlap)
	dftResult := imdctOverlapWithPrev(spectrum, prevOverlap, overlap)

	fmt.Println("\n=== Realistic spectrum comparison ===")

	// Check first and last magnitudes
	var foldFirst, foldLast, dftFirst, dftLast float64
	for i := 0; i < 10; i++ {
		foldFirst += math.Abs(foldResult[i])
		foldLast += math.Abs(foldResult[n2-10+i])
		dftFirst += math.Abs(dftResult[i])
		dftLast += math.Abs(dftResult[n2-10+i])
	}
	foldFirst /= 10
	foldLast /= 10
	dftFirst /= 10
	dftLast /= 10

	fmt.Printf("Fold: first avg=%.6f, last avg=%.6f, ratio=%.2f\n",
		foldFirst, foldLast, foldLast/foldFirst)
	fmt.Printf("DFT:  first avg=%.6f, last avg=%.6f, ratio=%.2f\n",
		dftFirst, dftLast, dftLast/dftFirst)
}
