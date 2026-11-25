//go:build gopus_fixed_point && gopus_qext

package gopus_test

import (
	"fmt"
	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/benchutil"
	"github.com/thesyncim/gopus/internal/libopustest"
	"math"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPublicFixedQEXTStructuralMalformedPacketPreservesCELTState(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "QEXT-enabled opus_demo", err)
		return
	}
	const channels = 1
	const sampleRate = 48000
	const frameSize = 960
	packets := encodeNative48kQEXTPackets(t, opusDemo, channels, 2)
	badCases := []struct {
		name   string
		packet []byte
	}{
		// CELT config 16, code-3 CBR with M=3 and five payload bytes. The parser
		// rejects the uneven frame split before entering CELT decode.
		{name: "uneven-cbr", packet: []byte{0x83, 3, 1, 2, 3, 4, 5}},
		// CELT config 19 is 20 ms. Seven CBR frames exceed the 120 ms packet
		// duration limit; the parser rejects them before decoding their payloads.
		{name: "duration-over-120ms", packet: []byte{0x9B, 7, 1, 2, 3, 4, 5, 6, 7}},
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
	for _, bad := range badCases {
		bad := bad
		sequence := [][]byte{packets[0], bad.packet, packets[1]}
		for _, format := range formats {
			format := format
			t.Run(bad.name+"/"+formatNames[format], func(t *testing.T) {
				want, err := libopustest.ProbeQEXTDecodeFixedSequence(libopustest.QEXTDecode96kParams{
					SampleFormat: format,
					Channels:     channels,
					SampleRate:   sampleRate,
					MaxFrameSize: frameSize,
					Packets:      sequence,
				})
				if err != nil {
					libopustest.HelperUnavailable(t, "selected fixed-QEXT decode sequence", err)
					return
				}
				if len(want.Status) != len(sequence) || want.Status[0] != frameSize || want.Status[1] != -4 || want.Status[2] != frameSize {
					t.Fatalf("selected C sequence statuses=%v, want [%d -4 %d]", want.Status, frameSize, frameSize)
				}
				dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatal(err)
				}
				f32 := make([]float32, frameSize*channels)
				i16 := make([]int16, len(f32))
				i24 := make([]int32, len(f32))
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
					for i := 0; i < frameSize*channels; i++ {
						switch format {
						case libopustest.QEXTDecode96kFormatInt16:
							if got, expected := i16[i], want.Int16[i+frame*frameSize*channels]; got != expected {
								t.Fatalf("frame %d int16[%d]=%d C=%d", frame, i, got, expected)
							}
						case libopustest.QEXTDecode96kFormatInt24:
							if got, expected := i24[i], want.Int24[i+frame*frameSize*channels]; got != expected {
								t.Fatalf("frame %d int24[%d]=%d C=%d", frame, i, got, expected)
							}
						default:
							if got, expected := math.Float32bits(f32[i]), math.Float32bits(want.PCM[frame*frameSize*channels+i]); got != expected {
								t.Fatalf("frame %d float32[%d]=%08x C=%08x", frame, i, got, expected)
							}
						}
					}
				}

				if n, err := decode(packets[0]); err != nil || n != frameSize {
					t.Fatalf("first received frame samples=%d err=%v", n, err)
				}
				if got, expected := dec.FinalRange(), want.FinalRanges[0]; got != expected {
					t.Fatalf("first range=%08x C=%08x", got, expected)
				}
				compareFrame(0)
				if n, err := decode(bad.packet); n != 0 || err != gopus.ErrInvalidPacket {
					t.Fatalf("malformed code-3 packet samples=%d err=%v, want 0/%v", n, err, gopus.ErrInvalidPacket)
				}
				if got, expected := dec.FinalRange(), want.FinalRanges[1]; got != expected {
					t.Fatalf("malformed packet range=%08x C=%08x", got, expected)
				}
				if n, err := decode(packets[1]); err != nil || n != frameSize {
					t.Fatalf("received frame after malformed packet samples=%d err=%v", n, err)
				}
				if got, expected := dec.FinalRange(), want.FinalRanges[2]; got != expected {
					t.Fatalf("resumed range=%08x C=%08x", got, expected)
				}
				compareFrame(1)
			})
		}
	}
}

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

