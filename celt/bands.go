package celt

import "math"

// Band processing orchestration for CELT decoding.
// This file contains the top-level band decoding loop that processes all
// frequency bands, applying PVQ decoding for coded bands and folding for
// uncoded bands, then denormalizing by energy.
//
// Reference: RFC 6716 Section 4.3.4, libopus celt/bands.c quant_all_bands()

// DecodeBands decodes all frequency bands from the bitstream.
// energies: per-band energy values (from coarse + fine energy decoding)
// bandBits: bits allocated per band (from bit allocation)
// nbBands: number of bands to decode
// stereo: true if stereo mode
// frameSize: frame size in samples (120, 240, 480, 960)
// Returns: MDCT coefficients (denormalized) of length frameSize.
//
// The output is zero-padded to frameSize. Band coefficients fill bins 0 to totalBins-1,
// where totalBins = sum(ScaledBandWidth(i, frameSize) for i in 0..nbBands-1).
// Upper bins (totalBins to frameSize-1) remain zero, representing highest frequencies.
// This ensures IMDCT receives exactly frameSize coefficients, producing correct sample count.
func (d *Decoder) DecodeBands(
	energies []float64,
	bandBits []int,
	nbBands int,
	stereo bool,
	frameSize int,
) []float64 {
	if nbBands <= 0 || nbBands > MaxBands {
		return nil
	}
	if len(energies) < nbBands {
		return nil
	}
	if len(bandBits) < nbBands {
		return nil
	}

	// Calculate total bins from bands (for band processing)
	totalBins := 0
	for band := 0; band < nbBands; band++ {
		totalBins += ScaledBandWidth(band, frameSize)
	}

	if totalBins <= 0 {
		return nil
	}

	// Use pre-allocated coeffs buffer from scratch space
	// Upper bins (totalBins to frameSize-1) stay zero (highest frequencies)
	// This ensures IMDCT(frameSize) produces 2*frameSize samples
	coeffsSize := frameSize
	if stereo {
		coeffsSize = frameSize * 2 // Double for stereo
	}
	coeffs := d.scratchBands.ensureCoeffs(coeffsSize)
	// Zero the buffer (required since we reuse it)
	for i := range coeffs {
		coeffs[i] = 0
	}

	// Track coded bands for folding
	var collapseMask uint32
	// Use pre-allocated band vectors slice from scratch space
	bandVectors := d.scratchBands.ensureBandVectors(nbBands)

	offset := 0
	for band := 0; band < nbBands; band++ {
		n := ScaledBandWidth(band, frameSize) // Band width in MDCT bins
		if n <= 0 {
			continue
		}

		// Convert bits to pulse count
		k := bitsToK(bandBits[band], n)

		// Trace allocation
		traceAllocation(band, bandBits[band], k)

		// Get pre-allocated storage for this band's shape vector
		shape := d.scratchBands.getBandStorage(band, n)
		if k > 0 {
			// Decode PVQ vector for this band (with tracing) into pre-allocated buffer
			d.decodePVQInto(band, n, k, shape)
			UpdateCollapseMask(&collapseMask, band)
		} else {
			// No pulses - fold from lower band into pre-allocated buffer
			srcBand := FindFoldSource(band, collapseMask, nil)
			if srcBand >= 0 && bandVectors[srcBand] != nil {
				d.foldBandInto(bandVectors[srcBand], n, shape)
			} else {
				// No source - generate noise into pre-allocated buffer
				d.foldBandInto(nil, n, shape)
			}
		}

		// Store vector reference for potential folding by later bands
		bandVectors[band] = shape

		// Denormalize: scale shape by energy (log2 units, 1 = 6 dB).
		// Add per-band mean energy (log2 units) to recover absolute level.
		// Clamp energy to prevent overflow (libopus clamps to 32).
		e := energies[band]
		if band < len(eMeans) {
			e += eMeans[band] * DB6
		}
		if e > 32*DB6 {
			e = 32 * DB6
		}
		gain := math.Exp2(e / DB6)

		// Apply gain to shape and write to output
		for i := 0; i < n && i < len(shape); i++ {
			coeffs[offset+i] = shape[i] * gain
		}

		// Trace denormalized coefficients
		traceEnd := offset + n
		if traceEnd > len(coeffs) {
			traceEnd = len(coeffs)
		}
		traceCoeffs(band, coeffs[offset:traceEnd])

		offset += n
	}

	// Store collapse mask in decoder state (for anti-collapse in next frame)
	d.collapseMask = collapseMask

	return coeffs
}

