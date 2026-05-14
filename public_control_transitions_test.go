package gopus

import (
	"errors"
	"testing"
)

func TestPublicEncoderControlTransitionContracts(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)

	requireControlEqual(t, "application", enc.Application(), ApplicationAudio)
	requireControlEqual(t, "sample_rate", enc.SampleRate(), 48000)
	requireControlEqual(t, "channels", enc.Channels(), 2)
	requireControlEqual(t, "frame_size", enc.FrameSize(), 960)
	requireControlEqual(t, "expert_frame_duration", enc.ExpertFrameDuration(), ExpertFrameDurationArg)
	requireControlEqual(t, "bitrate", enc.Bitrate(), 120000)
	requireControlEqual(t, "complexity", enc.Complexity(), 9)
	requireControlEqual(t, "bitrate_mode", enc.BitrateMode(), BitrateModeCVBR)
	requireControlEqual(t, "vbr", enc.VBR(), true)
	requireControlEqual(t, "vbr_constraint", enc.VBRConstraint(), true)
	requireControlEqual(t, "fec", enc.FECEnabled(), false)
	requireControlEqual(t, "dtx", enc.DTXEnabled(), false)
	requireControlEqual(t, "packet_loss", enc.PacketLoss(), 0)
	requireControlEqual(t, "force_channels", enc.ForceChannels(), -1)
	requireControlEqual(t, "signal", enc.Signal(), SignalAuto)
	requireControlEqual(t, "bandwidth", enc.Bandwidth(), BandwidthFullband)
	requireControlEqual(t, "max_bandwidth", enc.MaxBandwidth(), BandwidthFullband)
	requireControlEqual(t, "lsb_depth", enc.LSBDepth(), 24)
	requireControlEqual(t, "prediction_disabled", enc.PredictionDisabled(), false)
	requireControlEqual(t, "phase_inversion_disabled", enc.PhaseInversionDisabled(), false)
	requireControlEqual(t, "lookahead", enc.Lookahead(), 312)

	enc.SetVBRConstraint(false)
	requireControlEqual(t, "bitrate_mode after unconstrained VBR", enc.BitrateMode(), BitrateModeVBR)
	enc.SetVBR(false)
	requireControlEqual(t, "bitrate_mode after SetVBR(false)", enc.BitrateMode(), BitrateModeCBR)
	requireControlEqual(t, "remembered unconstrained VBR", enc.VBRConstraint(), false)
	enc.SetVBRConstraint(true)
	requireControlEqual(t, "bitrate_mode while VBR disabled", enc.BitrateMode(), BitrateModeCBR)
	requireControlEqual(t, "remembered constrained VBR", enc.VBRConstraint(), true)
	enc.SetVBR(true)
	requireControlEqual(t, "bitrate_mode after SetVBR(true)", enc.BitrateMode(), BitrateModeCVBR)

	requireNoControlError(t, enc.SetBitrateMode(BitrateModeVBR), "SetBitrateMode(VBR)")
	requireControlEqual(t, "VBR flag after BitrateModeVBR", enc.VBR(), true)
	requireControlEqual(t, "constraint after BitrateModeVBR", enc.VBRConstraint(), false)
	requireNoControlError(t, enc.SetBitrateMode(BitrateModeCBR), "SetBitrateMode(CBR)")
	requireControlEqual(t, "VBR flag after BitrateModeCBR", enc.VBR(), false)
	requireNoControlError(t, enc.SetBitrateMode(BitrateModeCVBR), "SetBitrateMode(CVBR)")
	requireControlEqual(t, "constraint after BitrateModeCVBR", enc.VBRConstraint(), true)

	requireNoControlError(t, enc.SetExpertFrameDuration(ExpertFrameDuration10Ms), "SetExpertFrameDuration(10ms)")
	requireControlEqual(t, "frame size after 10ms", enc.FrameSize(), 480)
	requireNoControlError(t, enc.SetExpertFrameDuration(ExpertFrameDurationArg), "SetExpertFrameDuration(arg)")
	requireControlEqual(t, "frame size after arg duration", enc.FrameSize(), 480)

	requireNoControlError(t, enc.SetApplication(ApplicationVoIP), "SetApplication(VoIP)")
	requireControlEqual(t, "application after VoIP", enc.Application(), ApplicationVoIP)
	requireControlEqual(t, "VoIP lookahead", enc.Lookahead(), 312)
	requireControlEqual(t, "VoIP bandwidth policy", enc.Bandwidth(), BandwidthFullband)

	requireNoControlError(t, enc.SetApplication(ApplicationLowDelay), "SetApplication(LowDelay)")
	requireControlEqual(t, "application after LowDelay", enc.Application(), ApplicationLowDelay)
	requireControlEqual(t, "LowDelay lookahead", enc.Lookahead(), 120)
	requireControlEqual(t, "LowDelay bandwidth policy", enc.Bandwidth(), BandwidthFullband)

	packet := make([]byte, maxPacketBytesPerStream)
	pcm := publicCodecPCM(48000, enc.FrameSize(), enc.Channels(), 0, false)
	n, err := enc.Encode(pcm, packet)
	if err != nil {
		t.Fatalf("Encode after application transitions: %v", err)
	}
	requirePacketBasics(t, packet[:n], enc.Channels(), enc.FrameSize())
	if err := enc.SetApplication(ApplicationAudio); !errors.Is(err, ErrInvalidApplication) {
		t.Fatalf("SetApplication after first encode error=%v want %v", err, ErrInvalidApplication)
	}
	requireNoControlError(t, enc.SetApplication(ApplicationLowDelay), "SetApplication(same after encode)")
	enc.Reset()
	requireNoControlError(t, enc.SetApplication(ApplicationAudio), "SetApplication after Reset")
	requireControlEqual(t, "application after Reset change", enc.Application(), ApplicationAudio)

	restricted := mustNewTestEncoder(t, 48000, 1, ApplicationRestrictedSilk)
	if err := restricted.SetApplication(ApplicationAudio); !errors.Is(err, ErrInvalidApplication) {
		t.Fatalf("restricted SetApplication error=%v want %v", err, ErrInvalidApplication)
	}
}

