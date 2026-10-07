package gopus

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/testsignal"
)

// speechTestPCM returns n frames of a corpus signal at sampleRate as 16-bit PCM
// and as the float samples the detector derives from it.
func speechTestPCM(t testing.TB, class string, sampleRate, frameSize, frames int) ([]int16, []float32) {
	t.Helper()
	sig, err := testsignal.GenerateCorpusSignal(class, sampleRate, frames*frameSize, 1)
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]int16, len(sig))
	floats := make([]float32, len(sig))
	for i, v := range sig {
		pcm[i] = int16(math.Max(-32768, math.Min(32767, math.Round(float64(v)*32768))))
		floats[i] = float32(pcm[i]) * (1.0 / 32768.0)
	}
	return pcm, floats
}

func TestSpeechDetectorMatchesEncoderAnalysis(t *testing.T) {
	classes := []string{testsignal.CorpusCleanSpeechV1, testsignal.CorpusMixedV1, testsignal.CorpusMusicV1, testsignal.CorpusWhiteNoiseV1}
	for _, sampleRate := range []int{16000, 24000, 48000} {
		for _, frameMS := range []int{10, 20} {
			for _, class := range classes {
				t.Run(fmt.Sprintf("%dk/%dms/%s", sampleRate/1000, frameMS, class), func(t *testing.T) {
					frameSize := sampleRate * frameMS / 1000
					const frames = 100
					pcm, floats := speechTestPCM(t, class, sampleRate, frameSize, frames)
					fromInt16, err := NewSpeechDetector(sampleRate)
					if err != nil {
						t.Fatal(err)
					}
					fromFloat32, _ := NewSpeechDetector(sampleRate)
					oracle := encoder.NewTonalityAnalysisState(sampleRate)
					for f := range frames {
						span := f*frameSize + frameSize
						got16, err := fromInt16.AnalyzeInt16(pcm[f*frameSize : span])
						if err != nil {
							t.Fatal(err)
						}
						got32, err := fromFloat32.AnalyzeFloat32(floats[f*frameSize : span])
						if err != nil {
							t.Fatal(err)
						}
						info := oracle.RunAnalysis(floats[f*frameSize:span], frameSize, 1)
						want := float32(0)
						if info.Valid {
							want = info.VADProb
						}
						if math.Float32bits(got16) != math.Float32bits(want) || math.Float32bits(got32) != math.Float32bits(want) {
							t.Fatalf("frame %d: int16 %v float32 %v, encoder analysis %v", f, got16, got32, want)
						}
					}
				})
			}
		}
	}
}

func TestSpeechDetectorValidatesInput(t *testing.T) {
	for _, rate := range []int{0, 8000, 12000, 44100, 96000} {
		if _, err := NewSpeechDetector(rate); err == nil {
			t.Fatalf("NewSpeechDetector(%d) accepted an unsupported rate", rate)
		}
	}
	if _, err := (*SpeechDetector)(nil).AnalyzeInt16(nil); err != ErrInvalidFrameSize {
		t.Fatalf("nil detector: %v", err)
	}
	if _, err := (*SpeechDetector)(nil).AnalyzeFloat32(nil); err != ErrInvalidFrameSize {
		t.Fatalf("nil detector float32: %v", err)
	}
	if _, err := new(SpeechDetector).AnalyzeInt16(nil); err != ErrInvalidFrameSize {
		t.Fatalf("zero-value detector: %v", err)
	}
	new(SpeechDetector).Reset()
	(*SpeechDetector)(nil).Reset()
	for _, rate := range []int{16000, 24000, 48000} {
		d, err := NewSpeechDetector(rate)
		if err != nil {
			t.Fatal(err)
		}
		for _, ms := range []int{10, 20} {
			size := rate * ms / 1000
			if _, err := d.AnalyzeInt16(make([]int16, size)); err != nil {
				t.Fatalf("rate=%d %d ms int16: %v", rate, ms, err)
			}
			if _, err := d.AnalyzeFloat32(make([]float32, size)); err != nil {
				t.Fatalf("rate=%d %d ms float32: %v", rate, ms, err)
			}
		}
		for _, size := range []int{0, 1, rate/100 - 1, rate/100 + 1, rate/50 - 1, rate/50 + 1, rate * 3 / 100, rate / 25} {
			if _, err := d.AnalyzeInt16(make([]int16, size)); err != ErrInvalidFrameSize {
				t.Fatalf("rate=%d int16 size=%d: %v", rate, size, err)
			}
			if _, err := d.AnalyzeFloat32(make([]float32, size)); err != ErrInvalidFrameSize {
				t.Fatalf("rate=%d float32 size=%d: %v", rate, size, err)
			}
		}
	}
}

func TestSpeechDetectorReset(t *testing.T) {
	const sampleRate, frameSize, frames = 16000, 320, 60
	pcm, _ := speechTestPCM(t, testsignal.CorpusCleanSpeechV1, sampleRate, frameSize, frames)
	run := func(d *SpeechDetector) []float32 {
		out := make([]float32, frames)
		for f := range frames {
			p, err := d.AnalyzeInt16(pcm[f*frameSize : (f+1)*frameSize])
			if err != nil {
				t.Fatal(err)
			}
			out[f] = p
		}
		return out
	}
	d, _ := NewSpeechDetector(sampleRate)
	fresh := run(d)
	continued := run(d)
	differs := false
	for f := range fresh {
		differs = differs || fresh[f] != continued[f]
	}
	if !differs {
		t.Fatal("a continued stream scores like a fresh one, so the comparison below proves nothing")
	}
	d.Reset()
	again := run(d)
	for f := range fresh {
		if math.Float32bits(fresh[f]) != math.Float32bits(again[f]) {
			t.Fatalf("frame %d after Reset: %v, fresh detector %v", f, again[f], fresh[f])
		}
	}
}

