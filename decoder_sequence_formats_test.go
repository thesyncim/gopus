package gopus

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func assertDecoderSequenceFormatsMatchSelectedLibopus(t *testing.T, rate, channels int, packets [][]byte) {
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