func TestPublicEncoderExpertDurationEntryTransitions(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationRestrictedCelt)
	dec := mustNewTestDecoder(t, 48000, 1)

	tests := []struct {
		name      string
		duration  ExpertFrameDuration
		frameSize int
		entry     string
	}{
		{name: "arg_float32_buffer", duration: ExpertFrameDurationArg, frameSize: 960, entry: "float32_buffer"},
		{name: "2_5ms_float32_slice", duration: ExpertFrameDuration2_5Ms, frameSize: 120, entry: "float32_slice"},
		{name: "5ms_int16_buffer", duration: ExpertFrameDuration5Ms, frameSize: 240, entry: "int16_buffer"},
		{name: "10ms_int16_slice", duration: ExpertFrameDuration10Ms, frameSize: 480, entry: "int16_slice"},
		{name: "20ms_int24_buffer", duration: ExpertFrameDuration20Ms, frameSize: 960, entry: "int24_buffer"},
		{name: "40ms_int24_slice", duration: ExpertFrameDuration40Ms, frameSize: 1920, entry: "int24_slice"},
		{name: "60ms_float32_buffer", duration: ExpertFrameDuration60Ms, frameSize: 2880, entry: "float32_buffer"},
		{name: "80ms_int16_buffer", duration: ExpertFrameDuration80Ms, frameSize: 3840, entry: "int16_buffer"},
		{name: "100ms_int24_buffer", duration: ExpertFrameDuration100Ms, frameSize: 4800, entry: "int24_buffer"},
		{name: "120ms_float32_slice", duration: ExpertFrameDuration120Ms, frameSize: 5760, entry: "float32_slice"},
	}

	out := make([]float32, defaultMaxPacketSamples)
	for i, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			requireNoControlError(t, enc.SetExpertFrameDuration(tc.duration), "SetExpertFrameDuration")
			requireControlEqual(t, "expert duration", enc.ExpertFrameDuration(), tc.duration)
			requireControlEqual(t, "frame size", enc.FrameSize(), tc.frameSize)

			packet := encodePublicEntryPoint(t, enc, tc.entry, i)
			n, err := dec.Decode(packet, out)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			requireDecodedFloat32(t, out[:n], n, tc.frameSize, true)
		})
	}
}

func TestPublicEncoderReportsDTXStateFromSilence(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationVoIP)
	dec := mustNewTestDecoder(t, 48000, 1)
	requireNoControlError(t, enc.SetBitrate(16000), "SetBitrate")
	requireNoControlError(t, enc.SetBandwidth(BandwidthWideband), "SetBandwidth")
	requireNoControlError(t, enc.SetSignal(SignalVoice), "SetSignal")
	enc.SetDTX(true)

	packetBuf := make([]byte, maxPacketBytesPerStream)
	pcm := publicCodecPCM(48000, enc.FrameSize(), enc.Channels(), 0, true)
	var dtxPacket []byte
	for i := 0; i < 16; i++ {
		n, err := enc.Encode(pcm, packetBuf)
		if err != nil {
			t.Fatalf("Encode silence frame %d: %v", i, err)
		}
		if enc.InDTX() {
			dtxPacket = append([]byte(nil), packetBuf[:n]...)
			break
		}
	}
	if !enc.InDTX() {
		t.Fatal("encoder never entered DTX during silence")
	}
	if len(dtxPacket) != 1 {
		t.Fatalf("DTX packet length=%d want 1 byte", len(dtxPacket))
	}
	if toc := ParseTOC(dtxPacket[0]); toc.FrameCode != 0 {
		t.Fatalf("DTX packet frame code=%d want 0", toc.FrameCode)
	}

	out := make([]float32, defaultMaxPacketSamples)
	n, err := dec.Decode(dtxPacket, out)
	if err != nil {
		t.Fatalf("Decode DTX packet % X: %v", dtxPacket, err)
	}
	if n != enc.FrameSize() {
		t.Fatalf("Decode DTX samples=%d want %d", n, enc.FrameSize())
	}
	if !dec.InDTX() {
		t.Fatal("decoder InDTX()=false after DTX packet")
	}
	if got := dec.FinalRange(); got != 0 {
		t.Fatalf("decoder FinalRange()=0x%08X after DTX packet, want 0", got)
	}
}

