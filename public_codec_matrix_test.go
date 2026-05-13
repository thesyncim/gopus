package gopus

import (
	"math"
	"testing"
)

type publicCodecScenario struct {
	name        string
	sampleRate  int
	channels    int
	application Application
	setup       func(*testing.T, *Encoder, *Decoder)
	steps       []publicCodecStep
}

type publicCodecStep struct {
	name      string
	frameSize int
	silence   bool
	setup     func(*testing.T, *Encoder, *Decoder)
	wantModes []Mode
}

func runPublicCodecScenario(t *testing.T, sc publicCodecScenario) {
	t.Helper()

	enc := mustNewTestEncoder(t, sc.sampleRate, sc.channels, sc.application)
	dec := mustNewTestDecoder(t, sc.sampleRate, sc.channels)
	if sc.setup != nil {
		sc.setup(t, enc, dec)
	}

	packet := make([]byte, maxPacketBytesPerStream)
	pcmOut := make([]float32, defaultMaxPacketSamples*sc.channels)

	for i, step := range sc.steps {
		step := step
		t.Run(step.name, func(t *testing.T) {
			if step.frameSize > 0 && step.frameSize != enc.FrameSize() {
				if err := enc.SetFrameSize(step.frameSize); err != nil {
					t.Fatalf("SetFrameSize(%d): %v", step.frameSize, err)
				}
			}
			if step.setup != nil {
				step.setup(t, enc, dec)
			}

			pcmIn := publicCodecPCM(sc.sampleRate, enc.FrameSize(), sc.channels, i, step.silence)
			nPacket, err := enc.Encode(pcmIn, packet)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if nPacket == 0 {
				t.Fatal("Encode returned an empty packet")
			}

			info, err := ParsePacket(packet[:nPacket])
			if err != nil {
				t.Fatalf("ParsePacket: %v", err)
			}
			if len(step.wantModes) > 0 && !modeAllowed(info.TOC.Mode, step.wantModes) {
				t.Fatalf("packet mode=%v want one of %v", info.TOC.Mode, step.wantModes)
			}

			nPCM, err := dec.Decode(packet[:nPacket], pcmOut)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if nPCM != enc.FrameSize() {
				t.Fatalf("Decode samples=%d want %d", nPCM, enc.FrameSize())
			}
			if got := dec.LastPacketDuration(); got != nPCM {
				t.Fatalf("LastPacketDuration()=%d want %d", got, nPCM)
			}
			if got := dec.Bandwidth(); got != info.TOC.Bandwidth {
				t.Fatalf("Bandwidth()=%v want packet bandwidth %v", got, info.TOC.Bandwidth)
			}

			decoded := pcmOut[:nPCM*sc.channels]
			assertPublicCodecPCM(t, decoded)
			if !step.silence && computeEnergyFloat32(decoded) == 0 {
				t.Fatal("decoded output has zero energy")
			}
		})
	}
}

func publicCodecPCM(sampleRate, frameSize, channels, frameIndex int, silence bool) []float32 {
	pcm := make([]float32, frameSize*channels)
	if silence {
		return pcm
	}

	start := frameIndex * frameSize
	for i := 0; i < frameSize; i++ {
		for ch := 0; ch < channels; ch++ {
			freq := 180.0 + float64(ch)*170.0 + float64(frameIndex)*37.0
			t := float64(start+i) / float64(sampleRate)
			carrier := 0.34 * math.Sin(2*math.Pi*freq*t)
			motion := 0.05 * math.Sin(2*math.Pi*float64(3+ch)*t)
			pcm[i*channels+ch] = float32(carrier + motion)
		}
	}
	return pcm
}

func assertPublicCodecPCM(t *testing.T, pcm []float32) {
	t.Helper()

	for i, v := range pcm {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			t.Fatalf("decoded sample %d is not finite: %v", i, v)
		}
		if math.Abs(float64(v)) > 4 {
			t.Fatalf("decoded sample %d is out of range: %v", i, v)
		}
	}
}

func modeAllowed(got Mode, want []Mode) bool {
	for _, mode := range want {
		if got == mode {
			return true
		}
	}
	return false
}

