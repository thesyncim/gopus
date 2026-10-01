//go:build gopus_fixed_point && gopus_qext

package gopus_test

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/benchutil"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestPublicFixedQEXTLostCELTFrameMatchesSelectedReference(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "QEXT-enabled opus_demo", err)
		return
	}
	type channelCase struct {
		packetChannels int
		outputChannels int
		phaseDisabled  bool
	}
	// Cover every supported API rate, both native CELT geometries, and a
	// downmixed stereo packet. Stereo phase-inversion control is tested both ways.
	cases := []struct {
		sampleRate int
		channels   channelCase
	}{
		{8000, channelCase{packetChannels: 1, outputChannels: 1}},
		{12000, channelCase{packetChannels: 1, outputChannels: 1}},
		{16000, channelCase{packetChannels: 1, outputChannels: 1}},
		{24000, channelCase{packetChannels: 2, outputChannels: 1}},
		{48000, channelCase{packetChannels: 2, outputChannels: 2}},
		{48000, channelCase{packetChannels: 2, outputChannels: 2, phaseDisabled: true}},
		{96000, channelCase{packetChannels: 2, outputChannels: 2}},
		{96000, channelCase{packetChannels: 2, outputChannels: 1, phaseDisabled: true}},
	}
	formats := []uint32{
		libopustest.QEXTDecode96kFormatFloat32,
		libopustest.QEXTDecode96kFormatInt16,
		libopustest.QEXTDecode96kFormatInt24,
	}
	formatNames := map[uint32]string{
		libopustest.QEXTDecode96kFormatFloat32: "float32",
		libopustest.QEXTDecode96kFormatInt16:   "int16",
		libopustest.QEXTDecode96kFormatInt24:   "int24",
	}
	const gainQ8 = 8 * 256
	for _, tc := range cases {
		tc := tc
		frameSize := tc.sampleRate / 50
		var packets [][]byte
		if tc.sampleRate == 96000 {
			packets = encodeNative96kQEXTPackets(t, opusDemo, tc.channels.packetChannels,
				native96kSine(tc.channels.packetChannels, 2), 320000)
		} else {
			packets = encodeNative48kQEXTPackets(t, opusDemo, tc.channels.packetChannels, 2)
		}
		oneFrameTOC := packets[0][0] & 0xFC
		lossCases := []struct {
			name   string
			packet []byte
		}{
			{name: "nil"},
			{name: "one-byte-body", packet: []byte{oneFrameTOC, 0}},
		}
		for _, loss := range lossCases {
			loss := loss
			sequence := [][]byte{packets[0], loss.packet, packets[1]}
			for _, format := range formats {
				format := format
				t.Run(fmt.Sprintf("%dk/packet%d-output%d/phaseDisabled=%t/%s/%s",
					tc.sampleRate/1000, tc.channels.packetChannels, tc.channels.outputChannels,
					tc.channels.phaseDisabled, loss.name, formatNames[format]), func(t *testing.T) {
					want, err := libopustest.ProbeQEXTDecodeFixed(libopustest.QEXTDecode96kParams{
						SampleFormat:           format,
						Channels:               tc.channels.outputChannels,
						SampleRate:             tc.sampleRate,
						MaxFrameSize:           frameSize,
						GainQ8:                 gainQ8,
						PhaseInversionDisabled: tc.channels.phaseDisabled,
						Packets:                sequence,
					})
					if err != nil {
						libopustest.HelperUnavailable(t, "selected fixed-QEXT decoder", err)
						return
					}
					if len(want.FinalRanges) != len(sequence) {
						t.Fatalf("oracle ranges=%d packets=%d", len(want.FinalRanges), len(sequence))
					}
					dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(tc.sampleRate, tc.channels.outputChannels))
					if err != nil {
						t.Fatal(err)
					}
					if err := dec.SetGain(gainQ8); err != nil {
						t.Fatal(err)
					}
					dec.SetPhaseInversionDisabled(tc.channels.phaseDisabled)
					sampleCount := frameSize * tc.channels.outputChannels
					f32 := make([]float32, sampleCount)
					i16 := make([]int16, sampleCount)
					i24 := make([]int32, sampleCount)
					decode := func(packet []byte) (int, error) {
						switch format {
						case libopustest.QEXTDecode96kFormatInt16:
							return dec.DecodeInt16(packet, i16)
						case libopustest.QEXTDecode96kFormatInt24:
							return dec.DecodeInt24(packet, i24)
						default:
							return dec.Decode(packet, f32)
						}
					}
					compareFrame := func(frame int) {
						start := frame * sampleCount
						for i := 0; i < sampleCount; i++ {
							switch format {
							case libopustest.QEXTDecode96kFormatInt16:
								if got, expected := i16[i], want.Int16[start+i]; got != expected {
									t.Fatalf("frame %d int16[%d]=%d C=%d", frame, i, got, expected)
								}
							case libopustest.QEXTDecode96kFormatInt24:
								if got, expected := i24[i], want.Int24[start+i]; got != expected {
									t.Fatalf("frame %d int24[%d]=%d C=%d", frame, i, got, expected)
								}
							default:
								if got, expected := math.Float32bits(f32[i]), math.Float32bits(want.PCM[start+i]); got != expected {
									t.Fatalf("frame %d float32[%d]=%08x C=%08x", frame, i, got, expected)
								}
							}
						}
					}
					runSequence := func() {
						for frame, packet := range sequence {
							if n, err := decode(packet); err != nil || n != frameSize {
								t.Fatalf("frame %d samples=%d err=%v, want %d,nil", frame, n, err, frameSize)
							}
							if got, expected := dec.FinalRange(), want.FinalRanges[frame]; got != expected {
								t.Fatalf("frame %d range=%08x C=%08x", frame, got, expected)
							}
							compareFrame(frame)
						}
					}
					runSequence()
					dec.Reset()
					runSequence()
					if allocs := testing.AllocsPerRun(20, func() {
						if n, err := decode(nil); err != nil || n != frameSize {
							t.Fatalf("measured PLC samples=%d err=%v", n, err)
						}
					}); allocs != 0 {
						t.Fatalf("warm QEXT PLC allocations=%g, want 0", allocs)
					}
				})
			}
		}
	}
}

