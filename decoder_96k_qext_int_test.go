//go:build gopus_qext && !gopus_fixed_point

package gopus_test

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/benchutil"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestNative96kIntegerDecodeFormatsMatchQEXTOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "QEXT-enabled opus_demo", err)
	}

	const frames = 4
	for _, channels := range []int{1, 2} {
		packets := encodeNative96kQEXTPackets(t, opusDemo, channels, native96kSine(channels, frames), 320000)
		for _, format := range []uint32{libopustest.QEXTDecode96kFormatInt16, libopustest.QEXTDecode96kFormatInt24} {
			name := "int16"
			if format == libopustest.QEXTDecode96kFormatInt24 {
				name = "int24"
			}
			t.Run(fmt.Sprintf("%s/%dch", name, channels), func(t *testing.T) {
				ref, err := libopustest.ProbeQEXTDecode96k(libopustest.QEXTDecode96kParams{
					SampleFormat: format,
					Channels:     channels,
					MaxFrameSize: 1920,
					Packets:      packets,
				})
				if err != nil {
					libopustest.HelperUnavailable(t, "qext decode96k integer output", err)
				}
				frameSamples := 1920 * channels
				if len(ref.FinalRanges) != len(packets) {
					t.Fatalf("oracle ranges=%d packets=%d", len(ref.FinalRanges), len(packets))
				}
				dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(96000, channels))
				if err != nil {
					t.Fatalf("NewDecoder(96000, %d): %v", channels, err)
				}
				var decode func([]byte) (int, error)
				var compareFrame func(int)
				if format == libopustest.QEXTDecode96kFormatInt16 {
					out := make([]int16, frameSamples)
					decode = func(packet []byte) (int, error) { return dec.DecodeInt16(packet, out) }
					compareFrame = func(frame int) {
						start := frame * frameSamples
						for i, got := range out {
							if want := ref.Int16[start+i]; got != want {
								t.Fatalf("frame %d int16[%d]=%d want %d", frame, i, got, want)
							}
						}
					}
				} else {
					out := make([]int32, frameSamples)
					decode = func(packet []byte) (int, error) { return dec.DecodeInt24(packet, out) }
					compareFrame = func(frame int) {
						start := frame * frameSamples
						for i, got := range out {
							if want := ref.Int24[start+i]; got != want {
								t.Fatalf("frame %d int24[%d]=%d want %d", frame, i, got, want)
							}
						}
					}
				}

				for frame, packet := range packets {
					n, err := decode(packet)
					if err != nil || n != 1920 {
						t.Fatalf("frame %d decode samples=%d err=%v, want 1920,nil", frame, n, err)
					}
					if got := dec.FinalRange(); got != ref.FinalRanges[frame] {
						t.Fatalf("frame %d final range=%08x want %08x", frame, got, ref.FinalRanges[frame])
					}
					compareFrame(frame)
				}

				dec.Reset()
				if n, err := decode(packets[0]); err != nil || n != 1920 {
					t.Fatalf("reset replay samples=%d err=%v", n, err)
				}
				if got := dec.FinalRange(); got != ref.FinalRanges[0] {
					t.Fatalf("reset replay range=%08x want %08x", got, ref.FinalRanges[0])
				}
				compareFrame(0)

				var allocErr error
				allocs := testing.AllocsPerRun(40, func() {
					_, allocErr = decode(packets[0])
				})
				if allocErr != nil {
					t.Fatalf("warmed decode: %v", allocErr)
				}
				if allocs != 0 {
					t.Fatalf("warmed %s decode allocated %g times/call", name, allocs)
				}
			})
		}
	}
}