func TestPublicCodecScenarioMatrix(t *testing.T) {
	scenarios := []publicCodecScenario{
		{
			name:        "restricted_silk_voice_controls",
			sampleRate:  48000,
			channels:    1,
			application: ApplicationRestrictedSilk,
			setup: func(t *testing.T, enc *Encoder, dec *Decoder) {
				t.Helper()
				if err := enc.SetBitrate(32000); err != nil {
					t.Fatalf("SetBitrate: %v", err)
				}
				if err := enc.SetBitrateMode(BitrateModeCBR); err != nil {
					t.Fatalf("SetBitrateMode: %v", err)
				}
				if err := enc.SetComplexity(4); err != nil {
					t.Fatalf("SetComplexity: %v", err)
				}
				if err := enc.SetBandwidth(BandwidthWideband); err != nil {
					t.Fatalf("SetBandwidth: %v", err)
				}
				if err := enc.SetLSBDepth(16); err != nil {
					t.Fatalf("SetLSBDepth: %v", err)
				}
				if err := enc.SetPacketLoss(12); err != nil {
					t.Fatalf("SetPacketLoss: %v", err)
				}
				if err := enc.SetSignal(SignalVoice); err != nil {
					t.Fatalf("SetSignal: %v", err)
				}
				enc.SetDTX(true)
				enc.SetFEC(true)
			},
			steps: []publicCodecStep{
				{name: "10ms", frameSize: 480, wantModes: []Mode{ModeSILK}},
				{name: "20ms", frameSize: 960, wantModes: []Mode{ModeSILK}},
				{name: "40ms", frameSize: 1920, wantModes: []Mode{ModeSILK}},
				{name: "silence_with_dtx_enabled", frameSize: 960, silence: true, wantModes: []Mode{ModeSILK}},
			},
		},
		{
			name:        "restricted_celt_stereo_short_frames",
			sampleRate:  48000,
			channels:    2,
			application: ApplicationRestrictedCelt,
			setup: func(t *testing.T, enc *Encoder, dec *Decoder) {
				t.Helper()
				if err := enc.SetBitrate(128000); err != nil {
					t.Fatalf("SetBitrate: %v", err)
				}
				if err := enc.SetComplexity(10); err != nil {
					t.Fatalf("SetComplexity: %v", err)
				}
				if err := enc.SetSignal(SignalMusic); err != nil {
					t.Fatalf("SetSignal: %v", err)
				}
				enc.SetPhaseInversionDisabled(true)
				enc.SetPredictionDisabled(true)
			},
			steps: []publicCodecStep{
				{name: "2_5ms", frameSize: 120, wantModes: []Mode{ModeCELT}},
				{name: "5ms", frameSize: 240, wantModes: []Mode{ModeCELT}},
				{name: "10ms", frameSize: 480, wantModes: []Mode{ModeCELT}},
				{name: "20ms", frameSize: 960, wantModes: []Mode{ModeCELT}},
			},
		},
		{
			name:        "auto_audio_controls_and_mode_changes",
			sampleRate:  48000,
			channels:    2,
			application: ApplicationAudio,
			setup: func(t *testing.T, enc *Encoder, dec *Decoder) {
				t.Helper()
				enc.SetVBR(true)
				enc.SetVBRConstraint(false)
				if err := enc.SetForceChannels(-1); err != nil {
					t.Fatalf("SetForceChannels: %v", err)
				}
			},
			steps: []publicCodecStep{
				{
					name:      "music_fullband",
					frameSize: 960,
					setup: func(t *testing.T, enc *Encoder, dec *Decoder) {
						t.Helper()
						if err := enc.SetBitrate(128000); err != nil {
							t.Fatalf("SetBitrate: %v", err)
						}
						if err := enc.SetBandwidth(BandwidthFullband); err != nil {
							t.Fatalf("SetBandwidth: %v", err)
						}
						if err := enc.SetSignal(SignalMusic); err != nil {
							t.Fatalf("SetSignal: %v", err)
						}
					},
				},
				{
					name:      "voice_wideband_fec",
					frameSize: 960,
					setup: func(t *testing.T, enc *Encoder, dec *Decoder) {
						t.Helper()
						if err := enc.SetBitrate(28000); err != nil {
							t.Fatalf("SetBitrate: %v", err)
						}
						if err := enc.SetBandwidth(BandwidthWideband); err != nil {
							t.Fatalf("SetBandwidth: %v", err)
						}
						if err := enc.SetPacketLoss(20); err != nil {
							t.Fatalf("SetPacketLoss: %v", err)
						}
						if err := enc.SetSignal(SignalVoice); err != nil {
							t.Fatalf("SetSignal: %v", err)
						}
						enc.SetFEC(true)
					},
				},
				{
					name:      "voice_fullband",
					frameSize: 960,
					setup: func(t *testing.T, enc *Encoder, dec *Decoder) {
						t.Helper()
						if err := enc.SetBitrate(48000); err != nil {
							t.Fatalf("SetBitrate: %v", err)
						}
						if err := enc.SetBandwidth(BandwidthFullband); err != nil {
							t.Fatalf("SetBandwidth: %v", err)
						}
						if err := enc.SetSignal(SignalVoice); err != nil {
							t.Fatalf("SetSignal: %v", err)
						}
					},
				},
				{
					name:      "short_music_after_voice",
					frameSize: 240,
					setup: func(t *testing.T, enc *Encoder, dec *Decoder) {
						t.Helper()
						if err := enc.SetBitrate(128000); err != nil {
							t.Fatalf("SetBitrate: %v", err)
						}
						if err := enc.SetSignal(SignalMusic); err != nil {
							t.Fatalf("SetSignal: %v", err)
						}
					},
				},
			},
		},
	}

	for _, sc := range scenarios {
		sc := sc
		t.Run(sc.name, func(t *testing.T) {
			runPublicCodecScenario(t, sc)
		})
	}
}