// DecodeBandsStereo decodes all frequency bands in stereo mode.
// energiesL/R: per-band energies for left/right channels
// bandBits: bits allocated per band
// nbBands: number of bands
// frameSize: frame size in samples
// intensity: intensity stereo start band (-1 if not used)
// Returns: left and right channel MDCT coefficients, each of length frameSize.
//
// In stereo mode, bands can use:
// 1. Dual stereo: separate PVQ vectors for L and R
// 2. Mid-side: decode mid/side and rotate to L/R
// 3. Intensity: copy mono to both with sign flag
//
// Output is zero-padded to frameSize per channel for correct IMDCT operation.
//
// Reference: libopus celt/bands.c quant_all_bands() stereo path
func (d *Decoder) DecodeBandsStereo(
	energiesL, energiesR []float64,
	bandBits []int,
	nbBands int,
	frameSize int,
	intensity int,
) (left, right []float64) {
	if nbBands <= 0 || nbBands > MaxBands {
		return nil, nil
	}

	// Calculate total bins from bands (for band processing)
	totalBins := 0
	for band := 0; band < nbBands; band++ {
		totalBins += ScaledBandWidth(band, frameSize)
	}

	if totalBins <= 0 {
		return nil, nil
	}

	// Use pre-allocated buffers from scratch space
	// Upper bins (totalBins to frameSize-1) stay zero (highest frequencies)
	left = d.scratchBands.ensureCoeffs(frameSize * 2)[:frameSize]
	right = left[frameSize : frameSize*2]
	// Zero the buffers (required since we reuse them)
	for i := range left {
		left[i] = 0
	}
	for i := range right {
		right[i] = 0
	}

	// Track coded bands for folding
	var collapseMask uint32
	// Use pre-allocated band vectors slices from scratch space
	bandVectorsL, bandVectorsR := d.scratchBands.ensureBandVectorsStereo(nbBands)

	offset := 0
	for band := 0; band < nbBands; band++ {
		n := ScaledBandWidth(band, frameSize)
		if n <= 0 {
			continue
		}

		// Convert bits to pulse count
		k := bitsToK(bandBits[band], n)
		// Trace allocation
		traceAllocation(band, bandBits[band], k)

		// Get pre-allocated storage for this band's shape vectors
		shapeL := d.scratchBands.getBandStorageL(band, n)
		shapeR := d.scratchBands.getBandStorageR(band, n)

		if band >= intensity && intensity >= 0 {
			// Intensity stereo: decode mono and duplicate with sign
			if k > 0 {
				// Use pvqNorm as temporary for mid shape
				shapeMid := d.scratchBands.ensurePVQNorm(n)
				d.decodePVQInto(band, n, k, shapeMid)
				d.decodeIntensityStereoInto(shapeMid, shapeL, shapeR)
				UpdateCollapseMask(&collapseMask, band)
			} else {
				// Fold from lower band
				srcBand := FindFoldSource(band, collapseMask, nil)
				var src []float64
				if srcBand >= 0 {
					src = bandVectorsL[srcBand]
				}
				// Use foldResult as temporary for mid shape
				shapeMid := d.scratchBands.ensureFoldResult(n)
				d.foldBandInto(src, n, shapeMid)
				copy(shapeL, shapeMid)
				copy(shapeR, shapeMid)
			}
		} else if k > 0 {
			// Mid-side or dual stereo
			// For simplicity, use mid-side when bits are allocated
			// Decode mid shape
			kMid := k / 2
			if kMid < 1 {
				kMid = 1
			}
			kSide := k - kMid

			// Use pvqNorm as temporary for mid shape
			shapeMid := d.scratchBands.ensurePVQNorm(n)
			d.decodePVQInto(band, n, kMid, shapeMid)

			// Use foldResult as temporary for side shape
			shapeSide := d.scratchBands.ensureFoldResult(n)
			if kSide > 0 {
				d.decodePVQInto(band, n, kSide, shapeSide)
			} else {
				for i := 0; i < n; i++ {
					shapeSide[i] = 0
				}
			}

			// Decode theta for M/S mixing
			itheta := d.DecodeStereoTheta(8) // 8 quantization steps
			midGain, sideGain := ThetaToGains(itheta, 8)

			// Apply rotation directly into pre-allocated buffers
			applyMidSideRotationInto(shapeMid, shapeSide, midGain, sideGain, shapeL, shapeR)

			UpdateCollapseMask(&collapseMask, band)
		} else {
			// Fold from lower band into pre-allocated buffers
			srcBand := FindFoldSource(band, collapseMask, nil)
			var srcL, srcR []float64
			if srcBand >= 0 {
				srcL = bandVectorsL[srcBand]
				srcR = bandVectorsR[srcBand]
			}
			d.foldBandInto(srcL, n, shapeL)
			d.foldBandInto(srcR, n, shapeR)
		}

		// Store vector references for folding
		bandVectorsL[band] = shapeL
		bandVectorsR[band] = shapeR

		// Denormalize: scale by energy (log2 units, 1 = 6 dB).
		// Add per-band mean energy (log2 units) to recover absolute level.
		// Clamp energy to prevent overflow (libopus clamps to 32).
		eL := energiesL[band]
		if band < len(eMeans) {
			eL += eMeans[band] * DB6
		}
		if eL > 32*DB6 {
			eL = 32 * DB6
		}
		eR := energiesR[band]
		if band < len(eMeans) {
			eR += eMeans[band] * DB6
		}
		if eR > 32*DB6 {
			eR = 32 * DB6
		}
		gainL := math.Exp2(eL / DB6)
		gainR := math.Exp2(eR / DB6)

		for i := 0; i < n && i < len(shapeL); i++ {
			left[offset+i] = shapeL[i] * gainL
		}
		for i := 0; i < n && i < len(shapeR); i++ {
			right[offset+i] = shapeR[i] * gainR
		}

		offset += n
	}

	d.collapseMask = collapseMask

	return left, right
}

