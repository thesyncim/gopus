// Copyright (c) 2003-2004, Mark Borgerding
// Lots of modifications by Jean-Marc Valin
// Copyright (c) 2005-2007, Xiph.Org Foundation
// Copyright (c) 2008, Xiph.Org Foundation, CSIRO
// Go port for gopus project
//
// All rights reserved.
//
// Redistribution and use in source and binary forms, with or without
// modification, are permitted provided that the following conditions are met:
//
//   - Redistributions of source code must retain the above copyright notice,
//     this list of conditions and the following disclaimer.
//   - Redistributions in binary form must reproduce the above copyright notice,
//     this list of conditions and the following disclaimer in the
//     documentation and/or other materials provided with the distribution.
//
// THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
// AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
// IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE
// ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT OWNER OR CONTRIBUTORS BE
// LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR
// CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF
// SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS
// INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN
// CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE)
// ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE
// POSSIBILITY OF SUCH DAMAGE.

package celt

import (
	"math"
	"sync"
)

// KissFFT64State holds the precomputed state for mixed-radix FFT (float64).
// Supports sizes that factor into 2, 3, 4, and 5.
// This is based on libopus kiss_fft implementation optimized for CELT.
type KissFFT64State struct {
	nfft     int          // FFT size
	scale    float64      // Scaling factor (1/nfft for forward FFT)
	factors  []int        // Factorization: pairs of (radix, m) where radix*m = previous m
	twiddles []complex128 // Precomputed twiddle factors
	bitrev   []int        // Bit-reversal (mixed-radix digit reversal) permutation
}

// kissFFT64Cache caches FFT states for commonly used sizes
var (
	kissFFT64Cache   = make(map[int]*KissFFT64State)
	kissFFT64CacheMu sync.Mutex
)

// GetKissFFT64State returns a cached or newly created FFT state for the given size.
func GetKissFFT64State(nfft int) *KissFFT64State {
	kissFFT64CacheMu.Lock()
	defer kissFFT64CacheMu.Unlock()

	if state, ok := kissFFT64Cache[nfft]; ok {
		return state
	}

	state := newKissFFT64State(nfft)
	if state != nil {
		kissFFT64Cache[nfft] = state
	}
	return state
}

// newKissFFT64State creates a new FFT state for the given size.
func newKissFFT64State(nfft int) *KissFFT64State {
	state := &KissFFT64State{
		nfft:  nfft,
		scale: 1.0 / float64(nfft),
	}

	// Compute factorization
	if !state.computeFactors() {
		return nil // Size not supported (contains prime > 5)
	}

	// Compute twiddle factors: exp(-2*pi*i*k/nfft) for k = 0..nfft-1
	state.twiddles = make([]complex128, nfft)
	for k := 0; k < nfft; k++ {
		phase := -2.0 * math.Pi * float64(k) / float64(nfft)
		state.twiddles[k] = complex(math.Cos(phase), math.Sin(phase))
	}

	// Compute bit-reversal (digit-reversal) permutation
	state.computeBitrev()

	return state
}

