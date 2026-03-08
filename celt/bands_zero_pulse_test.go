package celt

import "testing"

func legacyZeroPulseResynth(x, lowband []float64, seed *uint32, gain float64) {
	if lowband == nil {
		if seed != nil {
			for i := range x {
				*seed = (*seed)*1664525 + 1013904223
				x[i] = float64(int32(*seed) >> 20)
			}
		}
		renormalizeVector(x, gain)
		return
	}
	if seed != nil {
		for i := range x {
			*seed = (*seed)*1664525 + 1013904223
			tmp := 1.0 / 256.0
			if (*seed & 0x8000) == 0 {
				tmp = -tmp
			}
			if i < len(lowband) {
				x[i] = lowband[i] + tmp
			} else {
				x[i] = tmp
			}
		}
	}
	renormalizeVector(x, gain)
}

func makeLowband(n int) []float64 {
	lowband := make([]float64, n)
	for i := range lowband {
		v := float64((i%7)-3) * 0.125
		if i%3 == 1 {
			v = -v * 0.75
		}
		lowband[i] = v
	}
	return lowband
}

func TestSeededZeroPulseResynthMatchesLegacy(t *testing.T) {
	gains := []float64{0.25, 0.75, 1.0, 1.5}
	seeds := []uint32{1, 0x8000, 0x12345678, 0xdeadbeef}
	lengths := []int{1, 2, 3, 4, 7, 8, 15, 16, 17}

	for _, n := range lengths {
		for _, gain := range gains {
			for _, seed0 := range seeds {
				currentNoise := make([]float64, n)
				legacyNoise := make([]float64, n)
				seedCurrentNoise := seed0
				seedLegacyNoise := seed0

				if ok := seededZeroPulseResynth(currentNoise, nil, &seedCurrentNoise, gain); !ok {
					t.Fatalf("noise fast path unexpectedly refused n=%d gain=%v", n, gain)
				}
				legacyZeroPulseResynth(legacyNoise, nil, &seedLegacyNoise, gain)

				if seedCurrentNoise != seedLegacyNoise {
					t.Fatalf("noise seed mismatch n=%d gain=%v seed=%08x: got %08x want %08x",
						n, gain, seed0, seedCurrentNoise, seedLegacyNoise)
				}
				for i := range currentNoise {
					if currentNoise[i] != legacyNoise[i] {
						t.Fatalf("noise mismatch n=%d gain=%v seed=%08x at %d: got %v want %v",
							n, gain, seed0, i, currentNoise[i], legacyNoise[i])
					}
				}

				currentFold := make([]float64, n)
				legacyFold := make([]float64, n)
				lowband := makeLowband(n)
				seedCurrentFold := seed0
				seedLegacyFold := seed0

				if ok := seededZeroPulseResynth(currentFold, lowband, &seedCurrentFold, gain); !ok {
					t.Fatalf("fold fast path unexpectedly refused n=%d gain=%v", n, gain)
				}
				legacyZeroPulseResynth(legacyFold, lowband, &seedLegacyFold, gain)

				if seedCurrentFold != seedLegacyFold {
					t.Fatalf("fold seed mismatch n=%d gain=%v seed=%08x: got %08x want %08x",
						n, gain, seed0, seedCurrentFold, seedLegacyFold)
				}
				for i := range currentFold {
					if currentFold[i] != legacyFold[i] {
						t.Fatalf("fold mismatch n=%d gain=%v seed=%08x at %d: got %v want %v",
							n, gain, seed0, i, currentFold[i], legacyFold[i])
					}
				}
			}
		}
	}
}

func TestSeededZeroPulseResynthFallback(t *testing.T) {
	x := make([]float64, 4)
	seed := uint32(123)
	if seededZeroPulseResynth(x, nil, nil, 1.0) {
		t.Fatal("expected nil-seed call to refuse fast path")
	}
	if seededZeroPulseResynth(x, []float64{1, 2}, &seed, 1.0) {
		t.Fatal("expected short lowband to refuse fast path")
	}
}

func BenchmarkZeroPulseResynthCurrent(b *testing.B) {
	benchmarks := []struct {
		name    string
		lowband []float64
	}{
		{name: "noise", lowband: nil},
		{name: "fold", lowband: makeLowband(32)},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			x := make([]float64, 32)
			seedBase := uint32(0x12345678)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				seed := seedBase + uint32(i)
				if !seededZeroPulseResynth(x, bm.lowband, &seed, 1.0) {
					b.Fatal("fast path refused benchmark input")
				}
			}
		})
	}
}

func BenchmarkZeroPulseResynthLegacy(b *testing.B) {
	benchmarks := []struct {
		name    string
		lowband []float64
	}{
		{name: "noise", lowband: nil},
		{name: "fold", lowband: makeLowband(32)},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			x := make([]float64, 32)
			seedBase := uint32(0x12345678)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				seed := seedBase + uint32(i)
				legacyZeroPulseResynth(x, bm.lowband, &seed, 1.0)
			}
		})
	}
}
