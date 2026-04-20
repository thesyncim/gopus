package testvectors

import "testing"

func TestOpusCompareDelayCandidatesIncludeSymmetricNeighborhood(t *testing.T) {
	ref := makeAperiodicSignal(4096)
	decoded := shiftSignal(ref, 8)

	got := opusCompareDelayCandidates(decoded, ref, 1, 120)
	want := map[int]struct{}{
		0:   {},
		8:   {},
		-8:  {},
		16:  {},
		-16: {},
	}

	seen := make(map[int]struct{}, len(got))
	for _, delay := range got {
		seen[delay] = struct{}{}
	}
	for delay := range want {
		if _, ok := seen[delay]; !ok {
			t.Fatalf("missing candidate delay %d in %v", delay, got)
		}
	}
}

func TestQualityDelaySearchWindowKeepsShortFramesWideEnough(t *testing.T) {
	if got := qualityDelaySearchWindow(120); got != 240 {
		t.Fatalf("2.5 ms window mismatch: got %d want 240", got)
	}
	if got := qualityDelaySearchWindow(240); got != 240 {
		t.Fatalf("5 ms window mismatch: got %d want 240", got)
	}
	if got := qualityDelaySearchWindow(480); got != 480 {
		t.Fatalf("10 ms window mismatch: got %d want 480", got)
	}
}

func TestEstimateDelayByWaveformCorrelationFindsNegativeDelay(t *testing.T) {
	ref := makeAperiodicSignal(4096)
	const wantDelay = -137
	decoded := shiftSignal(ref, wantDelay)

	gotDelay := estimateDelayByWaveformCorrelation(decoded, ref, 1, 300)
	if gotDelay != wantDelay {
		t.Fatalf("delay mismatch: got %d want %d", gotDelay, wantDelay)
	}
}

func TestEstimateDelayByWaveformCorrelationFindsPositiveDelay(t *testing.T) {
	ref := makeAperiodicSignal(4096)
	const wantDelay = 173
	decoded := shiftSignal(ref, wantDelay)

	gotDelay := estimateDelayByWaveformCorrelation(decoded, ref, 1, 300)
	if gotDelay != wantDelay {
		t.Fatalf("delay mismatch: got %d want %d", gotDelay, wantDelay)
	}
}

func TestEstimateDelayByWaveformCorrelationFindsLargeDelayOnLongSignal(t *testing.T) {
	ref := makeAperiodicSignal(48000)
	const wantDelay = -381
	decoded := shiftSignal(ref, wantDelay)

	gotDelay := estimateDelayByWaveformCorrelation(decoded, ref, 1, 960)
	if gotDelay != wantDelay {
		t.Fatalf("delay mismatch: got %d want %d", gotDelay, wantDelay)
	}
}

func TestEstimateDelayByWaveformCorrelationFindsStereoInterleavedDelay(t *testing.T) {
	left := makeAperiodicSignal(8192)
	right := makeAperiodicSignal(8192)
	for i := range right {
		right[i] = -right[i]
	}
	ref := interleaveStereo(left, right)
	const wantDelay = 174
	decoded := shiftSignal(ref, wantDelay)

	gotDelay := estimateDelayByWaveformCorrelation(decoded, ref, 2, 300)
	if gotDelay != wantDelay {
		t.Fatalf("delay mismatch: got %d want %d", gotDelay, wantDelay)
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

func interleaveStereo(left, right []float32) []float32 {
	n := len(left)
	if len(right) < n {
		n = len(right)
	}
	out := make([]float32, n*2)
	for i := 0; i < n; i++ {
		out[i*2] = left[i]
		out[i*2+1] = right[i]
	}
	return out
}