// computeFactors computes the factorization for mixed-radix FFT.
// Returns false if the size contains a prime factor > 5.
// The factorization is stored as pairs (radix, m) where radix*m = previous_m.
func (s *KissFFT64State) computeFactors() bool {
	n := s.nfft
	s.factors = nil

	// Factor out in order: 4, 2, 3, 5
	// This order is chosen to maximize radix-4 usage (most efficient)
	p := 4
	for n > 1 {
		for n%p != 0 {
			switch p {
			case 4:
				p = 2
			case 2:
				p = 3
			case 3:
				p = 5
			default:
				p += 2
			}
			if p > 5 && p*p > n {
				p = n // Remaining factor
			}
		}
		if p > 5 {
			return false // Unsupported prime factor
		}
		n /= p
		s.factors = append(s.factors, p, n)
	}

	// Reverse the order so we process smaller radixes first
	// (improves cache locality and matches libopus behavior)
	numStages := len(s.factors) / 2
	for i := 0; i < numStages/2; i++ {
		j := numStages - 1 - i
		s.factors[2*i], s.factors[2*j] = s.factors[2*j], s.factors[2*i]
		s.factors[2*i+1], s.factors[2*j+1] = s.factors[2*j+1], s.factors[2*i+1]
	}

	// If we have a radix-2 not at the end, swap with radix-4 at the end
	// to use the optimized radix-2 after radix-4 pattern
	if numStages >= 2 && s.factors[0] == 2 {
		// Move radix-2 to after radix-4 stages
		for i := 0; i < numStages-1; i++ {
			if s.factors[2*i] == 2 && s.factors[2*(i+1)] == 4 {
				s.factors[2*i], s.factors[2*(i+1)] = s.factors[2*(i+1)], s.factors[2*i]
			}
		}
	}

	// Recompute m values after reordering
	n = s.nfft
	for i := 0; i < numStages; i++ {
		n /= s.factors[2*i]
		s.factors[2*i+1] = n
	}

	return true
}

// computeBitrev computes the mixed-radix digit reversal permutation.
// This mirrors the C version from kiss_fft.c:compute_bitrev_table.
func (s *KissFFT64State) computeBitrev() {
	s.bitrev = make([]int, s.nfft)
	s.computeBitrevRecursive(0, 0, 1, 1, s.factors)
}

// computeBitrevRecursive fills the bitrev table using kiss FFT factor recursion.
// fout: starting output index (value to write)
// fIdx: starting write index in bitrev
// fstride: stride for this level
// inStride: always 1 for our use
// factors: [p, m, ...] pairs
func (s *KissFFT64State) computeBitrevRecursive(fout int, fIdx int, fstride int, inStride int, factors []int) {
	if len(factors) < 2 {
		return
	}
	p := factors[0] // radix
	m := factors[1] // stage's fft length / p
	step := fstride * inStride

	if m == 1 {
		// Leaf level: write p consecutive values with stride step
		for j := 0; j < p; j++ {
			if fIdx >= 0 && fIdx < len(s.bitrev) {
				s.bitrev[fIdx] = fout + j
			}
			fIdx += step
		}
	} else {
		// Recursive level: call p times, advancing fIdx by step after each
		for j := 0; j < p; j++ {
			s.computeBitrevRecursive(fout, fIdx, fstride*p, inStride, factors[2:])
			fIdx += step
			fout += m
		}
	}
}

// KissFFT performs the forward FFT.
func (s *KissFFT64State) KissFFT(fin, fout []complex128) {
	// Bit-reverse copy with scaling
	for i := 0; i < s.nfft; i++ {
		fout[s.bitrev[i]] = fin[i] * complex(s.scale, 0)
	}

	// Perform the FFT
	s.fftImpl(fout)
}

// KissIFFT performs the inverse FFT.
func (s *KissFFT64State) KissIFFT(fin, fout []complex128) {
	// Bit-reverse copy (no scaling for inverse)
	for i := 0; i < s.nfft; i++ {
		fout[s.bitrev[i]] = fin[i]
	}

	// Conjugate input
	for i := 0; i < s.nfft; i++ {
		fout[i] = complex(real(fout[i]), -imag(fout[i]))
	}

	// Forward FFT
	s.fftImpl(fout)

	// Conjugate output
	for i := 0; i < s.nfft; i++ {
		fout[i] = complex(real(fout[i]), -imag(fout[i]))
	}
}

