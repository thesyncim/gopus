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

	pcm := make([]opusRes, 1920)
	for i := range pcm {
		pcm[i] = opusRes(math.NaN())
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

	pcm := make([]opusRes, 960)
	for i := range pcm {
		t := float64(i) / 48000.0
		pcm[i] = opusRes(0.8*math.Sin(2*math.Pi*3000*t) + 0.2*math.Sin(2*math.Pi*120*t))
	}

	if got := enc.autoSignalFromPCM(pcm, 960); got != types.SignalAuto {
		t.Fatalf("autoSignalFromPCM(no-analyzer) = %v, want %v", got, types.SignalAuto)
	}
}

func TestAutoModePreservesVoiceRatioOnDigitalSilence(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeAuto)
	enc.SetBitrate(24000)
	enc.SetBitrateMode(ModeVBR)
	enc.analyzer = nil
	enc.voiceRatio = 73

	pcm := make([]opusRes, 960)
	_ = enc.autoModeAndBandwidthDecision(pcm, 960, maxSilkPacketBytes, true)

	if got := enc.voiceRatio; got != 73 {
		t.Fatalf("voiceRatio on silence = %d, want preserved 73", got)
	}
}

func TestAutoModeResetsVoiceRatioOnNonSilentFrame(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeAuto)
	enc.SetBitrate(24000)
	enc.SetBitrateMode(ModeVBR)
	enc.analyzer = nil
	enc.voiceRatio = 73

	pcm := make([]opusRes, 960)
	pcm[0] = opusRes(1.0 / (1 << 12))
	_ = enc.autoModeAndBandwidthDecision(pcm, 960, maxSilkPacketBytes, false)

	if got := enc.voiceRatio; got != -1 {
		t.Fatalf("voiceRatio on non-silence = %d, want reset -1", got)
	}
}

func TestAutoClampBandwidthUsesPacketBudgetMaxRate(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetBandwidthAuto()
	enc.SetMaxBandwidth(types.BandwidthFullband)
	enc.SetBitrate(64000)

	if got := enc.autoClampBandwidth(types.BandwidthFullband, ModeHybrid, 64000, enc.maxRateForFrame(960, 37)); got != types.BandwidthWideband {
		t.Fatalf("autoClampBandwidth(low max_rate) = %v, want %v", got, types.BandwidthWideband)
	}
	if got := enc.autoClampBandwidth(types.BandwidthFullband, ModeHybrid, 64000, enc.maxRateForFrame(960, 38)); got != types.BandwidthFullband {
		t.Fatalf("autoClampBandwidth(safe max_rate) = %v, want %v", got, types.BandwidthFullband)
	}
}

func TestAutoModeLowRateCELTFallbackUsesPacketBudget(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeAuto)
	enc.bitrate = 4000

	const (
		frameSize       = 960
		silkEquivRate   = 0
		voiceEst        = 128
		monoStereoWidth = 0
	)
	threshold := lowRateCELTByteThreshold(enc.sampleRate, frameSize)
	if threshold != 15 {
		t.Fatalf("lowRateCELTByteThreshold(20ms) = %d, want 15", threshold)
	}

	if got := enc.autoModeDecision(monoStereoWidth, voiceEst, silkEquivRate, frameSize, threshold-1); got != ModeCELT {
		t.Fatalf("autoModeDecision(low packet budget) = %v, want %v", got, ModeCELT)
	}
	if got := enc.autoModeDecision(monoStereoWidth, voiceEst, silkEquivRate, frameSize, threshold); got != ModeSILK {
		t.Fatalf("autoModeDecision(exact packet budget) = %v, want %v", got, ModeSILK)
	}
}

func TestLowRateCELTByteThresholdMatchesLibopusIntegerDivision(t *testing.T) {
	tests := []struct {
		name       string
		sampleRate int
		frameSize  int
		want       int
	}{
		{name: "20ms_48k", sampleRate: 48000, frameSize: 960, want: 15},
		{name: "10ms_48k", sampleRate: 48000, frameSize: 480, want: 11},
		{name: "5ms_48k", sampleRate: 48000, frameSize: 240, want: 5},
		{name: "20ms_16k", sampleRate: 16000, frameSize: 320, want: 15},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lowRateCELTByteThreshold(tt.sampleRate, tt.frameSize); got != tt.want {
				t.Fatalf("lowRateCELTByteThreshold(%d, %d) = %d, want %d",
					tt.sampleRate, tt.frameSize, got, tt.want)
			}
		})
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
