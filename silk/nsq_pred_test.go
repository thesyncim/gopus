package silk

import (
	"math/rand"
	"testing"
)

// refShortTermPrediction16 is the pure Go reference for verification.
func refShortTermPrediction16(sLPCQ14 []int32, idx int, aQ12 []int16) int32 {
	out := int32(8)
	for k := 0; k < 16; k++ {
		c16 := int16(aQ12[k])
		out += int32((int64(sLPCQ14[idx-k]) * int64(c16)) >> 16)
	}
	return out
}

// refShortTermPrediction10 is the pure Go reference for verification.
func refShortTermPrediction10(sLPCQ14 []int32, idx int, aQ12 []int16) int32 {
	out := int32(5)
	for k := 0; k < 10; k++ {
		c16 := int16(aQ12[k])
		out += int32((int64(sLPCQ14[idx-k]) * int64(c16)) >> 16)
	}
	return out
}

func TestShortTermPrediction16Asm(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	// Test with random realistic values.
	for trial := 0; trial < 1000; trial++ {
		sLPCQ14 := make([]int32, 96)
		aQ12 := make([]int16, 16)
		for i := range sLPCQ14 {
			sLPCQ14[i] = rng.Int31n(1<<24) - (1 << 23) // Q14 range
		}
		for i := range aQ12 {
			aQ12[i] = int16(rng.Int31n(1<<13) - (1 << 12)) // Q12 range
		}
		idx := 15 + rng.Intn(80) // valid range: idx >= 15

		got := shortTermPrediction16(sLPCQ14, idx, aQ12)
		want := refShortTermPrediction16(sLPCQ14, idx, aQ12)
		if got != want {
			t.Fatalf("trial %d: shortTermPrediction16 mismatch: got %d, want %d (idx=%d)", trial, got, want, idx)
		}
	}
}

func TestShortTermPrediction10Asm(t *testing.T) {
	rng := rand.New(rand.NewSource(99))

	for trial := 0; trial < 1000; trial++ {
		sLPCQ14 := make([]int32, 96)
		aQ12 := make([]int16, 10)
		for i := range sLPCQ14 {
			sLPCQ14[i] = rng.Int31n(1<<24) - (1 << 23)
		}
		for i := range aQ12 {
			aQ12[i] = int16(rng.Int31n(1<<13) - (1 << 12))
		}
		idx := 9 + rng.Intn(86)

		got := shortTermPrediction10(sLPCQ14, idx, aQ12)
		want := refShortTermPrediction10(sLPCQ14, idx, aQ12)
		if got != want {
			t.Fatalf("trial %d: shortTermPrediction10 mismatch: got %d, want %d (idx=%d)", trial, got, want, idx)
		}
	}
}

func TestShortTermPrediction16EdgeCases(t *testing.T) {
	// Max positive signal, max positive coefficients.
	sLPCQ14 := make([]int32, 32)
	aQ12 := make([]int16, 16)
	for i := range sLPCQ14 {
		sLPCQ14[i] = 0x7FFFFFFF
	}
	for i := range aQ12 {
		aQ12[i] = 0x7FFF
	}
	idx := 16
	got := shortTermPrediction16(sLPCQ14, idx, aQ12)
	want := refShortTermPrediction16(sLPCQ14, idx, aQ12)
	if got != want {
		t.Fatalf("max positive: got %d, want %d", got, want)
	}

	// Max negative signal, negative coefficients.
	for i := range sLPCQ14 {
		sLPCQ14[i] = -0x7FFFFFFF
	}
	for i := range aQ12 {
		aQ12[i] = -0x7FFF
	}
	got = shortTermPrediction16(sLPCQ14, idx, aQ12)
	want = refShortTermPrediction16(sLPCQ14, idx, aQ12)
	if got != want {
		t.Fatalf("max negative: got %d, want %d", got, want)
	}

	// Zero signal.
	for i := range sLPCQ14 {
		sLPCQ14[i] = 0
	}
	for i := range aQ12 {
		aQ12[i] = 1000
	}
	got = shortTermPrediction16(sLPCQ14, idx, aQ12)
	if got != 8 { // only the bias remains
		t.Fatalf("zero signal: got %d, want 8", got)
	}

	// Zero coefficients.
	for i := range sLPCQ14 {
		sLPCQ14[i] = 100000
	}
	for i := range aQ12 {
		aQ12[i] = 0
	}
	got = shortTermPrediction16(sLPCQ14, idx, aQ12)
	if got != 8 {
		t.Fatalf("zero coefficients: got %d, want 8", got)
	}
}

func BenchmarkShortTermPrediction16(b *testing.B) {
	sLPCQ14 := make([]int32, 96)
	aQ12 := make([]int16, 16)
	rng := rand.New(rand.NewSource(1))
	for i := range sLPCQ14 {
		sLPCQ14[i] = rng.Int31()
	}
	for i := range aQ12 {
		aQ12[i] = int16(rng.Int31())
	}
	idx := 30
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		shortTermPrediction16(sLPCQ14, idx, aQ12)
	}
}

func BenchmarkShortTermPrediction10(b *testing.B) {
	sLPCQ14 := make([]int32, 96)
	aQ12 := make([]int16, 10)
	rng := rand.New(rand.NewSource(1))
	for i := range sLPCQ14 {
		sLPCQ14[i] = rng.Int31()
	}
	for i := range aQ12 {
		aQ12[i] = int16(rng.Int31())
	}
	idx := 30
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		shortTermPrediction10(sLPCQ14, idx, aQ12)
	}
}

func BenchmarkShortTermPredictionDispatch(b *testing.B) {
	sLPCQ14 := make([]int32, 96)
	aQ12 := make([]int16, 16)
	rng := rand.New(rand.NewSource(1))
	for i := range sLPCQ14 {
		sLPCQ14[i] = rng.Int31()
	}
	for i := range aQ12 {
		aQ12[i] = int16(rng.Int31())
	}
	idx := 30
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		shortTermPrediction(sLPCQ14, idx, aQ12, 16)
	}
}
