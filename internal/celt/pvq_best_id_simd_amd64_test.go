//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import (
	"github.com/thesyncim/gopus/internal/libopustest"
	"math"
	"math/rand"
	"os"
	"simd/archsimd"
	"testing"
	"unsafe"
)

var pvqBestIDVectorSink int

func pvqBestIDScalarLaneReference(absX, y []float32, xy, yy float32) int {
	xy4 := broadcastF32x4Arch(xy)
	yy4 := broadcastF32x4Arch(yy)
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
	// _mm_max_ps(max, shuffle(max, 1,0,3,2)), then the same with 2,3,0,1.
	var half, max2 [4]float32
	for lane := range 4 {
		half[lane] = x86MaxPS32(laneMax[lane], laneMax[lane^2])
	}
	for lane := range 4 {
		max2[lane] = x86MaxPS32(half[lane], half[lane^1])
	}
	bestID := 0
	for lane := range 4 {
		if laneMax[lane] == max2[lane] && laneID[lane] > bestID {
			bestID = laneID[lane]
		}
	}
	return bestID
}

// x86MaxPS32 is one lane of _mm_max_ps(a, b): b unless a > b.
func x86MaxPS32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
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

func TestPVQPulsesFiniteMatchesExactSearch(t *testing.T) {
	if !useX86PVQSearchSSE2 {
		t.Skip("AVX unavailable")
	}
	rng := rand.New(rand.NewSource(0x9f1e))
	for trial := range 2000 {
		n := 1 + rng.Intn(176)
		workN := n + 3
		absX := make([]float32, workN)
		y := make([]float32, workN)
		for j := range n {
			switch rng.Intn(6) {
			case 0:
				absX[j] = 0
			case 1:
				absX[j] = absX[rng.Intn(j+1)]
			default:
				absX[j] = float32(math.Abs(rng.NormFloat64()))
			}
			y[j] = float32(2 * rng.Intn(4))
		}
		for j := n; j < workN; j++ {
			absX[j], y[j] = -100, 100
		}
		var xy, yy float32
		for j := range n {
			xy += absX[j] * y[j] / 2
			yy += y[j] * y[j] / 4
		}
		pulses := rng.Intn(n + 4)

		gotY := append([]float32(nil), y...)
		gotIy := make([]int32, workN)
		gxy, gyy := x86PVQPulsesFinite(absX, gotY, gotIy, xy, yy, n, pulses)

		wantY := append([]float32(nil), y...)
		wantIy := make([]int32, workN)
		wxy, wyy := xy, yy
		for range pulses {
			wyy++
			best := x86PVQSearchBestIDExact(absX, wantY, wxy, wyy, n)
			wxy += absX[best]
			wyy += wantY[best]
			wantY[best] += 2
			wantIy[best]++
		}
		if math.Float32bits(gxy) != math.Float32bits(wxy) || math.Float32bits(gyy) != math.Float32bits(wyy) {
			t.Fatalf("trial %d n %d pulses %d: xy/yy got %v/%v want %v/%v", trial, n, pulses, gxy, gyy, wxy, wyy)
		}
		for j := range workN {
			if gotIy[j] != wantIy[j] || math.Float32bits(gotY[j]) != math.Float32bits(wantY[j]) {
				t.Fatalf("trial %d n %d pulses %d: position %d iy got %d want %d", trial, n, pulses, j, gotIy[j], wantIy[j])
			}
		}
	}
}

func TestPVQSearchSIMDDispatchUsesAVX(t *testing.T) {
	avx := archsimd.X86.AVX()
	if useX86PVQSearchSSE2 != avx {
		t.Fatalf("SIMD PVQ dispatch=%t, AVX feature=%t", useX86PVQSearchSSE2, avx)
	}
	if !avx {
		if os.Getenv("GOPUS_REQUIRE_PVQ_SIMD") == "1" {
			t.Fatal("SIMD PVQ oracle job requires AVX so the Go SIMD path is compared with libopus SSE2")
		}
		if libopustest.StrictRefRequired() {
			yy, iy, err := libopustest.ProbeCELTPVQSearchFloatSSE2([]float32{1, -1, 1, -1}, 2)
			if err != nil {
				t.Fatalf("compile or run the direct libopus SSE2 PVQ oracle: %v", err)
			}
			if len(iy) != 4 || yy <= 0 {
				t.Fatalf("direct libopus SSE2 PVQ oracle returned yy=%g, iy=%v", yy, iy)
			}
		}
		t.Log("AVX is unavailable; the Go PVQ dispatch selects the scalar path")
		return
	}
	t.Log("Go PVQ dispatch: AVX-enabled archsimd path; C oracle: compiled celt/x86/vq_sse2.c with -msse2")
}