func TestPublicEncoderForceChannelsChangesPacketShape(t *testing.T) {
	tests := []struct {
		name        string
		channels    int
		force       int
		application Application
		bandwidth   Bandwidth
		signal      Signal
		bitrate     int
		wantStereo  bool
	}{
		{
			name:        "stereo_input_forced_mono_silk",
			channels:    2,
			force:       1,
			application: ApplicationVoIP,
			bandwidth:   BandwidthWideband,
			signal:      SignalVoice,
			bitrate:     24000,
			wantStereo:  false,
		},
		{
			name:        "mono_input_forced_stereo_celt",
			channels:    1,
			force:       2,
			application: ApplicationLowDelay,
			bandwidth:   BandwidthFullband,
			signal:      SignalMusic,
			bitrate:     96000,
			wantStereo:  true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			enc := mustNewTestEncoder(t, 48000, tc.channels, tc.application)
			requireNoControlError(t, enc.SetBitrate(tc.bitrate), "SetBitrate")
			requireNoControlError(t, enc.SetBandwidth(tc.bandwidth), "SetBandwidth")
			requireNoControlError(t, enc.SetMaxBandwidth(tc.bandwidth), "SetMaxBandwidth")
			requireNoControlError(t, enc.SetSignal(tc.signal), "SetSignal")
			requireNoControlError(t, enc.SetForceChannels(tc.force), "SetForceChannels")

			packet := encodePublicEntryPoint(t, enc, "float32_buffer", 0)
			info, err := ParsePacket(packet)
			if err != nil {
				t.Fatalf("ParsePacket: %v", err)
			}
			if info.TOC.Stereo != tc.wantStereo {
				t.Fatalf("packet stereo=%v want %v", info.TOC.Stereo, tc.wantStereo)
			}

			decodeChannels := 1
			if tc.wantStereo {
				decodeChannels = 2
			}
			dec := mustNewTestDecoder(t, 48000, decodeChannels)
			out := make([]float32, defaultMaxPacketSamples*decodeChannels)
			n, err := dec.Decode(packet, out)
			if err != nil {
				t.Fatalf("Decode forced-channel packet: %v", err)
			}
			requireDecodedFloat32(t, out[:n*decodeChannels], n, enc.FrameSize(), true)
		})
	}
}

func TestPublicEncoderControlValidationRejectsBadValues(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)

	requireNoControlError(t, enc.SetBitrate(32000), "SetBitrate(valid)")
	requireControlError(t, enc.SetBitrate(5999), ErrInvalidBitrate, "SetBitrate(low)")
	requireControlError(t, enc.SetBitrate(510001), ErrInvalidBitrate, "SetBitrate(high)")
	requireControlEqual(t, "bitrate after invalid", enc.Bitrate(), 32000)

	requireNoControlError(t, enc.SetComplexity(5), "SetComplexity(valid)")
	requireControlError(t, enc.SetComplexity(-1), ErrInvalidComplexity, "SetComplexity(low)")
	requireControlError(t, enc.SetComplexity(11), ErrInvalidComplexity, "SetComplexity(high)")
	requireControlEqual(t, "complexity after invalid", enc.Complexity(), 5)

	requireNoControlError(t, enc.SetBitrateMode(BitrateModeCVBR), "SetBitrateMode(valid)")
	requireControlError(t, enc.SetBitrateMode(BitrateMode(99)), ErrInvalidBitrateMode, "SetBitrateMode(invalid)")
	requireControlEqual(t, "bitrate mode after invalid", enc.BitrateMode(), BitrateModeCVBR)

	requireNoControlError(t, enc.SetPacketLoss(12), "SetPacketLoss(valid)")
	requireControlError(t, enc.SetPacketLoss(-1), ErrInvalidPacketLoss, "SetPacketLoss(low)")
	requireControlError(t, enc.SetPacketLoss(101), ErrInvalidPacketLoss, "SetPacketLoss(high)")
	requireControlEqual(t, "packet loss after invalid", enc.PacketLoss(), 12)

	requireNoControlError(t, enc.SetSignal(SignalVoice), "SetSignal(valid)")
	requireControlError(t, enc.SetSignal(Signal(99)), ErrInvalidSignal, "SetSignal(invalid)")
	requireControlEqual(t, "signal after invalid", enc.Signal(), SignalVoice)

	requireNoControlError(t, enc.SetBandwidth(BandwidthWideband), "SetBandwidth(valid)")
	requireControlError(t, enc.SetBandwidth(Bandwidth(99)), ErrInvalidBandwidth, "SetBandwidth(invalid)")
	requireControlEqual(t, "bandwidth after invalid", enc.Bandwidth(), BandwidthWideband)

	requireNoControlError(t, enc.SetMaxBandwidth(BandwidthSuperwideband), "SetMaxBandwidth(valid)")
	requireControlError(t, enc.SetMaxBandwidth(Bandwidth(99)), ErrInvalidBandwidth, "SetMaxBandwidth(invalid)")
	requireControlEqual(t, "max bandwidth after invalid", enc.MaxBandwidth(), BandwidthSuperwideband)

	requireNoControlError(t, enc.SetForceChannels(1), "SetForceChannels(valid)")
	requireControlError(t, enc.SetForceChannels(0), ErrInvalidForceChannels, "SetForceChannels(zero)")
	requireControlError(t, enc.SetForceChannels(3), ErrInvalidForceChannels, "SetForceChannels(three)")
	requireControlEqual(t, "force channels after invalid", enc.ForceChannels(), 1)

	requireNoControlError(t, enc.SetLSBDepth(16), "SetLSBDepth(valid)")
	requireControlError(t, enc.SetLSBDepth(7), ErrInvalidLSBDepth, "SetLSBDepth(low)")
	requireControlError(t, enc.SetLSBDepth(25), ErrInvalidLSBDepth, "SetLSBDepth(high)")
	requireControlEqual(t, "lsb depth after invalid", enc.LSBDepth(), 16)

	requireNoControlError(t, enc.SetFrameSize(960), "SetFrameSize(valid)")
	requireControlError(t, enc.SetFrameSize(121), ErrInvalidFrameSize, "SetFrameSize(invalid)")
	requireControlEqual(t, "frame size after invalid", enc.FrameSize(), 960)

	requireNoControlError(t, enc.SetExpertFrameDuration(ExpertFrameDuration20Ms), "SetExpertFrameDuration(valid)")
	requireControlError(t, enc.SetExpertFrameDuration(ExpertFrameDuration(0)), ErrInvalidArgument, "SetExpertFrameDuration(invalid)")
	requireControlEqual(t, "expert duration after invalid", enc.ExpertFrameDuration(), ExpertFrameDuration20Ms)

	restricted := mustNewTestEncoder(t, 48000, 1, ApplicationRestrictedSilk)
	requireControlError(t, restricted.SetFrameSize(120), ErrInvalidFrameSize, "restricted silk 2.5ms frame")
	requireControlEqual(t, "restricted frame size after invalid", restricted.FrameSize(), 960)
}

