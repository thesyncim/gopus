package celt

import (
	"reflect"
	"testing"
)

func pvqSearchBestPosRef(absX, y []float32, xy, yy float64, n int) int {
	if n <= 0 {
		return 0
	}
	xyf := float32(xy)
	yyf := float32(yy)
	bestID := 0
	rxy := xyf + absX[0]
	ryy := yyf + y[0]
	bestNum := rxy * rxy
	bestDen := ryy
	for j := 1; j < n; j++ {
		rxy = xyf + absX[j]
		ryy = yyf + y[j]
		num := rxy * rxy
		if bestDen*num > ryy*bestNum {
			bestDen = ryy
			bestNum = num
			bestID = j
		}
	}
	return bestID
}

func pvqSearchPulseLoopRef(absX, y []float32, iy []int, xy, yy float64, n, pulsesLeft int) (float64, float64) {
	xyf := float32(xy)
	yyf := float32(yy)
	for i := 0; i < pulsesLeft; i++ {
		yyf += 1

		bestID := 0
		rxy := xyf + absX[0]
		ryy := yyf + y[0]
		bestNum := rxy * rxy
		bestDen := ryy
		for j := 1; j < n; j++ {
			rxy = xyf + absX[j]
			ryy = yyf + y[j]
			num := rxy * rxy
			if bestDen*num > ryy*bestNum {
				bestDen = ryy
				bestNum = num
				bestID = j
			}
		}

		xyf += absX[bestID]
		yyf += y[bestID]
		y[bestID] += 2
		iy[bestID]++
	}
	return float64(xyf), float64(yyf)
}

func pvqExtractAbsSignRef(x []float64, absX []float32, y []float32, signx []byte, iy []int, n int) {
	for j := 0; j < n; j++ {
		iy[j] = 0
		signx[j] = 0
		y[j] = 0
		xj := x[j]
		if xj < 0 {
			signx[j] = 1
			absX[j] = float32(-xj)
		} else {
			absX[j] = float32(xj)
		}
	}
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

	signInput := []float64{-1.5, 2.25, 0, -0.125, 4, -7.5, 8.25, -9.75}
	signGotAbs := make([]float32, len(signInput))
	signWantAbs := make([]float32, len(signInput))
	signGotY := make([]float32, len(signInput))
	signWantY := make([]float32, len(signInput))
	signGotBits := make([]byte, len(signInput))
	signWantBits := make([]byte, len(signInput))
	signGotIY := make([]int, len(signInput))
	signWantIY := make([]int, len(signInput))
	pvqExtractAbsSign(signInput, signGotAbs, signGotY, signGotBits, signGotIY, len(signInput))
	pvqExtractAbsSignRef(signInput, signWantAbs, signWantY, signWantBits, signWantIY, len(signInput))
	if !reflect.DeepEqual(signGotAbs, signWantAbs) || !reflect.DeepEqual(signGotY, signWantY) || !reflect.DeepEqual(signGotBits, signWantBits) || !reflect.DeepEqual(signGotIY, signWantIY) {
		t.Fatalf("pvqExtractAbsSign mismatch")
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

func BenchmarkPVQExtractAbsSignCurrent(b *testing.B) {
	x := make([]float64, 48)
	absX := make([]float32, 48)
	y := make([]float32, 48)
	signx := make([]byte, 48)
	iy := make([]int, 48)
	for i := range x {
		x[i] = float64((i%13)-6) * 0.375
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pvqExtractAbsSign(x, absX, y, signx, iy, len(x))
	}
}

func BenchmarkPVQExtractAbsSignGeneric(b *testing.B) {
	x := make([]float64, 48)
	absX := make([]float32, 48)
	y := make([]float32, 48)
	signx := make([]byte, 48)
	iy := make([]int, 48)
	for i := range x {
		x[i] = float64((i%13)-6) * 0.375
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pvqExtractAbsSignRef(x, absX, y, signx, iy, len(x))
	}
}