// bitsToK computes the number of PVQ pulses from bit allocation.
// bits: number of bits allocated to this band in Q3
// n: band width (number of MDCT bins)
// Returns: number of pulses K for PVQ coding.
//
// This mirrors libopus bits2pulses() and get_pulses() using a cached
// PVQ rate table for the given band width.
//
// Reference: libopus celt/rate.h bits2pulses() / get_pulses()
func bitsToK(bits, n int) int {
	if bits <= 0 || n <= 0 {
		return 0
	}
	band, lm, ok := bandFromWidth(n)
	if !ok {
		return 0
	}
	q := bitsToPulses(band, lm, bits)
	return getPulses(q)
}

const maxBandWidthLookup = 1024

var (
	bandByWidth [maxBandWidthLookup + 1]int8
	lmByWidth   [maxBandWidthLookup + 1]int8
)

func init() {
	for i := range bandByWidth {
		bandByWidth[i] = -1
		lmByWidth[i] = -1
	}
	// Preserve existing lookup behavior: first match wins with lm ascending.
	for lm := 0; lm <= 3; lm++ {
		for band := 0; band < MaxBands; band++ {
			width := (EBands[band+1] - EBands[band]) << lm
			if width <= 0 || width > maxBandWidthLookup {
				continue
			}
			if bandByWidth[width] < 0 {
				bandByWidth[width] = int8(band)
				lmByWidth[width] = int8(lm)
			}
		}
	}
}

func bandFromWidth(width int) (band int, lm int, ok bool) {
	if width <= 0 {
		return 0, 0, false
	}
	if width <= maxBandWidthLookup {
		b := bandByWidth[width]
		if b >= 0 {
			return int(b), int(lmByWidth[width]), true
		}
	}
	for lm = 0; lm <= 3; lm++ {
		for band = 0; band < MaxBands; band++ {
			if (EBands[band+1]-EBands[band])<<lm == width {
				return band, lm, true
			}
		}
	}
	return 0, 0, false
}

// kToBits computes the approximate bits needed to code K pulses in N dimensions.
// k: number of pulses
// n: number of dimensions
// Returns: approximate bits needed.
//
// Reference: libopus celt/rate.c pulses2bits()
func kToBits(k, n int) int {
	if k <= 0 {
		return 0
	}
	if n <= 0 {
		return 0
	}

	// For small values, use exact codebook size.
	if k <= 8 && n <= 16 {
		v := PVQ_V(n, k)
		if v <= 1 {
			return 0
		}
		return ilog2(int(v - 1))
	}

	// Approximate for larger values to avoid PVQ_V overflow.
	// bits ~= n * log2(1 + 2k/n) + k (sign bits)
	bits := float64(n)*math.Log2(1.0+2.0*float64(k)/float64(n)) + float64(k)
	if bits < 0 {
		return 0
	}
	return int(bits + 0.5)
}

