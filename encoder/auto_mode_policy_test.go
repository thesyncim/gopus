package encoder

import (
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
