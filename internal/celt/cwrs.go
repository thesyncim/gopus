// Package celt implements the CELT (Constrained-Energy Lapped Transform) layer
// of the Opus codec as specified in RFC 6716 Section 4.3.
package celt

// CWRS (Combinatorial Radix-based With Signs) implements combinatorial indexing
// for PVQ (Pyramid Vector Quantization) decoding. This is the core algorithm
// for decoding normalized band vectors from compact indices.
//
// Reference: RFC 6716 Section 4.3.4.1, libopus celt/cwrs.c

// Constants for CWRS
const (
	// MaxPVQK is the maximum number of pulses we support in PVQ coding.
	MaxPVQK = 128
	// MaxPVQN is the maximum number of dimensions we support.
	MaxPVQN = 256
)

// pvqVCache memoizes computed V(N,K) values.
// V(N,K) = number of codewords in N dimensions with K pulses (including signs).
var pvqVCache = make(map[uint64]uint32)

// makeCacheKey creates a unique key for the V cache.
func makeCacheKey(n, k int) uint64 {
	return uint64(n)<<32 | uint64(uint32(k))
}

// PVQ_V computes V(N,K), the total number of PVQ codewords with N dimensions
// and K pulses (where the sum of absolute values equals K).
//
// V(N,K) follows the recurrence:
//   V(N, K) = V(N-1, K) + V(N, K-1) + V(N-1, K-1) for N > 1, K > 0
//   V(N, 0) = 1 for any N >= 0 (only the zero vector)
//   V(0, K) = 0 for K > 0 (no dimensions, can't have pulses)
//   V(1, K) = 2 for K > 0 (only +K and -K are valid)
//
// Reference: RFC 6716 Section 4.3.4.1, libopus celt/cwrs.c
func PVQ_V(n, k int) uint32 {
	if k < 0 {
		return 0
	}
	if k == 0 {
		return 1 // Only the zero vector
	}
	if n <= 0 {
		return 0 // No dimensions
	}
	if n == 1 {
		return 2 // Only +K and -K
	}

	// Check cache
	key := makeCacheKey(n, k)
	if val, ok := pvqVCache[key]; ok {
		return val
	}

	// Use recurrence: V(N,K) = V(N-1,K) + V(N,K-1) + V(N-1,K-1)
	result := PVQ_V(n-1, k) + PVQ_V(n, k-1) + PVQ_V(n-1, k-1)

	// Cache result
	pvqVCache[key] = result

	return result
}

// abs returns the absolute value of x.
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// DecodePulses converts a CWRS index to a pulse vector.
//
// Parameters:
//   - index: the combinatorial index (0 to V(n,k)-1)
//   - n: number of dimensions (band width)
//   - k: total number of pulses (sum of absolute values)
//
// Returns: pulse vector of length n, where sum(|v[i]|) == k
//
// The algorithm walks through positions, determining how many pulses
// go at each position by counting codewords in the combinatorial structure.
//
// Reference: libopus celt/cwrs.c decode_pulses() / cwrs64_decode_pulses()
func DecodePulses(index uint32, n, k int) []int {
	if n <= 0 || k < 0 {
		return nil
	}

	y := make([]int, n)

	if k == 0 {
		// Zero pulses - return zero vector
		return y
	}

	// Special case for n=1
	if n == 1 {
		// For n=1, there are only 2 codewords: +K and -K
		// Index 0 = +K, Index 1 = -K
		if index == 0 {
			y[0] = k
		} else {
			y[0] = -k
		}
		return y
	}

	// Work through positions 0 to n-2
	for i := 0; i < n-1 && k > 0; i++ {
		// Determine the number of pulses at position i
		// We find p such that the index falls within the range for p pulses at this position
		remaining := n - i - 1
		p := 0
		var cumulative uint32 = 0

		// First, count codewords with 0 pulses at position i
		v0 := PVQ_V(remaining, k)
		if index >= v0 {
			cumulative = v0
			p = 1

			// Now count codewords with p >= 1 pulses at position i
			// Each value of p contributes 2 * V(remaining, k-p) codewords (for +p and -p)
			for p <= k {
				vp := PVQ_V(remaining, k-p)
				contribution := 2 * vp // Both signs
				if index < cumulative+contribution {
					break
				}
				cumulative += contribution
				p++
			}
		}

		// Subtract cumulative count to get index within the subspace
		index -= cumulative

		// Extract sign if p > 0
		if p > 0 {
			// Sign bit is encoded in the least significant bit
			if index&1 == 1 {
				p = -p
			}
			index >>= 1
		}

		y[i] = p
		k -= abs(p)
	}

	// Last position gets all remaining pulses
	if k > 0 {
		y[n-1] = k
		// Sign from remaining index bit
		if index&1 == 1 {
			y[n-1] = -k
		}
	}

	return y
}

// EncodePulses converts a pulse vector to a CWRS index.
// This is the inverse of DecodePulses, useful for testing round-trip.
//
// Parameters:
//   - y: pulse vector of length n
//   - n: number of dimensions
//   - k: total number of pulses (should equal sum(|y[i]|))
//
// Returns: the combinatorial index (0 to V(n,k)-1)
func EncodePulses(y []int, n, k int) uint32 {
	if n <= 0 || k < 0 || len(y) != n {
		return 0
	}

	// Verify sum of absolute values equals k
	sum := 0
	for _, v := range y {
		sum += abs(v)
	}
	if sum != k {
		return 0 // Invalid input
	}

	// Special case for n=1
	if n == 1 {
		// Encode value directly: val -> index = val + k
		return uint32(y[0] + k)
	}

	var index uint32 = 0
	kRemaining := k

	for i := 0; i < n-1 && kRemaining > 0; i++ {
		p := abs(y[i])
		remaining := n - i - 1

		// Add cumulative count for all positions with fewer pulses
		for j := 0; j < p; j++ {
			pulsesLeft := kRemaining - j
			if pulsesLeft >= 0 {
				index += PVQ_V(remaining, pulsesLeft)
			}
		}

		// Encode sign if pulse is non-zero
		if p > 0 {
			// Insert sign bit at LSB of remaining index space
			index <<= 1
			if y[i] < 0 {
				index |= 1
			}
		}

		kRemaining -= p
	}

	// Handle sign of last position if it has pulses
	if kRemaining > 0 && y[n-1] < 0 {
		index <<= 1
		index |= 1
	} else if kRemaining > 0 {
		index <<= 1
	}

	return index
}

// PVQ_U computes U(N,K), the number of codewords where the first position has no pulse.
// U(N,K) = V(N-1, K) for N > 1
// U(1,K) = K (special handling in libopus)
func PVQ_U(n, k int) uint32 {
	if k <= 0 {
		return 0
	}
	if n <= 1 {
		return uint32(k)
	}
	return PVQ_V(n-1, k)
}

// ClearCache clears the PVQ_V cache.
// Call this if you need to free memory after decoding.
func ClearCache() {
	pvqVCache = make(map[uint64]uint32)
}
