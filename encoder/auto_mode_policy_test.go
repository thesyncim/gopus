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

func TestNonAutoEncodeUpdatesStreamChannelsForCELTState(t *testing.T) {
	pcm := make([]float64, 960*2)
	for i := 0; i < 960; i++ {
		pcm[2*i] = 0.2 * math.Sin(2*math.Pi*440*float64(i)/48000)
		pcm[2*i+1] = 0.2 * math.Sin(2*math.Pi*660*float64(i)/48000)
	}

	tests := []struct {
		name          string
		bitrate       int
		signal        types.Signal
		forceChannels int
		want          int
	}{
		{name: "music_threshold_mono", bitrate: 16000, signal: types.SignalMusic, forceChannels: -1, want: 1},
		{name: "voice_threshold_stereo", bitrate: 20000, signal: types.SignalVoice, forceChannels: -1, want: 2},
		{name: "forced_mono", bitrate: 64000, signal: types.SignalAuto, forceChannels: 1, want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc := NewEncoder(48000, 2)
			enc.SetMode(ModeCELT)
			enc.SetBandwidth(types.BandwidthFullband)
			enc.SetBitrate(tt.bitrate)
			enc.signalType = tt.signal
			enc.SetForceChannels(tt.forceChannels)

			if _, err := enc.Encode(pcm, 960); err != nil {
				t.Fatalf("Encode() error: %v", err)
			}
			if got := enc.streamChannels; got != tt.want {
				t.Fatalf("streamChannels = %d, want %d", got, tt.want)
			}
			if enc.celtEncoder == nil {
				t.Fatal("celt encoder was not initialized")
			}
			if got := enc.celtEncoder.StreamChannels(); got != tt.want {
				t.Fatalf("CELT StreamChannels() = %d, want %d", got, tt.want)
			}
		})
	}
}
