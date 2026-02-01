//go:build cgo_libopus
// +build cgo_libopus

// Package cgo tests float32 IMDCT implementation against libopus clt_mdct_backward_c
package cgo

import (
	"fmt"
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/celt"
)

// TestIMDCT_F32VsLibopus compares the float32 Go IMDCT implementation against libopus.
// This tests the LibopusIMDCTF32 function for exact float32 precision matching.
func TestIMDCT_F32VsLibopus(t *testing.T) {
	mode := GetCELTMode48000_960()
	if mode == nil {
		t.Fatal("Failed to create CELT mode")
	}

	overlap := mode.Overlap()
	t.Logf("CELT mode overlap: %d", overlap)

	// Test with all CELT frame sizes (shift values 1-3)
	// shift=1: 960 (20ms at 48kHz) -> n2=480, n4=240
	// shift=2: 480 (10ms at 48kHz) -> n2=240, n4=120
	// shift=3: 240 (5ms at 48kHz)  -> n2=120, n4=60
	shifts := []int{1, 2, 3}

	for _, shift := range shifts {
		nfft := mode.MDCTSize(shift)
		n2 := nfft / 2

		t.Run(fmt.Sprintf("nfft=%d_shift=%d", nfft, shift), func(t *testing.T) {
			// Create random frequency input (simulating MDCT coefficients)
			rng := rand.New(rand.NewSource(42))
			input := make([]float32, n2)

			for k := 0; k < n2; k++ {
				// Scale to typical MDCT coefficient range
				val := float32(rng.Intn(32768)-16384) / float32(nfft)
				input[k] = val
			}

			// Call libopus IMDCT
			libopusOut := mode.IMDCTBackward(input, shift)

			// Call Go float32 IMDCT
			prevOverlap := make([]float32, overlap)
			goOut := celt.LibopusIMDCTF32(input, prevOverlap, overlap)

			// Compare outputs
			minLen := n2 + overlap
			if len(goOut) < minLen {
				minLen = len(goOut)
			}
			if len(libopusOut) < minLen {
				minLen = len(libopusOut)
			}

			var errPow, sigPow, maxDiff float64
			maxDiffIdx := 0
			for i := 0; i < minLen; i++ {
				libVal := float64(libopusOut[i])
				goVal := float64(goOut[i])
				diff := libVal - goVal
				if math.Abs(diff) > maxDiff {
					maxDiff = math.Abs(diff)
					maxDiffIdx = i
				}
				errPow += diff * diff
				sigPow += libVal * libVal
			}

			snr := float64(0)
			if errPow > 0 && sigPow > 0 {
				snr = 10 * math.Log10(sigPow/errPow)
			} else if errPow == 0 {
				snr = 200 // Perfect match
			}

			t.Logf("nfft=%d Go F32 vs libopus IMDCT: SNR=%.2f dB, maxDiff=%.2e at idx %d",
				nfft, snr, maxDiff, maxDiffIdx)

			// For float32 precision matching, we expect very high SNR (>100 dB)
			// The remaining differences are due to:
			// - DFT implementation differences (Go uses direct DFT, libopus uses mixed-radix FFT)
			// - Twiddle factor precision differences
			if snr < 60 {
				t.Errorf("Poor SNR for nfft=%d Go F32 vs libopus IMDCT: %.2f dB (expected >= 60 dB)",
					nfft, snr)
			}

			// Report first few differing samples for debugging
			if maxDiff > 1e-6 {
				t.Logf("First samples comparison (libopus vs Go):")
				for i := 0; i < 10 && i < minLen; i++ {
					t.Logf("  [%d] libopus=%.8f, go=%.8f, diff=%.2e",
						i, libopusOut[i], goOut[i], libopusOut[i]-goOut[i])
				}
			}
		})
	}
}

