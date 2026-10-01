//go:build gopus_fixed_point

package gopus

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// encodeFixedModeSwitchSequence encodes a sequence of frames that alternates the
// forced coding mode (Hybrid <-> CELT) on a single encoder so the bitstream
// carries genuine CELT redundancy and CELT<->SILK/Hybrid mode-transition frames,
// mirroring how libopus inserts redundant CELT frames and the 5 ms smooth_fade
// crossfade at mode boundaries. The first frame is forced Hybrid so the integer
// Hybrid path is the one carrying the redundancy / transition.
func encodeFixedModeSwitchSequence(t *testing.T, channels, frameSize int, modes []EncoderMode) [][]byte {
	t.Helper()
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: ApplicationAudio,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(96000); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetInBandFEC(InBandFECDisabled); err != nil {
		t.Fatalf("SetInBandFEC: %v", err)
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
	}

	packets := make([][]byte, 0, len(modes))
	phase := 0.0
	for f, m := range modes {
		if err := enc.SetMode(m); err != nil {
			t.Fatalf("frame %d SetMode: %v", f, err)
		}
		pcm := make([]float32, frameSize*channels)
		for i := 0; i < frameSize; i++ {
			tm := (phase + float64(i)) / sampleRate
			pcm[i*channels] = 0.24*float32(math.Sin(2*math.Pi*220*tm)) +
				0.12*float32(math.Sin(2*math.Pi*1300*tm+0.17))
			if channels == 2 {
				pcm[i*channels+1] = 0.21*float32(math.Sin(2*math.Pi*330*tm+0.09)) +
					0.10*float32(math.Sin(2*math.Pi*1700*tm+0.31))
			}
		}
		phase += float64(frameSize)
		pkt, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("frame %d Encode: %v", f, err)
		}
		packets = append(packets, append([]byte(nil), pkt...))
	}
	return packets
}

// encodeFixedSingleModePacket encodes one independent frame in the given forced
// mode from a fresh encoder, so a concatenated CELT-then-Hybrid sequence produces
// a Hybrid frame whose previous frame was CELT but which carries no redundancy
// (a fresh encoder has no prior frame to prefill from), exercising the pure
// integer transition smooth_fade crossfade.
func encodeFixedSingleModePacket(t *testing.T, channels, frameSize int, mode EncoderMode, phase float64) []byte {
	t.Helper()
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: ApplicationAudio,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(96000); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetInBandFEC(InBandFECDisabled); err != nil {
		t.Fatalf("SetInBandFEC: %v", err)
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
	}
	if err := enc.SetMode(mode); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	pcm := make([]float32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		tm := (phase + float64(i)) / sampleRate
		pcm[i*channels] = 0.24*float32(math.Sin(2*math.Pi*220*tm)) +
			0.12*float32(math.Sin(2*math.Pi*1300*tm+0.17))
		if channels == 2 {
			pcm[i*channels+1] = 0.21*float32(math.Sin(2*math.Pi*330*tm+0.09)) +
				0.10*float32(math.Sin(2*math.Pi*1700*tm+0.31))
		}
	}
	pkt, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return append([]byte(nil), pkt...)
}

