package gopus

import (
	"errors"
	"fmt"
	"math"
	"testing"
)

// generateSineWaveFloat32 generates a sine wave at the given frequency.
func generateSineWaveFloat32(sampleRate int, freq float64, samples int, channels int) []float32 {
	pcm := make([]float32, samples*channels)
	for i := 0; i < samples; i++ {
		val := float32(0.5 * math.Sin(2*math.Pi*freq*float64(i)/float64(sampleRate)))
		for ch := 0; ch < channels; ch++ {
			pcm[i*channels+ch] = val
		}
	}
	return pcm
}

// generateSineWaveInt16 generates a sine wave as int16.
func generateSineWaveInt16(sampleRate int, freq float64, samples int, channels int) []int16 {
	pcm := make([]int16, samples*channels)
	for i := 0; i < samples; i++ {
		val := int16(16384 * math.Sin(2*math.Pi*freq*float64(i)/float64(sampleRate)))
		for ch := 0; ch < channels; ch++ {
			pcm[i*channels+ch] = val
		}
	}
	return pcm
}

func TestPublicAPIRoundTripBasics(t *testing.T) {
	tests := []struct {
		name       string
		sampleRate int
		channels   int
		format     string
	}{
		{name: "float32_8k_mono", sampleRate: 8000, channels: 1, format: "float32"},
		{name: "float32_12k_mono", sampleRate: 12000, channels: 1, format: "float32"},
		{name: "float32_16k_mono", sampleRate: 16000, channels: 1, format: "float32"},
		{name: "float32_24k_mono", sampleRate: 24000, channels: 1, format: "float32"},
		{name: "float32_48k_mono", sampleRate: 48000, channels: 1, format: "float32"},
		{name: "float32_48k_stereo", sampleRate: 48000, channels: 2, format: "float32"},
		{name: "int16_48k_mono", sampleRate: 48000, channels: 1, format: "int16"},
		{name: "int16_48k_stereo", sampleRate: 48000, channels: 2, format: "int16"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			enc := mustNewTestEncoder(t, tc.sampleRate, tc.channels, ApplicationAudio)
			dec := mustNewTestDecoder(t, tc.sampleRate, tc.channels)

			const frameSize = 960
			switch tc.format {
			case "float32":
				pcmIn := publicCodecPCM(tc.sampleRate, frameSize, tc.channels, 0, false)
				packet, err := enc.EncodeFloat32(pcmIn)
				if err != nil {
					t.Fatalf("EncodeFloat32: %v", err)
				}
				requirePacketBasics(t, packet, tc.channels, frameSize)

				pcmOut := make([]float32, defaultMaxPacketSamples*tc.channels)
				n, err := dec.Decode(packet, pcmOut)
				if err != nil {
					t.Fatalf("Decode: %v", err)
				}
				requireDecodedFloat32(t, pcmOut[:n*tc.channels], n, frameSize, true)
			case "int16":
				pcmIn := generateSineWaveInt16(tc.sampleRate, 440, frameSize, tc.channels)
				packet, err := enc.EncodeInt16Slice(pcmIn)
				if err != nil {
					t.Fatalf("EncodeInt16Slice: %v", err)
				}
				requirePacketBasics(t, packet, tc.channels, frameSize)

				pcmOut := make([]int16, defaultMaxPacketSamples*tc.channels)
				n, err := dec.DecodeInt16(packet, pcmOut)
				if err != nil {
					t.Fatalf("DecodeInt16: %v", err)
				}
				requireDecodedInt16(t, pcmOut[:n*tc.channels], n, frameSize)
			default:
				t.Fatalf("unknown format %q", tc.format)
			}
		})
	}
}

