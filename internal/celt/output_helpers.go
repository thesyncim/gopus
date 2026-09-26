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
// and widening would cause precision drift relative to libopus.
func (d *Decoder) applyDeemphasis(samples []float32) {
	d.applyDeemphasisAndScale(samples, 1.0)
}

// deemphCoefficient returns the first-order de-emphasis coefficient for the
// active mode. Zero d.deemphCoef selects the 48 kHz fullband PreemphCoef, so
// the 48 kHz decode path is numerically identical to the prior constant; the
// native 96 kHz HD mode threads its own coefficient via d.deemphCoef.
func (d *Decoder) deemphCoefficient() float32 {
	if d.deemphCoef != 0 {
		return d.deemphCoef
	}
	return float32(PreemphCoef)
}

// applyDeemphasis2TapInterleaved runs the libopus 2-tap de-emphasis used when
// mode->preemph[1] != 0 (custom/QEXT modes such as native 96 kHz HD):
//
//	tmp = x[j] + m + VERY_SMALL
//	m   = coef0*tmp - coef1*x[j]
//	y   = coef3*tmp        (SHL32 is a no-op in the float build)
//
// per channel (interleaved stride = channels). dst and samples may alias.
// Reference: celt/celt_decoder.c deemphasis(), coef[1]!=0 branch.
func (d *Decoder) applyDeemphasis2TapInterleaved(dst, samples []float32, scale float32) {
	channels := max(int(d.channels), 1)
	n := min(len(dst), len(samples))
	frames := n / channels
	if frames <= 0 {
		return
	}
	const verySmall float32 = 1e-30
	coef0 := d.deemphCoef
	coef1 := d.deemphCoef1
	// coef3 folded with the SIG2RES scale (the caller's 1/32768).
	out := noFMA32Mul(d.deemphCoef3, scale)
	for c := range channels {
		m := float32(d.preemphState[c])
		for j := range frames {
			idx := j*channels + c
			x := samples[idx]
			tmp := x + m + verySmall
			m = noFMA32Mul(coef0, tmp) - noFMA32Mul(coef1, x)
			dst[idx] = noFMA32Mul(out, tmp)
		}
		d.preemphState[c] = celtSig(m)
	}
}

