// Package celt implements the CELT decoder per RFC 6716 Section 4.3.
package celt

import "github.com/thesyncim/gopus/rangecoding"

// Laplace decoding constants per libopus celt/laplace.c.
const (
	laplaceLogMinP = 0
	laplaceMinP    = 1 << laplaceLogMinP
	laplaceNMin    = 16
	laplaceFTBits  = 15
	laplaceFS      = 1 << laplaceFTBits
)

// ec_laplace_get_freq1 returns the frequency of the "1" symbol.
// Reference: libopus celt/laplace.c
func ec_laplace_get_freq1(fs0 int, decay int) int {
	ft := laplaceFS - laplaceMinP*(2*laplaceNMin) - fs0
	return (ft * (16384 - decay)) >> 15
}

// DecodeLaplaceTest is an exported wrapper for testing.
//
// This helper exists for tests and codec-development tooling and may change.
// It decodes a Laplace-distributed integer using the range coder.
func (d *Decoder) DecodeLaplaceTest(fs int, decay int) int {
	return d.decodeLaplace(fs, decay)
}

// decodeLaplace decodes a Laplace-distributed integer using the range coder.
// Uses the probability model from RFC 6716 Section 4.3.2.1.
// Parameters:
//   - fs: total frequency (typically 32768)
//   - decay: controls the distribution spread (larger = narrower around 0)
//
// Reference: libopus celt/laplace.c ec_laplace_decode()
func (d *Decoder) decodeLaplace(fs int, decay int) int {
	rd := d.rangeDecoder
	if rd == nil {
		return 0
	}
	return decodeLaplaceWithRangeDecoder(rd, fs, decay)
}

func decodeLaplaceWithRangeDecoder(rd *rangecoding.Decoder, fs int, decay int) int {
	fm := int(rd.DecodeBin(laplaceFTBits))
	if fm < fs {
		rd.Update(0, uint32(fs), uint32(laplaceFS))
		return 0
	}

	val := 1
	fl := fs
	fs = ec_laplace_get_freq1(fs, decay) + laplaceMinP
	for fs > laplaceMinP && fm >= fl+2*fs {
		fs *= 2
		fl += fs
		fs = ((fs - 2*laplaceMinP) * decay >> 15) + laplaceMinP
		val++
	}
	if fs <= laplaceMinP {
		di := (fm - fl) >> (laplaceLogMinP + 1)
		val += di
		fl += 2 * di * laplaceMinP
	}
	if fm < fl+fs {
		val = -val
	} else {
		fl += fs
	}
	fh := fl + fs
	if fh > laplaceFS {
		fh = laplaceFS
	}
	rd.Update(uint32(fl), uint32(fh), uint32(laplaceFS))
	return val
}

// decodeBin mirrors libopus ec_decode_bin() without updating the range coder.
func (d *Decoder) decodeBin(bits uint) uint32 {
	rd := d.rangeDecoder
	if rd == nil || bits == 0 {
		return 0
	}

	rng := rd.Range()
	ext := rng >> bits
	if ext == 0 {
		ext = 1
	}
	s := rd.Val() / ext
	top := uint32(1) << bits
	if s+1 > top {
		s = top - 1
	}
	return top - (s + 1)
}

// updateRange updates the range decoder state after decoding a symbol.
// fl: cumulative frequency of symbols before this one
// fh: cumulative frequency up to and including this symbol
// ft: total frequency
func (d *Decoder) updateRange(fl, fh, ft uint32) {
	rd := d.rangeDecoder
	if rd == nil {
		return
	}
	rd.DecodeSymbol(fl, fh, ft)
}

// DecodeCoarseEnergy decodes coarse band energies in log2 units (1 = 6 dB).
// intra=true: no inter-frame prediction (first frame or after loss)
// intra=false: uses alpha prediction from previous frame
// Reference: RFC 6716 Section 4.3.2, libopus celt/quant_bands.c unquant_coarse_energy()
func (d *Decoder) DecodeCoarseEnergy(nbBands int, intra bool, lm int) []float64 {
	energies := make([]float64, nbBands*d.channels)
	return d.decodeCoarseEnergyInto(energies, nbBands, intra, lm)
}

