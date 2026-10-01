//go:build gopus_fixed_point && gopus_qext

package gopus

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/benchutil"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestPublicFixedQEXTHybridReceivedFramesMatchSelectedReference(t *testing.T) {
	libopustest.RequireOracle(t)
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "QEXT-enabled opus_demo", err)
		return
	}
	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			packet := makeHybridQEXTPacketForTest(t, opusDemo, channels)
			info, frames, _, _, err := parsePacketFramesAndPadding(packet)
			if err != nil {
				t.Fatalf("parsePacketFramesAndPadding: %v", err)
			}
			if info.TOC.Mode != ModeHybrid || info.TOC.FrameSize != 960 || len(frames) != 1 {
				t.Fatalf("packet mode=%v frameSize=%d frames=%d, want Hybrid/960/1", info.TOC.Mode, info.TOC.FrameSize, len(frames))
			}
			plainPacket := make([]byte, len(packet)+16)
			plainN, err := buildRepacketizedPacketWithOptions(packet[0]&^byte(0x03), frames, plainPacket, 0, false, nil)
			if err != nil {
				t.Fatalf("build Hybrid packet without extension: %v", err)
			}
			plainPacket = plainPacket[:plainN]
			for _, packetCase := range []struct {
				name             string
				packet           []byte
				ignoreExtensions bool
			}{
				{name: "qext-active", packet: packet},
				{name: "qext-absent", packet: plainPacket},
				{name: "qext-ignored", packet: packet, ignoreExtensions: true},
			} {
				packetCase := packetCase
				goSequence := [][]byte{packetCase.packet, packetCase.packet, packetCase.packet}
				for _, format := range []uint32{
					libopustest.QEXTDecode96kFormatFloat32,
					libopustest.QEXTDecode96kFormatInt16,
					libopustest.QEXTDecode96kFormatInt24,
				} {
					format := format
					formatName := map[uint32]string{
						libopustest.QEXTDecode96kFormatFloat32: "float32",
						libopustest.QEXTDecode96kFormatInt16:   "int16",
						libopustest.QEXTDecode96kFormatInt24:   "int24",
					}[format]
					t.Run(packetCase.name+"/"+formatName, func(t *testing.T) {
						const gainQ8 = 8 * 256
						want, err := libopustest.ProbeQEXTDecodeFixed(libopustest.QEXTDecode96kParams{
							SampleFormat:           format,
							Channels:               channels,
							SampleRate:             48000,
							MaxFrameSize:           960,
							GainQ8:                 gainQ8,
							PhaseInversionDisabled: false,
							IgnoreExtensions:       packetCase.ignoreExtensions,
							Packets:                goSequence,
						})
						if err != nil {
							libopustest.HelperUnavailable(t, "selected fixed-QEXT decoder", err)
							return
						}
						dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
						if err != nil {
							t.Fatal(err)
						}
						if err := dec.SetGain(gainQ8); err != nil {
							t.Fatal(err)
						}
						dec.SetIgnoreExtensions(packetCase.ignoreExtensions)
						outF32 := make([]float32, 960*channels)
						out16 := make([]int16, len(outF32))
						out24 := make([]int32, len(outF32))
						decode := func(encoded []byte) (int, error) {
							switch format {
							case libopustest.QEXTDecode96kFormatInt16:
								return dec.DecodeInt16(encoded, out16)
							case libopustest.QEXTDecode96kFormatInt24:
								return dec.DecodeInt24(encoded, out24)
							default:
								return dec.Decode(encoded, outF32)
							}
						}
						compareFrame := func(frame int) {
							start := frame * len(outF32)
							for i := range outF32 {
								switch format {
								case libopustest.QEXTDecode96kFormatInt16:
									if got, expected := out16[i], want.Int16[start+i]; got != expected {
										t.Fatalf("frame %d int16[%d]=%d C=%d", frame, i, got, expected)
									}
								case libopustest.QEXTDecode96kFormatInt24:
									if got, expected := out24[i], want.Int24[start+i]; got != expected {
										t.Fatalf("frame %d int24[%d]=%d C=%d", frame, i, got, expected)
									}
								default:
									if got, expected := math.Float32bits(outF32[i]), math.Float32bits(want.PCM[start+i]); got != expected {
										t.Fatalf("frame %d float32[%d]=%08x C=%08x", frame, i, got, expected)
									}
								}
							}
						}
						runSequence := func() {
							for frame, encoded := range goSequence {
								if n, err := decode(encoded); err != nil || n != 960 {
									t.Fatalf("frame %d samples=%d err=%v", frame, n, err)
								}
								if got, expected := dec.FinalRange(), want.FinalRanges[frame]; got != expected {
									t.Fatalf("frame %d FinalRange=%08x C=%08x", frame, got, expected)
								}
								compareFrame(frame)
							}
						}
						runSequence()
						dec.Reset()
						runSequence()
						var allocErr error
						allocs := testing.AllocsPerRun(20, func() {
							_, allocErr = decode(packetCase.packet)
						})
						if allocErr != nil {
							t.Fatalf("warmed Hybrid decode: %v", allocErr)
						}
						if allocs != 0 {
							t.Fatalf("warmed Hybrid decode allocated %g times/call", allocs)
						}
					})
				}
			}
		})
	}
}

