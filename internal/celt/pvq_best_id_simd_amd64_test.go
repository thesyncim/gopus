//go:build amd64 && goexperiment.simd && !nosimd

package celt

import (
	"math"
	"testing"
	"unsafe"

	"simd/archsimd"
)

var pvqBestIDVectorSink int

func pvqBestIDScalarLaneReference(absX, y []float32, xy, yy float32) int {
	xy4 := archsimd.BroadcastFloat32x4(xy)
	yy4 := archsimd.BroadcastFloat32x4(yy)
	var laneMax [4]float32
	var laneID [4]int
	for i := 0; i < len(absX); i += 4 {
		x4 := archsimd.LoadFloat32x4Array((*[4]float32)(unsafe.Pointer(&absX[i])))
		y4 := archsimd.LoadFloat32x4Array((*[4]float32)(unsafe.Pointer(&y[i])))
		var score [4]float32
		x4.Add(xy4).Mul(y4.Add(yy4).ReciprocalSqrt()).StoreArray(&score)
		for lane := range 4 {
			previous := laneMax[lane]
			if score[lane] > previous {
				laneID[lane] = i + lane
			}
			laneMax[lane] = x86MaxPS32(previous, score[lane])
		}
	}
	max02 := x86MaxPS32(laneMax[0], laneMax[2])
	max13 := x86MaxPS32(laneMax[1], laneMax[3])
	bestScore := x86MaxPS32(max02, max13)
	bestID := 0
	for lane := range 4 {
		if laneMax[lane] == bestScore && laneID[lane] > bestID {
			bestID = laneID[lane]
		}
	}
	return bestID
}

func TestPVQBestIDVectorMatchesScalarLaneOrder(t *testing.T) {
	if !archsimd.X86.AVX() {
		t.Skip("AVX unavailable")
	}
	for _, n := range []int{4, 8, 16, 48, 96} {
		absX := make([]float32, n)
		y := make([]float32, n)
		for i := range absX {
			absX[i] = float32((i*37)%101+1) * 0.03125
			y[i] = float32((i*19)%97) * 0.0625
		}
		for _, edge := range []float32{0, math.Float32frombits(1), float32(math.Inf(1)), float32(math.NaN())} {
			absX[n/2] = edge
			got := x86PVQSearchBestIDSSE2(absX, y, 3.25, 9.5, n)
			want := pvqBestIDScalarLaneReference(absX, y, 3.25, 9.5)
			if got != want {
				t.Fatalf("n=%d edge=%08x: best ID=%d, want %d", n, math.Float32bits(edge), got, want)
			}
		}
	}
}

func TestPVQBestIDVectorZeroAllocs(t *testing.T) {
	if !archsimd.X86.AVX() {
		t.Skip("AVX unavailable")
	}
	const n = 48
	absX := make([]float32, n)
	y := make([]float32, n)
	for i := range absX {
		absX[i] = float32(i+1) * 0.0625
		y[i] = float32(i+3) * 0.03125
	}
	run := func() { pvqBestIDVectorSink = x86PVQSearchBestIDSSE2(absX, y, 3.25, 9.5, n) }
	run()
	if got := testing.AllocsPerRun(100, run); got != 0 {
		t.Fatalf("PVQ best ID allocated: %g allocs/run", got)
	}
}