// TestDecoderFixedPointHybridTransitionParity gates that the public DecodeInt16 /
// DecodeInt24 of a CELT-then-Hybrid transition (no redundancy) is bit-exact with
// the libopus FIXED_POINT opus_decode / opus_decode24 reference. The Hybrid frame
// after a CELT frame triggers the 5 ms CELT PLC pcm_transition decode and the
// integer opus_res smooth_fade crossfade (opus_decode_frame transition path).
// The tested integer outputs match the selected FIXED_POINT reference build.
func TestDecoderFixedPointHybridTransitionParity(t *testing.T) {
	libopustest.RequireOracle(t)

	type tc struct {
		name      string
		channels  int
		frameSize int
	}
	cases := []tc{
		{"mono_960", 1, 960},
		{"stereo_960", 2, 960},
		{"mono_480", 1, 480},
	}

	const sampleRate = 48000
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			phase := 0.0
			celtPkt := encodeFixedSingleModePacket(t, c.channels, c.frameSize, EncoderModeCELT, phase)
			phase += float64(c.frameSize)
			hyb1 := encodeFixedSingleModePacket(t, c.channels, c.frameSize, EncoderModeHybrid, phase)
			phase += float64(c.frameSize)
			hyb2 := encodeFixedSingleModePacket(t, c.channels, c.frameSize, EncoderModeHybrid, phase)
			packets := [][]byte{celtPkt, hyb1, hyb2}

			if toc := ParseTOC(celtPkt[0]); toc.Mode != ModeCELT {
				t.Fatalf("first packet mode %v, want CELT", toc.Mode)
			}
			if toc := ParseTOC(hyb1[0]); toc.Mode != ModeHybrid {
				t.Fatalf("second packet mode %v, want Hybrid", toc.Mode)
			}

			refInt16, err := decodeWithLibopusFixedInt16(sampleRate, c.channels, c.frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed reference decode int16", err)
				return
			}
			refInt24, err := decodeWithLibopusFixedInt24(sampleRate, c.channels, c.frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed reference decode int24", err)
				return
			}

			dec16, _ := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
			dec24, _ := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
			var got16, got24 []int32
			for p, pkt := range packets {
				o16 := make([]int16, c.frameSize*c.channels)
				if _, err := dec16.DecodeInt16(pkt, o16); err != nil {
					t.Fatalf("packet %d DecodeInt16: %v", p, err)
				}
				got16 = append(got16, int16ToInt32(o16)...)
				o24 := make([]int32, c.frameSize*c.channels)
				if _, err := dec24.DecodeInt24(pkt, o24); err != nil {
					t.Fatalf("packet %d DecodeInt24: %v", p, err)
				}
				got24 = append(got24, o24...)
			}

			if dec16.fixedTransitionApplied == 0 {
				t.Fatalf("stream did not exercise the integer transition crossfade")
			}
			t.Logf("integer transition applied=%d, redundancy applied=%d",
				dec16.fixedTransitionApplied, dec16.fixedRedundancyApplied)

			assertFixedExact(t, "int16", got16, int16ToInt32(refInt16))
			assertFixedExact(t, "int24", got24, refInt24)
		})
	}
}

