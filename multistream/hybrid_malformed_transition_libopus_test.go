package multistream

import (
	"encoding/hex"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestMultistreamMalformedHybridTransitionMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	malformed, err := hex.DecodeString("60825c38a0075e8e66c39989487af264b20624da")
	if err != nil {
		t.Fatal(err)
	}
	celtPacket := encodeModeSwitchSingleStreamPackets(t, 1, 960, []encoder.Mode{encoder.ModeCELT})[0]
	rates := []int{48000}
	if extsupport.QEXT {
		rates = append(rates, 96000)
	}
	for _, rate := range rates {
		for _, channels := range []int{1, 2} {
			t.Run(fmt.Sprintf("fs%d/ch%d", rate, channels), func(t *testing.T) {
				assertMultistreamSequenceFormatsMatchSelectedLibopus(t, rate, channels, [][]byte{celtPacket, malformed, nil, celtPacket})
			})
		}
	}
}

func assertMultistreamSequenceFormatsMatchSelectedLibopus(t *testing.T, rate, channels int, packets [][]byte) {
	t.Helper()
	frameSize := rate / 50
	mapping := trivialMapping(channels)
	coupled := channels - 1
	cases := make([]libopustest.DecodeDiffCase, len(packets))
	for i, p := range packets {
		cases[i] = libopustest.DecodeDiffCase{Packet: p, FrameSize: uint32(frameSize)}
	}
	ranges, err := libopustest.ProbeDecodeSequence(rate, channels, cases)
	if err != nil {
		t.Fatal(err)
	}
	want, err := decodeWithLibopusReferencePackets(1, rate, channels, 1, coupled, frameSize, mapping, nil, packets)
	if err != nil {
		t.Fatal(err)
	}
	want16, err := decodeWithLibopusReferencePacketsInt16Gain(1, rate, channels, 1, coupled, frameSize, 0, mapping, nil, packets)
	if err != nil {
		t.Fatal(err)
	}
	want24, err := decodeWithLibopusReferencePacketsInt24Gain(1, rate, channels, 1, coupled, frameSize, 0, mapping, nil, packets)
	if err != nil {
		t.Fatal(err)
	}
	for format := 0; format < 3; format++ {
		t.Run(fmt.Sprintf("format%d", format), func(t *testing.T) {
			d, err := NewDecoder(rate, channels, 1, coupled, mapping)
			if err != nil {
				t.Fatal(err)
			}
			pcm := make([]float32, frameSize*channels)
			offset := 0
			for step, p := range packets {
				var n int
				switch format {
				case 0:
					n, err = d.DecodeIntoFloat32(p, pcm, frameSize)
					if err == nil {
						for i, v := range pcm[:n*channels] {
							if math.Float32bits(v) != math.Float32bits(want[offset+i]) {
								t.Fatalf("step%d sample%d=%08x C=%08x", step, i, math.Float32bits(v), math.Float32bits(want[offset+i]))
							}
						}
					}
				case 1:
					var out []int16
					out, err = d.DecodeToInt16(p, frameSize)
					n = len(out) / channels
					if err == nil {
						for i, v := range out {
							if v != want16[offset+i] {
								t.Fatalf("step%d sample%d=%d C=%d", step, i, v, want16[offset+i])
							}
						}
					}
				case 2:
					var out []int32
					out, err = d.DecodeToInt24(p, frameSize)
					n = len(out) / channels
					if err == nil {
						for i, v := range out {
							if v != want24[offset+i] {
								t.Fatalf("step%d sample%d=%d C=%d", step, i, v, want24[offset+i])
							}
						}
					}
				}
				if err != nil || n != int(ranges[step].Code) {
					t.Fatalf("step%d samples%d err%v C%d", step, n, err, ranges[step].Code)
				}
				if d.FinalRange() != ranges[step].FinalRange {
					t.Fatalf("step%d range%08x C%08x", step, d.FinalRange(), ranges[step].FinalRange)
				}
				offset += n * channels
			}
			if offset != len(want) || offset != len(want16) || offset != len(want24) {
				t.Fatalf("sample counts differ: Go%d C%d/%d/%d", offset, len(want), len(want16), len(want24))
			}
			if format == 0 {
				if allocs := testing.AllocsPerRun(20, func() {
					for _, p := range packets {
						if _, err := d.DecodeIntoFloat32(p, pcm, frameSize); err != nil {
							panic(err)
						}
					}
				}); allocs != 0 {
					t.Fatalf("warm decode allocations=%g", allocs)
				}
			}
		})
	}
}
