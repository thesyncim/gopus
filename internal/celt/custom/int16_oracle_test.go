//go:build gopus_custom_modes

package custom_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/thesyncim/gopus/internal/celt/custom"
	"github.com/thesyncim/gopus/internal/libopustest"
)

// The public short decoder applies RES2INT16 after CELT synthesis. The selected
// C oracle calls opus_custom_decode on a fresh decoder for the same packet.
func TestOracleCustomInt16DecodeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	var cases []oracleCase
	for _, frameSize := range []int{120, 240, 480, 960} {
		for _, channels := range []int{1, 2} {
			for _, gain := range []float32{0.125, 1, 4} {
				pcm := generateSineStereo(440, 523.25, 48000, frameSize)
				if channels == 1 {
					pcm = generateSine(440, 48000, frameSize)
				}
				for i := range pcm {
					pcm[i] *= gain
				}
				cases = append(cases, oracleCase{48000, frameSize, channels, 400, pcm})
			}
		}
	}
	results := runCustomOracle(t, cases)
	clipped := false
	for i, tc := range cases {
		for _, sample := range results[i].decodedShort {
			clipped = clipped || sample == -32768 || sample == 32767
		}
		t.Run(fmt.Sprintf("frame%d/ch%d/gain%d", tc.frameSize, tc.channels, i%3), func(t *testing.T) {
			ref := results[i]
			if ref.status < 0 || len(ref.decodedShort) != tc.frameSize*tc.channels {
				t.Fatalf("C short decode: status=%d samples=%d want=%d", ref.status, len(ref.decodedShort), tc.frameSize*tc.channels)
			}
			mode, err := custom.NewMode(tc.fs, tc.frameSize)
			if err != nil {
				t.Fatal(err)
			}
			dec, err := custom.NewDecoder(mode, tc.channels)
			if err != nil {
				t.Fatal(err)
			}
			got, err := dec.Decode(ref.packet, tc.frameSize)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, ref.decodedShort) {
				if len(got) != len(ref.decodedShort) {
					t.Fatalf("samples=%d want=%d", len(got), len(ref.decodedShort))
				}
				for j := range got {
					if got[j] != ref.decodedShort[j] {
						t.Fatalf("sample %d: got=%d want=%d", j, got[j], ref.decodedShort[j])
					}
				}
			}
			if dec.FinalRange() != ref.shortDecRange || ref.shortDecRange != ref.decRange {
				t.Fatalf("range=%08x want=%08x C float=%08x", dec.FinalRange(), ref.shortDecRange, ref.decRange)
			}
		})
	}
	if !clipped {
		t.Fatal("high-amplitude cases do not exercise saturation")
	}
}
