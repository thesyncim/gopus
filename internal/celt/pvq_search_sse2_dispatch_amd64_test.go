//go:build amd64 && goexperiment.simd && !nosimd

package celt

import (
	"os"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"simd/archsimd"
)

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
