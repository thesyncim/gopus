//go:build gopus_custom_modes && gopus_fixed_point && gopus_qext

package custom_test

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt/custom"
)

func TestFixedCustomQEXTDynamicStatefulParity(t *testing.T) {
	for _, spec := range []struct{ fs, frame int }{
		{32000, 640}, {44100, 1024}, {48000, 640}, {48000, 720},
		{96000, 600}, {96000, 1440}, {96000, 1536}, {96000, 1920}, {96000, 2048},
	} {
		for _, channels := range []int{1, 2} {
			tc := customSequenceCase{
				name: fmt.Sprintf("fs%d_n%d_ch%d", spec.fs, spec.frame, channels),
				fs:   spec.fs, frameSize: spec.frame, channels: channels, maxBytes: 200,
			}
			frames := 9
			if spec.fs == 96000 && spec.frame == 2048 {
				// The pinned C decoder reads beyond its buffer after its
				// first 2,048-sample frame; compare only defined oracle output.
				frames = 1
			}
			for frame := range frames {
				op := customEncodeFrame
				if frame == 3 || frame == 4 {
					op = customLostFrame
				}
				if frame == 6 {
					op = customResetFrame
				}
				tc.records = append(tc.records, customSequenceRecord{
					op: op, pcm: customSequenceInput(spec.frame, channels, frame),
				})
			}
			t.Run(tc.name, func(t *testing.T) {
				refs := runCustomSequenceOracle(t, []customSequenceCase{tc})
				assertCustomSequenceParity(t, tc, refs[0])
			})
		}
	}
}

func TestFixedCustomQEXT2048SafeSequence(t *testing.T) {
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
			for frame := range 12 {
				if frame == 10 {
					enc.Reset()
					dec.Reset()
				}
				var packet []byte
				if frame < 3 || frame > 7 {
					packet, err = enc.EncodeFloat(customSequenceInput(2048, channels, frame%7), 200)
					if err != nil {
						t.Fatalf("frame %d encode: %v", frame, err)
					}
					if len(packet) == 0 || len(packet) > 200 {
						t.Fatalf("frame %d packet length %d", frame, len(packet))
					}
				}
				pcm, err := dec.DecodeFloat(packet, 2048)
				if err != nil || len(pcm) != 2048*channels {
					t.Fatalf("frame %d decode: count=%d error=%v", frame, len(pcm), err)
				}
				for i, sample := range pcm {
					if math.Float32bits(sample)&0x7f800000 == 0x7f800000 {
						t.Fatalf("frame %d sample %d is non-finite: %g", frame, i, sample)
					}
				}
			}
		})
	}
}

func TestFixedCustomQEXTDynamicZeroAlloc(t *testing.T) {
	for _, spec := range []struct{ fs, frame, channels int }{
		{44100, 1024, 2}, {96000, 2048, 1}, {96000, 2048, 2},
	} {
		t.Run(fmt.Sprintf("fs%d_n%d_ch%d", spec.fs, spec.frame, spec.channels), func(t *testing.T) {
			mode, err := custom.NewMode(spec.fs, spec.frame)
			if err != nil {
				t.Fatal(err)
			}
			enc, err := custom.NewEncoder(mode, spec.channels)
			if err != nil {
				t.Fatal(err)
			}
			dec, err := custom.NewDecoder(mode, spec.channels)
			if err != nil {
				t.Fatal(err)
			}
			pcm := customSequenceInput(spec.frame, spec.channels, 1)
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
				t.Fatalf("steady-state custom QEXT encode/decode allocated %g times", got)
			}
			lossAndRecover := func() {
				for range 5 {
					if _, err := dec.DecodeFloat(nil, spec.frame); err != nil {
						panic(err)
					}
				}
				run()
			}
			lossAndRecover()
			if got := testing.AllocsPerRun(10, lossAndRecover); got != 0 {
				t.Fatalf("custom QEXT PLC/recovery allocated %g times", got)
			}
		})
	}
}