// fftImpl performs the mixed-radix FFT computation.
func (s *KissFFT64State) fftImpl(fout []complex128) {
	numFactors := len(s.factors) / 2
	fstride := make([]int, numFactors+1)
	fstride[0] = 1

	// Compute stride for each stage
	for i := 0; i < numFactors; i++ {
		p := s.factors[2*i]
		fstride[i+1] = fstride[i] * p
	}

	m := s.factors[2*numFactors-1] // Start with the last m value

	// Process stages from last to first
	for i := numFactors - 1; i >= 0; i-- {
		var m2 int
		if i > 0 {
			m2 = s.factors[2*i-1]
		} else {
			m2 = 1
		}

		switch s.factors[2*i] {
		case 2:
			s.bfly2(fout, fstride[i], m, fstride[i], m2)
		case 3:
			s.bfly3(fout, fstride[i], m, fstride[i], m2)
		case 4:
			s.bfly4(fout, fstride[i], m, fstride[i], m2)
		case 5:
			s.bfly5(fout, fstride[i], m, fstride[i], m2)
		}
		m = m2
	}
}

// bfly2 performs radix-2 butterfly with twiddle factors.
func (s *KissFFT64State) bfly2(fout []complex128, fstride, m, n, mm int) {
	twIdx := 0
	for j := 0; j < m; j++ {
		tw := s.twiddles[twIdx]
		for i := 0; i < n; i++ {
			idx := j + mm*i
			if idx+m >= len(fout) {
				break
			}
			t := fout[idx+m] * tw
			fout[idx+m] = fout[idx] - t
			fout[idx] = fout[idx] + t
		}
		twIdx += fstride
	}
}

// bfly3 performs radix-3 butterfly.
func (s *KissFFT64State) bfly3(fout []complex128, fstride int, m, n, mm int) {
	m2 := 2 * m
	epi3 := s.twiddles[fstride*m]

	for i := 0; i < n; i++ {
		foutBase := i * mm
		tw1Idx := 0
		tw2Idx := 0

		for k := 0; k < m; k++ {
			if foutBase+m2 >= len(fout) {
				break
			}

			scratch1 := fout[foutBase+m] * s.twiddles[tw1Idx]
			scratch2 := fout[foutBase+m2] * s.twiddles[tw2Idx]

			scratch3 := scratch1 + scratch2
			scratch0 := scratch1 - scratch2

			tw1Idx += fstride
			tw2Idx += fstride * 2

			fout[foutBase+m] = fout[foutBase] - complex(0.5*real(scratch3), 0.5*imag(scratch3))

			scratch0 = complex(real(scratch0)*imag(epi3), imag(scratch0)*imag(epi3))

			fout[foutBase] = fout[foutBase] + scratch3

			fout[foutBase+m2] = complex(
				real(fout[foutBase+m])+imag(scratch0),
				imag(fout[foutBase+m])-real(scratch0),
			)

			fout[foutBase+m] = complex(
				real(fout[foutBase+m])-imag(scratch0),
				imag(fout[foutBase+m])+real(scratch0),
			)

			foutBase++
		}
	}
}

// bfly4 performs radix-4 butterfly.
func (s *KissFFT64State) bfly4(fout []complex128, fstride int, m, n, mm int) {
	m2 := 2 * m
	m3 := 3 * m

	if m == 1 {
		// Degenerate case: all twiddles are 1
		for i := 0; i < n; i++ {
			base := i * 4
			if base+3 >= len(fout) {
				break
			}

			scratch0 := fout[base] - fout[base+2]
			fout[base] = fout[base] + fout[base+2]
			scratch1 := fout[base+1] + fout[base+3]
			fout[base+2] = fout[base] - scratch1
			fout[base] = fout[base] + scratch1
			scratch1 = fout[base+1] - fout[base+3]

			fout[base+1] = complex(real(scratch0)+imag(scratch1), imag(scratch0)-real(scratch1))
			fout[base+3] = complex(real(scratch0)-imag(scratch1), imag(scratch0)+real(scratch1))
		}
	} else {
		for i := 0; i < n; i++ {
			foutBase := i * mm
			tw1Idx := 0
			tw2Idx := 0
			tw3Idx := 0

			for j := 0; j < m; j++ {
				if foutBase+m3 >= len(fout) {
					break
				}

				scratch0 := fout[foutBase+m] * s.twiddles[tw1Idx]
				scratch1 := fout[foutBase+m2] * s.twiddles[tw2Idx]
				scratch2 := fout[foutBase+m3] * s.twiddles[tw3Idx]

				scratch5 := fout[foutBase] - scratch1
				fout[foutBase] = fout[foutBase] + scratch1
				scratch3 := scratch0 + scratch2
				scratch4 := scratch0 - scratch2
				fout[foutBase+m2] = fout[foutBase] - scratch3

				tw1Idx += fstride
				tw2Idx += fstride * 2
				tw3Idx += fstride * 3

				fout[foutBase] = fout[foutBase] + scratch3

				fout[foutBase+m] = complex(
					real(scratch5)+imag(scratch4),
					imag(scratch5)-real(scratch4),
				)
				fout[foutBase+m3] = complex(
					real(scratch5)-imag(scratch4),
					imag(scratch5)+real(scratch4),
				)

				foutBase++
			}
		}
	}
}