func (d *Decoder) decodeCoarseEnergyInto(dst []float64, nbBands int, intra bool, lm int) []float64 {
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

	needed := nbBands * d.channels
	if len(dst) < needed {
		dst = make([]float64, needed)
	} else {
		dst = dst[:needed]
	}

	rd := d.rangeDecoder
	if rd == nil {
		return dst
	}

	// Get prediction coefficients
	var alpha, beta float32
	if intra {
		alpha = 0.0
		beta = float32(BetaIntra)
	} else {
		alpha = float32(AlphaCoef[lm])
		beta = float32(BetaCoefInter[lm])
	}

	prob := eProbModel[lm][0]
	if intra {
		prob = eProbModel[lm][1]
	}

	budget := rd.StorageBits()

	// Decode band-major to match libopus ordering.
	var prevBandEnergy [2]float32
	for band := 0; band < nbBands; band++ {
		for c := 0; c < d.channels; c++ {
			// Decode Laplace-distributed residual
			tell := rd.Tell()
			qi := 0
			remaining := budget - tell
			if remaining >= 15 {
				pi := 2 * band
				if pi > 40 {
					pi = 40
				}
				fs := int(prob[pi]) << 7
				decay := int(prob[pi+1]) << 6
				qi = decodeLaplaceWithRangeDecoder(rd, fs, decay)
			} else if remaining >= 2 {
				qi = rd.DecodeICDF(smallEnergyICDF, 2)
				qi = (qi >> 1) ^ -(qi & 1)
			} else if remaining >= 1 {
				qi = -rd.DecodeBit(1)
			} else {
				qi = -1
			}

			// Apply prediction
			// pred = alpha * prevEnergy[band] + prevBandEnergy
			prevFrameEnergy := float32(d.prevEnergy[c*MaxBands+band])
			minEnergy := float32(-9.0 * DB6)
			if prevFrameEnergy < minEnergy {
				prevFrameEnergy = minEnergy
			}

			// Compute energy: pred + qi * DB6 (6 dB per step)
			q := float32(qi) * float32(DB6)
			energy := alpha*prevFrameEnergy + prevBandEnergy[c] + q

			// Store result
			dst[c*nbBands+band] = float64(energy)

			// Update prev band energy for next band's inter-band prediction.
			// Per libopus: prev is filtered by the quantized delta.
			// Formula: prev = prev + q - beta*q, where q = qi*DB6
			prevBandEnergy[c] = prevBandEnergy[c] + q - beta*q
		}
	}

	// Update previous frame energy for next frame's inter-frame prediction
	for c := 0; c < d.channels; c++ {
		for band := 0; band < nbBands; band++ {
			d.prevEnergy[c*MaxBands+band] = celtGLog(dst[c*nbBands+band])
		}
	}

	return dst
}

func (d *Decoder) decodeCoarseEnergyGLogInto(dst []celtGLog, nbBands int, intra bool, lm int) []celtGLog {
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

	needed := nbBands * d.channels
	if len(dst) < needed {
		dst = make([]celtGLog, needed)
	} else {
		dst = dst[:needed]
	}

	rd := d.rangeDecoder
	if rd == nil {
		return dst
	}

	// Get prediction coefficients
	var alpha, beta float32
	if intra {
		alpha = 0.0
		beta = float32(BetaIntra)
	} else {
		alpha = float32(AlphaCoef[lm])
		beta = float32(BetaCoefInter[lm])
	}

	prob := eProbModel[lm][0]
	if intra {
		prob = eProbModel[lm][1]
	}

	budget := rd.StorageBits()

	// Decode band-major to match libopus ordering.
	var prevBandEnergy [2]float32
	for band := 0; band < nbBands; band++ {
		for c := 0; c < d.channels; c++ {
			// Decode Laplace-distributed residual
			tell := rd.Tell()
			qi := 0
			remaining := budget - tell
			if remaining >= 15 {
				pi := 2 * band
				if pi > 40 {
					pi = 40
				}
				fs := int(prob[pi]) << 7
				decay := int(prob[pi+1]) << 6
				qi = decodeLaplaceWithRangeDecoder(rd, fs, decay)
			} else if remaining >= 2 {
				qi = rd.DecodeICDF(smallEnergyICDF, 2)
				qi = (qi >> 1) ^ -(qi & 1)
			} else if remaining >= 1 {
				qi = -rd.DecodeBit(1)
			} else {
				qi = -1
			}

			// Apply prediction
			// pred = alpha * prevEnergy[band] + prevBandEnergy
			prevFrameEnergy := float32(d.prevEnergy[c*MaxBands+band])
			minEnergy := float32(-9.0 * DB6)
			if prevFrameEnergy < minEnergy {
				prevFrameEnergy = minEnergy
			}

			// Compute energy: pred + qi * DB6 (6 dB per step)
			q := float32(qi) * float32(DB6)
			energy := alpha*prevFrameEnergy + prevBandEnergy[c] + q

			// Store result
			dst[c*nbBands+band] = celtGLog(energy)

			// Update prev band energy for next band's inter-band prediction.
			// Per libopus: prev is filtered by the quantized delta.
			// Formula: prev = prev + q - beta*q, where q = qi*DB6
			prevBandEnergy[c] = prevBandEnergy[c] + q - beta*q
		}
	}

	// Update previous frame energy for next frame's inter-frame prediction
	for c := 0; c < d.channels; c++ {
		for band := 0; band < nbBands; band++ {
			d.prevEnergy[c*MaxBands+band] = dst[c*nbBands+band]
		}
	}

	return dst
}

