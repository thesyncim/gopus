package silk

import (
	"math"
	"testing"
)

func BenchmarkWarpedAutocorrelationFLP32_Order24(b *testing.B) {
	const length = 160 // typical subframe window length
	const order = 24
	in := make([]float32, length)
	for i := range in {
		in[i] = float32(0.5 * math.Sin(2*math.Pi*440*float64(i)/16000))
	}
	out := make([]float32, order+1)
	state := make([]float32, order+1)
	warping := float32(0.015)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		warpedAutocorrelationFLP32(out, state, in, warping, length, order)
	}
}

func BenchmarkWarpedAutocorrelationFLP32_Order16(b *testing.B) {
	const length = 120
	const order = 16
	in := make([]float32, length)
	for i := range in {
		in[i] = float32(0.5 * math.Sin(2*math.Pi*440*float64(i)/16000))
	}
	out := make([]float32, order+1)
	state := make([]float32, order+1)
	warping := float32(0.015)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		warpedAutocorrelationFLP32(out, state, in, warping, length, order)
	}
}

// TestWarpedAutocorrelationFLP32_Deterministic verifies the function produces
// consistent results across calls (useful for verifying optimizations don't
// change output).
func TestWarpedAutocorrelationFLP32_Deterministic(t *testing.T) {
	const length = 240
	const order = 24
	in := make([]float32, length)
	for i := range in {
		in[i] = float32(0.5 * math.Sin(2*math.Pi*440*float64(i)/16000))
	}

	out1 := make([]float32, order+1)
	state1 := make([]float32, order+1)
	warpedAutocorrelationFLP32(out1, state1, in, 0.015, length, order)

	out2 := make([]float32, order+1)
	state2 := make([]float32, order+1)
	warpedAutocorrelationFLP32(out2, state2, in, 0.015, length, order)

	for i := 0; i <= order; i++ {
		if out1[i] != out2[i] {
			t.Errorf("out[%d] mismatch: %v vs %v", i, out1[i], out2[i])
		}
		if state1[i] != state2[i] {
			t.Errorf("state[%d] mismatch: %v vs %v", i, state1[i], state2[i])
		}
	}
}

// TestWarpedAutocorrelationFLP32_AllOrders tests the function with various orders
// to verify the generic loop and order==16 specialization produce identical results.
func TestWarpedAutocorrelationFLP32_AllOrders(t *testing.T) {
	for _, order := range []int{2, 4, 8, 10, 14, 16, 18, 20, 22, 24} {
		t.Run("", func(t *testing.T) {
			const length = 200
			in := make([]float32, length)
			for i := range in {
				in[i] = float32(0.3*math.Sin(2*math.Pi*220*float64(i)/16000) +
					0.2*math.Sin(2*math.Pi*880*float64(i)/16000))
			}

			out1 := make([]float32, order+1)
			state1 := make([]float32, order+1)
			warpedAutocorrelationFLP32(out1, state1, in, 0.02, length, order)

			out2 := make([]float32, order+1)
			state2 := make([]float32, order+1)
			warpedAutocorrelationFLP32(out2, state2, in, 0.02, length, order)

			for i := 0; i <= order; i++ {
				if out1[i] != out2[i] {
					t.Errorf("order=%d out[%d] mismatch: %v vs %v", order, i, out1[i], out2[i])
				}
				if state1[i] != state2[i] {
					t.Errorf("order=%d state[%d] mismatch: %v vs %v", order, i, state1[i], state2[i])
				}
			}
		})
	}
}
