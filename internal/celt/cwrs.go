// Package celt implements the CELT (Constrained-Energy Lapped Transform) layer
// of the Opus codec as specified in RFC 6716 Section 4.3.
package celt

// CWRS (Combinatorial Radix-based With Signs) implements combinatorial indexing
// for PVQ (Pyramid Vector Quantization) decoding. This is the core algorithm
// for decoding normalized band vectors from compact indices.
//
// Reference: RFC 6716 Section 4.3.4.1, libopus celt/cwrs.c

// Constants for CWRS table dimensions
const (
	// MaxPulsesTable is the maximum K (pulses) in the precomputed U table
	MaxPulsesTable = 128
	// MaxDimTable is the maximum N (dimensions) for direct table lookup
	MaxDimTable = 8
)

// pvqU contains precomputed U(N,K) values.
// U(N,K) = number of PVQ codewords with N dimensions, K pulses,
// where position 0 has no pulse.
//
// The table is indexed as pvqU[n][k] for small n (0-6).
// For larger N or K, use computePVQ_U().
//
// Values extracted from libopus CELT_PVQ_U_DATA in celt/cwrs.c
// Note: We only include rows that fit in uint32 without overflow.
var pvqU = [][]uint32{
	// Row 0: N=0
	{1},
	// Row 1: N=1, U(1,k) = k for all k
	{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15,
		16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31,
		32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 45, 46, 47,
		48, 49, 50, 51, 52, 53, 54, 55, 56, 57, 58, 59, 60, 61, 62, 63,
		64, 65, 66, 67, 68, 69, 70, 71, 72, 73, 74, 75, 76, 77, 78, 79,
		80, 81, 82, 83, 84, 85, 86, 87, 88, 89, 90, 91, 92, 93, 94, 95,
		96, 97, 98, 99, 100, 101, 102, 103, 104, 105, 106, 107, 108, 109, 110, 111,
		112, 113, 114, 115, 116, 117, 118, 119, 120, 121, 122, 123, 124, 125, 126, 127, 128},
	// Row 2: N=2, U(2,k) = 2*k for k>=1
	{0, 0, 2, 4, 6, 8, 10, 12, 14, 16, 18, 20, 22, 24, 26, 28,
		30, 32, 34, 36, 38, 40, 42, 44, 46, 48, 50, 52, 54, 56, 58, 60,
		62, 64, 66, 68, 70, 72, 74, 76, 78, 80, 82, 84, 86, 88, 90, 92,
		94, 96, 98, 100, 102, 104, 106, 108, 110, 112, 114, 116, 118, 120, 122, 124,
		126, 128, 130, 132, 134, 136, 138, 140, 142, 144, 146, 148, 150, 152, 154, 156,
		158, 160, 162, 164, 166, 168, 170, 172, 174, 176, 178, 180, 182, 184, 186, 188,
		190, 192, 194, 196, 198, 200, 202, 204, 206, 208, 210, 212, 214, 216, 218, 220,
		222, 224, 226, 228, 230, 232, 234, 236, 238, 240, 242, 244, 246, 248, 250, 252, 254},
	// Row 3: N=3
	{0, 0, 0, 2, 6, 12, 20, 30, 42, 56, 72, 90, 110, 132, 156, 182,
		210, 240, 272, 306, 342, 380, 420, 462, 506, 552, 600, 650, 702, 756, 812, 870,
		930, 992, 1056, 1122, 1190, 1260, 1332, 1406, 1482, 1560, 1640, 1722, 1806, 1892, 1980, 2070,
		2162, 2256, 2352, 2450, 2550, 2652, 2756, 2862, 2970, 3080, 3192, 3306, 3422, 3540, 3660, 3782,
		3906, 4032, 4160, 4290, 4422, 4556, 4692, 4830, 4970, 5112, 5256, 5402, 5550, 5700, 5852, 6006,
		6162, 6320, 6480, 6642, 6806, 6972, 7140, 7310, 7482, 7656, 7832, 8010, 8190, 8372, 8556, 8742,
		8930, 9120, 9312, 9506, 9702, 9900, 10100, 10302, 10506, 10712, 10920, 11130, 11342, 11556, 11772, 11990,
		12210, 12432, 12656, 12882, 13110, 13340, 13572, 13806, 14042, 14280, 14520, 14762, 15006, 15252, 15500, 15750, 16002},
	// Row 4: N=4
	{0, 0, 0, 0, 2, 8, 20, 40, 70, 112, 168, 240, 330, 440, 572, 728,
		910, 1120, 1360, 1632, 1938, 2280, 2660, 3080, 3542, 4048, 4600, 5200, 5850, 6552, 7308, 8120,
		8990, 9920, 10912, 11968, 13090, 14280, 15540, 16872, 18278, 19760, 21320, 22960, 24682, 26488, 28380, 30360,
		32430, 34592, 36848, 39200, 41650, 44200, 46852, 49608, 52470, 55440, 58520, 61712, 65018, 68440, 71980, 75640,
		79422, 83328, 87360, 91520, 95810, 100232, 104788, 109480, 114310, 119280, 124392, 129648, 135050, 140600, 146300, 152152,
		158158, 164320, 170640, 177120, 183762, 190568, 197540, 204680, 211990, 219472, 227128, 234960, 242970, 251160, 259532, 268088,
		276830, 285760, 294880, 304192, 313698, 323400, 333300, 343400, 353702, 364208, 374920, 385840, 396970, 408312, 419868, 431640,
		443630, 455840, 468272, 480928, 493810, 506920, 520260, 533832, 547638, 561680, 575960, 590480, 605242, 620248, 635500, 651000, 666750},
	// Row 5: N=5
	{0, 0, 0, 0, 0, 2, 10, 30, 70, 140, 252, 420, 660, 990, 1430, 2002,
		2730, 3640, 4760, 6120, 7752, 9690, 11970, 14630, 17710, 21252, 25300, 29900, 35100, 40950, 47502, 54810,
		62930, 71920, 81840, 92752, 104720, 117810, 132090, 147630, 164502, 182780, 202540, 223860, 246820, 271502, 297990, 326370,
		356730, 389160, 423752, 460600, 499800, 541450, 585650, 632502, 682110, 734580, 790020, 848540, 910252, 975270, 1043710, 1115690,
		1191330, 1270752, 1354080, 1441440, 1532960, 1628770, 1729002, 1833790, 1943270, 2057580, 2176860, 2301252, 2430900, 2565950, 2706550, 2852850,
		3005002, 3163160, 3327480, 3498120, 3675240, 3859002, 4049570, 4247110, 4451790, 4663780, 4883252, 5110380, 5345340, 5588310, 5839470, 6099002,
		6367090, 6643920, 6929680, 7224560, 7528752, 7842450, 8165850, 8499150, 8842550, 9196252, 9560460, 9935380, 10321220, 10718190, 11126502, 11546370,
		11978010, 12421640, 12877480, 13345752, 13826680, 14320490, 14827410, 15347670, 15881502, 16429140, 16990820, 17566780, 18157260, 18762502, 19382750, 20018250, 20669250},
	// Row 6: N=6
	{0, 0, 0, 0, 0, 0, 2, 12, 42, 112, 252, 504, 924, 1584, 2574, 4004,
		6006, 8736, 12376, 17136, 23256, 31008, 40698, 52668, 67298, 85008, 106260, 131560, 161460, 196560, 237510, 285012,
		339822, 402752, 474672, 556512, 649264, 753984, 871794, 1003884, 1151514, 1316016, 1498796, 1701336, 1925196, 2172016, 2443518, 2741508,
		3067878, 3424608, 3813768, 4237520, 4698120, 5197920, 5739370, 6325020, 6957522, 7639632, 8374212, 9164232, 10012772, 10923024, 11898294, 12942004,
		14057694, 15249024, 16519776, 17873856, 19315296, 20848256, 22477026, 24206028, 26039818, 27983088, 30040668, 32217528, 34518778, 36949672, 39515610, 42222140,
		45074962, 48079928, 51243044, 54570472, 58068532, 61743704, 65602630, 69652116, 73899132, 78350816, 83014476, 87897592, 93007818, 98352984, 103941098, 109780348,
		115879104, 122245920, 128889534, 135818872, 143043050, 150571376, 158413352, 166578676, 175077244, 183919152, 193114698, 202674384, 212608918, 222929216, 233646404, 244771820,
		256317014, 268293752, 280714020, 293590024, 306934192, 320759176, 335077854, 349903332, 365248946, 381128264, 397555088, 414543456, 432107646, 450262176, 468921806, 488101540, 507816630},
}

