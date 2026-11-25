//go:build gopus_fixed_point && gopus_qext

package gopus

import (
	"bytes"
	"fmt"
	"github.com/thesyncim/gopus/internal/benchutil"
	internalencoder "github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
	"math"
	"math/rand"
	"os/exec"
	"path/filepath"
	"reflect"
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

func TestPublicFixedQEXT96kAutoChannelTransitionsMatchLibopus(t *testing.T) {
	testPublicFixedQEXTAutoChannelTransitionsMatchLibopus(t, 96000)
}

func TestPublicFixedQEXT48kAutoChannelTransitionsMatchLibopus(t *testing.T) {
	testPublicFixedQEXTAutoChannelTransitionsMatchLibopus(t, 48000)
}

func testPublicFixedQEXTAutoChannelTransitionsMatchLibopus(t *testing.T, sampleRate int) {
	t.Helper()
	libopustest.RequireOracle(t)
	const (
		maxPacket     = 4000
		complexity    = 10
		configuredLSB = 24
	)
	frameSize := sampleRate / 50
	bitrates := []int{15000, 25000, 14000, 17000, 23000}
	pcm := makeFixedQEXTInventoryPCM(sampleRate, 2, frameSize, len(bitrates))
	frames := make([]libopustest.FixedQEXTAutoChannelFrame, len(bitrates))
	for i, bitrate := range bitrates {
		lo := i * frameSize * 2
		frames[i] = libopustest.FixedQEXTAutoChannelFrame{
			Bitrate: bitrate,
			PCM:     pcm[lo : lo+frameSize*2],
		}
	}
	for _, qext := range []bool{false, true} {
		t.Run(fmt.Sprintf("%dk/qext_%t", sampleRate/1000, qext), func(t *testing.T) {
			want, err := libopustest.ProbeOpusEncodeFixedQEXTAutoChannelRecords(
				sampleRate, frameSize, maxPacket, complexity, configuredLSB, qext, frames)
			if err != nil {
				libopustest.HelperUnavailable(t, fmt.Sprintf("fixed-QEXT %d kHz auto-channel sequence", sampleRate/1000), err)
				return
			}
			enc, err := NewEncoder(EncoderConfig{SampleRate: sampleRate, Channels: 2, Application: ApplicationAudio})
			if err != nil {
				t.Fatal(err)
			}
			for _, set := range []func() error{
				func() error { return enc.SetMode(EncoderModeCELT) },
				func() error { return enc.SetBandwidth(BandwidthFullband) },
				func() error { return enc.SetMaxBandwidth(BandwidthFullband) },
				func() error { return enc.SetFrameSize(frameSize) },
				func() error { return enc.SetComplexity(complexity) },
				func() error { return enc.SetBitrateMode(BitrateModeVBR) },
				func() error { return enc.SetLSBDepth(configuredLSB) },
				func() error { return enc.SetQEXT(qext) },
			} {
				if err := set(); err != nil {
					t.Fatal(err)
				}
			}
			if enc.ForceChannels() != -1 {
				t.Fatalf("force channels=%d, want auto (-1)", enc.ForceChannels())
			}
			packet := make([]byte, maxPacket)
			for frame := range frames {
				if err := enc.SetBitrate(frames[frame].Bitrate); err != nil {
					t.Fatalf("frame %d SetBitrate: %v", frame, err)
				}
				n, err := enc.EncodeInt16(frames[frame].PCM, packet)
				if err != nil {
					t.Fatalf("frame %d EncodeInt16: %v", frame, err)
				}
				got := packet[:n]
				if want[frame].Status != 0 || n != len(want[frame].Packet) ||
					enc.FinalRange() != want[frame].FinalRange || !bytes.Equal(got, want[frame].Packet) {
					t.Fatalf("frame %d bitrate=%d packet{%s} range=%08x want=%08x",
						frame, frames[frame].Bitrate, fixedQEXTBytesDiff(got, want[frame].Packet),
						enc.FinalRange(), want[frame].FinalRange)
				}
			}

			// These 20 ms VBR rates select mono, stereo, mono, mono, then stereo.
			// Check C also exercises both hysteresis directions before packet
			// equality can pass vacuously.
			wantStereo := [...]bool{false, true, false, false, true}
			for frame, stereo := range wantStereo {
				if len(want[frame].Packet) == 0 || (want[frame].Packet[0]&0x04 != 0) != stereo {
					t.Fatalf("C frame %d bitrate=%d stereo TOC=%t, want %t",
						frame, frames[frame].Bitrate, len(want[frame].Packet) > 0 && want[frame].Packet[0]&0x04 != 0, stereo)
				}
			}
			if qext {
				encode := func() {
					if err := enc.SetBitrate(frames[1].Bitrate); err != nil {
						panic(err)
					}
					if _, err := enc.EncodeInt16(frames[1].PCM, packet); err != nil {
						panic(err)
					}
				}
				for range 4 {
					encode()
				}
				if allocs := testing.AllocsPerRun(20, encode); allocs != 0 {
					t.Fatalf("warmed fixed-QEXT auto-channel encode allocated %g objects at %d Hz", allocs, sampleRate)
				}
			}
		})
	}
}

func TestPublicFixedQEXT96kAutoChannelTransitionStateMatchesCELT(t *testing.T) {
	testPublicFixedQEXTAutoChannelTransitionStateMatchesCELT(t, 96000)
}

func TestPublicFixedQEXT48kAutoChannelTransitionStateMatchesCELT(t *testing.T) {
	testPublicFixedQEXTAutoChannelTransitionStateMatchesCELT(t, 48000)
}

func testPublicFixedQEXTAutoChannelTransitionStateMatchesCELT(t *testing.T, sampleRate int) {
	t.Helper()
	libopustest.RequireOracle(t)
	const (
		maxPacket     = 4000
		complexity    = 10
		configuredLSB = 24
	)
	frameSize := sampleRate / 50
	bitrates := []int{15000, 25000, 14000, 17000, 23000}
	pcm := makeFixedQEXTInventoryPCM(sampleRate, 2, frameSize, len(bitrates))
	frames := make([]libopustest.FixedQEXTAutoChannelFrame, len(bitrates))
	for i, bitrate := range bitrates {
		lo := i * frameSize * 2
		frames[i] = libopustest.FixedQEXTAutoChannelFrame{
			Bitrate: bitrate,
			PCM:     pcm[lo : lo+frameSize*2],
		}
	}

	for _, qext := range []bool{false, true} {
		t.Run(fmt.Sprintf("%dk/qext_%t", sampleRate/1000, qext), func(t *testing.T) {
			cPublic, err := libopustest.ProbeOpusEncodeFixedQEXTAutoChannelRecords(
				sampleRate, frameSize, maxPacket, complexity, configuredLSB, qext, frames)
			if err != nil {
				libopustest.HelperUnavailable(t, fmt.Sprintf("fixed-QEXT %d kHz auto-channel sequence", sampleRate/1000), err)
				return
			}
			enc, err := NewEncoder(EncoderConfig{SampleRate: sampleRate, Channels: 2, Application: ApplicationAudio})
			if err != nil {
				t.Fatal(err)
			}
			for _, set := range []func() error{
				func() error { return enc.SetMode(EncoderModeCELT) },
				func() error { return enc.SetBandwidth(BandwidthFullband) },
				func() error { return enc.SetMaxBandwidth(BandwidthFullband) },
				func() error { return enc.SetFrameSize(frameSize) },
				func() error { return enc.SetComplexity(complexity) },
				func() error { return enc.SetBitrateMode(BitrateModeVBR) },
				func() error { return enc.SetLSBDepth(configuredLSB) },
				func() error { return enc.SetQEXT(qext) },
			} {
				if err := set(); err != nil {
					t.Fatal(err)
				}
			}

			goFrames := make([]libopustest.CELTFixedQ8Frame, len(frames))
			goStates := make([]libopustest.CELTFixedQ8EncoderState, len(frames))
			goPackets := make([][]byte, len(frames))
			packet := make([]byte, maxPacket)
			for frame := range frames {
				if err := enc.SetBitrate(frames[frame].Bitrate); err != nil {
					t.Fatalf("frame %d SetBitrate: %v", frame, err)
				}
				lo := frame * frameSize * 2
				n, err := enc.EncodeInt16(pcm[lo:lo+frameSize*2], packet)
				if err != nil {
					t.Fatalf("frame %d EncodeInt16: %v", frame, err)
				}
				goPackets[frame] = append([]byte(nil), packet[:n]...)
				input := enc.enc.LastFixedCELTInputQ8()
				bitrate, maxBytes, lsbDepth := enc.enc.LastFixedCELTControls()
				if len(input) != frameSize*2 || maxBytes < 2 || bitrate <= 0 || lsbDepth != 16 {
					t.Fatalf("frame %d raw-Q8 controls/input: samples=%d bitrate=%d maxBytes=%d LSB=%d",
						frame, len(input), bitrate, maxBytes, lsbDepth)
				}
				streamChannels := int32(1)
				if goPackets[frame][0]&0x04 != 0 {
					streamChannels = 2
				}
				goFrames[frame] = libopustest.CELTFixedQ8Frame{
					PCM:            append([]int32(nil), input...),
					MaxBytes:       maxBytes,
					StreamChannels: streamChannels,
					Bitrate:        int32(bitrate),
					Analysis:       fixedQEXTOracleAnalysis(enc.enc.LastFixedCELTAnalysis()),
					SetPrediction:  true,
					Prediction:     2,
				}
				goStates[frame] = fixedQEXTGoStateSnapshot(enc, 2, sampleRate)
			}

			cFrames := make([]libopustest.CELTFixedQ8Frame, len(frames))
			sameInput := make([]bool, len(frames))
			for frame := range frames {
				c := cPublic[frame]
				goFrame := goFrames[frame]
				if c.CELTCalls != 1 || c.CELTFrameSize != frameSize || len(c.CELTInputQ8) != frameSize*2 {
					t.Fatalf("frame %d selected C CELT trace shape: calls=%d frameSize=%d samples=%d",
						frame, c.CELTCalls, c.CELTFrameSize, len(c.CELTInputQ8))
				}
				goEffectiveBytes := goFrame.MaxBytes
				cEffectiveBytes := c.CELTMaxBytes
				if qext {
					goEffectiveBytes = min(goEffectiveBytes, 3825)
					cEffectiveBytes = min(cEffectiveBytes, 3825)
				} else {
					goEffectiveBytes = min(goEffectiveBytes, 1275)
					cEffectiveBytes = min(cEffectiveBytes, 1275)
				}
				if c.CELTStreamChannels != goFrame.StreamChannels || c.CELTBitrate != goFrame.Bitrate ||
					c.CELTLSBDepth != 16 || cEffectiveBytes != goEffectiveBytes {
					t.Errorf("frame %d selected C/Go CELT controls: C{channels=%d bitrate=%d lsb=%d max=%d} Go{channels=%d bitrate=%d lsb=16 max=%d}",
						frame, c.CELTStreamChannels, c.CELTBitrate, c.CELTLSBDepth, c.CELTMaxBytes,
						goFrame.StreamChannels, goFrame.Bitrate, goFrame.MaxBytes)
				}
				inputDiff := -1
				for i := range c.CELTInputQ8 {
					if c.CELTInputQ8[i] != goFrame.PCM[i] {
						inputDiff = i
						break
					}
				}
				if inputDiff >= 0 {
					raw16 := pcm[frame*frameSize*2+inputDiff]
					t.Errorf("frame %d public CELT Q8 input differs at sample %d: Go=%d C=%d input16=%d inputQ8=%d",
						frame, inputDiff, goFrame.PCM[inputDiff], c.CELTInputQ8[inputDiff], raw16, int32(raw16)<<8)
				}
				sameInput[frame] = inputDiff < 0
				cFrames[frame] = goFrame
				cFrames[frame].PCM = append([]int32(nil), c.CELTInputQ8...)
				cFrames[frame].MaxBytes = c.CELTMaxBytes
				cFrames[frame].StreamChannels = c.CELTStreamChannels
				cFrames[frame].Bitrate = c.CELTBitrate
			}

			cTrace, err := libopustest.ProbeCELTFixedQEXTQ8State(libopustest.CELTFixedQ8Params{
				SampleRate: sampleRate, Channels: 2, StreamChannels: 2, FrameSize: frameSize,
				Start: 0, End: 21, Bitrate: int(cFrames[0].Bitrate), Complexity: complexity,
				LSBDepth: 16, VBR: true, QEXTEnabled: qext, Frames: cFrames,
			})
			if err != nil {
				t.Fatalf("selected-C per-frame channel state replay: %v", err)
			}
			for frame := range frames {
				cPacket := cTrace[frame].Packet
				if cPublic[frame].Packet[0]&0x03 == 3 {
					cMain, cSide := fixedQEXTPacketParts(t, append([]byte{cPublic[frame].Packet[0]}, cPacket...))
					publicMain, publicSide := fixedQEXTPacketParts(t, cPublic[frame].Packet)
					if !bytes.Equal(cMain, publicMain) || !bytes.Equal(cSide, publicSide) {
						t.Errorf("frame %d raw C vs public C: main{%s} side{%s}", frame,
							fixedQEXTBytesDiff(cMain, publicMain), fixedQEXTBytesDiff(cSide, publicSide))
					}
				} else {
					if !bytes.Equal(cPacket, cPublic[frame].Packet[1:]) {
						t.Errorf("frame %d raw C vs public C: %s", frame,
							fixedQEXTBytesDiff(cPacket, cPublic[frame].Packet[1:]))
					}
				}
				if cFrames[frame].StreamChannels == goFrames[frame].StreamChannels &&
					cFrames[frame].Bitrate == goFrames[frame].Bitrate &&
					min(cFrames[frame].MaxBytes, 3825) == min(goFrames[frame].MaxBytes, 3825) &&
					sameInput[frame] {
					stateDiff := fixedQEXTStateDifference(cTrace[frame].State, goStates[frame])
					if cTrace[frame].FinalRange != goStates[frame].RNG || stateDiff != "equal" {
						t.Errorf("frame %d same-input raw state: range=%08x/%08x state{%s}",
							frame, goStates[frame].RNG, cTrace[frame].FinalRange, stateDiff)
					}
				}
			}
		})
	}
}

func TestPublicFixedQEXT96kDurationsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		for _, frameSize := range []int{240, 480, 960, 1920} {
			for _, qext := range []bool{false, true} {
				for _, maxBytes := range []int{256, 4000} {
					for _, bitrateMode := range []BitrateMode{BitrateModeCBR, BitrateModeCVBR, BitrateModeVBR} {
						channels, frameSize, qext, maxBytes, bitrateMode := channels, frameSize, qext, maxBytes, bitrateMode
						t.Run(fmt.Sprintf("ch%d/frame%d/qext_%t/cap%d/bitrate_mode_%d", channels, frameSize, qext, maxBytes, bitrateMode), func(t *testing.T) {
							const (
								frameCount = 4
								bitrate    = 256000
							)
							pcm := makeFixedQEXTInventoryPCM(96000, channels, frameSize, frameCount)
							frames := make([]libopustest.OpusEncodeFixedMixedFrame, frameCount)
							for frame := range frames {
								lo := frame * frameSize * channels
								short := pcm[lo : lo+frameSize*channels]
								frames[frame].Format = uint32(frame % 3)
								switch frames[frame].Format {
								case 0:
									frames[frame].ShortPCM = short
								case 1:
									frames[frame].FloatPCM = make([]float32, len(short))
									for i, sample := range short {
										frames[frame].FloatPCM[i] = float32(sample) * (1.0 / 32768.0)
									}
								case 2:
									frames[frame].PCM24 = make([]int32, len(short))
									for i, sample := range short {
										frames[frame].PCM24[i] = int32(sample) << 8
									}
								}
							}
							frames[2].ResetBefore = true
							params := libopustest.OpusEncodeFixedParams{
								SampleRate: 96000, Channels: channels, Application: libopustest.OpusApplicationAudio,
								MaxPacketBytes: maxBytes, ForceMode: libopustest.OpusForceModeCELTOnly,
								Bandwidth: libopustest.OpusBandwidthFullband, Bitrate: bitrate, Complexity: 10,
								VBR: bitrateMode != BitrateModeCBR, VBRConstraint: bitrateMode == BitrateModeCVBR,
								ForceChannels: channels, LSBDepth: 24, FrameSize: frameSize,
							}
							var want []libopustest.OpusEncodeFixedRecord
							var err error
							if qext {
								want, err = libopustest.ProbeOpusEncodeFixedQEXTMixedRecords(params, frames)
							} else {
								want, err = libopustest.ProbeOpusEncodeFixedQEXTRuntimeOffMixedRecords(params, frames)
							}
							if err != nil {
								libopustest.HelperUnavailable(t, "fixed-QEXT native 96 kHz encoder", err)
								return
							}

							enc, err := NewEncoder(EncoderConfig{SampleRate: 96000, Channels: channels, Application: ApplicationAudio})
							if err != nil {
								t.Fatal(err)
							}
							for _, set := range []func() error{
								func() error { return enc.SetMode(EncoderModeCELT) },
								func() error { return enc.SetBandwidth(BandwidthFullband) },
								func() error { return enc.SetMaxBandwidth(BandwidthFullband) },
								func() error { return enc.SetFrameSize(frameSize) },
								func() error { return enc.SetBitrate(bitrate) },
								func() error { return enc.SetComplexity(10) },
								func() error { return enc.SetBitrateMode(bitrateMode) },
								func() error { return enc.SetForceChannels(channels) },
								func() error { return enc.SetLSBDepth(24) },
								func() error { return enc.SetQEXT(qext) },
							} {
								if err := set(); err != nil {
									t.Fatal(err)
								}
							}

							packet := make([]byte, maxBytes)
							for frame := range frameCount {
								if frames[frame].ResetBefore {
									enc.Reset()
								}
								var n int
								switch frames[frame].Format {
								case 0:
									n, err = enc.EncodeInt16(frames[frame].ShortPCM, packet)
								case 1:
									n, err = enc.Encode(frames[frame].FloatPCM, packet)
								case 2:
									n, err = enc.EncodeInt24(frames[frame].PCM24, packet)
								}
								if err != nil {
									t.Fatalf("frame %d EncodeInt16: %v", frame, err)
								}
								got := packet[:n]
								if want[frame].Status != 0 || n != len(want[frame].Packet) ||
									enc.FinalRange() != want[frame].FinalRange || !bytes.Equal(got, want[frame].Packet) {
									t.Fatalf("frame %d: packet{%s}, range got=%08x want=%08x, C status=%d",
										frame, fixedQEXTBytesDiff(got, want[frame].Packet), enc.FinalRange(),
										want[frame].FinalRange, want[frame].Status)
								}
							}

							var encodeErr error
							allocs := testing.AllocsPerRun(20, func() {
								if _, err := enc.EncodeInt16(pcm[:frameSize*channels], packet); err != nil {
									encodeErr = err
								}
							})
							if encodeErr != nil {
								t.Fatal(encodeErr)
							}
							if allocs != 0 {
								t.Fatalf("warm native 96 kHz encode allocs/run = %g, want 0", allocs)
							}
						})
					}
				}
			}
		}
	}
}

