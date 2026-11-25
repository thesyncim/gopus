package celt

import (
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// EncodeFineEnergy encodes fine energy refinement bits.
// This adds fractional precision to coarse energy values.
// fineBits[band] specifies bits allocated for refinement (0 = no refinement).
//
// This mirrors decoder's DecodeFineEnergy exactly (in reverse).
//
// Reference: RFC 6716 Section 4.3.2, libopus celt/quant_bands.c quant_fine_energy()
func (e *Encoder) EncodeFineEnergy(energies []celtGLog, quantizedCoarse []celtGLog, nbBands int, fineBits []int32) {
	if e.rangeEncoder == nil {
		return
	}
	if nbBands > e.predStride() {
		nbBands = e.predStride()
	}
	if nbBands > len(fineBits) {
		nbBands = len(fineBits)
	}

	channels := int(e.channels)
	if len(energies) < nbBands*channels {
		channels = 1
	}

	re := e.rangeEncoder

	for band := 0; band < nbBands; band++ {
		bits := fineBits[band]
		if bits <= 0 {
			continue
		}

		ft := 1 << bits
		scale32 := float32(ft)
		for c := 0; c < channels; c++ {
			idx := c*nbBands + band
			if idx >= len(energies) || idx >= len(quantizedCoarse) {
				continue
			}

			// Keep fine-energy quantization in float32 precision to mirror libopus float path.
			fine := energies[idx] - quantizedCoarse[idx]

			// Quantize to fineBits[band] levels
			q := max(
				// Clamp to valid range
				floor32ToInt((fine+0.5)*scale32), 0)
			if q >= ft {
				q = ft - 1
			}

			// Encode raw bits to match decoder
			re.EncodeRawBits(uint32(q), uint(bits))

			// Apply decoded offset to quantized energies for remainder coding
			offset := (float32(q)+0.5)/scale32 - 0.5
			quantizedCoarse[idx] = celtGLog(quantizedCoarse[idx] + offset)
		}
	}
}

// encodeFineEnergyFromError mirrors libopus quant_fine_energy() with prev_quant=NULL.
// It consumes and updates errorVals in-place so the same residual state can be used
// by energy finalisation and next-frame energyError clipping. stateStride is
// the mode's oldBandE channel stride; QEXT keeps its history in encoder state.
func (e *Encoder) encodeFineEnergyFromError(quantizedEnergies []celtGLog, nbBands, stateStride int, fineBits []int32, errorVals []celtGLog) {
	if e.rangeEncoder == nil {
		return
	}
	if nbBands > e.predStride() {
		nbBands = e.predStride()
	}
	if nbBands > len(fineBits) {
		nbBands = len(fineBits)
	}

	channels := e.codedChannels()
	if len(quantizedEnergies) < (channels-1)*stateStride+nbBands || len(errorVals) < nbBands*channels {
		channels = 1
	}

	re := e.rangeEncoder

	for band := 0; band < nbBands; band++ {
		bits := fineBits[band]
		if bits <= 0 {
			continue
		}
		// Match libopus quant_fine_energy(): if there is not enough storage
		// left to code this band's fine bits for all channels, skip the band.
		if re.Tell()+channels*int(bits) > re.StorageBits() {
			continue
		}
		extra := 1 << uint(bits)
		scale32 := float32(extra)
		for c := 0; c < channels; c++ {
			idx := c*nbBands + band
			stateIdx := c*stateStride + band
			if stateIdx >= len(quantizedEnergies) || idx >= len(errorVals) {
				continue
			}

			// libopus float: q2 = floor((error + 0.5) * extra)
			err := float32(errorVals[idx])
			q2 := min(max(floor32ToInt((err+0.5)*scale32), 0), extra-1)

			re.EncodeRawBits(uint32(q2), uint(bits))

			offset := (float32(q2)+0.5)*float32(uint(1)<<(14-bits))*(1.0/16384.0) - 0.5
			quantizedEnergies[stateIdx] = celtGLog(quantizedEnergies[stateIdx] + offset)
			errorVals[idx] = celtGLog(err - offset)
		}
	}
}

// EncodeFineEnergyRange encodes fine energies for bands in [start, end).
func (e *Encoder) EncodeFineEnergyRange(energies []celtGLog, quantizedCoarse []celtGLog, start, end int, fineBits []int32) {
	if e.rangeEncoder == nil {
		return
	}
	if start < 0 {
		start = 0
	}
	if end > e.predStride() {
		end = e.predStride()
	}
	if end <= start {
		return
	}

	nbBands := min(end, len(fineBits))

	channels := int(e.channels)
	if len(energies) < nbBands*channels {
		channels = 1
	}

	re := e.rangeEncoder

	for band := start; band < nbBands; band++ {
		bits := fineBits[band]
		if bits <= 0 {
			continue
		}

		ft := 1 << bits
		scale32 := float32(ft)
		for c := 0; c < channels; c++ {
			idx := c*nbBands + band
			if idx >= len(energies) || idx >= len(quantizedCoarse) {
				continue
			}

			fine := energies[idx] - quantizedCoarse[idx]
			q := max(floor32ToInt((fine+0.5)*scale32), 0)
			if q >= ft {
				q = ft - 1
			}

			re.EncodeRawBits(uint32(q), uint(bits))

			offset := (float32(q)+0.5)/scale32 - 0.5
			quantizedCoarse[idx] = celtGLog(quantizedCoarse[idx] + offset)
		}
	}
}

// EncodeFineEnergyRangeFromError mirrors libopus quant_fine_energy() for hybrid
// range coding, consuming the in-place coarse error residual state.
func (e *Encoder) EncodeFineEnergyRangeFromError(quantizedEnergies []celtGLog, start, end int, fineBits []int32) {
	if e.rangeEncoder == nil {
		return
	}
	if start < 0 {
		start = 0
	}
	if end > e.predStride() {
		end = e.predStride()
	}
	if end <= start {
		return
	}

	nbBands := min(end, len(fineBits))
	channels := max(int(e.channels), 1)

	required := nbBands * channels
	if len(quantizedEnergies) < required {
		channels = 1
		required = nbBands
	}

	errorVals := ensureGLogSliceNoClear(&e.scratch.coarseError, required)
	re := e.rangeEncoder

	for band := start; band < nbBands; band++ {
		bits := fineBits[band]
		if bits <= 0 {
			continue
		}
		// Match libopus: if there is not enough storage left for this band's
		// fine bits for all channels, skip this band.
		if re.Tell()+channels*int(bits) > re.StorageBits() {
			continue
		}
		extra := 1 << uint(bits)
		scale32 := float32(extra)
		for c := 0; c < channels; c++ {
			idx := c*nbBands + band
			if idx >= len(quantizedEnergies) || idx >= len(errorVals) {
				continue
			}

			err := float32(errorVals[idx])
			q2 := min(max(floor32ToInt((err+0.5)*scale32), 0), extra-1)

			re.EncodeRawBits(uint32(q2), uint(bits))

			offset := (float32(q2)+0.5)/scale32 - 0.5
			quantizedEnergies[idx] = celtGLog(quantizedEnergies[idx] + offset)
			errorVals[idx] = celtGLog(err - offset)
		}
	}
}

// EncodeEnergyRemainder encodes any leftover precision bits.
// Called after PVQ bands decoded, uses leftover bits from bit allocation.
// This mirrors decoder's DecodeEnergyRemainder exactly (in reverse).
//
// Reference: RFC 6716 Section 4.3.2, libopus celt/quant_bands.c quant_energy_finalise()
func (e *Encoder) EncodeEnergyRemainder(energies []celtGLog, quantizedEnergies []celtGLog, nbBands int, remainderBits []int) {
	if e.rangeEncoder == nil {
		return
	}
	if nbBands > e.predStride() {
		nbBands = e.predStride()
	}
	if nbBands > len(remainderBits) {
		nbBands = len(remainderBits)
	}

	channels := int(e.channels)
	if len(energies) < nbBands*channels {
		channels = 1
	}

	for c := 0; c < channels; c++ {
		for band := 0; band < nbBands; band++ {
			bits := remainderBits[band]
			if bits <= 0 {
				continue
			}

			idx := c*nbBands + band
			if idx >= len(energies) || idx >= len(quantizedEnergies) {
				continue
			}

			// Compute residual from already-quantized energy
			residual := energies[idx] - quantizedEnergies[idx]

			// Each bit provides finer precision
			// Encode single bit for each remainder bit
			for i := 0; i < bits && i < 8; i++ {
				// Precision for this bit level
				precision := celtGLog(DB6) / celtGLog(uint(1)<<(i+2))

				// Decide bit based on sign of residual
				var bit int
				if residual > 0 {
					bit = 1
				} else {
					bit = 0
				}

				// Encode the bit
				e.rangeEncoder.EncodeBit(bit, 1)

				// Update residual based on decision
				if bit == 1 {
					residual -= precision
				} else {
					residual += precision
				}
			}
		}
	}
}

// EncodeEnergyFinalise consumes leftover bits for additional energy refinement.
// This mirrors decoder's DecodeEnergyFinalise (libopus quant_energy_finalise).
// energies: original target energies
// quantizedEnergies: current quantized energies (coarse + fine)
// fineQuant/finePriority: allocation outputs
// bitsLeft: remaining whole bits available in the packet
func (e *Encoder) EncodeEnergyFinalise(energies []celtGLog, quantizedEnergies []celtGLog, nbBands int, fineQuant []int32, finePriority []int32, bitsLeft int) {
	if e.rangeEncoder == nil {
		return
	}
	if nbBands > e.predStride() {
		nbBands = e.predStride()
	}
	if nbBands <= 0 {
		return
	}
	if bitsLeft < 0 {
		bitsLeft = 0
	}

	channels := int(e.channels)
	if len(energies) < nbBands*channels || len(quantizedEnergies) < nbBands*channels {
		channels = 1
	}

	re := e.rangeEncoder

	for prio := range 2 {
		for band := 0; band < nbBands && bitsLeft >= channels; band++ {
			if band >= len(fineQuant) || band >= len(finePriority) {
				continue
			}
			if fineQuant[band] >= maxFineBits || finePriority[band] != int32(prio) {
				continue
			}
			for c := 0; c < channels; c++ {
				idx := c*nbBands + band
				if idx >= len(energies) || idx >= len(quantizedEnergies) {
					continue
				}
				errorVal := energies[idx] - quantizedEnergies[idx]
				q2 := 0
				if errorVal >= 0 {
					q2 = 1
				}
				re.EncodeRawBits(uint32(q2), 1)
				offset := (float32(q2) - 0.5) / float32(uint(1)<<(fineQuant[band]+1))
				quantizedEnergies[idx] = celtGLog(quantizedEnergies[idx] + offset)
				bitsLeft--
			}
		}
	}
}

// encodeEnergyFinaliseFromError mirrors libopus quant_energy_finalise().
// It consumes the remaining bit budget using the in-place residual state.
func (e *Encoder) encodeEnergyFinaliseFromError(quantizedEnergies []celtGLog, nbBands int, fineQuant []int32, finePriority []int32, bitsLeft int, errorVals []celtGLog) {
	if e.rangeEncoder == nil {
		return
	}
	if nbBands > e.predStride() {
		nbBands = e.predStride()
	}
	if nbBands <= 0 {
		return
	}
	if bitsLeft < 0 {
		bitsLeft = 0
	}

	channels := int(e.channels)
	if len(quantizedEnergies) < nbBands*channels || len(errorVals) < nbBands*channels {
		channels = 1
	}
	encodeEnergyFinaliseResidual(e.rangeEncoder, quantizedEnergies, errorVals, 0, nbBands, channels, fineQuant, finePriority, bitsLeft)
}

// encodeEnergyFinaliseResidual follows libopus quant_energy_finalise(). A nil
// oldEBands leaves the refined energy history intact while coding final bits
// from a residual backup, as the QEXT encoder does when it reserves bytes.
func encodeEnergyFinaliseResidual(re *rangecoding.Encoder, oldEBands, errorVals []celtGLog, start, end, channels int, fineQuant, finePriority []int32, bitsLeft int) {
	if re == nil || channels <= 0 {
		return
	}
	start = max(start, 0)
	if end <= start {
		return
	}
	bitsLeft = max(bitsLeft, 0)
	for prio := range 2 {
		for band := start; band < end && bitsLeft >= channels; band++ {
			if band >= len(fineQuant) || band >= len(finePriority) {
				continue
			}
			if fineQuant[band] >= maxFineBits || finePriority[band] != int32(prio) {
				continue
			}
			for c := 0; c < channels; c++ {
				idx := c*end + band
				if idx >= len(errorVals) || (oldEBands != nil && idx >= len(oldEBands)) {
					continue
				}

				q2 := 0
				if float32(errorVals[idx]) >= 0 {
					q2 = 1
				}
				re.EncodeRawBits(uint32(q2), 1)

				offset := (float32(q2) - 0.5) * float32(uint(1)<<(14-fineQuant[band]-1)) * (1.0 / 16384.0)
				if oldEBands != nil {
					oldEBands[idx] = celtGLog(oldEBands[idx] + offset)
				}
				errorVals[idx] = celtGLog(float32(errorVals[idx]) - offset)
				bitsLeft--
			}
		}
	}
}

// EncodeEnergyFinaliseRange consumes leftover bits for energy refinement in [start, end).
func (e *Encoder) EncodeEnergyFinaliseRange(energies []celtGLog, quantizedEnergies []celtGLog, start, end int, fineQuant []int32, finePriority []int32, bitsLeft int) {
	if e.rangeEncoder == nil {
		return
	}
	if start < 0 {
		start = 0
	}
	if end > e.predStride() {
		end = e.predStride()
	}
	if end <= start {
		return
	}
	if bitsLeft < 0 {
		bitsLeft = 0
	}

	nbBands := end
	channels := int(e.channels)
	if len(energies) < nbBands*channels || len(quantizedEnergies) < nbBands*channels {
		channels = 1
	}

	re := e.rangeEncoder

	for prio := range 2 {
		for band := start; band < nbBands && bitsLeft >= channels; band++ {
			if band >= len(fineQuant) || band >= len(finePriority) {
				continue
			}
			if fineQuant[band] >= maxFineBits || finePriority[band] != int32(prio) {
				continue
			}
			for c := 0; c < channels; c++ {
				idx := c*nbBands + band
				if idx >= len(energies) || idx >= len(quantizedEnergies) {
					continue
				}
				errorVal := energies[idx] - quantizedEnergies[idx]
				q2 := 0
				if errorVal >= 0 {
					q2 = 1
				}
				re.EncodeRawBits(uint32(q2), 1)
				offset := (float32(q2) - 0.5) / float32(uint(1)<<(fineQuant[band]+1))
				quantizedEnergies[idx] = celtGLog(quantizedEnergies[idx] + offset)
				bitsLeft--
			}
		}
	}
}

// EncodeEnergyFinaliseRangeFromError mirrors libopus quant_energy_finalise() for
// hybrid range coding, consuming the in-place residual state from coarse/fine.
func (e *Encoder) EncodeEnergyFinaliseRangeFromError(quantizedEnergies []celtGLog, start, end int, fineQuant []int32, finePriority []int32, bitsLeft int) {
	if e.rangeEncoder == nil {
		return
	}
	if start < 0 {
		start = 0
	}
	if end > e.predStride() {
		end = e.predStride()
	}
	if end <= start {
		return
	}
	if bitsLeft < 0 {
		bitsLeft = 0
	}

	nbBands := end
	channels := max(int(e.channels), 1)

	required := nbBands * channels
	if len(quantizedEnergies) < required {
		channels = 1
		required = nbBands
	}

	errorVals := ensureGLogSliceNoClear(&e.scratch.coarseError, required)
	re := e.rangeEncoder

	for prio := range 2 {
		for band := start; band < nbBands && bitsLeft >= channels; band++ {
			if band >= len(fineQuant) || band >= len(finePriority) {
				continue
			}
			if fineQuant[band] >= maxFineBits || finePriority[band] != int32(prio) {
				continue
			}
			for c := 0; c < channels; c++ {
				idx := c*nbBands + band
				if idx >= len(quantizedEnergies) || idx >= len(errorVals) {
					continue
				}

				q2 := 0
				if float32(errorVals[idx]) >= 0 {
					q2 = 1
				}
				re.EncodeRawBits(uint32(q2), 1)

				offset := (float32(q2) - 0.5) / float32(uint(1)<<(fineQuant[band]+1))
				quantizedEnergies[idx] = celtGLog(quantizedEnergies[idx] + offset)
				errorVals[idx] = celtGLog(float32(errorVals[idx]) - offset)
				bitsLeft--
			}
		}
	}
}

// EncodeCoarseEnergyWithEncoder encodes coarse energies using an explicit range encoder.
// This variant allows passing a range encoder directly rather than using e.rangeEncoder.
func (e *Encoder) EncodeCoarseEnergyWithEncoder(re *rangecoding.Encoder, energies []celtGLog, nbBands int, intra bool, lm int) []CeltGLog {
	oldRE := e.rangeEncoder
	e.rangeEncoder = re
	defer func() { e.rangeEncoder = oldRE }()

	return e.EncodeCoarseEnergy(energies, nbBands, intra, lm)
}

// EncodeFineEnergyWithEncoder encodes fine energies using an explicit range encoder.
func (e *Encoder) EncodeFineEnergyWithEncoder(re *rangecoding.Encoder, energies []celtGLog, quantizedCoarse []celtGLog, nbBands int, fineBits []int32) {
	oldRE := e.rangeEncoder
	e.rangeEncoder = re
	defer func() { e.rangeEncoder = oldRE }()

	e.EncodeFineEnergy(energies, quantizedCoarse, nbBands, fineBits)
}

func (e *Encoder) encodeFineEnergyFromErrorWithEncoder(re *rangecoding.Encoder, quantizedEnergies []celtGLog, nbBands, stateStride int, fineBits []int32, errorVals []celtGLog) {
	oldRE := e.rangeEncoder
	e.rangeEncoder = re
	defer func() { e.rangeEncoder = oldRE }()

	e.encodeFineEnergyFromError(quantizedEnergies, nbBands, stateStride, fineBits, errorVals)
}

// encodeFineEnergyFromErrorWithPrev mirrors libopus quant_fine_energy() when
// prevQuant is non-nil and extraQuant carries the incremental QEXT refinement.
func (e *Encoder) encodeFineEnergyFromErrorWithPrev(quantizedEnergies []celtGLog, nbBands int, prevQuant, extraQuant []int32, errorVals []celtGLog) {
	if e.rangeEncoder == nil {
		return
	}
	if nbBands > e.predStride() {
		nbBands = e.predStride()
	}
	if nbBands > len(extraQuant) {
		nbBands = len(extraQuant)
	}

	channels := int(e.channels)
	if len(quantizedEnergies) < nbBands*channels || len(errorVals) < nbBands*channels {
		channels = 1
	}

	re := e.rangeEncoder

	for band := 0; band < nbBands; band++ {
		extraBits := extraQuant[band]
		if extraBits <= 0 {
			continue
		}
		if re.Tell()+channels*int(extraBits) > re.StorageBits() {
			continue
		}

		prevBits := 0
		if prevQuant != nil && band < len(prevQuant) {
			prevBits = int(prevQuant[band])
		}
		extra := 1 << uint(extraBits)
		scale32 := float32(extra)
		prevScale32 := float32(uint(1) << prevBits)

		for c := 0; c < channels; c++ {
			idx := c*nbBands + band
			if idx >= len(quantizedEnergies) || idx >= len(errorVals) {
				continue
			}

			err := float32(errorVals[idx])
			q2 := min(max(floor32ToInt((err*prevScale32+0.5)*scale32), 0), extra-1)

			re.EncodeRawBits(uint32(q2), uint(extraBits))

			offset := ((float32(q2)+0.5)/scale32 - 0.5) / prevScale32
			quantizedEnergies[idx] = celtGLog(quantizedEnergies[idx] + offset)
			errorVals[idx] = celtGLog(err - offset)
		}
	}
}

func (e *Encoder) encodeFineEnergyFromErrorWithPrevWithEncoder(re *rangecoding.Encoder, quantizedEnergies []celtGLog, nbBands int, prevQuant, extraQuant []int32, errorVals []celtGLog) {
	oldRE := e.rangeEncoder
	e.rangeEncoder = re
	defer func() { e.rangeEncoder = oldRE }()

	e.encodeFineEnergyFromErrorWithPrev(quantizedEnergies, nbBands, prevQuant, extraQuant, errorVals)
}

func (e *Encoder) encodeFineEnergyRangeFromErrorWithEncoder(re *rangecoding.Encoder, quantizedEnergies []celtGLog, start, end int, fineBits []int32) {
	oldRE := e.rangeEncoder
	e.rangeEncoder = re
	defer func() { e.rangeEncoder = oldRE }()

	e.EncodeFineEnergyRangeFromError(quantizedEnergies, start, end, fineBits)
}

// EncodeEnergyRemainderWithEncoder encodes remainder bits using an explicit range encoder.
func (e *Encoder) EncodeEnergyRemainderWithEncoder(re *rangecoding.Encoder, energies []celtGLog, quantizedEnergies []celtGLog, nbBands int, remainderBits []int) {
	oldRE := e.rangeEncoder
	e.rangeEncoder = re
	defer func() { e.rangeEncoder = oldRE }()

	e.EncodeEnergyRemainder(energies, quantizedEnergies, nbBands, remainderBits)
}

// EncodeCoarseEnergyHybrid encodes coarse energies for hybrid mode.
// Only encodes bands from startBand onwards (typically band 17).
func (e *Encoder) EncodeCoarseEnergyHybrid(energies []celtGLog, nbBands int, intra bool, lm int, startBand int) []CeltGLog {
	if e.rangeEncoder == nil || nbBands == 0 {
		return make([]celtGLog, nbBands*int(e.channels))
	}

	return e.EncodeCoarseEnergyRange(energies, startBand, nbBands, intra, lm)
}

// EncodeFineEnergyHybrid encodes fine energies for hybrid mode.
// Only encodes bands from startBand onwards.
func (e *Encoder) EncodeFineEnergyHybrid(energies []celtGLog, quantizedCoarse []celtGLog, nbBands int, fineBits []int32, startBand int) {
	if e.rangeEncoder == nil || nbBands == 0 {
		return
	}

	e.EncodeFineEnergyRange(energies, quantizedCoarse, startBand, nbBands, fineBits)
}
