package celt

import (
	"fmt"
	"math"
	"testing"
)

func TestIMDCTSequence(t *testing.T) {
	// Test multiple frames to see if overlap-add corrects errors
	n2 := 960
	overlap := 120
	numFrames := 5

	// Create consistent spectrum for all frames
	spectrum := make([]float64, n2)
	spectrum[0] = 1.0 // DC impulse

	// Track overlap buffers
	dftOverlap := make([]float64, overlap)
	scaledOverlap := make([]float64, overlap)

	fmt.Println("=== Multi-frame IMDCT sequence (DC impulse) ===")

	for frame := 0; frame < numFrames; frame++ {
		dftResult := imdctOverlapWithPrev(spectrum, dftOverlap, overlap)
		scaledResult := imdctOverlapWithPrevScaled(spectrum, scaledOverlap, overlap)

		// Extract output and new overlap
		dftOut := dftResult[:n2]
		scaledOut := scaledResult[:n2]
		copy(dftOverlap, dftResult[n2:n2+overlap])
		copy(scaledOverlap, scaledResult[n2:n2+overlap])

		// Compute RMS of output
		var dftRMS, scaledRMS float64
		for i := 0; i < n2; i++ {
			dftRMS += dftOut[i] * dftOut[i]
			scaledRMS += scaledOut[i] * scaledOut[i]
		}
		dftRMS = math.Sqrt(dftRMS / float64(n2))
		scaledRMS = math.Sqrt(scaledRMS / float64(n2))

		// Compute RMS of overlap
		var dftOvRMS, scaledOvRMS float64
		for i := 0; i < overlap; i++ {
			dftOvRMS += dftOverlap[i] * dftOverlap[i]
			scaledOvRMS += scaledOverlap[i] * scaledOverlap[i]
		}
		dftOvRMS = math.Sqrt(dftOvRMS / float64(overlap))
		scaledOvRMS = math.Sqrt(scaledOvRMS / float64(overlap))

		fmt.Printf("Frame %d: dft out RMS=%.4f, overlap RMS=%.4f | scaled out RMS=%.4f, overlap RMS=%.4f\n",
			frame, dftRMS, dftOvRMS, scaledRMS, scaledOvRMS)
	}
}

func TestIMDCTSequenceRealistic(t *testing.T) {
	// Test with varying spectra like real audio
	n2 := 960
	overlap := 120
	numFrames := 10

	dftOverlap := make([]float64, overlap)
	scaledOverlap := make([]float64, overlap)

	fmt.Println("\n=== Multi-frame IMDCT sequence (varying realistic spectra) ===")

	for frame := 0; frame < numFrames; frame++ {
		// Create varying spectrum for each frame
		spectrum := make([]float64, n2)
		for i := 0; i < n2; i++ {
			// Different pattern each frame
			phase := float64(frame) * 0.5
			spectrum[i] = math.Sin(float64(i)*0.1+phase) * math.Exp(-float64(i)/300.0)
		}

		dftResult := imdctOverlapWithPrev(spectrum, dftOverlap, overlap)
		scaledResult := imdctOverlapWithPrevScaled(spectrum, scaledOverlap, overlap)

		dftOut := dftResult[:n2]
		scaledOut := scaledResult[:n2]
		copy(dftOverlap, dftResult[n2:n2+overlap])
		copy(scaledOverlap, scaledResult[n2:n2+overlap])

		// Check for NaN/Inf
		hasNaN := false
		for _, v := range dftOut {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				hasNaN = true
				break
			}
		}

		// Compute max value
		var dftMax, scaledMax float64
		for i := 0; i < n2; i++ {
			if math.Abs(dftOut[i]) > dftMax {
				dftMax = math.Abs(dftOut[i])
			}
			if math.Abs(scaledOut[i]) > scaledMax {
				scaledMax = math.Abs(scaledOut[i])
			}
		}

		var dftOvMax, scaledOvMax float64
		for i := 0; i < overlap; i++ {
			if math.Abs(dftOverlap[i]) > dftOvMax {
				dftOvMax = math.Abs(dftOverlap[i])
			}
			if math.Abs(scaledOverlap[i]) > scaledOvMax {
				scaledOvMax = math.Abs(scaledOverlap[i])
			}
		}

		fmt.Printf("Frame %d: dft max=%.2f, ov=%.2f%s | scaled max=%.4f, ov=%.4f\n",
			frame, dftMax, dftOvMax,
			func() string {
				if hasNaN {
					return " NaN!"
				}
				return ""
			}(),
			scaledMax, scaledOvMax)
	}
}