func TestPublicDecoderControlAndFECTransitions(t *testing.T) {
	packet := publicEncodedPacket(t, ApplicationRestrictedCelt, 1, 960, func(t *testing.T, enc *Encoder) {
		t.Helper()
		requireNoControlError(t, enc.SetBitrate(96000), "SetBitrate")
	})

	dec := mustNewTestDecoder(t, 48000, 1)
	requireControlEqual(t, "sample_rate", dec.SampleRate(), 48000)
	requireControlEqual(t, "channels", dec.Channels(), 1)
	requireControlEqual(t, "default gain", dec.Gain(), 0)
	requireControlEqual(t, "default ignore extensions", dec.IgnoreExtensions(), false)
	requireControlEqual(t, "cold bandwidth", dec.Bandwidth(), BandwidthUnknown)
	requireControlEqual(t, "cold last packet duration", dec.LastPacketDuration(), 0)
	requireControlEqual(t, "cold final range", dec.FinalRange(), uint32(0))

	requireNoControlError(t, dec.SetGain(-32768), "SetGain(min)")
	requireNoControlError(t, dec.SetGain(32767), "SetGain(max)")
	requireControlError(t, dec.SetGain(-32769), ErrInvalidGain, "SetGain(low)")
	requireControlError(t, dec.SetGain(32768), ErrInvalidGain, "SetGain(high)")
	requireControlEqual(t, "gain after invalid", dec.Gain(), 32767)
	requireNoControlError(t, dec.SetGain(0), "SetGain(reset)")
	dec.SetIgnoreExtensions(true)
	requireControlEqual(t, "ignore extensions", dec.IgnoreExtensions(), true)

	pcm := make([]float32, defaultMaxPacketSamples)
	n, err := dec.Decode(packet, pcm)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	requireDecodedFloat32(t, pcm[:n], n, 960, true)
	requireControlEqual(t, "last packet duration after decode", dec.LastPacketDuration(), 960)
	requireControlEqual(t, "bandwidth after decode", dec.Bandwidth(), BandwidthFullband)
	if dec.FinalRange() == 0 {
		t.Fatal("FinalRange after decode is zero")
	}
	if dec.Pitch() < 0 {
		t.Fatalf("Pitch()=%d want non-negative", dec.Pitch())
	}

	n, err = dec.DecodeWithFEC(packet, pcm, false)
	if err != nil {
		t.Fatalf("DecodeWithFEC(false): %v", err)
	}
	requireDecodedFloat32(t, pcm[:n], n, 960, true)

	n, err = dec.DecodeWithFEC(packet, pcm, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC(true): %v", err)
	}
	requireDecodedFloat32(t, pcm[:n], n, 960, false)

	pcm16 := make([]int16, defaultMaxPacketSamples)
	n16, err := dec.DecodeInt16(nil, pcm16)
	if err != nil {
		t.Fatalf("DecodeInt16 PLC: %v", err)
	}
	if n16 != 960 {
		t.Fatalf("DecodeInt16 PLC samples=%d want 960", n16)
	}

	dec.Reset()
	requireControlEqual(t, "gain after reset", dec.Gain(), 0)
	requireControlEqual(t, "ignore extensions after reset", dec.IgnoreExtensions(), true)
	requireControlEqual(t, "final range after reset", dec.FinalRange(), uint32(0))
	requireControlEqual(t, "InDTX after reset", dec.InDTX(), false)
}

