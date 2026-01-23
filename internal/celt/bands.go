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

	// Allocate frameSize for MDCT, not totalBins
	// Upper bins (totalBins to frameSize-1) stay zero (highest frequencies)
	// This ensures IMDCT(frameSize) produces 2*frameSize samples
	coeffs := make([]float64, frameSize)
	if stereo {
		coeffs = make([]float64, frameSize*2) // Double for stereo
	}

	// Track coded bands for folding
	var collapseMask uint32
	bandVectors := make([][]float64, nbBands) // Store decoded vectors for folding

	offset := 0
	for band := 0; band < nbBands; band++ {
		n := ScaledBandWidth(band, frameSize) // Band width in MDCT bins
		if n <= 0 {
			continue
		}

		// Convert bits to pulse count
		k := bitsToK(bandBits[band], n)

		// Trace allocation
		DefaultTracer.TraceAllocation(band, bandBits[band], k)

		var shape []float64
		if k > 0 {
			// Decode PVQ vector for this band (with tracing)
			shape = d.DecodePVQWithTrace(band, n, k)
			UpdateCollapseMask(&collapseMask, band)
		} else {
			// No pulses - fold from lower band
			srcBand := FindFoldSource(band, collapseMask, nil)
			if srcBand >= 0 && bandVectors[srcBand] != nil {
				shape = FoldBand(bandVectors[srcBand], n, &d.rng)
			} else {
				// No source - generate noise
				shape = FoldBand(nil, n, &d.rng)
			}
		}

		// Store vector for potential folding by later bands
		bandVectors[band] = shape

		// Denormalize: scale shape by energy (energy is in dB units).
		// A 6 dB step corresponds to a 2x gain, so gain = 2^(energy/DB6).
		// Clamp energy to prevent overflow (libopus clamps to 32).
		e := energies[band]
		if e > 32 {
			e = 32
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
		DefaultTracer.TraceCoeffs(band, coeffs[offset:traceEnd])

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

	// Allocate frameSize for MDCT, not totalBins
	// Upper bins (totalBins to frameSize-1) stay zero (highest frequencies)
	left = make([]float64, frameSize)
	right = make([]float64, frameSize)

	// Track coded bands for folding
	var collapseMask uint32
	bandVectorsL := make([][]float64, nbBands)
	bandVectorsR := make([][]float64, nbBands)

	offset := 0
	for band := 0; band < nbBands; band++ {
		n := ScaledBandWidth(band, frameSize)
		if n <= 0 {
			continue
		}

		// Convert bits to pulse count
		k := bitsToK(bandBits[band], n)

		var shapeL, shapeR []float64

		if band >= intensity && intensity >= 0 {
			// Intensity stereo: decode mono and duplicate with sign
			if k > 0 {
				shapeMid := d.DecodePVQ(n, k)
				shapeL, shapeR = d.DecodeIntensityStereo(shapeMid)
				UpdateCollapseMask(&collapseMask, band)
			} else {
				// Fold from lower band
				srcBand := FindFoldSource(band, collapseMask, nil)
				var src []float64
				if srcBand >= 0 {
					src = bandVectorsL[srcBand]
				}
				shapeMid := FoldBand(src, n, &d.rng)
				shapeL = make([]float64, n)
				shapeR = make([]float64, n)
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

			shapeMid := d.DecodePVQ(n, kMid)
			var shapeSide []float64
			if kSide > 0 {
				shapeSide = d.DecodePVQ(n, kSide)
			} else {
				shapeSide = make([]float64, n)
			}

			// Decode theta for M/S mixing
			itheta := d.DecodeStereoTheta(8) // 8 quantization steps
			midGain, sideGain := ThetaToGains(itheta, 8)

			// Apply rotation
			shapeL, shapeR = ApplyMidSideRotation(shapeMid, shapeSide, midGain, sideGain)

			UpdateCollapseMask(&collapseMask, band)
		} else {
			// Fold from lower band
			srcBand := FindFoldSource(band, collapseMask, nil)
			var srcL, srcR []float64
			if srcBand >= 0 {
				srcL = bandVectorsL[srcBand]
				srcR = bandVectorsR[srcBand]
			}
			shapeL = FoldBand(srcL, n, &d.rng)
			shapeR = FoldBand(srcR, n, &d.rng)
		}

		// Store vectors for folding
		bandVectorsL[band] = shapeL
		bandVectorsR[band] = shapeR

		// Denormalize: scale by energy (energy is in dB units).
		// Energy is in log2 scale: gain = 2^energy
		// Clamp energy to prevent overflow (libopus clamps to 32)
		eL := energiesL[band]
		if eL > 32 {
			eL = 32
		}
		eR := energiesR[band]
		if eR > 32 {
			eR = 32
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
// bits: number of bits allocated to this band
// n: band width (number of MDCT bins)
// Returns: number of pulses K for PVQ coding.
//
// This is the inverse of the encoding rate computation. The relationship
// between bits and pulses depends on the number of dimensions N and the
// entropy required to code the PVQ index.
//
// Approximate formula: bits ~= N * log2(1 + 2K/N) + K
// We solve for K given bits and N.
//
// Reference: libopus celt/rate.c pulses2bits() / bits2pulses()
func bitsToK(bits, n int) int {
	if bits <= 0 || n <= 0 {
		return 0
	}

	// Minimum bits needed for 1 pulse: approximately log2(V(n,1)) = log2(2n)
	minBits := ilog2(n) + 1
	if bits < minBits {
		return 0
	}

	// Binary search for K
	// V(n,k) = number of codewords = approximately (2k+1)^n / n! for small k
	// bits ~= log2(V(n,k)) = n*log2(2k/n + 1) + log2(n!)
	lo := 0
	hi := bits // Upper bound: can't have more pulses than bits

	if hi > 128 {
		hi = 128 // Cap at reasonable maximum
	}

	for lo < hi {
		mid := (lo + hi + 1) / 2
		b := kToBits(mid, n)
		if b <= bits {
			lo = mid
		} else {
			hi = mid - 1
		}
	}

	return lo
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

	// Approximate using log2(V(n,k))
	// V(n,k) can be computed but we use approximation for speed
	// bits ~= n * log2(1 + 2k/n) + k (sign bits)

	// For small k: V(n,k) ~= (2k+1)^n / n!
	// More accurate: use actual V computation for small values

	v := PVQ_V(n, k)
	if v <= 1 {
		return 0
	}

	// log2(v)
	return ilog2(int(v - 1))
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
// energy: band energy in dB units (6 dB per doubling)
// Returns: denormalized MDCT coefficients.
//
// This matches libopus celt/bands.c denormalise_bands().
func DenormalizeBand(shape []float64, energy float64) []float64 {
	if len(shape) == 0 {
		return nil
	}

	// Clamp energy to prevent overflow (libopus clamps to 32)
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

// ComputeBandEnergy computes the L2 energy of a band in dB units.
// coeffs: MDCT coefficients for the band
// Returns: energy = DB6 * log2(sqrt(sum(x^2)))
func ComputeBandEnergy(coeffs []float64) float64 {
	if len(coeffs) == 0 {
		return -28.0 // Default low energy
	}

	var energy float64
	for _, x := range coeffs {
		energy += x * x
	}

	if energy < 1e-15 {
		return -28.0
	}

	// log2(sqrt(energy)) = 0.5 * log2(energy) = 0.5 * ln(energy) / ln(2)
	// Convert to dB units: 6 dB per doubling.
	return DB6 * (0.5 * math.Log(energy) / 0.6931471805599453)
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
