package gopus

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestShortHybridTransitionPLCMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	seed := encodeAPIRateCELTPacketFrameSize(t, 1, 120)
	mutatedHybrid, err := hex.DecodeString("70a8f77ca6242c85d1dfe89250f7bcd05a152957cab69d04b1d02d2dfdcd23b79593e89ff61d1dcedaa4fb8ad2155e16052b21c563cc828769374b9334e79f124655123c13ca005d9c048c566b1cc473ccef7467dda10aa8d9612fd230288c14f3f1106a98d19a1b22c89b867b7027648f65e0ef6ba4cdcd2320bbd578e89ccbe410ebf9b12176d174ac92")
	if err != nil {
		t.Fatal(err)
	}
	shortCELT, err := hex.DecodeString("e470378d859b002ea26903dbf5ce78a48c66f7173b6e6bf546f1a3149c1d049e7c0768ab7279bc38cee724a0f0ca5b423816a563d6")
	if err != nil {
		t.Fatal(err)
	}
	if mutatedHybrid[0] != 0x70 || shortCELT[0] != 0xe4 {
		t.Fatalf("unexpected witness TOCs %02x/%02x", mutatedHybrid[0], shortCELT[0])
	}

	rates := []int{8000, 12000, 16000, 24000, 48000}
	if extsupport.QEXT {
		rates = append(rates, 96000)
	}
	for _, rate := range rates {
		for format := uint32(0); format < 3; format++ {
			t.Run(fmt.Sprintf("rate%d/format%d", rate, format), func(t *testing.T) {
				frameSizes := []int{rate / 400, rate / 100, rate / 400, rate / 400, rate / 400}
				packets := [][]byte{seed, mutatedHybrid, shortCELT, nil, shortCELT}
				cases := make([]libopustest.DecodeDiffCase, len(packets))
				for i, packet := range packets {
					cases[i] = libopustest.DecodeDiffCase{
						Packet:    packet,
						Format:    format,
						FrameSize: uint32(frameSizes[i]),
					}
				}
				want, err := libopustest.ProbeDecodeSequence(rate, 1, cases)
				if err != nil {
					t.Fatalf("ProbeDecodeSequence: %v", err)
				}
				wantCounts := []int32{int32(rate / 400), int32(rate / 100), int32(rate / 400), int32(rate / 400), int32(rate / 400)}
				for i := range want {
					if want[i].Code != wantCounts[i] {
						t.Fatalf("C step%d returned %d samples, want %d", i, want[i].Code, wantCounts[i])
					}
				}

				dec, err := NewDecoder(DefaultDecoderConfig(rate, 1))
				if err != nil {
					t.Fatal(err)
				}
				pcm32 := make([]float32, rate/50)
				pcm16 := make([]int16, rate/50)
				pcm24 := make([]int32, rate/50)
				decode := func(packet []byte, frameSize int) (int, error) {
					switch format {
					case libopustest.DecodeDiffFormatFloat32:
						return dec.Decode(packet, pcm32[:frameSize])
					case libopustest.DecodeDiffFormatInt16:
						return dec.DecodeInt16(packet, pcm16[:frameSize])
					default:
						return dec.DecodeInt24(packet, pcm24[:frameSize])
					}
				}
				compare := func(step, n int) {
					if dec.FinalRange() != want[step].FinalRange {
						t.Fatalf("step%d range=%08x C=%08x", step, dec.FinalRange(), want[step].FinalRange)
					}
					switch format {
					case libopustest.DecodeDiffFormatFloat32:
						for sample := 0; sample < n; sample++ {
							got := math.Float32bits(pcm32[sample])
							expected := binary.LittleEndian.Uint32(want[step].PCM[sample*4:])
							if got != expected {
								t.Fatalf("step%d sample%d=%08x C=%08x", step, sample, got, expected)
							}
						}
					case libopustest.DecodeDiffFormatInt16:
						for sample, expected := range want[step].Int16() {
							if pcm16[sample] != expected {
								t.Fatalf("step%d sample%d=%d C=%d", step, sample, pcm16[sample], expected)
							}
						}
					default:
						for sample, expected := range want[step].Int24() {
							if pcm24[sample] != expected {
								t.Fatalf("step%d sample%d=%d C=%d", step, sample, pcm24[sample], expected)
							}
						}
					}
				}

				for step, packet := range packets {
					n, err := decode(packet, frameSizes[step])
					if err != nil || int32(n) != want[step].Code {
						t.Fatalf("step%d returned (%d,%v), C returned %d", step, n, err, want[step].Code)
					}
					compare(step, n)
				}
				if allocs := testing.AllocsPerRun(10, func() {
					for step, packet := range packets {
						n, err := decode(packet, frameSizes[step])
						if err != nil || int32(n) != want[step].Code {
							panic(fmt.Sprintf("step%d returned (%d,%v), C returned %d", step, n, err, want[step].Code))
						}
					}
				}); allocs != 0 {
					t.Fatalf("warm sequence allocations=%g", allocs)
				}
			})
		}
	}
}
