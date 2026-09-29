package celt

import (
	"math"
	"math/rand"
	"testing"
)

// TestApplyPreemphasisUpsampledMatchesStuffedCore pins the native-rate
// sub-48 kHz pre-emphasis to the zero-stuffed core-frame path bit for bit:
// the filtered planar frame, the per-channel filter state, the silence
// decision and the overlap maximum, including signed-zero samples and state.
func TestApplyPreemphasisUpsampledMatchesStuffedCore(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	negZero := float32(math.Copysign(0, -1))
	for trial := range 4000 {
		channels := 1 + rng.Intn(2)
		upsample := []int{2, 3, 4, 6}[rng.Intn(4)]
		apiN := []int{20, 40, 80, 160}[rng.Intn(4)] * 12 / upsample / 2
		frame := apiN * upsample
		overlap := min(120, frame)
		if rng.Intn(3) == 0 {
			overlap = rng.Intn(frame + 1)
		}
		native := make([]float32, channels*apiN)
		for i := range native {
			switch rng.Intn(6) {
			case 0:
			case 1:
				native[i] = negZero
			case 2:
				native[i] = float32(rng.NormFloat64() * 1e-6)
			default:
				native[i] = float32(rng.NormFloat64() * 0.3)
			}
		}
		stuffed := NewEncoder(channels)
		direct := NewEncoder(channels)
		coded := int32(1 + rng.Intn(channels))
		prev := float32(rng.NormFloat64() * 1e-5)
		for _, e := range []*Encoder{stuffed, direct} {
			e.upsample = int32(upsample)
			e.streamChannels = coded
			e.overlapMax = prev
		}
		for c := range channels {
			m := float32(rng.NormFloat64())
			if rng.Intn(4) == 0 {
				m = negZero
			}
			stuffed.preemphState[c], direct.preemphState[c] = m, m
		}
		wantOut := make([]float32, channels*(frame+overlap))
		gotOut := make([]float32, len(wantOut))
		core := stuffed.upsampleZeroStuff(native, apiN, channels, upsample)
		wantSilence := stuffed.applyPreemphasisWithScalingAndSilenceCore(core, wantOut, frame, overlap)
		gotSilence := direct.applyPreemphasisUpsampled(native, gotOut, frame, overlap)
		if gotSilence != wantSilence || math.Float32bits(direct.overlapMax) != math.Float32bits(stuffed.overlapMax) {
			t.Fatalf("trial %d: silence %t/%t overlap %08x/%08x", trial, gotSilence, wantSilence,
				math.Float32bits(direct.overlapMax), math.Float32bits(stuffed.overlapMax))
		}
		for c := range channels {
			if math.Float32bits(direct.preemphState[c]) != math.Float32bits(stuffed.preemphState[c]) {
				t.Fatalf("trial %d: channel %d state %v want %v", trial, c, direct.preemphState[c], stuffed.preemphState[c])
			}
		}
		for i := range wantOut {
			if math.Float32bits(gotOut[i]) != math.Float32bits(wantOut[i]) {
				t.Fatalf("trial %d: in[%d]=%v want %v", trial, i, gotOut[i], wantOut[i])
			}
		}
	}
}

// TestPreemphUpsampledPartialGroup covers an output length that ends inside a
// zero-stuffed group, against the single-tap loop on the stuffed input.
func TestPreemphUpsampledPartialGroup(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	for trial := range 5000 {
		upsample := 2 + rng.Intn(5)
		step := 1 + rng.Intn(2)
		n := 1 + rng.Intn(40)
		groups := (n + upsample - 1) / upsample
		src := make([]float32, groups*step)
		for i := range src {
			src[i] = float32(rng.NormFloat64())
		}
		stuffed := make([]float32, n)
		for j := 0; j < n; j += upsample {
			stuffed[j] = src[(j/upsample)*step]
		}
		m0 := float32(rng.NormFloat64())
		want := make([]float32, n)
		wantM := preemphMonoScalar(stuffed, want, PreemphCoef, m0)
		got := make([]float32, n)
		gotM := preemphUpsampled(src, step, upsample, got, PreemphCoef, m0)
		if math.Float32bits(gotM) != math.Float32bits(wantM) {
			t.Fatalf("trial %d: m %v want %v", trial, gotM, wantM)
		}
		for i := range want {
			if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
				t.Fatalf("trial %d (upsample %d n %d): out[%d]=%v want %v", trial, upsample, n, i, got[i], want[i])
			}
		}
	}
}