// bfly5 performs radix-5 butterfly.
func (s *KissFFT64State) bfly5(fout []complex128, fstride int, m, n, mm int) {
	// Radix-5 constants
	ya := complex(0.30901699437494742, -0.95105651629515353)  // exp(-2*pi*i/5)
	yb := complex(-0.80901699437494742, -0.58778525229247313) // exp(-4*pi*i/5)

	for i := 0; i < n; i++ {
		foutBase := i * mm
		fout0 := foutBase
		fout1 := fout0 + m
		fout2 := fout0 + 2*m
		fout3 := fout0 + 3*m
		fout4 := fout0 + 4*m

		for u := 0; u < m; u++ {
			if fout4 >= len(fout) {
				break
			}

			scratch0 := fout[fout0]

			scratch1 := fout[fout1] * s.twiddles[u*fstride]
			scratch2 := fout[fout2] * s.twiddles[2*u*fstride]
			scratch3 := fout[fout3] * s.twiddles[3*u*fstride]
			scratch4 := fout[fout4] * s.twiddles[4*u*fstride]

			scratch7 := scratch1 + scratch4
			scratch10 := scratch1 - scratch4
			scratch8 := scratch2 + scratch3
			scratch9 := scratch2 - scratch3

			fout[fout0] = scratch0 + scratch7 + scratch8

			scratch5 := complex(
				real(scratch0)+real(ya)*real(scratch7)+real(yb)*real(scratch8),
				imag(scratch0)+real(ya)*imag(scratch7)+real(yb)*imag(scratch8),
			)

			scratch6 := complex(
				imag(ya)*imag(scratch10)+imag(yb)*imag(scratch9),
				-(imag(ya)*real(scratch10) + imag(yb)*real(scratch9)),
			)

			fout[fout1] = scratch5 - scratch6
			fout[fout4] = scratch5 + scratch6

			scratch11 := complex(
				real(scratch0)+real(yb)*real(scratch7)+real(ya)*real(scratch8),
				imag(scratch0)+real(yb)*imag(scratch7)+real(ya)*imag(scratch8),
			)

			scratch12 := complex(
				-imag(yb)*imag(scratch10)+imag(ya)*imag(scratch9),
				imag(yb)*real(scratch10)-imag(ya)*real(scratch9),
			)

			fout[fout2] = scratch11 + scratch12
			fout[fout3] = scratch11 - scratch12

			fout0++
			fout1++
			fout2++
			fout3++
			fout4++
		}
	}
}

// kissFFT64Forward performs forward FFT using precomputed state without 1/n scaling.
// This is used internally by dftTo for efficient O(n log n) FFT.
func kissFFT64Forward(out []complex128, in []complex128, state *KissFFT64State) {
	n := state.nfft

	// Bit-reverse copy (no scaling for unscaled forward FFT)
	for i := 0; i < n; i++ {
		out[state.bitrev[i]] = in[i]
	}

	// Perform the FFT using the same implementation
	state.fftImpl(out)
}
