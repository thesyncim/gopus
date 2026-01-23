// Package celt implements the CELT decoder per RFC 6716 Section 4.3.
package celt

import "github.com/thesyncim/gopus/internal/rangecoding"

// Laplace decoding constants per RFC 6716 Section 4.3.2.1
const (
	laplaceNMIN   = 16    // Minimum probability for non-zero magnitude
	laplaceFS     = 32768 // Total frequency space
	laplaceScale  = laplaceFS - laplaceNMIN
	laplaceFTBits = 15 // log2(laplaceFS)
)

// ec_laplace_get_freq1 returns the frequency of the "1" symbol.
// Reference: libopus celt/laplace.c
func ec_laplace_get_freq1(fs0 int, decay int) int {
	// ft = fs0 - decay
	ft := fs0 - decay
	if ft < 0 {
		ft = 0
	}
	return ft
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

	// Probability model:
	// P(0) is centered, with exponentially decreasing tails
	// decay controls how fast probabilities decrease for larger |k|

	// Get the current position in the range
	rng := rd.Range()
	val := rd.Val()

	// Scale factor
	s := rng / uint32(fs)
	if s == 0 {
		s = 1
	}

	// Frequency from current state
	fm := val / s
	if fm >= uint32(fs) {
		fm = uint32(fs) - 1
	}

	// Compute center frequency (probability of value 0)
	// fs0 is frequency mass for symbol 0
	fs0 := laplaceNMIN + (laplaceScale*decay)>>15
	if fs0 > fs-1 {
		fs0 = fs - 1
	}

	// Check if fm is in the "0" symbol range
	// Symbol 0 starts at fl=0, has fs0 frequency mass
	if int(fm) < fs0 {
		// Symbol is 0
		// Update: need to consume the proper range
		// Range update: rng = s * fs0, val = val - s * 0
		d.updateRange(0, uint32(fs0), uint32(fs))
		return 0
	}

	// Symbol is not 0 - decode positive or negative
	// The distribution is symmetric: P(k) = P(-k) for k != 0

	// Current cumulative past 0
	cumFL := fs0
	k := 1
	prevFk := fs0

	for {
		// Frequency for symbol k (and -k)
		// fk decreases geometrically: fk = prevFk * decay / 32768 approximately
		// Using the recurrence from libopus
		fk := (prevFk * decay) >> 15
		if fk < laplaceNMIN {
			fk = laplaceNMIN
		}

		// Check positive k: cumulative range [cumFL, cumFL + fk)
		if int(fm) >= cumFL && int(fm) < cumFL+fk {
			// Positive k
			d.updateRange(uint32(cumFL), uint32(cumFL+fk), uint32(fs))
			return k
		}

		// Check negative k: from the end
		// Negative symbols are at the top of frequency range
		negFL := fs - cumFL - fk
		if negFL < 0 {
			negFL = 0
		}
		if int(fm) >= negFL && int(fm) < negFL+fk {
			// Negative k
			d.updateRange(uint32(negFL), uint32(negFL+fk), uint32(fs))
			return -k
		}

		cumFL += fk
		k++
		prevFk = fk

		// Safety: prevent infinite loop
		if k > 127 || cumFL >= fs/2 {
			// Default to largest magnitude
			remaining := fs - 2*cumFL
			if remaining < laplaceNMIN {
				remaining = laplaceNMIN
			}
			if int(fm) >= cumFL && int(fm) < cumFL+remaining {
				d.updateRange(uint32(cumFL), uint32(cumFL+remaining), uint32(fs))
				return k
			}
			low := fs - cumFL - remaining
			d.updateRange(uint32(low), uint32(low+remaining), uint32(fs))
			return -k
		}
	}
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

	// Get prediction coefficients
	var alpha, beta float64
	if intra {
		// Intra-frame: no inter-frame prediction, only inter-band
		alpha = 0.0
		beta = BetaIntra // Fixed 0.15 for intra mode
	} else {
		// Inter-frame: use both alpha (previous frame) and beta (previous band)
		alpha = AlphaCoef[lm]
		beta = BetaCoefInter[lm] // LM-dependent for inter mode
	}

	// Decay parameter for Laplace model depends on intra/inter mode and LM
	// Per libopus: different decay values for different modes
	// Typical values from libopus celt/quant_bands.c
	decay := 16384 // Default decay (fairly narrow)
	if !intra {
		// Inter-frame mode uses wider distribution (smaller decay)
		decay = 24000
	}

	// Decode for each channel
	for c := 0; c < d.channels; c++ {
		prevBandEnergy := 0.0 // Energy of previous band (for inter-band prediction)

		for band := 0; band < nbBands; band++ {
			// Decode Laplace-distributed residual
			qi := d.decodeLaplace(laplaceFS, decay)

			// Apply prediction
			// pred = alpha * prevEnergy[band] + beta * prevBandEnergy
			prevFrameEnergy := d.prevEnergy[c*MaxBands+band]
			pred := alpha*prevFrameEnergy + beta*prevBandEnergy

			// Compute energy: pred + qi * 6.0 (6 dB per step)
			energy := pred + float64(qi)*DB6

			// Trace coarse energy (coarse=pred, fine=qi*DB6, total=energy)
			DefaultTracer.TraceEnergy(band, pred, float64(qi)*DB6, energy)

			// Store result
			energies[c*nbBands+band] = energy

			// Update prev band energy for next band's inter-band prediction
			// Per libopus: prevBandEnergy accumulates a filtered version of quantized deltas
			// Formula: prev = prev + q - beta*q, where q = qi*DB6
			q := float64(qi) * DB6
			prevBandEnergy = prevBandEnergy + q - beta*q
		}

		// Update previous frame energy for next frame's inter-frame prediction
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

	for c := 0; c < d.channels; c++ {
		for band := 0; band < nbBands; band++ {
			bits := fineBits[band]
			if bits <= 0 {
				continue
			}

			// Clamp to reasonable maximum (8 bits max precision)
			if bits > 8 {
				bits = 8
			}

			// Decode fineBits[band] bits uniformly
			ft := uint(1 << bits)
			q := d.decodeUniform(ft)

			// Compute offset: (q + 0.5) / (1 << fineBits) - 0.5
			// This centers the quantization levels
			offset := (float64(q)+0.5)/float64(ft) - 0.5

			// Add offset * 6.0 to coarse energy (6 dB range for fine adjustment)
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
