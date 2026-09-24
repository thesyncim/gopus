//go:build arm64 && goexperiment.simd && !nosimd

package celt

import "testing"

func TestMDCTMidFoldStoreNeonChecksPackedDestinationBounds(t *testing.T) {
	const n4, blocks = 4, 1
	dst := make([]kissCpx, n4)
	bitrev := []int{0, 1, 2, n4}
	samples := make([]float32, 8)
	trig := make([]float32, n4*2)
	defer func() {
		if recover() == nil {
			t.Fatal("mid-fold packed store did not reject an out-of-range bit-reversal index")
		}
	}()
	mdctMidFoldStoreNeon(dst, bitrev, samples, trig, 0, n4, 0, 6, blocks, 0.5)
}
