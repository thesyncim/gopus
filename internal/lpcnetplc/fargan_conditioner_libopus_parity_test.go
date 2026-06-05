//go:build gopus_osce

package lpcnetplc

import (
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestFARGANConditionerMatchesLibopusOnRealModel(t *testing.T) {
	libopustest.RequireOracle(t)
	modelBlob, err := probeLibopusFARGANModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "fargan model", err)
	}
	blob, err := dnnblob.Clone(modelBlob)
	if err != nil {
		t.Fatalf("dnnblob.Clone error: %v", err)
	}
	var conditioner FARGANConditioner
	if err := conditioner.SetModel(blob); err != nil {
		t.Fatalf("FARGANConditioner.SetModel(real model) error: %v", err)
	}

	var features1 [NumFeatures]float32
	var features2 [NumFeatures]float32
	for i := range features1 {
		features1[i] = float32((i%7)-3) / 9
		features2[i] = float32((i%5)-2) / 7
	}
	features1[NumBands] = -0.35
	features2[NumBands] = 0.2
	period1 := PeriodFromFeatures(features1[:])
	period2 := PeriodFromFeatures(features2[:])

	var zeroState [FARGANCondConv1State]float32
	want1, wantState1, err := probeLibopusFARGANCond(features1[:], period1, zeroState[:])
	if err != nil {
		libopustest.HelperUnavailable(t, "fargan conditioner", err)
	}
	var cond [FARGANCondDense2Size]float32
	if n := conditioner.Compute(cond[:], features1[:]); n != FARGANCondDense2Size {
		t.Fatalf("Compute(features1)=%d want %d", n, FARGANCondDense2Size)
	}
	assertFloat32Close(t, cond[:], want1, 2.5e-4, "fargan cond 1")
	var gotState1 [FARGANCondConv1State]float32
	conditioner.FillCondConv1State(gotState1[:])
	assertFloat32Close(t, gotState1[:], wantState1, 2.5e-4, "fargan cond state 1")

	want2, wantState2, err := probeLibopusFARGANCond(features2[:], period2, wantState1)
	if err != nil {
		libopustest.HelperUnavailable(t, "fargan conditioner second step", err)
	}
	if n := conditioner.Compute(cond[:], features2[:]); n != FARGANCondDense2Size {
		t.Fatalf("Compute(features2)=%d want %d", n, FARGANCondDense2Size)
	}
	assertFloat32Close(t, cond[:], want2, 2.5e-4, "fargan cond 2")
	var gotState2 [FARGANCondConv1State]float32
	conditioner.FillCondConv1State(gotState2[:])
	assertFloat32Close(t, gotState2[:], wantState2, 2.5e-4, "fargan cond state 2")
}
