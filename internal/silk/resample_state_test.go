package silk

import (
	"math"
	"math/rand"
	"testing"
)

func TestDownsamplingResamplerZeroStateMatchesFreshResampler(t *testing.T) {
	dirty := NewDownsamplingResampler(48000, 16000)
	fresh := NewDownsamplingResampler(48000, 16000)

	warm := make([]float32, 480)
	for i := range warm {
		warm[i] = float32((i%31)-15) / 31.0
	}
	warmOut := make([]float32, 160)
	dirty.ProcessInto(warm, warmOut)

	dirty.SetState(DownsamplingResamplerState{})

	in := make([]float32, 480)
	for i := 168; i < len(in); i++ {
		in[i] = float32(((i*17)%29)-14) / 29.0
	}

	got := make([]float32, 160)
	want := make([]float32, 160)
	nGot := dirty.ProcessInto(in, got)
	nWant := fresh.ProcessInto(in, want)
	if nGot != nWant {
		t.Fatalf("output len=%d want=%d", nGot, nWant)
	}
	for i := range nWant {
		if got[i] != want[i] {
			t.Fatalf("sample[%d]=%.9f want %.9f", i, got[i], want[i])
		}
	}
}

// TestResampleStereoInt16MatchesProcessInt16Into checks that resampling both
// channels with ResampleStereoInt16 and interleaving them with
// InterleaveInt16AsFloat32 matches ProcessInt16Into per channel followed by an
// interleave, sample for sample and across calls, and allocates nothing.
func TestResampleStereoInt16MatchesProcessInt16Into(t *testing.T) {
	rng := rand.New(rand.NewSource(0x5e7e))
	for _, rates := range [][2]int{{8000, 8000}, {16000, 16000}, {8000, 12000}, {12000, 24000}, {16000, 24000}, {16000, 48000}, {8000, 48000}, {12000, 48000}} {
		fsIn, fsOut := rates[0], rates[1]
		gotL, gotR := NewLibopusResampler(fsIn, fsOut), NewLibopusResampler(fsIn, fsOut)
		wantL, wantR := NewLibopusResampler(fsIn, fsOut), NewLibopusResampler(fsIn, fsOut)
		for iter := range 40 {
			n := fsIn / 1000 * (5 + 5*rng.Intn(4))
			if iter%7 == 0 {
				n = 1 + rng.Intn(fsIn/1000)
			}
			left := make([]int16, n)
			right := make([]int16, n)
			for i := range left {
				left[i] = int16(rng.Uint32())
				right[i] = int16(rng.Uint32())
			}
			outL, outR, ok := ResampleStereoInt16(gotL, gotR, left, right)
			if !ok {
				t.Fatalf("%d->%d: ResampleStereoInt16 declined an upsampling configuration", fsIn, fsOut)
			}
			m := min(len(outL), len(outR))
			got := make([]float32, 2*m)
			InterleaveInt16AsFloat32(got, outL[:m], outR[:m])

			bufL := make([]float32, 2*m+8)
			bufR := make([]float32, 2*m+8)
			nL := wantL.ProcessInt16Into(left, bufL)
			nR := wantR.ProcessInt16Into(right, bufR)
			if nL != m || nR != m {
				t.Fatalf("%d->%d iter %d: ProcessInt16Into wrote %d/%d, want %d", fsIn, fsOut, iter, nL, nR, m)
			}
			for i := range m {
				if math.Float32bits(got[2*i]) != math.Float32bits(bufL[i]) || math.Float32bits(got[2*i+1]) != math.Float32bits(bufR[i]) {
					t.Fatalf("%d->%d iter %d: sample %d = (%g, %g), want (%g, %g)", fsIn, fsOut, iter, i, got[2*i], got[2*i+1], bufL[i], bufR[i])
				}
			}
		}
		left := make([]int16, fsIn/100)
		right := make([]int16, fsIn/100)
		dst := make([]float32, 2*fsOut/100)
		if allocs := testing.AllocsPerRun(100, func() {
			outL, outR, _ := ResampleStereoInt16(gotL, gotR, left, right)
			InterleaveInt16AsFloat32(dst, outL, outR)
		}); allocs != 0 {
			t.Fatalf("%d->%d: %v allocs per run, want 0", fsIn, fsOut, allocs)
		}
	}
	if _, _, ok := ResampleStereoInt16(NewLibopusResampler(48000, 16000), NewLibopusResampler(48000, 16000), make([]int16, 480), make([]int16, 480)); ok {
		t.Fatal("ResampleStereoInt16 accepted a down_FIR configuration")
	}
}