func (d *Decoder) applyDeemphasisAndScale(samples []float32, scale float32) {
	if len(samples) == 0 {
		return
	}
	if d.deemphCoef1 != 0 {
		d.applyDeemphasis2TapInterleaved(samples, samples, scale)
		return
	}
	// VERY_SMALL prevents denormal numbers that can cause performance issues.
	// This matches libopus celt/celt_decoder.c celt_decode_with_ec().
	// Using float32 constant to match libopus VERY_SMALL = 1e-30f
	const verySmall float32 = 1e-30

	// Use float32 for filter coefficient to match libopus
	coef := d.deemphCoefficient()
	scale32 := scale
	if d.channels == 1 {
		// Mono de-emphasis - use float32 precision for state
		state := d.preemphState[0]
		n := len(samples)
		samples = samples[:n:n]
		_ = samples[n-1]
		i := 0
		for ; i+7 < n; i += 8 {
			tmp0 := samples[i] + verySmall + state
			state = noFMA32Mul(coef, tmp0)
			samples[i] = tmp0 * scale32

			tmp1 := samples[i+1] + verySmall + state
			state = noFMA32Mul(coef, tmp1)
			samples[i+1] = tmp1 * scale32

			tmp2 := samples[i+2] + verySmall + state
			state = noFMA32Mul(coef, tmp2)
			samples[i+2] = tmp2 * scale32

			tmp3 := samples[i+3] + verySmall + state
			state = noFMA32Mul(coef, tmp3)
			samples[i+3] = tmp3 * scale32

			tmp4 := samples[i+4] + verySmall + state
			state = noFMA32Mul(coef, tmp4)
			samples[i+4] = tmp4 * scale32

			tmp5 := samples[i+5] + verySmall + state
			state = noFMA32Mul(coef, tmp5)
			samples[i+5] = tmp5 * scale32

			tmp6 := samples[i+6] + verySmall + state
			state = noFMA32Mul(coef, tmp6)
			samples[i+6] = tmp6 * scale32

			tmp7 := samples[i+7] + verySmall + state
			state = noFMA32Mul(coef, tmp7)
			samples[i+7] = tmp7 * scale32
		}
		for ; i+3 < n; i += 4 {
			tmp0 := samples[i] + verySmall + state
			state = noFMA32Mul(coef, tmp0)
			samples[i] = tmp0 * scale32

			tmp1 := samples[i+1] + verySmall + state
			state = noFMA32Mul(coef, tmp1)
			samples[i+1] = tmp1 * scale32

			tmp2 := samples[i+2] + verySmall + state
			state = noFMA32Mul(coef, tmp2)
			samples[i+2] = tmp2 * scale32

			tmp3 := samples[i+3] + verySmall + state
			state = noFMA32Mul(coef, tmp3)
			samples[i+3] = tmp3 * scale32
		}
		for ; i < n; i++ {
			tmp := samples[i] + verySmall + state
			state = noFMA32Mul(coef, tmp)
			samples[i] = tmp * scale32
		}
		d.preemphState[0] = state
	} else {
		// Stereo de-emphasis (interleaved samples) - use float32 precision
		stateL := d.preemphState[0]
		stateR := d.preemphState[1]
		n := len(samples)
		samples = samples[:n:n]
		_ = samples[n-1]
		i := 0
		for ; i+7 < n; i += 8 {
			tmpL0 := samples[i] + verySmall + stateL
			stateL = noFMA32Mul(coef, tmpL0)
			samples[i] = tmpL0 * scale32

			tmpR0 := samples[i+1] + verySmall + stateR
			stateR = noFMA32Mul(coef, tmpR0)
			samples[i+1] = tmpR0 * scale32

			tmpL1 := samples[i+2] + verySmall + stateL
			stateL = noFMA32Mul(coef, tmpL1)
			samples[i+2] = tmpL1 * scale32

			tmpR1 := samples[i+3] + verySmall + stateR
			stateR = noFMA32Mul(coef, tmpR1)
			samples[i+3] = tmpR1 * scale32

			tmpL2 := samples[i+4] + verySmall + stateL
			stateL = noFMA32Mul(coef, tmpL2)
			samples[i+4] = tmpL2 * scale32

			tmpR2 := samples[i+5] + verySmall + stateR
			stateR = noFMA32Mul(coef, tmpR2)
			samples[i+5] = tmpR2 * scale32

			tmpL3 := samples[i+6] + verySmall + stateL
			stateL = noFMA32Mul(coef, tmpL3)
			samples[i+6] = tmpL3 * scale32

			tmpR3 := samples[i+7] + verySmall + stateR
			stateR = noFMA32Mul(coef, tmpR3)
			samples[i+7] = tmpR3 * scale32
		}
		for ; i+3 < n; i += 4 {
			tmpL0 := samples[i] + verySmall + stateL
			stateL = noFMA32Mul(coef, tmpL0)
			samples[i] = tmpL0 * scale32

			tmpR0 := samples[i+1] + verySmall + stateR
			stateR = noFMA32Mul(coef, tmpR0)
			samples[i+1] = tmpR0 * scale32

			tmpL1 := samples[i+2] + verySmall + stateL
			stateL = noFMA32Mul(coef, tmpL1)
			samples[i+2] = tmpL1 * scale32

			tmpR1 := samples[i+3] + verySmall + stateR
			stateR = noFMA32Mul(coef, tmpR1)
			samples[i+3] = tmpR1 * scale32
		}
		for ; i+1 < n; i += 2 {
			tmpL := samples[i] + verySmall + stateL
			stateL = noFMA32Mul(coef, tmpL)
			samples[i] = tmpL * scale32

			tmpR := samples[i+1] + verySmall + stateR
			stateR = noFMA32Mul(coef, tmpR)
			samples[i+1] = tmpR * scale32
		}

		d.preemphState[0] = stateL
		d.preemphState[1] = stateR
	}
}

func (d *Decoder) applyDeemphasisAndScaleStereoPlanarToFloat32(dst []float32, left, right []float32, scale float32) {
	n := min(len(right), len(left))
	if n == 0 {
		return
	}
	if len(dst) < n*2 {
		n = len(dst) >> 1
	}
	if n == 0 {
		return
	}
	n2 := n * 2
	dst = dst[:n2:n2]
	left = left[:n:n]
	right = right[:n:n]
	_ = dst[n2-1]
	_ = left[n-1]
	_ = right[n-1]

	const verySmall float32 = 1e-30
	const coef float32 = float32(PreemphCoef)
	stateL := d.preemphState[0]
	stateR := d.preemphState[1]
	stateL, stateR = deemphasisStereoPlanarF32Core(dst, left, right, n, scale, stateL, stateR, coef, verySmall)

	d.preemphState[0] = stateL
	d.preemphState[1] = stateR
}