func TestPublicFixedQEXTWarmEncodeAllocations(t *testing.T) {
	for _, channels := range []int{1, 2} {
		t.Run(fmt.Sprintf("channels_%d", channels), func(t *testing.T) {
			const frameSize = 960
			enc, err := NewEncoder(EncoderConfig{
				SampleRate:  48000,
				Channels:    channels,
				Application: ApplicationAudio,
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, set := range []func() error{
				func() error { return enc.SetMode(EncoderModeCELT) },
				func() error { return enc.SetBandwidth(BandwidthFullband) },
				func() error { return enc.SetMaxBandwidth(BandwidthFullband) },
				func() error { return enc.SetFrameSize(frameSize) },
				func() error { return enc.SetBitrate(256000) },
				func() error { return enc.SetComplexity(10) },
				func() error { return enc.SetBitrateMode(BitrateModeCBR) },
				func() error { return enc.SetForceChannels(channels) },
				func() error { return enc.SetLSBDepth(24) },
				func() error { return enc.SetQEXT(true) },
			} {
				if err := set(); err != nil {
					t.Fatal(err)
				}
			}

			pcm := make([]int16, frameSize*channels)
			for i := range pcm {
				pcm[i] = int16((i*7919+17)%65536 - 32768)
			}
			packet := make([]byte, 4000)
			lastN := 0
			encode := func() {
				n, err := enc.EncodeInt16(pcm, packet)
				if err != nil {
					panic(err)
				}
				if n == 0 {
					panic("fixed QEXT encoder returned an empty packet")
				}
				lastN = n
			}
			for range 4 {
				encode()
			}
			if got := len(enc.enc.LastFixedCELTInputQ8()); got != frameSize*channels {
				t.Fatalf("QEXT frame bypassed the fixed CELT bridge: Q8 input length=%d want=%d", got, frameSize*channels)
			}
			_, qext := fixedQEXTPacketParts(t, packet[:lastN])
			if len(qext) == 0 {
				t.Fatal("fixed CELT frame did not produce a QEXT extension")
			}
			if allocs := testing.AllocsPerRun(20, encode); allocs != 0 {
				t.Fatalf("warmed public fixed-QEXT encode allocated %g objects", allocs)
			}
		})
	}
}

func TestPublicFixedQEXTPacketsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, sampleRate := range []int{48000, 96000} {
		for _, channels := range []int{1, 2} {
			sampleRate, channels := sampleRate, channels
			t.Run(fmt.Sprintf("rate_%d/channels_%d", sampleRate, channels), func(t *testing.T) {
				frameSize := sampleRate / 50
				const (
					frameCount = 4
					bitrate    = 256000
					maxBytes   = 4000
				)
				pcm := makeFixedQEXTInventoryPCM(sampleRate, channels, frameSize, frameCount)
				frames := make([]libopustest.OpusEncodeFixedMixedFrame, frameCount)
				for frame := range frames {
					lo := frame * frameSize * channels
					frames[frame] = libopustest.OpusEncodeFixedMixedFrame{
						Format: 0, ShortPCM: pcm[lo : lo+frameSize*channels],
					}
				}
				params := libopustest.OpusEncodeFixedParams{
					SampleRate: sampleRate, Channels: channels, Application: libopustest.OpusApplicationAudio,
					MaxPacketBytes: maxBytes, ForceMode: libopustest.OpusForceModeCELTOnly,
					Bandwidth: libopustest.OpusBandwidthFullband, Bitrate: bitrate, Complexity: 10,
					VBR: false, VBRConstraint: false, ForceChannels: channels,
					LSBDepth: 24, FrameSize: frameSize,
				}
				want, err := libopustest.ProbeOpusEncodeFixedQEXTMixedRecords(params, frames)
				if err != nil {
					libopustest.HelperUnavailable(t, "fixed-QEXT public encoder", err)
					return
				}

				enc, err := NewEncoder(EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: ApplicationAudio})
				if err != nil {
					t.Fatal(err)
				}
				for _, set := range []func() error{
					func() error { return enc.SetMode(EncoderModeCELT) },
					func() error { return enc.SetBandwidth(BandwidthFullband) },
					func() error { return enc.SetMaxBandwidth(BandwidthFullband) },
					func() error { return enc.SetFrameSize(frameSize) },
					func() error { return enc.SetBitrate(bitrate) },
					func() error { return enc.SetComplexity(10) },
					func() error { return enc.SetBitrateMode(BitrateModeCBR) },
					func() error { return enc.SetForceChannels(channels) },
					func() error { return enc.SetLSBDepth(24) },
					func() error { return enc.SetQEXT(true) },
				} {
					if err := set(); err != nil {
						t.Fatal(err)
					}
				}

				packet := make([]byte, maxBytes)
				cbrPacketBytes := bitrate * frameSize / (sampleRate * 8)
				wantCELTMaxBytes := cbrPacketBytes - 1 // The Opus TOC consumes one byte.
				q8Frames := make([]libopustest.CELTFixedQ8Frame, frameCount)
				goStates := make([]libopustest.CELTFixedQ8EncoderState, frameCount)
				goMain := make([][]byte, frameCount)
				goSide := make([][]byte, frameCount)
				goRanges := make([]uint32, frameCount)
				traceReady := true
				matched := 0
				for frame := range frameCount {
					lo := frame * frameSize * channels
					n, err := enc.EncodeInt16(pcm[lo:lo+frameSize*channels], packet)
					if err != nil {
						t.Fatalf("frame %d EncodeInt16: %v", frame, err)
					}
					got := packet[:n]
					input := enc.enc.LastFixedCELTInputQ8()
					if len(input) == frameSize*channels {
						analysis := enc.enc.LastFixedCELTAnalysis()
						q8Frames[frame] = libopustest.CELTFixedQ8Frame{
							PCM:      append([]int32(nil), input...),
							MaxBytes: wantCELTMaxBytes,
							Analysis: fixedQEXTOracleAnalysis(analysis),
						}
						goStates[frame] = fixedQEXTGoStateSnapshot(enc, channels, sampleRate)
						main, side := fixedQEXTPacketParts(t, got)
						goMain[frame] = append([]byte(nil), main...)
						goSide[frame] = append([]byte(nil), side...)
						goRanges[frame] = enc.FinalRange()
					} else if sampleRate == 48000 {
						traceReady = false
					}
					if want[frame].Status == 0 && n == len(want[frame].Packet) && enc.FinalRange() == want[frame].FinalRange && bytes.Equal(got, want[frame].Packet) {
						matched++
						continue
					}
					gotMain, gotQEXT := fixedQEXTPacketParts(t, got)
					wantMain, wantQEXT := fixedQEXTPacketParts(t, want[frame].Packet)
					gotBitrate, gotMaxBytes, gotLSBDepth := enc.enc.LastFixedCELTControls()
					analysis := enc.enc.LastFixedCELTAnalysis()
					t.Errorf("frame %d: public fixed-QEXT differs: packet{%s}; main{%s}; side{%s}; controls{%s}; input{%s}; range{got=%08x want=%08x}",
						frame,
						fixedQEXTBytesDiff(got, want[frame].Packet),
						fixedQEXTBytesDiff(gotMain, wantMain),
						fixedQEXTBytesDiff(gotQEXT, wantQEXT),
						fixedQEXTControlSummary(gotBitrate, gotMaxBytes, gotLSBDepth, wantCELTMaxBytes),
						fixedQEXTInputSummary(input, frameSize*channels, analysis.Valid),
						enc.FinalRange(), want[frame].FinalRange)
				}
				if traceReady {
					if sampleRate == 48000 {
						cAnalysis, err := probePublicFixedQEXTAnalysis(t, sampleRate, channels, frameSize, pcm)
						if err != nil {
							t.Errorf("same-input public analysis oracle: %v", err)
						} else {
							for frame, cInfo := range cAnalysis {
								if difference := fixedQEXTAnalysisDifference(cInfo, q8Frames[frame].Analysis); difference != "equal" {
									t.Errorf("same-input analysis frame %d: %s", frame, difference)
								}
							}
						}
					}
					trace, err := libopustest.ProbeCELTFixedQEXTQ8State(libopustest.CELTFixedQ8Params{
						SampleRate: sampleRate, Channels: channels, StreamChannels: channels, FrameSize: frameSize,
						Start: 0, End: 21, Bitrate: -1, Complexity: 10, LSBDepth: 16, QEXTEnabled: true, ConstrainedVBR: true,
						Frames: q8Frames,
					})
					if err != nil {
						t.Errorf("same-Q8 active-QEXT state replay: %v", err)
					} else {
						for frame, c := range trace {
							cMain, cSide := fixedQEXTPacketParts(t, append([]byte{want[frame].Packet[0]}, c.Packet...))
							stateDiff := fixedQEXTStateDifference(c.State, goStates[frame])
							if !bytes.Equal(cMain, goMain[frame]) || !bytes.Equal(cSide, goSide[frame]) || c.FinalRange != goRanges[frame] || stateDiff != "equal" {
								t.Errorf("same-Q8 active-QEXT frame %d: main{%s}; side{%s}; range=%08x/%08x; state{%s}",
									frame, fixedQEXTBytesDiff(goMain[frame], cMain), fixedQEXTBytesDiff(goSide[frame], cSide),
									goRanges[frame], c.FinalRange, stateDiff)
							} else {
								t.Logf("same-Q8 active-QEXT frame %d: raw CELT bytes, range, and persistent state match", frame)
							}
						}
					}
				}
				t.Logf("public fixed+QEXT exact packets=%d/%d at %d Hz, %d channels", matched, frameCount, sampleRate, channels)
			})
		}
	}
}

