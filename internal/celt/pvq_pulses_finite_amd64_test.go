//go:build amd64 && goexperiment.simd && !nosimd

package celt

import (
	"math"
	"math/rand"
	"testing"
	"unsafe"
)

// TestPVQPulsesFiniteMatchesExactSearch requires the finite-score pulse loop
// to place every pulse where the _mm_max_ps-exact per-pulse search does, and
// to return the same xy and yy bits.
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
		gxy, gyy := x86PVQPulsesFinite(unsafe.Pointer(&absX[0]), unsafe.Pointer(&gotY[0]), unsafe.Pointer(&gotIy[0]), xy, yy, n, pulses)

		wantY := append([]float32(nil), y...)
		wantIy := make([]int32, workN)
		wxy, wyy := xy, yy
		for range pulses {
			wyy++
			best := x86PVQSearchBestIDExact(unsafe.Pointer(&absX[0]), unsafe.Pointer(&wantY[0]), wxy, wyy, n)
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