// TestDecoderFixedPointHybridRedundancyTransitionParity gates that the public
// DecodeInt16 / DecodeInt24 of a mode-switching stream (Hybrid <-> CELT) remains
// bit-exact with the libopus FIXED_POINT opus_decode / opus_decode24 reference
// across the frames that carry CELT redundancy and the CELT->SILK/Hybrid
// transition smooth_fade. The integer redundancy frame is decoded in the opus_res
// domain on the same integer CELT decoder as the main hybrid highband, and the
// crossfade uses the integer smooth_fade (FIXED_POINT ENABLE_RES24), mirroring
// opus_decode_frame. The tested integer outputs match the selected FIXED_POINT
// reference build.
func TestDecoderFixedPointHybridRedundancyTransitionParity(t *testing.T) {
	libopustest.RequireOracle(t)

	type tc struct {
		name      string
		channels  int
		frameSize int
		modes     []EncoderMode
	}
	cases := []tc{
		{
			name:      "mono_hybrid_celt_hybrid",
			channels:  1,
			frameSize: 960,
			modes:     []EncoderMode{EncoderModeHybrid, EncoderModeHybrid, EncoderModeCELT, EncoderModeHybrid, EncoderModeHybrid},
		},
		{
			name:      "stereo_hybrid_celt_hybrid",
			channels:  2,
			frameSize: 960,
			modes:     []EncoderMode{EncoderModeHybrid, EncoderModeHybrid, EncoderModeCELT, EncoderModeHybrid, EncoderModeHybrid},
		},
		{
			name:      "mono_celt_hybrid_celt",
			channels:  1,
			frameSize: 960,
			modes:     []EncoderMode{EncoderModeHybrid, EncoderModeCELT, EncoderModeHybrid, EncoderModeCELT, EncoderModeHybrid, EncoderModeHybrid},
		},
		{
			name:      "mono_480_alternating",
			channels:  1,
			frameSize: 480,
			modes:     []EncoderMode{EncoderModeHybrid, EncoderModeHybrid, EncoderModeCELT, EncoderModeCELT, EncoderModeHybrid, EncoderModeHybrid},
		},
	}

	const sampleRate = 48000
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			packets := encodeFixedModeSwitchSequence(t, c.channels, c.frameSize, c.modes)

			refInt16, err := decodeWithLibopusFixedInt16(sampleRate, c.channels, c.frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed reference decode int16", err)
				return
			}
			refInt24, err := decodeWithLibopusFixedInt24(sampleRate, c.channels, c.frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed reference decode int24", err)
				return
			}

			dec16, _ := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
			dec24, _ := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
			var got16, got24 []int32
			for p, pkt := range packets {
				o16 := make([]int16, c.frameSize*c.channels)
				if _, err := dec16.DecodeInt16(pkt, o16); err != nil {
					t.Fatalf("packet %d DecodeInt16: %v", p, err)
				}
				got16 = append(got16, int16ToInt32(o16)...)
				o24 := make([]int32, c.frameSize*c.channels)
				if _, err := dec24.DecodeInt24(pkt, o24); err != nil {
					t.Fatalf("packet %d DecodeInt24: %v", p, err)
				}
				got24 = append(got24, o24...)
			}

			// Confirm the stream actually exercises the integer redundancy /
			// transition smooth_fade path so the gate is meaningful.
			if dec16.fixedRedundancyApplied == 0 && dec16.fixedTransitionApplied == 0 {
				t.Fatalf("stream did not exercise the integer redundancy/transition path "+
					"(redundancy=%d transition=%d)", dec16.fixedRedundancyApplied, dec16.fixedTransitionApplied)
			}
			t.Logf("integer redundancy applied=%d, transition applied=%d",
				dec16.fixedRedundancyApplied, dec16.fixedTransitionApplied)

			assertFixedExact(t, "int16", got16, int16ToInt32(refInt16))
			assertFixedExact(t, "int24", got24, refInt24)
		})
	}
}

// TestDecoderFixedPointRedundancyCodedOutputChannelParity keeps the CELT
// redundancy decode's packet-coded channel count separate from the public
// decoder output channel count. libopus passes stream_channels as C while
// retaining the decoder's channel count as CC during synthesis.
func TestDecoderFixedPointRedundancyCodedOutputChannelParity(t *testing.T) {
	libopustest.RequireOracle(t)

	const sampleRate = 48000
	const frameSize = 960
	modes := []EncoderMode{
		EncoderModeHybrid, EncoderModeHybrid, EncoderModeCELT,
		EncoderModeHybrid, EncoderModeHybrid,
	}

	for _, tc := range []struct {
		name           string
		codedChannels  int
		outputChannels int
	}{
		{name: "stereo_packet_mono_output", codedChannels: 2, outputChannels: 1},
		{name: "mono_packet_stereo_output", codedChannels: 1, outputChannels: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packets := encodeFixedModeSwitchSequence(t, tc.codedChannels, frameSize, modes)

			refInt16, err := decodeWithLibopusFixedInt16(sampleRate, tc.outputChannels, frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed reference decode int16", err)
				return
			}
			refInt24, err := decodeWithLibopusFixedInt24(sampleRate, tc.outputChannels, frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed reference decode int24", err)
				return
			}

			dec16, err := NewDecoder(DefaultDecoderConfig(sampleRate, tc.outputChannels))
			if err != nil {
				t.Fatal(err)
			}
			dec24, err := NewDecoder(DefaultDecoderConfig(sampleRate, tc.outputChannels))
			if err != nil {
				t.Fatal(err)
			}
			for p, packet := range packets {
				out16 := make([]int16, frameSize*tc.outputChannels)
				if _, err := dec16.DecodeInt16(packet, out16); err != nil {
					t.Fatalf("packet %d DecodeInt16: %v", p, err)
				}
				frame16 := int16ToInt32(out16)
				out24 := make([]int32, frameSize*tc.outputChannels)
				if _, err := dec24.DecodeInt24(packet, out24); err != nil {
					t.Fatalf("packet %d DecodeInt24: %v", p, err)
				}
				start := p * frameSize * tc.outputChannels
				assertFixedExact(t, fmt.Sprintf("int16 packet %d", p), frame16, int16ToInt32(refInt16[start:start+len(frame16)]))
				assertFixedExact(t, fmt.Sprintf("int24 packet %d", p), out24, refInt24[start:start+len(out24)])
			}

			if dec16.fixedRedundancyApplied == 0 {
				t.Fatalf("stream did not exercise integer redundancy decode")
			}
		})
	}
}

