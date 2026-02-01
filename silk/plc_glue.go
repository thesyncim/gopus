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
				// Calculate gain and slope
				// LZ = leading zeros for normalization
				lz := silkCLZ32(concEnergy)
				if lz > 0 {
					lz--
				}
				concEnergy = concEnergy << lz
				shiftAmount := 24 - lz
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
				slopeQ16 := silkDiv32((1<<16)-gainQ16, int32(length))

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

	// Start with no shift
	var nrg int64
	shft := 0

	// Calculate energy with overflow detection
	for i := 0; i < length; i++ {
		s := int64(samples[i])
		nrg += s * s

		// Check for potential overflow and shift if needed
		if nrg > 0x3FFFFFFF {
			nrg >>= 2
			shft += 2
		}
	}

	// Ensure result fits in int32
	for nrg > 0x7FFFFFFF {
		nrg >>= 1
		shft++
	}

	return int32(nrg), shft
}

// Note: silkCLZ32 and silkDiv32 are defined in libopus_fixed.go

// silkSqrtApproxPLC approximates square root using the SILK_SQRT_APPROX algorithm.
// Input is Q24 scaled, output is Q12 scaled (sqrt of Q24 = Q12).
// Based on libopus silk/SigProc_FIX.h SILK_SQRT_APPROX macro.
func silkSqrtApproxPLC(x int32) int32 {
	if x <= 0 {
		return 0
	}

	// Count leading zeros and get fractional part
	lz := silkCLZ32(x)
	if lz < 1 {
		lz = 1
	}

	// Normalize to Q30 range
	// y = x << (lz - 1) gets us close to 2^30 range
	_ = x << (lz - 1) // normalized value (unused in simplified implementation)

	// Initial approximation using linear interpolation
	// For sqrt, we use: sqrt(y) ~= 0.5 + 0.5*y (normalized to [0.5, 1] range)
	// But simpler: just use right half of the leading bit position

	// libopus approach: use a lookup or linear approximation
	// Here we use a simpler Newton-Raphson with better initialization

	// Shift to get into reasonable range for integer sqrt
	// sqrt(x << (lz-1)) = sqrt(x) * sqrt(2^(lz-1)) = sqrt(x) * 2^((lz-1)/2)
	// We want sqrt in Q12, input is Q24
	// sqrt(Q24) = sqrt(val * 2^24) = sqrt(val) * 2^12 = Q12

	// Use integer sqrt approximation
	// Start with estimate based on bit position
	estimate := int32(1) << (16 - lz/2)
	if estimate == 0 {
		estimate = 1
	}

	// Newton-Raphson iterations
	for i := 0; i < 5; i++ {
		if estimate == 0 {
			break
		}
		estimate = (estimate + x/estimate) >> 1
	}

	// The result needs to be scaled properly for Q12 output from Q24 input
	// sqrt(Q24) = sqrt(value * 2^24) = sqrt(value) * 2^12
	// Our Newton-Raphson gives sqrt(x), we need to adjust

	// Since we're taking sqrt of Q24, the result is naturally Q12
	// But Newton-Raphson on the raw value gives us sqrt(rawValue)
	// We need: sqrt(x) where x is Q24, result should be Q12

	return estimate
}
