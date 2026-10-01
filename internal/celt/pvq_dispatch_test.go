package celt

import (
	"math"
	"reflect"
	"testing"
)

func pvqSearchPulseLoopRef(absX, y []float32, iy []int32, xy, yy float32, n, pulsesLeft int) (float32, float32) {
	for range pulsesLeft {
		yy += 1

		bestID := 0
		rxy := xy + absX[0]
		ryy := yy + y[0]
		bestNum := rxy * rxy
		bestDen := ryy
		for j := 1; j < n; j++ {
			rxy = xy + absX[j]
			ryy = yy + y[j]
			num := rxy * rxy
			if bestDen*num > ryy*bestNum {
				bestDen = ryy
				bestNum = num
				bestID = j
			}
		}

		xy += absX[bestID]
		yy += y[bestID]
		y[bestID] += 2
		iy[bestID]++
	}
	return xy, yy
}

func TestPVQDispatchMatchesGeneric(t *testing.T) {
	absX := []float32{1.5, 0.75, 2.25, 0.5, 1.125, 0.875, 1.75, 0.25}
	y := []float32{2, 0, 4, 2, 0, 6, 2, 0}

	yGot := append([]float32(nil), y...)
	yWant := append([]float32(nil), y...)
	iyGot := make([]int32, len(absX))
	iyWant := make([]int32, len(absX))
	gotXY, gotYY := pvqSearchPulseLoop(absX, yGot, iyGot, 1.25, 3.5, len(absX), 4)
	wantXY, wantYY := pvqSearchPulseLoopRef(absX, yWant, iyWant, 1.25, 3.5, len(absX), 4)
	if gotXY != wantXY || gotYY != wantYY || !reflect.DeepEqual(yGot, yWant) || !reflect.DeepEqual(iyGot, iyWant) {
		t.Fatalf("pvqSearchPulseLoop mismatch: got (%v,%v,%v,%v) want (%v,%v,%v,%v)", gotXY, gotYY, yGot, iyGot, wantXY, wantYY, yWant, iyWant)
	}

}

func TestPVQSearchPulseLoopExactOrder(t *testing.T) {
	negativeZero := math.Float32frombits(1 << 31)
	cases := []struct {
		name       string
		n          int
		pulses     int
		xy, yy     float32
		tie        bool
		signedZero bool
	}{
		{name: "n1", n: 1, pulses: 3},
		{name: "n2", n: 2, pulses: 3, xy: -0.25, yy: 1},
		{name: "n3-tail", n: 3, pulses: 4, xy: 0.5, yy: 2},
		{name: "n4-tail", n: 4, pulses: 4, xy: -1, yy: 4},
		{name: "n5-tail", n: 5, pulses: 7, xy: 1.25, yy: 8},
		{name: "n48-tie", n: 48, pulses: 16, tie: true},
		{name: "n48-signed-zero", n: 48, pulses: 16, xy: negativeZero, yy: negativeZero, signedZero: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			absX := make([]float32, tc.n)
			yInitial := make([]float32, tc.n)
			for i := range absX {
				if tc.tie {
					absX[i] = 1
					yInitial[i] = 0
				} else {
					absX[i] = float32((i*7)%11+1) / 16
					yInitial[i] = float32((i*3)%6) * 2
				}
			}
			if tc.signedZero {
				absX[0] = negativeZero
				yInitial[0] = negativeZero
			}

			yGot := append([]float32(nil), yInitial...)
			yWant := append([]float32(nil), yInitial...)
			iyGot := make([]int32, tc.n)
			iyWant := make([]int32, tc.n)
			gotXY, gotYY := pvqSearchPulseLoop(absX, yGot, iyGot, tc.xy, tc.yy, tc.n, tc.pulses)
			wantXY, wantYY := pvqSearchPulseLoopRef(absX, yWant, iyWant, tc.xy, tc.yy, tc.n, tc.pulses)
			if math.Float32bits(gotXY) != math.Float32bits(wantXY) || math.Float32bits(gotYY) != math.Float32bits(wantYY) {
				t.Fatalf("xy/yy bits got (%08x,%08x), want (%08x,%08x)", math.Float32bits(gotXY), math.Float32bits(gotYY), math.Float32bits(wantXY), math.Float32bits(wantYY))
			}
			for i := range yGot {
				if math.Float32bits(yGot[i]) != math.Float32bits(yWant[i]) || iyGot[i] != iyWant[i] {
					t.Fatalf("index %d got y/iy (%08x,%d), want (%08x,%d)", i, math.Float32bits(yGot[i]), iyGot[i], math.Float32bits(yWant[i]), iyWant[i])
				}
			}
		})
	}
}

func TestPVQSearchPulseLoopZeroAllocs(t *testing.T) {
	const n, pulses = 48, 16
	absX := make([]float32, n)
	y := make([]float32, n)
	iy := make([]int32, n)
	for i := range absX {
		absX[i] = float32((i*7)%11+1) / 16
	}
	run := func() {
		clear(y)
		clear(iy)
		_, _ = pvqSearchPulseLoop(absX, y, iy, 0, 0, n, pulses)
	}
	run()
	if got := testing.AllocsPerRun(100, run); got != 0 {
		t.Fatalf("pvqSearchPulseLoop allocations/run = %v, want 0", got)
	}
}

func BenchmarkPVQSearchPulseLoopCurrent(b *testing.B) {
	absX := make([]float32, 48)
	yBase := make([]float32, 48)
	iyBase := make([]int32, 48)
	for i := range absX {
		absX[i] = float32(((i * 7) % 11) + 1)
		yBase[i] = float32((i * 3) % 6)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		y := append([]float32(nil), yBase...)
		iy := append([]int32(nil), iyBase...)
		_, _ = pvqSearchPulseLoop(absX, y, iy, 3.25, 9.5, len(absX), 16)
	}
}

func BenchmarkPVQSearchPulseLoopGeneric(b *testing.B) {
	absX := make([]float32, 48)
	yBase := make([]float32, 48)
	iyBase := make([]int32, 48)
	for i := range absX {
		absX[i] = float32(((i * 7) % 11) + 1)
		yBase[i] = float32((i * 3) % 6)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		y := append([]float32(nil), yBase...)
		iy := append([]int32(nil), iyBase...)
		_, _ = pvqSearchPulseLoopRef(absX, y, iy, 3.25, 9.5, len(absX), 16)
	}
}
