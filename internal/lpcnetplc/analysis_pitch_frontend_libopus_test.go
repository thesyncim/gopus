//go:build gopus_osce

package lpcnetplc

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/libopustest"
)

var libopusPitchFrontendHelper libopustest.HelperCache

type pitchFrontendCase struct {
	exc, lp [analysisPitchBufSize]float32
	pitch   int
}

func TestLPCNetPitchFrontendPrimitivesMatchSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	raw, err := probeLibopusPitchDNNModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "pitchdnn model", err)
	}
	blob, err := dnnblob.Clone(raw)
	if err != nil {
		t.Fatal(err)
	}
	const capturedFrames = 16
	var analysis Analysis
	analysis.SetDREDEncoderMode(true)
	if err := analysis.SetModel(blob); err != nil {
		t.Fatal(err)
	}
	frames := dredParityAnalysisFrames(4, 1920)
	if len(frames) < capturedFrames*FrameSize {
		t.Fatalf("analysis samples=%d want at least %d", len(frames), capturedFrames*FrameSize)
	}
	cases := make([]pitchFrontendCase, capturedFrames)
	for frame := range cases {
		var features [NumTotalFeatures]float32
		if n := analysis.ComputeSingleFrameFeaturesFloat(features[:], frames[frame*FrameSize:(frame+1)*FrameSize]); n != NumTotalFeatures {
			t.Fatalf("frame %d feature count=%d", frame, n)
		}
		cases[frame].exc = analysis.excBuf
		cases[frame].lp = analysis.lpBuf
		// This is a fixed valid lag. The five scalar products are tested on the
		// same buffers that the full LPCNet analysis passes to CELT.
		cases[frame].pitch = 96
	}
	bin, err := libopusPitchFrontendHelper.Path(func() (string, error) {
		return buildLibopusPLCHelper("libopus_lpcnet_pitch_frontend_info.c", "gopus_libopus_lpcnet_pitch_frontend")
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "selected LPCNet pitch frontend", err)
	}
	payload := libopustest.NewOraclePayload("GLXI", uint32(len(cases)))
	for _, c := range cases {
		payload.I32(int32(c.pitch))
		payload.Float32s(c.exc[:]...)
		payload.Float32s(c.lp[:]...)
	}
	reader, err := libopustest.RunOracle(bin, payload.Bytes(), "selected LPCNet pitch frontend", "GLXO")
	if err != nil {
		t.Fatal(err)
	}
	reader.Count(len(cases))
	arch := reader.U32()
	if err := libopustest.ValidateDNNDispatchArch(arch); err != nil {
		t.Fatalf("selected LPCNet pitch frontend dispatch: %v", err)
	}
	for frame, c := range cases {
		var wantCorr [pitchXcorrFeatures]float32
		for i := range wantCorr {
			wantCorr[i] = reader.Float32()
		}
		var wantProd [5]float32
		for i := range wantProd {
			wantProd[i] = reader.Float32()
		}
		t.Run(fmt.Sprintf("frame_%02d", frame), func(t *testing.T) {
			var gotCorr [pitchXcorrFeatures]float32
			pitchXCorrFloat(gotCorr[:], c.exc[PitchMaxPeriod:PitchMaxPeriod+FrameSize], c.exc[:], FrameSize, pitchXcorrFeatures)
			for i := range gotCorr {
				if math.Float32bits(gotCorr[i]) != math.Float32bits(wantCorr[i]) {
					t.Fatalf("arch=%d xcorr[%d] Go=%08x C=%08x", arch, i,
						math.Float32bits(gotCorr[i]), math.Float32bits(wantCorr[i]))
				}
			}
			gotProd := [5]float32{
				innerProdFloat(c.exc[PitchMaxPeriod:PitchMaxPeriod+FrameSize], c.exc[PitchMaxPeriod:PitchMaxPeriod+FrameSize], FrameSize),
				innerProdFloat(c.exc[:FrameSize], c.exc[:FrameSize], FrameSize),
				innerProdFloat(c.lp[PitchMaxPeriod:PitchMaxPeriod+FrameSize], c.lp[PitchMaxPeriod:PitchMaxPeriod+FrameSize], FrameSize),
				innerProdFloat(c.lp[PitchMaxPeriod-c.pitch:PitchMaxPeriod-c.pitch+FrameSize], c.lp[PitchMaxPeriod-c.pitch:PitchMaxPeriod-c.pitch+FrameSize], FrameSize),
				innerProdFloat(c.lp[PitchMaxPeriod:PitchMaxPeriod+FrameSize], c.lp[PitchMaxPeriod-c.pitch:PitchMaxPeriod-c.pitch+FrameSize], FrameSize),
			}
			for i := range gotProd {
				if math.Float32bits(gotProd[i]) != math.Float32bits(wantProd[i]) {
					t.Fatalf("arch=%d inner_product[%d] Go=%08x C=%08x", arch, i,
						math.Float32bits(gotProd[i]), math.Float32bits(wantProd[i]))
				}
			}
		})
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	var gotCorr [pitchXcorrFeatures]float32
	last := cases[len(cases)-1]
	pitchXCorrFloat(gotCorr[:], last.exc[PitchMaxPeriod:PitchMaxPeriod+FrameSize], last.exc[:], FrameSize, pitchXcorrFeatures)
	if allocs := testing.AllocsPerRun(100, func() {
		pitchXCorrFloat(gotCorr[:], last.exc[PitchMaxPeriod:PitchMaxPeriod+FrameSize], last.exc[:], FrameSize, pitchXcorrFeatures)
		_ = innerProdFloat(last.lp[PitchMaxPeriod:PitchMaxPeriod+FrameSize], last.lp[PitchMaxPeriod:PitchMaxPeriod+FrameSize], FrameSize)
	}); allocs != 0 {
		t.Fatalf("warm selected pitch frontend allocation=%g want 0", allocs)
	}
}