func TestNative96kIntegerDecodeSmallBufferPreservesState(t *testing.T) {
	libopustest.RequireOracle(t)
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "QEXT-enabled opus_demo", err)
	}

	const frames = 2
	for _, channels := range []int{1, 2} {
		packets := encodeNative96kQEXTPackets(t, opusDemo, channels, native96kSine(channels, frames), 320000)
		for _, format := range []uint32{libopustest.QEXTDecode96kFormatInt16, libopustest.QEXTDecode96kFormatInt24} {
			name := "int16"
			if format == libopustest.QEXTDecode96kFormatInt24 {
				name = "int24"
			}
			t.Run(fmt.Sprintf("%s/%dch", name, channels), func(t *testing.T) {
				ref, err := libopustest.ProbeQEXTDecode96k(libopustest.QEXTDecode96kParams{
					SampleFormat: format,
					Channels:     channels,
					MaxFrameSize: 1920,
					Packets:      packets,
				})
				if err != nil {
					libopustest.HelperUnavailable(t, "qext decode96k small-buffer oracle", err)
				}
				dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(96000, channels))
				if err != nil {
					t.Fatalf("NewDecoder(96000, %d): %v", channels, err)
				}
				frameSamples := 1920 * channels
				var out16 []int16
				var out24 []int32
				if format == libopustest.QEXTDecode96kFormatInt16 {
					out16 = make([]int16, frameSamples)
				} else {
					out24 = make([]int32, frameSamples)
				}
				decode := func(packet []byte, small bool) (int, error) {
					if format == libopustest.QEXTDecode96kFormatInt16 {
						out := out16
						if small {
							out = out[:frameSamples-1]
						}
						return dec.DecodeInt16(packet, out)
					}
					out := out24
					if small {
						out = out[:frameSamples-1]
					}
					return dec.DecodeInt24(packet, out)
				}
				if n, err := decode(packets[0], false); err != nil || n != 1920 {
					t.Fatalf("first frame samples=%d err=%v", n, err)
				}
				firstRange := dec.FinalRange()
				if firstRange != ref.FinalRanges[0] {
					t.Fatalf("first frame range=%08x want %08x", firstRange, ref.FinalRanges[0])
				}
				if n, err := decode(packets[1], true); err != gopus.ErrBufferTooSmall || n != 0 {
					t.Fatalf("undersized frame samples=%d err=%v, want 0,%v", n, err, gopus.ErrBufferTooSmall)
				}
				if got := dec.FinalRange(); got != firstRange {
					t.Fatalf("undersized frame changed range to %08x, want retained %08x", got, firstRange)
				}
				if n, err := decode(packets[1], false); err != nil || n != 1920 {
					t.Fatalf("received frame after short buffer samples=%d err=%v", n, err)
				}
				if got := dec.FinalRange(); got != ref.FinalRanges[1] {
					t.Fatalf("received frame after short buffer range=%08x want %08x", got, ref.FinalRanges[1])
				}
				start := frameSamples
				if format == libopustest.QEXTDecode96kFormatInt16 {
					for i, got := range out16 {
						if want := ref.Int16[start+i]; got != want {
							t.Fatalf("received frame after short buffer int16[%d]=%d want %d", i, got, want)
						}
					}
				} else {
					for i, got := range out24 {
						if want := ref.Int24[start+i]; got != want {
							t.Fatalf("received frame after short buffer int24[%d]=%d want %d", i, got, want)
						}
					}
				}
			})
		}
	}
}

func TestNative96kIntegerDecodeGainMatchesQEXTOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "QEXT-enabled opus_demo", err)
	}

	const frames = 3
	const gainQ8 = 8 * 256
	for _, channels := range []int{1, 2} {
		packets := encodeNative96kQEXTPackets(t, opusDemo, channels, native96kSine(channels, frames), 320000)
		for _, format := range []uint32{libopustest.QEXTDecode96kFormatInt16, libopustest.QEXTDecode96kFormatInt24} {
			name := "int16"
			if format == libopustest.QEXTDecode96kFormatInt24 {
				name = "int24"
			}
			t.Run(fmt.Sprintf("%s/%dch", name, channels), func(t *testing.T) {
				ref, err := libopustest.ProbeQEXTDecode96k(libopustest.QEXTDecode96kParams{
					SampleFormat: format,
					Channels:     channels,
					MaxFrameSize: 1920,
					GainQ8:       gainQ8,
					Packets:      packets,
				})
				if err != nil {
					libopustest.HelperUnavailable(t, "qext decode96k gain oracle", err)
				}
				dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(96000, channels))
				if err != nil {
					t.Fatalf("NewDecoder(96000, %d): %v", channels, err)
				}
				if err := dec.SetGain(gainQ8); err != nil {
					t.Fatalf("SetGain(%d): %v", gainQ8, err)
				}
				frameSamples := 1920 * channels
				if format == libopustest.QEXTDecode96kFormatInt16 {
					out := make([]int16, frameSamples)
					for frame, packet := range packets {
						n, err := dec.DecodeInt16(packet, out)
						if err != nil || n != 1920 {
							t.Fatalf("frame %d samples=%d err=%v", frame, n, err)
						}
						if dec.FinalRange() != ref.FinalRanges[frame] {
							t.Fatalf("frame %d range=%08x want %08x", frame, dec.FinalRange(), ref.FinalRanges[frame])
						}
						start := frame * frameSamples
						for i, got := range out {
							if want := ref.Int16[start+i]; got != want {
								t.Fatalf("frame %d int16[%d]=%d want %d", frame, i, got, want)
							}
						}
					}
					return
				}
				out := make([]int32, frameSamples)
				for frame, packet := range packets {
					n, err := dec.DecodeInt24(packet, out)
					if err != nil || n != 1920 {
						t.Fatalf("frame %d samples=%d err=%v", frame, n, err)
					}
					if dec.FinalRange() != ref.FinalRanges[frame] {
						t.Fatalf("frame %d range=%08x want %08x", frame, dec.FinalRange(), ref.FinalRanges[frame])
					}
					start := frame * frameSamples
					for i, got := range out {
						if want := ref.Int24[start+i]; got != want {
							t.Fatalf("frame %d int24[%d]=%d want %d", frame, i, got, want)
						}
					}
				}
			})
		}
	}
}

