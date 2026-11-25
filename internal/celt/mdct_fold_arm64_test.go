//go:build arm64 && !nosimd && !purego

package celt

import (
	"math"
	"math/rand"
	"testing"
)

func TestMDCTFoldStoreNeonBitExact(t *testing.T) {
	rng := rand.New(rand.NewSource(43))
	const overlap = 120
	const limit1 = 30
	for _, n4 := range []int{30, 60, 120, 240} {
		n2 := 2 * n4
		samples := make([]float32, n2+overlap)
		for i := range samples {
			samples[i] = float32(rng.NormFloat64())
		}
		window := make([]float32, overlap)
		for i := range window {
			window[i] = rng.Float32()
		}
		trig := make([]float32, n2)
		for i := range trig {
			trig[i] = float32(rng.NormFloat64())
		}
		bitrev := rng.Perm(n4)
		preScale := float32(1.0) / float32(n4)

		for _, blocks := range []int{1, limit1 / 4} {
			// Leading fold geometry.
			i0 := 0
			xp1 := overlap / 2
			xp2 := n2 - 1 + overlap/2
			wp1 := overlap / 2
			wp2 := overlap/2 - 1
			got := make([]float32, 2*n4)
			want := make([]float32, 2*n4)
			for j := 0; j < 4*blocks; j++ {
				re := mdctMulAddMix(samples[xp1+n2+2*j], samples[xp2-2*j], window[wp2-2*j], window[wp1+2*j])
				im := mdctMulSubMix(samples[xp1+2*j], samples[xp2-n2-2*j], window[wp1+2*j], window[wp2-2*j])
				mdctStoreDirectStageFMALike(want, bitrev[i0+j], preScale, re, im, trig[i0+j], trig[n4+i0+j])
			}
			mdctFold1StoreNeon(got, bitrev, samples, window, trig, i0, n4, n2, xp1, xp2, wp1, wp2, blocks, preScale)
			compareInterleavedCpx(t, "fold1", n4, blocks, got, want)
			if allocs := testing.AllocsPerRun(100, func() {
				mdctFold1StoreNeon(got, bitrev, samples, window, trig, i0, n4, n2, xp1, xp2, wp1, wp2, blocks, preScale)
			}); allocs != 0 {
				t.Fatalf("fold1 n4=%d blocks=%d: %.2f allocs/op, want 0", n4, blocks, allocs)
			}

			// Trailing fold geometry.
			if n4 < 2*limit1 {
				continue
			}
			i0 = n4 - limit1
			xp1 = overlap/2 + 2*i0
			xp2 = n2 - 1 + overlap/2 - 2*i0
			wp1 = 0
			wp2 = overlap - 1
			got = make([]float32, 2*n4)
			want = make([]float32, 2*n4)
			for j := 0; j < 4*blocks; j++ {
				re := mdctNegMulAddMixEncode(samples[xp1-n2+2*j], samples[xp2-2*j], window[wp1+2*j], window[wp2-2*j])
				im := mdctMulAddMix(samples[xp1+2*j], samples[xp2+n2-2*j], window[wp2-2*j], window[wp1+2*j])
				mdctStoreDirectStageFMALike(want, bitrev[i0+j], preScale, re, im, trig[i0+j], trig[n4+i0+j])
			}
			mdctFold3StoreNeon(got, bitrev, samples, window, trig, i0, n4, n2, xp1, xp2, wp1, wp2, blocks, preScale)
			compareInterleavedCpx(t, "fold3", n4, blocks, got, want)
			if allocs := testing.AllocsPerRun(100, func() {
				mdctFold3StoreNeon(got, bitrev, samples, window, trig, i0, n4, n2, xp1, xp2, wp1, wp2, blocks, preScale)
			}); allocs != 0 {
				t.Fatalf("fold3 n4=%d blocks=%d: %.2f allocs/op, want 0", n4, blocks, allocs)
			}
		}
	}
}