func (d *Decoder) decodeCoarseEnergyRangeGLog(start, end int, intra bool, lm int, energies []celtGLog) {
	if d.rangeDecoder == nil {
		return
	}
	if start < 0 {
		start = 0
	}
	if end > MaxBands {
		end = MaxBands
	}
	if end <= start {
		return
	}
	if lm < 0 {
		lm = 0
	}
	if lm > 3 {
		lm = 3
	}
	if len(energies) < end*d.channels {
		return
	}

	rd := d.rangeDecoder

	// Prediction coefficients
	var alpha, beta float32
	if intra {
		alpha = 0.0
		beta = float32(BetaIntra)
	} else {
		alpha = float32(AlphaCoef[lm])
		beta = float32(BetaCoefInter[lm])
	}

	prob := eProbModel[lm][0]
	if intra {
		prob = eProbModel[lm][1]
	}

	budget := rd.StorageBits()

	// Inter-band prediction state starts at 0 (matches libopus).
	var prevBandEnergy [2]float32
	for band := start; band < end; band++ {
		for c := 0; c < d.channels; c++ {
			tell := rd.Tell()
			qi := 0
			remaining := budget - tell
			if remaining >= 15 {
				pi := 2 * band
				if pi > 40 {
					pi = 40
				}
				fs := int(prob[pi]) << 7
				decay := int(prob[pi+1]) << 6
				qi = d.decodeLaplace(fs, decay)
			} else if remaining >= 2 {
				qi = rd.DecodeICDF(smallEnergyICDF, 2)
				qi = (qi >> 1) ^ -(qi & 1)
			} else if remaining >= 1 {
				qi = -rd.DecodeBit(1)
			} else {
				qi = -1
			}

			prevFrameEnergy := float32(d.prevEnergy[c*MaxBands+band])
			minEnergy := float32(-9.0 * DB6)
			if prevFrameEnergy < minEnergy {
				prevFrameEnergy = minEnergy
			}

			q := float32(qi) * float32(DB6)
			energy := alpha*prevFrameEnergy + prevBandEnergy[c] + q

			energies[c*end+band] = celtGLog(energy)
			prevBandEnergy[c] = prevBandEnergy[c] + q - beta*q
		}
	}
}

// DecodeCoarseEnergyWithDecoder decodes coarse energies using an explicit range decoder.
// This variant allows passing a range decoder directly rather than using d.rangeDecoder.
func (d *Decoder) DecodeCoarseEnergyWithDecoder(rd *rangecoding.Decoder, nbBands int, intra bool, lm int) []float64 {
	// Temporarily set range decoder
	oldRD := d.rangeDecoder
	d.rangeDecoder = rd
	defer func() { d.rangeDecoder = oldRD }()

	return d.DecodeCoarseEnergy(nbBands, intra, lm)
}

// DecodeFineEnergy adds fine energy precision to coarse values.
// fineBits[band] specifies bits allocated for refinement (0 = no refinement).
// Reference: RFC 6716 Section 4.3.2, libopus celt/quant_bands.c unquant_fine_energy()
func (d *Decoder) DecodeFineEnergy(energies []float64, nbBands int, fineBits []int) {
	d.decodeFineEnergyRange(energies, 0, nbBands, nil, fineBits)
}

// DecodeFineEnergyWithDecoder adds fine energy precision using an explicit range decoder.
func (d *Decoder) DecodeFineEnergyWithDecoder(rd *rangecoding.Decoder, energies []float64, nbBands int, fineBits []int) {
	oldRD := d.rangeDecoder
	d.rangeDecoder = rd
	defer func() { d.rangeDecoder = oldRD }()

	d.decodeFineEnergyRange(energies, 0, nbBands, nil, fineBits)
}