func TestPublicFixedQEXTCELTReceivedFramesMatchSelectedReference(t *testing.T) {
	libopustest.RequireOracle(t)
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "QEXT-enabled opus_demo", err)
	}

	const frames = 3
	const gainQ8 = 5 * 256
	formats := []uint32{
		libopustest.QEXTDecode96kFormatFloat32,
		libopustest.QEXTDecode96kFormatInt16,
		libopustest.QEXTDecode96kFormatInt24,
	}
	for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000, 96000} {
		frameSize := sampleRate / 50
		for _, channelCase := range []struct {
			packetChannels int
			outputChannels int
			phaseDisabled  bool
		}{
			{packetChannels: 1, outputChannels: 1, phaseDisabled: false},
			{packetChannels: 2, outputChannels: 2, phaseDisabled: true},
			{packetChannels: 2, outputChannels: 1, phaseDisabled: true},
		} {
			var packets [][]byte
			if sampleRate == 96000 {
				packets = encodeNative96kQEXTPackets(t, opusDemo, channelCase.packetChannels,
					native96kSine(channelCase.packetChannels, frames), 320000)
			} else {
				packets = encodeNative48kQEXTPackets(t, opusDemo, channelCase.packetChannels, frames)
			}
			for _, format := range formats {
				formatName := map[uint32]string{
					libopustest.QEXTDecode96kFormatFloat32: "float32",
					libopustest.QEXTDecode96kFormatInt16:   "int16",
					libopustest.QEXTDecode96kFormatInt24:   "int24",
				}[format]
				t.Run(fmt.Sprintf("%dk/packet%dch/output%dch/%s", sampleRate/1000,
					channelCase.packetChannels, channelCase.outputChannels, formatName), func(t *testing.T) {
					ref, err := libopustest.ProbeQEXTDecodeFixed(libopustest.QEXTDecode96kParams{
						SampleFormat:           format,
						Channels:               channelCase.outputChannels,
						SampleRate:             sampleRate,
						MaxFrameSize:           frameSize,
						GainQ8:                 gainQ8,
						PhaseInversionDisabled: channelCase.phaseDisabled,
						Packets:                packets,
					})
					if err != nil {
						libopustest.HelperUnavailable(t, "selected fixed-QEXT decoder", err)
					}
					if len(ref.FinalRanges) != len(packets) {
						t.Fatalf("oracle ranges=%d packets=%d", len(ref.FinalRanges), len(packets))
					}

					dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(sampleRate, channelCase.outputChannels))
					if err != nil {
						t.Fatalf("NewDecoder(%d,%d): %v", sampleRate, channelCase.outputChannels, err)
					}
					if err := dec.SetGain(gainQ8); err != nil {
						t.Fatal(err)
					}
					dec.SetPhaseInversionDisabled(channelCase.phaseDisabled)
					sampleCount := frameSize * channelCase.outputChannels
					outF32 := make([]float32, sampleCount)
					out16 := make([]int16, sampleCount)
					out24 := make([]int32, sampleCount)
					decode := func(packet []byte) (int, error) {
						switch format {
						case libopustest.QEXTDecode96kFormatFloat32:
							return dec.Decode(packet, outF32)
						case libopustest.QEXTDecode96kFormatInt16:
							return dec.DecodeInt16(packet, out16)
						default:
							return dec.DecodeInt24(packet, out24)
						}
					}
					compareFrame := func(frame int) {
						start := frame * sampleCount
						switch format {
						case libopustest.QEXTDecode96kFormatFloat32:
							for i, got := range outF32 {
								if want := ref.PCM[start+i]; math.Float32bits(got) != math.Float32bits(want) {
									t.Fatalf("frame %d float32[%d]=%08x want %08x", frame, i,
										math.Float32bits(got), math.Float32bits(want))
								}
							}
						case libopustest.QEXTDecode96kFormatInt16:
							for i, got := range out16 {
								if want := ref.Int16[start+i]; got != want {
									t.Fatalf("frame %d int16[%d]=%d want %d", frame, i, got, want)
								}
							}
						default:
							for i, got := range out24 {
								if want := ref.Int24[start+i]; got != want {
									t.Fatalf("frame %d int24[%d]=%d want %d", frame, i, got, want)
								}
							}
						}
					}

					for frame, packet := range packets {
						n, err := decode(packet)
						if err != nil || n != frameSize {
							t.Fatalf("frame %d samples=%d err=%v, want %d,nil", frame, n, err, frameSize)
						}
						if got := dec.FinalRange(); got != ref.FinalRanges[frame] {
							t.Fatalf("frame %d final range=%08x want %08x", frame, got, ref.FinalRanges[frame])
						}
						compareFrame(frame)
					}

					dec.Reset()
					n, err := decode(packets[0])
					if err != nil || n != frameSize || dec.FinalRange() != ref.FinalRanges[0] {
						t.Fatalf("reset replay samples=%d range=%08x err=%v", n, dec.FinalRange(), err)
					}
					compareFrame(0)

					var allocErr error
					allocs := testing.AllocsPerRun(30, func() {
						_, allocErr = decode(packets[0])
					})
					if allocErr != nil {
						t.Fatalf("warmed decode: %v", allocErr)
					}
					if allocs != 0 {
						t.Fatalf("warmed %s decode allocated %g times/call", formatName, allocs)
					}
				})
			}
		}
	}
}

