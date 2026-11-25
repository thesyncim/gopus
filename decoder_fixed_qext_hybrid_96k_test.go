//go:build gopus_fixed_point && gopus_qext

package gopus

import (
	"fmt"
	"github.com/thesyncim/gopus/internal/benchutil"
	"github.com/thesyncim/gopus/internal/libopustest"
	"math"
	"math/rand"
	"testing"
)

func TestPublicFixedQEXTHybridNative96TransitionsMatchSelectedReference(t *testing.T) {
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
			packets := [][]byte{celtPacket, celtPacket, hybridPacket, hybridPacket,
				hybridPacket, hybridPacket, celtPacket, celtPacket, celtPacket}
			assertFixedQEXTHybridNative96Sequence(t, packets, channels)
		})
	}
}

func TestPublicFixedQEXTNative96SILKOutputMatchesSelectedReference(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			packets := encodeSelectedFixedQEXTRedundancyPackets(t, channels,
				[]int{libopustest.OpusForceModeSILKOnly, libopustest.OpusForceModeSILKOnly})
			for _, format := range []uint32{
				libopustest.QEXTDecode96kFormatFloat32,
				libopustest.QEXTDecode96kFormatInt16,
				libopustest.QEXTDecode96kFormatInt24,
			} {
				assertFixedQEXTDecodeSequence(t, channels, 96000, 1920, 0, false, packets, format)
			}
		})
	}
}

func TestPublicFixedQEXTHybridNative96RedundancyMatchesSelectedReference(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			for sequence, controls := range [][]int{
				{libopustest.OpusForceModeCELTOnly, libopustest.OpusForceModeCELTOnly,
					libopustest.OpusForceModeHybrid, libopustest.OpusForceModeHybrid,
					libopustest.OpusForceModeCELTOnly, libopustest.OpusForceModeCELTOnly},
				{libopustest.OpusForceModeCELTOnly, libopustest.OpusForceModeCELTOnly,
					libopustest.OpusForceModeHybrid, libopustest.OpusForceModeHybrid,
					libopustest.OpusForceModeCELTOnly, libopustest.OpusForceModeCELTOnly,
					libopustest.OpusForceModeHybrid, libopustest.OpusForceModeHybrid,
					libopustest.OpusForceModeCELTOnly, libopustest.OpusForceModeCELTOnly},
			} {
				t.Run(fmt.Sprintf("sequence%d", sequence+1), func(t *testing.T) {
					packets := encodeSelectedFixedQEXTRedundancyPackets(t, channels, controls)
					previousMode := libopustest.OpusForceModeCELTOnly
					for i, packet := range packets {
						wantMode := ModeHybrid
						if controls[i] == libopustest.OpusForceModeCELTOnly {
							wantMode = ModeCELT
							if previousMode != libopustest.OpusForceModeCELTOnly {
								wantMode = ModeHybrid
							}
						}
						if got := ParseTOC(packet[0]).Mode; got != wantMode {
							t.Fatalf("frame %d mode=%v, want %v", i, got, wantMode)
						}
						previousMode = controls[i]
					}
					assertFixedQEXTHybridNative96Sequence(t, packets, channels)
					assertFixedQEXTHybridNative96RedundancyCounters(t, packets, channels)
				})
			}
		})
	}
}

func assertFixedQEXTHybridNative96RedundancyCounters(t *testing.T, packets [][]byte, channels int) {
	t.Helper()
	dec, err := NewDecoder(DefaultDecoderConfig(96000, channels))
	if err != nil {
		t.Fatal(err)
	}
	frame := make([]int32, 1920*channels)
	for i, packet := range packets {
		if n, err := dec.DecodeInt24(packet, frame); err != nil || n != 1920 {
			t.Fatalf("counter witness frame %d decode=(%d,%v), want (1920,nil)", i, n, err)
		}
	}
	if dec.fixedRedundancySilkToCeltApplied == 0 || dec.fixedRedundancyCeltToSilkApplied == 0 {
		t.Fatalf("native-96 redundancy witness applied Silk→CELT=%d CELT→Silk=%d",
			dec.fixedRedundancySilkToCeltApplied, dec.fixedRedundancyCeltToSilkApplied)
	}
}

