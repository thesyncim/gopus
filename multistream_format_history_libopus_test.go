//go:build !gopus_fixed_point

package gopus

import (
	"fmt"
	"slices"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestMultistreamOutputFormatHistoryMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const rate, frameSize, gain = 48000, 960, 8192
	helper, err := multistreamReferenceDecoderPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "multistream mixed-format decode", err)
	}
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateCELTPacket(t, channels)
		for _, middle := range []uint32{libopustest.DecodeDiffFormatFloat32, libopustest.DecodeDiffFormatInt24} {
			for _, plc := range []bool{false, true} {
				t.Run(fmt.Sprintf("%dch/format%d/plc=%t", channels, middle, plc), func(t *testing.T) {
					middlePacket := packet
					if plc {
						middlePacket = nil
					}
					packets := [][]byte{packet, middlePacket, packet}
					mapping := []byte{0, 1}[:channels]
					formats := []uint32{libopustest.DecodeDiffFormatInt16, middle, libopustest.DecodeDiffFormatInt16}
					payload := libopustest.NewOraclePayloadVersion("GMSI", 6,
						rate, gain, 0, 1, uint32(channels), 1, uint32(channels-1),
						frameSize, uint32(len(formats)), uint32(channels), 0, 0)
					payload.Raw(mapping)
					for step, format := range formats {
						payload.U32(format)
						payload.U32(uint32(len(packets[step])))
						payload.Raw(packets[step])
					}
					want, err := libopustest.RunOracle(helper, payload.Bytes(), "multistream mixed-format decode", "GMSO")
					if err != nil {
						t.Fatal(err)
					}
					want.Count(frameSize * channels * 8) // int16 + float32/int24 + int16
					dec, err := NewMultistreamDecoder(rate, channels, 1, channels-1, mapping)
					if err != nil {
						t.Fatal(err)
					}
					if err := dec.SetGain(gain); err != nil {
						t.Fatal(err)
					}
					out16 := make([]int16, frameSize*channels)
					out24 := make([]int32, len(out16))
					outFloat := make([]float32, len(out16))
					decode := func(packet []byte, format uint32) (int, error) {
						switch format {
						case libopustest.DecodeDiffFormatFloat32:
							return dec.Decode(packet, outFloat)
						case libopustest.DecodeDiffFormatInt24:
							return dec.DecodeInt24(packet, out24)
						default:
							return dec.DecodeInt16(packet, out16)
						}
					}
					for step, format := range formats {
						if n, err := decode(packets[step], format); err != nil || n != frameSize {
							t.Fatalf("step %d: samples=%d err=%v", step, n, err)
						}
						if format == libopustest.DecodeDiffFormatInt16 {
							for i, got := range out16 {
								if expected := want.I16(); got != expected {
									t.Fatalf("step %d sample %d: Go=%d C=%d", step, i, got, expected)
								}
							}
						} else {
							want.Bytes(4 * len(out16))
						}
						if step == 0 {
							history := slices.Clone(dec.softClipMem)
							if _, err := dec.Decode(packet, outFloat[:1]); err != ErrBufferTooSmall {
								t.Fatalf("short float buffer: %v", err)
							}
							if _, err := dec.DecodeInt24(packet, out24[:1]); err != ErrBufferTooSmall {
								t.Fatalf("short int24 buffer: %v", err)
							}
							if !slices.Equal(history, dec.softClipMem) {
								t.Fatal("failed decode changed clipping history")
							}
							haveHistory := false
							for _, v := range dec.softClipMem {
								haveHistory = haveHistory || v != 0
							}
							if !haveHistory {
								t.Fatal("input did not establish clipping history")
							}
						}
					}
					if err := want.ExpectConsumed(); err != nil {
						t.Fatal(err)
					}
					if allocs := testing.AllocsPerRun(20, func() {
						for step, format := range formats {
							if _, err := decode(packets[step], format); err != nil {
								panic(err)
							}
						}
					}); allocs != 0 {
						t.Fatalf("mixed-format decode allocated %g times", allocs)
					}
				})
			}
		}
	}
}