func compareInterleavedCpx(t *testing.T, name string, n4, blocks int, got, want []float32) {
	t.Helper()
	for k := 0; k < len(want); k += 2 {
		if math.Float32bits(got[k]) != math.Float32bits(want[k]) ||
			math.Float32bits(got[k+1]) != math.Float32bits(want[k+1]) {
			t.Fatalf("%s n4=%d blocks=%d: dst[%d] = (%08x,%08x), want (%08x,%08x)",
				name, n4, blocks, k/2,
				math.Float32bits(got[k]), math.Float32bits(got[k+1]),
				math.Float32bits(want[k]), math.Float32bits(want[k+1]))
		}
	}
}

func TestMDCTMidFoldStoreNeonBitExact(t *testing.T) {
	rng := rand.New(rand.NewSource(41))
	type shape struct {
		n4, i0, xp1, xp2, blocks int
	}
	var shapes []shape
	// Real geometries: i0 = limit1, xp1 = overlap/2 + 2*limit1,
	// xp2 = n2-1+overlap/2 - 2*limit1, overlap=120, limit1=30.
	for _, n4 := range []int{60, 120, 240} {
		mid := n4 - 2*30
		if mid < 4 {
			continue
		}
		shapes = append(shapes, shape{
			n4: n4, i0: 30, xp1: 60 + 60, xp2: 2*n4 - 1 + 60 - 60, blocks: mid >> 2,
		})
	}
	// Off-grid shapes exercising remainder-adjacent block counts.
	for range 20 {
		n4 := 8 + rng.Intn(64)*4
		blocks := 1 + rng.Intn(n4/4)
		i0 := rng.Intn(n4 - 4*blocks + 1)
		xp1 := rng.Intn(8)
		xp2 := 8*blocks - 2 + rng.Intn(16) // ensures xp2-2*done+2 >= 0
		shapes = append(shapes, shape{n4: n4, i0: i0, xp1: xp1, xp2: xp2, blocks: blocks})
	}

	for si, s := range shapes {
		done := 4 * s.blocks
		need := s.xp1 + 2*done
		if low := s.xp2 + 2; low > need {
			need = low
		}
		samples := make([]float32, need)
		for i := range samples {
			samples[i] = float32(rng.NormFloat64())
		}
		trig := make([]float32, 2*s.n4)
		for i := range trig {
			trig[i] = float32(rng.NormFloat64())
		}
		bitrev := make([]int, s.n4)
		perm := rng.Perm(s.n4)
		copy(bitrev, perm)
		preScale := float32(1.0) / float32(s.n4)

		got := make([]float32, 2*s.n4)
		want := make([]float32, 2*s.n4)
		for j := range done {
			re := samples[s.xp2-2*j]
			im := samples[s.xp1+2*j]
			mdctStoreDirectStageFMALike(want, bitrev[s.i0+j], preScale, re, im, trig[s.i0+j], trig[s.n4+s.i0+j])
		}
		mdctMidFoldStoreNeon(got, bitrev, samples, trig, s.i0, s.n4, s.xp1, s.xp2, s.blocks, preScale)

		for k := range want {
			if math.Float32bits(got[k]) != math.Float32bits(want[k]) {
				t.Fatalf("shape %d (%+v): dst[%d] = %08x, want %08x",
					si, s, k,
					math.Float32bits(got[k]), math.Float32bits(want[k]))
			}
		}
	}
}

func TestMDCTMidFoldStoreNeonNoAllocs(t *testing.T) {
	const n4, blocks = 64, 8
	dst := make([]float32, 2*n4)
	bitrev := make([]int, n4)
	for i := range bitrev {
		bitrev[i] = i
	}
	samples := make([]float32, 320)
	trig := make([]float32, n4*2)
	for i := range samples {
		samples[i] = float32((i*37)%127-63) * 0.0078125
	}
	for i := range trig {
		trig[i] = float32((i*37)%127-63) * 0.0078125
	}
	call := func() {
		mdctMidFoldStoreNeon(dst, bitrev, samples, trig, 0, n4, 80, 220, blocks, 0.5)
	}
	call()
	if allocs := testing.AllocsPerRun(100, call); allocs != 0 {
		t.Fatalf("allocs per call = %v, want 0", allocs)
	}
}

