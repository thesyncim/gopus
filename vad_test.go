package gopus

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/encoder"
)

func TestVADMatchesSILK(t *testing.T) {
	for _, sampleRate := range []int{8000, 12000, 16000} {
		for _, frameMS := range []int{10, 20} {
			vad, err := NewVAD(sampleRate)
			if err != nil {
				t.Fatal(err)
			}
			oracle := encoder.NewVADState()
			frameSize := sampleRate * frameMS / 1000
			pcm := make([]int16, frameSize)
			floats := make([]float32, frameSize)
			for frame := range 40 {
				for i := range pcm {
					value := int16(0)
					if frame >= 20 && frame < 30 {
						value = int16(10000 * math.Sin(2*math.Pi*220*float64(i)/float64(sampleRate)))
					}
					pcm[i] = value
					floats[i] = float32(value) * (1.0 / 32768.0)
				}
				got, err := vad.AnalyzeInt16(pcm)
				if err != nil {
					t.Fatal(err)
				}
				want, _ := oracle.GetSpeechActivity(floats, frameSize, sampleRate/1000)
				if got != want {
					t.Fatalf("rate=%d frameMS=%d frame=%d: activity=%d want %d", sampleRate, frameMS, frame, got, want)
				}
			}
			vad.Reset()
			oracle.Reset()
			got, _ := vad.AnalyzeInt16(pcm)
			want, _ := oracle.GetSpeechActivity(floats, frameSize, sampleRate/1000)
			if got != want {
				t.Fatalf("reset rate=%d frameMS=%d: activity=%d want %d", sampleRate, frameMS, got, want)
			}
		}
	}
}

func TestVADValidatesInput(t *testing.T) {
	for _, rate := range []int{0, 44100, 24000, 48000} {
		if _, err := NewVAD(rate); err == nil {
			t.Fatalf("NewVAD(%d) accepted an unsupported rate", rate)
		}
	}
	if _, err := (*VAD)(nil).AnalyzeInt16(nil); err != ErrInvalidFrameSize {
		t.Fatalf("nil VAD: %v", err)
	}
	for _, size := range []int{0, 159, 161, 319, 321} {
		vad, _ := NewVAD(16000)
		if _, err := vad.AnalyzeInt16(make([]int16, size)); err != ErrInvalidFrameSize {
			t.Fatalf("size=%d: %v", size, err)
		}
	}
}

func TestVADSteadyStateNoAllocations(t *testing.T) {
	vad, _ := NewVAD(16000)
	pcm := make([]int16, 320)
	_, _ = vad.AnalyzeInt16(pcm)
	if n := testing.AllocsPerRun(100, func() { _, _ = vad.AnalyzeInt16(pcm) }); n != 0 {
		t.Fatalf("AnalyzeInt16 allocations = %v", n)
	}
}