func TestPublicFixedQEXTConstraintPersistsAcrossCBR(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = sampleRate / 50
		frameCount = 3
		bitrate    = 256000
		maxBytes   = 4000
	)
	pcm := makeFixedQEXTInventoryPCM(sampleRate, channels, frameSize, frameCount)
	frames := make([]libopustest.OpusEncodeFixedMixedFrame, frameCount)
	for frame := range frames {
		lo := frame * frameSize * channels
		frames[frame].ShortPCM = pcm[lo : lo+frameSize*channels]
	}
	frames[1].VBRAction = libopustest.OpusVBRDisable

	for _, tc := range []struct {
		name        string
		startMode   BitrateMode
		constrained bool
		maskAndLFE  bool
	}{
		{name: "cvbr_to_cbr", startMode: BitrateModeCVBR, constrained: true},
		{name: "vbr_to_cbr", startMode: BitrateModeVBR},
		{name: "cvbr_to_cbr_with_lfe_mask", startMode: BitrateModeCVBR, constrained: true, maskAndLFE: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caseFrames := append([]libopustest.OpusEncodeFixedMixedFrame(nil), frames...)
			energyMask := make([]int32, 21)
			if tc.maskAndLFE {
				for band := range energyMask {
					energyMask[band] = -int32(band+1) * (1 << 20)
				}
				caseFrames[0].EnergyMaskAction = libopustest.OpusEnergyMaskSet
				caseFrames[0].EnergyMask = energyMask
				caseFrames[2].EnergyMaskAction = libopustest.OpusEnergyMaskClear
			}
			want, err := libopustest.ProbeOpusEncodeFixedQEXTMixedRecords(libopustest.OpusEncodeFixedParams{
				SampleRate: sampleRate, Channels: channels, Application: libopustest.OpusApplicationAudio,
				MaxPacketBytes: maxBytes, ForceMode: libopustest.OpusForceModeCELTOnly,
				Bandwidth: libopustest.OpusBandwidthFullband, Bitrate: bitrate, Complexity: 10,
				VBR: true, VBRConstraint: tc.constrained, ForceChannels: channels, LSBDepth: 24,
				LFE: tc.maskAndLFE, FrameSize: frameSize,
			}, caseFrames)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed-QEXT VBR transition encoder", err)
				return
			}

			enc, err := NewEncoder(EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: ApplicationAudio})
			if err != nil {
				t.Fatal(err)
			}
			for _, set := range []func() error{
				func() error { return enc.SetMode(EncoderModeCELT) },
				func() error { return enc.SetBandwidth(BandwidthFullband) },
				func() error { return enc.SetMaxBandwidth(BandwidthFullband) },
				func() error { return enc.SetFrameSize(frameSize) },
				func() error { return enc.SetBitrate(bitrate) },
				func() error { return enc.SetComplexity(10) },
				func() error { return enc.SetBitrateMode(tc.startMode) },
				func() error { return enc.SetForceChannels(channels) },
				func() error { return enc.SetLSBDepth(24) },
				func() error { return enc.SetQEXT(true) },
			} {
				if err := set(); err != nil {
					t.Fatal(err)
				}
			}
			if tc.maskAndLFE {
				enc.enc.SetLFE(true)
			}

			packet := make([]byte, maxBytes)
			for frame := range caseFrames {
				if frame == 1 {
					if err := enc.SetBitrateMode(BitrateModeCBR); err != nil {
						t.Fatal(err)
					}
				}
				if tc.maskAndLFE {
					switch caseFrames[frame].EnergyMaskAction {
					case libopustest.OpusEnergyMaskSet:
						enc.enc.SetCELTEnergyMaskQ24(energyMask)
					case libopustest.OpusEnergyMaskClear:
						enc.enc.SetCELTEnergyMaskQ24(nil)
					}
				}
				lo := frame * frameSize * channels
				n, err := enc.EncodeInt16(pcm[lo:lo+frameSize*channels], packet)
				if err != nil {
					t.Fatalf("frame %d EncodeInt16: %v", frame, err)
				}
				got := packet[:n]
				if want[frame].Status != 0 || n != len(want[frame].Packet) ||
					enc.FinalRange() != want[frame].FinalRange || !bytes.Equal(got, want[frame].Packet) {
					t.Errorf("frame %d: packet{%s}; final range=%08x want=%08x",
						frame, fixedQEXTBytesDiff(got, want[frame].Packet), enc.FinalRange(), want[frame].FinalRange)
				}
			}
		})
	}
}