func TestPublicFixedQEXTCELTMainWithoutExtensionMatchesSelectedReference(t *testing.T) {
	libopustest.RequireOracle(t)
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "QEXT-enabled opus_demo", err)
	}
	packets := encodeNative48kQEXTPackets(t, opusDemo, 1, 3)
	stripped := make([][]byte, len(packets))
	for i, packet := range packets {
		stripped[i], err = stripFixedQEXTPacketExtension(packet)
		if err != nil {
			t.Fatalf("strip packet %d extension: %v", i, err)
		}
	}
	const gainQ8 = 5 * 256
	for _, format := range []uint32{
		libopustest.QEXTDecode96kFormatFloat32,
		libopustest.QEXTDecode96kFormatInt16,
		libopustest.QEXTDecode96kFormatInt24,
	} {
		formatName := map[uint32]string{
			libopustest.QEXTDecode96kFormatFloat32: "float32",
			libopustest.QEXTDecode96kFormatInt16:   "int16",
			libopustest.QEXTDecode96kFormatInt24:   "int24",
		}[format]
		t.Run(formatName, func(t *testing.T) {
			ref, err := libopustest.ProbeQEXTDecodeFixed(libopustest.QEXTDecode96kParams{
				SampleFormat:           format,
				Channels:               1,
				SampleRate:             48000,
				MaxFrameSize:           960,
				GainQ8:                 gainQ8,
				PhaseInversionDisabled: false,
				Packets:                stripped,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "selected fixed-QEXT base decoder", err)
			}
			dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
			if err != nil {
				t.Fatal(err)
			}
			if err := dec.SetGain(gainQ8); err != nil {
				t.Fatal(err)
			}
			dec.SetPhaseInversionDisabled(false)
			outF32 := make([]float32, 960)
			out16 := make([]int16, 960)
			out24 := make([]int32, 960)
			decode := func(packet []byte) (int, error) {
				switch format {
				case libopustest.QEXTDecode96kFormatFloat32:
					return dec.Decode(packet, outF32)
				case libopustest.QEXTDecode96kFormatInt16:
					return dec.DecodeInt16(packet, out16)
				default:
					return dec.DecodeInt24(packet, out24)
				}
			}
			compare := func(frame int) {
				start := frame * 960
				switch format {
				case libopustest.QEXTDecode96kFormatFloat32:
					for i, got := range outF32 {
						if want := ref.PCM[start+i]; math.Float32bits(got) != math.Float32bits(want) {
							t.Fatalf("frame %d float32[%d]=%08x want %08x", frame, i,
								math.Float32bits(got), math.Float32bits(want))
						}
					}
				case libopustest.QEXTDecode96kFormatInt16:
					for i, got := range out16 {
						if want := ref.Int16[start+i]; got != want {
							t.Fatalf("frame %d int16[%d]=%d want %d", frame, i, got, want)
						}
					}
				default:
					for i, got := range out24 {
						if want := ref.Int24[start+i]; got != want {
							t.Fatalf("frame %d int24[%d]=%d want %d", frame, i, got, want)
						}
					}
				}
			}
			for frame, packet := range stripped {
				n, err := decode(packet)
				if err != nil || n != 960 {
					t.Fatalf("frame %d samples=%d err=%v", frame, n, err)
				}
				if got := dec.FinalRange(); got != ref.FinalRanges[frame] {
					t.Fatalf("frame %d final range Go=%08x C=%08x", frame, got, ref.FinalRanges[frame])
				}
				compare(frame)
			}
			dec.Reset()
			if n, err := decode(stripped[0]); err != nil || n != 960 || dec.FinalRange() != ref.FinalRanges[0] {
				t.Fatalf("reset replay samples=%d range=%08x err=%v", n, dec.FinalRange(), err)
			}
			compare(0)
			var allocErr error
			allocs := testing.AllocsPerRun(30, func() {
				_, allocErr = decode(stripped[0])
			})
			if allocErr != nil || allocs != 0 {
				t.Fatalf("warmed %s decode err=%v allocations=%g", formatName, allocErr, allocs)
			}
		})
	}
}

