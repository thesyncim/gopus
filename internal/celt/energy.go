// Package celt implements the CELT decoder per RFC 6716 Section 4.3.
package celt

import "github.com/thesyncim/gopus/internal/rangecoding"

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

	fm := int(d.decodeBin(laplaceFTBits))
	val := 0
	fl := 0
	if fm >= fs {
		val++
		fl = fs
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
	}

	fh := fl + fs
	if fh > laplaceFS {
		fh = laplaceFS
	}
	d.updateRange(uint32(fl), uint32(fh), uint32(laplaceFS))
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

// DecodeCoarseEnergy decodes coarse (6dB step) band energies.
// intra=true: no inter-frame prediction (first frame or after loss)
// intra=false: uses alpha prediction from previous frame
// Reference: RFC 6716 Section 4.3.2, libopus celt/quant_bands.c unquant_coarse_energy()
func (d *Decoder) DecodeCoarseEnergy(nbBands int, intra bool, lm int) []float64 {
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

	energies := make([]float64, nbBands*d.channels)

	rd := d.rangeDecoder
	if rd == nil {
		return energies
	}

	// Get prediction coefficients
	var alpha, beta float64
	if intra {
		alpha = 0.0
		beta = BetaIntra
	} else {
		alpha = AlphaCoef[lm]
		beta = BetaCoefInter[lm]
	}

	prob := eProbModel[lm][0]
	if intra {
		prob = eProbModel[lm][1]
	}

	budget := rd.StorageBits()

	// Decode band-major to match libopus ordering.
	prevBandEnergy := make([]float64, d.channels)
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
				qi = d.decodeLaplace(fs, decay)
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
			prevFrameEnergy := d.prevEnergy[c*MaxBands+band]
			minEnergy := -9.0 * DB6
			if prevFrameEnergy < minEnergy {
				prevFrameEnergy = minEnergy
			}
			pred := alpha*prevFrameEnergy + prevBandEnergy[c]

			// Compute energy: pred + qi * 6.0 (6 dB per step)
			q := float64(qi) * DB6
			energy := pred + q

			// Trace coarse energy (coarse=pred, fine=qi*DB6, total=energy)
			DefaultTracer.TraceEnergy(band, pred, q, energy)

			// Store result
			energies[c*nbBands+band] = energy

			// Update prev band energy for next band's inter-band prediction
			// Per libopus: prevBandEnergy accumulates a filtered version of quantized deltas
			// Formula: prev = prev + q - beta*q, where q = qi*DB6
			prevBandEnergy[c] = prevBandEnergy[c] + q - beta*q
		}
	}

	// Update previous frame energy for next frame's inter-frame prediction
	for c := 0; c < d.channels; c++ {
		for band := 0; band < nbBands; band++ {
			d.prevEnergy[c*MaxBands+band] = energies[c*nbBands+band]
		}
	}

	return energies
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

// decodeUniform decodes a value uniformly in [0, ft).
// Reference: libopus celt/entdec.c ec_dec_uint()
func (d *Decoder) decodeUniform(ft uint) int {
	rd := d.rangeDecoder
	if rd == nil || ft == 0 {
		return 0
	}

	// For uniform distribution, all symbols have equal probability
	// Each symbol has frequency 1, total frequency ft

	if ft == 1 {
		return 0
	}

	// Get current range state
	rng := rd.Range()
	val := rd.Val()

	// Scale
	s := rng / uint32(ft)
	if s == 0 {
		s = 1
	}

	// Find symbol
	k := val / s
	if k >= uint32(ft) {
		k = uint32(ft) - 1
	}

	// Update range decoder state (uniform => [k, k+1))
	d.updateRange(k, k+1, uint32(ft))

	return int(k)
}

// DecodeFineEnergy adds fine energy precision to coarse values.
// fineBits[band] specifies bits allocated for refinement (0 = no refinement).
// Reference: RFC 6716 Section 4.3.2, libopus celt/quant_bands.c unquant_fine_energy()
func (d *Decoder) DecodeFineEnergy(energies []float64, nbBands int, fineBits []int) {
	if d.rangeDecoder == nil {
		return
	}
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands > len(fineBits) {
		nbBands = len(fineBits)
	}
	rd := d.rangeDecoder

	for band := 0; band < nbBands; band++ {
		extra := fineBits[band]
		if extra <= 0 {
			continue
		}

		scale := float64(uint(1) << extra)
		for c := 0; c < d.channels; c++ {
			q2 := rd.DecodeRawBits(uint(extra))
			offset := (float64(q2)+0.5)/scale - 0.5

			idx := c*nbBands + band
			if idx < len(energies) {
				energies[idx] += offset * DB6
			}
		}
	}
}

// DecodeFineEnergyWithDecoder adds fine energy precision using an explicit range decoder.
func (d *Decoder) DecodeFineEnergyWithDecoder(rd *rangecoding.Decoder, energies []float64, nbBands int, fineBits []int) {
	oldRD := d.rangeDecoder
	d.rangeDecoder = rd
	defer func() { d.rangeDecoder = oldRD }()

	d.DecodeFineEnergy(energies, nbBands, fineBits)
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
func (d *Decoder) DecodeEnergyFinalise(energies []float64, nbBands int, fineQuant []int, finePriority []int, bitsLeft int) {
	if d.rangeDecoder == nil {
		return
	}
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands <= 0 {
		return
	}
	if bitsLeft < 0 {
		bitsLeft = 0
	}

	for prio := 0; prio < 2; prio++ {
		for band := 0; band < nbBands && bitsLeft >= d.channels; band++ {
			if fineQuant[band] >= maxFineBits || finePriority[band] != prio {
				continue
			}
			for c := 0; c < d.channels; c++ {
				q2 := d.rangeDecoder.DecodeRawBits(1)
				offset := (float64(q2) - 0.5) / float64(uint(1)<<(fineQuant[band]+1))
				idx := c*nbBands + band
				if idx < len(energies) {
					energies[idx] += offset * DB6
				}
				bitsLeft--
			}
		}
	}
}