func TestPublicFixedQEXTFrameSizeModeAndInputMatrix(t *testing.T) {
	libopustest.RequireOracle(t)
	type modeCase struct {
		name        string
		mode        BitrateMode
		vbr         bool
		constrained bool
	}
	modes := []modeCase{
		{name: "cbr", mode: BitrateModeCBR, constrained: true},
		{name: "cvbr", mode: BitrateModeCVBR, vbr: true, constrained: true},
		{name: "vbr", mode: BitrateModeVBR, vbr: true},
	}
	durations := []struct {
		name string
		size int
	}{
		{name: "2_5ms", size: 120},
		{name: "5ms", size: 240},
		{name: "10ms", size: 480},
		{name: "20ms", size: 960},
	}
	const sampleRate = 48000
	const bitrate = 640000
	const frameCount = 3
	for di, duration := range durations {
		for mi, mode := range modes {
			for _, channels := range []int{1, 2} {
				di, mi, duration, mode, channels := di, mi, duration, mode, channels
				inputFormat := uint32((di + mi + channels) % 3)
				maxPacketBytes := 4000
				if (di+mi+channels)%2 == 0 {
					maxPacketBytes = 256
				}
				resetBefore := (di+mi+channels)%2 == 1
				t.Run(fmt.Sprintf("%s/%s/ch%d/input%d/cap%d/reset=%t", duration.name, mode.name, channels, inputFormat, maxPacketBytes, resetBefore), func(t *testing.T) {
					pcm := makeFixedQEXTInventoryPCM(sampleRate, channels, duration.size, frameCount)
					frames := make([]libopustest.OpusEncodeFixedMixedFrame, frameCount)
					perFrame := duration.size * channels
					for frame := range frames {
						lo := frame * perFrame
						short := pcm[lo : lo+perFrame]
						frames[frame].Format = inputFormat
						frames[frame].ResetBefore = resetBefore && frame == 2
						switch inputFormat {
						case 0:
							frames[frame].ShortPCM = short
						case 1:
							frames[frame].FloatPCM = make([]float32, perFrame)
							for i, sample := range short {
								frames[frame].FloatPCM[i] = float32(sample) * (1.0 / 32768.0)
							}
						case 2:
							frames[frame].PCM24 = make([]int32, perFrame)
							for i, sample := range short {
								frames[frame].PCM24[i] = int32(sample) << 8
							}
						}
					}

					want, err := libopustest.ProbeOpusEncodeFixedQEXTMixedRecords(libopustest.OpusEncodeFixedParams{
						SampleRate: sampleRate, Channels: channels, Application: libopustest.OpusApplicationAudio,
						MaxPacketBytes: maxPacketBytes, ForceMode: libopustest.OpusForceModeCELTOnly,
						Bandwidth: libopustest.OpusBandwidthFullband, Bitrate: bitrate, Complexity: 10,
						VBR: mode.vbr, VBRConstraint: mode.constrained, ForceChannels: channels,
						LSBDepth: 24, FrameSize: duration.size,
					}, frames)
					if err != nil {
						libopustest.HelperUnavailable(t, "fixed-QEXT public frame-size/input matrix", err)
						return
					}

					enc, err := NewEncoder(EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: ApplicationAudio})
					if err != nil {
						t.Fatal(err)
					}
					for _, set := range []func() error{
						func() error { return enc.SetMode(EncoderModeCELT) },
						func() error { return enc.SetBandwidth(BandwidthFullband) },
						func() error { return enc.SetMaxBandwidth(BandwidthFullband) },
						func() error { return enc.SetFrameSize(duration.size) },
						func() error { return enc.SetBitrate(bitrate) },
						func() error { return enc.SetComplexity(10) },
						func() error { return enc.SetBitrateMode(mode.mode) },
						func() error { return enc.SetForceChannels(channels) },
						func() error { return enc.SetLSBDepth(24) },
						func() error { return enc.SetQEXT(true) },
					} {
						if err := set(); err != nil {
							t.Fatal(err)
						}
					}
					packet := make([]byte, maxPacketBytes)
					for frame := range frames {
						if frames[frame].ResetBefore {
							enc.Reset()
						}
						var n int
						switch inputFormat {
						case 0:
							n, err = enc.EncodeInt16(frames[frame].ShortPCM, packet)
						case 1:
							n, err = enc.Encode(frames[frame].FloatPCM, packet)
						case 2:
							n, err = enc.EncodeInt24(frames[frame].PCM24, packet)
						}
						if err != nil {
							t.Fatalf("frame %d encode: %v", frame, err)
						}
						if want[frame].Status != 0 || n != len(want[frame].Packet) ||
							enc.FinalRange() != want[frame].FinalRange || !bytes.Equal(packet[:n], want[frame].Packet) {
							t.Fatalf("frame %d: packet{%s}; final range=%08x want=%08x",
								frame, fixedQEXTBytesDiff(packet[:n], want[frame].Packet), enc.FinalRange(), want[frame].FinalRange)
						}
					}
				})
			}
		}
	}
}

