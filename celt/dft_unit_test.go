// Package celt provides tests for DFT (Discrete Fourier Transform) operations.
// This file is a port of libopus celt/tests/test_unit_dft.c
//
// Copyright (c) 2008 Xiph.Org Foundation
// Written by Jean-Marc Valin
// Go port: 2024
//
// Redistribution and use in source and binary forms, with or without
// modification, are permitted provided that the following conditions
// are met:
//
// - Redistributions of source code must retain the above copyright
//   notice, this list of conditions and the following disclaimer.
//
// - Redistributions in binary form must reproduce the above copyright
//   notice, this list of conditions and the following disclaimer in the
//   documentation and/or other materials provided with the distribution.
//
// THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
// ``AS IS'' AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
// LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
// A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT OWNER
// OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL,
// EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO,
// PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR
// PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF
// LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING
// NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS
// SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

package celt

import (
	"math"
	"math/rand"
	"testing"
)

// checkDFT verifies DFT/IDFT accuracy by computing the expected result via
// direct DFT formula and comparing with the actual output.
// Returns the SNR in dB.
//
// Note: This is adapted from libopus test_unit_dft.c check() function.
// The normalization convention in this test matches our Go implementation:
// - Forward FFT: output = sum (no 1/N normalization)
// - Inverse FFT: output = sum / N (with 1/N normalization)
func checkDFT(t *testing.T, in, out []complex128, nfft int, isInverse bool) float64 {
	var errpow, sigpow float64

	for bin := 0; bin < nfft; bin++ {
		var ansr, ansi float64

		for k := 0; k < nfft; k++ {
			phase := -2.0 * math.Pi * float64(bin) * float64(k) / float64(nfft)
			re := math.Cos(phase)
			im := math.Sin(phase)
			if isInverse {
				im = -im
			}

			// Our Go FFT convention:
			// - Forward: no normalization (output = sum)
			// - Inverse: normalized by 1/N
			if isInverse {
				re /= float64(nfft)
				im /= float64(nfft)
			}

			ansr += real(in[k])*re - imag(in[k])*im
			ansi += real(in[k])*im + imag(in[k])*re
		}

		difr := ansr - real(out[bin])
		difi := ansi - imag(out[bin])
		errpow += difr*difr + difi*difi
		sigpow += ansr*ansr + ansi*ansi
	}

	snr := 10.0 * math.Log10(sigpow/errpow)
	return snr
}

// test1d tests forward or inverse FFT for a given size.
// It generates random input, applies the transform, and checks the result
// against a reference DFT implementation.
func test1d(t *testing.T, nfft int, isInverse bool) {
	// Generate random input with same distribution as C test
	rng := rand.New(rand.NewSource(42)) // Fixed seed for reproducibility
	in := make([]complex128, nfft)
	for k := 0; k < nfft; k++ {
		r := float64((rng.Intn(32767)) - 16384)
		i := float64((rng.Intn(32767)) - 16384)
		in[k] = complex(r, i)
	}

	// Scale by 32768 as in C test
	for k := 0; k < nfft; k++ {
		in[k] = complex(real(in[k])*32768, imag(in[k])*32768)
	}

	// For inverse, pre-divide by nfft
	if isInverse {
		for k := 0; k < nfft; k++ {
			in[k] = complex(real(in[k])/float64(nfft), imag(in[k])/float64(nfft))
		}
	}

	// Compute FFT/IFFT
	var out []complex128
	if isInverse {
		out = ifft(in)
	} else {
		out = fft(in)
	}

	// Check result
	snr := checkDFT(t, in, out, nfft, isInverse)

	inverseStr := "forward"
	if isInverse {
		inverseStr = "inverse"
	}

	t.Logf("nfft=%d %s, snr = %.2f dB", nfft, inverseStr, snr)

	if snr < 60 {
		t.Errorf("Poor SNR for nfft=%d %s: %.2f dB (expected >= 60 dB)", nfft, inverseStr, snr)
	}
}

