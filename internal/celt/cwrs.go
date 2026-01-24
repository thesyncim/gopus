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
//
//	V(N, K) = V(N-1, K) + V(N, K-1) + V(N-1, K-1) for N > 1, K > 0
//	V(N, 0) = 1 for any N >= 0 (only the zero vector)
//	V(0, K) = 0 for K > 0 (no dimensions, can't have pulses)
//	V(1, K) = 2 for K > 0 (only +K and -K are valid)
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
	v1 := PVQ_V(n-1, k)
	v2 := PVQ_V(n, k-1)
	v3 := PVQ_V(n-1, k-1)
	sum := uint64(v1) + uint64(v2) + uint64(v3)

	const maxUint32 = ^uint32(0)
	var result uint32
	if sum > uint64(maxUint32) {
		result = maxUint32
	} else {
		result = uint32(sum)
	}

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

func unext(u []uint32, length int, u0 uint32) {
	if length < 2 {
		return
	}
	for j := 1; j < length; j++ {
		u1 := u[j] + u[j-1] + u0
		u[j-1] = u0
		u0 = u1
	}
	u[length-1] = u0
}

func uprev(u []uint32, length int, u0 uint32) {
	if length < 2 {
		return
	}
	for j := 1; j < length; j++ {
		u1 := u[j] - u[j-1] - u0
		u[j-1] = u0
		u0 = u1
	}
	u[length-1] = u0
}

// ncwrsUrow computes V(n,k) and fills u with U(n,0..k+1).
// u must have length at least k+2.
func ncwrsUrow(n, k int, u []uint32) uint32 {
	if n < 2 || k <= 0 || len(u) < k+2 {
		return 0
	}
	u[0] = 0
	u[1] = 1
	for j := 2; j < k+2; j++ {
		u[j] = uint32((j << 1) - 1)
	}
	for j := 2; j < n; j++ {
		unext(u[1:], k+1, 1)
	}
	return u[k] + u[k+1]
}

func cwrsi(n, k int, i uint32, y []int, u []uint32) uint32 {
	if n <= 0 || k <= 0 || len(y) < n || len(u) < k+2 {
		return 0
	}
	j := 0
	var yy uint32
	for {
		p := u[k+1]
		sign := 0
		if i >= p {
			sign = -1
			i -= p
		}
		yj := k
		p = u[k]
		for p > i {
			k--
			p = u[k]
		}
		i -= p
		yj -= k
		val := yj
		if sign != 0 {
			val = -yj
		}
		y[j] = val
		yy += uint32(val * val)
		uprev(u, k+2, 0)
		j++
		if j >= n {
			break
		}
	}
	return yy
}

func icwrs1(y int) (uint32, int) {
	k := abs(y)
	if y < 0 {
		return 1, k
	}
	return 0, k
}

func icwrs(n, k int, y []int, u []uint32) (uint32, uint32) {
	if n < 2 || k <= 0 || len(y) < n || len(u) < k+2 {
		return 0, 0
	}
	u[0] = 0
	for kk := 1; kk <= k+1; kk++ {
		u[kk] = uint32((kk << 1) - 1)
	}
	i, k1 := icwrs1(y[n-1])
	j := n - 2
	i += u[k1]
	k1 += abs(y[j])
	if y[j] < 0 {
		i += u[k1+1]
	}
	for j--; j >= 0; j-- {
		unext(u, k+2, 0)
		i += u[k1]
		k1 += abs(y[j])
		if y[j] < 0 {
			i += u[k1+1]
		}
	}
	return i, u[k1] + u[k1+1]
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
		return y
	}

	if n == 1 {
		if index&1 == 1 {
			y[0] = -k
		} else {
			y[0] = k
		}
		return y
	}

	u := make([]uint32, k+2)
	ncwrsUrow(n, k, u)
	_ = cwrsi(n, k, index, y, u)

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

	if n == 1 {
		if y[0] < 0 {
			return 1
		}
		return 0
	}

	u := make([]uint32, k+2)
	index, _ := icwrs(n, k, y, u)
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