func TestPublicFixedQEXTHighBudgetWarmEncodeAllocations(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 2
		frameSize  = 960
		bitrate    = 640000
	)
	enc, err := NewEncoder(EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: ApplicationAudio})
	if err != nil {
		t.Fatal(err)
	}
	for _, set := range []func() error{
		func() error { return enc.SetMode(EncoderModeCELT) },
		func() error { return enc.SetBandwidth(BandwidthFullband) },
		func() error { return enc.SetMaxBandwidth(BandwidthFullband) },
		func() error { return enc.SetFrameSize(frameSize) },
		func() error { return enc.SetBitrate(bitrate) },
		func() error { return enc.SetComplexity(10) },
		func() error { return enc.SetBitrateMode(BitrateModeCBR) },
		func() error { return enc.SetForceChannels(channels) },
		func() error { return enc.SetLSBDepth(24) },
		func() error { return enc.SetQEXT(true) },
	} {
		if err := set(); err != nil {
			t.Fatal(err)
		}
	}
	pcm := makeFixedQEXTInventoryPCM(sampleRate, channels, frameSize, 1)
	packet := make([]byte, 4000)
	encode := func() {
		n, err := enc.EncodeInt16(pcm, packet)
		if err != nil {
			panic(err)
		}
		if n <= 1276 {
			panic(fmt.Sprintf("high-budget fixed-QEXT output length=%d, want >1276", n))
		}
	}
	for range 4 {
		encode()
	}
	_, maxBytes, _ := enc.enc.LastFixedCELTControls()
	if maxBytes <= 1275 {
		t.Fatalf("QEXT CELT budget=%d, want caller capacity beyond 1275", maxBytes)
	}
	if allocs := testing.AllocsPerRun(20, encode); allocs != 0 {
		t.Fatalf("warmed high-budget fixed-QEXT encode allocated %g objects", allocs)
	}
}

func makeFixedQEXTInventoryPCM(sampleRate, channels, frameSize, frameCount int) []int16 {
	pcm := make([]int16, frameSize*channels*frameCount)
	for frame := 0; frame < frameCount; frame++ {
		for i := 0; i < frameSize; i++ {
			low := 0.34 * math.Sin(2*math.Pi*6000*float64(i)/float64(sampleRate))
			highFrequency := 30000.0
			if sampleRate == 48000 {
				highFrequency = 21000
			}
			high := 0.21 * math.Sin(2*math.Pi*highFrequency*float64(i)/float64(sampleRate))
			for ch := 0; ch < channels; ch++ {
				v := low + high*float64(ch+1)/float64(channels)
				pcm[(frame*frameSize+i)*channels+ch] = int16(math.Round(v * 32767))
			}
		}
	}
	return pcm
}

func fixedQEXTPacketParts(t *testing.T, packet []byte) (main, qext []byte) {
	t.Helper()
	if len(packet) < 2 || packet[0]&0x03 != 3 {
		t.Fatalf("QEXT packet is not code 3: len=%d packet=%x", len(packet), packet)
	}
	count := packet[1]
	if count&0x80 != 0 || count&0x3f != 1 {
		t.Fatalf("QEXT packet is not one-frame CBR: count byte=%02x", count)
	}
	frameStart := 2
	padding := 0
	if count&0x40 != 0 {
		for {
			if frameStart >= len(packet) {
				t.Fatalf("QEXT packet padding length overruns packet: %x", packet)
			}
			b := int(packet[frameStart])
			frameStart++
			if b == 255 {
				padding += 254
				continue
			}
			padding += b
			break
		}
	}
	end := len(packet) - padding
	if end < frameStart {
		t.Fatalf("invalid QEXT packet bounds start=%d end=%d len=%d", frameStart, end, len(packet))
	}
	main = packet[frameStart:end]
	if padding > 0 {
		var payloads [maxRepacketizerFrames][]byte
		collectQEXTPacketExtensions(packet[end:], 1, qextPacketExtensionID, &payloads)
		qext = payloads[0]
	}
	return main, qext
}

func firstPacketByteDiff(got, want []byte) int {
	limit := min(len(got), len(want))
	for i := 0; i < limit; i++ {
		if got[i] != want[i] {
			return i
		}
	}
	if len(got) != len(want) {
		return limit
	}
	return -1
}

func fixedQEXTBytesDiff(got, want []byte) string {
	first := firstPacketByteDiff(got, want)
	if first < 0 {
		return fmt.Sprintf("exact len=%d", len(got))
	}
	const context = 4
	gotStart := max(0, first-context)
	wantStart := max(0, first-context)
	gotEnd := min(len(got), first+context)
	wantEnd := min(len(want), first+context)
	return fmt.Sprintf("len=%d/%d first=%d got[%d:%d]=% x want[%d:%d]=% x",
		len(got), len(want), first,
		gotStart, gotEnd, got[gotStart:gotEnd],
		wantStart, wantEnd, want[wantStart:wantEnd])
}

