package silk

import (
	"math/rand"
	"testing"
)

// refWarpedARFeedback computes warped AR feedback in pure Go (reference).
func refWarpedARFeedback(sAR []int32, diffQ14 int32, arShpQ13 []int16, warpQ16 int32, order int) int32 {
	w := int64(warpQ16)
	tmp2 := diffQ14 + int32((int64(sAR[0])*w)>>16)
	tmp1 := sAR[0] + int32((int64(sAR[1]-tmp2)*w)>>16)
	sAR[0] = tmp2
	acc := int32(order>>1) + int32((int64(tmp2)*int64(arShpQ13[0]))>>16)

	for j := 2; j < order; j += 2 {
		tmp2 = sAR[j-1] + int32((int64(sAR[j]-tmp1)*w)>>16)
		sAR[j-1] = tmp1
		acc += int32((int64(tmp1) * int64(arShpQ13[j-1])) >> 16)
		tmp1 = sAR[j] + int32((int64(sAR[j+1]-tmp2)*w)>>16)
		sAR[j] = tmp2
		acc += int32((int64(tmp2) * int64(arShpQ13[j])) >> 16)
	}
	sAR[order-1] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[order-1])) >> 16)
	return acc
}

func TestWarpedARFeedback24(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 1000; trial++ {
		var sAR [maxShapeLpcOrder]int32
		var sARRef [maxShapeLpcOrder]int32
		arShpQ13 := make([]int16, 24)
		for i := 0; i < 24; i++ {
			v := rng.Int31n(1<<20) - (1 << 19)
			sAR[i] = v
			sARRef[i] = v
		}
		for i := range arShpQ13 {
			arShpQ13[i] = int16(rng.Int31n(1<<13) - (1 << 12))
		}
		diffQ14 := rng.Int31n(1<<20) - (1 << 19)
		warpQ16 := rng.Int31n(1<<15) - (1 << 14) // typical range

		got := warpedARFeedback24(&sAR, diffQ14, arShpQ13, warpQ16)
		want := refWarpedARFeedback(sARRef[:], diffQ14, arShpQ13, warpQ16, 24)

		if got != want {
			t.Fatalf("trial %d: warpedARFeedback24 mismatch: got %d, want %d", trial, got, want)
		}
		for i := 0; i < 24; i++ {
			if sAR[i] != sARRef[i] {
				t.Fatalf("trial %d: sAR[%d] mismatch: got %d, want %d", trial, i, sAR[i], sARRef[i])
			}
		}
	}
}

func TestWarpedARFeedback16(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for trial := 0; trial < 1000; trial++ {
		var sAR [maxShapeLpcOrder]int32
		var sARRef [maxShapeLpcOrder]int32
		arShpQ13 := make([]int16, 16)
		for i := 0; i < 24; i++ {
			v := rng.Int31n(1<<20) - (1 << 19)
			sAR[i] = v
			sARRef[i] = v
		}
		for i := range arShpQ13 {
			arShpQ13[i] = int16(rng.Int31n(1<<13) - (1 << 12))
		}
		diffQ14 := rng.Int31n(1<<20) - (1 << 19)
		warpQ16 := rng.Int31n(1<<15) - (1 << 14)

		got := warpedARFeedback16(&sAR, diffQ14, arShpQ13, warpQ16)
		want := refWarpedARFeedback(sARRef[:], diffQ14, arShpQ13, warpQ16, 16)

		if got != want {
			t.Fatalf("trial %d: warpedARFeedback16 mismatch: got %d, want %d", trial, got, want)
		}
		for i := 0; i < 16; i++ {
			if sAR[i] != sARRef[i] {
				t.Fatalf("trial %d: sAR[%d] mismatch: got %d, want %d", trial, i, sAR[i], sARRef[i])
			}
		}
	}
}

func TestWarpedARFeedback24EdgeCases(t *testing.T) {
	// Zero warping
	var sAR [maxShapeLpcOrder]int32
	var sARRef [maxShapeLpcOrder]int32
	arShpQ13 := make([]int16, 24)
	for i := 0; i < 24; i++ {
		sAR[i] = int32(i * 1000)
		sARRef[i] = sAR[i]
		arShpQ13[i] = int16(100 + i)
	}
	got := warpedARFeedback24(&sAR, 500, arShpQ13, 0)
	want := refWarpedARFeedback(sARRef[:], 500, arShpQ13, 0, 24)
	if got != want {
		t.Fatalf("zero warp: got %d, want %d", got, want)
	}

	// Max values
	for i := 0; i < 24; i++ {
		sAR[i] = 0x7FFFF
		sARRef[i] = sAR[i]
		arShpQ13[i] = 0x7FFF
	}
	got = warpedARFeedback24(&sAR, 0x7FFFF, arShpQ13, 0x7FFF)
	want = refWarpedARFeedback(sARRef[:], 0x7FFFF, arShpQ13, 0x7FFF, 24)
	if got != want {
		t.Fatalf("max values: got %d, want %d", got, want)
	}

	// Negative values
	for i := 0; i < 24; i++ {
		sAR[i] = -0x7FFFF
		sARRef[i] = sAR[i]
		arShpQ13[i] = -0x7FFF
	}
	got = warpedARFeedback24(&sAR, -0x7FFFF, arShpQ13, -0x7FFF)
	want = refWarpedARFeedback(sARRef[:], -0x7FFFF, arShpQ13, -0x7FFF, 24)
	if got != want {
		t.Fatalf("negative values: got %d, want %d", got, want)
	}
}

func BenchmarkWarpedARFeedback24(b *testing.B) {
	rng := rand.New(rand.NewSource(1))
	var sAR [maxShapeLpcOrder]int32
	arShpQ13 := make([]int16, 24)
	for i := range sAR {
		sAR[i] = rng.Int31()
	}
	for i := range arShpQ13 {
		arShpQ13[i] = int16(rng.Int31())
	}
	diffQ14 := rng.Int31()
	warpQ16 := int32(rng.Int31n(1 << 15))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		warpedARFeedback24(&sAR, diffQ14, arShpQ13, warpQ16)
	}
}

func BenchmarkWarpedARFeedback16(b *testing.B) {
	rng := rand.New(rand.NewSource(1))
	var sAR [maxShapeLpcOrder]int32
	arShpQ13 := make([]int16, 16)
	for i := range sAR {
		sAR[i] = rng.Int31()
	}
	for i := range arShpQ13 {
		arShpQ13[i] = int16(rng.Int31())
	}
	diffQ14 := rng.Int31()
	warpQ16 := int32(rng.Int31n(1 << 15))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		warpedARFeedback16(&sAR, diffQ14, arShpQ13, warpQ16)
	}
}
