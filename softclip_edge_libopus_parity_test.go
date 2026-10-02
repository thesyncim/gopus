package gopus

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestPCMSoftClipEdgeValuesMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	negativeZero := math.Float32frombits(0x80000000)
	quietNaN := math.Float32frombits(0x7fc00001)
	signalingNaN := math.Float32frombits(0x7f800001)
	inRangeBlock := make([]float32, 32)
	for i := range inRangeBlock {
		inRangeBlock[i] = 0.5
		if i%2 != 0 {
			inRangeBlock[i] = -0.3
		}
	}
	tests := []struct {
		name     string
		channels int
		samples  []float32
		mem      []float32
	}{
		{"negative_zero_memory", 1, []float32{0.5, -0.3}, []float32{negativeZero}},
		{"stereo_negative_zero_memory", 2, []float32{0.5, -0.3, negativeZero, 0}, []float32{negativeZero, negativeZero}},
		{"vector_block_negative_zero_memory", 2, inRangeBlock, []float32{negativeZero, negativeZero}},
		{"nan_between_positive_peaks", 1, []float32{0.5, 1.4, quietNaN, 1.9, 0.5}, []float32{0}},
		{"nan_between_negative_peaks", 1, []float32{-0.5, -1.4, quietNaN, -1.9, -0.5}, []float32{0}},
		{"nan_after_peak", 1, []float32{0.5, 1.4, quietNaN}, []float32{0}},
		{"signaling_nan_between_peaks", 1, []float32{0.5, 1.4, signalingNaN, 1.9, 0.5}, []float32{0}},
		{"in_range_signaling_nan_prefix", 1, []float32{signalingNaN, 0.5, -0.3}, []float32{0}},
		{"stereo_nan_between_peaks", 2, []float32{0.5, -0.5, 1.4, -1.4, quietNaN, signalingNaN, 1.9, -1.9, 0.5, -0.5}, []float32{0, 0}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want, wantMem, err := libopustest.ProbeSoftClip(len(tc.samples)/tc.channels, tc.channels, tc.samples, tc.mem)
			if err != nil {
				libopustest.HelperUnavailable(t, "softclip", err)
			}
			got := append([]float32(nil), tc.samples...)
			gotMem := append([]float32(nil), tc.mem...)
			PCMSoftClip(got, tc.channels, gotMem)
			assertSoftClipFloat32BitsEqual(t, got, want, "pcm")
			assertSoftClipFloat32BitsEqual(t, gotMem, wantMem, "mem")

			wantInt16, err := probeLibopusFloatQuant(libopustest.FloatQuantModeCELTDispatch, want)
			if err != nil {
				libopustest.HelperUnavailable(t, "float quant", err)
			}
			copy(got, tc.samples)
			copy(gotMem, tc.mem)
			pcmInt16 := make([]int16, len(got))
			softClipAndFloat32ToInt16(pcmInt16, got, len(got)/tc.channels, tc.channels, gotMem)
			assertSoftClipFloat32BitsEqual(t, got, want, "int16 softclipped pcm")
			assertSoftClipFloat32BitsEqual(t, gotMem, wantMem, "int16 mem")
			for i, sample := range wantInt16 {
				if pcmInt16[i] != sample {
					t.Fatalf("int16[%d]=%d want %d", i, pcmInt16[i], sample)
				}
			}

			if allocs := testing.AllocsPerRun(20, func() {
				copy(got, tc.samples)
				copy(gotMem, tc.mem)
				PCMSoftClip(got, tc.channels, gotMem)
				copy(got, tc.samples)
				copy(gotMem, tc.mem)
				softClipAndFloat32ToInt16(pcmInt16, got, len(got)/tc.channels, tc.channels, gotMem)
			}); allocs != 0 {
				t.Fatalf("soft clipping allocs=%g want 0", allocs)
			}
		})
	}
}