func fixedQEXTControlSummary(gotBitrate, gotMaxBytes, gotLSBDepth, wantMaxBytes int) string {
	status := "ok"
	// C leaves the CELT target at OPUS_BITRATE_MAX in CBR and opus_encode
	// supplies the 16-bit input depth regardless of the configured depth ctl.
	if gotBitrate != -1 || gotLSBDepth != 16 || gotMaxBytes != wantMaxBytes {
		status = "mismatch"
	}
	return fmt.Sprintf("%s celtBitrate=%d/-1 maxBytes=%d/%d lsbDepth=%d/16", status,
		gotBitrate, gotMaxBytes, wantMaxBytes, gotLSBDepth)
}

func fixedQEXTInputSummary(input []int32, expected int, analysisValid bool) string {
	if len(input) != expected {
		return fmt.Sprintf("bridge-not-used len=%d want=%d", len(input), expected)
	}
	const samplePreview = 6
	end := min(len(input), samplePreview)
	return fmt.Sprintf("captured Q8 len=%d first[%d]=%v analysisValid=%t", len(input), end, input[:end], analysisValid)
}

func fixedQEXTOracleAnalysis(info internalencoder.AnalysisInfo) libopustest.CELTFixedQ8Analysis {
	return libopustest.CELTFixedQ8Analysis{
		Valid: info.Valid, Tonality: info.Tonality, TonalitySlope: info.TonalitySlope,
		Noisiness: info.NoisySpeech, Activity: info.Activity, MusicProb: info.MusicProb,
		MusicProbMin: info.MusicProbMin, MusicProbMax: info.MusicProbMax,
		Bandwidth: info.BandwidthIndex, ActivityProbability: info.VADProb,
		MaxPitchRatio: info.MaxPitchRatio, LeakBoost: info.LeakBoost,
	}
}

var publicFixedQEXTAnalysisHelper libopustest.HelperCache

func buildPublicFixedQEXTAnalysisHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:        "fixed-QEXT public int16 analysis",
		OutputBase:   "gopus_libopus_fixed_qext_analysis",
		SourceFile:   "libopus_analysis_info.c",
		FixedQEXTRef: true,
		CFlags:       []string{"-DHAVE_CONFIG_H", "-DGOPUS_REQUIRE_QEXT=1", "-O3", "-DNDEBUG"},
		RefIncludes:  []string{"include", "src", "celt", "silk", "silk/float"},
		Libs:         []string{libopustest.FixedQEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:    true,
	})
}

func probePublicFixedQEXTAnalysis(t *testing.T, sampleRate, channels, frameSize int, pcm []int16) ([]libopustest.CELTFixedQ8Analysis, error) {
	t.Helper()
	frameCount := len(pcm) / (channels * frameSize)
	if frameCount < 1 || frameCount*channels*frameSize != len(pcm) {
		return nil, fmt.Errorf("invalid analysis PCM size %d", len(pcm))
	}
	c2 := int32(-2)
	payload := libopustest.NewOraclePayloadVersion("GANI", 1,
		uint32(sampleRate), uint32(channels), uint32(frameSize), uint32(frameCount),
		16, 0, uint32(c2), 1, uint32(len(pcm)))
	for _, sample := range pcm {
		payload.I16(sample)
	}
	if len(pcm)&1 != 0 {
		payload.I16(0)
	}
	bin, err := publicFixedQEXTAnalysisHelper.Path(buildPublicFixedQEXTAnalysisHelper)
	if err != nil {
		return nil, err
	}
	reader, err := libopustest.RunOracle(bin, payload.Bytes(), "fixed-QEXT public int16 analysis", "GANO")
	if err != nil {
		return nil, err
	}
	reader.Count(frameCount)
	readInfo := func() libopustest.CELTFixedQ8Analysis {
		info := libopustest.CELTFixedQ8Analysis{Valid: reader.U32() != 0}
		info.Tonality = reader.Float32()
		info.TonalitySlope = reader.Float32()
		info.Noisiness = reader.Float32()
		info.Activity = reader.Float32()
		info.MusicProb = reader.Float32()
		info.MusicProbMin = reader.Float32()
		info.MusicProbMax = reader.Float32()
		info.Bandwidth = int32(reader.U32())
		info.ActivityProbability = reader.Float32()
		info.MaxPitchRatio = reader.Float32()
		_ = reader.U32() // reserved field in the trace record
		copy(info.LeakBoost[:], reader.Bytes(len(info.LeakBoost)))
		_ = reader.Bytes(1) // 20-byte C record aligns the 19 leak boosts.
		return info
	}
	infos := make([]libopustest.CELTFixedQ8Analysis, frameCount)
	for frame := range infos {
		infos[frame] = readInfo()
		_ = reader.Bytes(68 + 48 + 48) // latest info plus scalar and state hashes.
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return infos, nil
}

func fixedQEXTAnalysisDifference(c, goInfo libopustest.CELTFixedQ8Analysis) string {
	if c.Valid != goInfo.Valid {
		return fmt.Sprintf("valid C=%t Go=%t", c.Valid, goInfo.Valid)
	}
	for _, field := range []struct {
		name string
		c, g float32
	}{
		{"tonality", c.Tonality, goInfo.Tonality}, {"tonalitySlope", c.TonalitySlope, goInfo.TonalitySlope},
		{"noisiness", c.Noisiness, goInfo.Noisiness}, {"activity", c.Activity, goInfo.Activity},
		{"musicProb", c.MusicProb, goInfo.MusicProb}, {"musicProbMin", c.MusicProbMin, goInfo.MusicProbMin},
		{"musicProbMax", c.MusicProbMax, goInfo.MusicProbMax}, {"activityProbability", c.ActivityProbability, goInfo.ActivityProbability},
		{"maxPitchRatio", c.MaxPitchRatio, goInfo.MaxPitchRatio},
	} {
		if math.Float32bits(field.c) != math.Float32bits(field.g) {
			return fmt.Sprintf("%s C=%08x Go=%08x", field.name, math.Float32bits(field.c), math.Float32bits(field.g))
		}
	}
	if c.Bandwidth != goInfo.Bandwidth {
		return fmt.Sprintf("bandwidth C=%d Go=%d", c.Bandwidth, goInfo.Bandwidth)
	}
	for i := range c.LeakBoost {
		if c.LeakBoost[i] != goInfo.LeakBoost[i] {
			return fmt.Sprintf("leakBoost[%d] C=%d Go=%d", i, c.LeakBoost[i], goInfo.LeakBoost[i])
		}
	}
	return "equal"
}

func fixedQEXTGoStateSnapshot(public *Encoder, channels, sampleRate int) libopustest.CELTFixedQ8EncoderState {
	outer := reflect.ValueOf(public.enc).Elem()
	fixed := outer.FieldByName("fixedCELT")
	if fixed.IsNil() {
		panic("fixed CELT encoder state is nil")
	}
	root := fixed.Elem().FieldByName("enc").Elem()
	intField := func(name string) int32 {
		field := root.FieldByName(name)
		switch field.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return int32(field.Int())
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return int32(field.Uint())
		default:
			panic("unexpected fixed CELT state field kind: " + name)
		}
	}
	copySlice := func(name string, limit int) []int32 {
		field := root.FieldByName(name)
		if field.IsNil() {
			return nil
		}
		length := field.Len()
		if limit >= 0 && length > limit {
			length = limit
		}
		values := make([]int32, length)
		for i := range values {
			values[i] = int32(field.Index(i).Int())
		}
		return values
	}
	boolInt := func(name string) int32 {
		if root.FieldByName(name).Bool() {
			return 1
		}
		return 0
	}
	spreading := root.FieldByName("spreading")
	analysis := root.FieldByName("analysis")
	overlap, maxPeriod := 120, 1024
	if sampleRate == 96000 {
		overlap, maxPeriod = 240, 2048
	}
	floatBits := func(name string) uint32 {
		return math.Float32bits(float32(analysis.FieldByName(name).Float()))
	}
	state := libopustest.CELTFixedQ8EncoderState{
		RNG:              uint32(root.FieldByName("rng").Uint()),
		SpreadDecision:   intField("spreadDecision"),
		DelayedIntra:     intField("delayedIntra"),
		TonalAverage:     int32(spreading.FieldByName("TonalAverage").Int()),
		LastCodedBands:   intField("lastCodedBands"),
		HFAverage:        int32(spreading.FieldByName("HFAverage").Int()),
		TapsetDecision:   int32(spreading.FieldByName("TapsetDecision").Int()),
		PrefilterPeriod:  intField("prefilterPeriod"),
		PrefilterGain:    intField("prefilterGain"),
		PrefilterTapset:  intField("prefilterTapset"),
		ConsecTransient:  intField("consecTransient"),
		VBRReservoir:     intField("vbrReservoir"),
		VBRDrift:         intField("vbrDrift"),
		VBROffset:        intField("vbrOffset"),
		VBRCount:         intField("vbrCount"),
		OverlapMax:       intField("overlapMax"),
		StereoSaving:     intField("stereoSaving"),
		Intensity:        intField("intensity"),
		SpecAvg:          intField("specAvg"),
		ForceIntra:       boolInt("forceIntra"),
		DisablePrefilter: boolInt("disablePrefilter"),
		SilkSignalType:   intField("silkSignalType"),
		SilkOffset:       intField("silkOffset"),
		EnergyMask:       copySlice("energyMask", channels*21),
		PreemphMemE:      copySlice("preemphMemE", channels),
		InMem:            copySlice("inMem", channels*overlap),
		PrefilterMem:     copySlice("prefilterMem", channels*maxPeriod),
		OldBandE:         copySlice("oldBandE", channels*21),
		OldLogE:          copySlice("oldLogE", channels*21),
		OldLogE2:         copySlice("oldLogE2", channels*21),
		EnergyError:      copySlice("energyError", channels*21),
	}
	qext := root.FieldByName("qext")
	state.QEXTOldBandE = make([]int32, qext.FieldByName("oldBandE").Len())
	for i := range state.QEXTOldBandE {
		state.QEXTOldBandE[i] = int32(qext.FieldByName("oldBandE").Index(i).Int())
	}
	if analysis.FieldByName("Valid").Bool() {
		state.AnalysisValid = 1
	}
	state.AnalysisBandwidth = int32(analysis.FieldByName("Bandwidth").Int())
	state.AnalysisActivityBits = floatBits("Activity")
	state.AnalysisTonalityBits = floatBits("Tonality")
	state.AnalysisSlopeBits = floatBits("TonalitySlope")
	state.AnalysisMaxPitchRatioBits = floatBits("MaxPitchRatio")
	leakBoost := analysis.FieldByName("LeakBoost")
	for i := range state.AnalysisLeakBoost {
		state.AnalysisLeakBoost[i] = uint8(leakBoost.Index(i).Uint())
	}
	return state
}