func TestNative96kMixedIntegerFormatsMatchQEXTOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "QEXT-enabled opus_demo", err)
	}

	const frames = 6
	const gainQ8 = 8 * 256
	for _, channels := range []int{1, 2} {
		packets := encodeNative96kQEXTPackets(t, opusDemo, channels, native96kSine(channels, frames), 320000)
		formats := make([]uint32, len(packets))
		for i := range formats {
			formats[i] = libopustest.QEXTDecode96kFormatInt16
			if i%2 != 0 {
				formats[i] = libopustest.QEXTDecode96kFormatInt24
			}
		}
		ref, err := libopustest.ProbeQEXTDecode96kMixed(libopustest.QEXTDecode96kParams{
			Channels:      channels,
			MaxFrameSize:  1920,
			GainQ8:        gainQ8,
			PacketFormats: formats,
			Packets:       packets,
		})
		if err != nil {
			libopustest.HelperUnavailable(t, "qext decode96k mixed-format helper", err)
		}
		frameSamples := 1920 * channels
		if len(ref.MixedInt32) != len(packets)*frameSamples || len(ref.FinalRanges) != len(packets) {
			t.Fatalf("%dch mixed oracle output: samples=%d ranges=%d", channels, len(ref.MixedInt32), len(ref.FinalRanges))
		}
		dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(96000, channels))
		if err != nil {
			t.Fatalf("NewDecoder(96000, %d): %v", channels, err)
		}
		if err := dec.SetGain(gainQ8); err != nil {
			t.Fatalf("SetGain(%d): %v", gainQ8, err)
		}
		out16 := make([]int16, frameSamples)
		out24 := make([]int32, frameSamples)
		decode := func(frame int) error {
			var n int
			var err error
			if formats[frame] == libopustest.QEXTDecode96kFormatInt16 {
				n, err = dec.DecodeInt16(packets[frame], out16)
			} else {
				n, err = dec.DecodeInt24(packets[frame], out24)
			}
			if err != nil || n != 1920 {
				return fmt.Errorf("frame %d samples=%d err=%v", frame, n, err)
			}
			if got, want := dec.FinalRange(), ref.FinalRanges[frame]; got != want {
				return fmt.Errorf("frame %d final range=%08x want %08x", frame, got, want)
			}
			start := frame * frameSamples
			for i := 0; i < frameSamples; i++ {
				got := out24[i]
				if formats[frame] == libopustest.QEXTDecode96kFormatInt16 {
					got = int32(out16[i])
				}
				if want := ref.MixedInt32[start+i]; got != want {
					return fmt.Errorf("frame %d sample[%d]=%d want %d", frame, i, got, want)
				}
			}
			return nil
		}

		for frame := range packets {
			if err := decode(frame); err != nil {
				t.Fatalf("%dch: %v", channels, err)
			}
		}
		dec.Reset()
		if err := decode(0); err != nil {
			t.Fatalf("%dch reset replay: %v", channels, err)
		}

		// Warm both public integer formats, then check their shared persistent
		// decoder path with caller-owned buffers.
		if _, err := dec.DecodeInt16(packets[0], out16); err != nil {
			t.Fatalf("%dch warm int16: %v", channels, err)
		}
		if _, err := dec.DecodeInt24(packets[1], out24); err != nil {
			t.Fatalf("%dch warm int24: %v", channels, err)
		}
		var decodeErr error
		allocs := testing.AllocsPerRun(30, func() {
			_, decodeErr = dec.DecodeInt16(packets[2], out16)
			if decodeErr == nil {
				_, decodeErr = dec.DecodeInt24(packets[3], out24)
			}
		})
		if decodeErr != nil {
			t.Fatalf("%dch warmed mixed decode: %v", channels, decodeErr)
		}
		if allocs != 0 {
			t.Fatalf("%dch warmed mixed int16/int24 decode allocated %g times/call", channels, allocs)
		}
	}
}