func (d *Decoder) applyDeemphasisAndScaleMonoFloat32ToFloat32(dst []float32, samples []float32, scale float32) {
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

	const verySmall float32 = 1e-30
	const coef float32 = float32(PreemphCoef)
	state := d.preemphState[0]
	_ = samples[n-1]
	_ = dst[n-1]
	i := 0
	for ; i+7 < n; i += 8 {
		tmp0 := samples[i] + verySmall + state
		state = noFMA32Mul(coef, tmp0)
		dst[i] = tmp0 * scale

		tmp1 := samples[i+1] + verySmall + state
		state = noFMA32Mul(coef, tmp1)
		dst[i+1] = tmp1 * scale

		tmp2 := samples[i+2] + verySmall + state
		state = noFMA32Mul(coef, tmp2)
		dst[i+2] = tmp2 * scale

		tmp3 := samples[i+3] + verySmall + state
		state = noFMA32Mul(coef, tmp3)
		dst[i+3] = tmp3 * scale

		tmp4 := samples[i+4] + verySmall + state
		state = noFMA32Mul(coef, tmp4)
		dst[i+4] = tmp4 * scale

		tmp5 := samples[i+5] + verySmall + state
		state = noFMA32Mul(coef, tmp5)
		dst[i+5] = tmp5 * scale

		tmp6 := samples[i+6] + verySmall + state
		state = noFMA32Mul(coef, tmp6)
		dst[i+6] = tmp6 * scale

		tmp7 := samples[i+7] + verySmall + state
		state = noFMA32Mul(coef, tmp7)
		dst[i+7] = tmp7 * scale
	}
	for ; i+3 < n; i += 4 {
		tmp0 := samples[i] + verySmall + state
		state = noFMA32Mul(coef, tmp0)
		dst[i] = tmp0 * scale

		tmp1 := samples[i+1] + verySmall + state
		state = noFMA32Mul(coef, tmp1)
		dst[i+1] = tmp1 * scale

		tmp2 := samples[i+2] + verySmall + state
		state = noFMA32Mul(coef, tmp2)
		dst[i+2] = tmp2 * scale

		tmp3 := samples[i+3] + verySmall + state
		state = noFMA32Mul(coef, tmp3)
		dst[i+3] = tmp3 * scale
	}
	for ; i < n; i++ {
		tmp := samples[i] + verySmall + state
		state = noFMA32Mul(coef, tmp)
		dst[i] = tmp * scale
	}
	d.preemphState[0] = state
}

func (d *Decoder) applyDeemphasisAndScaleStereoPlanarFloat32ToFloat32(dst []float32, left, right []float32, scale float32) {
	n := min(len(right), len(left))
	if n == 0 {
		return
	}
	if len(dst) < n*2 {
		n = len(dst) >> 1
	}
	if n == 0 {
		return
	}
	n2 := n * 2
	dst = dst[:n2:n2]
	left = left[:n:n]
	right = right[:n:n]
	_ = dst[n2-1]
	_ = left[n-1]
	_ = right[n-1]

	const verySmall float32 = 1e-30
	const coef float32 = float32(PreemphCoef)
	stateL := d.preemphState[0]
	stateR := d.preemphState[1]
	stateL, stateR = deemphasisStereoPlanarF32Core(dst, left, right, n, scale, stateL, stateR, coef, verySmall)

	d.preemphState[0] = stateL
	d.preemphState[1] = stateR
}