func TestPublicAPISequentialDecodeAndPLC(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)
	dec := mustNewTestDecoder(t, 48000, 1)

	packet := make([]byte, maxPacketBytesPerStream)
	pcmOut := make([]float32, defaultMaxPacketSamples)

	for i := 0; i < 6; i++ {
		pcmIn := publicCodecPCM(48000, enc.FrameSize(), 1, i, false)
		nPacket, err := enc.Encode(pcmIn, packet)
		if err != nil {
			t.Fatalf("frame %d Encode: %v", i, err)
		}
		requirePacketBasics(t, packet[:nPacket], 1, enc.FrameSize())

		nPCM, err := dec.Decode(packet[:nPacket], pcmOut)
		if err != nil {
			t.Fatalf("frame %d Decode: %v", i, err)
		}
		requireDecodedFloat32(t, pcmOut[:nPCM], nPCM, enc.FrameSize(), true)
	}

	for _, lost := range []struct {
		name string
		data []byte
	}{
		{name: "nil", data: nil},
		{name: "empty", data: []byte{}},
	} {
		t.Run("plc_"+lost.name, func(t *testing.T) {
			n, err := dec.Decode(lost.data, pcmOut)
			if err != nil {
				t.Fatalf("Decode PLC: %v", err)
			}
			requireDecodedFloat32(t, pcmOut[:n], n, enc.FrameSize(), false)
		})
	}
}

func TestPublicAPIBufferAndLimitContracts(t *testing.T) {
	if _, err := NewDecoder(DecoderConfig{SampleRate: 48000, Channels: 1, MaxPacketSamples: -1}); !errors.Is(err, ErrInvalidMaxPacketSamples) {
		t.Fatalf("NewDecoder invalid max samples error=%v want=%v", err, ErrInvalidMaxPacketSamples)
	}
	if _, err := NewDecoder(DecoderConfig{SampleRate: 48000, Channels: 1, MaxPacketBytes: -1}); !errors.Is(err, ErrInvalidMaxPacketBytes) {
		t.Fatalf("NewDecoder invalid max bytes error=%v want=%v", err, ErrInvalidMaxPacketBytes)
	}

	enc := mustNewTestEncoder(t, 48000, 1, ApplicationRestrictedCelt)
	pcm := publicCodecPCM(48000, enc.FrameSize(), 1, 0, false)
	if _, err := enc.Encode(pcm, nil); !errors.Is(err, ErrBufferTooSmall) {
		t.Fatalf("Encode into nil buffer error=%v want=%v", err, ErrBufferTooSmall)
	}

	packet := publicEncodedPacket(t, ApplicationRestrictedCelt, 1, 960, nil)
	decSmallPacket := mustNewDecoderWithConfig(t, DecoderConfig{
		SampleRate:       48000,
		Channels:         1,
		MaxPacketSamples: defaultMaxPacketSamples,
		MaxPacketBytes:   len(packet) - 1,
	})
	if _, err := decSmallPacket.Decode(packet, make([]float32, 960)); !errors.Is(err, ErrPacketTooLarge) {
		t.Fatalf("Decode over byte cap error=%v want=%v", err, ErrPacketTooLarge)
	}

	decSmallSamples := mustNewDecoderWithConfig(t, DecoderConfig{
		SampleRate:       48000,
		Channels:         1,
		MaxPacketSamples: 959,
		MaxPacketBytes:   defaultMaxPacketBytes,
	})
	if _, err := decSmallSamples.Decode(packet, make([]float32, 960)); !errors.Is(err, ErrPacketTooLarge) {
		t.Fatalf("Decode over sample cap error=%v want=%v", err, ErrPacketTooLarge)
	}

	dec := mustNewTestDecoder(t, 48000, 1)
	if _, err := dec.Decode(packet, make([]float32, 959)); !errors.Is(err, ErrBufferTooSmall) {
		t.Fatalf("Decode into short buffer error=%v want=%v", err, ErrBufferTooSmall)
	}
}

