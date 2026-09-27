//go:build gopus_fixed_point && gopus_qext

package gopus_test

import (
	"fmt"
	"math"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/benchutil"
	"github.com/thesyncim/gopus/internal/libopustest"
)

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
	for _, sampleRate := range []int{48000, 96000} {
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
			if sampleRate == 48000 {
				packets = encodeNative48kQEXTPackets(t, opusDemo, channelCase.packetChannels, frames)
			} else {
				packets = encodeNative96kQEXTPackets(t, opusDemo, channelCase.packetChannels,
					native96kSine(channelCase.packetChannels, frames), 320000)
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
						compareFrame(frame)
						if got := dec.FinalRange(); got != ref.FinalRanges[frame] {
							t.Fatalf("frame %d final range=%08x want %08x", frame, got, ref.FinalRanges[frame])
						}
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
	for _, sampleRate := range []int{48000, 96000} {
		frameSize := sampleRate / 50
		packets := encodeQEXTPacketsAtNativeRate(t, opusDemo, sampleRate, 1, 2)
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

func encodeQEXTPacketsAtNativeRate(t *testing.T, opusDemo string, sampleRate, channels, frames int) [][]byte {
	t.Helper()
	if sampleRate == 48000 {
		return encodeNative48kQEXTPackets(t, opusDemo, channels, frames)
	}
	return encodeNative96kQEXTPackets(t, opusDemo, channels, native96kSine(channels, frames), 320000)
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