// ilog2 returns floor(log2(x)) for x > 0, or 0 for x <= 0.
func ilog2(x int) int {
	if x <= 0 {
		return 0
	}
	n := 0
	for x > 1 {
		x >>= 1
		n++
	}
	return n
}

// DenormalizeBand scales a normalized band vector by its energy.
// shape: normalized vector (unit L2 norm)
// energy: band energy in log2 units (1 = 6 dB)
// Returns: denormalized MDCT coefficients.
//
// This matches libopus celt/bands.c denormalise_bands().
func DenormalizeBand(shape []float64, energy float64) []float64 {
	if len(shape) == 0 {
		return nil
	}

	// Clamp energy to prevent overflow (libopus clamps to 32).
	e := energy
	if e > 32 {
		e = 32
	}
	gain := math.Exp2(e / DB6)
	result := make([]float64, len(shape))
	for i, x := range shape {
		result[i] = x * gain
	}
	return result
}

func denormalizeCoeffs(coeffs []float64, energies []float64, nbBands, frameSize int) {
	if len(coeffs) == 0 || len(energies) == 0 || nbBands <= 0 {
		return
	}
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if len(energies) < nbBands {
		nbBands = len(energies)
	}

	coeffsLen := len(coeffs)
	coeffs = coeffs[:coeffsLen:coeffsLen]
	_ = coeffs[coeffsLen-1]
	offset := 0
	for band := 0; band < nbBands; band++ {
		width := ScaledBandWidth(band, frameSize)
		if width <= 0 {
			continue
		}
		e := energies[band]
		if band < len(eMeans) {
			e += eMeans[band] * DB6
		}
		if e > 32 {
			e = 32
		}
		gain := math.Exp2(e / DB6)
		end := offset + width
		if end > coeffsLen {
			end = coeffsLen
		}
		i := offset
		for ; i+3 < end; i += 4 {
			coeffs[i] *= gain
			coeffs[i+1] *= gain
			coeffs[i+2] *= gain
			coeffs[i+3] *= gain
		}
		for ; i < end; i++ {
			coeffs[i] *= gain
		}
		traceEnd := end
		if traceEnd > offset {
			traceCoeffs(band, coeffs[offset:traceEnd])
		}
		offset += width
	}
}

// ComputeBandEnergy computes the per-band log2 amplitude.
// coeffs: MDCT coefficients for the band
// Returns: log2(sqrt(sum(x^2))) with libopus epsilon
func ComputeBandEnergy(coeffs []float64) float64 {
	if len(coeffs) == 0 {
		return 0.5 * math.Log2(1e-27)
	}

	sumSq := float32(1e-27)
	for _, x := range coeffs {
		v := float32(x)
		sumSq += v * v
	}

	// log2(sqrt(sumSq)) = log2(amp) with libopus FLOAT_APPROX log2.
	amp := float32(math.Sqrt(float64(sumSq)))
	return float64(celtLog2(amp))
}

// InterleaveBands interleaves band coefficients for transient frames.
// bands: slice of band vectors
// shortBlocks: number of short MDCT blocks
// Returns: interleaved coefficient array.
//
// In transient mode, CELT uses multiple short MDCTs instead of one long MDCT.
// The coefficients are interleaved so that each short block can be processed.
//
// Reference: libopus celt/celt_decoder.c, transient mode
func InterleaveBands(bands [][]float64, shortBlocks int) []float64 {
	if len(bands) == 0 || shortBlocks <= 1 {
		// No interleaving needed
		total := 0
		for _, b := range bands {
			total += len(b)
		}
		result := make([]float64, total)
		offset := 0
		for _, b := range bands {
			copy(result[offset:], b)
			offset += len(b)
		}
		return result
	}

	// Calculate total size and size per short block
	total := 0
	for _, b := range bands {
		total += len(b)
	}

	if total%shortBlocks != 0 {
		// Not divisible - fall back to simple concatenation
		result := make([]float64, total)
		offset := 0
		for _, b := range bands {
			copy(result[offset:], b)
			offset += len(b)
		}
		return result
	}

	blockSize := total / shortBlocks
	result := make([]float64, total)

	// Interleave coefficients
	flatIdx := 0
	for _, b := range bands {
		for i, x := range b {
			// Determine which short block this bin belongs to
			block := i % shortBlocks
			pos := i / shortBlocks

			// Output position
			outIdx := block*blockSize + pos + flatIdx/shortBlocks
			if outIdx < len(result) {
				result[outIdx] = x
			}
		}
		flatIdx += len(b)
	}

	return result
}
