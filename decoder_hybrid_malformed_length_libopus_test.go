package gopus

import (
	"encoding/hex"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
)

// These malformed Hybrid packets declare redundancy beyond the packet end.
// opus_decode_frame keeps the entropy storage but sets the logical main length
// to zero, decoding the SILK lowband and concealing the CELT highband.
func TestHybridMalformedMainLengthMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	rates := []int{48000}
	if extsupport.QEXT {
		rates = append(rates, 96000)
	}
	for _, rate := range rates {
		for _, durationMS := range []int{10, 20} {
			frameSize := rate * durationMS / 1000
			good := encodeAPIRateHybridPacketFrameSize(t, 2, 48*durationMS)
			packetHexes := []string{
				"60825c38a0075e8e66c39989487af264b20624da",
				"60825c38a0075e8e66c39989486ef264b20624da",
			}
			if durationMS == 20 {
				packetHexes = []string{"78822eb7c2136d88ca1c827853c90bb9cb90a413fa1b9c476d5d20eb2f99eb3ee083f35a305ebd15faf960a9e081556ad0d42cf3fc73a94764f37efcaef43b27567656737aaec0914d6f6b9cdc0c08b5cffab01a0ee90c93f9a1edccb3a0524d471bfff74fa3a76810b30378372e7390b38928ee68fa958e36b8c2444afdea3cc85fb145aa3aa20eacc5e0e7e0053bab5869338f4bb9ded000000000000000000266908bc08578ce52243abc87bb5f0e14d276914669a8e2973486c0efa82eb89692"}
			}
			for witness, packetHex := range packetHexes {
				packet, err := hex.DecodeString(packetHex)
				if err != nil {
					t.Fatal(err)
				}
				for format := uint32(0); format < 3; format++ {
					t.Run(fmt.Sprintf("fs%d/ms%d/witness%d/format%d", rate, durationMS, witness, format), func(t *testing.T) {
						packets := [][]byte{packet, good, nil, good}
						cases := make([]libopustest.DecodeDiffCase, len(packets))
						for i, p := range packets {
							cases[i] = libopustest.DecodeDiffCase{Packet: p, Format: format, FrameSize: uint32(frameSize)}
						}
						want, err := libopustest.ProbeDecodeSequence(rate, 2, cases)
						if err != nil {
							t.Fatal(err)
						}
						if want[0].FinalRange != 0 {
							t.Fatalf("witness does not discard main CELT data: C range=%08x", want[0].FinalRange)
						}
						d, err := NewDecoder(DefaultDecoderConfig(rate, 2))
						if err != nil {
							t.Fatal(err)
						}
						pcm := make([]float32, frameSize*2)
						pcm16 := make([]int16, frameSize*2)
						pcm24 := make([]int32, frameSize*2)
						decode := func(p []byte) (int, error) {
							switch format {
							case libopustest.DecodeDiffFormatFloat32:
								return d.Decode(p, pcm)
							case libopustest.DecodeDiffFormatInt16:
								return d.DecodeInt16(p, pcm16)
							default:
								return d.DecodeInt24(p, pcm24)
							}
						}
						for step, p := range packets {
							n, err := decode(p)
							if err != nil || n != int(want[step].Code) {
								t.Fatalf("step%d samples=%d err=%v C=%d", step, n, err, want[step].Code)
							}
							if d.FinalRange() != want[step].FinalRange {
								t.Fatalf("step%d range=%08x C=%08x", step, d.FinalRange(), want[step].FinalRange)
							}
							switch format {
							case libopustest.DecodeDiffFormatFloat32:
								for i, v := range want[step].Float32() {
									if math.Float32bits(pcm[i]) != math.Float32bits(v) {
										t.Fatalf("step%d sample%d=%08x C=%08x", step, i, math.Float32bits(pcm[i]), math.Float32bits(v))
									}
								}
							case libopustest.DecodeDiffFormatInt16:
								for i, v := range want[step].Int16() {
									if pcm16[i] != v {
										t.Fatalf("step%d sample%d=%d C=%d", step, i, pcm16[i], v)
									}
								}
							default:
								for i, v := range want[step].Int24() {
									if pcm24[i] != v {
										t.Fatalf("step%d sample%d=%d C=%d", step, i, pcm24[i], v)
									}
								}
							}
						}
						if allocs := testing.AllocsPerRun(20, func() {
							for _, p := range packets {
								if _, err := decode(p); err != nil {
									panic(err)
								}
							}
						}); allocs != 0 {
							t.Fatalf("warm decode allocations=%g", allocs)
						}
					})
				}
			}
		}
	}
}
