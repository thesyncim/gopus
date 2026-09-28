//go:build gopus_custom_modes && gopus_fixed_point

package custom_test

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/celt/custom"
)

func TestFixedCustomNonstandardFailsClosed(t *testing.T) {
	mode, err := custom.NewMode(48000, 640)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := custom.NewEncoder(mode, 1); !errors.Is(err, custom.ErrFixedCustomModeUnsupported) {
		t.Fatalf("encoder error=%v, want fixed-mode unsupported", err)
	}
	if _, err := custom.NewDecoder(mode, 1); !errors.Is(err, custom.ErrFixedCustomModeUnsupported) {
		t.Fatalf("decoder error=%v, want fixed-mode unsupported", err)
	}
}

func TestFixedCustomStandardStatefulParity(t *testing.T) {
	var cases []customSequenceCase
	for _, channels := range []int{1, 2} {
		caseData := customSequenceCase{
			name: fmt.Sprintf("48k_960_ch%d", channels),
			fs:   48000, frameSize: 960, channels: channels, maxBytes: 200,
		}
		for frame := range 9 {
			op := customEncodeFrame
			if frame == 3 || frame == 4 {
				op = customLostFrame
			}
			if frame == 6 {
				op = customResetFrame
			}
			caseData.records = append(caseData.records, customSequenceRecord{
				op: op, pcm: customSequenceInput(960, channels, frame),
			})
		}
		cases = append(cases, caseData)
	}
	refs := runCustomSequenceOracle(t, cases)
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mode, err := custom.NewMode(tc.fs, tc.frameSize)
			if err != nil {
				t.Fatal(err)
			}
			enc, err := custom.NewEncoder(mode, tc.channels)
			if err != nil {
				t.Fatal(err)
			}
			dec, err := custom.NewDecoder(mode, tc.channels)
			if err != nil {
				t.Fatal(err)
			}
			dec16, err := custom.NewDecoder(mode, tc.channels)
			if err != nil {
				t.Fatal(err)
			}
			for frame, rec := range tc.records {
				ref := refs[i][frame]
				if rec.op == customResetFrame {
					enc.Reset()
					dec.Reset()
					dec16.Reset()
				}
				if rec.op != customLostFrame {
					packet, err := enc.EncodeFloat(rec.pcm, tc.maxBytes)
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(packet, ref.packet) || enc.FinalRange() != ref.encRange {
						t.Fatalf("frame %d packet/range differs: Go=%x/%08x C=%x/%08x", frame, packet, enc.FinalRange(), ref.packet, ref.encRange)
					}
				}
				var packet []byte
				if rec.op != customLostFrame {
					packet = ref.packet
				}
				pcm, err := dec.DecodeFloat(packet, tc.frameSize)
				if err != nil {
					t.Fatal(err)
				}
				if dec.FinalRange() != ref.decRange || len(pcm) != len(ref.pcm) {
					t.Fatalf("frame %d float range/count=%08x/%d want %08x/%d", frame, dec.FinalRange(), len(pcm), ref.decRange, len(ref.pcm))
				}
				assertCustomDecodeExact(t, fmt.Sprintf("frame %d", frame), pcm, ref.pcm)
				pcm16, err := dec16.Decode(packet, tc.frameSize)
				if err != nil {
					t.Fatal(err)
				}
				if dec16.FinalRange() != ref.intRange || len(pcm16) != len(ref.pcm16) {
					t.Fatalf("frame %d int16 range/count=%08x/%d want %08x/%d", frame, dec16.FinalRange(), len(pcm16), ref.intRange, len(ref.pcm16))
				}
				for j, sample := range pcm16 {
					if sample != ref.pcm16[j] {
						t.Fatalf("frame %d int16[%d]=%d want %d", frame, j, sample, ref.pcm16[j])
					}
				}
			}
		})
	}
}

func TestFixedCustomScaledStatefulParity(t *testing.T) {
	var cases []customSequenceCase
	for _, mode := range []struct{ fs, frame int }{{32000, 640}, {16000, 320}} {
		for _, channels := range []int{1, 2} {
			tc := customSequenceCase{
				name: fmt.Sprintf("fs%d_n%d_ch%d", mode.fs, mode.frame, channels),
				fs:   mode.fs, frameSize: mode.frame, channels: channels, maxBytes: 200,
			}
			for frame := range 9 {
				op := customEncodeFrame
				if frame == 3 || frame == 4 {
					op = customLostFrame
				}
				if frame == 6 {
					op = customResetFrame
				}
				tc.records = append(tc.records, customSequenceRecord{op: op, pcm: customSequenceInput(mode.frame, channels, frame)})
			}
			cases = append(cases, tc)
		}
	}
	refs := runCustomSequenceOracle(t, cases)
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assertCustomSequenceParity(t, tc, refs[i]) })
	}
}

func TestFixedCustomStandardZeroAlloc(t *testing.T) {
	mode, err := custom.NewMode(48000, 960)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := custom.NewEncoder(mode, 1)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := custom.NewDecoder(mode, 1)
	if err != nil {
		t.Fatal(err)
	}
	pcm := customSequenceInput(960, 1, 0)
	for range 3 {
		packet, err := enc.EncodeFloat(pcm, 200)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := dec.DecodeFloat(packet, 960); err != nil {
			t.Fatal(err)
		}
	}
	var runErr error
	allocs := testing.AllocsPerRun(100, func() {
		packet, err := enc.EncodeFloat(pcm, 200)
		if err != nil {
			runErr = err
			return
		}
		_, runErr = dec.DecodeFloat(packet, 960)
	})
	if runErr != nil {
		t.Fatal(runErr)
	}
	if allocs != 0 {
		t.Fatalf("steady-state custom fixed encode/decode allocated %g times", allocs)
	}
}

func TestFixedCustomScaledZeroAlloc(t *testing.T) {
	mode, err := custom.NewMode(32000, 640)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := custom.NewEncoder(mode, 2)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := custom.NewDecoder(mode, 2)
	if err != nil {
		t.Fatal(err)
	}
	pcm := customSequenceInput(640, 2, 1)
	run := func() {
		packet, err := enc.EncodeFloat(pcm, 200)
		if err != nil {
			panic(err)
		}
		if _, err := dec.DecodeFloat(packet, 640); err != nil {
			panic(err)
		}
	}
	for range 5 {
		run()
	}
	if got := testing.AllocsPerRun(100, run); got != 0 {
		t.Fatalf("scaled fixed encode/decode allocated %g times", got)
	}
	lossAndRecover := func() {
		if _, err := dec.DecodeFloat(nil, 640); err != nil {
			panic(err)
		}
		run()
	}
	lossAndRecover()
	if got := testing.AllocsPerRun(10, lossAndRecover); got != 0 {
		t.Fatalf("scaled fixed PLC/recovery allocated %g times", got)
	}
}
