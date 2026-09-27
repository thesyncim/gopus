//go:build gopus_dred

package lpcnetplc

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/libopustest"
)

// This is the 160-sample integer-grid input to the first Burg analysis at
// frame 77 of TestDREDLongLossPCMMatchesLibopusRawBits. It retains the raw
// scale used by the decoder rather than normalizing away float-product widths.
var dredFirstLossBurgInputBits = [FrameSize]uint32{
	0x44514000, 0x446d8000, 0x4472c000, 0x44690000, 0x44584000, 0x4445c000, 0x443a0000, 0x44364000,
	0x443e8000, 0x444e8000, 0x44618000, 0x4471c000, 0x447a0000, 0x44724000, 0x445c8000, 0x44394000,
	0x440e0000, 0x43c00000, 0x43500000, 0x42180000, 0xc2f40000, 0xc38d0000, 0xc3d98000, 0xc4148000,
	0xc4398000, 0xc4550000, 0xc4678000, 0xc46c8000, 0xc4634000, 0xc453c000, 0xc4470000, 0xc4404000,
	0xc44cc000, 0xc46c0000, 0xc4910000, 0xc4b4c000, 0xc4dea000, 0xc5071000, 0xc51fd000, 0xc539e000,
	0xc5538000, 0xc56a0000, 0xc57cb000, 0xc5856000, 0xc589d800, 0xc58ae800, 0xc588b000, 0xc5835000,
	0xc5757000, 0xc5600000, 0xc546d000, 0xc52b5000, 0xc50ec000, 0xc4e58000, 0xc4afa000, 0xc479c000,
	0xc41b4000, 0xc3818000, 0x42920000, 0x43ba8000, 0x441b8000, 0x44514000, 0x447ec000, 0x4491c000,
	0x44a28000, 0x44b12000, 0x44bda000, 0x44c7a000, 0x44cc4000, 0x44cc8000, 0x44c6c000, 0x44c0c000,
	0x44b9c000, 0x44b2e000, 0x44ade000, 0x44a96000, 0x44a4c000, 0x449f4000, 0x4496a000, 0x448e0000,
	0x44888000, 0x448a6000, 0x44922000, 0x449da000, 0x44ab2000, 0x44b72000, 0x44bfe000, 0x44c32000,
	0x44c24000, 0x44bce000, 0x44b40000, 0x44aa4000, 0x449fe000, 0x4494e000, 0x448a4000, 0x447f4000,
	0x44690000, 0x4451c000, 0x443c0000, 0x442d0000, 0x44258000, 0x44288000, 0x443a4000, 0x44504000,
	0x44660000, 0x44794000, 0x44806000, 0x447d8000, 0x44728000, 0x44634000, 0x4453c000, 0x444a4000,
	0x44450000, 0x4443c000, 0x4442c000, 0x44418000, 0x443fc000, 0x44370000, 0x44298000, 0x441b8000,
	0x44134000, 0x4415c000, 0x441a8000, 0x441e8000, 0x441f8000, 0x44184000, 0x4405c000, 0x43d98000,
	0x439e0000, 0x43420000, 0x42900000, 0xc1d80000, 0xc2d80000, 0xc33e0000, 0xc38a8000, 0xc3bd0000,
	0xc3fb0000, 0xc41c8000, 0xc435c000, 0xc4474000, 0xc4504000, 0xc4510000, 0xc4508000, 0xc459c000,
	0xc46fc000, 0xc48c6000, 0xc4aaa000, 0xc4d0a000, 0xc4fb2000, 0xc5139000, 0xc529e000, 0xc53e7000,
	0xc551e000, 0xc5639000, 0xc572f000, 0xc57f1000, 0xc5831000, 0xc583e000, 0xc5812800, 0xc576e000,
}