func (d *Decoder) applyDeemphasisAndScaleToFloat32(dst []float32, samples []float32, scale float32) {
	n := len(samples)
	if n == 0 {
		return
	}
	dst = dst[:n]

	if d.deemphCoef1 != 0 {
		d.applyDeemphasis2TapInterleaved(dst, samples, scale)
		return
	}

	const verySmall float32 = 1e-30
	coef := d.deemphCoefficient()
	if d.channels == 1 {
		state := d.preemphState[0]
		_ = samples[n-1]
		_ = dst[n-1]
		i := 0
		for ; i+7 < n; i += 8 {
			tmp0 := samples[i] + verySmall + state
			state = noFMA32Mul(coef, tmp0)
			dst[i] = tmp0 * scale

			tmp1 := samples[i+1] + verySmall + state
			state = noFMA32Mul(coef, tmp1)
			dst[i+1] = tmp1 * scale

			tmp2 := samples[i+2] + verySmall + state
			state = noFMA32Mul(coef, tmp2)
			dst[i+2] = tmp2 * scale

			tmp3 := samples[i+3] + verySmall + state
			state = noFMA32Mul(coef, tmp3)
			dst[i+3] = tmp3 * scale

			tmp4 := samples[i+4] + verySmall + state
			state = noFMA32Mul(coef, tmp4)
			dst[i+4] = tmp4 * scale

			tmp5 := samples[i+5] + verySmall + state
			state = noFMA32Mul(coef, tmp5)
			dst[i+5] = tmp5 * scale

			tmp6 := samples[i+6] + verySmall + state
			state = noFMA32Mul(coef, tmp6)
			dst[i+6] = tmp6 * scale

			tmp7 := samples[i+7] + verySmall + state
			state = noFMA32Mul(coef, tmp7)
			dst[i+7] = tmp7 * scale
		}
		for ; i+3 < n; i += 4 {
			tmp0 := samples[i] + verySmall + state
			state = noFMA32Mul(coef, tmp0)
			dst[i] = tmp0 * scale

			tmp1 := samples[i+1] + verySmall + state
			state = noFMA32Mul(coef, tmp1)
			dst[i+1] = tmp1 * scale

			tmp2 := samples[i+2] + verySmall + state
			state = noFMA32Mul(coef, tmp2)
			dst[i+2] = tmp2 * scale

			tmp3 := samples[i+3] + verySmall + state
			state = noFMA32Mul(coef, tmp3)
			dst[i+3] = tmp3 * scale
		}
		for ; i < n; i++ {
			tmp := samples[i] + verySmall + state
			state = noFMA32Mul(coef, tmp)
			dst[i] = tmp * scale
		}
		d.preemphState[0] = state
		return
	}

	stateL := d.preemphState[0]
	stateR := d.preemphState[1]
	_ = samples[n-1]
	_ = dst[n-1]
	i := 0
	for ; i+7 < n; i += 8 {
		tmpL0 := samples[i] + verySmall + stateL
		stateL = noFMA32Mul(coef, tmpL0)
		dst[i] = tmpL0 * scale

		tmpR0 := samples[i+1] + verySmall + stateR
		stateR = noFMA32Mul(coef, tmpR0)
		dst[i+1] = tmpR0 * scale

		tmpL1 := samples[i+2] + verySmall + stateL
		stateL = noFMA32Mul(coef, tmpL1)
		dst[i+2] = tmpL1 * scale

		tmpR1 := samples[i+3] + verySmall + stateR
		stateR = noFMA32Mul(coef, tmpR1)
		dst[i+3] = tmpR1 * scale

		tmpL2 := samples[i+4] + verySmall + stateL
		stateL = noFMA32Mul(coef, tmpL2)
		dst[i+4] = tmpL2 * scale

		tmpR2 := samples[i+5] + verySmall + stateR
		stateR = noFMA32Mul(coef, tmpR2)
		dst[i+5] = tmpR2 * scale

		tmpL3 := samples[i+6] + verySmall + stateL
		stateL = noFMA32Mul(coef, tmpL3)
		dst[i+6] = tmpL3 * scale

		tmpR3 := samples[i+7] + verySmall + stateR
		stateR = noFMA32Mul(coef, tmpR3)
		dst[i+7] = tmpR3 * scale
	}
	for ; i+3 < n; i += 4 {
		tmpL0 := samples[i] + verySmall + stateL
		stateL = noFMA32Mul(coef, tmpL0)
		dst[i] = tmpL0 * scale

		tmpR0 := samples[i+1] + verySmall + stateR
		stateR = noFMA32Mul(coef, tmpR0)
		dst[i+1] = tmpR0 * scale

		tmpL1 := samples[i+2] + verySmall + stateL
		stateL = noFMA32Mul(coef, tmpL1)
		dst[i+2] = tmpL1 * scale

		tmpR1 := samples[i+3] + verySmall + stateR
		stateR = noFMA32Mul(coef, tmpR1)
		dst[i+3] = tmpR1 * scale
	}
	for ; i+1 < n; i += 2 {
		tmpL := samples[i] + verySmall + stateL
		stateL = noFMA32Mul(coef, tmpL)
		dst[i] = tmpL * scale

		tmpR := samples[i+1] + verySmall + stateR
		stateR = noFMA32Mul(coef, tmpR)
		dst[i+1] = tmpR * scale
	}

	d.preemphState[0] = stateL
	d.preemphState[1] = stateR
}