func TestPublicFixedQEXTCELTToHybridTransitionMatchesSelectedReference(t *testing.T) {
	libopustest.RequireOracle(t)
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "QEXT-enabled opus_demo", err)
		return
	}
	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			celtPacket := makeCELTQEXTPacketForTransition(t, opusDemo, channels)
			hybridPacket := makeHybridQEXTPacketForTest(t, opusDemo, channels)
			sequence := [][]byte{celtPacket, celtPacket, hybridPacket, hybridPacket}
			for i, packet := range sequence {
				info, _, _, _, err := parsePacketFramesAndPadding(packet)
				if err != nil {
					t.Fatalf("parse packet %d: %v", i, err)
				}
				wantMode := ModeCELT
				if i >= 2 {
					wantMode = ModeHybrid
				}
				if info.TOC.Mode != wantMode {
					t.Fatalf("packet %d mode=%v, want %v", i, info.TOC.Mode, wantMode)
				}
			}
			for _, format := range []uint32{
				libopustest.QEXTDecode96kFormatFloat32,
				libopustest.QEXTDecode96kFormatInt16,
				libopustest.QEXTDecode96kFormatInt24,
			} {
				format := format
				formatName := map[uint32]string{
					libopustest.QEXTDecode96kFormatFloat32: "float32",
					libopustest.QEXTDecode96kFormatInt16:   "int16",
					libopustest.QEXTDecode96kFormatInt24:   "int24",
				}[format]
				t.Run(formatName, func(t *testing.T) {
					assertFixedQEXTDecodeSequence(t, channels, 48000, 960, 8*256, false, sequence, format)
				})
			}
		})
	}
}

func TestPublicFixedQEXTHybridToCELTTransitionMatchesSelectedReference(t *testing.T) {
	libopustest.RequireOracle(t)
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "QEXT-enabled opus_demo", err)
		return
	}
	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			hybridPacket := makeHybridQEXTPacketForTest(t, opusDemo, channels)
			celtPacket := makeCELTQEXTPacketForTransition(t, opusDemo, channels)
			sequence := [][]byte{hybridPacket, hybridPacket, celtPacket, celtPacket}
			for i, packet := range sequence {
				info, _, _, _, err := parsePacketFramesAndPadding(packet)
				if err != nil {
					t.Fatalf("parse packet %d: %v", i, err)
				}
				wantMode := ModeHybrid
				if i >= 2 {
					wantMode = ModeCELT
				}
				if info.TOC.Mode != wantMode {
					t.Fatalf("packet %d mode=%v, want %v", i, info.TOC.Mode, wantMode)
				}
			}
			for _, format := range []uint32{
				libopustest.QEXTDecode96kFormatFloat32,
				libopustest.QEXTDecode96kFormatInt16,
				libopustest.QEXTDecode96kFormatInt24,
			} {
				format := format
				formatName := map[uint32]string{
					libopustest.QEXTDecode96kFormatFloat32: "float32",
					libopustest.QEXTDecode96kFormatInt16:   "int16",
					libopustest.QEXTDecode96kFormatInt24:   "int24",
				}[format]
				t.Run(formatName, func(t *testing.T) {
					assertFixedQEXTDecodeSequence(t, channels, 48000, 960, 8*256, false, sequence, format)
				})
			}
		})
	}
}

