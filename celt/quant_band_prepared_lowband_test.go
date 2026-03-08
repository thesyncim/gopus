package celt

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/thesyncim/gopus/rangecoding"
)

func TestQuantBandStereoPreparedLowbandMatchesStandard(t *testing.T) {
	cases := []struct {
		name       string
		band       int
		n          int
		b          int
		B          int
		lm         int
		tfChange   int
		fill       int
		thetaRound int
	}{
		{name: "flat_stride8_round_down", band: 17, n: 32, b: 60, B: 8, lm: 2, tfChange: 0, fill: 0xE7, thetaRound: -1},
		{name: "flat_stride8_round_up", band: 17, n: 32, b: 60, B: 8, lm: 2, tfChange: 0, fill: 0xE7, thetaRound: 1},
		{name: "time_divide_round_down", band: 18, n: 48, b: 72, B: 8, lm: 2, tfChange: -1, fill: 0xDB, thetaRound: -1},
		{name: "recombine_round_up", band: 16, n: 24, b: 36, B: 4, lm: 1, tfChange: 1, fill: 0x0D, thetaRound: 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			xBase := make([]float64, tc.n)
			yBase := make([]float64, tc.n)
			lowbandBase := make([]float64, tc.n)
			for i := 0; i < tc.n; i++ {
				xBase[i] = float64(((i*7)%19)-9) * 0.0875
				yBase[i] = float64(((i*5)%23)-11) * 0.07125
				lowbandBase[i] = float64(((i*3)%17)-8) * 0.0625
			}

			bandE := make([]float64, 2*MaxBands)
			for i := range bandE {
				bandE[i] = 0.35 + float64((i%9)+1)*0.03
			}

			x1 := append([]float64(nil), xBase...)
			y1 := append([]float64(nil), yBase...)
			lowband1 := append([]float64(nil), lowbandBase...)
			out1 := make([]float64, tc.n)
			var buf1 [512]byte
			var re1 rangecoding.Encoder
			re1.Init(buf1[:])
			var scratch1 bandEncodeScratch
			ctx1 := bandCtx{
				re:            &re1,
				encode:        true,
				bandE:         bandE,
				nbBands:       MaxBands,
				channels:      2,
				spread:        spreadNormal,
				tfChange:      tc.tfChange,
				remainingBits: tc.b + 96,
				intensity:     MaxBands,
				band:          tc.band,
				resynth:       true,
				thetaRound:    tc.thetaRound,
				encScratch:    &scratch1,
			}
			cm1 := quantBandStereo(&ctx1, x1, y1, tc.n, tc.b, tc.B, lowband1, tc.lm, out1, scratch1.ensureLowbandScratch(tc.n), tc.fill)
			data1 := append([]byte(nil), re1.Done()...)

			x2 := append([]float64(nil), xBase...)
			y2 := append([]float64(nil), yBase...)
			lowband2 := append([]float64(nil), lowbandBase...)
			out2 := make([]float64, tc.n)
			var buf2 [512]byte
			var re2 rangecoding.Encoder
			re2.Init(buf2[:])
			var scratch2 bandEncodeScratch
			prepared := prepareQuantBandLowband(scratch2.ensureLowbandScratch(tc.n), lowband2, tc.n, tc.B, tc.tfChange, &scratch2)
			if prepared == nil {
				t.Fatal("prepareQuantBandLowband returned nil")
			}
			ctx2 := bandCtx{
				re:            &re2,
				encode:        true,
				bandE:         bandE,
				nbBands:       MaxBands,
				channels:      2,
				spread:        spreadNormal,
				tfChange:      tc.tfChange,
				remainingBits: tc.b + 96,
				intensity:     MaxBands,
				band:          tc.band,
				resynth:       true,
				thetaRound:    tc.thetaRound,
				encScratch:    &scratch2,
			}
			cm2 := quantBandStereoPreparedLowband(&ctx2, x2, y2, tc.n, tc.b, tc.B, prepared, tc.lm, out2, scratch2.ensureLowbandScratch(tc.n), tc.fill, true)
			data2 := append([]byte(nil), re2.Done()...)

			if cm1 != cm2 {
				t.Fatalf("collapse mask mismatch: got %d want %d", cm2, cm1)
			}
			if ctx1.remainingBits != ctx2.remainingBits {
				t.Fatalf("remainingBits mismatch: got %d want %d", ctx2.remainingBits, ctx1.remainingBits)
			}
			if !reflect.DeepEqual(x2, x1) {
				t.Fatalf("x mismatch: got %v want %v", x2, x1)
			}
			if !reflect.DeepEqual(y2, y1) {
				t.Fatalf("y mismatch: got %v want %v", y2, y1)
			}
			if !reflect.DeepEqual(out2, out1) {
				t.Fatalf("lowbandOut mismatch: got %v want %v", out2, out1)
			}
			if !bytes.Equal(data2, data1) {
				t.Fatalf("range coder output mismatch: got %v want %v", data2, data1)
			}
		})
	}
}
