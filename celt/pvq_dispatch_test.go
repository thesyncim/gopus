package celt

import (
	"reflect"
	"testing"
)

func pvqSearchBestPosRef(absX, y []float32, xy, yy float32, n int) int {
	if n <= 0 {
		return 0
	}
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
	return bestID
}

func pvqSearchPulseLoopRef(absX, y []float32, iy []int, xy, yy float32, n, pulsesLeft int) (float32, float32) {
	for i := 0; i < pulsesLeft; i++ {
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

	if got, want := pvqSearchBestPos(absX, y, 1.25, 3.5, len(absX)), pvqSearchBestPosRef(absX, y, 1.25, 3.5, len(absX)); got != want {
		t.Fatalf("pvqSearchBestPos mismatch: got %v want %v", got, want)
	}

	yGot := append([]float32(nil), y...)
	yWant := append([]float32(nil), y...)
	iyGot := make([]int, len(absX))
	iyWant := make([]int, len(absX))
	gotXY, gotYY := pvqSearchPulseLoop(absX, yGot, iyGot, 1.25, 3.5, len(absX), 4)
	wantXY, wantYY := pvqSearchPulseLoopRef(absX, yWant, iyWant, 1.25, 3.5, len(absX), 4)
	if gotXY != wantXY || gotYY != wantYY || !reflect.DeepEqual(yGot, yWant) || !reflect.DeepEqual(iyGot, iyWant) {
		t.Fatalf("pvqSearchPulseLoop mismatch: got (%v,%v,%v,%v) want (%v,%v,%v,%v)", gotXY, gotYY, yGot, iyGot, wantXY, wantYY, yWant, iyWant)
	}

}

func BenchmarkPVQSearchPulseLoopCurrent(b *testing.B) {
	absX := make([]float32, 48)
	yBase := make([]float32, 48)
	iyBase := make([]int, 48)
	for i := range absX {
		absX[i] = float32(((i * 7) % 11) + 1)
		yBase[i] = float32((i * 3) % 6)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		y := append([]float32(nil), yBase...)
		iy := append([]int(nil), iyBase...)
		_, _ = pvqSearchPulseLoop(absX, y, iy, 3.25, 9.5, len(absX), 16)
	}
}

func BenchmarkPVQSearchPulseLoopGeneric(b *testing.B) {
	absX := make([]float32, 48)
	yBase := make([]float32, 48)
	iyBase := make([]int, 48)
	for i := range absX {
		absX[i] = float32(((i * 7) % 11) + 1)
		yBase[i] = float32((i * 3) % 6)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		y := append([]float32(nil), yBase...)
		iy := append([]int(nil), iyBase...)
		_, _ = pvqSearchPulseLoopRef(absX, y, iy, 3.25, 9.5, len(absX), 16)
	}
}