func TestPublicEncoderResetKeepsConfiguration(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)

	if err := enc.SetBitrate(96000); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetBitrateMode(BitrateModeVBR); err != nil {
		t.Fatalf("SetBitrateMode: %v", err)
	}
	if err := enc.SetComplexity(7); err != nil {
		t.Fatalf("SetComplexity: %v", err)
	}
	if err := enc.SetExpertFrameDuration(ExpertFrameDuration40Ms); err != nil {
		t.Fatalf("SetExpertFrameDuration: %v", err)
	}
	if err := enc.SetForceChannels(1); err != nil {
		t.Fatalf("SetForceChannels: %v", err)
	}
	if err := enc.SetLSBDepth(16); err != nil {
		t.Fatalf("SetLSBDepth: %v", err)
	}
	if err := enc.SetMaxBandwidth(BandwidthWideband); err != nil {
		t.Fatalf("SetMaxBandwidth: %v", err)
	}
	if err := enc.SetSignal(SignalVoice); err != nil {
		t.Fatalf("SetSignal: %v", err)
	}
	enc.SetDTX(true)
	enc.SetFEC(true)
	enc.SetPhaseInversionDisabled(true)
	enc.SetPredictionDisabled(true)
	enc.SetVBRConstraint(false)

	enc.Reset()

	checks := []struct {
		name string
		got  any
		want any
	}{
		{name: "bitrate", got: enc.Bitrate(), want: 96000},
		{name: "bitrate_mode", got: enc.BitrateMode(), want: BitrateModeVBR},
		{name: "complexity", got: enc.Complexity(), want: 7},
		{name: "expert_frame_duration", got: enc.ExpertFrameDuration(), want: ExpertFrameDuration40Ms},
		{name: "frame_size", got: enc.FrameSize(), want: 1920},
		{name: "force_channels", got: enc.ForceChannels(), want: 1},
		{name: "lsb_depth", got: enc.LSBDepth(), want: 16},
		{name: "max_bandwidth", got: enc.MaxBandwidth(), want: BandwidthWideband},
		{name: "signal", got: enc.Signal(), want: SignalVoice},
		{name: "dtx", got: enc.DTXEnabled(), want: true},
		{name: "fec", got: enc.FECEnabled(), want: true},
		{name: "phase_inversion_disabled", got: enc.PhaseInversionDisabled(), want: true},
		{name: "prediction_disabled", got: enc.PredictionDisabled(), want: true},
		{name: "vbr_constraint", got: enc.VBRConstraint(), want: false},
	}
	for _, check := range checks {
		if check.got != check.want {
			t.Fatalf("%s after Reset=%v want %v", check.name, check.got, check.want)
		}
	}
}

func TestPublicDecoderResetKeepsControls(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	if err := dec.SetGain(512); err != nil {
		t.Fatalf("SetGain: %v", err)
	}
	dec.SetIgnoreExtensions(true)

	dec.Reset()

	if got := dec.Gain(); got != 512 {
		t.Fatalf("Gain() after Reset=%d want 512", got)
	}
	if !dec.IgnoreExtensions() {
		t.Fatal("IgnoreExtensions() after Reset=false want true")
	}
}

func requirePacketBasics(t *testing.T, packet []byte, channels, frameSize int) {
	t.Helper()

	if len(packet) == 0 {
		t.Fatal("packet is empty")
	}
	info, err := ParsePacket(packet)
	if err != nil {
		t.Fatalf("ParsePacket: %v", err)
	}
	if info.FrameCount < 1 {
		t.Fatalf("FrameCount=%d want >=1", info.FrameCount)
	}
	if info.TOC.Stereo != (channels == 2) {
		t.Fatalf("packet stereo=%v want %v", info.TOC.Stereo, channels == 2)
	}
	if info.TOC.FrameSize > frameSize {
		t.Fatalf("TOC frame size=%d exceeds encode frame size %d", info.TOC.FrameSize, frameSize)
	}
}

func requireDecodedFloat32(t *testing.T, pcm []float32, gotSamples, wantSamples int, wantEnergy bool) {
	t.Helper()

	if gotSamples != wantSamples {
		t.Fatalf("decoded samples=%d want %d", gotSamples, wantSamples)
	}
	assertPublicCodecPCM(t, pcm)
	if wantEnergy && computeEnergyFloat32(pcm) == 0 {
		t.Fatal("decoded output has zero energy")
	}
}

func requireDecodedInt16(t *testing.T, pcm []int16, gotSamples, wantSamples int) {
	t.Helper()

	if gotSamples != wantSamples {
		t.Fatalf("decoded samples=%d want %d", gotSamples, wantSamples)
	}
	for i, v := range pcm {
		if v != 0 {
			return
		}
		if i == len(pcm)-1 {
			t.Fatal("decoded int16 output is silent")
		}
	}
}

func mustNewDecoderWithConfig(t *testing.T, cfg DecoderConfig) *Decoder {
	t.Helper()

	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder(%s): %v", decoderConfigName(cfg), err)
	}
	return dec
}

func decoderConfigName(cfg DecoderConfig) string {
	return fmt.Sprintf("rate=%d/ch=%d/max_samples=%d/max_bytes=%d", cfg.SampleRate, cfg.Channels, cfg.MaxPacketSamples, cfg.MaxPacketBytes)
}
