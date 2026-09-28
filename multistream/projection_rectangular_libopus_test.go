package multistream

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestProjectionRectangularDecodeMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const rate, frameSize = 48000, 960
	for _, channels := range []int{2, 3} {
		t.Run(fmt.Sprintf("rows%d_cols%d", channels-1, channels), func(t *testing.T) {
			input := generateAmbisonicsSweep(channels, frameSize, 3)
			ref, err := encodeLibopusSurround(rate, channels, 1, 2049, 128000, false, true, 10, -1000, frameSize, 3, 4000, input, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "rectangular projection packets", err)
			}
			rows, cols := channels-1, ref.streams+ref.coupledStreams
			if cols != channels || len(ref.packets) != 3 {
				t.Fatal("unexpected C layout or packet count")
			}
			matrix := make([]byte, rows*cols*2)
			for col := range cols {
				for row := range rows {
					// Include nonzero coefficients in the unmapped last column.
					v := int16(16384/(row+1) - col*4096)
					binary.LittleEndian.PutUint16(matrix[(col*rows+row)*2:], uint16(v))
				}
			}
			packets := [][]byte{ref.packets[0], ref.packets[1], nil, ref.packets[2]}
			got, err := decodeProjectionFormatsWithGain(rate, rows, ref.streams, ref.coupledStreams, frameSize, 0, matrix, packets)
			if err != nil {
				t.Fatal(err)
			}
			wantFloat, err := decodeWithLibopusReferencePacketsGain(3, rate, rows, ref.streams, ref.coupledStreams, frameSize, 0, trivialMapping(rows), matrix, packets)
			if err != nil {
				t.Fatal(err)
			}
			assertProjectionFloatSampleExact(t, got.float32, wantFloat, "rectangular projection")
			want16, err := decodeWithLibopusReferencePacketsInt16Gain(3, rate, rows, ref.streams, ref.coupledStreams, frameSize, 0, trivialMapping(rows), matrix, packets)
			if err != nil {
				t.Fatal(err)
			}
			want24, err := decodeWithLibopusReferencePacketsInt24Gain(3, rate, rows, ref.streams, ref.coupledStreams, frameSize, 0, trivialMapping(rows), matrix, packets)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.int16) != len(want16) || len(got.int24) != len(want24) {
				t.Fatal("integer output lengths differ")
			}
			for i, want := range want16 {
				if got.int16[i] != want {
					t.Fatalf("int16[%d]=%d C=%d", i, got.int16[i], want)
				}
			}
			for i, want := range want24 {
				if got.int24[i] != want {
					t.Fatalf("int24[%d]=%d C=%d", i, got.int24[i], want)
				}
			}
			dec, err := NewProjectionDecoder(rate, rows, ref.streams, ref.coupledStreams, matrix)
			if err != nil {
				t.Fatal(err)
			}
			pcm := make([]float32, frameSize*rows)
			for range 3 {
				if _, err := dec.DecodeIntoFloat32(ref.packets[0], pcm, frameSize); err != nil {
					t.Fatal(err)
				}
			}
			if allocs := testing.AllocsPerRun(20, func() {
				if _, err := dec.DecodeIntoFloat32(ref.packets[0], pcm, frameSize); err != nil {
					panic(err)
				}
			}); allocs != 0 {
				t.Fatalf("warmed decode allocations=%g, want 0", allocs)
			}
		})
	}
}