func TestPublicFixedQEXTHybridLostFrameMatchesSelectedReference(t *testing.T) {
	libopustest.RequireOracle(t)
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "QEXT-enabled opus_demo", err)
		return
	}
	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			packet := makeHybridQEXTPacketForTest(t, opusDemo, channels)
			sequence := [][]byte{packet, nil, packet}
			for _, format := range []uint32{
				libopustest.QEXTDecode96kFormatFloat32,
				libopustest.QEXTDecode96kFormatInt16,
				libopustest.QEXTDecode96kFormatInt24,
			} {
				format := format
				formatName := map[uint32]string{
					libopustest.QEXTDecode96kFormatFloat32: "float32",
					libopustest.QEXTDecode96kFormatInt16:   "int16",
					libopustest.QEXTDecode96kFormatInt24:   "int24",
				}[format]
				t.Run(formatName, func(t *testing.T) {
					assertFixedQEXTDecodeSequence(t, channels, 48000, 960, 8*256, false, sequence, format)
				})
			}
		})
	}
}

func TestPublicFixedQEXTHybridLowerRateAndDownmixMatchesSelectedReference(t *testing.T) {
	libopustest.RequireOracle(t)
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "QEXT-enabled opus_demo", err)
		return
	}
	for _, packetChannels := range []int{1, 2} {
		packetChannels := packetChannels
		for _, outputChannels := range []int{1, 2} {
			if outputChannels > packetChannels {
				continue
			}
			outputChannels := outputChannels
			for _, sampleRate := range []int{8000, 12000, 16000, 24000} {
				sampleRate := sampleRate
				t.Run(fmt.Sprintf("packet%dch/output%dch/%dhz", packetChannels, outputChannels, sampleRate), func(t *testing.T) {
					packet := makeHybridQEXTPacketForTest(t, opusDemo, packetChannels)
					sequence := [][]byte{packet, packet, packet}
					for _, format := range []uint32{
						libopustest.QEXTDecode96kFormatFloat32,
						libopustest.QEXTDecode96kFormatInt16,
						libopustest.QEXTDecode96kFormatInt24,
					} {
						format := format
						formatName := map[uint32]string{
							libopustest.QEXTDecode96kFormatFloat32: "float32",
							libopustest.QEXTDecode96kFormatInt16:   "int16",
							libopustest.QEXTDecode96kFormatInt24:   "int24",
						}[format]
						t.Run(formatName, func(t *testing.T) {
							assertFixedQEXTDecodeSequenceForOutput(t, outputChannels,
								sampleRate, sampleRate/50, 8*256, false, sequence, format)
						})
					}
				})
			}
		}
	}
}