func TestPublicMultistreamControlPropagation(t *testing.T) {
	enc := mustNewDefaultMultistreamEncoder(t, 48000, 2, ApplicationAudio)
	dec := mustNewDefaultMultistreamDecoder(t, 48000, 2)

	requireControlEqual(t, "streams", enc.Streams(), 1)
	requireControlEqual(t, "coupled streams", enc.CoupledStreams(), 1)
	requireControlEqual(t, "complexity default", enc.Complexity(), 9)
	requireControlEqual(t, "bitrate mode default", enc.BitrateMode(), BitrateModeCVBR)

	requireNoControlError(t, enc.SetBitrate(96000), "SetBitrate")
	requireNoControlError(t, enc.SetComplexity(6), "SetComplexity")
	requireNoControlError(t, enc.SetBitrateMode(BitrateModeVBR), "SetBitrateMode")
	requireNoControlError(t, enc.SetExpertFrameDuration(ExpertFrameDuration20Ms), "SetExpertFrameDuration")
	requireNoControlError(t, enc.SetBandwidth(BandwidthFullband), "SetBandwidth")
	requireNoControlError(t, enc.SetMaxBandwidth(BandwidthFullband), "SetMaxBandwidth")
	requireNoControlError(t, enc.SetForceChannels(2), "SetForceChannels")
	requireNoControlError(t, enc.SetLSBDepth(16), "SetLSBDepth")
	requireNoControlError(t, enc.SetPacketLoss(15), "SetPacketLoss")
	requireNoControlError(t, enc.SetSignal(SignalMusic), "SetSignal")
	enc.SetFEC(true)
	enc.SetDTX(true)
	enc.SetPredictionDisabled(true)
	enc.SetPhaseInversionDisabled(true)

	checks := []struct {
		name string
		got  any
		want any
	}{
		{name: "bitrate", got: enc.Bitrate(), want: 96000},
		{name: "complexity", got: enc.Complexity(), want: 6},
		{name: "bitrate_mode", got: enc.BitrateMode(), want: BitrateModeVBR},
		{name: "frame_size", got: enc.FrameSize(), want: 960},
		{name: "expert_frame_duration", got: enc.ExpertFrameDuration(), want: ExpertFrameDuration20Ms},
		{name: "bandwidth", got: enc.Bandwidth(), want: BandwidthFullband},
		{name: "max_bandwidth", got: enc.MaxBandwidth(), want: BandwidthFullband},
		{name: "force_channels", got: enc.ForceChannels(), want: 2},
		{name: "lsb_depth", got: enc.LSBDepth(), want: 16},
		{name: "packet_loss", got: enc.PacketLoss(), want: 15},
		{name: "signal", got: enc.Signal(), want: SignalMusic},
		{name: "fec", got: enc.FECEnabled(), want: true},
		{name: "dtx", got: enc.DTXEnabled(), want: true},
		{name: "prediction_disabled", got: enc.PredictionDisabled(), want: true},
		{name: "phase_inversion_disabled", got: enc.PhaseInversionDisabled(), want: true},
	}
	for _, check := range checks {
		if check.got != check.want {
			t.Fatalf("%s=%v want %v", check.name, check.got, check.want)
		}
	}

	packet := make([]byte, maxPacketBytesPerStream*enc.Streams())
	pcm := publicCodecPCM(48000, enc.FrameSize(), enc.Channels(), 0, false)
	nPacket, err := enc.Encode(pcm, packet)
	if err != nil {
		t.Fatalf("multistream Encode: %v", err)
	}
	if nPacket == 0 {
		t.Fatal("multistream Encode returned an empty packet")
	}
	out := make([]float32, defaultMaxPacketSamples*enc.Channels())
	nPCM, err := dec.Decode(packet[:nPacket], out)
	if err != nil {
		t.Fatalf("multistream Decode: %v", err)
	}
	requireDecodedFloat32(t, out[:nPCM*enc.Channels()], nPCM, enc.FrameSize(), true)
	if enc.FinalRange() == 0 {
		t.Fatal("multistream FinalRange after encode is zero")
	}

	if err := enc.SetApplication(ApplicationLowDelay); !errors.Is(err, ErrInvalidApplication) {
		t.Fatalf("multistream SetApplication after encode error=%v want %v", err, ErrInvalidApplication)
	}
	enc.Reset()
	requireNoControlError(t, enc.SetApplication(ApplicationLowDelay), "multistream SetApplication after Reset")
	requireControlEqual(t, "multistream lowdelay lookahead", enc.Lookahead(), 120)

	dec.SetIgnoreExtensions(true)
	dec.Reset()
	requireControlEqual(t, "multistream decoder ignore after reset", dec.IgnoreExtensions(), true)
	requireControlEqual(t, "multistream decoder last frame after reset", dec.lastFrameSize, 960)
}

