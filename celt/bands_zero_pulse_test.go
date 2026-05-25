package celt

import "testing"

func referenceZeroPulseResynth(x, lowband []celtNorm, seed *uint32, gain opusVal16) {
	if lowband == nil {
		if seed != nil {
			for i := range x {
				*seed = (*seed)*1664525 + 1013904223
				x[i] = celtNorm(float32(int32(*seed) >> 20))
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
				x[i] = celtNorm(float32(lowband[i]) + float32(tmp))
			} else {
				x[i] = celtNorm(tmp)
			}
		}
	}
	renormalizeVector(x, gain)
}

func makeLowband(n int) []celtNorm {
	lowband := make([]celtNorm, n)
	for i := range lowband {
		v := celtNorm(float32((i%7)-3) * 0.125)
		if i%3 == 1 {
			v = -v * 0.75
		}
		lowband[i] = v
	}
	return lowband
}

func makeLowbandNorm(n int) []celtNorm {
	return makeLowband(n)
}

func TestSeededZeroPulseResynthMatchesReference(t *testing.T) {
	gains := []opusVal16{0.25, 0.75, 1.0, 1.5}
	seeds := []uint32{1, 0x8000, 0x12345678, 0xdeadbeef}
	lengths := []int{1, 2, 3, 4, 7, 8, 15, 16, 17}

	for _, n := range lengths {
		for _, gain := range gains {
			for _, seed0 := range seeds {
				currentNoise := make([]celtNorm, n)
				referenceNoise := make([]celtNorm, n)
				seedCurrentNoise := seed0
				seedReferenceNoise := seed0

				if ok := seededZeroPulseResynth(currentNoise, nil, &seedCurrentNoise, gain); !ok {
					t.Fatalf("noise fast path unexpectedly refused n=%d gain=%v", n, gain)
				}
				referenceZeroPulseResynth(referenceNoise, nil, &seedReferenceNoise, gain)

				if seedCurrentNoise != seedReferenceNoise {
					t.Fatalf("noise seed mismatch n=%d gain=%v seed=%08x: got %08x want %08x",
						n, gain, seed0, seedCurrentNoise, seedReferenceNoise)
				}
				for i := range currentNoise {
					if currentNoise[i] != referenceNoise[i] {
						t.Fatalf("noise mismatch n=%d gain=%v seed=%08x at %d: got %v want %v",
							n, gain, seed0, i, currentNoise[i], referenceNoise[i])
					}
				}

				currentFold := make([]celtNorm, n)
				referenceFold := make([]celtNorm, n)
				lowbandReference := makeLowband(n)
				lowband := append([]celtNorm(nil), lowbandReference...)
				seedCurrentFold := seed0
				seedReferenceFold := seed0

				if ok := seededZeroPulseResynth(currentFold, lowband, &seedCurrentFold, gain); !ok {
					t.Fatalf("fold fast path unexpectedly refused n=%d gain=%v", n, gain)
				}
				referenceZeroPulseResynth(referenceFold, lowbandReference, &seedReferenceFold, gain)

				if seedCurrentFold != seedReferenceFold {
					t.Fatalf("fold seed mismatch n=%d gain=%v seed=%08x: got %08x want %08x",
						n, gain, seed0, seedCurrentFold, seedReferenceFold)
				}
				for i := range currentFold {
					if currentFold[i] != referenceFold[i] {
						t.Fatalf("fold mismatch n=%d gain=%v seed=%08x at %d: got %v want %v",
							n, gain, seed0, i, currentFold[i], referenceFold[i])
					}
				}
			}
		}
	}
}

func TestSeededZeroPulseResynthFallback(t *testing.T) {
	x := make([]celtNorm, 4)
	seed := uint32(123)
	if seededZeroPulseResynth(x, nil, nil, 1.0) {
		t.Fatal("expected nil-seed call to refuse fast path")
	}
	if seededZeroPulseResynth(x, []celtNorm{1, 2}, &seed, 1.0) {
		t.Fatal("expected short lowband to refuse fast path")
	}
}

func BenchmarkZeroPulseResynthCurrent(b *testing.B) {
	benchmarks := []struct {
		name    string
		lowband []celtNorm
	}{
		{name: "noise", lowband: nil},
		{name: "fold", lowband: makeLowbandNorm(32)},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			x := make([]celtNorm, 32)
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

func BenchmarkZeroPulseResynthReference(b *testing.B) {
	benchmarks := []struct {
		name    string
		lowband []celtNorm
	}{
		{name: "noise", lowband: nil},
		{name: "fold", lowband: makeLowband(32)},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			x := make([]celtNorm, 32)
			seedBase := uint32(0x12345678)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				seed := seedBase + uint32(i)
				referenceZeroPulseResynth(x, bm.lowband, &seed, 1.0)
			}
		})
	}
}