func TestPublicFixedQEXTHybridFECMatchesSelectedReference(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			seed, recovery := encodeAPIRateFECSequence(t, EncoderModeHybrid, ModeHybrid,
				BandwidthFullband, 64000, channels, 960)
			if got := ParseTOC(seed[0]).Mode; got != ModeHybrid {
				t.Fatalf("seed mode=%v, want Hybrid", got)
			}
			if got := ParseTOC(recovery[0]).Mode; got != ModeHybrid {
				t.Fatalf("recovery mode=%v, want Hybrid", got)
			}
			if !packetHasInBandFEC(t, recovery) {
				t.Fatal("recovery packet has no Hybrid LBRR frame")
			}
			packets := [][]byte{seed, recovery}
			want, err := libopustest.ProbeQEXTDecodeFixedFECSequence(libopustest.QEXTDecode96kParams{
				SampleFormat: libopustest.QEXTDecode96kFormatFloat32,
				Channels:     channels,
				SampleRate:   48000,
				MaxFrameSize: 960,
				DecodeFEC:    []bool{false, true},
				Packets:      packets,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "selected fixed-QEXT Hybrid FEC decoder", err)
				return
			}
			if len(want.Status) != len(packets) || want.Status[0] != 960 || want.Status[1] != 960 {
				t.Fatalf("selected C decode statuses=%v, want [960 960]", want.Status)
			}

			dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatal(err)
			}
			frame := make([]float32, 960*channels)
			if n, err := dec.Decode(seed, frame); err != nil || n != 960 {
				t.Fatalf("seed Decode=(%d,%v), want (960,nil)", n, err)
			}
			if n, err := dec.DecodeWithFEC(recovery, frame, true); err != nil || n != 960 {
				t.Fatalf("FEC Decode=(%d,%v), want (960,nil)", n, err)
			}
			if got, expected := dec.FinalRange(), want.FinalRanges[1]; got != expected {
				t.Fatalf("FEC FinalRange=%08x C=%08x", got, expected)
			}
			start := 960 * channels
			for i, got := range frame {
				if a, b := math.Float32bits(got), math.Float32bits(want.PCM[start+i]); a != b {
					t.Fatalf("FEC float32[%d]=%08x C=%08x", i, a, b)
				}
			}
			var decodeErr error
			allocs := testing.AllocsPerRun(20, func() {
				_, decodeErr = dec.DecodeWithFEC(recovery, frame, true)
			})
			if decodeErr != nil {
				t.Fatalf("warmed Hybrid FEC decode: %v", decodeErr)
			}
			if allocs != 0 {
				t.Fatalf("warmed Hybrid FEC allocated %g times/call", allocs)
			}
		})
	}
}

func TestPublicFixedQEXTHybridRedundancyDirectionsMatchSelectedReference(t *testing.T) {
	libopustest.RequireOracle(t)
	totalSilkToCelt, totalCeltToSilk := 0, 0
	for _, channels := range []int{1, 2} {
		channels := channels
		for _, controls := range [][]int{
			{libopustest.OpusForceModeCELTOnly, libopustest.OpusForceModeCELTOnly, libopustest.OpusForceModeHybrid, libopustest.OpusForceModeHybrid, libopustest.OpusForceModeCELTOnly, libopustest.OpusForceModeCELTOnly},
			{libopustest.OpusForceModeCELTOnly, libopustest.OpusForceModeCELTOnly, libopustest.OpusForceModeHybrid, libopustest.OpusForceModeHybrid, libopustest.OpusForceModeCELTOnly, libopustest.OpusForceModeCELTOnly, libopustest.OpusForceModeHybrid, libopustest.OpusForceModeHybrid, libopustest.OpusForceModeCELTOnly, libopustest.OpusForceModeCELTOnly},
		} {
			controls := controls
			t.Run(fmt.Sprintf("%dch/%dframes", channels, len(controls)), func(t *testing.T) {
				packets := encodeSelectedFixedQEXTRedundancyPackets(t, channels, controls)
				previousMode := libopustest.OpusForceModeCELTOnly
				for i, packet := range packets {
					wantMode := ModeHybrid
					if controls[i] == libopustest.OpusForceModeCELTOnly {
						wantMode = ModeCELT
						// A switch from Hybrid to CELT carries the redundant CELT frame
						// in one final Hybrid packet. libopus commits prev_mode to CELT
						// after encoding that packet.
						if previousMode != libopustest.OpusForceModeCELTOnly {
							wantMode = ModeHybrid
						}
					}
					if got := ParseTOC(packet[0]).Mode; got != wantMode {
						t.Fatalf("frame %d mode=%v, requested %v", i, got, wantMode)
					}
					previousMode = controls[i]
				}
				want, err := libopustest.ProbeQEXTDecodeFixed(libopustest.QEXTDecode96kParams{
					SampleFormat: libopustest.QEXTDecode96kFormatInt24,
					Channels:     channels,
					SampleRate:   48000,
					MaxFrameSize: 960,
					Packets:      packets,
				})
				if err != nil {
					libopustest.HelperUnavailable(t, "selected fixed-QEXT redundancy decoder", err)
					return
				}
				dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
				if err != nil {
					t.Fatal(err)
				}
				frame := make([]int32, 960*channels)
				for frameIndex, packet := range packets {
					if n, err := dec.DecodeInt24(packet, frame); err != nil || n != 960 {
						t.Fatalf("frame %d DecodeInt24=(%d,%v), want (960,nil)", frameIndex, n, err)
					}
					if got, expected := dec.FinalRange(), want.FinalRanges[frameIndex]; got != expected {
						t.Fatalf("frame %d FinalRange=%08x C=%08x", frameIndex, got, expected)
					}
					start := frameIndex * len(frame)
					for i, got := range frame {
						if expected := want.Int24[start+i]; got != expected {
							t.Fatalf("frame %d int24[%d]=%d C=%d", frameIndex, i, got, expected)
						}
					}
				}
				if dec.fixedRedundancyApplied == 0 {
					t.Fatalf("sequence did not exercise QEXT redundancy (Silk→CELT=%d CELT→Silk=%d)",
						dec.fixedRedundancySilkToCeltApplied, dec.fixedRedundancyCeltToSilkApplied)
				}
				totalSilkToCelt += dec.fixedRedundancySilkToCeltApplied
				totalCeltToSilk += dec.fixedRedundancyCeltToSilkApplied
				var decodeErr error
				allocs := testing.AllocsPerRun(20, func() {
					_, decodeErr = dec.DecodeInt24(packets[len(packets)-1], frame)
				})
				if decodeErr != nil {
					t.Fatalf("warmed redundancy decode: %v", decodeErr)
				}
				if allocs != 0 {
					t.Fatalf("warmed redundancy decode allocated %g times/call", allocs)
				}
			})
		}
	}
	if totalSilkToCelt == 0 || totalCeltToSilk == 0 {
		t.Fatalf("mode-switch sequences exercised only one redundancy direction: Silk→CELT=%d CELT→Silk=%d", totalSilkToCelt, totalCeltToSilk)
	}
}