// TestDFTUnit tests the FFT/IFFT implementation for various sizes.
// This is a port of libopus celt/tests/test_unit_dft.c
func TestDFTUnit(t *testing.T) {
	// Power-of-2 sizes (always tested in libopus)
	// These use the fast FFT radix-2 implementation
	t.Run("PowerOfTwo", func(t *testing.T) {
		sizes := []int{32, 128, 256}
		for _, size := range sizes {
			t.Run("", func(t *testing.T) {
				test1d(t, size, false) // Forward FFT
				test1d(t, size, true)  // Inverse FFT
			})
		}
	})

	// Non-power-of-2 sizes (tested when RADIX_TWO_ONLY is not defined)
	// These sizes are used in CELT for custom modes and standard Opus
	// The libopus C test includes: 36, 50, 60, 120, 240, 480
	t.Run("NonPowerOfTwo", func(t *testing.T) {
		// Mixed-radix sizes from libopus test
		sizes := []int{36, 50, 60, 120, 240, 480}
		for _, size := range sizes {
			t.Run("", func(t *testing.T) {
				testDFT1d(t, size, false) // Forward DFT
				testDFT1d(t, size, true)  // Inverse DFT
			})
		}
	})
}

// testDFT1d tests the direct DFT implementation for non-power-of-2 sizes.
func testDFT1d(t *testing.T, nfft int, isInverse bool) {
	// Generate random input with same distribution as C test
	rng := rand.New(rand.NewSource(42)) // Fixed seed for reproducibility
	in := make([]complex128, nfft)
	for k := 0; k < nfft; k++ {
		r := float64((rng.Intn(32767)) - 16384)
		i := float64((rng.Intn(32767)) - 16384)
		in[k] = complex(r, i)
	}

	// Scale by 32768 as in C test
	for k := 0; k < nfft; k++ {
		in[k] = complex(real(in[k])*32768, imag(in[k])*32768)
	}

	// For inverse, pre-divide by nfft
	if isInverse {
		for k := 0; k < nfft; k++ {
			in[k] = complex(real(in[k])/float64(nfft), imag(in[k])/float64(nfft))
		}
	}

	// Compute DFT/IDFT
	var out []complex128
	if isInverse {
		out = idftComplex(in)
	} else {
		out = dft(in)
	}

	// Check result
	snr := checkDFT(t, in, out, nfft, isInverse)

	inverseStr := "forward"
	if isInverse {
		inverseStr = "inverse"
	}

	t.Logf("nfft=%d %s (DFT), snr = %.2f dB", nfft, inverseStr, snr)

	if snr < 60 {
		t.Errorf("Poor SNR for nfft=%d %s: %.2f dB (expected >= 60 dB)", nfft, inverseStr, snr)
	}
}

// idftComplex computes inverse DFT for complex input with 1/N normalization.
// This matches the convention of our Go ifft function.
func idftComplex(x []complex128) []complex128 {
	n := len(x)
	if n <= 1 {
		return x
	}

	out := make([]complex128, n)
	twoPi := 2.0 * math.Pi / float64(n)
	scale := 1.0 / float64(n)
	for k := 0; k < n; k++ {
		angle := twoPi * float64(k)
		wStep := complex(math.Cos(angle), math.Sin(angle))
		w := complex(1.0, 0.0)
		var sum complex128
		for t := 0; t < n; t++ {
			sum += x[t] * w
			w *= wStep
		}
		out[k] = sum * complex(scale, 0)
	}
	return out
}