// The 57 predictor features follow the frame-77 Burg and spectral analysis.
// The last value is the lost-frame flag supplied to compute_plc_pred.
var dredFirstLossPredictorInputBits = [InputSize]uint32{
	0x4031d771, 0x40cecba0, 0x4019ae89, 0xbf6c2b32, 0xbf29c593, 0x3e951a50, 0xbdc2cfac, 0x3ece9b96,
	0xbdbba1ec, 0x3cf8e600, 0xbeb04ead, 0x3e0ea4f6, 0x3daf9f7a, 0x3dc801cb, 0x3d8cb895, 0xbd2b5bca,
	0xbccc6f00, 0xbcf3885b, 0xbfa1a13c, 0xbe654fc0, 0x3f31aa60, 0xbed46e9e, 0xbf36606a, 0x3ed2b50a,
	0x3f3d4162, 0x3db4810c, 0xbdca2928, 0xbe75f780, 0xbed88e26, 0x3e4e3c1b, 0x3ddfce9f, 0x3e854cca,
	0x3d578ccb, 0xbd6bab37, 0xbdf4e1c0, 0xbd7a3ae5, 0x40ac0872, 0x40933f2b, 0x403ce154, 0xbebd61cd,
	0xbf818bd8, 0xbe6bd090, 0x3f163d81, 0x3ee7814f, 0xbe3d1068, 0xbf044456, 0xbee90681, 0xbd1273d4,
	0x3d6e0580, 0x3e1e517b, 0x3df98efc, 0x3e0dea09, 0xbc6b8259, 0xbdc28809, 0xbed3a7d8, 0x3ea917f0,
	0x3f800000,
}

func TestDREDBurgSelectedCFirstLossRawBits(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, name := range []string{"first_loss_77", "low_amplitude"} {
		t.Run(name, func(t *testing.T) {
			var frame [FrameSize]float32
			for i := range frame {
				if name == "first_loss_77" {
					frame[i] = math.Float32frombits(dredFirstLossBurgInputBits[i])
				} else {
					frame[i] = float32((i%47)-23) / 21
				}
			}
			want, err := probeLibopusBurgCepstrum(frame[:])
			if err != nil {
				t.Fatalf("selected C Burg oracle: %v", err)
			}
			var analysis Analysis
			var got [2 * NumBands]float32
			if n := analysis.BurgCepstralAnalysis(got[:], frame[:]); n != len(got) {
				t.Fatalf("BurgCepstralAnalysis()=%d want %d", n, len(got))
			}
			for i := range got {
				if gb, cb := math.Float32bits(got[i]), math.Float32bits(want[i]); gb != cb {
					t.Fatalf("cepstrum[%d] Go=%08x C=%08x", i, gb, cb)
				}
			}
			if allocs := testing.AllocsPerRun(20, func() { analysis.BurgCepstralAnalysis(got[:], frame[:]) }); allocs != 0 {
				t.Fatalf("warm Burg allocations=%v want 0", allocs)
			}
		})
	}
}

func TestDREDPredictorSelectedCFirstLossRawBits(t *testing.T) {
	libopustest.RequireOracle(t)
	modelBlob, err := probeLibopusPLCModelBlob()
	if err != nil {
		t.Fatalf("selected C PLC model: %v", err)
	}
	blob, err := dnnblob.Clone(modelBlob)
	if err != nil {
		t.Fatalf("clone PLC model: %v", err)
	}
	var predictor Predictor
	if err := predictor.SetModel(blob); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	var input [InputSize]float32
	for i, bits := range dredFirstLossPredictorInputBits {
		input[i] = math.Float32frombits(bits)
	}
	var z1 [GRU1Size]float32
	var z2 [GRU2Size]float32
	for i := range z1 {
		z1[i] = float32((i%13)-6) / 17
	}
	for i := range z2 {
		z2[i] = float32((i%11)-5) / 19
	}
	predictor.state.gru1 = z1
	predictor.state.gru2 = z2
	want, wantG1, wantG2, err := probeLibopusPLCPredict(input[:], z1[:], z2[:])
	if err != nil {
		t.Fatalf("selected C PLC predictor: %v", err)
	}
	var got [NumFeatures]float32
	if n := predictor.Predict(got[:], input[:]); n != len(got) {
		t.Fatalf("Predict()=%d want %d", n, len(got))
	}
	compare := func(label string, got, want []float32) {
		t.Helper()
		for i := range got {
			if gb, cb := math.Float32bits(got[i]), math.Float32bits(want[i]); gb != cb {
				t.Fatalf("%s[%d] Go=%08x C=%08x", label, i, gb, cb)
			}
		}
	}
	compare("output", got[:], want)
	compare("gru1", predictor.state.gru1[:], wantG1)
	compare("gru2", predictor.state.gru2[:], wantG2)
	if allocs := testing.AllocsPerRun(20, func() { predictor.Predict(got[:], input[:]) }); allocs != 0 {
		t.Fatalf("warm predictor allocations=%v want 0", allocs)
	}
}
