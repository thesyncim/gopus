package encoder

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/testsignal"
)

// TestSpeechProbabilityMatchesLibopus requires SpeechProbability to equal the
// activity_probability that libopus run_analysis() returns for the same frames,
// bit for bit, and the analyzer state to stay identical. The one intended
// difference is digital silence, where libopus repeats the previous entry and
// SpeechProbability reports 0.
func TestSpeechProbabilityMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	classes := []string{testsignal.CorpusCleanSpeechV1, testsignal.CorpusMixedV1, testsignal.CorpusSpeechInNoiseV1,
		testsignal.CorpusWhiteNoiseV1, testsignal.CorpusMusicV1, testsignal.CorpusSilenceBurstsV1}
	for _, fs := range []int{16000, 24000, 48000} {
		for _, ms := range []int{10, 20} {
			for _, class := range classes {
				t.Run(fmt.Sprintf("%dk/%dms/%s", fs/1000, ms, class), func(t *testing.T) {
					frameSize := fs * ms / 1000
					numFrames := 2000 / ms
					pcm, err := testsignal.GenerateCorpusSignal(class, fs, numFrames*frameSize, 1)
					if err != nil {
						t.Fatalf("GenerateCorpusSignal: %v", err)
					}
					want := runLibopusAnalysisOracle(t, fs, 1, frameSize, 24, pcm)
					an := NewTonalityAnalysisState(fs)
					silent := 0
					for f := range want {
						got := an.SpeechProbability(pcm[f*frameSize : (f+1)*frameSize])
						expect := float32(0)
						if an.silentWindow {
							silent++
						} else if want[f].ret.valid != 0 {
							expect = math.Float32frombits(want[f].ret.activityProb)
						}
						if math.Float32bits(got) != math.Float32bits(expect) {
							t.Fatalf("frame %d: probability %v want %v", f, got, expect)
						}
						if s := analysisStateDiff(an, want[f]); s != "" {
							t.Fatalf("frame %d: analyzer state differs:%s", f, s)
						}
					}
					if (class == testsignal.CorpusSilenceBurstsV1) != (silent > 0) {
						t.Fatalf("%d frames ended on a silent window for %s", silent, class)
					}
				})
			}
		}
	}
}

// TestSpeechProbabilityInt16MatchesLibopus feeds 16-bit PCM scaled to the float
// range, as the public detector does, against libopus's 16-bit input path.
func TestSpeechProbabilityInt16MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, fs := range []int{16000, 48000} {
		t.Run(fmt.Sprintf("%dk", fs/1000), func(t *testing.T) {
			const frameMS = 20
			frameSize := fs * frameMS / 1000
			numFrames := 2000 / frameMS
			sig, err := testsignal.GenerateCorpusSignal(testsignal.CorpusCleanSpeechV1, fs, numFrames*frameSize, 1)
			if err != nil {
				t.Fatalf("GenerateCorpusSignal: %v", err)
			}
			pcm := make([]int16, len(sig))
			scaled := make([]float32, len(sig))
			for i, v := range sig {
				pcm[i] = int16(math.Max(-32768, math.Min(32767, math.Round(float64(v)*32768))))
				scaled[i] = float32(pcm[i]) * (1.0 / 32768.0)
			}
			want := runLibopusAnalysisOracleShort(t, fs, 1, frameSize, 24, 0, -2, pcm)
			an := NewTonalityAnalysisState(fs)
			for f := range want {
				got := an.SpeechProbability(scaled[f*frameSize : (f+1)*frameSize])
				expect := float32(0)
				if want[f].ret.valid != 0 {
					expect = math.Float32frombits(want[f].ret.activityProb)
				}
				if math.Float32bits(got) != math.Float32bits(expect) {
					t.Fatalf("frame %d: probability %v want %v", f, got, expect)
				}
			}
		})
	}
}

func TestSpeechProbabilityDigitalSilence(t *testing.T) {
	const fs, frameSize = 16000, 320
	speech, err := testsignal.GenerateCorpusSignal(testsignal.CorpusCleanSpeechV1, fs, 40*frameSize, 1)
	if err != nil {
		t.Fatalf("GenerateCorpusSignal: %v", err)
	}
	an := NewTonalityAnalysisState(fs)
	var last float32
	for f := range 40 {
		last = an.SpeechProbability(speech[f*frameSize : (f+1)*frameSize])
	}
	if last == 0 || an.silentWindow {
		t.Fatalf("speech probability %v silentWindow %v after speech", last, an.silentWindow)
	}
	zeros := make([]float32, frameSize)
	var held float32
	for f := range 3 {
		got := an.SpeechProbability(zeros)
		if f == 2 {
			held = an.GetInfo().VADProb
			if !an.silentWindow || got != 0 {
				t.Fatalf("silent window: probability %v silentWindow %v", got, an.silentWindow)
			}
		}
	}
	if held == 0 {
		t.Fatal("the analyzer's own entry does not repeat the last speech estimate through silence")
	}
	if got := an.SpeechProbability(speech[:frameSize]); an.silentWindow || got == 0 {
		t.Fatalf("speech after silence: probability %v silentWindow %v", got, an.silentWindow)
	}
	an.SpeechProbability(zeros)
	an.SpeechProbability(zeros)
	an.Reset()
	if an.silentWindow {
		t.Fatal("Reset keeps the silent-window flag")
	}
}
