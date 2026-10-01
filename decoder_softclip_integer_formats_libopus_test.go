//go:build gopus_qext && !gopus_fixed_point && !gopus_osce && !gopus_dred

package gopus

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestIntegerFormatSoftClipLifecycleMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	for _, sampleRate := range []int{48000, 96000} {
		for _, channels := range []int{1, 2} {
			t.Run(fmt.Sprintf("%dk/%dch", sampleRate/1000, channels), func(t *testing.T) {
				base := encodeAPIRateCELTPacketFrameSize(t, channels, 960)
				padding := append([]byte{248}, bytes.Repeat([]byte{255}, 32)...)
				qext := append([]byte{base[0] | 3, 0x41, byte(len(padding))}, base[1:]...)
				packets := [][]byte{append(qext, padding...), base, base}
				formats := []uint32{
					libopustest.DecodeDiffFormatInt16,
					libopustest.DecodeDiffFormatInt24,
					libopustest.DecodeDiffFormatInt16,
				}
				frameSize := sampleRate / 50
				cases := make([]libopustest.DecodeDiffCase, len(packets))
				for i := range packets {
					cases[i] = libopustest.DecodeDiffCase{
						Packet:    packets[i],
						Format:    formats[i],
						FrameSize: uint32(frameSize),
					}
				}
				want, err := libopustest.ProbeDecodeSequence(sampleRate, channels, cases)
				if err != nil {
					libopustest.HelperUnavailable(t, "mixed integer decode", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatal(err)
				}
				out16 := make([]int16, frameSize*channels)
				out24 := make([]int32, frameSize*channels)
				decode := func(step int) (int, error) {
					var n int
					var err error
					if formats[step] == libopustest.DecodeDiffFormatInt16 {
						n, err = dec.DecodeInt16(packets[step], out16)
					} else {
						n, err = dec.DecodeInt24(packets[step], out24)
					}
					if err != nil || n != frameSize {
						return n, fmt.Errorf("step %d samples=%d err=%v", step, n, err)
					}
					return n, nil
				}
				compare := func(step int) error {
					n, err := decode(step)
					if err != nil {
						return err
					}
					if n != int(want[step].Code) {
						return fmt.Errorf("step %d samples=%d C=%d", step, n, want[step].Code)
					}
					if got, expected := dec.FinalRange(), want[step].FinalRange; got != expected {
						return fmt.Errorf("step %d final range=%08x want %08x", step, got, expected)
					}
					if formats[step] == libopustest.DecodeDiffFormatInt16 {
						expected := want[step].Int16()
						if len(expected) != n*channels {
							return fmt.Errorf("step %d C samples=%d want %d", step, len(expected), n*channels)
						}
						for i := 0; i < n*channels; i++ {
							if out16[i] != expected[i] {
								return fmt.Errorf("step %d int16[%d]=%d want %d", step, i, out16[i], expected[i])
							}
						}
					} else {
						expected := want[step].Int24()
						if len(expected) != n*channels {
							return fmt.Errorf("step %d C samples=%d want %d", step, len(expected), n*channels)
						}
						for i := 0; i < n*channels; i++ {
							if out24[i] != expected[i] {
								return fmt.Errorf("step %d int24[%d]=%d want %d", step, i, out24[i], expected[i])
							}
						}
					}
					return nil
				}

				if err := compare(0); err != nil {
					t.Fatal(err)
				}
				if dec.softClipMem == [2]float32{} {
					t.Fatal("first int16 packet did not establish soft-clip history")
				}
				if err := compare(1); err != nil {
					t.Fatal(err)
				}
				if dec.softClipMem != [2]float32{} {
					t.Fatalf("successful int24 decode left soft-clip history %v", dec.softClipMem)
				}
				if err := compare(2); err != nil {
					t.Fatal(err)
				}

				// Warm all public formats and keep the persistent mixed-format path
				// allocation-free after its first call.
				dec.Reset()
				for i := range packets {
					if _, err := decode(i); err != nil {
						t.Fatal(err)
					}
				}
				var decodeErr error
				allocs := testing.AllocsPerRun(20, func() {
					for i := range packets {
						_, decodeErr = decode(i)
						if decodeErr != nil {
							return
						}
					}
				})
				if decodeErr != nil {
					t.Fatal(decodeErr)
				}
				if allocs != 0 {
					t.Fatalf("mixed integer decode allocated %g times per sequence", allocs)
				}
			})
		}
	}
}