func fixedQEXTStateDifference(c, goState libopustest.CELTFixedQ8EncoderState) string {
	if c.RNG != goState.RNG {
		return fmt.Sprintf("rng C=%08x Go=%08x", c.RNG, goState.RNG)
	}
	for _, field := range []string{"EnergyMask", "PreemphMemE", "InMem", "PrefilterMem"} {
		cv := reflect.ValueOf(c).FieldByName(field)
		gv := reflect.ValueOf(goState).FieldByName(field)
		if cv.Len() != gv.Len() {
			return fmt.Sprintf("%s length C=%d Go=%d", field, cv.Len(), gv.Len())
		}
		for i := 0; i < cv.Len(); i++ {
			if cv.Index(i).Int() != gv.Index(i).Int() {
				return fmt.Sprintf("%s[%d] C=%d Go=%d", field, i, cv.Index(i).Int(), gv.Index(i).Int())
			}
		}
	}
	for _, field := range []string{
		"SpreadDecision", "DelayedIntra", "TonalAverage", "LastCodedBands", "HFAverage", "TapsetDecision",
		"PrefilterPeriod", "PrefilterGain", "PrefilterTapset", "ConsecTransient", "VBRReservoir", "VBRDrift",
		"VBROffset", "VBRCount", "OverlapMax", "StereoSaving", "Intensity", "SpecAvg", "ForceIntra",
		"DisablePrefilter", "SilkSignalType", "SilkOffset", "AnalysisValid", "AnalysisBandwidth",
		"AnalysisActivityBits", "AnalysisTonalityBits", "AnalysisSlopeBits", "AnalysisMaxPitchRatioBits",
		"AnalysisLeakBoost",
	} {
		cv := reflect.ValueOf(c).FieldByName(field)
		gv := reflect.ValueOf(goState).FieldByName(field)
		if !reflect.DeepEqual(cv.Interface(), gv.Interface()) {
			return fmt.Sprintf("%s C=%v Go=%v", field, cv.Interface(), gv.Interface())
		}
	}
	for _, field := range []string{"OldBandE", "OldLogE", "OldLogE2", "EnergyError", "QEXTOldBandE"} {
		cv := reflect.ValueOf(c).FieldByName(field)
		gv := reflect.ValueOf(goState).FieldByName(field)
		if cv.Len() != gv.Len() {
			return fmt.Sprintf("%s length C=%d Go=%d", field, cv.Len(), gv.Len())
		}
		for i := 0; i < cv.Len(); i++ {
			if cv.Index(i).Int() != gv.Index(i).Int() {
				return fmt.Sprintf("%s[%d] C=%d Go=%d", field, i, cv.Index(i).Int(), gv.Index(i).Int())
			}
		}
	}
	return "equal"
}

func encodeLibopusQEXTMultiFramePacket(t *testing.T, opusDemo string, channels, frameSizeMs, bitrate int, pcm []float32) []byte {
	t.Helper()
	tmpDir := t.TempDir()
	inputPath := filepath.Join(tmpDir, "qext-multiframe.f32")
	packetPath := filepath.Join(tmpDir, "qext-multiframe.bit")
	if err := benchutil.WriteRepeatedRawFloat32(inputPath, pcm, 1); err != nil {
		t.Fatalf("write QEXT multiframe input: %v", err)
	}
	args := []string{
		"-e", "restricted-celt", "48000", fmt.Sprint(channels), fmt.Sprint(bitrate),
		"-f32", "-complexity", "10", "-bandwidth", "FB", "-framesize", fmt.Sprint(frameSizeMs),
		"-qext", "-cbr", inputPath, packetPath,
	}
	if output, err := exec.Command(opusDemo, args...).CombinedOutput(); err != nil {
		t.Fatalf("opus_demo encode %d ms QEXT packet: %v (%s)", frameSizeMs, err, output)
	}
	packet, err := firstOpusDemoPacket(packetPath)
	if err != nil {
		t.Fatalf("read %d ms QEXT packet: %v", frameSizeMs, err)
	}
	return packet
}