func TestPublicFixedQEXTSmallBufferDoesNotAdvanceCELTState(t *testing.T) {
	libopustest.RequireOracle(t)
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "QEXT-enabled opus_demo", err)
	}
	const gainQ8 = 5 * 256
	for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000, 96000} {
		frameSize := sampleRate / 50
		packets := encodeQEXTPacketsForAPIRate(t, opusDemo, sampleRate, 1, 2)
		for i, packet := range packets {
			packets[i], err = stripFixedQEXTPacketExtension(packet)
			if err != nil {
				t.Fatalf("%dk strip packet %d: %v", sampleRate/1000, i, err)
			}
		}
		for _, format := range []uint32{
			libopustest.QEXTDecode96kFormatFloat32,
			libopustest.QEXTDecode96kFormatInt16,
			libopustest.QEXTDecode96kFormatInt24,
		} {
			formatName := map[uint32]string{
				libopustest.QEXTDecode96kFormatFloat32: "float32",
				libopustest.QEXTDecode96kFormatInt16:   "int16",
				libopustest.QEXTDecode96kFormatInt24:   "int24",
			}[format]
			t.Run(fmt.Sprintf("%dk/%s", sampleRate/1000, formatName), func(t *testing.T) {
				ref, err := libopustest.ProbeQEXTDecodeFixed(libopustest.QEXTDecode96kParams{
					SampleFormat:           format,
					Channels:               1,
					SampleRate:             sampleRate,
					MaxFrameSize:           frameSize,
					GainQ8:                 gainQ8,
					PhaseInversionDisabled: false,
					Packets:                packets,
				})
				if err != nil {
					libopustest.HelperUnavailable(t, "selected fixed-QEXT short-buffer reference", err)
				}
				dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(sampleRate, 1))
				if err != nil {
					t.Fatal(err)
				}
				if err := dec.SetGain(gainQ8); err != nil {
					t.Fatal(err)
				}
				dec.SetPhaseInversionDisabled(false)
				outF32 := make([]float32, frameSize)
				out16 := make([]int16, frameSize)
				out24 := make([]int32, frameSize)
				decode := func(packet []byte, small bool) (int, error) {
					switch format {
					case libopustest.QEXTDecode96kFormatFloat32:
						out := outF32
						if small {
							out = out[:frameSize-1]
						}
						return dec.Decode(packet, out)
					case libopustest.QEXTDecode96kFormatInt16:
						out := out16
						if small {
							out = out[:frameSize-1]
						}
						return dec.DecodeInt16(packet, out)
					default:
						out := out24
						if small {
							out = out[:frameSize-1]
						}
						return dec.DecodeInt24(packet, out)
					}
				}
				compare := func(frame int) {
					start := frame * frameSize
					switch format {
					case libopustest.QEXTDecode96kFormatFloat32:
						for i, got := range outF32 {
							if want := ref.PCM[start+i]; math.Float32bits(got) != math.Float32bits(want) {
								t.Fatalf("frame %d float32[%d]=%08x want %08x", frame, i,
									math.Float32bits(got), math.Float32bits(want))
							}
						}
					case libopustest.QEXTDecode96kFormatInt16:
						for i, got := range out16 {
							if want := ref.Int16[start+i]; got != want {
								t.Fatalf("frame %d int16[%d]=%d want %d", frame, i, got, want)
							}
						}
					default:
						for i, got := range out24 {
							if want := ref.Int24[start+i]; got != want {
								t.Fatalf("frame %d int24[%d]=%d want %d", frame, i, got, want)
							}
						}
					}
				}
				if n, err := decode(packets[0], false); err != nil || n != frameSize {
					t.Fatalf("first frame samples=%d err=%v", n, err)
				}
				if got := dec.FinalRange(); got != ref.FinalRanges[0] {
					t.Fatalf("first range Go=%08x C=%08x", got, ref.FinalRanges[0])
				}
				compare(0)
				if n, err := decode(packets[1], true); n != 0 || err != gopus.ErrBufferTooSmall {
					t.Fatalf("undersized decode samples=%d err=%v", n, err)
				}
				if got := dec.FinalRange(); got != ref.FinalRanges[0] {
					t.Fatalf("undersized decode changed range to %08x, want %08x", got, ref.FinalRanges[0])
				}
				if n, err := decode(packets[1], false); err != nil || n != frameSize {
					t.Fatalf("received after undersized samples=%d err=%v", n, err)
				}
				if got := dec.FinalRange(); got != ref.FinalRanges[1] {
					t.Fatalf("received after undersized range Go=%08x C=%08x", got, ref.FinalRanges[1])
				}
				compare(1)
			})
		}
	}
}

