//go:build gopus_fixed_point && gopus_qext

package fixedpoint

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

func TestQuantAllBandsDecodeQEXTMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const nbEBands = 21
	mainCoded := qextOracleBytes(0x47a3185d, 512)
	extCoded := qextOracleBytes(0xc21b9a73, 512)
	pulses := make([]int32, nbEBands)
	tfRes := make([]int32, nbEBands)
	extraPulses := make([]int32, nbEBands+qextCELTMaxQEXTBands)
	extraCaps := make([]int32, len(extraPulses))
	for i := 0; i < nbEBands; i++ {
		pulses[i] = 200
		extraPulses[i] = 3072
		extraCaps[i] = 2048
	}
	type testCase struct {
		name       string
		dualStereo int
		intensity  int
	}
	cases := []testCase{
		{name: "dual-stereo-extension-budget-halves", dualStereo: 1, intensity: nbEBands},
		{name: "qn-one-intensity-does-not-refine-angle", intensity: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			totalBits := int32(len(mainCoded) * (8 << bitRes))
			extTotalBits := int32(len(extCoded) * (8 << bitRes))
			ref, err := libopustest.ProbeCELTFixedQuantAllBands(libopustest.CELTQuantAllBandsParams{
				Channels: 2, LM: 2, Start: 0, End: nbEBands, Spread: 2,
				DualStereo: tc.dualStereo, Intensity: tc.intensity,
				TotalBits: totalBits, CodedBands: nbEBands, Seed: 0x17d9a62b,
				NbEBands: nbEBands, Pulses: pulses, TfRes: tfRes, Coded: mainCoded,
				QEXTCoded: extCoded, QEXTTotalBits: extTotalBits,
				QEXTPulses: extraPulses, QEXTCaps: extraCaps,
			})
			if err != nil {
				t.Fatalf("selected C oracle: %v", err)
			}

			var main, ext rangecoding.Decoder
			main.Init(mainCoded)
			ext.Init(extCoded)
			startExtTellFrac := ext.TellFrac()
			seed := uint32(0x17d9a62b)
			frameSize := 120 << 2
			left, right, collapse := QuantAllBandsDecodeQEXT(&main, 2, frameSize, 2, 0, nbEBands,
				pulses, tfRes, 0, 2, tc.dualStereo, tc.intensity, int(totalBits), 0, nbEBands,
				false, &seed, QEXTBandDecodeState{
					Decoder: &ext, ExtraPulses: extraPulses[:nbEBands],
					TotalBitsQ3: int(extTotalBits), Caps: extraCaps[:nbEBands],
				}, nil)
			if seed != ref.Seed {
				t.Fatalf("seed=%08x, selected C=%08x", seed, ref.Seed)
			}
			for i := 0; i < frameSize; i++ {
				if left[i] != ref.X[i] || right[i] != ref.X[frameSize+i] {
					t.Fatalf("band sample[%d] got=(%d,%d), selected C=(%d,%d)",
						i, left[i], right[i], ref.X[i], ref.X[frameSize+i])
				}
			}
			if len(collapse) != len(ref.Collapse) {
				t.Fatalf("collapse length=%d, selected C=%d", len(collapse), len(ref.Collapse))
			}
			for i := range collapse {
				if collapse[i] != ref.Collapse[i] {
					t.Fatalf("collapse[%d]=%d, selected C=%d", i, collapse[i], ref.Collapse[i])
				}
			}
			assertQEXTRangeDecoderState(t, "main", &main,
				ref.MainRange, ref.MainVal, int(ref.MainTell), int(ref.MainTellFrac), int(ref.MainError))
			assertQEXTRangeDecoderState(t, "extension", &ext,
				ref.ExtRange, ref.ExtVal, int(ref.ExtTell), int(ref.ExtTellFrac), int(ref.ExtError))
			if tc.dualStereo != 0 && ext.TellFrac() <= startExtTellFrac {
				t.Fatal("dual-stereo case did not consume extension coder symbols")
			}
		})
	}
}

func TestQuantPartitionQEXTUsesZeroResolutionCubicLeaf(t *testing.T) {
	libopustest.RequireOracle(t)
	coded := qextOracleBytes(0x9d21a54f, 32)
	ref, err := libopustest.ProbeCELTFixedQEXTCubic(libopustest.CELTFixedQEXTCubicParams{
		N: 3, Resolution: 0, Blocks: 1, Gain: q31One, Coded: coded,
	})
	if err != nil {
		t.Fatalf("selected C cubic oracle: %v", err)
	}

	var main, ext rangecoding.Decoder
	main.Init(qextOracleBytes(0x6b28df03, 16))
	ext.Init(coded)
	x := []int32{1 << 23, -(1 << 22), 1 << 21}
	seed := uint32(0x13579bdf)
	ctx := bandDecCtx{
		dec: &main, extDec: &ext,
		extTotalBits: ext.TellFrac() + 25,
		band:         0, seed: seed,
	}
	cm := quantPartitionDecodeWithExtBudget(&ctx, x, len(x), 0, 1, nil, 0, q31One, 1, 49)
	if cm != uint(ref.Collapse) {
		t.Fatalf("collapse=%d, selected C=%d", cm, ref.Collapse)
	}
	for i := range x {
		if x[i] != ref.Samples[i] {
			t.Fatalf("sample[%d]=%d, selected C=%d", i, x[i], ref.Samples[i])
		}
	}
	if ctx.seed != seed {
		t.Fatalf("zero-resolution cubic changed noise seed: got=%08x want=%08x", ctx.seed, seed)
	}
	assertQEXTRangeDecoderState(t, "extension", &ext,
		ref.Range, ref.Val, int(ref.Tell), int(ref.TellFrac), int(ref.Error))
	if ref.Collapse != 0 {
		t.Fatalf("zero-resolution cubic collapse=%d, want 0", ref.Collapse)
	}
}

func qextOracleBytes(seed uint32, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		seed ^= seed << 13
		seed ^= seed >> 17
		seed ^= seed << 5
		out[i] = byte(seed >> 11)
	}
	return out
}
