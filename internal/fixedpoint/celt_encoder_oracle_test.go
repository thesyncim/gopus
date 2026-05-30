//go:build gopus_fixedpoint

package fixedpoint

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestCELTEncoderFrontEndOracle checks the FIXED_POINT encode front-end
// (preemphasis -> compute_mdcts -> compute_band_energies -> normalise_bands)
// bit-for-bit against the real libopus reference for the static 48000/960 mode,
// across mono and stereo, the normal and transient MDCT stripings, and every
// time-resolution shift (LM 1..3), over a deterministic PCM sweep.
func TestCELTEncoderFrontEndOracle(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		shortMdctSize = 120
		start         = 0
		end           = 21
	)

	// xorshift32 PRNG for deterministic, reproducible inputs.
	next := func(state *uint32) uint32 {
		s := *state
		s ^= s << 13
		s ^= s >> 17
		s ^= s << 5
		*state = s
		return s
	}

	for lm := 1; lm <= 3; lm++ {
		frameSize := shortMdctSize << lm
		for _, C := range []int{1, 2} {
			for _, isTransient := range []bool{false, true} {
				state := uint32(0xC0FFEE + lm*101 + C*4099)
				if isTransient {
					state ^= 0x5A5A5A5A
				}

				pcm := make([]int16, C*frameSize)
				for i := range pcm {
					// Mix of low-amplitude tone-like and wideband content.
					v := int32(next(&state))
					pcm[i] = int16(v >> 17)
				}

				want, err := libopustest.ProbeCELTFixedEncodeFrontend(
					pcm, C, frameSize, start, end, isTransient)
				if err != nil {
					libopustest.HelperUnavailable(t, "celt fixed encode frontend", err)
					return
				}

				enc := NewCELTEncoder(C)
				gotFreq, gotBandE, gotX := enc.FrontEnd(pcm, frameSize, isTransient)

				if len(gotFreq) != len(want.Freq) {
					t.Fatalf("LM=%d C=%d transient=%v: freq len %d, want %d",
						lm, C, isTransient, len(gotFreq), len(want.Freq))
				}
				for i := range gotFreq {
					if gotFreq[i] != want.Freq[i] {
						t.Errorf("LM=%d C=%d transient=%v: freq[%d] = %d, libopus = %d",
							lm, C, isTransient, i, gotFreq[i], want.Freq[i])
						break
					}
				}
				for i := range gotBandE {
					if gotBandE[i] != want.BandE[i] {
						t.Errorf("LM=%d C=%d transient=%v: bandE[%d] = %d, libopus = %d",
							lm, C, isTransient, i, gotBandE[i], want.BandE[i])
						break
					}
				}
				for i := range gotX {
					if gotX[i] != want.X[i] {
						t.Errorf("LM=%d C=%d transient=%v: X[%d] = %d, libopus = %d",
							lm, C, isTransient, i, gotX[i], want.X[i])
						break
					}
				}
			}
		}
	}
}