func encodeSelectedFixedQEXTRedundancyPackets(t *testing.T, channels int, modes []int) [][]byte {
	t.Helper()
	frames := make([]libopustest.OpusEncodeFixedMixedFrame, len(modes))
	for frame, mode := range modes {
		pcm := make([]float32, 960*channels)
		for i := 0; i < 960; i++ {
			tm := float64(frame*960+i) / 48000
			pcm[i*channels] = float32(0.31*math.Sin(2*math.Pi*(217+13*float64(frame))*tm) +
				0.17*math.Sin(2*math.Pi*(601+29*float64(frame))*tm+0.17))
			if channels == 2 {
				pcm[i*channels+1] = float32(0.26*math.Sin(2*math.Pi*(283+17*float64(frame))*tm+0.11) +
					0.12*math.Sin(2*math.Pi*(887+31*float64(frame))*tm+0.29))
			}
		}
		frames[frame] = libopustest.OpusEncodeFixedMixedFrame{
			Format: 1, FloatPCM: pcm, ForceMode: mode, Bandwidth: libopustest.OpusBandwidthFullband,
		}
	}
	records, err := libopustest.ProbeOpusEncodeFixedQEXTMixedRecords(libopustest.OpusEncodeFixedParams{
		SampleRate: 48000, Channels: channels, Application: libopustest.OpusApplicationAudio,
		MaxPacketBytes: 4000, Bitrate: 96000, Complexity: 10, ForceChannels: channels,
		FrameSize: 960, FrameCount: len(frames),
	}, frames)
	if err != nil {
		libopustest.HelperUnavailable(t, "selected fixed-QEXT mode-switch encoder", err)
		return nil
	}
	packets := make([][]byte, len(records))
	for i, record := range records {
		if record.Status < 0 || len(record.Packet) == 0 {
			t.Fatalf("selected C frame %d encode status=%d packetLen=%d", i, record.Status, len(record.Packet))
		}
		packets[i] = append([]byte(nil), record.Packet...)
	}
	return packets
}

