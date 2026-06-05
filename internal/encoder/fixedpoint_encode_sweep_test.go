//go:build gopus_fixed_point

package encoder

import (
	"fmt"
	"math"
	"testing"
)

// TestFixedPointEncodeShortFrameNoPanic is the gopus_fixed_point companion to
// TestLargeFrameNoPanic. It sweeps the full opus_encode grid that the
// FIXED_POINT SILK encode bodies must tolerate without panicking:
//
//	frame sizes 2.5/5/10/20/40/60/80/100/120 ms
//	x 8/12/16/24/48 kHz x mono/stereo
//	x auto / forced-SILK / forced-Hybrid / forced-CELT,
//
// plus the CELT->mode transition prefill. libopus opus_encode accepts every one
// of these (it emits a packet, never returning OPUS_BAD_ARG), so gopus must
// never panic either.
//
// At sub-48 kHz API rates the SILK input resampler hands the encode body a
// frame shorter than a full 10/20 ms SILK block, which broke four FIXED_POINT
// invariants that libopus guarantees via its encoder setup / celt_assert:
//   - silk_stereo_LR_to_MS interpolation loop (mid[n+2] past the buffer),
//   - silk_VAD_GetSA_Q8 band decimation (X[-1] for a zero-length lowest band),
//   - silk_find_pitch_lags_FIX windowing (x_ptr = buf_len - pitch_LPC_win_length
//     going negative, asserted as buf_len >= pitch_LPC_win_length in libopus),
//   - silk_noise_shape_analysis_FIX sparseness loop (reading past pitchRes when
//     nSegs*nSamples exceeds the residual frame length).
//
// Each guard mirrors the precondition the float (default-build) encode path
// already applies, so valid-rate (48 kHz) output is unchanged.
func TestFixedPointEncodeShortFrameNoPanic(t *testing.T) {
	rates := []int{8000, 12000, 16000, 24000, 48000}
	durations := []struct {
		name string
		num  int
		den  int
	}{
		{"2.5ms", 25, 10000},
		{"5ms", 5, 1000},
		{"10ms", 10, 1000},
		{"20ms", 20, 1000},
		{"40ms", 40, 1000},
		{"60ms", 60, 1000},
		{"80ms", 80, 1000},
		{"100ms", 100, 1000},
		{"120ms", 120, 1000},
	}
	channelsList := []int{1, 2}
	modes := []struct {
		name string
		m    Mode
	}{
		{"auto", ModeAuto},
		{"silk", ModeSILK},
		{"hybrid", ModeHybrid},
		{"celt", ModeCELT},
	}

	for _, fs := range rates {
		for _, ch := range channelsList {
			for _, md := range modes {
				for _, d := range durations {
					frameSize := fs * d.num / d.den
					if frameSize <= 0 {
						continue
					}
					name := fmt.Sprintf("%s_%dHz_%dch_%s", md.name, fs, ch, d.name)
					t.Run(name, func(t *testing.T) {
						n := frameSize * ch
						silkPCM := make([]float32, n)
						for i := range silkPCM {
							silkPCM[i] = float32(0.2 * math.Sin(float64(i)*0.02))
						}

						// Direct encode.
						enc := NewEncoder(fs, ch)
						enc.SetComplexity(10)
						enc.SetVoIPApplication(true)
						enc.SetMode(md.m)
						if ch == 2 {
							enc.SetForceChannels(2)
						}
						mustNotPanic(t, "encode", fs, ch, md.m, frameSize, func() {
							_, _ = enc.Encode(silkPCM, frameSize)
						})

						// CELT->mode transition prefill: a forced-CELT frame first
						// so the next frame runs the transition prefill onto the
						// (possibly short) SILK/Hybrid front-end.
						encT := NewEncoder(fs, ch)
						encT.SetComplexity(10)
						encT.SetVoIPApplication(true)
						if ch == 2 {
							encT.SetForceChannels(2)
						}
						encT.SetMode(ModeCELT)
						celtPCM := make([]float32, n)
						for i := range celtPCM {
							celtPCM[i] = float32(0.5 * math.Sin(float64(i)*0.9))
						}
						mustNotPanic(t, "celt-prime", fs, ch, md.m, frameSize, func() {
							_, _ = encT.Encode(celtPCM, frameSize)
						})
						encT.SetMode(md.m)
						mustNotPanic(t, "post-transition", fs, ch, md.m, frameSize, func() {
							_, _ = encT.Encode(silkPCM, frameSize)
						})
					})
				}
			}
		}
	}
}
