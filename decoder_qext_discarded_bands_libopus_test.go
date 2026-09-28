//go:build gopus_qext

package gopus

import (
	"bytes"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// celt_decoder.c consumes all signaled QEXT bands even when the active 48 kHz
// mode can render only two. The discarded bands still affect entropy and history.
func TestQEXTDiscardedBandsMatchSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, hybridMode := range []bool{false, true} {
		for _, codedChannels := range []int{1, 2} {
			var base []byte
			if hybridMode {
				base = encodeAPIRateHybridPacketFrameSize(t, codedChannels, 960)
			} else {
				base = encodeAPIRateCELTPacketFrameSize(t, codedChannels, 960)
			}
			if base[0]&3 != 0 {
				t.Fatalf("expected one-frame packet, TOC=%02x", base[0])
			}
			for _, length := range []int{16, 32, 64, 128} {
				padding := append([]byte{248}, bytes.Repeat([]byte{255}, length)...)
				packet := append([]byte{base[0] | 3, 0x41, byte(len(padding))}, base[1:]...)
				packet = append(packet, padding...)
				for _, rate := range []int{48000, 96000} {
					for _, channels := range []int{1, 2} {
						t.Run(fmt.Sprintf("hybrid%t/coded%d/fs%d/api%d/bytes%d", hybridMode, codedChannels, rate, channels, length), func(t *testing.T) {
							assertQEXTSequenceFormatsMatchSelectedLibopus(t, rate, channels, [][]byte{packet, base, nil, base})
						})
					}
				}
			}
		}
	}
}

func assertQEXTSequenceFormatsMatchSelectedLibopus(t *testing.T, rate, channels int, packets [][]byte) {
	t.Helper()
	frameSize := rate / 50
	for format := uint32(0); format < 3; format++ {
		t.Run(fmt.Sprintf("format%d", format), func(t *testing.T) {
			cases := make([]libopustest.DecodeDiffCase, len(packets))
			for i, p := range packets {
				cases[i] = libopustest.DecodeDiffCase{Packet: p, Format: format, FrameSize: uint32(frameSize)}
			}
			want, err := libopustest.ProbeDecodeSequence(rate, channels, cases)
			if err != nil {
				t.Fatal(err)
			}
			d, err := NewDecoder(DefaultDecoderConfig(rate, channels))
			if err != nil {
				t.Fatal(err)
			}
			pcm := make([]float32, frameSize*channels)
			pcm16 := make([]int16, frameSize*channels)
			pcm24 := make([]int32, frameSize*channels)
			decode := func(p []byte) (int, error) {
				switch format {
				case 0:
					return d.Decode(p, pcm)
				case 1:
					return d.DecodeInt16(p, pcm16)
				default:
					return d.DecodeInt24(p, pcm24)
				}
			}
			for step, p := range packets {
				n, err := decode(p)
				if err != nil || n != int(want[step].Code) {
					t.Fatalf("step%d samples%d err%v C%d", step, n, err, want[step].Code)
				}
				if d.FinalRange() != want[step].FinalRange {
					t.Fatalf("step%d range%08x C%08x", step, d.FinalRange(), want[step].FinalRange)
				}
				switch format {
				case 0:
					for i, v := range want[step].Float32() {
						if math.Float32bits(pcm[i]) != math.Float32bits(v) {
							t.Fatalf("step%d sample%d=%08x C=%08x", step, i, math.Float32bits(pcm[i]), math.Float32bits(v))
						}
					}
				case 1:
					for i, v := range want[step].Int16() {
						if pcm16[i] != v {
							t.Fatalf("step%d sample%d=%d C=%d", step, i, pcm16[i], v)
						}
					}
				case 2:
					for i, v := range want[step].Int24() {
						if pcm24[i] != v {
							t.Fatalf("step%d sample%d=%d C=%d", step, i, pcm24[i], v)
						}
					}
				}
			}
			if allocs := testing.AllocsPerRun(10, func() {
				for _, p := range packets {
					if _, err := decode(p); err != nil {
						panic(err)
					}
				}
			}); allocs != 0 {
				t.Fatalf("warm allocations=%g", allocs)
			}
		})
	}
}
