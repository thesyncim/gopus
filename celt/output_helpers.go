package celt

// applyDeemphasis applies the de-emphasis filter for natural sound.
// CELT uses pre-emphasis during encoding; this reverses it.
// The filter is: y[n] = x[n] + PreemphCoef * y[n-1]
//
// This is a first-order IIR filter that boosts low frequencies,
// countering the high-frequency boost from pre-emphasis.
//
// IMPORTANT: This function uses float32 precision for the filter state
// to match libopus exactly. The IIR filter accumulates state over time,
// and using float64 would cause precision drift relative to libopus.
func (d *Decoder) applyDeemphasis(samples []float64) {
	d.applyDeemphasisAndScale(samples, 1.0)
}

func (d *Decoder) applyDeemphasisAndScale(samples []float64, scale float64) {
	if len(samples) == 0 {
		return
	}
	// Silence fast path: when de-emphasis state is zero and all samples are zero,
	// output remains zero regardless of scale. Skipping the filter avoids extra work
	// on CELT silence frames and keeps state at exact zero.
	if d.channels == 1 {
		if d.preemphState[0] == 0 {
			allZero := true
			for i := 0; i < len(samples); i++ {
				if samples[i] != 0 {
					allZero = false
					break
				}
			}
			if allZero {
				return
			}
		}
	} else if d.preemphState[0] == 0 && d.preemphState[1] == 0 {
		allZero := true
		for i := 0; i < len(samples); i++ {
			if samples[i] != 0 {
				allZero = false
				break
			}
		}
		if allZero {
			return
		}
	}

	// VERY_SMALL prevents denormal numbers that can cause performance issues.
	// This matches libopus celt/celt_decoder.c celt_decode_with_ec().
	// Using float32 constant to match libopus VERY_SMALL = 1e-30f
	const verySmall float32 = 1e-30

	// Use float32 for filter coefficient to match libopus
	const coef float32 = float32(PreemphCoef)
	scale32 := float32(scale)

	if d.channels == 1 {
		// Mono de-emphasis - use float32 precision for state
		state := float32(d.preemphState[0])
		n := len(samples)
		samples = samples[:n:n]
		_ = samples[n-1]
		i := 0
		for ; i+7 < n; i += 8 {
			tmp0 := float32(samples[i]) + verySmall + state
			state = coef * tmp0
			samples[i] = float64(tmp0 * scale32)

			tmp1 := float32(samples[i+1]) + verySmall + state
			state = coef * tmp1
			samples[i+1] = float64(tmp1 * scale32)

			tmp2 := float32(samples[i+2]) + verySmall + state
			state = coef * tmp2
			samples[i+2] = float64(tmp2 * scale32)

			tmp3 := float32(samples[i+3]) + verySmall + state
			state = coef * tmp3
			samples[i+3] = float64(tmp3 * scale32)

			tmp4 := float32(samples[i+4]) + verySmall + state
			state = coef * tmp4
			samples[i+4] = float64(tmp4 * scale32)

			tmp5 := float32(samples[i+5]) + verySmall + state
			state = coef * tmp5
			samples[i+5] = float64(tmp5 * scale32)

			tmp6 := float32(samples[i+6]) + verySmall + state
			state = coef * tmp6
			samples[i+6] = float64(tmp6 * scale32)

			tmp7 := float32(samples[i+7]) + verySmall + state
			state = coef * tmp7
			samples[i+7] = float64(tmp7 * scale32)
		}
		for ; i+3 < n; i += 4 {
			tmp0 := float32(samples[i]) + verySmall + state
			state = coef * tmp0
			samples[i] = float64(tmp0 * scale32)

			tmp1 := float32(samples[i+1]) + verySmall + state
			state = coef * tmp1
			samples[i+1] = float64(tmp1 * scale32)

			tmp2 := float32(samples[i+2]) + verySmall + state
			state = coef * tmp2
			samples[i+2] = float64(tmp2 * scale32)

			tmp3 := float32(samples[i+3]) + verySmall + state
			state = coef * tmp3
			samples[i+3] = float64(tmp3 * scale32)
		}
		for ; i < n; i++ {
			tmp := float32(samples[i]) + verySmall + state
			state = coef * tmp
			samples[i] = float64(tmp * scale32)
		}
		d.preemphState[0] = float64(state)
	} else {
		// Stereo de-emphasis (interleaved samples) - use float32 precision
		stateL := float32(d.preemphState[0])
		stateR := float32(d.preemphState[1])
		n := len(samples)
		samples = samples[:n:n]
		_ = samples[n-1]
		i := 0
		for ; i+7 < n; i += 8 {
			tmpL0 := float32(samples[i]) + verySmall + stateL
			stateL = coef * tmpL0
			samples[i] = float64(tmpL0 * scale32)

			tmpR0 := float32(samples[i+1]) + verySmall + stateR
			stateR = coef * tmpR0
			samples[i+1] = float64(tmpR0 * scale32)

			tmpL1 := float32(samples[i+2]) + verySmall + stateL
			stateL = coef * tmpL1
			samples[i+2] = float64(tmpL1 * scale32)

			tmpR1 := float32(samples[i+3]) + verySmall + stateR
			stateR = coef * tmpR1
			samples[i+3] = float64(tmpR1 * scale32)

			tmpL2 := float32(samples[i+4]) + verySmall + stateL
			stateL = coef * tmpL2
			samples[i+4] = float64(tmpL2 * scale32)

			tmpR2 := float32(samples[i+5]) + verySmall + stateR
			stateR = coef * tmpR2
			samples[i+5] = float64(tmpR2 * scale32)

			tmpL3 := float32(samples[i+6]) + verySmall + stateL
			stateL = coef * tmpL3
			samples[i+6] = float64(tmpL3 * scale32)

			tmpR3 := float32(samples[i+7]) + verySmall + stateR
			stateR = coef * tmpR3
			samples[i+7] = float64(tmpR3 * scale32)
		}
		for ; i+3 < n; i += 4 {
			tmpL0 := float32(samples[i]) + verySmall + stateL
			stateL = coef * tmpL0
			samples[i] = float64(tmpL0 * scale32)

			tmpR0 := float32(samples[i+1]) + verySmall + stateR
			stateR = coef * tmpR0
			samples[i+1] = float64(tmpR0 * scale32)

			tmpL1 := float32(samples[i+2]) + verySmall + stateL
			stateL = coef * tmpL1
			samples[i+2] = float64(tmpL1 * scale32)

			tmpR1 := float32(samples[i+3]) + verySmall + stateR
			stateR = coef * tmpR1
			samples[i+3] = float64(tmpR1 * scale32)
		}
		for ; i+1 < n; i += 2 {
			tmpL := float32(samples[i]) + verySmall + stateL
			stateL = coef * tmpL
			samples[i] = float64(tmpL * scale32)

			tmpR := float32(samples[i+1]) + verySmall + stateR
			stateR = coef * tmpR
			samples[i+1] = float64(tmpR * scale32)
		}

		d.preemphState[0] = float64(stateL)
		d.preemphState[1] = float64(stateR)
	}
}

