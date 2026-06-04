package silk

import "testing"

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
	for i := 0; i < nWant; i++ {
		if got[i] != want[i] {
			t.Fatalf("sample[%d]=%.9f want %.9f", i, got[i], want[i])
		}
	}
}
