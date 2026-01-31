// Package celt implements the CELT decoder per RFC 6716 Section 4.3.
package celt

import (
	"fmt"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

// DebugEnergyDecoding enables debug output for energy decoding.
var DebugEnergyDecoding = false

// Ensure fmt is used
var _ = fmt.Sprint

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

	if nbBands < 0 {
		nbBands = 0
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

	if DebugEnergyDecoding {
		fmt.Printf("DecodeCoarseEnergy: nbBands=%d, channels=%d, intra=%v, lm=%d, alpha=%.4f, beta=%.4f, budget=%d\n",
			nbBands, d.channels, intra, lm, alpha, beta, budget)
	}

	// Decode band-major to match libopus ordering.
	prevBandEnergy := ensureFloat64Slice(&d.scratchPrevBandEnergy, d.channels)
	for i := range prevBandEnergy {
		prevBandEnergy[i] = 0
	}
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

			if DebugEnergyDecoding {
				ch := "L"
				if c == 1 {
					ch = "R"
				}
				fmt.Printf("  Band %2d %s: tell=%d, qi=%d, remaining=%d\n", band, ch, tell, qi, remaining)
			}

			// Apply prediction
			// pred = alpha * prevEnergy[band] + prevBandEnergy
			prevFrameEnergy := d.prevEnergy[c*MaxBands+band]
			minEnergy := -9.0 * DB6
			if prevFrameEnergy < minEnergy {
				prevFrameEnergy = minEnergy
			}
			pred := alpha*prevFrameEnergy + prevBandEnergy[c]

			// Compute energy: pred + qi * DB6 (6 dB per step)
			q := float64(qi) * DB6
			energy := pred + q

			if DebugEnergyDecoding {
				ch := "L"
				if c == 1 {
					ch = "R"
				}
				fmt.Printf("         %s: prevFrame=%.4f, prevBand=%.4f, pred=%.4f, q=%.4f, energy=%.4f\n",
					ch, prevFrameEnergy, prevBandEnergy[c], pred, q, energy)
			}

			// Trace coarse energy (coarse=pred, fine=qi*DB6, total=energy)
			DefaultTracer.TraceEnergy(band, pred, q, energy)

			// Store result
			dst[c*nbBands+band] = energy

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

// decodeCoarseEnergyRange decodes coarse energies for bands in [start, end).
// energies must be sized for end bands (compact layout: [c*end+band]).
// Bands below start are left unchanged (caller should prefill).
// This mirrors libopus unquant_coarse_energy() with a non-zero start.
func (d *Decoder) decodeCoarseEnergyRange(start, end int, intra bool, lm int, energies []float64) {
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

	// Inter-band prediction state starts at 0 (matches libopus).
	prevBandEnergy := ensureFloat64Slice(&d.scratchPrevBandEnergy, d.channels)
	for i := range prevBandEnergy {
		prevBandEnergy[i] = 0
	}
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

			prevFrameEnergy := d.prevEnergy[c*MaxBands+band]
			minEnergy := -9.0 * DB6
			if prevFrameEnergy < minEnergy {
				prevFrameEnergy = minEnergy
			}
			pred := alpha*prevFrameEnergy + prevBandEnergy[c]

			q := float64(qi) * DB6
			energy := pred + q

			DefaultTracer.TraceEnergy(band, pred, q, energy)

			energies[c*end+band] = energy
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
	d.decodeFineEnergy(energies, nbBands, nil, fineBits)
}

// DecodeFineEnergyWithDecoder adds fine energy precision using an explicit range decoder.
func (d *Decoder) DecodeFineEnergyWithDecoder(rd *rangecoding.Decoder, energies []float64, nbBands int, fineBits []int) {
	oldRD := d.rangeDecoder
	d.rangeDecoder = rd
	defer func() { d.rangeDecoder = oldRD }()

	d.decodeFineEnergy(energies, nbBands, nil, fineBits)
}

// decodeFineEnergy mirrors libopus unquant_fine_energy() for float builds.
// prevQuant may be nil; extraQuant provides per-band refinement bits.
func (d *Decoder) decodeFineEnergy(energies []float64, nbBands int, prevQuant, extraQuant []int) {
	if d.rangeDecoder == nil {
		return
	}
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands > len(extraQuant) {
		nbBands = len(extraQuant)
	}
	if nbBands <= 0 {
		return
	}

	rd := d.rangeDecoder
	for band := 0; band < nbBands; band++ {
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

		scale := float64(uint(1) << extra)
		for c := 0; c < d.channels; c++ {
			q2 := rd.DecodeRawBits(uint(extra))
			offset := (float64(q2)+0.5)/scale - 0.5
			if prev > 0 {
				offset /= float64(uint(1) << prev)
			}

			idx := c*nbBands + band
			if idx < len(energies) {
				energies[idx] += offset * DB6
				traceEnergyFine(band, c, energies[idx])
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

	for prio := 0; prio < 2; prio++ {
		for band := start; band < end && bitsLeft >= d.channels; band++ {
			if fineQuant[band] >= maxFineBits || finePriority[band] != prio {
				continue
			}
			for c := 0; c < d.channels; c++ {
				q2 := d.rangeDecoder.DecodeRawBits(1)
				offset := (float64(q2) - 0.5) / float64(uint(1)<<(fineQuant[band]+1))
				idx := c*end + band
				if idx < len(energies) {
					energies[idx] += offset * DB6
					traceEnergyFinal(band, c, energies[idx])
				}
				bitsLeft--
			}
		}
	}
}