func decodeWithLibopusFixedPhaseControl(sampleFormat uint32, sampleRate, channels, frameSize int, disabled bool, packets [][]byte) (*libopustest.OracleReader, error) {
	binPath, err := getFixedRefdecodeHelperPath()
	if err != nil {
		return nil, err
	}
	phase := uint32(0)
	if disabled {
		phase = 1
	}
	payload := libopustest.NewOraclePayloadVersion("GOSI", 9,
		sampleFormat, uint32(sampleRate), 0, phase,
		uint32(channels), uint32(frameSize), uint32(len(packets)))
	for _, packet := range packets {
		payload.U32(0) // decode_fec
		payload.U32(uint32(frameSize))
		payload.U32(uint32(len(packet)))
		payload.Raw(packet)
	}
	return libopustest.RunOracle(binPath, payload.Bytes(), "fixed reference phase-control decode", "GOSO")
}

func TestFixedPointPhaseInversionControlMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate, channels, frameSize = 48000, 2, 960
	packets := encodeFixedModeSwitchSequence(t, channels, frameSize,
		[]EncoderMode{EncoderModeCELT, EncoderModeCELT, EncoderModeCELT})
	if len(packets) < 2 {
		t.Fatalf("encoder produced %d packets", len(packets))
	}
	for i, packet := range packets {
		if len(packet) == 0 || packet[0]&4 == 0 {
			t.Fatalf("packet %d is not stereo-coded: %x", i, packet[:min(len(packet), 1)])
		}
	}

	for _, phaseDisabled := range []bool{true, false} {
		name := "phase_enabled"
		if phaseDisabled {
			name = "phase_disabled"
		}
		t.Run(name, func(t *testing.T) {
			for _, sampleFormat := range []uint32{libopusRefdecodeSingleFormatInt16, libopusRefdecodeSingleFormatInt24} {
				refReader, err := decodeWithLibopusFixedPhaseControl(sampleFormat, sampleRate, channels, frameSize, phaseDisabled, packets)
				if err != nil {
					libopustest.HelperUnavailable(t, "fixed reference phase-control decode", err)
					return
				}
				count := refReader.Count(-1)
				if count != len(packets)*frameSize*channels {
					t.Fatalf("reference samples=%d want %d", count, len(packets)*frameSize*channels)
				}
				want16 := make([]int16, count)
				want24 := make([]int32, count)
				if sampleFormat == libopusRefdecodeSingleFormatInt16 {
					refReader.ExpectRemaining(count * 2)
					for i := range want16 {
						want16[i] = refReader.I16()
					}
				} else {
					refReader.ExpectRemaining(count * 4)
					for i := range want24 {
						want24[i] = refReader.I32()
					}
				}
				if err := refReader.ExpectConsumed(); err != nil {
					t.Fatal(err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatal(err)
				}
				if dec.PhaseInversionDisabled() {
					t.Fatal("stereo decoder default disables phase inversion")
				}
				if phaseDisabled {
					// Set before the fixed CELT sidecar exists; its lazy creation
					// must inherit the public decoder control.
					dec.SetPhaseInversionDisabled(true)
				}
				got16 := make([]int16, 0, count)
				got24 := make([]int32, 0, count)
				out16 := make([]int16, frameSize*channels)
				out24 := make([]int32, frameSize*channels)
				for p, packet := range packets {
					if sampleFormat == libopusRefdecodeSingleFormatInt16 {
						if n, err := dec.DecodeInt16(packet, out16); err != nil || n != frameSize {
							t.Fatalf("packet %d DecodeInt16 samples=%d err=%v", p, n, err)
						}
						got16 = append(got16, out16...)
					} else {
						if n, err := dec.DecodeInt24(packet, out24); err != nil || n != frameSize {
							t.Fatalf("packet %d DecodeInt24 samples=%d err=%v", p, n, err)
						}
						got24 = append(got24, out24...)
					}
				}
				if sampleFormat == libopusRefdecodeSingleFormatInt16 {
					assertFixedExact(t, "phase-control int16", int16ToInt32(got16), int16ToInt32(want16))
				} else {
					assertFixedExact(t, "phase-control int24", got24, want24)
				}

				// A live control update reaches an already initialized Q15 CELT
				// decoder. Reset clears codec history but preserves the control,
				// as OPUS_RESET_STATE does for disable_inv.
				dec.SetPhaseInversionDisabled(!phaseDisabled)
				dec.Reset()
				if got := dec.PhaseInversionDisabled(); got == phaseDisabled {
					t.Fatalf("phase-inversion setting after Reset=%t, want %t", got, !phaseDisabled)
				}
				refReader, err = decodeWithLibopusFixedPhaseControl(sampleFormat, sampleRate, channels, frameSize, !phaseDisabled, packets)
				if err != nil {
					libopustest.HelperUnavailable(t, "fixed reference phase-control reset decode", err)
					return
				}
				count = refReader.Count(-1)
				if sampleFormat == libopusRefdecodeSingleFormatInt16 {
					refReader.ExpectRemaining(count * 2)
					for i := range want16 {
						want16[i] = refReader.I16()
					}
				} else {
					refReader.ExpectRemaining(count * 4)
					for i := range want24 {
						want24[i] = refReader.I32()
					}
				}
				if err := refReader.ExpectConsumed(); err != nil {
					t.Fatal(err)
				}
				if sampleFormat == libopusRefdecodeSingleFormatInt16 {
					got16 = got16[:0]
					for p, packet := range packets {
						if n, err := dec.DecodeInt16(packet, out16); err != nil || n != frameSize {
							t.Fatalf("post-reset packet %d DecodeInt16 samples=%d err=%v", p, n, err)
						}
						got16 = append(got16, out16...)
					}
					assertFixedExact(t, "post-reset phase-control int16", int16ToInt32(got16), int16ToInt32(want16))
				} else {
					got24 = got24[:0]
					for p, packet := range packets {
						if n, err := dec.DecodeInt24(packet, out24); err != nil || n != frameSize {
							t.Fatalf("post-reset packet %d DecodeInt24 samples=%d err=%v", p, n, err)
						}
						got24 = append(got24, out24...)
					}
					assertFixedExact(t, "post-reset phase-control int24", got24, want24)
				}

				// Measure the warmed fixed-point CELT path with caller-owned
				// output storage; helper setup and reset are outside the run.
				allocs := testing.AllocsPerRun(20, func() {
					if sampleFormat == libopusRefdecodeSingleFormatInt16 {
						if _, err := dec.DecodeInt16(packets[len(packets)-1], out16); err != nil {
							panic(err)
						}
					} else if _, err := dec.DecodeInt24(packets[len(packets)-1], out24); err != nil {
						panic(err)
					}
				})
				if allocs != 0 {
					t.Fatalf("warmed phase-control decode allocated %g times/run", allocs)
				}
			}
		})
	}
}