func (d *Decoder) applyDeemphasisAndScaleStereoPlanarToFloat32(dst []float32, left, right []float64, scale float64) {
	n := len(left)
	if len(right) < n {
		n = len(right)
	}
	if n == 0 {
		return
	}
	if len(dst) < n*2 {
		n = len(dst) >> 1
	}
	if n == 0 {
		return
	}
	dst = dst[:n*2]
	left = left[:n]
	right = right[:n]

	if d.preemphState[0] == 0 && d.preemphState[1] == 0 {
		allZero := true
		for i := 0; i < n; i++ {
			if left[i] != 0 || right[i] != 0 {
				allZero = false
				break
			}
		}
		if allZero {
			clear(dst)
			return
		}
	}

	const verySmall float32 = 1e-30
	const coef float32 = float32(PreemphCoef)
	scale32 := float32(scale)

	stateL := float32(d.preemphState[0])
	stateR := float32(d.preemphState[1])
	_ = left[n-1]
	_ = right[n-1]
	_ = dst[n*2-1]
	i := 0
	j := 0
	for ; i+3 < n; i, j = i+4, j+8 {
		tmpL0 := float32(left[i]) + verySmall + stateL
		stateL = coef * tmpL0
		dst[j] = tmpL0 * scale32

		tmpR0 := float32(right[i]) + verySmall + stateR
		stateR = coef * tmpR0
		dst[j+1] = tmpR0 * scale32

		tmpL1 := float32(left[i+1]) + verySmall + stateL
		stateL = coef * tmpL1
		dst[j+2] = tmpL1 * scale32

		tmpR1 := float32(right[i+1]) + verySmall + stateR
		stateR = coef * tmpR1
		dst[j+3] = tmpR1 * scale32

		tmpL2 := float32(left[i+2]) + verySmall + stateL
		stateL = coef * tmpL2
		dst[j+4] = tmpL2 * scale32

		tmpR2 := float32(right[i+2]) + verySmall + stateR
		stateR = coef * tmpR2
		dst[j+5] = tmpR2 * scale32

		tmpL3 := float32(left[i+3]) + verySmall + stateL
		stateL = coef * tmpL3
		dst[j+6] = tmpL3 * scale32

		tmpR3 := float32(right[i+3]) + verySmall + stateR
		stateR = coef * tmpR3
		dst[j+7] = tmpR3 * scale32
	}
	for ; i < n; i, j = i+1, j+2 {
		tmpL := float32(left[i]) + verySmall + stateL
		stateL = coef * tmpL
		dst[j] = tmpL * scale32

		tmpR := float32(right[i]) + verySmall + stateR
		stateR = coef * tmpR
		dst[j+1] = tmpR * scale32
	}

	d.preemphState[0] = float64(stateL)
	d.preemphState[1] = float64(stateR)
}

