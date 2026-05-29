//go:build gopus_fixedpoint

package fixedpoint

// Integer KISS-FFT building blocks ported from libopus celt/kiss_fft.c under
// FIXED_POINT (without ENABLE_QEXT). The complex samples are kiss_fft_cpx with
// kiss_fft_scalar == opus_int32 (Q-format carried by the caller), and the
// twiddles are kiss_twiddle_scalar == celt_coef == opus_val16 (Q15, since
// COEF_SHIFT-1 == 15). The arithmetic reproduces the libopus integer kernels
// bit-for-bit on OPUS_FAST_INT64 targets.

// FFTCpx is a complex sample with int32 real and imaginary parts, matching
// libopus kiss_fft_cpx in the FIXED_POINT (non-QEXT) build.
type FFTCpx struct {
	R int32
	I int32
}

// kfBfly2Tw is QCONST32(0.7071067812f, COEF_SHIFT-1) with COEF_SHIFT==16, i.e.
// round(0.7071067812 * 2^15) stored as a Q15 twiddle (opus_val16).
const kfBfly2Tw int16 = 23170

// sMul implements libopus S_MUL(a, b) == MULT16_32_Q15(b, a) on an
// OPUS_FAST_INT64 target: (opus_val32)((opus_int64)(opus_val16)b * a >> 15).
// Here a is the int32 sample and b is the int16 twiddle.
func sMul(a int32, b int16) int32 {
	return int32((int64(b) * int64(a)) >> 15)
}

// add32Ovflw implements libopus ADD32_ovflw: add as uint32, ignoring overflow.
func add32Ovflw(a, b int32) int32 {
	return int32(uint32(a) + uint32(b))
}

// sub32Ovflw implements libopus SUB32_ovflw: subtract as uint32, ignoring overflow.
func sub32Ovflw(a, b int32) int32 {
	return int32(uint32(a) - uint32(b))
}

// neg32Ovflw implements libopus NEG32_ovflw: negate as uint32, ignoring overflow.
func neg32Ovflw(a int32) int32 {
	return int32(0 - uint32(a))
}

// KFBfly2 is the radix-2 KISS-FFT butterfly from libopus celt/kiss_fft.c
// (FIXED_POINT, non-CUSTOM_MODES path). It operates in place on fout starting
// at offset, processing N groups; m is always 4 in this path (the radix-2 stage
// immediately follows a radix-4 stage), so each group spans 8 complex samples.
//
// fout must hold at least offset + 8*N elements.
func KFBfly2(fout []FFTCpx, offset, n int) {
	tw := kfBfly2Tw
	base := offset
	for i := 0; i < n; i++ {
		// Fout points at base; Fout2 = Fout + 4.
		f0 := base
		f2 := base + 4

		// k == 0: t = Fout2[0]; C_SUB(Fout2[0], Fout[0], t); C_ADDTO(Fout[0], t).
		var t FFTCpx
		t = fout[f2+0]
		fout[f2+0] = FFTCpx{
			R: sub32Ovflw(fout[f0+0].R, t.R),
			I: sub32Ovflw(fout[f0+0].I, t.I),
		}
		fout[f0+0] = FFTCpx{
			R: add32Ovflw(fout[f0+0].R, t.R),
			I: add32Ovflw(fout[f0+0].I, t.I),
		}

		// k == 1.
		t.R = sMul(add32Ovflw(fout[f2+1].R, fout[f2+1].I), tw)
		t.I = sMul(sub32Ovflw(fout[f2+1].I, fout[f2+1].R), tw)
		fout[f2+1] = FFTCpx{
			R: sub32Ovflw(fout[f0+1].R, t.R),
			I: sub32Ovflw(fout[f0+1].I, t.I),
		}
		fout[f0+1] = FFTCpx{
			R: add32Ovflw(fout[f0+1].R, t.R),
			I: add32Ovflw(fout[f0+1].I, t.I),
		}

		// k == 2.
		t.R = fout[f2+2].I
		t.I = neg32Ovflw(fout[f2+2].R)
		fout[f2+2] = FFTCpx{
			R: sub32Ovflw(fout[f0+2].R, t.R),
			I: sub32Ovflw(fout[f0+2].I, t.I),
		}
		fout[f0+2] = FFTCpx{
			R: add32Ovflw(fout[f0+2].R, t.R),
			I: add32Ovflw(fout[f0+2].I, t.I),
		}

		// k == 3.
		t.R = sMul(sub32Ovflw(fout[f2+3].I, fout[f2+3].R), tw)
		t.I = sMul(neg32Ovflw(add32Ovflw(fout[f2+3].I, fout[f2+3].R)), tw)
		fout[f2+3] = FFTCpx{
			R: sub32Ovflw(fout[f0+3].R, t.R),
			I: sub32Ovflw(fout[f0+3].I, t.I),
		}
		fout[f0+3] = FFTCpx{
			R: add32Ovflw(fout[f0+3].R, t.R),
			I: add32Ovflw(fout[f0+3].I, t.I),
		}

		base += 8
	}
}