func assertFixedQEXTHybridNative96Sequence(t *testing.T, packets [][]byte, channels int) {
	t.Helper()
	for _, format := range []uint32{
		libopustest.QEXTDecode96kFormatFloat32,
		libopustest.QEXTDecode96kFormatInt16,
		libopustest.QEXTDecode96kFormatInt24,
	} {
		format := format
		name := map[uint32]string{
			libopustest.QEXTDecode96kFormatFloat32: "float32",
			libopustest.QEXTDecode96kFormatInt16:   "int16",
			libopustest.QEXTDecode96kFormatInt24:   "int24",
		}[format]
		t.Run(name, func(t *testing.T) {
			const frameSize = 1920
			want, err := libopustest.ProbeQEXTDecodeFixed(libopustest.QEXTDecode96kParams{
				SampleFormat: format,
				Channels:     channels,
				SampleRate:   96000,
				MaxFrameSize: frameSize,
				Packets:      packets,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "selected native-96 fixed-QEXT decoder", err)
				return
			}
			dec, err := NewDecoder(DefaultDecoderConfig(96000, channels))
			if err != nil {
				t.Fatal(err)
			}
			sampleCount := frameSize * channels
			outF32 := make([]float32, sampleCount)
			out16 := make([]int16, sampleCount)
			out24 := make([]int32, sampleCount)
			decode := func(packet []byte) (int, error) {
				switch format {
				case libopustest.QEXTDecode96kFormatInt16:
					return dec.DecodeInt16(packet, out16)
				case libopustest.QEXTDecode96kFormatInt24:
					return dec.DecodeInt24(packet, out24)
				default:
					return dec.Decode(packet, outF32)
				}
			}
			run := func() {
				for frame, packet := range packets {
					if n, err := decode(packet); err != nil || n != frameSize {
						t.Fatalf("frame %d decode=(%d,%v), want (%d,nil)", frame, n, err, frameSize)
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
			run()
			dec.Reset()
			run()
			var decodeErr error
			allocs := testing.AllocsPerRun(20, func() {
				_, decodeErr = decode(packets[len(packets)-1])
			})
			if decodeErr != nil {
				t.Fatalf("warmed native-96 decode: %v", decodeErr)
			}
			if allocs != 0 {
				t.Fatalf("warmed native-96 decode allocated %g times/call", allocs)
			}
		})
	}
}

func TestFixedQEXTHybridSWBMonoPLCRecoveryMatchesSelectedReference(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		frameCount = 9
		seed       = int64(34994603)
	)
	spec := encodeSweepSpec{
		name:        "hybrid_swb_ch1_10ms_24000bps_vbr0_fecfalse_dtxfalse",
		application: ApplicationVoIP,
		mode:        EncoderModeHybrid,
		bandwidth:   BandwidthSuperwideband,
		frameMs:     ExpertFrameDuration10Ms,
		bitrate:     24000,
		channels:    1,
		vbr:         BitrateModeVBR,
	}
	rng := rand.New(rand.NewSource(seed))
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  48000,
		Channels:    spec.channels,
		Application: spec.application,
	})
	if err != nil {
		t.Fatalf("NewEncoder(%s): %v", spec.name, err)
	}
	for _, configure := range []func() error{
		func() error { return enc.SetMode(spec.mode) },
		func() error { return enc.SetFrameSize(spec.frameSamples48k()) },
		func() error { return enc.SetExpertFrameDuration(spec.frameMs) },
		func() error { return enc.SetBandwidth(spec.bandwidth) },
		func() error { return enc.SetBitrate(spec.bitrate) },
		func() error { return enc.SetBitrateMode(spec.vbr) },
		func() error { return enc.SetComplexity(10) },
	} {
		if err := configure(); err != nil {
			t.Fatalf("configure %s: %v", spec.name, err)
		}
	}
	enc.SetFEC(false)
	enc.SetDTX(false)
	encoded := make([][]byte, frameCount)
	for frame := range encoded {
		pcm := genPCM(rng, spec.frameSamples48k(), spec.channels, 48000)
		packet, err := encodeOneFrame(enc, pcm)
		if err != nil {
			t.Fatalf("encode %s frame %d: %v", spec.name, frame, err)
		}
		encoded[frame] = append([]byte(nil), packet...)
	}
	loss := plcDropPattern(rng, frameCount)
	// The fixed seed preserves the PCM and loss schedule independently of the
	// broader encoder sweep's ordering.
	packets := make([][]byte, frameCount)
	for frame := range packets {
		if !loss[frame] {
			packets[frame] = encoded[frame]
		}
	}
	for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
		sampleRate := sampleRate
		t.Run(fmt.Sprintf("%dhz", sampleRate), func(t *testing.T) {
			frameSize := fixedFrameSamplesAtRate(spec, sampleRate)
			assertFixedDecodeSequence(t, sampleRate, spec.channels, frameSize, packets)

			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, spec.channels))
			if err != nil {
				t.Fatal(err)
			}
			out := make([]int16, frameSize*spec.channels)
			decodeSequence := func() error {
				dec.Reset()
				for _, packet := range packets {
					n, err := dec.DecodeInt16(packet, out)
					if err != nil {
						return err
					}
					if n != frameSize {
						return fmt.Errorf("decoded %d samples, want %d", n, frameSize)
					}
				}
				return nil
			}
			if err := decodeSequence(); err != nil {
				t.Fatal(err)
			}
			var decodeErr error
			allocs := testing.AllocsPerRun(20, func() {
				decodeErr = decodeSequence()
			})
			if decodeErr != nil {
				t.Fatalf("warmed PLC/recovery sequence: %v", decodeErr)
			}
			if allocs != 0 {
				t.Fatalf("warmed PLC/recovery sequence allocated %g times", allocs)
			}
		})
	}
}

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