func (d *Decoder) applyDeemphasisAndScaleFloat32(samples []float32, scale float32) {
	n := len(samples)
	if n == 0 {
		return
	}

	const verySmall float32 = 1e-30
	const coef float32 = float32(PreemphCoef)
	if d.channels == 1 {
		state := d.preemphState[0]
		_ = samples[n-1]
		for i := range n {
			tmp := samples[i] + verySmall + state
			state = noFMA32Mul(coef, tmp)
			samples[i] = tmp * scale
		}
		d.preemphState[0] = state
		return
	}

	stateL := d.preemphState[0]
	stateR := d.preemphState[1]
	_ = samples[n-1]
	i := 0
	for ; i+1 < n; i += 2 {
		tmpL := samples[i] + verySmall + stateL
		stateL = noFMA32Mul(coef, tmpL)
		samples[i] = tmpL * scale

		tmpR := samples[i+1] + verySmall + stateR
		stateR = noFMA32Mul(coef, tmpR)
		samples[i+1] = tmpR * scale
	}
	d.preemphState[0] = stateL
	d.preemphState[1] = stateR
}

func (d *Decoder) applyDeemphasisAndScaleDownsampleToFloat32(dst []float32, samples []float32, downsample int, scale float32) {
	if downsample <= 1 {
		d.applyDeemphasisAndScaleToFloat32(dst, samples, scale)
		return
	}
	if len(samples) == 0 || len(dst) == 0 {
		return
	}
	const verySmall float32 = 1e-30
	const coef float32 = float32(PreemphCoef)

	if d.channels == 1 {
		n := min(len(dst), len(samples)/downsample)
		if n <= 0 {
			return
		}
		internal := n * downsample
		state := d.preemphState[0]
		out := 0
		for i := range internal {
			tmp := samples[i] + verySmall + state
			state = noFMA32Mul(coef, tmp)
			if i%downsample == 0 {
				dst[out] = tmp * scale
				out++
			}
		}
		d.preemphState[0] = state
		return
	}

	frames := len(samples) / 2
	n := min(len(dst)/2, frames/downsample)
	if n <= 0 {
		return
	}
	internal := n * downsample
	stateL := d.preemphState[0]
	stateR := d.preemphState[1]
	out := 0
	for i := range internal {
		base := i * 2
		tmpL := samples[base] + verySmall + stateL
		stateL = noFMA32Mul(coef, tmpL)
		tmpR := samples[base+1] + verySmall + stateR
		stateR = noFMA32Mul(coef, tmpR)
		if i%downsample == 0 {
			dst[out] = tmpL * scale
			dst[out+1] = tmpR * scale
			out += 2
		}
	}
	d.preemphState[0] = stateL
	d.preemphState[1] = stateR
}

func (d *Decoder) applyDeemphasisAndScaleMonoFloat32DownsampleToFloat32(dst []float32, samples []float32, downsample int, scale float32) {
	if downsample <= 1 {
		d.applyDeemphasisAndScaleMonoFloat32ToFloat32(dst, samples, scale)
		return
	}
	n := min(len(dst), len(samples)/downsample)
	if n <= 0 {
		return
	}
	internal := n * downsample
	const verySmall float32 = 1e-30
	const coef float32 = float32(PreemphCoef)
	state := d.preemphState[0]
	out := 0
	for i := range internal {
		tmp := samples[i] + verySmall + state
		state = noFMA32Mul(coef, tmp)
		if i%downsample == 0 {
			dst[out] = tmp * scale
			out++
		}
	}
	d.preemphState[0] = state
}

func (d *Decoder) applyDeemphasisAndScaleStereoPlanarFloat32DownsampleToFloat32(dst []float32, left, right []float32, downsample int, scale float32) {
	if downsample <= 1 {
		d.applyDeemphasisAndScaleStereoPlanarFloat32ToFloat32(dst, left, right, scale)
		return
	}
	frames := min(len(right), len(left))
	n := min(len(dst)/2, frames/downsample)
	if n <= 0 {
		return
	}
	internal := n * downsample
	const verySmall float32 = 1e-30
	const coef float32 = float32(PreemphCoef)
	stateL := d.preemphState[0]
	stateR := d.preemphState[1]
	out := 0
	for i := range internal {
		tmpL := left[i] + verySmall + stateL
		stateL = noFMA32Mul(coef, tmpL)
		tmpR := right[i] + verySmall + stateR
		stateR = noFMA32Mul(coef, tmpR)
		if i%downsample == 0 {
			dst[out] = tmpL * scale
			dst[out+1] = tmpR * scale
			out += 2
		}
	}
	d.preemphState[0] = stateL
	d.preemphState[1] = stateR
}
