//go:build gopus_custom_modes && gopus_qext && !gopus_fixed_point

package custom_test

import (
	"bytes"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt/custom"
)

// QEXT changes mode creation even when the custom packet has no side payload.
// These frames cover the 96 kHz coefficients and the extended frame-size limit
// in celt/modes.c opus_custom_mode_create.
func TestOracleQEXTFloatCustomModes(t *testing.T) {
	var cases []oracleCase
	for _, n := range []int{240, 600, 1024, 1440, 1920, 2048} {
		for _, channels := range []int{1, 2} {
			cases = append(cases, oracleCase{96000, n, channels, 200, customSequenceInput(n, channels, 0)})
		}
	}
	refs := runCustomOracle(t, cases)
	for i, tc := range cases {
		t.Run(fmt.Sprintf("n%d_ch%d", tc.frameSize, tc.channels), func(t *testing.T) {
			ref := refs[i]
			if ref.status < 0 {
				t.Fatalf("C rejected mode: %d", ref.status)
			}
			mode, err := custom.NewMode(tc.fs, tc.frameSize)
			if err != nil {
				t.Fatal(err)
			}
			if mode.Preemph != ref.preemph {
				t.Fatalf("preemphasis=%v want %v", mode.Preemph, ref.preemph)
			}
			packet, enc := gopusEncode(t, tc)
			if !bytes.Equal(packet, ref.packet) || enc.FinalRange() != ref.encRange {
				t.Fatalf("packet/range=%x/%08x want %x/%08x", packet, enc.FinalRange(), ref.packet, ref.encRange)
			}
			dec, err := custom.NewDecoder(mode, tc.channels)
			if err != nil {
				t.Fatal(err)
			}
			pcm, err := dec.DecodeFloat(ref.packet, tc.frameSize)
			if err != nil {
				t.Fatal(err)
			}
			if dec.FinalRange() != ref.decRange {
				t.Fatalf("decode range=%08x want %08x", dec.FinalRange(), ref.decRange)
			}
			assertCustomDecodeExact(t, "QEXT custom", pcm, ref.decoded)
			run := func() {
				packet, err := enc.EncodeFloat(tc.pcm, tc.maxBytes)
				if err != nil {
					panic(err)
				}
				pcm, err := dec.DecodeFloat(packet, tc.frameSize)
				if err != nil {
					panic(err)
				}
				customWideAllocationSink = pcm[0]
			}
			for range 5 {
				run()
			}
			if got := testing.AllocsPerRun(50, run); got != 0 {
				t.Fatalf("steady-state encode/decode allocations=%g", got)
			}
		})
	}
	if _, err := custom.NewMode(96000, 2050); err != custom.ErrBadArg {
		t.Fatalf("frame above QEXT limit: %v", err)
	}
}

func TestOracleQEXTFloatCustomSequence(t *testing.T) {
	var cases []customSequenceCase
	// libopus reads outside its synthesis history at n=2048. That geometry
	// has a safe-Go regression below; it is not a valid stateful C oracle.
	// See reports/validation.md#reference-boundary for the sanitizer evidence.
	for _, n := range []int{240, 600, 1024, 1440, 1920} {
		for _, channels := range []int{1, 2} {
			tc := customSequenceCase{name: fmt.Sprintf("n%d_ch%d", n, channels), fs: 96000, frameSize: n, channels: channels, maxBytes: 200}
			for frame := range 9 {
				op := customEncodeFrame
				if frame == 3 || frame == 4 {
					op = customLostFrame
				}
				if frame == 7 {
					op = customResetFrame
				}
				tc.records = append(tc.records, customSequenceRecord{op: op, pcm: customSequenceInput(n, channels, frame)})
			}
			cases = append(cases, tc)
		}
	}
	refs := runCustomSequenceOracle(t, cases)
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assertCustomSequenceParity(t, tc, refs[i]) })
	}
}

func TestQEXTFloatCustom2048SafeSequence(t *testing.T) {
	for _, channels := range []int{1, 2} {
		t.Run(fmt.Sprintf("ch%d", channels), func(t *testing.T) {
			mode, err := custom.NewMode(96000, 2048)
			if err != nil {
				t.Fatal(err)
			}
			enc, err := custom.NewEncoder(mode, channels)
			if err != nil {
				t.Fatal(err)
			}
			dec, err := custom.NewDecoder(mode, channels)
			if err != nil {
				t.Fatal(err)
			}
			var inputs [12][]float32
			for frame := range inputs {
				inputs[frame] = customSequenceInput(2048, channels, frame)
			}
			run := func() {
				enc.Reset()
				dec.Reset()
				for frame := range 12 {
					var packet []byte
					if frame < 3 || frame > 7 {
						packet, err = enc.EncodeFloat(inputs[frame], 200)
						if err != nil {
							t.Fatal(err)
						}
					}
					pcm, err := dec.DecodeFloat(packet, 2048)
					if err != nil || len(pcm) != 2048*channels {
						t.Fatalf("frame %d: samples=%d error=%v", frame, len(pcm), err)
					}
					for i, sample := range pcm {
						if math.Float32bits(sample)&0x7f800000 == 0x7f800000 {
							t.Fatalf("frame %d sample %d is non-finite", frame, i)
						}
					}
				}
			}
			run()
			if got := testing.AllocsPerRun(20, run); got != 0 {
				t.Fatalf("steady-state encode/decode/loss/recovery allocations=%g", got)
			}

		})
	}
}