func TestPublicMultistreamControlValidationRejectsBadValues(t *testing.T) {
	enc := mustNewDefaultMultistreamEncoder(t, 48000, 3, ApplicationAudio)

	requireNoControlError(t, enc.SetBitrate(96000), "SetBitrate(valid)")
	requireControlError(t, enc.SetBitrate(5999), ErrInvalidBitrate, "SetBitrate(low)")
	requireControlError(t, enc.SetBitrate(510000*enc.Channels()+1), ErrInvalidBitrate, "SetBitrate(high)")
	requireControlEqual(t, "bitrate after invalid", enc.Bitrate(), 96000)

	requireNoControlError(t, enc.SetComplexity(4), "SetComplexity(valid)")
	requireControlError(t, enc.SetComplexity(-1), ErrInvalidComplexity, "SetComplexity(low)")
	requireControlError(t, enc.SetComplexity(11), ErrInvalidComplexity, "SetComplexity(high)")
	requireControlEqual(t, "complexity after invalid", enc.Complexity(), 4)

	requireNoControlError(t, enc.SetBitrateMode(BitrateModeCVBR), "SetBitrateMode(valid)")
	requireControlError(t, enc.SetBitrateMode(BitrateMode(99)), ErrInvalidBitrateMode, "SetBitrateMode(invalid)")
	requireControlEqual(t, "bitrate mode after invalid", enc.BitrateMode(), BitrateModeCVBR)

	enc.SetVBRConstraint(false)
	requireControlEqual(t, "multistream bitrate mode after unconstrained VBR", enc.BitrateMode(), BitrateModeVBR)
	enc.SetVBR(false)
	requireControlEqual(t, "multistream bitrate mode after SetVBR(false)", enc.BitrateMode(), BitrateModeCBR)
	requireControlEqual(t, "multistream remembered unconstrained VBR", enc.VBRConstraint(), false)
	enc.SetVBRConstraint(true)
	enc.SetVBR(true)
	requireControlEqual(t, "multistream bitrate mode after constrained VBR", enc.BitrateMode(), BitrateModeCVBR)

	requireNoControlError(t, enc.SetPacketLoss(18), "SetPacketLoss(valid)")
	requireControlError(t, enc.SetPacketLoss(-1), ErrInvalidPacketLoss, "SetPacketLoss(low)")
	requireControlError(t, enc.SetPacketLoss(101), ErrInvalidPacketLoss, "SetPacketLoss(high)")
	requireControlEqual(t, "packet loss after invalid", enc.PacketLoss(), 18)

	requireNoControlError(t, enc.SetSignal(SignalVoice), "SetSignal(valid)")
	requireControlError(t, enc.SetSignal(Signal(99)), ErrInvalidSignal, "SetSignal(invalid)")
	requireControlEqual(t, "signal after invalid", enc.Signal(), SignalVoice)

	requireNoControlError(t, enc.SetBandwidth(BandwidthWideband), "SetBandwidth(valid)")
	requireControlError(t, enc.SetBandwidth(Bandwidth(99)), ErrInvalidBandwidth, "SetBandwidth(invalid)")
	requireControlEqual(t, "bandwidth after invalid", enc.Bandwidth(), BandwidthWideband)

	requireNoControlError(t, enc.SetMaxBandwidth(BandwidthSuperwideband), "SetMaxBandwidth(valid)")
	requireControlError(t, enc.SetMaxBandwidth(Bandwidth(99)), ErrInvalidBandwidth, "SetMaxBandwidth(invalid)")
	requireControlEqual(t, "max bandwidth after invalid", enc.MaxBandwidth(), BandwidthSuperwideband)

	requireNoControlError(t, enc.SetForceChannels(1), "SetForceChannels(valid)")
	requireControlError(t, enc.SetForceChannels(0), ErrInvalidForceChannels, "SetForceChannels(zero)")
	requireControlError(t, enc.SetForceChannels(3), ErrInvalidForceChannels, "SetForceChannels(three)")
	requireControlEqual(t, "force channels after invalid", enc.ForceChannels(), 1)

	requireNoControlError(t, enc.SetLSBDepth(16), "SetLSBDepth(valid)")
	requireControlError(t, enc.SetLSBDepth(7), ErrInvalidLSBDepth, "SetLSBDepth(low)")
	requireControlError(t, enc.SetLSBDepth(25), ErrInvalidLSBDepth, "SetLSBDepth(high)")
	requireControlEqual(t, "lsb depth after invalid", enc.LSBDepth(), 16)

	requireNoControlError(t, enc.SetFrameSize(960), "SetFrameSize(valid)")
	requireControlError(t, enc.SetFrameSize(121), ErrInvalidFrameSize, "SetFrameSize(invalid)")
	requireControlEqual(t, "frame size after invalid", enc.FrameSize(), 960)

	requireNoControlError(t, enc.SetExpertFrameDuration(ExpertFrameDuration20Ms), "SetExpertFrameDuration(valid)")
	requireControlError(t, enc.SetExpertFrameDuration(ExpertFrameDuration(0)), ErrInvalidArgument, "SetExpertFrameDuration(invalid)")
	requireControlEqual(t, "expert duration after invalid", enc.ExpertFrameDuration(), ExpertFrameDuration20Ms)
}

