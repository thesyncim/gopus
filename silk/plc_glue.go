package silk

// silkPLCGlueFrames ensures smooth connection between concealed and recovered frames.
// This implements silk_PLC_glue_frames from libopus PLC.c.
//
// The algorithm works as follows:
// 1. During concealment (lossCnt > 0): Calculate and store energy of concealed frame
// 2. During recovery (first good frame after loss): Compare energies and apply gain ramp
//
// The gain ramp prevents abrupt amplitude changes that would cause audible clicks.
// If the recovered frame has higher energy than the concealed frame, we start with
// a lower gain and ramp up to unity over the frame duration.
//
// Parameters:
//   - st: decoder state (contains PLC glue state)
//   - frame: decoded samples (modified in place)
//   - length: number of samples
func silkPLCGlueFrames(st *decoderState, frame []int16, length int) {
	if st.lossCnt > 0 {
		// Currently in loss - calculate energy of concealed frame
		// This will be used for gluing when we receive a good frame
		st.plcConcEnergy, st.plcConcEnergyShift = silkSumSqrShift(frame, length)
		st.plcLastFrameLost = true
	} else {
		if st.plcLastFrameLost {
			// First good frame after loss - apply glue
			// Calculate energy of recovered frame
			energy, energyShift := silkSumSqrShift(frame, length)

			// Normalize energies to same scale
			concEnergy := st.plcConcEnergy
			concEnergyShift := st.plcConcEnergyShift

			if energyShift > concEnergyShift {
				concEnergy = concEnergy >> (energyShift - concEnergyShift)
			} else if energyShift < concEnergyShift {
				energy = energy >> (concEnergyShift - energyShift)
			}

			// Fade in the energy difference
			// Only apply if recovered frame has higher energy (would cause a "pop")
			if energy > concEnergy {
				// Match libopus silk_PLC_glue_frames() fixed-point cadence.
				lz := silkCLZ32(concEnergy) - 1
				concEnergy = concEnergy << lz
				shiftAmount := int32(24) - lz
				if shiftAmount < 0 {
					shiftAmount = 0
				}
				energy = energy >> shiftAmount

				// frac_Q24 = concEnergy / energy (in Q24)
				if energy < 1 {
					energy = 1
				}
				fracQ24 := silkDiv32(concEnergy, energy)

				// gain_Q16 = sqrt(frac_Q24) << 4 (to get Q16)
				gainQ16 := silkSqrtApproxPLC(fracQ24) << 4

				// slope_Q16 = (1.0 - gain) / length
				slopeQ16 := silkDiv32_16((1<<16)-gainQ16, int32(length))

				// Make slope 4x steeper to avoid missing onsets after DTX
				slopeQ16 = slopeQ16 << 2

				// Apply gain ramp
				for i := 0; i < length; i++ {
					frame[i] = int16(silkSMULWB(gainQ16, int32(frame[i])))
					gainQ16 += slopeQ16
					if gainQ16 > (1 << 16) {
						break
					}
				}
			}
		}
		st.plcLastFrameLost = false
	}
}

// silkSumSqrShift calculates sum of squared samples with automatic shift.
// Returns (energy, shift) where energy is the sum of squares shifted by 'shift'.
// This avoids overflow for large signals.
func silkSumSqrShift(samples []int16, length int) (int32, int) {
	if length <= 0 {
		return 0, 0
	}

	// Port of libopus silk_sum_sqr_shift() two-pass shift selection.
	shft := int(31 - silkCLZ32(int32(length)))
	nrg := int32(length)
	i := 0
	for ; i < length-1; i += 2 {
		nrgTmp := uint32(silkSMULBB(int32(samples[i]), int32(samples[i])))
		nrgTmp = uint32(int32(nrgTmp) + silkSMULBB(int32(samples[i+1]), int32(samples[i+1])))
		nrg = int32(uint32(nrg) + (nrgTmp >> uint(shft)))
	}
	if i < length {
		nrgTmp := uint32(silkSMULBB(int32(samples[i]), int32(samples[i])))
		nrg = int32(uint32(nrg) + (nrgTmp >> uint(shft)))
	}

	shft = max(0, shft+3-int(silkCLZ32(nrg)))

	nrg = 0
	i = 0
	for ; i < length-1; i += 2 {
		nrgTmp := uint32(silkSMULBB(int32(samples[i]), int32(samples[i])))
		nrgTmp = uint32(int32(nrgTmp) + silkSMULBB(int32(samples[i+1]), int32(samples[i+1])))
		nrg = int32(uint32(nrg) + (nrgTmp >> uint(shft)))
	}
	if i < length {
		nrgTmp := uint32(silkSMULBB(int32(samples[i]), int32(samples[i])))
		nrg = int32(uint32(nrg) + (nrgTmp >> uint(shft)))
	}

	return nrg, shft
}

// Note: silkCLZ32 and silkDiv32 are defined in libopus_fixed.go

func silkCLZFrac(in int32) (lz, fracQ7 int32) {
	lz = silkCLZ32(in)
	if lz <= 24 {
		fracQ7 = (in >> uint(24-lz)) & 0x7f
	} else {
		fracQ7 = (in << uint(lz-24)) & 0x7f
	}
	return lz, fracQ7
}

// silkSqrtApproxPLC approximates square root using the SILK_SQRT_APPROX algorithm.
// Input is Q24 scaled, output is Q12 scaled (sqrt of Q24 = Q12).
// Based on libopus silk/SigProc_FIX.h SILK_SQRT_APPROX macro.
func silkSqrtApproxPLC(x int32) int32 {
	if x <= 0 {
		return 0
	}

	lz, fracQ7 := silkCLZFrac(x)

	var y int32
	if lz&1 != 0 {
		y = 32768
	} else {
		y = 46214 // sqrt(2) * 32768
	}

	// Get scaling right.
	y >>= uint(lz >> 1)

	// Increment using fractional part of input.
	y = silkSMLAWB(y, y, silkSMULBB(213, fracQ7))
	return y
}