func encodeQEXTPacketsForAPIRate(t *testing.T, opusDemo string, sampleRate, channels, frames int) [][]byte {
	t.Helper()
	if sampleRate == 96000 {
		return encodeNative96kQEXTPackets(t, opusDemo, channels, native96kSine(channels, frames), 320000)
	}
	return encodeNative48kQEXTPackets(t, opusDemo, channels, frames)
}

func stripFixedQEXTPacketExtension(packet []byte) ([]byte, error) {
	if len(packet) < 3 || packet[0]&3 != 3 || packet[1]&0x40 == 0 {
		return nil, fmt.Errorf("packet has no padded code-3 extension region")
	}
	offset := 2
	padding := 0
	for offset < len(packet) {
		value := int(packet[offset])
		offset++
		if value == 255 {
			padding += 254
			continue
		}
		padding += value
		break
	}
	if padding <= 1 || padding > len(packet) {
		return nil, fmt.Errorf("invalid padding length %d", padding)
	}
	extension := packet[len(packet)-padding:]
	if extension[0] != 0xF8 {
		return nil, fmt.Errorf("packet padding starts with extension ID %02x, want f8", extension[0])
	}
	bodyEnd := len(packet) - padding
	stripped := make([]byte, 0, bodyEnd-1)
	stripped = append(stripped, packet[0], packet[1]&^0x40)
	stripped = append(stripped, packet[3:bodyEnd]...)
	return stripped, nil
}

func encodeNative48kQEXTPackets(t *testing.T, opusDemo string, channels, frames int) [][]byte {
	t.Helper()
	pcm := make([]float32, 960*frames*channels)
	for i := 0; i < 960*frames; i++ {
		phase := 2 * math.Pi * float64(i) / 48000
		left := float32(0.22*math.Sin(2*math.Pi*19000*float64(i)/48000) +
			0.24*math.Sin(2*math.Pi*22000*float64(i)/48000) + 0.08*math.Sin(phase*13))
		pcm[i*channels] = left
		if channels == 2 {
			pcm[i*channels+1] = float32(0.91 * float64(left))
		}
	}
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "input48.f32")
	bitPath := filepath.Join(dir, "output48.bit")
	if err := benchutil.WriteRepeatedRawFloat32(inputPath, pcm, 1); err != nil {
		t.Fatalf("WriteRepeatedRawFloat32: %v", err)
	}
	args := []string{"-e", "restricted-celt", "48000", fmt.Sprint(channels), "256000",
		"-f32", "-complexity", "10", "-bandwidth", "FB", "-framesize", "20",
		"-qext", "-cbr", inputPath, bitPath}
	if output, err := exec.Command(opusDemo, args...).CombinedOutput(); err != nil {
		t.Fatalf("opus_demo 48k encode: %v (%s)", err, output)
	}
	packets, err := allOpusDemoPackets(bitPath)
	if err != nil {
		t.Fatalf("parse 48k bitstream: %v", err)
	}
	return packets
}
