package testvectors

import "testing"

func TestComputeQualityFloat32WithDelayFindsNegativeDelay(t *testing.T) {
	ref := makeAperiodicSignal(4096)
	const wantDelay = -137
	decoded := shiftSignal(ref, wantDelay)

	q, gotDelay := ComputeQualityFloat32WithDelay(decoded, ref, 48000, 300)
	if gotDelay != wantDelay {
		t.Fatalf("delay mismatch: got %d want %d (q=%.2f)", gotDelay, wantDelay, q)
	}
	if q < 90 {
		t.Fatalf("unexpected low quality at best delay: q=%.2f", q)
	}
}

func TestComputeQualityFloat32WithDelayFindsPositiveDelay(t *testing.T) {
	ref := makeAperiodicSignal(4096)
	const wantDelay = 173
	decoded := shiftSignal(ref, wantDelay)

	q, gotDelay := ComputeQualityFloat32WithDelay(decoded, ref, 48000, 300)
	if gotDelay != wantDelay {
		t.Fatalf("delay mismatch: got %d want %d (q=%.2f)", gotDelay, wantDelay, q)
	}
	if q < 90 {
		t.Fatalf("unexpected low quality at best delay: q=%.2f", q)
	}
}

func makeAperiodicSignal(n int) []float32 {
	out := make([]float32, n)
	var x uint32 = 0x1234567
	for i := 0; i < n; i++ {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		v := float32(int32(x&0xFFFF)-32768) / 32768.0
		// Shape edges so boundary effects do not dominate quality.
		if i < 200 {
			v *= float32(i) / 200.0
		}
		if i > n-201 {
			v *= float32(n-1-i) / 200.0
		}
		out[i] = v
	}
	return out
}

// shiftSignal creates a shifted copy that satisfies:
// reference[i] aligns with decoded[i+delay].
func shiftSignal(reference []float32, delay int) []float32 {
	decoded := make([]float32, len(reference))
	for j := range decoded {
		src := j - delay
		if src >= 0 && src < len(reference) {
			decoded[j] = reference[src]
		}
	}
	return decoded
}