func TestMDCTPostTwiddleNeonBitExact(t *testing.T) {
	rng := rand.New(rand.NewSource(37))
	for _, n4 := range []int{8, 16, 24, 30, 60, 120, 240} {
		n2 := 2 * n4
		for trial := range 6 {
			stageStorage := make([]float32, 2*n4+4)
			stageStorage[0], stageStorage[1] = 123.5, -91.25
			stageStorage[2*n4+2], stageStorage[2*n4+3] = -65.75, 47.125
			stage := stageStorage[2 : 2*n4+2]
			for i := range stage {
				stage[i] = float32(rng.NormFloat64())
			}
			trigStorage := make([]float32, n2+2)
			trigStorage[0] = 87.75
			trigStorage[n2+1] = -43.625
			trig := trigStorage[1 : n2+1]
			for i := range trig {
				trig[i] = float32(rng.NormFloat64())
			}
			pairBlocks := n4 >> 3
			if pairBlocks == 0 {
				continue
			}
			gotStorage := make([]float32, n2+2)
			gotStorage[0] = 31.875
			gotStorage[n2+1] = -27.125
			got := gotStorage[1 : n2+1]
			want := make([]float32, n2)
			for i := 0; i < 4*pairBlocks; i++ {
				j := n4 - 1 - i
				want[2*i] = mdctMulSubMixEncode(stage[2*i+1], stage[2*i], trig[n4+i], trig[i])
				want[n2-1-2*i] = mdctMulAddMixEncode(stage[2*i], stage[2*i+1], trig[n4+i], trig[i])
				want[2*j] = mdctMulSubMixEncode(stage[2*j+1], stage[2*j], trig[n4+j], trig[j])
				want[n2-1-2*j] = mdctMulAddMixEncode(stage[2*j], stage[2*j+1], trig[n4+j], trig[j])
			}
			mdctPostTwiddleNeon(got, stage, trig, n2, n4, pairBlocks)
			for k := range want {
				if math.Float32bits(got[k]) != math.Float32bits(want[k]) {
					t.Fatalf("n4=%d trial=%d: coeffs[%d] = %08x, want %08x", n4, trial, k,
						math.Float32bits(got[k]), math.Float32bits(want[k]))
				}
			}
			if stageStorage[0] != 123.5 || stageStorage[1] != -91.25 || stageStorage[2*n4+2] != -65.75 || stageStorage[2*n4+3] != 47.125 {
				t.Fatalf("n4=%d trial=%d: FFT input canary changed", n4, trial)
			}
			if trigStorage[0] != 87.75 || trigStorage[n2+1] != -43.625 {
				t.Fatalf("n4=%d trial=%d: trig input canary changed", n4, trial)
			}
			if gotStorage[0] != 31.875 || gotStorage[n2+1] != -27.125 {
				t.Fatalf("n4=%d trial=%d: coefficient output canary changed", n4, trial)
			}
		}
	}
}

func TestMDCTPostTwiddleNeonAllocs(t *testing.T) {
	const n4, n2 = 64, 128
	coeffs := make([]float32, n2)
	stage := make([]float32, 2*n4)
	trig := make([]float32, n2)
	for i := range n4 {
		stage[2*i] = float32(i) * 0.03125
		stage[2*i+1] = -float32(i) * 0.015625
	}
	for i := range trig {
		trig[i] = float32(i) * 0.0078125
	}
	run := func() { mdctPostTwiddleNeon(coeffs, stage, trig, n2, n4, n4/8) }
	run()
	if allocs := testing.AllocsPerRun(100, run); allocs != 0 {
		t.Fatalf("post-twiddle allocated %v times per call, want 0", allocs)
	}
}
