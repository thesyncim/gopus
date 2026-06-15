package qualitycompare

import (
	"math"
	"testing"
)

// TestOpusCompareHelperCacheMatchesSingleCandidate guards the band_energy cache
// in the compare helper: the best Q returned for a multi-delay request must be
// bit-identical to the Q returned for a request carrying only that winning
// delay. A prefix-slicing or start-offset bug in the cache would shift the
// energies and perturb Q, so this catches such regressions exactly.
func TestOpusCompareHelperCacheMatchesSingleCandidate(t *testing.T) {
	for _, channels := range []int{1, 2} {
		ref := makeAperiodicSignal(48000)
		// A perceptibly different decoded side so Q is finite and delay-sensitive.
		decShift := shiftSignal(ref, 3)
		var refPCM, decPCM []int16
		if channels == 2 {
			refPCM = float32ToPCM16(interleaveStereo(ref, ref))
			decPCM = float32ToPCM16(interleaveStereo(decShift, ref))
		} else {
			refPCM = float32ToPCM16(ref)
			decPCM = float32ToPCM16(decShift)
		}

		delays := opusCompareDelayCandidates(pcm16ToFloat32(decPCM), pcm16ToFloat32(refPCM), channels, 240)
		q, best, err := runOpusCompareHelper(refPCM, decPCM, 48000, channels, delays)
		if err != nil {
			t.Skipf("compare helper unavailable: %v", err)
		}
		qSingle, bestSingle, err := runOpusCompareHelper(refPCM, decPCM, 48000, channels, []int{best})
		if err != nil {
			t.Fatalf("single-candidate helper: %v", err)
		}
		if bestSingle != best {
			t.Fatalf("ch=%d single-candidate delay %d != multi best %d", channels, bestSingle, best)
		}
		if math.Float64bits(q) != math.Float64bits(qSingle) {
			t.Fatalf("ch=%d cache Q mismatch: multi=%v single=%v (bits %x vs %x)",
				channels, q, qSingle, math.Float64bits(q), math.Float64bits(qSingle))
		}
	}
}

func pcm16ToFloat32(in []int16) []float32 {
	out := make([]float32, len(in))
	for i, v := range in {
		out[i] = float32(v) / 32768.0
	}
	return out
}

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
	for i := range n {
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
	n := min(len(right), len(left))
	out := make([]float32, n*2)
	for i := range n {
		out[i*2] = left[i]
		out[i*2+1] = right[i]
	}
	return out
}