func TestPublicMultistreamResetKeepsConfiguration(t *testing.T) {
	enc := mustNewDefaultMultistreamEncoder(t, 48000, 2, ApplicationAudio)
	requireNoControlError(t, enc.SetBitrate(128000), "SetBitrate")
	requireNoControlError(t, enc.SetComplexity(7), "SetComplexity")
	requireNoControlError(t, enc.SetBitrateMode(BitrateModeVBR), "SetBitrateMode")
	requireNoControlError(t, enc.SetExpertFrameDuration(ExpertFrameDuration20Ms), "SetExpertFrameDuration")
	requireNoControlError(t, enc.SetBandwidth(BandwidthWideband), "SetBandwidth")
	requireNoControlError(t, enc.SetMaxBandwidth(BandwidthWideband), "SetMaxBandwidth")
	requireNoControlError(t, enc.SetForceChannels(1), "SetForceChannels")
	requireNoControlError(t, enc.SetLSBDepth(12), "SetLSBDepth")
	requireNoControlError(t, enc.SetPacketLoss(11), "SetPacketLoss")
	requireNoControlError(t, enc.SetSignal(SignalMusic), "SetSignal")
	enc.SetFEC(true)
	enc.SetDTX(true)
	enc.SetPredictionDisabled(true)
	enc.SetPhaseInversionDisabled(true)
	enc.SetVBRConstraint(false)

	packet := encodePublicMultistreamEntryPoint(t, enc, "float32_buffer", 0)
	if len(packet) == 0 {
		t.Fatal("multistream Encode returned an empty packet")
	}

	enc.Reset()
	checks := []struct {
		name string
		got  any
		want any
	}{
		{name: "application", got: enc.Application(), want: ApplicationAudio},
		{name: "bitrate", got: enc.Bitrate(), want: 128000},
		{name: "complexity", got: enc.Complexity(), want: 7},
		{name: "bitrate_mode", got: enc.BitrateMode(), want: BitrateModeVBR},
		{name: "expert_frame_duration", got: enc.ExpertFrameDuration(), want: ExpertFrameDuration20Ms},
		{name: "frame_size", got: enc.FrameSize(), want: 960},
		{name: "bandwidth", got: enc.Bandwidth(), want: BandwidthWideband},
		{name: "max_bandwidth", got: enc.MaxBandwidth(), want: BandwidthWideband},
		{name: "force_channels", got: enc.ForceChannels(), want: 1},
		{name: "lsb_depth", got: enc.LSBDepth(), want: 12},
		{name: "packet_loss", got: enc.PacketLoss(), want: 11},
		{name: "signal", got: enc.Signal(), want: SignalMusic},
		{name: "fec", got: enc.FECEnabled(), want: true},
		{name: "dtx", got: enc.DTXEnabled(), want: true},
		{name: "prediction_disabled", got: enc.PredictionDisabled(), want: true},
		{name: "phase_inversion_disabled", got: enc.PhaseInversionDisabled(), want: true},
		{name: "vbr_constraint", got: enc.VBRConstraint(), want: false},
	}
	for _, check := range checks {
		if check.got != check.want {
			t.Fatalf("%s after Reset=%v want %v", check.name, check.got, check.want)
		}
	}
}

func TestPublicMultistreamEncodeEntryPointsDecode(t *testing.T) {
	entries := []string{
		"float32_buffer",
		"float32_slice",
		"int16_buffer",
		"int16_slice",
		"int24_buffer",
		"int24_slice",
	}
	for i, entry := range entries {
		entry := entry
		t.Run(entry, func(t *testing.T) {
			enc := mustNewDefaultMultistreamEncoder(t, 48000, 3, ApplicationAudio)
			dec := mustNewDefaultMultistreamDecoder(t, 48000, 3)
			requireNoControlError(t, enc.SetFrameSize(480), "SetFrameSize")

			packet := encodePublicMultistreamEntryPoint(t, enc, entry, i)
			out := make([]float32, defaultMaxPacketSamples*enc.Channels())
			n, err := dec.Decode(packet, out)
			if err != nil {
				t.Fatalf("multistream Decode: %v", err)
			}
			requireDecodedFloat32(t, out[:n*enc.Channels()], n, enc.FrameSize(), true)
		})
	}
}

