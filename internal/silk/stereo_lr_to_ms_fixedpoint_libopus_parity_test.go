//go:build gopus_fixed_point

package silk

import (
	"fmt"
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusSILKFixedStereoLRInputMagic  = "GSLI"
	libopusSILKFixedStereoLROutputMagic = "GSLO"
)

type silkFixedStereoLRCase struct {
	name            string
	totalRateBps    int32
	prevSpeechActQ8 int32
	toMono          int32
	fsKHz           int
	frameLength     int
	// state in
	predPrevQ13  [2]int16
	sMid         [2]int16
	sSide        [2]int16
	midSideAmpQ0 [4]int32
	smthWidthQ14 int16
	widthPrevQ14 int16
	silentSide   int16
	// x1 includes 2 leading history samples (x1[-2..]), len = frameLength+2.
	x1 []int16
	x2 []int16 // same layout, len = frameLength+2
}

type silkFixedStereoLRResult struct {
	mid          []int16 // frameLength+2
	side         []int16 // frameLength (x2[n-1] for n in [0,frameLength))
	ix           [2][3]int32
	midOnly      int32
	rates        [2]int32
	predPrevQ13  [2]int16
	sMid         [2]int16
	sSide        [2]int16
	midSideAmpQ0 [4]int32
	smthWidthQ14 int16
	widthPrevQ14 int16
	silentSide   int16
}