func TestSpeechDetectorSilenceAndFrameCadence(t *testing.T) {
	const sampleRate = 16000
	pcm, _ := speechTestPCM(t, testsignal.CorpusCleanSpeechV1, sampleRate, 160, 60)

	// A 10 ms frame completes an analysis window on every second call and repeats
	// the previous result in between; nothing is known before the first window.
	d, _ := NewSpeechDetector(sampleRate)
	var results []float32
	for f := range 60 {
		p, err := d.AnalyzeInt16(pcm[f*160 : (f+1)*160])
		if err != nil {
			t.Fatal(err)
		}
		results = append(results, p)
	}
	if results[0] != 0 {
		t.Fatalf("first 10 ms call = %v, want 0 before the first window", results[0])
	}
	for f := 1; f+1 < len(results); f += 2 {
		if results[f] != results[f+1] {
			t.Fatalf("10 ms calls %d and %d differ: %v %v", f, f+1, results[f], results[f+1])
		}
	}

	// Exactly zero input reports 0 rather than repeating the last speech estimate.
	d.Reset()
	var peak float32
	for f := range 50 {
		p, _ := d.AnalyzeInt16(pcm[f*160 : (f+1)*160])
		if f >= 30 {
			peak = max(peak, p)
		}
	}
	if peak <= 0.5 {
		t.Fatalf("peak probability %v over the last speech calls, want a speech-like value", peak)
	}
	for f := range 6 {
		p, _ := d.AnalyzeInt16(make([]int16, 160))
		if f >= 4 && p != 0 {
			t.Fatalf("silent call %d = %v, want 0", f, p)
		}
	}
	// Speech after silence is scored again.
	var after float32
	for f := range 20 {
		after, _ = d.AnalyzeInt16(pcm[f*160 : (f+1)*160])
	}
	if after <= 0 {
		t.Fatalf("probability %v after silence, want speech to be scored", after)
	}
}

// TestSpeechDetectorSeparatesSpeechFromNoise checks the sign of the output on
// the synthetic corpus: voiced speech scores high and steady noise, a hum-free
// noise floor and near silence score near zero once the warm-up has passed.
func TestSpeechDetectorSeparatesSpeechFromNoise(t *testing.T) {
	cases := []struct {
		class    string
		min, max float64
	}{
		{testsignal.CorpusCleanSpeechV1, 0.7, 1},
		{testsignal.CorpusWhiteNoiseV1, 0, 0.05},
		{testsignal.CorpusPinkNoiseV1, 0, 0.05},
		{testsignal.CorpusNearSilenceV1, 0, 0.05},
	}
	for _, sampleRate := range []int{16000, 48000} {
		frameSize := sampleRate / 50
		for _, tc := range cases {
			pcm, _ := speechTestPCM(t, tc.class, sampleRate, frameSize, 150)
			d, _ := NewSpeechDetector(sampleRate)
			var sum float64
			count := 0
			for f := range 150 {
				p, err := d.AnalyzeInt16(pcm[f*frameSize : (f+1)*frameSize])
				if err != nil {
					t.Fatal(err)
				}
				if p < 0 || p > 1 {
					t.Fatalf("%s %d Hz: probability %v outside [0, 1]", tc.class, sampleRate, p)
				}
				if f >= 25 { // skip the 10-window warm-up and its settling
					sum += float64(p)
					count++
				}
			}
			if mean := sum / float64(count); mean < tc.min || mean > tc.max {
				t.Fatalf("%s %d Hz: mean probability %.3f outside [%.2f, %.2f]", tc.class, sampleRate, mean, tc.min, tc.max)
			}
		}
	}
}

func TestSpeechDetectorSteadyStateNoAllocations(t *testing.T) {
	for _, sampleRate := range []int{16000, 24000, 48000} {
		for _, ms := range []int{10, 20} {
			frameSize := sampleRate * ms / 1000
			pcm, floats := speechTestPCM(t, testsignal.CorpusMixedV1, sampleRate, frameSize, 1)
			d, _ := NewSpeechDetector(sampleRate)
			if n := testing.AllocsPerRun(100, func() { _, _ = d.AnalyzeInt16(pcm) }); n != 0 {
				t.Fatalf("%d Hz %d ms: AnalyzeInt16 allocations = %v", sampleRate, ms, n)
			}
			if n := testing.AllocsPerRun(100, func() { _, _ = d.AnalyzeFloat32(floats) }); n != 0 {
				t.Fatalf("%d Hz %d ms: AnalyzeFloat32 allocations = %v", sampleRate, ms, n)
			}
			if n := testing.AllocsPerRun(20, func() {
				d.Reset()
				_, _ = d.AnalyzeFloat32(floats)
			}); n != 0 {
				t.Fatalf("%d Hz %d ms: Reset and analyze allocations = %v", sampleRate, ms, n)
			}
		}
	}
}