func TestPublicFixedQEXTCELTShortFramesMatchSelectedReference(t *testing.T) {
	libopustest.RequireOracle(t)

	formats := []struct {
		name string
		kind uint32
	}{
		{name: "float32", kind: libopustest.QEXTDecode96kFormatFloat32},
		{name: "int16", kind: libopustest.QEXTDecode96kFormatInt16},
		{name: "int24", kind: libopustest.QEXTDecode96kFormatInt24},
	}
	for _, frameSize := range []int{960, 480, 240, 120} {
		for _, channels := range []int{1, 2} {
			name := map[int]string{960: "20ms", 480: "10ms", 240: "5ms", 120: "2.5ms"}[frameSize]
			t.Run(name+"/"+map[int]string{1: "mono", 2: "stereo"}[channels], func(t *testing.T) {
				const frames = 4
				inputFrames := make([]libopustest.OpusEncodeFixedMixedFrame, frames)
				for frame := 0; frame < frames; frame++ {
					pcm := make([]int16, frameSize*channels)
					for i := 0; i < frameSize; i++ {
						sample := frame*frameSize + i
						tm := float64(sample) / 48000
						left := 0.31*math.Sin(2*math.Pi*14000*tm+0.07*float64(frame)) +
							0.22*math.Sin(2*math.Pi*21300*tm+0.13)
						pcm[i*channels] = int16(math.Round(left * 32767))
						if channels == 2 {
							right := 0.28*math.Sin(2*math.Pi*17000*tm+0.19) +
								0.17*math.Sin(2*math.Pi*22000*tm+0.31*float64(frame))
							pcm[i*channels+1] = int16(math.Round(right * 32767))
						}
					}
					inputFrames[frame] = libopustest.OpusEncodeFixedMixedFrame{Format: 0, ShortPCM: pcm}
				}

				records, err := libopustest.ProbeOpusEncodeFixedQEXTMixedRecords(libopustest.OpusEncodeFixedParams{
					SampleRate: 48000, Channels: channels, Application: libopustest.OpusApplicationAudio,
					MaxPacketBytes: 4000, ForceMode: libopustest.OpusForceModeCELTOnly,
					Bandwidth: libopustest.OpusBandwidthFullband, Bitrate: 256000, Complexity: 10,
					ForceChannels: channels, LSBDepth: 24, FrameSize: frameSize, FrameCount: frames,
				}, inputFrames)
				if err != nil {
					libopustest.HelperUnavailable(t, "selected fixed-QEXT CELT encoder", err)
					return
				}
				packets := make([][]byte, len(records))
				for i, record := range records {
					if record.Status < 0 || len(record.Packet) == 0 {
						t.Fatalf("C encode frame %d status=%d packet length=%d", i, record.Status, len(record.Packet))
					}
					info, rawFrames, padding, frameCount, err := parsePacketFramesAndPadding(record.Packet)
					if err != nil {
						t.Fatalf("parse C packet %d: %v", i, err)
					}
					if len(rawFrames) != 1 || info.TOC.Mode != ModeCELT {
						t.Fatalf("C packet %d framing=(%d parsed, mode %v), want one CELT frame", i,
							len(rawFrames), info.TOC.Mode)
					}
					if len(padding) > 0 {
						if frameCount != 1 {
							t.Fatalf("C packet %d padding frame count=%d, want 1", i, frameCount)
						}
						ext, ok, err := findPacketExtension(padding, frameCount, qextPacketExtensionID)
						if err != nil {
							t.Fatalf("find C packet %d QEXT extension: %v", i, err)
						}
						if !ok || len(ext.Data) == 0 {
							t.Fatalf("C packet %d has padding but no QEXT payload", i)
						}
					} else if frameSize != 120 {
						t.Fatalf("C packet %d has no QEXT padding for CELT frame size %d", i, frameSize)
					}
					packets[i] = append([]byte(nil), record.Packet...)
				}

				for _, format := range formats {
					format := format
					t.Run(format.name, func(t *testing.T) {
						want, err := libopustest.ProbeQEXTDecodeFixed(libopustest.QEXTDecode96kParams{
							SampleFormat: format.kind, Channels: channels, SampleRate: 48000,
							MaxFrameSize: frameSize, Packets: packets,
						})
						if err != nil {
							libopustest.HelperUnavailable(t, "selected fixed-QEXT CELT decoder", err)
							return
						}
						if len(want.FinalRanges) != len(packets) {
							t.Fatalf("reference ranges=%d packets=%d", len(want.FinalRanges), len(packets))
						}

						dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
						if err != nil {
							t.Fatal(err)
						}
						count := frameSize * channels
						f32 := make([]float32, count)
						i16 := make([]int16, count)
						i24 := make([]int32, count)
						decode := func(packet []byte) (int, error) {
							switch format.kind {
							case libopustest.QEXTDecode96kFormatFloat32:
								return dec.Decode(packet, f32)
							case libopustest.QEXTDecode96kFormatInt16:
								return dec.DecodeInt16(packet, i16)
							default:
								return dec.DecodeInt24(packet, i24)
							}
						}
						compare := func(frame int) {
							start := frame * count
							switch format.kind {
							case libopustest.QEXTDecode96kFormatFloat32:
								for i, got := range f32 {
									if expected := want.PCM[start+i]; math.Float32bits(got) != math.Float32bits(expected) {
										t.Fatalf("frame %d float32[%d]=%08x want %08x", frame, i,
											math.Float32bits(got), math.Float32bits(expected))
									}
								}
							case libopustest.QEXTDecode96kFormatInt16:
								for i, got := range i16 {
									if expected := want.Int16[start+i]; got != expected {
										t.Fatalf("frame %d int16[%d]=%d want %d", frame, i, got, expected)
									}
								}
							default:
								for i, got := range i24 {
									if expected := want.Int24[start+i]; got != expected {
										t.Fatalf("frame %d int24[%d]=%d want %d", frame, i, got, expected)
									}
								}
							}
						}

						for frame, packet := range packets {
							n, err := decode(packet)
							if err != nil || n != frameSize {
								t.Fatalf("frame %d samples=%d err=%v, want %d,nil", frame, n, err, frameSize)
							}
							if got, expected := dec.FinalRange(), want.FinalRanges[frame]; got != expected {
								t.Fatalf("frame %d range=%08x want %08x", frame, got, expected)
							}
							compare(frame)
						}

						dec.Reset()
						n, err := decode(packets[0])
						if err != nil || n != frameSize || dec.FinalRange() != want.FinalRanges[0] {
							t.Fatalf("reset replay samples=%d range=%08x err=%v", n, dec.FinalRange(), err)
						}
						compare(0)

						var decodeErr error
						allocs := testing.AllocsPerRun(30, func() {
							_, decodeErr = decode(packets[0])
						})
						if decodeErr != nil {
							t.Fatalf("warmed decode: %v", decodeErr)
						}
						if allocs != 0 {
							t.Fatalf("warmed %s decode allocated %g times/call", format.name, allocs)
						}
					})
				}
			})
		}
	}
}
