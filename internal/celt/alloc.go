// Package celt implements the CELT decoder per RFC 6716 Section 4.3.
package celt

// Bit allocation per RFC 6716 Section 4.3.3.
// This module computes how many bits each band receives for PVQ shape coding.

// BandAlloc contains base bits per band at each quality level.
// Index: BandAlloc[quality][band] where quality is 0-10 (11 levels).
// Values represent bits in 1/8th bit resolution (Q3 format).
// Source: libopus celt/static_modes_float.h band_allocation table
var BandAlloc = [11][21]int{
	// Quality 0 (lowest)
	{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	// Quality 1
	{90, 80, 75, 69, 63, 56, 49, 40, 34, 29, 20, 18, 10, 0, 0, 0, 0, 0, 0, 0, 0},
	// Quality 2
	{110, 100, 90, 84, 78, 71, 65, 58, 51, 45, 39, 32, 26, 20, 12, 0, 0, 0, 0, 0, 0},
	// Quality 3
	{118, 110, 103, 93, 86, 80, 75, 70, 65, 59, 53, 47, 40, 31, 23, 15, 4, 0, 0, 0, 0},
	// Quality 4
	{126, 119, 112, 104, 95, 89, 83, 78, 72, 66, 60, 54, 47, 39, 32, 25, 17, 12, 1, 0, 0},
	// Quality 5 (mid)
	{134, 127, 120, 114, 108, 102, 96, 90, 84, 78, 72, 66, 60, 54, 47, 41, 35, 29, 23, 16, 8},
	// Quality 6
	{144, 137, 130, 124, 118, 113, 108, 103, 98, 93, 88, 82, 76, 69, 62, 55, 48, 42, 36, 30, 24},
	// Quality 7
	{152, 145, 139, 133, 128, 122, 117, 112, 107, 102, 97, 92, 86, 80, 74, 67, 60, 53, 47, 40, 33},
	// Quality 8
	{162, 155, 148, 143, 137, 132, 127, 122, 117, 112, 107, 102, 96, 90, 84, 77, 71, 64, 57, 50, 43},
	// Quality 9
	{172, 165, 159, 153, 147, 142, 137, 132, 127, 122, 117, 112, 106, 100, 94, 88, 82, 75, 68, 62, 55},
	// Quality 10 (highest)
	{183, 177, 171, 165, 160, 155, 150, 145, 140, 135, 130, 125, 120, 114, 108, 102, 96, 90, 84, 77, 70},
}

// LogNTable contains log2 of band widths (in Q8 fixed-point) for bit allocation.
// Used to weight bands based on their width.
// For band i, LogNTable[i] = round(log2(BandWidth(i)) * 256)
// Source: libopus celt/modes.c (logN400 table)
var LogNTable = LogN // Use the table from tables.go

// AllocationResult holds the output of bit allocation computation.
type AllocationResult struct {
	BandBits      []int // Bits allocated per band for PVQ
	FineBits      []int // Bits for fine energy refinement
	RemainderBits []int // Leftover bits after PVQ
	PulseCaps     []int // Maximum PVQ index bits per band
	Total         int   // Total bits allocated (should match budget)
	Skip          int   // Number of bands to skip at the start
}

// pulseCap returns maximum PVQ index bits for a band given its width.
// Larger bands can encode more pulses.
// Reference: libopus celt/rate.c interp_bits2pulses
func pulseCap(bandWidth int) int {
	// Maximum bits for PVQ is related to band width
	// Roughly: cap = 8 * log2(bandWidth) + constant
	// For CELT, typical caps range from 8 bits (narrow bands) to 64+ bits (wide bands)
	if bandWidth <= 0 {
		return 0
	}
	if bandWidth == 1 {
		return 8 // Minimum: single coefficient
	}

	// Log2 approximation
	log2Width := 0
	w := bandWidth
	for w > 1 {
		w >>= 1
		log2Width++
	}

	// Cap formula from libopus: 8 * (log2(width) + 1)
	cap := 8 * (log2Width + 1)
	if cap > 255 {
		cap = 255
	}
	return cap
}

// interpolateAlloc computes allocation at fractional quality level.
// quality is in 1/8th steps (0-80 for 11 levels, where each level is 8 steps).
// nbBands is the number of bands to allocate for.
// Reference: libopus celt/rate.c interp_bits2pulses
func interpolateAlloc(quality int, nbBands int) []int {
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands < 0 {
		nbBands = 0
	}

	result := make([]int, nbBands)

	// Clamp quality to valid range
	if quality < 0 {
		quality = 0
	}
	if quality > 80 {
		quality = 80
	}

	// Find integer quality levels to interpolate between
	// quality = 8 * level + frac
	level := quality / 8
	frac := quality % 8

	if level >= 10 {
		// At or above max quality
		for band := 0; band < nbBands; band++ {
			result[band] = BandAlloc[10][band]
		}
		return result
	}

	// Interpolate between level and level+1
	for band := 0; band < nbBands; band++ {
		low := BandAlloc[level][band]
		high := BandAlloc[level+1][band]
		// Linear interpolation: low + (high - low) * frac / 8
		result[band] = low + (high-low)*frac/8
	}

	return result
}

// applyTrim adjusts allocation based on trim value (-6 to +6).
// Boosts high bands when trim > 0, low bands when trim < 0.
// Reference: libopus celt/rate.c compute_allocation
func applyTrim(alloc []int, trim int, nbBands int, lm int) {
	if nbBands > len(alloc) {
		nbBands = len(alloc)
	}
	if nbBands <= 0 {
		return
	}

	// Trim adjusts spectral tilt of allocation
	// trim > 0: boost high frequencies
	// trim < 0: boost low frequencies
	// trim = 0: no change

	if trim == 0 {
		return
	}

	// Trim amount scales with LM (frame size)
	// Per libopus: larger frames get more trim effect
	trimScale := (lm + 1) * 4

	for band := 0; band < nbBands; band++ {
		// Position from center (0 to nbBands-1 mapped to -1.0 to +1.0)
		position := float64(2*band-nbBands+1) / float64(nbBands-1)

		// Adjustment: trim * position * scale
		// Positive trim + high band = positive adjustment
		// Positive trim + low band = negative adjustment
		adjustment := int(float64(trim) * position * float64(trimScale))

		alloc[band] += adjustment

		// Ensure non-negative
		if alloc[band] < 0 {
			alloc[band] = 0
		}
	}
}

// applyDynalloc applies per-band dynamic allocation boosts.
// dynalloc[band] specifies additional bits to allocate.
// Reference: libopus celt/rate.c compute_allocation
func applyDynalloc(alloc []int, dynalloc []int, nbBands int) {
	if nbBands > len(alloc) {
		nbBands = len(alloc)
	}
	if nbBands > len(dynalloc) {
		nbBands = len(dynalloc)
	}

	for band := 0; band < nbBands; band++ {
		alloc[band] += dynalloc[band]
	}
}

// applyCaps enforces per-band bit caps.
// Reference: libopus celt/rate.c compute_allocation
func applyCaps(alloc []int, caps []int, nbBands int) {
	if nbBands > len(alloc) {
		nbBands = len(alloc)
	}
	if nbBands > len(caps) {
		nbBands = len(caps)
	}

	for band := 0; band < nbBands; band++ {
		if alloc[band] > caps[band] {
			alloc[band] = caps[band]
		}
	}
}

// computeFineBits determines fine energy bits from allocation.
// Bits above PVQ minimum go to fine energy.
// Reference: libopus celt/rate.c compute_allocation
func computeFineBits(alloc []int, nbBands int, stereo bool) ([]int, []int) {
	fineBits := make([]int, nbBands)
	pvqBits := make([]int, nbBands)

	// Minimum bits needed for PVQ per band
	// Depends on band width and whether it's coded at all
	minPVQBits := 8 // Minimum to encode any pulses

	for band := 0; band < nbBands; band++ {
		bits := alloc[band]

		if bits < minPVQBits {
			// Not enough bits for PVQ, all goes to fine energy if any
			fineBits[band] = bits / 2 // Half goes to fine energy
			pvqBits[band] = bits - fineBits[band]
		} else {
			// Split between fine energy and PVQ
			// Fine energy gets 1 bit per 24 bits total (approx)
			fine := bits / 24
			if fine > 8 {
				fine = 8 // Max 8 fine bits
			}
			fineBits[band] = fine
			pvqBits[band] = bits - fine
		}

		// Stereo bands may need more bits
		if stereo && fineBits[band] < 2 {
			fineBits[band] = 2
			if pvqBits[band] < 0 {
				pvqBits[band] = 0
			}
		}
	}

	return fineBits, pvqBits
}

// ComputePulseCaps computes the maximum bits per band based on band width.
// Reference: libopus celt/rate.c interp_bits2pulses
func ComputePulseCaps(nbBands int, lm int) []int {
	caps := make([]int, nbBands)

	// LM affects effective band width
	scale := 1 << lm // 1, 2, 4, 8 for LM 0-3

	for band := 0; band < nbBands; band++ {
		width := BandWidth(band) * scale
		caps[band] = pulseCap(width)
	}

	return caps
}

// ComputeAllocation computes bits per band from total budget.
// Returns AllocationResult containing bandBits, fineBits, and more.
// Reference: RFC 6716 Section 4.3.3, libopus celt/rate.c compute_allocation()
//
// Parameters:
//   - totalBits: Total bit budget for the frame
//   - nbBands: Number of frequency bands
//   - cap: Per-band caps (max bits per band, nil for auto)
//   - dynalloc: Dynamic allocation boosts (nil for none)
//   - trim: Allocation trim (-6 to 6, 0 = neutral)
//   - intensity: Intensity stereo start band (-1 = no intensity stereo)
//   - dualStereo: Whether dual stereo mode is used
//   - lm: Frame size mode (0-3)
func ComputeAllocation(
	totalBits int,
	nbBands int,
	cap []int,
	dynalloc []int,
	trim int,
	intensity int,
	dualStereo bool,
	lm int,
) AllocationResult {
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands < 0 {
		nbBands = 0
	}
	if lm < 0 {
		lm = 0
	}
	if lm > 3 {
		lm = 3
	}

	result := AllocationResult{
		BandBits:      make([]int, nbBands),
		FineBits:      make([]int, nbBands),
		RemainderBits: make([]int, nbBands),
		PulseCaps:     make([]int, nbBands),
	}

	if nbBands == 0 || totalBits <= 0 {
		return result
	}

	// Compute pulse caps if not provided
	if cap == nil || len(cap) < nbBands {
		cap = ComputePulseCaps(nbBands, lm)
	}
	copy(result.PulseCaps, cap[:nbBands])

	// Create default dynalloc if not provided
	if dynalloc == nil {
		dynalloc = make([]int, nbBands)
	}

	// Step 1: Determine quality level from total bits
	// Quality roughly maps to bits per band
	// More bits = higher quality level
	avgBitsPerBand := totalBits / nbBands
	quality := avgBitsPerBand // Simple mapping: 1 bit = 1 quality step

	// Clamp quality to valid range for interpolation
	if quality > 80 {
		quality = 80
	}
	if quality < 0 {
		quality = 0
	}

	// Step 2: Get base allocation from quality interpolation
	baseAlloc := interpolateAlloc(quality, nbBands)

	// Step 3: Apply trim adjustment
	applyTrim(baseAlloc, trim, nbBands, lm)

	// Step 4: Apply dynamic allocation
	applyDynalloc(baseAlloc, dynalloc, nbBands)

	// Step 5: Apply caps
	applyCaps(baseAlloc, cap, nbBands)

	// Step 6: Scale to match total budget
	// Sum current allocation
	allocSum := 0
	for band := 0; band < nbBands; band++ {
		allocSum += baseAlloc[band]
	}

	// Scale factor to match budget
	var scaleFactor float64
	if allocSum > 0 {
		scaleFactor = float64(totalBits) / float64(allocSum)
	} else {
		scaleFactor = 1.0
	}

	// Apply scaling
	scaledAlloc := make([]int, nbBands)
	scaledSum := 0
	for band := 0; band < nbBands; band++ {
		scaled := int(float64(baseAlloc[band]) * scaleFactor)
		if scaled < 0 {
			scaled = 0
		}
		if scaled > cap[band] {
			scaled = cap[band]
		}
		scaledAlloc[band] = scaled
		scaledSum += scaled
	}

	// Distribute any remaining bits to bands with room
	remaining := totalBits - scaledSum
	for band := 0; band < nbBands && remaining > 0; band++ {
		room := cap[band] - scaledAlloc[band]
		if room > 0 {
			add := remaining
			if add > room {
				add = room
			}
			scaledAlloc[band] += add
			remaining -= add
		}
	}

	// Step 7: Split between fine energy and PVQ
	stereo := dualStereo || intensity >= 0
	fineBits, pvqBits := computeFineBits(scaledAlloc, nbBands, stereo)

	// Step 8: Handle intensity stereo
	if intensity >= 0 && intensity < nbBands {
		// Above intensity band, both channels share bits
		for band := intensity; band < nbBands; band++ {
			// In intensity stereo, side channel is implied from mid
			// So we effectively double the bits for those bands
			pvqBits[band] = pvqBits[band] * 2 / 3 // Reduce since sharing
		}
	}

	// Copy results
	copy(result.BandBits, pvqBits)
	copy(result.FineBits, fineBits)

	// Compute total
	for band := 0; band < nbBands; band++ {
		result.Total += result.BandBits[band] + result.FineBits[band]
	}

	// Remainder bits: any leftover after PVQ encoding goes back to fine energy
	// This is computed after actual PVQ encoding, so initialize to 0
	for band := 0; band < nbBands; band++ {
		result.RemainderBits[band] = 0
	}

	return result
}

// ComputeAllocationSimple is a simplified allocation for testing.
// Divides bits equally among bands.
func ComputeAllocationSimple(totalBits int, nbBands int) (bandBits, fineBits []int) {
	if nbBands <= 0 {
		return []int{}, []int{}
	}

	bandBits = make([]int, nbBands)
	fineBits = make([]int, nbBands)

	bitsPerBand := totalBits / nbBands
	leftover := totalBits % nbBands

	for band := 0; band < nbBands; band++ {
		bits := bitsPerBand
		if band < leftover {
			bits++ // Distribute remainder to first bands
		}

		// Split: 90% PVQ, 10% fine energy (minimum 1 fine bit)
		fine := bits / 10
		if fine < 1 {
			fine = 1
		}
		if fine > 8 {
			fine = 8
		}

		fineBits[band] = fine
		bandBits[band] = bits - fine
	}

	return bandBits, fineBits
}