func TestFixedQEXTMultistreamDecodeUsesSidePayload(t *testing.T) {
	libopustest.RequireOracle(t)
	opusDemo, err := libopustest.PublicAPIOpusDemoPath()
	if err != nil {
		t.Fatalf("QEXT-enabled opus_demo: %v", err)
	}

	const sampleRate, channels, frameSize, packetCount = 48000, 2, 960, 4
	packet := encodeLibopusPacketAtBitrate(t, opusDemo, channels,
		qextSinePCM(channels, frameSize), true, true, 256000)
	_, _, padding, frameCount, err := parsePacketFramesAndPadding(packet)
	if err != nil {
		t.Fatalf("parse QEXT packet: %v", err)
	}
	if _, found, _ := findPacketExtension(padding, frameCount, qextPacketExtensionID); !found {
		t.Fatalf("reference packet has no CELT QEXT payload (TOC %#02x)", packet[0])
	}

	const streams, coupled = 1, 1
	mapping := []byte{0, 1}
	packets := make([][]byte, packetCount)
	for i := range packets {
		packets[i] = packet
	}
	want16, err := decodeLibopusMultistreamFixedInt16(sampleRate, channels, streams, coupled, frameSize, mapping, packets)
	if err != nil {
		t.Fatalf("selected fixed+QEXT C int16 decode: %v", err)
	}
	want24, err := decodeLibopusMultistreamFixedInt24(sampleRate, channels, streams, coupled, frameSize, mapping, packets)
	if err != nil {
		t.Fatalf("selected fixed+QEXT C int24 decode: %v", err)
	}

	dec16, err := NewMultistreamDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewMultistreamDecoder int16: %v", err)
	}
	got16 := make([]int32, 0, len(want16))
	frame16 := make([]int16, frameSize*channels)
	for i, packet := range packets {
		n, err := dec16.DecodeInt16(packet, frame16)
		if err != nil || n != frameSize {
			t.Fatalf("DecodeInt16 frame %d returned samples=%d, err=%v; want %d", i, n, err, frameSize)
		}
		got16 = append(got16, int16ToInt32(frame16)...)
	}
	assertFixedExact(t, "fixed+QEXT multistream int16", got16, int16ToInt32(want16))
	if allocs := testing.AllocsPerRun(100, func() {
		if n, err := dec16.DecodeInt16(packet, frame16); err != nil || n != frameSize {
			t.Fatalf("warm DecodeInt16 returned samples=%d, err=%v; want %d", n, err, frameSize)
		}
	}); allocs != 0 {
		t.Fatalf("warm fixed+QEXT multistream DecodeInt16 allocations=%g want 0", allocs)
	}

	dec24, err := NewMultistreamDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewMultistreamDecoder int24: %v", err)
	}
	got24 := make([]int32, 0, len(want24))
	frame24 := make([]int32, frameSize*channels)
	for i, packet := range packets {
		n, err := dec24.DecodeInt24(packet, frame24)
		if err != nil || n != frameSize {
			t.Fatalf("DecodeInt24 frame %d returned samples=%d, err=%v; want %d", i, n, err, frameSize)
		}
		got24 = append(got24, frame24...)
	}
	assertFixedExact(t, "fixed+QEXT multistream int24", got24, want24)
	if allocs := testing.AllocsPerRun(100, func() {
		if n, err := dec24.DecodeInt24(packet, frame24); err != nil || n != frameSize {
			t.Fatalf("warm DecodeInt24 returned samples=%d, err=%v; want %d", n, err, frameSize)
		}
	}); allocs != 0 {
		t.Fatalf("warm fixed+QEXT multistream DecodeInt24 allocations=%g want 0", allocs)
	}

	for _, gainQ8 := range []int{512, -512, 2048, -2048, 8192, -8192, 32767, -32768} {
		t.Run(fmt.Sprintf("decode_gain_%d", gainQ8), func(t *testing.T) {
			wantGain16, err := decodeLibopusMultistreamFixedInt16WithGain(sampleRate, channels, streams, coupled, frameSize, gainQ8, mapping, packets)
			if err != nil {
				t.Fatalf("selected fixed+QEXT C int16 decode with gain: %v", err)
			}
			wantGain24, err := decodeLibopusMultistreamFixedInt24WithGain(sampleRate, channels, streams, coupled, frameSize, gainQ8, mapping, packets)
			if err != nil {
				t.Fatalf("selected fixed+QEXT C int24 decode with gain: %v", err)
			}

			gainDec16, err := NewMultistreamDecoder(sampleRate, channels, streams, coupled, mapping)
			if err != nil {
				t.Fatalf("NewMultistreamDecoder gain int16: %v", err)
			}
			if err := gainDec16.SetGain(gainQ8); err != nil {
				t.Fatalf("SetGain(%d) int16: %v", gainQ8, err)
			}
			gotGain16 := make([]int32, 0, len(wantGain16))
			gainOut16 := make([]int16, frameSize*channels)
			for i, packet := range packets {
				if n, err := gainDec16.DecodeInt16(packet, gainOut16); err != nil || n != frameSize {
					t.Fatalf("gain DecodeInt16 packet %d returned samples=%d, err=%v; want %d", i, n, err, frameSize)
				}
				gotGain16 = append(gotGain16, int16ToInt32(gainOut16)...)
			}
			assertFixedExact(t, "fixed+QEXT multistream gained int16", gotGain16, int16ToInt32(wantGain16))
			if allocs := testing.AllocsPerRun(100, func() {
				if n, err := gainDec16.DecodeInt16(packet, gainOut16); err != nil || n != frameSize {
					t.Fatalf("warm gained DecodeInt16 returned samples=%d, err=%v; want %d", n, err, frameSize)
				}
			}); allocs != 0 {
				t.Fatalf("warm fixed+QEXT gained DecodeInt16 allocations=%g want 0", allocs)
			}

			gainDec24, err := NewMultistreamDecoder(sampleRate, channels, streams, coupled, mapping)
			if err != nil {
				t.Fatalf("NewMultistreamDecoder gain int24: %v", err)
			}
			if err := gainDec24.SetGain(gainQ8); err != nil {
				t.Fatalf("SetGain(%d) int24: %v", gainQ8, err)
			}
			gotGain24 := make([]int32, 0, len(wantGain24))
			gainOut24 := make([]int32, frameSize*channels)
			for i, packet := range packets {
				if n, err := gainDec24.DecodeInt24(packet, gainOut24); err != nil || n != frameSize {
					t.Fatalf("gain DecodeInt24 packet %d returned samples=%d, err=%v; want %d", i, n, err, frameSize)
				}
				gotGain24 = append(gotGain24, gainOut24...)
			}
			assertFixedExact(t, "fixed+QEXT multistream gained int24", gotGain24, wantGain24)
			if allocs := testing.AllocsPerRun(100, func() {
				if n, err := gainDec24.DecodeInt24(packet, gainOut24); err != nil || n != frameSize {
					t.Fatalf("warm gained DecodeInt24 returned samples=%d, err=%v; want %d", n, err, frameSize)
				}
			}); allocs != 0 {
				t.Fatalf("warm fixed+QEXT gained DecodeInt24 allocations=%g want 0", allocs)
			}
		})
	}

	t.Run("multi_frame", func(t *testing.T) {
		const multiFrameSize, frameSizeMs = 1920, 40
		multiPCM := qextSinePCM(channels, multiFrameSize)
		multiPacket := encodeLibopusQEXTMultiFramePacket(t, opusDemo, channels, frameSizeMs, 256000, multiPCM)
		_, _, multiPadding, multiFrames, err := parsePacketFramesAndPadding(multiPacket)
		if err != nil {
			t.Fatalf("parse QEXT multiframe packet: %v", err)
		}
		if multiFrames < 2 {
			t.Fatalf("opus_demo %d ms packet has %d frame(s), want at least 2", frameSizeMs, multiFrames)
		}
		if _, found, _ := findPacketExtension(multiPadding, multiFrames, qextPacketExtensionID); !found {
			t.Fatalf("reference multiframe packet has no QEXT payload (TOC %#02x)", multiPacket[0])
		}
		multiPackets := [][]byte{multiPacket, multiPacket}
		wantMulti16, err := decodeLibopusMultistreamFixedInt16(sampleRate, channels, streams, coupled, multiFrameSize, mapping, multiPackets)
		if err != nil {
			t.Fatalf("selected fixed+QEXT multiframe C int16 decode: %v", err)
		}
		wantMulti24, err := decodeLibopusMultistreamFixedInt24(sampleRate, channels, streams, coupled, multiFrameSize, mapping, multiPackets)
		if err != nil {
			t.Fatalf("selected fixed+QEXT multiframe C int24 decode: %v", err)
		}

		multiDec16, err := NewMultistreamDecoder(sampleRate, channels, streams, coupled, mapping)
		if err != nil {
			t.Fatalf("NewMultistreamDecoder multiframe int16: %v", err)
		}
		multiGot16 := make([]int32, 0, len(wantMulti16))
		multiOut16 := make([]int16, multiFrameSize*channels)
		for i := range multiPackets {
			n, err := multiDec16.DecodeInt16(multiPackets[i], multiOut16)
			if err != nil || n != multiFrameSize {
				t.Fatalf("multiframe DecodeInt16 packet %d returned samples=%d, err=%v; want %d", i, n, err, multiFrameSize)
			}
			multiGot16 = append(multiGot16, int16ToInt32(multiOut16)...)
		}
		assertFixedExact(t, "fixed+QEXT multiframe multistream int16", multiGot16, int16ToInt32(wantMulti16))
		if allocs := testing.AllocsPerRun(100, func() {
			if n, err := multiDec16.DecodeInt16(multiPacket, multiOut16); err != nil || n != multiFrameSize {
				t.Fatalf("warm multiframe DecodeInt16 returned samples=%d, err=%v; want %d", n, err, multiFrameSize)
			}
		}); allocs != 0 {
			t.Fatalf("warm fixed+QEXT multiframe multistream DecodeInt16 allocations=%g want 0", allocs)
		}

		multiDec24, err := NewMultistreamDecoder(sampleRate, channels, streams, coupled, mapping)
		if err != nil {
			t.Fatalf("NewMultistreamDecoder multiframe int24: %v", err)
		}
		multiGot24 := make([]int32, 0, len(wantMulti24))
		multiOut24 := make([]int32, multiFrameSize*channels)
		for i := range multiPackets {
			n, err := multiDec24.DecodeInt24(multiPackets[i], multiOut24)
			if err != nil || n != multiFrameSize {
				t.Fatalf("multiframe DecodeInt24 packet %d returned samples=%d, err=%v; want %d", i, n, err, multiFrameSize)
			}
			multiGot24 = append(multiGot24, multiOut24...)
		}
		assertFixedExact(t, "fixed+QEXT multiframe multistream int24", multiGot24, wantMulti24)
		if allocs := testing.AllocsPerRun(100, func() {
			if n, err := multiDec24.DecodeInt24(multiPacket, multiOut24); err != nil || n != multiFrameSize {
				t.Fatalf("warm multiframe DecodeInt24 returned samples=%d, err=%v; want %d", n, err, multiFrameSize)
			}
		}); allocs != 0 {
			t.Fatalf("warm fixed+QEXT multiframe multistream DecodeInt24 allocations=%g want 0", allocs)
		}
	})
}
