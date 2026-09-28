//go:build gopus_custom_modes && gopus_fixed_point && !gopus_qext

package custom_test

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/celt/custom"
)

func TestFixedCustomDynamicModesSupported(t *testing.T) {
	for _, spec := range []struct{ fs, frame int }{{48000, 640}, {32000, 100}, {96000, 600}} {
		t.Run(fmt.Sprintf("fs%d_n%d", spec.fs, spec.frame), func(t *testing.T) {
			mode, err := custom.NewMode(spec.fs, spec.frame)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := custom.NewEncoder(mode, 1); err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			if _, err := custom.NewDecoder(mode, 1); err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
		})
	}
}

func TestFixedCustomWideStatefulParity(t *testing.T) {
	var cases []customSequenceCase
	for _, mode := range []struct{ fs, frame int }{{32000, 100}, {44100, 1024}, {96000, 600}} {
		for _, channels := range []int{1, 2} {
			tc := customSequenceCase{
				name: fmt.Sprintf("fs%d_n%d_ch%d", mode.fs, mode.frame, channels),
				fs:   mode.fs, frameSize: mode.frame, channels: channels, maxBytes: 200,
			}
			for frame := range 8 {
				op := customEncodeFrame
				if frame == 3 || frame == 4 {
					op = customLostFrame
				}
				if frame == 6 {
					op = customResetFrame
				}
				tc.records = append(tc.records, customSequenceRecord{
					op: op, pcm: customSequenceInput(mode.frame, channels, frame),
				})
			}
			cases = append(cases, tc)
		}
	}
	refs := runCustomSequenceOracle(t, cases)
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mode, err := custom.NewMode(tc.fs, tc.frameSize)
			if err != nil {
				t.Fatal(err)
			}
			if mode.NbEBands <= 21 {
				t.Fatalf("expected more than 21 bands, got %d", mode.NbEBands)
			}
			assertCustomSequenceParity(t, tc, refs[i])
		})
	}
}

func TestFixedCustomWideZeroAlloc(t *testing.T) {
	for _, spec := range []struct{ fs, frame int }{{44100, 1024}, {96000, 600}} {
		for _, channels := range []int{1, 2} {
			t.Run(fmt.Sprintf("fs%d_n%d_ch%d", spec.fs, spec.frame, channels), func(t *testing.T) {
				mode, err := custom.NewMode(spec.fs, spec.frame)
				if err != nil {
					t.Fatal(err)
				}
				if mode.NbEBands <= 21 {
					t.Fatalf("expected more than 21 bands, got %d", mode.NbEBands)
				}
				enc, err := custom.NewEncoder(mode, channels)
				if err != nil {
					t.Fatal(err)
				}
				dec, err := custom.NewDecoder(mode, channels)
				if err != nil {
					t.Fatal(err)
				}
				pcm := customSequenceInput(spec.frame, channels, 1)
				run := func() {
					packet, err := enc.EncodeFloat(pcm, 200)
					if err != nil {
						panic(err)
					}
					if _, err := dec.DecodeFloat(packet, spec.frame); err != nil {
						panic(err)
					}
				}
				for range 5 {
					run()
				}
				if got := testing.AllocsPerRun(100, run); got != 0 {
					t.Fatalf("warm encode/decode allocations=%g", got)
				}
				runLossRecovery := func() {
					run()
					for range 44 {
						if _, err := dec.DecodeFloat(nil, spec.frame); err != nil {
							panic(err)
						}
					}
					run()
				}
				runLossRecovery()
				if got := testing.AllocsPerRun(10, runLossRecovery); got != 0 {
					t.Fatalf("warm PLC/recovery allocations=%g", got)
				}
			})
		}
	}
}