func TestPublicFixedQEXTLostCELTBurstMatchesSelectedReference(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "QEXT-enabled opus_demo", err)
		return
	}
	type channelCase struct {
		packetChannels int
		outputChannels int
		phaseDisabled  bool
	}
	cases := []struct {
		sampleRate int
		channels   channelCase
	}{
		{8000, channelCase{packetChannels: 1, outputChannels: 1}},
		{12000, channelCase{packetChannels: 1, outputChannels: 1}},
		{16000, channelCase{packetChannels: 1, outputChannels: 1}},
		{48000, channelCase{packetChannels: 1, outputChannels: 1}},
		{48000, channelCase{packetChannels: 2, outputChannels: 2, phaseDisabled: true}},
		{24000, channelCase{packetChannels: 2, outputChannels: 1}},
		{96000, channelCase{packetChannels: 2, outputChannels: 2}},
		{96000, channelCase{packetChannels: 2, outputChannels: 1, phaseDisabled: true}},
	}
	formats := []uint32{
		libopustest.QEXTDecode96kFormatFloat32,
		libopustest.QEXTDecode96kFormatInt16,
		libopustest.QEXTDecode96kFormatInt24,
	}
	formatNames := map[uint32]string{
		libopustest.QEXTDecode96kFormatFloat32: "float32",
		libopustest.QEXTDecode96kFormatInt16:   "int16",
		libopustest.QEXTDecode96kFormatInt24:   "int24",
	}
	const gainQ8 = 8 * 256
	for _, tc := range cases {
		tc := tc
		frameSize := tc.sampleRate / 50
		var packets [][]byte
		if tc.sampleRate == 96000 {
			packets = encodeNative96kQEXTPackets(t, opusDemo, tc.channels.packetChannels,
				native96kSine(tc.channels.packetChannels, 2), 320000)
		} else {
			packets = encodeNative48kQEXTPackets(t, opusDemo, tc.channels.packetChannels, 2)
		}
		// Six consecutive 20 ms losses reach CELT's noise PLC branch after
		// periodic concealment. A received frame followed by another loss checks
		// the skipPLC recovery state against the persistent C decoder.
		sequence := make([][]byte, 0, 9)
		sequence = append(sequence, packets[0])
		for range 6 {
			sequence = append(sequence, nil)
		}
		sequence = append(sequence, packets[1], nil)
		for _, format := range formats {
			format := format
			t.Run(fmt.Sprintf("%dk/packet%d-output%d/phaseDisabled=%t/%s",
				tc.sampleRate/1000, tc.channels.packetChannels, tc.channels.outputChannels,
				tc.channels.phaseDisabled, formatNames[format]), func(t *testing.T) {
				want, err := libopustest.ProbeQEXTDecodeFixed(libopustest.QEXTDecode96kParams{
					SampleFormat:           format,
					Channels:               tc.channels.outputChannels,
					SampleRate:             tc.sampleRate,
					MaxFrameSize:           frameSize,
					GainQ8:                 gainQ8,
					PhaseInversionDisabled: tc.channels.phaseDisabled,
					Packets:                sequence,
				})
				if err != nil {
					libopustest.HelperUnavailable(t, "selected fixed-QEXT decoder", err)
					return
				}
				dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(tc.sampleRate, tc.channels.outputChannels))
				if err != nil {
					t.Fatal(err)
				}
				if err := dec.SetGain(gainQ8); err != nil {
					t.Fatal(err)
				}
				dec.SetPhaseInversionDisabled(tc.channels.phaseDisabled)
				sampleCount := frameSize * tc.channels.outputChannels
				f32 := make([]float32, sampleCount)
				i16 := make([]int16, sampleCount)
				i24 := make([]int32, sampleCount)
				decode := func(packet []byte) (int, error) {
					switch format {
					case libopustest.QEXTDecode96kFormatInt16:
						return dec.DecodeInt16(packet, i16)
					case libopustest.QEXTDecode96kFormatInt24:
						return dec.DecodeInt24(packet, i24)
					default:
						return dec.Decode(packet, f32)
					}
				}
				compareFrame := func(frame int) {
					start := frame * sampleCount
					for i := 0; i < sampleCount; i++ {
						switch format {
						case libopustest.QEXTDecode96kFormatInt16:
							if got, expected := i16[i], want.Int16[start+i]; got != expected {
								t.Fatalf("frame %d int16[%d]=%d C=%d", frame, i, got, expected)
							}
						case libopustest.QEXTDecode96kFormatInt24:
							if got, expected := i24[i], want.Int24[start+i]; got != expected {
								t.Fatalf("frame %d int24[%d]=%d C=%d", frame, i, got, expected)
							}
						default:
							if got, expected := math.Float32bits(f32[i]), math.Float32bits(want.PCM[start+i]); got != expected {
								t.Fatalf("frame %d float32[%d]=%08x C=%08x", frame, i, got, expected)
							}
						}
					}
				}
				runSequence := func() {
					for frame, packet := range sequence {
						if n, err := decode(packet); err != nil || n != frameSize {
							t.Fatalf("frame %d samples=%d err=%v, want %d,nil", frame, n, err, frameSize)
						}
						if got, expected := dec.FinalRange(), want.FinalRanges[frame]; got != expected {
							t.Fatalf("frame %d range=%08x C=%08x", frame, got, expected)
						}
						compareFrame(frame)
					}
				}
				runSequence()
				dec.Reset()
				runSequence()
				var allocErr error
				allocs := testing.AllocsPerRun(20, func() {
					_, allocErr = decode(nil)
				})
				if allocErr != nil {
					t.Fatalf("measured PLC: %v", allocErr)
				}
				if allocs != 0 {
					t.Fatalf("warm QEXT PLC allocations=%g, want 0", allocs)
				}
			})
		}
	}
}