func encodePublicEntryPoint(t *testing.T, enc *Encoder, entry string, frameIndex int) []byte {
	t.Helper()

	data := make([]byte, maxPacketBytesPerStream)
	switch entry {
	case "float32_buffer":
		pcm := publicCodecPCM(48000, enc.FrameSize(), enc.Channels(), frameIndex, false)
		n, err := enc.Encode(pcm, data)
		requireNoControlError(t, err, "Encode")
		return append([]byte(nil), data[:n]...)
	case "float32_slice":
		pcm := publicCodecPCM(48000, enc.FrameSize(), enc.Channels(), frameIndex, false)
		packet, err := enc.EncodeFloat32(pcm)
		requireNoControlError(t, err, "EncodeFloat32")
		return packet
	case "int16_buffer":
		pcm := publicCodecPCMInt16(48000, enc.FrameSize(), enc.Channels(), frameIndex)
		n, err := enc.EncodeInt16(pcm, data)
		requireNoControlError(t, err, "EncodeInt16")
		return append([]byte(nil), data[:n]...)
	case "int16_slice":
		pcm := publicCodecPCMInt16(48000, enc.FrameSize(), enc.Channels(), frameIndex)
		packet, err := enc.EncodeInt16Slice(pcm)
		requireNoControlError(t, err, "EncodeInt16Slice")
		return packet
	case "int24_buffer":
		pcm := publicCodecPCMInt24(48000, enc.FrameSize(), enc.Channels(), frameIndex)
		n, err := enc.EncodeInt24(pcm, data)
		requireNoControlError(t, err, "EncodeInt24")
		return append([]byte(nil), data[:n]...)
	case "int24_slice":
		pcm := publicCodecPCMInt24(48000, enc.FrameSize(), enc.Channels(), frameIndex)
		packet, err := enc.EncodeInt24Slice(pcm)
		requireNoControlError(t, err, "EncodeInt24Slice")
		return packet
	default:
		t.Fatalf("unknown entry point %q", entry)
		return nil
	}
}

func encodePublicMultistreamEntryPoint(t *testing.T, enc *MultistreamEncoder, entry string, frameIndex int) []byte {
	t.Helper()

	data := make([]byte, maxPacketBytesPerStream*enc.Streams())
	switch entry {
	case "float32_buffer":
		pcm := publicCodecPCM(48000, enc.FrameSize(), enc.Channels(), frameIndex, false)
		n, err := enc.Encode(pcm, data)
		requireNoControlError(t, err, "MultistreamEncoder.Encode")
		return append([]byte(nil), data[:n]...)
	case "float32_slice":
		pcm := publicCodecPCM(48000, enc.FrameSize(), enc.Channels(), frameIndex, false)
		packet, err := enc.EncodeFloat32(pcm)
		requireNoControlError(t, err, "MultistreamEncoder.EncodeFloat32")
		return packet
	case "int16_buffer":
		pcm := publicCodecPCMInt16(48000, enc.FrameSize(), enc.Channels(), frameIndex)
		n, err := enc.EncodeInt16(pcm, data)
		requireNoControlError(t, err, "MultistreamEncoder.EncodeInt16")
		return append([]byte(nil), data[:n]...)
	case "int16_slice":
		pcm := publicCodecPCMInt16(48000, enc.FrameSize(), enc.Channels(), frameIndex)
		packet, err := enc.EncodeInt16Slice(pcm)
		requireNoControlError(t, err, "MultistreamEncoder.EncodeInt16Slice")
		return packet
	case "int24_buffer":
		pcm := publicCodecPCMInt24(48000, enc.FrameSize(), enc.Channels(), frameIndex)
		n, err := enc.EncodeInt24(pcm, data)
		requireNoControlError(t, err, "MultistreamEncoder.EncodeInt24")
		return append([]byte(nil), data[:n]...)
	case "int24_slice":
		pcm := publicCodecPCMInt24(48000, enc.FrameSize(), enc.Channels(), frameIndex)
		packet, err := enc.EncodeInt24Slice(pcm)
		requireNoControlError(t, err, "MultistreamEncoder.EncodeInt24Slice")
		return packet
	default:
		t.Fatalf("unknown multistream entry point %q", entry)
		return nil
	}
}

func publicCodecPCMInt16(sampleRate, frameSize, channels, frameIndex int) []int16 {
	floatPCM := publicCodecPCM(sampleRate, frameSize, channels, frameIndex, false)
	pcm := make([]int16, len(floatPCM))
	for i, v := range floatPCM {
		pcm[i] = int16(v * 32767)
	}
	return pcm
}

func publicCodecPCMInt24(sampleRate, frameSize, channels, frameIndex int) []int32 {
	floatPCM := publicCodecPCM(sampleRate, frameSize, channels, frameIndex, false)
	pcm := make([]int32, len(floatPCM))
	for i, v := range floatPCM {
		pcm[i] = int32(v * 8388607)
	}
	return pcm
}

func requireControlEqual[T comparable](t *testing.T, name string, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("%s=%v want %v", name, got, want)
	}
}

func requireNoControlError(t *testing.T, err error, name string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func requireControlError(t *testing.T, err, want error, name string) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s error=%v want %v", name, err, want)
	}
}
