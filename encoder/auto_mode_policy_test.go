package encoder

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/types"
)

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