func (d *Decoder) applyDeemphasisAndScaleMonoFloat32ToFloat32(dst []float32, samples []float32, scale float64) {
	n := len(samples)
	if n == 0 {
		return
	}
	if len(dst) < n {
		n = len(dst)
	}
	if n == 0 {
		return
	}
	dst = dst[:n]
	samples = samples[:n]

	if d.preemphState[0] == 0 {
		allZero := true
		for i := 0; i < n; i++ {
			if samples[i] != 0 {
				allZero = false
				break
			}
		}
		if allZero {
			clear(dst)
			return
		}
	}

	const verySmall float32 = 1e-30
	const coef float32 = float32(PreemphCoef)
	scale32 := float32(scale)

	state := float32(d.preemphState[0])
	_ = samples[n-1]
	_ = dst[n-1]
	i := 0
	for ; i+7 < n; i += 8 {
		tmp0 := samples[i] + verySmall + state
		state = coef * tmp0
		dst[i] = tmp0 * scale32

		tmp1 := samples[i+1] + verySmall + state
		state = coef * tmp1
		dst[i+1] = tmp1 * scale32

		tmp2 := samples[i+2] + verySmall + state
		state = coef * tmp2
		dst[i+2] = tmp2 * scale32

		tmp3 := samples[i+3] + verySmall + state
		state = coef * tmp3
		dst[i+3] = tmp3 * scale32

		tmp4 := samples[i+4] + verySmall + state
		state = coef * tmp4
		dst[i+4] = tmp4 * scale32

		tmp5 := samples[i+5] + verySmall + state
		state = coef * tmp5
		dst[i+5] = tmp5 * scale32

		tmp6 := samples[i+6] + verySmall + state
		state = coef * tmp6
		dst[i+6] = tmp6 * scale32

		tmp7 := samples[i+7] + verySmall + state
		state = coef * tmp7
		dst[i+7] = tmp7 * scale32
	}
	for ; i+3 < n; i += 4 {
		tmp0 := samples[i] + verySmall + state
		state = coef * tmp0
		dst[i] = tmp0 * scale32

		tmp1 := samples[i+1] + verySmall + state
		state = coef * tmp1
		dst[i+1] = tmp1 * scale32

		tmp2 := samples[i+2] + verySmall + state
		state = coef * tmp2
		dst[i+2] = tmp2 * scale32

		tmp3 := samples[i+3] + verySmall + state
		state = coef * tmp3
		dst[i+3] = tmp3 * scale32
	}
	for ; i < n; i++ {
		tmp := samples[i] + verySmall + state
		state = coef * tmp
		dst[i] = tmp * scale32
	}
	d.preemphState[0] = float64(state)
}