// transformRoundtripTest is a parameterized test helper for FFT/DFT roundtrip verification.
// It takes forward and inverse transform functions and verifies that applying both
// returns the original signal within the specified error tolerance.
func transformRoundtripTest(t *testing.T, sizes []int, forward, inverse func([]complex128) []complex128, name string) {
	for _, n := range sizes {
		t.Run("", func(t *testing.T) {
			rng := rand.New(rand.NewSource(12345))
			original := make([]complex128, n)
			for i := 0; i < n; i++ {
				original[i] = complex(rng.Float64()*2-1, rng.Float64()*2-1)
			}

			// Forward then inverse
			forwardResult := forward(original)
			back := inverse(forwardResult)

			// Check roundtrip accuracy
			var maxErr float64
			for i := 0; i < n; i++ {
				errReal := math.Abs(real(back[i]) - real(original[i]))
				errImag := math.Abs(imag(back[i]) - imag(original[i]))
				if errReal > maxErr {
					maxErr = errReal
				}
				if errImag > maxErr {
					maxErr = errImag
				}
			}

			t.Logf("n=%d (%s), max roundtrip error = %.2e", n, name, maxErr)
			if maxErr > 1e-10 {
				t.Errorf("n=%d (%s): roundtrip error too large: %.2e", n, name, maxErr)
			}
		})
	}
}

// TestFFTRoundtrip verifies that FFT followed by IFFT returns the original signal.
func TestFFTRoundtrip(t *testing.T) {
	sizes := []int{8, 16, 32, 64, 128, 256, 512}
	transformRoundtripTest(t, sizes, fft, ifft, "FFT")
}

// TestDFTSpecificSizes tests DFT for sizes commonly used in CELT/Opus.
// These correspond to the FFT sizes used in the MDCT for various frame sizes.
func TestDFTSpecificSizes(t *testing.T) {
	// CELT mode sizes: 48000 Hz, 960 samples per frame
	// mdct.kfft sizes: 480, 240, 120, 60
	celtSizes := []int{480, 240, 120, 60}

	for _, n := range celtSizes {
		t.Run("", func(t *testing.T) {
			rng := rand.New(rand.NewSource(999))
			in := make([]complex128, n)
			for i := 0; i < n; i++ {
				in[i] = complex(rng.Float64()*20000-10000, rng.Float64()*20000-10000)
			}

			// Compute using our DFT
			out := dft(in)

			// Verify against reference
			snr := checkDFT(t, in, out, n, false)
			t.Logf("CELT DFT size=%d, snr = %.2f dB", n, snr)

			if snr < 100 { // DFT should have very high precision
				t.Errorf("DFT size=%d: SNR too low: %.2f dB (expected >= 100 dB)", n, snr)
			}
		})
	}
}

// TestDFTRoundtrip verifies that DFT followed by IDFT returns the original signal.
// Tests non-power-of-2 sizes commonly used in CELT/Opus.
func TestDFTRoundtrip(t *testing.T) {
	sizes := []int{36, 50, 60, 120, 240, 480}
	transformRoundtripTest(t, sizes, dft, idftComplex, "DFT")
}

// TestDFTParsevals verifies Parseval's theorem: energy in time domain equals energy in frequency domain.
// This is an important property for audio processing correctness.
func TestDFTParsevals(t *testing.T) {
	sizes := []int{32, 64, 128, 60, 120, 240, 480}

	for _, n := range sizes {
		t.Run("", func(t *testing.T) {
			rng := rand.New(rand.NewSource(7777))
			x := make([]complex128, n)
			for i := 0; i < n; i++ {
				x[i] = complex(rng.Float64()*2-1, rng.Float64()*2-1)
			}

			// Time domain energy
			var timeEnergy float64
			for _, v := range x {
				timeEnergy += real(v)*real(v) + imag(v)*imag(v)
			}

			// Frequency domain energy (for DFT, must divide by N)
			var out []complex128
			if isPowerOfTwo(n) {
				out = fft(x)
			} else {
				out = dft(x)
			}

			var freqEnergy float64
			for _, v := range out {
				freqEnergy += real(v)*real(v) + imag(v)*imag(v)
			}
			freqEnergy /= float64(n) // Parseval's theorem normalization

			relErr := math.Abs(timeEnergy-freqEnergy) / timeEnergy
			t.Logf("n=%d: time energy=%.6f, freq energy=%.6f, rel error=%.2e", n, timeEnergy, freqEnergy, relErr)

			if relErr > 1e-12 {
				t.Errorf("n=%d: Parseval's theorem violated: relative error %.2e > 1e-12", n, relErr)
			}
		})
	}
}