// pvqUCache provides memoization for computed U values beyond the precomputed table.
// This is a simple cache to avoid recomputation for frequently used values.
var pvqUCache = make(map[uint64]uint32)

// makeCacheKey creates a unique key from n and k for the cache.
func makeCacheKey(n, k int) uint64 {
	return uint64(n)<<32 | uint64(k)
}

// computePVQ_U computes U(n,k) for values not in the precomputed table.
// Uses the recurrence: U(N,K) = U(N-1,K) + U(N,K-1) + U(N-1,K-1)
func computePVQ_U(n, k int) uint32 {
	if k == 0 {
		return 0
	}
	if n == 0 {
		return 1
	}
	if n == 1 {
		return uint32(k)
	}
	if n == 2 {
		if k <= 1 {
			return 0
		}
		return uint32(2 * (k - 1))
	}

	// Check if in precomputed table
	if n < len(pvqU) && k < len(pvqU[n]) {
		return pvqU[n][k]
	}

	// Check cache
	key := makeCacheKey(n, k)
	if val, ok := pvqUCache[key]; ok {
		return val
	}

	// Compute using recurrence relation
	// U(N,K) = U(N-1,K) + U(N,K-1) + U(N-1,K-1)
	result := computePVQ_U(n-1, k) + computePVQ_U(n, k-1) + computePVQ_U(n-1, k-1)

	// Cache the result
	pvqUCache[key] = result

	return result
}

