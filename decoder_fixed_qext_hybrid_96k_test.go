//go:build gopus_fixed_point && gopus_qext

package gopus

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/benchutil"
	"github.com/thesyncim/gopus/internal/libopustest"
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