func probeLibopusSILKFixedStereoLR(cases []silkFixedStereoLRCase) ([]silkFixedStereoLRResult, error) {
	binPath, err := buildFixedSILKOracle("libopus_silk_fixed_stereo_lr_to_ms_info.c", "stereo_lr")
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKFixedStereoLRInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		payload.I32(tc.totalRateBps)
		payload.I32(tc.prevSpeechActQ8)
		payload.I32(tc.toMono)
		payload.I32(int32(tc.fsKHz))
		payload.I32(int32(tc.frameLength))
		for _, v := range tc.predPrevQ13 {
			payload.I16(v)
		}
		for _, v := range tc.sMid {
			payload.I16(v)
		}
		for _, v := range tc.sSide {
			payload.I16(v)
		}
		for _, v := range tc.midSideAmpQ0 {
			payload.I32(v)
		}
		payload.I16(tc.smthWidthQ14)
		payload.I16(tc.widthPrevQ14)
		payload.I16(tc.silentSide)
		for _, v := range tc.x1 {
			payload.I16(v)
		}
		for _, v := range tc.x2 {
			payload.I16(v)
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed stereo LR", libopusSILKFixedStereoLROutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]silkFixedStereoLRResult, count)
	for i := range out {
		fl := cases[i].frameLength
		out[i].mid = make([]int16, fl+2)
		for j := range out[i].mid {
			out[i].mid[j] = reader.I16()
		}
		out[i].side = make([]int16, fl)
		for j := range out[i].side {
			out[i].side[j] = reader.I16()
		}
		for n := 0; n < 2; n++ {
			for k := 0; k < 3; k++ {
				out[i].ix[n][k] = reader.I32()
			}
		}
		out[i].midOnly = reader.I32()
		out[i].rates[0] = reader.I32()
		out[i].rates[1] = reader.I32()
		for j := range out[i].predPrevQ13 {
			out[i].predPrevQ13[j] = reader.I16()
		}
		for j := range out[i].sMid {
			out[i].sMid[j] = reader.I16()
		}
		for j := range out[i].sSide {
			out[i].sSide[j] = reader.I16()
		}
		for j := range out[i].midSideAmpQ0 {
			out[i].midSideAmpQ0[j] = reader.I32()
		}
		out[i].smthWidthQ14 = reader.I16()
		out[i].widthPrevQ14 = reader.I16()
		out[i].silentSide = reader.I16()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKStereoLRToMSFixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0x57E2E0))

	// Build a stereo pair with a controllable correlation/pan and emit both
	// the left (x1) and right (x2) channels with two leading history samples.
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
			default: // noise
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
			// toMono path.
			cases = append(cases, mkCase(fmt.Sprintf("tomono-fs%d-ms%d", fs, ms),
				fs, ms, 24000, 200, 1, 0.8, 0.9, 16000, "noise", warm))
			// silence + fullscale edge cases.
			cases = append(cases, mkCase(fmt.Sprintf("silence-fs%d-ms%d", fs, ms),
				fs, ms, 24000, 0, 0, 0, 0, 0, "silence", zero))
			cases = append(cases, mkCase(fmt.Sprintf("fullscale-fs%d-ms%d", fs, ms),
				fs, ms, 96000, 256, 0, 1, 1, 32767, "fullscale", warm))
			// near-mono (high correlation) to drive mid-only.
			cases = append(cases, mkCase(fmt.Sprintf("nearmono-fs%d-ms%d", fs, ms),
				fs, ms, 6000, 32, 0, 0.99, 1.0, 20000, "noise", collapsed))
		}
	}

	// Random bulk over correlation/pan/amp.
	for i := 0; i < 200; i++ {
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
		// Build Go inputs: mid slice (len fl+2) with mid[2:] = x1's current
		// frame (x1[-2..] history at [0,1] are immediately overwritten by
		// state, so feed the current samples x1[2:]). side scratch (len fl+2).
		mid := make([]int16, fl+2)
		copy(mid[2:], tc.x1[2:]) // x1[0..fl-1] current frame
		side := make([]int16, fl+2)
		x2cur := make([]int16, fl)
		copy(x2cur, tc.x2[2:]) // x2[0..fl-1] current frame

		ix, midOnly, rates := silkStereoLRToMS(&st, mid, side, x2cur,
			tc.totalRateBps, tc.prevSpeechActQ8, tc.toMono != 0, tc.fsKHz, fl)

		w := want[i]

		for n := 0; n < 2; n++ {
			for k := 0; k < 3; k++ {
				if int32(ix[n][k]) != w.ix[n][k] {
					t.Fatalf("case %d (%s): ix[%d][%d]=%d want %d", i, tc.name, n, k, ix[n][k], w.ix[n][k])
				}
			}
		}
		if int32(midOnly) != w.midOnly {
			t.Fatalf("case %d (%s): mid_only=%d want %d", i, tc.name, midOnly, w.midOnly)
		}
		if rates[0] != w.rates[0] || rates[1] != w.rates[1] {
			t.Fatalf("case %d (%s): rates=%v want %v", i, tc.name, rates, w.rates)
		}
		for j := range mid {
			if mid[j] != w.mid[j] {
				t.Fatalf("case %d (%s): mid[%d]=%d want %d", i, tc.name, j, mid[j], w.mid[j])
			}
		}
		// Go side output is side[n+1] for n in [0,fl); C emits x2[n-1] in the
		// same n order.
		for n := 0; n < fl; n++ {
			if side[n+1] != w.side[n] {
				t.Fatalf("case %d (%s): side[n=%d]=%d want %d", i, tc.name, n, side[n+1], w.side[n])
			}
		}
		// State parity.
		if st.predPrevQ13 != w.predPrevQ13 {
			t.Fatalf("case %d (%s): predPrevQ13=%v want %v", i, tc.name, st.predPrevQ13, w.predPrevQ13)
		}
		if st.sMid != w.sMid {
			t.Fatalf("case %d (%s): sMid=%v want %v", i, tc.name, st.sMid, w.sMid)
		}
		if st.sSide != w.sSide {
			t.Fatalf("case %d (%s): sSide=%v want %v", i, tc.name, st.sSide, w.sSide)
		}
		if st.midSideAmpQ0 != w.midSideAmpQ0 {
			t.Fatalf("case %d (%s): midSideAmpQ0=%v want %v", i, tc.name, st.midSideAmpQ0, w.midSideAmpQ0)
		}
		if st.smthWidthQ14 != w.smthWidthQ14 {
			t.Fatalf("case %d (%s): smthWidthQ14=%d want %d", i, tc.name, st.smthWidthQ14, w.smthWidthQ14)
		}
		if st.widthPrevQ14 != w.widthPrevQ14 {
			t.Fatalf("case %d (%s): widthPrevQ14=%d want %d", i, tc.name, st.widthPrevQ14, w.widthPrevQ14)
		}
		if st.silentSideLen != w.silentSide {
			t.Fatalf("case %d (%s): silentSideLen=%d want %d", i, tc.name, st.silentSideLen, w.silentSide)
		}
	}
}