// getPVQ_U returns U(n,k) from precomputed table or computes it.
func getPVQ_U(n, k int) uint32 {
	if k == 0 {
		return 0
	}
	if n == 0 {
		return 1 // Edge case
	}
	if n == 1 {
		return uint32(k)
	}
	if n == 2 {
		if k <= 1 {
			return 0
		}
		return uint32(2 * (k - 1))
	}

	// Use precomputed table if available
	if n < len(pvqU) && k < len(pvqU[n]) {
		return pvqU[n][k]
	}

	// Fall back to computation for larger values
	return computePVQ_U(n, k)
}

// PVQ_V computes V(N,K), the total number of PVQ codewords with N dimensions
// and K pulses (where the sum of absolute values equals K).
//
// V(N,K) = U(N,K) + U(N,K+1) for K > 0
// V(N,0) = 1 (only the zero vector)
//
// For N=1: V(1,K) = 2K+1 (values from -K to +K)
func PVQ_V(n, k int) uint32 {
	if k == 0 {
		return 1
	}
	if n == 1 {
		return uint32(2*k + 1) // -k, -(k-1), ..., -1, 0, 1, ..., k-1, k
	}
	if n == 0 {
		return 0 // No dimensions, no valid codewords for k>0
	}

	return getPVQ_U(n, k) + getPVQ_U(n, k+1)
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

	// Work through positions 0 to n-2
	for i := 0; i < n-1 && k > 0; i++ {
		// Determine the number of pulses at position i
		p := 0

		// Count how many codewords have exactly p pulses at position i
		// by subtracting cumulative counts
		var cumulative uint32 = 0

		for {
			// V(n-i-1, k-p) = number of ways to distribute remaining pulses
			// in remaining dimensions
			remaining := n - i - 1
			pulsesLeft := k - p

			if pulsesLeft < 0 {
				// Can't have more pulses at position i than total remaining
				break
			}

			vRemaining := PVQ_V(remaining, pulsesLeft)
			if index < cumulative+vRemaining {
				break
			}
			cumulative += vRemaining
			p++
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
// This is the inverse of DecodePulses, useful for testing.
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

	var index uint32 = 0
	var signBits uint32 = 0
	var numSignBits uint = 0

	// Track remaining pulses
	kRemaining := k

	for i := 0; i < n-1 && kRemaining > 0; i++ {
		p := abs(y[i])

		// Add cumulative count for all positions with fewer pulses
		for j := 0; j < p; j++ {
			remaining := n - i - 1
			pulsesLeft := kRemaining - j
			if pulsesLeft >= 0 {
				index += PVQ_V(remaining, pulsesLeft)
			}
		}

		// Encode sign if pulse is non-zero
		if p > 0 {
			if y[i] < 0 {
				signBits |= 1 << numSignBits
			}
			numSignBits++
		}

		kRemaining -= p
	}

	// Handle sign of last position
	if kRemaining > 0 {
		if y[n-1] < 0 {
			signBits |= 1 << numSignBits
		}
		numSignBits++
	}

	// Combine index with sign bits
	// Signs are interleaved with index during decoding, so we need to
	// reconstruct the combined index
	return (index << numSignBits) | signBits
}
