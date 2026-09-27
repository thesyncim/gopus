//go:build gopus_osce

package lpcnetplc

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/opusmath"
)

var farganGainHelper libopustest.HelperCache
var farganGainAllocSink float32

func probeLibopusFARGANGain(cond []float32) (float32, float32, error) {
	binPath, err := farganGainHelper.Path(func() (string, error) {
		return buildLibopusPLCHelper("libopus_fargan_gain_info.c", "gopus_libopus_fargan_gain_info")
	})
	if err != nil {
		return 0, 0, err
	}
	payload := libopustest.NewOraclePayload("GFGI")
	payload.Float32s(cond...)
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "fargan gain", "GFGO")
	if err != nil {
		return 0, 0, err
	}
	pre, gain := reader.Float32(), reader.Float32()
	if err := reader.ExpectConsumed(); err != nil {
		return 0, 0, err
	}
	return pre, gain, nil
}

func TestFARGANGainMatchesSelectedLibopusRawBits(t *testing.T) {
	libopustest.RequireOracle(t)
	rawModel, err := probeLibopusFARGANModelBlob()
	if err != nil {
		t.Fatalf("selected libopus FARGAN model: %v", err)
	}
	model, err := dnnblob.Clone(rawModel)
	if err != nil {
		t.Fatalf("clone FARGAN model: %v", err)
	}
	var f FARGAN
	if err := f.SetModel(model); err != nil {
		t.Fatalf("FARGAN.SetModel: %v", err)
	}
	var pcm [FARGANContSamples]float32
	var features [ContVectors * NumFeatures]float32
	fillFARGANPrimeInputs(pcm[:], features[:])
	var condState [FARGANCondConv1State]float32
	var lastCond []float32
	for vector := 0; vector < ContVectors; vector++ {
		feature := features[vector*NumFeatures : (vector+1)*NumFeatures]
		cond, nextState, err := probeLibopusFARGANCond(feature, PeriodFromFeatures(feature), condState[:])
		if err != nil {
			t.Fatalf("selected libopus conditioner vector %d: %v", vector, err)
		}
		copy(condState[:], nextState)
		for subframe := 0; subframe < FARGANNBSubframes; subframe++ {
			input := cond[subframe*FARGANCondSize : (subframe+1)*FARGANCondSize]
			lastCond = input
			wantPre, wantGain, err := probeLibopusFARGANGain(input)
			if err != nil {
				t.Fatalf("selected libopus gain vector %d subframe %d: %v", vector, subframe, err)
			}
			computeFARGANSignalDense(&f.model.CondGainDense, f.scratch.gain[:], input, activationLinear, &f.scratch)
			gotPre := f.scratch.gain[0]
			gotGain := opusmath.ExpF32(gotPre)
			if gb, cb := math.Float32bits(gotPre), math.Float32bits(wantPre); gb != cb {
				t.Errorf("vector %d subframe %d preactivation Go=%08x C=%08x", vector, subframe, gb, cb)
			}
			if gb, cb := math.Float32bits(gotGain), math.Float32bits(wantGain); gb != cb {
				t.Errorf("vector %d subframe %d gain Go=%08x C=%08x", vector, subframe, gb, cb)
			}
		}
	}
	computeFARGANSignalDense(&f.model.CondGainDense, f.scratch.gain[:], lastCond, activationLinear, &f.scratch)
	if allocs := testing.AllocsPerRun(100, func() {
		computeFARGANSignalDense(&f.model.CondGainDense, f.scratch.gain[:], lastCond, activationLinear, &f.scratch)
		farganGainAllocSink = f.scratch.gain[0]
	}); allocs != 0 {
		t.Fatalf("warmed FARGAN gain allocations=%g want 0", allocs)
	}
}

func TestFARGANPrimeContinuityStateMatchesSelectedLibopusRawBits(t *testing.T) {
	libopustest.RequireOracle(t)
	rawModel, err := probeLibopusFARGANModelBlob()
	if err != nil {
		t.Fatalf("selected libopus FARGAN model: %v", err)
	}
	model, err := dnnblob.Clone(rawModel)
	if err != nil {
		t.Fatalf("clone FARGAN model: %v", err)
	}
	var f FARGAN
	if err := f.SetModel(model); err != nil {
		t.Fatalf("FARGAN.SetModel: %v", err)
	}
	var pcm [FARGANContSamples]float32
	var features [ContVectors * NumFeatures]float32
	fillFARGANPrimeInputs(pcm[:], features[:])
	want, err := probeLibopusFARGANContinuity(pcm[:], features[:])
	if err != nil {
		t.Fatalf("selected libopus continuity: %v", err)
	}
	if n := f.PrimeContinuity(pcm[:], features[:]); n != len(pcm) {
		t.Fatalf("PrimeContinuity samples=%d want %d", n, len(pcm))
	}
	for _, field := range []struct {
		name string
		got  []float32
		want []float32
	}{
		{"pitch", f.state.pitchBuf[:], want.PitchBuf},
		{"conditioner", f.state.condConv1State[:], want.CondConv1State},
		{"fwc0", f.state.fwc0Mem[:], want.FWC0Mem},
		{"gru1", f.state.gru1State[:], want.GRU1State},
		{"gru2", f.state.gru2State[:], want.GRU2State},
		{"gru3", f.state.gru3State[:], want.GRU3State},
	} {
		if len(field.got) != len(field.want) {
			t.Fatalf("%s length Go=%d C=%d", field.name, len(field.got), len(field.want))
		}
		for i, c := range field.want {
			if gb, cb := math.Float32bits(field.got[i]), math.Float32bits(c); gb != cb {
				t.Errorf("%s[%d] Go=%08x C=%08x", field.name, i, gb, cb)
			}
		}
	}
}
