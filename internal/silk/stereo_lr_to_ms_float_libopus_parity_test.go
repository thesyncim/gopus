//go:build gopus_fixed_point

package silk

import (
	"fmt"
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/opusmath"
)

// TestSILKStereoLRToMSFloatDecisionLibopusParity drives silkStereoLRToMS, the
// stereo front end both builds run, on a second randomized corpus and compares
// the stereo prediction indices, mid_only flag, per-channel rate split and
// updated stereo state against the libopus silk_stereo_LR_to_MS oracle. The
// input is int16 audio that has made the int16 -> float32 -> int16 round trip
// of the float API (RES2INT16 is exact on int16 magnitudes).
func TestSILKStereoLRToMSFloatDecisionLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0x5701F70A7))

	makeChannels := func(frameLength int, corr, pan, amp float64, kind string) (x1, x2 []int16) {
		x1 = make([]int16, frameLength+2)
		x2 = make([]int16, frameLength+2)
		for i := range x1 {
			var l, r float64
			switch kind {
			case "tonal":
				common := amp * math.Sin(2*math.Pi*float64(i)/13.7)
				lOnly := amp * 0.4 * math.Sin(2*math.Pi*float64(i)/7.1)
				rOnly := amp * 0.4 * math.Cos(2*math.Pi*float64(i)/5.3)
				l = common + (1-corr)*lOnly
				r = common*pan + (1-corr)*rOnly
			case "silence":
				l, r = 0, 0
			case "fullscale":
				if i%2 == 0 {
					l, r = 32767, -32768
				} else {
					l, r = -32768, 32767
				}
			default:
				common := (rng.Float64()*2 - 1) * amp
				lOnly := (rng.Float64()*2 - 1) * amp
				rOnly := (rng.Float64()*2 - 1) * amp
				l = corr*common + (1-corr)*lOnly
				r = corr*common*pan + (1-corr)*rOnly
			}
			x1[i] = clampI16(l)
			x2[i] = clampI16(r)
		}
		return x1, x2
	}

	var cases []silkFixedStereoLRCase

	fsList := []int{8, 12, 16}
	frameLenFor := func(fs, ms int) int { return ms * fs }

	mkCase := func(name string, fs, ms int, total, speech, toMono int32, corr, pan, amp float64, kind string,
		st stereoEncState) silkFixedStereoLRCase {
		fl := frameLenFor(fs, ms)
		x1, x2 := makeChannels(fl, corr, pan, amp, kind)
		return silkFixedStereoLRCase{
			name: name, totalRateBps: total, prevSpeechActQ8: speech, toMono: toMono,
			fsKHz: fs, frameLength: fl,
			predPrevQ13: st.predPrevQ13, sMid: st.sMid, sSide: st.sSide,
			midSideAmpQ0: st.midSideAmpQ0, smthWidthQ14: st.smthWidthQ14,
			widthPrevQ14: st.widthPrevQ14, silentSide: st.silentSideLen,
			x1: x1, x2: x2,
		}
	}

	zero := stereoEncState{}
	warm := stereoEncState{
		predPrevQ13:  [2]int16{1024, -512},
		sMid:         [2]int16{120, -90},
		sSide:        [2]int16{40, -30},
		midSideAmpQ0: [4]int32{5000, 1200, 4000, 900},
		smthWidthQ14: 12000,
		widthPrevQ14: 14000,
	}
	collapsed := stereoEncState{
		smthWidthQ14: 200,
		widthPrevQ14: 0,
		midSideAmpQ0: [4]int32{100, 80, 60, 50},
	}

	rates := []int32{6000, 12000, 24000, 48000, 96000, 1500}
	speeches := []int32{0, 64, 128, 256}

	for _, fs := range fsList {
		for _, ms := range []int{10, 20} {
			for _, total := range rates {
				for _, sp := range speeches {
					for _, kind := range []string{"noise", "tonal"} {
						for _, st := range []stereoEncState{zero, warm, collapsed} {
							cases = append(cases, mkCase(
								fmt.Sprintf("%s-fs%d-ms%d-r%d-sp%d", kind, fs, ms, total, sp),
								fs, ms, total, sp, 0, 0.7, 0.95, 18000, kind, st))
						}
					}
				}
			}
			cases = append(cases, mkCase(fmt.Sprintf("tomono-fs%d-ms%d", fs, ms),
				fs, ms, 24000, 200, 1, 0.8, 0.9, 16000, "noise", warm))
			cases = append(cases, mkCase(fmt.Sprintf("silence-fs%d-ms%d", fs, ms),
				fs, ms, 24000, 0, 0, 0, 0, 0, "silence", zero))
			cases = append(cases, mkCase(fmt.Sprintf("fullscale-fs%d-ms%d", fs, ms),
				fs, ms, 96000, 256, 0, 1, 1, 32767, "fullscale", warm))
			cases = append(cases, mkCase(fmt.Sprintf("nearmono-fs%d-ms%d", fs, ms),
				fs, ms, 6000, 32, 0, 0.99, 1.0, 20000, "noise", collapsed))
		}
	}

	for i := 0; i < 300; i++ {
		fs := fsList[rng.Intn(len(fsList))]
		ms := []int{10, 20}[rng.Intn(2)]
		total := int32(2000 + rng.Intn(120000))
		sp := int32(rng.Intn(257))
		corr := rng.Float64()
		pan := 0.5 + rng.Float64()
		amp := 500 + rng.Float64()*20000
		st := []stereoEncState{zero, warm, collapsed}[rng.Intn(3)]
		cases = append(cases, mkCase(fmt.Sprintf("rand-%d", i), fs, ms, total, sp, 0, corr, pan, amp, "noise", st))
	}

	want, err := probeLibopusSILKFixedStereoLR(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed stereo LR", err)
		return
	}

	for i, tc := range cases {
		st := stereoEncState{
			predPrevQ13:   tc.predPrevQ13,
			sMid:          tc.sMid,
			sSide:         tc.sSide,
			midSideAmpQ0:  tc.midSideAmpQ0,
			smthWidthQ14:  tc.smthWidthQ14,
			widthPrevQ14:  tc.widthPrevQ14,
			silentSideLen: tc.silentSide,
		}

		fl := tc.frameLength
		buf0 := make([]int16, fl+2)
		buf1 := make([]int16, fl+2)
		for n := 0; n < fl; n++ {
			buf0[n+2] = opusmath.Float32ToInt16(float32(tc.x1[n+2]) / 32768.0)
			buf1[n+2] = opusmath.Float32ToInt16(float32(tc.x2[n+2]) / 32768.0)
		}
		var scratch stereoLRToMSScratch
		ix, midOnly, rates := silkStereoLRToMS(&st, buf0, buf1,
			tc.totalRateBps, tc.prevSpeechActQ8, tc.toMono != 0, tc.fsKHz, fl, &scratch)

		w := want[i]

		for n := 0; n < 2; n++ {
			for k := 0; k < 3; k++ {
				if int32(ix[n][k]) != w.ix[n][k] {
					t.Fatalf("case %d (%s): ix[%d][%d]=%d want %d", i, tc.name, n, k, ix[n][k], w.ix[n][k])
				}
			}
		}
		if int32(midOnly) != w.midOnly {
			t.Fatalf("case %d (%s): mid_only=%d want %d (total=%d sp=%d fs=%d)", i, tc.name, midOnly, w.midOnly, tc.totalRateBps, tc.prevSpeechActQ8, tc.fsKHz)
		}
		if rates[0] != w.rates[0] || rates[1] != w.rates[1] {
			t.Fatalf("case %d (%s): rates=%v want %v", i, tc.name, rates, w.rates)
		}
		if st.predPrevQ13 != w.predPrevQ13 {
			t.Fatalf("case %d (%s): predPrevQ13=%v want %v", i, tc.name, st.predPrevQ13, w.predPrevQ13)
		}
		if st.smthWidthQ14 != w.smthWidthQ14 {
			t.Fatalf("case %d (%s): smthWidthQ14=%d want %d", i, tc.name, st.smthWidthQ14, w.smthWidthQ14)
		}
		if st.widthPrevQ14 != w.widthPrevQ14 {
			t.Fatalf("case %d (%s): widthPrevQ14=%d want %d", i, tc.name, st.widthPrevQ14, w.widthPrevQ14)
		}
		if st.midSideAmpQ0 != w.midSideAmpQ0 {
			t.Fatalf("case %d (%s): midSideAmpQ0=%v want %v", i, tc.name, st.midSideAmpQ0, w.midSideAmpQ0)
		}
		if st.silentSideLen != w.silentSide {
			t.Fatalf("case %d (%s): silentSideLen=%d want %d", i, tc.name, st.silentSideLen, w.silentSide)
		}
	}
}
