//go:build gopus_custom_modes && gopus_fixed_point && gopus_qext

package custom_test

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/celt/custom"
	"github.com/thesyncim/gopus/internal/fixedpoint"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestFixedCustomQEXTEncoderParity exercises custom ENABLE_QEXT geometry while
// the selected C helper encodes the same exact int16-valued float input.
func TestFixedCustomQEXTEncoderParity(t *testing.T) {
	for _, spec := range []struct{ fs, frame int }{
		{32000, 640}, {44100, 1024}, {48000, 640}, {48000, 720},
		{96000, 600}, {96000, 1440}, {96000, 1536}, {96000, 1920}, {96000, 2048},
	} {
		for _, channels := range []int{1, 2} {
			tc := customSequenceCase{
				name: fmt.Sprintf("fs%d_n%d_ch%d", spec.fs, spec.frame, channels),
				fs:   spec.fs, frameSize: spec.frame, channels: channels, maxBytes: 200,
			}
			frames := 4
			if spec.fs == 96000 && spec.frame == 2048 {
				// The pinned C decoder reads beyond its buffer on later
				// 2,048-sample frames. Only the first-frame oracle is defined.
				frames = 1
			}
			for frame := range frames {
				pcm := make([]float32, spec.frame*channels)
				for i := range pcm {
					sample := int16((i*97+frame*811)%28000 - 14000)
					pcm[i] = float32(sample) * (1.0 / 32768.0)
				}
				op := customEncodeFrame
				if frame == 3 {
					op = customResetFrame
				}
				tc.records = append(tc.records, customSequenceRecord{op: op, pcm: pcm})
			}
			t.Run(tc.name, func(t *testing.T) {
				refs := runCustomSequenceOracle(t, []customSequenceCase{tc})
				mode, err := custom.NewMode(tc.fs, tc.frameSize)
				if err != nil {
					t.Fatal(err)
				}
				enc := fixedpoint.NewCELTEncoderCustomQEXT(tc.channels, fixedQEXTModeConfig(mode))
				if enc == nil {
					t.Fatal("QEXT custom CELT constructor rejected the C-supported mode")
				}
				enc.SetComplexity(9)
				enc.SetLSBDepth(16)
				enc.SetVBR(false)
				enc.SetConstrainedVBR(false)
				pcm := make([]int16, tc.frameSize*tc.channels)
				packet := make([]byte, tc.maxBytes)
				var coder rangecoding.Encoder
				for frame, rec := range tc.records {
					if rec.op == customResetFrame {
						enc.Reset()
					}
					for i, v := range rec.pcm {
						pcm[i] = int16(v * 32768)
					}
					clear(packet)
					coder.Init(packet)
					n := enc.EncodeWithEC(pcm, tc.frameSize, &coder, tc.maxBytes)
					ref := refs[0][frame]
					if n < 0 || n > len(packet) {
						t.Fatalf("frame %d encode returned %d", frame, n)
					}
					got := coder.Buffer()[:n]
					if !bytes.Equal(got, ref.packet) || enc.FinalRange() != ref.encRange {
						t.Fatalf("frame %d packet/range differs: Go=%x/%08x C=%x/%08x",
							frame, got, enc.FinalRange(), ref.packet, ref.encRange)
					}
				}
			})
		}
	}
}

func fixedQEXTModeConfig(mode *custom.CustomMode) fixedpoint.CELTCustomMode {
	return fixedpoint.CELTCustomMode{
		Fs: mode.Fs, FrameSize: mode.FrameSize, ShortMdctSize: mode.ShortMdctSize,
		Overlap: mode.Overlap, MaxLM: mode.MaxLM, EffEBands: mode.EffEBands,
		EBands: mode.EBands, LogN: mode.LogN, AllocVectors: mode.AllocVectors,
		CacheIndex: mode.CacheIndex, CacheBits: mode.CacheBits, CacheCaps: mode.CacheCaps,
		ScaledBandFamily: mode.InScaledBandFamily(),
	}
}

func TestFixedCustomQEXTEncoderZeroAlloc(t *testing.T) {
	for _, spec := range []struct{ fs, frame, channels int }{
		{44100, 1024, 2}, {96000, 2048, 1}, {96000, 2048, 2},
	} {
		t.Run(fmt.Sprintf("fs%d_n%d_ch%d", spec.fs, spec.frame, spec.channels), func(t *testing.T) {
			mode, err := custom.NewMode(spec.fs, spec.frame)
			if err != nil {
				t.Fatal(err)
			}
			enc := fixedpoint.NewCELTEncoderCustomQEXT(spec.channels, fixedQEXTModeConfig(mode))
			if enc == nil {
				t.Fatal("QEXT custom CELT constructor rejected the mode")
			}
			enc.SetComplexity(9)
			enc.SetLSBDepth(16)
			enc.SetVBR(false)
			enc.SetConstrainedVBR(false)
			pcm := make([]int16, spec.frame*spec.channels)
			for i := range pcm {
				pcm[i] = int16((i*97)%28000 - 14000)
			}
			packet := make([]byte, 200)
			var coder rangecoding.Encoder
			run := func() {
				clear(packet)
				coder.Init(packet)
				if n := enc.EncodeWithEC(pcm, spec.frame, &coder, len(packet)); n < 0 {
					panic("QEXT custom encoder rejected the frame")
				}
			}
			for range 5 {
				run()
			}
			if got := testing.AllocsPerRun(100, run); got != 0 {
				t.Fatalf("steady-state custom QEXT encode allocated %g times", got)
			}
		})
	}
}