func (d *Decoder) decodeFineEnergyGLogWithDecoder(rd *rangecoding.Decoder, energies []celtGLog, nbBands int, fineBits []int) {
	oldRD := d.rangeDecoder
	d.rangeDecoder = rd
	defer func() { d.rangeDecoder = oldRD }()

	d.decodeFineEnergyGLogRange(energies, 0, nbBands, nil, fineBits)
}

// DecodeFineEnergyRange adds fine energy precision for bands in [start, end).
// For hybrid mode, start should be HybridCELTStartBand (17), matching libopus
// unquant_fine_energy().
func (d *Decoder) DecodeFineEnergyRange(energies []float64, start, end int, fineBits []int) {
	d.decodeFineEnergyRange(energies, start, end, nil, fineBits)
}

// decodeFineEnergy mirrors libopus unquant_fine_energy() for float builds.
// prevQuant may be nil; extraQuant provides per-band refinement bits.
func (d *Decoder) decodeFineEnergy(energies []float64, nbBands int, prevQuant, extraQuant []int) {
	d.decodeFineEnergyRange(energies, 0, nbBands, prevQuant, extraQuant)
}

func (d *Decoder) decodeFineEnergyGLog(energies []celtGLog, nbBands int, prevQuant, extraQuant []int) {
	d.decodeFineEnergyGLogRange(energies, 0, nbBands, prevQuant, extraQuant)
}

// decodeFineEnergyRange mirrors libopus unquant_fine_energy() for float builds
// over the supplied band range.
func (d *Decoder) decodeFineEnergyRange(energies []float64, start, end int, prevQuant, extraQuant []int) {
	if d.rangeDecoder == nil {
		return
	}
	if start < 0 {
		start = 0
	}
	if end > MaxBands {
		end = MaxBands
	}
	if end > len(extraQuant) {
		end = len(extraQuant)
	}
	if end <= start {
		return
	}

	rd := d.rangeDecoder
	for band := start; band < end; band++ {
		extra := extraQuant[band]
		if extra <= 0 {
			continue
		}
		if rd.Tell()+d.channels*extra > rd.StorageBits() {
			continue
		}

		prev := 0
		if prevQuant != nil && band < len(prevQuant) {
			prev = prevQuant[band]
		}

		for c := 0; c < d.channels; c++ {
			q2 := rd.DecodeRawBits(uint(extra))
			offset := (float32(q2)+float32(0.5))*float32(uint(1)<<uint(14-extra))*float32(1.0/16384.0) - float32(0.5)
			offset *= float32(uint(1)<<uint(14-prev)) * float32(1.0/16384.0)

			idx := c*end + band
			if idx < len(energies) {
				energies[idx] = float64(float32(energies[idx]) + offset)
			}
		}
	}
}

func (d *Decoder) decodeFineEnergyGLogRange(energies []celtGLog, start, end int, prevQuant, extraQuant []int) {
	if d.rangeDecoder == nil {
		return
	}
	if start < 0 {
		start = 0
	}
	if end > MaxBands {
		end = MaxBands
	}
	if end > len(extraQuant) {
		end = len(extraQuant)
	}
	if end <= start {
		return
	}

	rd := d.rangeDecoder
	for band := start; band < end; band++ {
		extra := extraQuant[band]
		if extra <= 0 {
			continue
		}
		if rd.Tell()+d.channels*extra > rd.StorageBits() {
			continue
		}

		prev := 0
		if prevQuant != nil && band < len(prevQuant) {
			prev = prevQuant[band]
		}

		for c := 0; c < d.channels; c++ {
			q2 := rd.DecodeRawBits(uint(extra))
			offset := (float32(q2)+float32(0.5))*float32(uint(1)<<uint(14-extra))*float32(1.0/16384.0) - float32(0.5)
			offset *= float32(uint(1)<<uint(14-prev)) * float32(1.0/16384.0)

			idx := c*end + band
			if idx < len(energies) {
				energies[idx] = celtGLog(float32(energies[idx]) + offset)
			}
		}
	}
}