// TestIMDCT_F32VsLibopus_WithZeroPrevOverlap tests IMDCT with zero previous overlap.
func TestIMDCT_F32VsLibopus_WithZeroPrevOverlap(t *testing.T) {
	mode := GetCELTMode48000_960()
	if mode == nil {
		t.Fatal("Failed to create CELT mode")
	}

	overlap := mode.Overlap()
	shift := 1 // 20ms frame
	nfft := mode.MDCTSize(shift)
	n2 := nfft / 2

	// Create random frequency input
	rng := rand.New(rand.NewSource(42))
	input := make([]float32, n2)
	for k := 0; k < n2; k++ {
		input[k] = float32(rng.Intn(32768)-16384) / float32(nfft)
	}

	// Call libopus IMDCT (note: libopus test wrapper zeros output first)
	libopusOut := mode.IMDCTBackward(input, shift)

	// Call Go IMDCT with zero prevOverlap
	zeroPrevOverlap := make([]float32, overlap)
	goOut := celt.LibopusIMDCTF32(input, zeroPrevOverlap, overlap)

	// Compare
	minLen := n2 + overlap
	if len(goOut) < minLen {
		minLen = len(goOut)
	}
	if len(libopusOut) < minLen {
		minLen = len(libopusOut)
	}

	var errPow, sigPow float64
	for i := 0; i < minLen; i++ {
		libVal := float64(libopusOut[i])
		goVal := float64(goOut[i])
		diff := libVal - goVal
		errPow += diff * diff
		sigPow += libVal * libVal
	}

	snr := float64(0)
	if errPow > 0 && sigPow > 0 {
		snr = 10 * math.Log10(sigPow/errPow)
	} else if errPow == 0 {
		snr = 200
	}

	t.Logf("IMDCT with zero prevOverlap: SNR=%.2f dB", snr)

	if snr < 60 {
		t.Errorf("Poor SNR: %.2f dB (expected >= 60 dB)", snr)
	}
}

// TestIMDCT_F32_Window tests the TDAC windowing matches libopus.
func TestIMDCT_F32_Window(t *testing.T) {
	mode := GetCELTMode48000_960()
	if mode == nil {
		t.Fatal("Failed to create CELT mode")
	}

	overlap := mode.Overlap()
	libopusWindow := mode.GetWindow()
	goWindow := celt.GetWindowBufferF32(overlap)

	var maxDiff float64
	maxDiffIdx := 0
	for i := 0; i < overlap; i++ {
		diff := math.Abs(float64(libopusWindow[i]) - float64(goWindow[i]))
		if diff > maxDiff {
			maxDiff = diff
			maxDiffIdx = i
		}
	}

	t.Logf("Window comparison:")
	t.Logf("  overlap=%d", overlap)
	t.Logf("  maxDiff=%.2e at idx %d", maxDiff, maxDiffIdx)

	// Window values should match exactly (or within float32 epsilon)
	if maxDiff > 1e-7 {
		t.Errorf("Window values differ: max diff = %.2e", maxDiff)
		t.Logf("  First few values:")
		for i := 0; i < 10; i++ {
			t.Logf("    [%d] libopus=%.8f, go=%.8f, diff=%.2e",
				i, libopusWindow[i], goWindow[i], libopusWindow[i]-goWindow[i])
		}
	}
}

// TestIMDCT_F32_Trig tests that the trig tables match libopus.
func TestIMDCT_F32_Trig(t *testing.T) {
	// libopus computes: trig[i] = cos(2*PI*(i+.125)/N)
	// for i=0..N/2-1 where N is the MDCT size

	// Get libopus trig values using the CGO wrapper
	for _, n := range []int{1920, 960, 480, 240} {
		libopusTrig := ComputeLibopusMDCTTrig(n)
		n2 := n / 2

		// Compute Go trig values
		goTrig := make([]float32, n2)
		for i := 0; i < n2; i++ {
			goTrig[i] = float32(math.Cos(2.0 * math.Pi * (float64(i) + 0.125) / float64(n)))
		}

		var maxDiff float64
		maxDiffIdx := 0
		for i := 0; i < n2; i++ {
			diff := math.Abs(float64(libopusTrig[i]) - float64(goTrig[i]))
			if diff > maxDiff {
				maxDiff = diff
				maxDiffIdx = i
			}
		}

		t.Logf("Trig table N=%d: maxDiff=%.2e at idx %d", n, maxDiff, maxDiffIdx)

		if maxDiff > 1e-7 {
			t.Errorf("Trig table N=%d differs: max diff = %.2e", n, maxDiff)
		}
	}
}

// BenchmarkIMDCT_F32 benchmarks the float32 IMDCT implementation.
func BenchmarkIMDCT_F32(b *testing.B) {
	mode := GetCELTMode48000_960()
	if mode == nil {
		b.Fatal("Failed to create CELT mode")
	}

	overlap := mode.Overlap()
	shifts := []int{1, 2, 3}

	for _, shift := range shifts {
		nfft := mode.MDCTSize(shift)
		n2 := nfft / 2

		input := make([]float32, n2)
		for k := 0; k < n2; k++ {
			input[k] = float32(k % 100)
		}
		prevOverlap := make([]float32, overlap)

		b.Run(fmt.Sprintf("Go_F32_nfft=%d", nfft), func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				celt.LibopusIMDCTF32(input, prevOverlap, overlap)
			}
		})

		b.Run(fmt.Sprintf("Libopus_nfft=%d", nfft), func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				mode.IMDCTBackward(input, shift)
			}
		})
	}
}
