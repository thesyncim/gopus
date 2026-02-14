package encoder

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/types"
)

func TestSelectSWBAutoSignal10msHysteresis(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeAuto)
	enc.SetBandwidth(types.BandwidthSuperwideband)
	enc.SetBitrate(20000)
	enc.SetBitrateMode(ModeVBR)

	enc.lastAnalysisValid = true
	enc.lastAnalysisInfo.MusicProb = 0.6
	enc.lastAnalysisInfo.MusicProbMin = 0.2
	enc.lastAnalysisInfo.MusicProbMax = 0.8

	if got := enc.selectSWBAutoSignal(480, ModeCELT); got != types.SignalMusic {
		t.Fatalf("selectSWBAutoSignal(10ms, prev=CELT) = %v, want %v", got, types.SignalMusic)
	}
	if got := enc.selectSWBAutoSignal(480, ModeHybrid); got != types.SignalVoice {
		t.Fatalf("selectSWBAutoSignal(10ms, prev=Hybrid) = %v, want %v", got, types.SignalVoice)
	}
}

func TestAutoSignalFromPCMSWB10UsesThresholdPolicy(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeAuto)
	enc.SetBandwidth(types.BandwidthSuperwideband)
	enc.SetBitrate(20000)
	enc.SetBitrateMode(ModeVBR)

	enc.lastAnalysisFresh = true
	enc.lastAnalysisValid = true
	enc.lastAnalysisInfo.MusicProb = 0.6
	enc.lastAnalysisInfo.MusicProbMin = 0.2
	enc.lastAnalysisInfo.MusicProbMax = 0.8

	pcm := make([]float64, 480)

	enc.prevSWB10AutoMode = ModeCELT
	if got := enc.autoSignalFromPCM(pcm, 480); got != types.SignalMusic {
		t.Fatalf("autoSignalFromPCM(prev=CELT) = %v, want %v", got, types.SignalMusic)
	}

	enc.prevSWB10AutoMode = ModeHybrid
	if got := enc.autoSignalFromPCM(pcm, 480); got != types.SignalVoice {
		t.Fatalf("autoSignalFromPCM(prev=Hybrid) = %v, want %v", got, types.SignalVoice)
	}
}

func TestAutoSignalFromPCMAnalyzerInvalidFallsBackToAuto(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeAuto)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(32000)
	enc.SetBitrateMode(ModeVBR)

	pcm := make([]float64, 1920)
	for i := range pcm {
		pcm[i] = math.NaN()
	}

	if got := enc.autoSignalFromPCM(pcm, 1920); got != types.SignalAuto {
		t.Fatalf("autoSignalFromPCM(invalid-analysis) = %v, want %v", got, types.SignalAuto)
	}
	if enc.lastAnalysisValid {
		t.Fatal("analysis should remain invalid on NaN input")
	}
}

func TestAutoSignalFromPCMAnalyzerUnavailableFallsBackToAuto(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeAuto)
	enc.SetBandwidth(types.BandwidthWideband)
	enc.SetBitrate(24000)
	enc.SetBitrateMode(ModeVBR)
	enc.analyzer = nil

	pcm := make([]float64, 960)
	for i := range pcm {
		t := float64(i) / 48000.0
		pcm[i] = 0.8*math.Sin(2*math.Pi*3000*t) + 0.2*math.Sin(2*math.Pi*120*t)
	}

	if got := enc.autoSignalFromPCM(pcm, 960); got != types.SignalAuto {
		t.Fatalf("autoSignalFromPCM(no-analyzer) = %v, want %v", got, types.SignalAuto)
	}
}