// DecodeEnergyRemainder uses remaining bits for additional energy precision.
// Called after all PVQ bands decoded, uses leftover bits from bit allocation.
// Reference: RFC 6716 Section 4.3.2, libopus celt/quant_bands.c unquant_energy_finalise()
func (d *Decoder) DecodeEnergyRemainder(energies []float64, nbBands int, remainderBits []int) {
	if d.rangeDecoder == nil {
		return
	}
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands > len(remainderBits) {
		nbBands = len(remainderBits)
	}

	for c := 0; c < d.channels; c++ {
		for band := 0; band < nbBands; band++ {
			bits := remainderBits[band]
			if bits <= 0 {
				continue
			}

			// Remainder bits provide even finer precision
			// Each bit halves the remaining quantization interval

			// Decode single bit for each remainder bit
			for i := 0; i < bits && i < 8; i++ {
				bit := d.rangeDecoder.DecodeBit(1)

				// Each bit provides 6dB / 2^(fineBits+i+1) precision
				// The precision gets finer with each additional bit
				precision := DB6 / float64(uint(1)<<(i+2))

				idx := c*nbBands + band
				if idx < len(energies) {
					if bit == 1 {
						energies[idx] += precision
					} else {
						energies[idx] -= precision
					}
				}
			}
		}
	}
}

// DecodeEnergyRemainderWithDecoder uses remainder bits with an explicit range decoder.
func (d *Decoder) DecodeEnergyRemainderWithDecoder(rd *rangecoding.Decoder, energies []float64, nbBands int, remainderBits []int) {
	oldRD := d.rangeDecoder
	d.rangeDecoder = rd
	defer func() { d.rangeDecoder = oldRD }()

	d.DecodeEnergyRemainder(energies, nbBands, remainderBits)
}

// DecodeEnergyFinalise consumes leftover bits for additional energy refinement.
// This mirrors libopus unquant_energy_finalise().
// For non-hybrid mode, use start=0.
func (d *Decoder) DecodeEnergyFinalise(energies []float64, nbBands int, fineQuant []int, finePriority []int, bitsLeft int) {
	d.DecodeEnergyFinaliseRange(0, nbBands, energies, fineQuant, finePriority, bitsLeft)
}

func (d *Decoder) decodeEnergyFinaliseGLog(energies []celtGLog, nbBands int, fineQuant []int, finePriority []int, bitsLeft int) {
	d.decodeEnergyFinaliseGLogRange(0, nbBands, energies, fineQuant, finePriority, bitsLeft)
}

// DecodeEnergyFinaliseRange consumes leftover bits for energy refinement in range [start, end).
// This mirrors libopus unquant_energy_finalise() which takes both start and end parameters.
// For hybrid mode, start should be HybridCELTStartBand (17).
func (d *Decoder) DecodeEnergyFinaliseRange(start, end int, energies []float64, fineQuant []int, finePriority []int, bitsLeft int) {
	if d.rangeDecoder == nil {
		return
	}
	if end > MaxBands {
		end = MaxBands
	}
	if start < 0 {
		start = 0
	}
	if end <= start {
		return
	}
	if bitsLeft < 0 {
		bitsLeft = 0
	}
	apply := len(energies) >= end*d.channels

	for prio := 0; prio < 2; prio++ {
		for band := start; band < end && bitsLeft >= d.channels; band++ {
			if fineQuant[band] >= maxFineBits || finePriority[band] != prio {
				continue
			}
			for c := 0; c < d.channels; c++ {
				q2 := d.rangeDecoder.DecodeRawBits(1)
				if apply {
					offset := (float32(q2) - float32(0.5)) * float32(uint(1)<<uint(14-fineQuant[band]-1)) * float32(1.0/16384.0)
					idx := c*end + band
					energies[idx] = float64(float32(energies[idx]) + offset)
				}
				bitsLeft--
			}
		}
	}
}

func (d *Decoder) decodeEnergyFinaliseGLogRange(start, end int, energies []celtGLog, fineQuant []int, finePriority []int, bitsLeft int) {
	if d.rangeDecoder == nil {
		return
	}
	if end > MaxBands {
		end = MaxBands
	}
	if start < 0 {
		start = 0
	}
	if end <= start {
		return
	}
	if bitsLeft < 0 {
		bitsLeft = 0
	}
	apply := len(energies) >= end*d.channels

	for prio := 0; prio < 2; prio++ {
		for band := start; band < end && bitsLeft >= d.channels; band++ {
			if fineQuant[band] >= maxFineBits || finePriority[band] != prio {
				continue
			}
			for c := 0; c < d.channels; c++ {
				q2 := d.rangeDecoder.DecodeRawBits(1)
				if apply {
					offset := (float32(q2) - float32(0.5)) * float32(uint(1)<<uint(14-fineQuant[band]-1)) * float32(1.0/16384.0)
					idx := c*end + band
					energies[idx] = celtGLog(float32(energies[idx]) + offset)
				}
				bitsLeft--
			}
		}
	}
}