func TestPublicEncoderControlsProduceDecodablePackets(t *testing.T) {
	tests := []struct {
		name        string
		channels    int
		application Application
		setup       func(*testing.T, *Encoder)
	}{
		{
			name:        "bitrate_complexity_and_cbr_floor",
			channels:    1,
			application: ApplicationAudio,
			setup: func(t *testing.T, enc *Encoder) {
				t.Helper()
				if err := enc.SetBitrate(6000); err != nil {
					t.Fatalf("SetBitrate: %v", err)
				}
				if err := enc.SetComplexity(0); err != nil {
					t.Fatalf("SetComplexity: %v", err)
				}
				if err := enc.SetBitrateMode(BitrateModeCBR); err != nil {
					t.Fatalf("SetBitrateMode: %v", err)
				}
			},
		},
		{
			name:        "expert_frame_duration_2_5ms",
			channels:    1,
			application: ApplicationLowDelay,
			setup: func(t *testing.T, enc *Encoder) {
				t.Helper()
				if err := enc.SetExpertFrameDuration(ExpertFrameDuration2_5Ms); err != nil {
					t.Fatalf("SetExpertFrameDuration: %v", err)
				}
			},
		},
		{
			name:        "expert_frame_duration_120ms",
			channels:    1,
			application: ApplicationRestrictedCelt,
			setup: func(t *testing.T, enc *Encoder) {
				t.Helper()
				if err := enc.SetExpertFrameDuration(ExpertFrameDuration120Ms); err != nil {
					t.Fatalf("SetExpertFrameDuration: %v", err)
				}
			},
		},
		{
			name:        "fec_dtx_packet_loss_and_voice_signal",
			channels:    1,
			application: ApplicationVoIP,
			setup: func(t *testing.T, enc *Encoder) {
				t.Helper()
				if err := enc.SetBitrate(24000); err != nil {
					t.Fatalf("SetBitrate: %v", err)
				}
				if err := enc.SetPacketLoss(25); err != nil {
					t.Fatalf("SetPacketLoss: %v", err)
				}
				if err := enc.SetMaxBandwidth(BandwidthWideband); err != nil {
					t.Fatalf("SetMaxBandwidth: %v", err)
				}
				if err := enc.SetSignal(SignalVoice); err != nil {
					t.Fatalf("SetSignal: %v", err)
				}
				enc.SetDTX(true)
				enc.SetFEC(true)
			},
		},
		{
			name:        "stereo_channel_and_prediction_controls",
			channels:    2,
			application: ApplicationAudio,
			setup: func(t *testing.T, enc *Encoder) {
				t.Helper()
				if err := enc.SetBitrate(96000); err != nil {
					t.Fatalf("SetBitrate: %v", err)
				}
				if err := enc.SetForceChannels(1); err != nil {
					t.Fatalf("SetForceChannels: %v", err)
				}
				if err := enc.SetLSBDepth(12); err != nil {
					t.Fatalf("SetLSBDepth: %v", err)
				}
				enc.SetPhaseInversionDisabled(true)
				enc.SetPredictionDisabled(true)
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			enc := mustNewTestEncoder(t, 48000, tc.channels, tc.application)
			dec := mustNewTestDecoder(t, 48000, tc.channels)
			tc.setup(t, enc)

			packet := make([]byte, maxPacketBytesPerStream)
			pcmIn := publicCodecPCM(48000, enc.FrameSize(), tc.channels, 0, false)
			nPacket, err := enc.Encode(pcmIn, packet)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if nPacket == 0 {
				t.Fatal("Encode returned an empty packet")
			}
			if _, err := ParsePacket(packet[:nPacket]); err != nil {
				t.Fatalf("ParsePacket: %v", err)
			}

			pcmOut := make([]float32, defaultMaxPacketSamples*tc.channels)
			nPCM, err := dec.Decode(packet[:nPacket], pcmOut)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if nPCM != enc.FrameSize() {
				t.Fatalf("Decode samples=%d want %d", nPCM, enc.FrameSize())
			}
			assertPublicCodecPCM(t, pcmOut[:nPCM*tc.channels])

			enc.Reset()
			nPacket, err = enc.Encode(pcmIn, packet)
			if err != nil {
				t.Fatalf("Encode after Reset: %v", err)
			}
			if nPacket == 0 {
				t.Fatal("Encode after Reset returned an empty packet")
			}
		})
	}
}

