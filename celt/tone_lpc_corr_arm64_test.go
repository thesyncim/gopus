//go:build arm64

package celt

import "testing"

func TestToneLPCCorrArm64MatchesGeneric(t *testing.T) {
	x := []float32{
		1.0000001, -2.25, 3.5, 4.125, -5.75, 6.5, 7.25, -8.875,
		9.5, -10.125, 11.75, -12.5, 13.25, -14.875, 15.5, -16.125,
		17.75, -18.5, 19.25, -20.875, 21.5, -22.125, 23.75, -24.5,
	}
	for _, tc := range []struct {
		name   string
		cnt    int
		delay  int
		delay2 int
	}{
		{name: "delay1", cnt: 22, delay: 1, delay2: 2},
		{name: "delay2", cnt: 20, delay: 2, delay2: 4},
		{name: "tail", cnt: 17, delay: 3, delay2: 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotR00, gotR01, gotR02 := toneLPCCorr(x, tc.cnt, tc.delay, tc.delay2)
			wantR00, wantR01, wantR02 := toneLPCCorrSequentialForTest(x, tc.cnt, tc.delay, tc.delay2)
			if gotR00 != wantR00 || gotR01 != wantR01 || gotR02 != wantR02 {
				t.Fatalf("toneLPCCorr mismatch: got (%v,%v,%v) want (%v,%v,%v)",
					gotR00, gotR01, gotR02, wantR00, wantR01, wantR02)
			}
		})
	}
}

func toneLPCCorrSequentialForTest(x []float32, cnt, delay, delay2 int) (r00, r01, r02 float32) {
	for i := 0; i < cnt; i++ {
		xi := x[i]
		r00 += xi * xi
		r01 += xi * x[i+delay]
		r02 += xi * x[i+delay2]
	}
	return r00, r01, r02
}