func assertFixedQEXTDecodeSequence(t *testing.T, channels, sampleRate, frameSize int, gainQ8 int32, phaseDisabled bool, packets [][]byte, format uint32) {
	assertFixedQEXTDecodeSequenceForOutput(t, channels, sampleRate, frameSize, gainQ8, phaseDisabled, packets, format)
}

func assertFixedQEXTDecodeSequenceForOutput(t *testing.T, outputChannels, sampleRate, frameSize int, gainQ8 int32, phaseDisabled bool, packets [][]byte, format uint32) {
	t.Helper()
	want, err := libopustest.ProbeQEXTDecodeFixed(libopustest.QEXTDecode96kParams{
		SampleFormat:           format,
		Channels:               outputChannels,
		SampleRate:             sampleRate,
		MaxFrameSize:           frameSize,
		GainQ8:                 gainQ8,
		PhaseInversionDisabled: phaseDisabled,
		Packets:                packets,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "selected fixed-QEXT decoder", err)
		return
	}
	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, outputChannels))
	if err != nil {
		t.Fatal(err)
	}
	if err := dec.SetGain(int(gainQ8)); err != nil {
		t.Fatal(err)
	}
	dec.SetPhaseInversionDisabled(phaseDisabled)
	sampleCount := frameSize * outputChannels
	outF32 := make([]float32, sampleCount)
	out16 := make([]int16, sampleCount)
	out24 := make([]int32, sampleCount)
	decode := func(encoded []byte) (int, error) {
		switch format {
		case libopustest.QEXTDecode96kFormatInt16:
			return dec.DecodeInt16(encoded, out16)
		case libopustest.QEXTDecode96kFormatInt24:
			return dec.DecodeInt24(encoded, out24)
		default:
			return dec.Decode(encoded, outF32)
		}
	}
	runSequence := func() {
		for frame, packet := range packets {
			if n, err := decode(packet); err != nil || n != frameSize {
				t.Fatalf("frame %d samples=%d err=%v", frame, n, err)
			}
			if got, expected := dec.FinalRange(), want.FinalRanges[frame]; got != expected {
				t.Fatalf("frame %d FinalRange=%08x C=%08x", frame, got, expected)
			}
			start := frame * sampleCount
			for i := 0; i < sampleCount; i++ {
				switch format {
				case libopustest.QEXTDecode96kFormatInt16:
					if got, expected := out16[i], want.Int16[start+i]; got != expected {
						t.Fatalf("frame %d int16[%d]=%d C=%d", frame, i, got, expected)
					}
				case libopustest.QEXTDecode96kFormatInt24:
					if got, expected := out24[i], want.Int24[start+i]; got != expected {
						t.Fatalf("frame %d int24[%d]=%d C=%d", frame, i, got, expected)
					}
				default:
					if got, expected := math.Float32bits(outF32[i]), math.Float32bits(want.PCM[start+i]); got != expected {
						t.Fatalf("frame %d float32[%d]=%08x C=%08x", frame, i, got, expected)
					}
				}
			}
		}
	}
	runSequence()
	dec.Reset()
	runSequence()
	var allocErr error
	allocs := testing.AllocsPerRun(20, func() {
		_, allocErr = decode(packets[len(packets)-1])
	})
	if allocErr != nil {
		t.Fatalf("warmed decode: %v", allocErr)
	}
	if allocs != 0 {
		t.Fatalf("warmed decode allocated %g times/call", allocs)
	}
}

func makeCELTQEXTPacketForTransition(t *testing.T, opusDemo string, channels int) []byte {
	t.Helper()
	pcm := make([]float32, 960*channels)
	for i := 0; i < 960; i++ {
		tm := float64(i) / 48000.0
		left := float32(0.31*math.Sin(2*math.Pi*6200*tm) + 0.19*math.Sin(2*math.Pi*21800*tm))
		pcm[i*channels] = left
		if channels == 2 {
			pcm[i*channels+1] = float32(float64(left) * 0.87)
		}
	}
	return encodeLibopusPacketAtBitrate(t, opusDemo, channels, pcm, true, true, 256000)
}
