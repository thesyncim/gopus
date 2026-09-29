//go:build gopus_osce && amd64 && goexperiment.simd && !nosimd && !purego

package lpcnetplc

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestLPCNetAMD64PitchStateMatchesSelectedLibopus locates the first persistent
// encoder frontend state difference on the same DRED sequence used by the
// public pitch feature gate. Each C probe replays the complete prefix, so its
// final state is the state after the matching Go frame.
func TestLPCNetAMD64PitchStateMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	raw, err := probeLibopusPitchDNNModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "pitchdnn model", err)
	}
	blob, err := dnnblob.Clone(raw)
	if err != nil {
		t.Fatal(err)
	}
	var analysis Analysis
	analysis.SetDREDEncoderMode(true)
	if err := analysis.SetModel(blob); err != nil {
		t.Fatal(err)
	}
	frames := dredParityAnalysisFrames(4, 1920)
	if len(frames) != 16*FrameSize {
		t.Fatalf("analysis frame count=%d want 16", len(frames)/FrameSize)
	}
	check := func(t *testing.T, label string, got, want []float32) {
		t.Helper()
		if len(got) != len(want) {
			t.Errorf("%s length=%d C=%d", label, len(got), len(want))
			return
		}
		for i := range got {
			if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
				t.Errorf("%s[%d] Go=%08x C=%08x", label, i,
					math.Float32bits(got[i]), math.Float32bits(want[i]))
				return
			}
		}
	}
	for frame := 0; frame < 16; frame++ {
		var got [NumTotalFeatures]float32
		if n := analysis.ComputeSingleFrameFeaturesFloat(got[:], frames[frame*FrameSize:(frame+1)*FrameSize]); n != NumTotalFeatures {
			t.Fatalf("frame %d feature count=%d", frame, n)
		}
		want, err := probeLibopusLPCNetFeatures(frames[:(frame+1)*FrameSize])
		if err != nil {
			t.Fatalf("frame %d selected C: %v", frame, err)
		}
		t.Run(fmt.Sprintf("frame_%02d", frame), func(t *testing.T) {
			check(t, "analysis_mem", analysis.analysisMem[:], want.AnalysisMem)
			check(t, "preemphasis_mem", []float32{analysis.memPreemph}, []float32{want.MemPreemph})
			check(t, "lpc", analysis.lpc[:], want.LPC)
			check(t, "exc_buf", analysis.excBuf[:], want.ExcBuf)
			check(t, "lp_buf", analysis.lpBuf[:], want.LPBuf)
			check(t, "if_features", analysis.ifFeatures[:], want.IFFeatures)
			check(t, "xcorr_features", analysis.xcorrFeatures[:], want.XCorr)
			check(t, "dnn_pitch", []float32{analysis.dnnPitch}, []float32{want.DNNPitch})
			check(t, "features", got[:], want.Features[frame*NumTotalFeatures:(frame+1)*NumTotalFeatures])
		})
	}
}