func (d *Decoder) applyDeemphasisAndScaleToFloat32(dst []float32, samples []float64, scale float64) {
	n := len(samples)
	if n == 0 {
		return
	}
	dst = dst[:n]

	if d.channels == 1 {
		if d.preemphState[0] == 0 {
			allZero := true
			for i := 0; i < n; i++ {
				if samples[i] != 0 {
					allZero = false
					break
				}
			}
			if allZero {
				clear(dst)
				return
			}
		}
	} else if d.preemphState[0] == 0 && d.preemphState[1] == 0 {
		allZero := true
		for i := 0; i < n; i++ {
			if samples[i] != 0 {
				allZero = false
				break
			}
		}
		if allZero {
			clear(dst)
			return
		}
	}

	const verySmall float32 = 1e-30
	const coef float32 = float32(PreemphCoef)
	scale32 := float32(scale)

	if d.channels == 1 {
		state := float32(d.preemphState[0])
		_ = samples[n-1]
		_ = dst[n-1]
		i := 0
		for ; i+7 < n; i += 8 {
			tmp0 := float32(samples[i]) + verySmall + state
			state = coef * tmp0
			dst[i] = tmp0 * scale32

			tmp1 := float32(samples[i+1]) + verySmall + state
			state = coef * tmp1
			dst[i+1] = tmp1 * scale32

			tmp2 := float32(samples[i+2]) + verySmall + state
			state = coef * tmp2
			dst[i+2] = tmp2 * scale32

			tmp3 := float32(samples[i+3]) + verySmall + state
			state = coef * tmp3
			dst[i+3] = tmp3 * scale32

			tmp4 := float32(samples[i+4]) + verySmall + state
			state = coef * tmp4
			dst[i+4] = tmp4 * scale32

			tmp5 := float32(samples[i+5]) + verySmall + state
			state = coef * tmp5
			dst[i+5] = tmp5 * scale32

			tmp6 := float32(samples[i+6]) + verySmall + state
			state = coef * tmp6
			dst[i+6] = tmp6 * scale32

			tmp7 := float32(samples[i+7]) + verySmall + state
			state = coef * tmp7
			dst[i+7] = tmp7 * scale32
		}
		for ; i+3 < n; i += 4 {
			tmp0 := float32(samples[i]) + verySmall + state
			state = coef * tmp0
			dst[i] = tmp0 * scale32

			tmp1 := float32(samples[i+1]) + verySmall + state
			state = coef * tmp1
			dst[i+1] = tmp1 * scale32

			tmp2 := float32(samples[i+2]) + verySmall + state
			state = coef * tmp2
			dst[i+2] = tmp2 * scale32

			tmp3 := float32(samples[i+3]) + verySmall + state
			state = coef * tmp3
			dst[i+3] = tmp3 * scale32
		}
		for ; i < n; i++ {
			tmp := float32(samples[i]) + verySmall + state
			state = coef * tmp
			dst[i] = tmp * scale32
		}
		d.preemphState[0] = float64(state)
		return
	}

	stateL := float32(d.preemphState[0])
	stateR := float32(d.preemphState[1])
	_ = samples[n-1]
	_ = dst[n-1]
	i := 0
	for ; i+7 < n; i += 8 {
		tmpL0 := float32(samples[i]) + verySmall + stateL
		stateL = coef * tmpL0
		dst[i] = tmpL0 * scale32

		tmpR0 := float32(samples[i+1]) + verySmall + stateR
		stateR = coef * tmpR0
		dst[i+1] = tmpR0 * scale32

		tmpL1 := float32(samples[i+2]) + verySmall + stateL
		stateL = coef * tmpL1
		dst[i+2] = tmpL1 * scale32

		tmpR1 := float32(samples[i+3]) + verySmall + stateR
		stateR = coef * tmpR1
		dst[i+3] = tmpR1 * scale32

		tmpL2 := float32(samples[i+4]) + verySmall + stateL
		stateL = coef * tmpL2
		dst[i+4] = tmpL2 * scale32

		tmpR2 := float32(samples[i+5]) + verySmall + stateR
		stateR = coef * tmpR2
		dst[i+5] = tmpR2 * scale32

		tmpL3 := float32(samples[i+6]) + verySmall + stateL
		stateL = coef * tmpL3
		dst[i+6] = tmpL3 * scale32

		tmpR3 := float32(samples[i+7]) + verySmall + stateR
		stateR = coef * tmpR3
		dst[i+7] = tmpR3 * scale32
	}
	for ; i+3 < n; i += 4 {
		tmpL0 := float32(samples[i]) + verySmall + stateL
		stateL = coef * tmpL0
		dst[i] = tmpL0 * scale32

		tmpR0 := float32(samples[i+1]) + verySmall + stateR
		stateR = coef * tmpR0
		dst[i+1] = tmpR0 * scale32

		tmpL1 := float32(samples[i+2]) + verySmall + stateL
		stateL = coef * tmpL1
		dst[i+2] = tmpL1 * scale32

		tmpR1 := float32(samples[i+3]) + verySmall + stateR
		stateR = coef * tmpR1
		dst[i+3] = tmpR1 * scale32
	}
	for ; i+1 < n; i += 2 {
		tmpL := float32(samples[i]) + verySmall + stateL
		stateL = coef * tmpL
		dst[i] = tmpL * scale32

		tmpR := float32(samples[i+1]) + verySmall + stateR
		stateR = coef * tmpR
		dst[i+1] = tmpR * scale32
	}

	d.preemphState[0] = float64(stateL)
	d.preemphState[1] = float64(stateR)
}

