//go:build gopus_fixed_point && gopus_qext

package gopus

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

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