func TestPublicDecoderTransitionsLossAndReset(t *testing.T) {
	silk := publicEncodedPacket(t, ApplicationRestrictedSilk, 1, 960, func(t *testing.T, enc *Encoder) {
		t.Helper()
		if err := enc.SetBitrate(32000); err != nil {
			t.Fatalf("SetBitrate: %v", err)
		}
		if err := enc.SetBandwidth(BandwidthWideband); err != nil {
			t.Fatalf("SetBandwidth: %v", err)
		}
	})
	celt := publicEncodedPacket(t, ApplicationRestrictedCelt, 1, 960, func(t *testing.T, enc *Encoder) {
		t.Helper()
		if err := enc.SetBitrate(96000); err != nil {
			t.Fatalf("SetBitrate: %v", err)
		}
	})
	hybrid := publicEncodedPacket(t, ApplicationAudio, 1, 960, func(t *testing.T, enc *Encoder) {
		t.Helper()
		if err := enc.SetBitrate(36000); err != nil {
			t.Fatalf("SetBitrate: %v", err)
		}
		if err := enc.SetBandwidth(BandwidthFullband); err != nil {
			t.Fatalf("SetBandwidth: %v", err)
		}
		if err := enc.SetSignal(SignalVoice); err != nil {
			t.Fatalf("SetSignal: %v", err)
		}
	})

	sequence := []struct {
		name string
		data []byte
		mode Mode
	}{
		{name: "silk", data: silk, mode: ModeSILK},
		{name: "celt", data: celt, mode: ModeCELT},
		{name: "hybrid", data: hybrid, mode: ModeHybrid},
	}

	dec := mustNewTestDecoder(t, 48000, 1)
	if err := dec.SetGain(0); err != nil {
		t.Fatalf("SetGain: %v", err)
	}
	dec.SetIgnoreExtensions(true)

	pcm := make([]float32, defaultMaxPacketSamples)
	for _, step := range sequence {
		step := step
		t.Run(step.name, func(t *testing.T) {
			info, err := ParsePacket(step.data)
			if err != nil {
				t.Fatalf("ParsePacket: %v", err)
			}
			if info.TOC.Mode != step.mode {
				t.Fatalf("packet mode=%v want %v", info.TOC.Mode, step.mode)
			}
			n, err := dec.Decode(step.data, pcm)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if n != 960 {
				t.Fatalf("Decode samples=%d want 960", n)
			}
			assertPublicCodecPCM(t, pcm[:n])
		})
	}

	n, err := dec.Decode(nil, pcm)
	if err != nil {
		t.Fatalf("Decode PLC nil: %v", err)
	}
	if n != 960 {
		t.Fatalf("Decode PLC nil samples=%d want 960", n)
	}
	n, err = dec.Decode([]byte{}, pcm)
	if err != nil {
		t.Fatalf("Decode PLC empty: %v", err)
	}
	if n != 960 {
		t.Fatalf("Decode PLC empty samples=%d want 960", n)
	}

	dec.Reset()
	if !dec.IgnoreExtensions() {
		t.Fatal("IgnoreExtensions should survive Reset")
	}
	n, err = dec.Decode(nil, pcm)
	if err != nil {
		t.Fatalf("cold Decode PLC after Reset: %v", err)
	}
	if n != 960 {
		t.Fatalf("cold Decode PLC after Reset samples=%d want 960", n)
	}
}

func publicEncodedPacket(t *testing.T, application Application, channels, frameSize int, setup func(*testing.T, *Encoder)) []byte {
	t.Helper()

	enc := mustNewTestEncoder(t, 48000, channels, application)
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize(%d): %v", frameSize, err)
	}
	if setup != nil {
		setup(t, enc)
	}

	packet := make([]byte, maxPacketBytesPerStream)
	pcm := publicCodecPCM(48000, frameSize, channels, 0, false)
	n, err := enc.Encode(pcm, packet)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if n == 0 {
		t.Fatal("Encode returned an empty packet")
	}
	return append([]byte(nil), packet[:n]...)
}