func (d *Decoder) advanceDeemphasisStateMono(samples []float64) {
	n := len(samples)
	if d == nil || d.channels != 1 || n == 0 {
		return
	}
	if d.preemphState[0] == 0 {
		allZero := true
		for i := 0; i < n; i++ {
			if samples[i] != 0 {
				allZero = false
				break
			}
		}
		if allZero {
			return
		}
	}
	const verySmall float32 = 1e-30
	const coef float32 = float32(PreemphCoef)
	state := float32(d.preemphState[0])
	for i := 0; i < n; i++ {
		tmp := float32(samples[i]) + verySmall + state
		state = coef * tmp
	}
	d.preemphState[0] = float64(state)
}

func copyFloat64ToFloat32(dst []float32, src []float64) {
	n := len(dst)
	if len(src) < n {
		n = len(src)
	}
	i := 0
	for ; i+3 < n; i += 4 {
		dst[i] = float32(src[i])
		dst[i+1] = float32(src[i+1])
		dst[i+2] = float32(src[i+2])
		dst[i+3] = float32(src[i+3])
	}
	for ; i < n; i++ {
		dst[i] = float32(src[i])
	}
	if n < len(dst) {
		clear(dst[n:])
	}
}

func copyFloat32ToFloat64(dst []float64, src []float32) {
	n := len(dst)
	if len(src) < n {
		n = len(src)
	}
	widenFloat32ToFloat64(dst, src, n)
}
