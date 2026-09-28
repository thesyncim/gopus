//go:build gopus_qext && !gopus_fixed_point

package celt

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

func TestAlgUnquantQEXTN2MatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		k         int
		index     uint32
		refine    uint32
		extraBits int
		gain      uint32
		band      int
		spread    int
		blocks    int
	}{
		// This is the first differing N=2 leaf from the public mono stream.
		{name: "public_band3", k: 128, index: 371, refine: 51, extraBits: 6, gain: 0x3f739934, band: 3, spread: spreadNormal, blocks: 1},
		{name: "negative_pulses", k: 1, index: 2, refine: 0, extraBits: 2, gain: 0x3fdd9168, band: 3, spread: spreadNormal, blocks: 1},
		{name: "zero_second_pulse", k: 3, index: 0, refine: 1, extraBits: 12, gain: 0x3eaaaaab, band: 3, spread: spreadNormal, blocks: 1},
		{name: "positive_second_pulse", k: 3, index: 1, refine: 8, extraBits: 12, gain: 0x3eaaaaab, band: 3, spread: spreadNormal, blocks: 1},
		{name: "public_multiframe_band0", k: 128, index: 281, refine: 14, extraBits: 4, gain: 0x3f7e713a, band: 0, spread: spreadNormal, blocks: 2},
		// This vector distinguishes the selected C contraction from its
		// separately rounded and double-precision alternatives.
		{name: "fma_rounding_boundary", k: 3, index: 1, refine: 15, extraBits: 12, gain: 0x3fffffff, band: 3, spread: spreadNormal, blocks: 1},
	}
	cases := make([]algUnquantQEXTOracleCase, len(tests))
	for i, tc := range tests {
		var mainEnc rangecoding.Encoder
		mainEnc.Init(make([]byte, 32))
		mainEnc.EncodeUniform(tc.index, uint32(PVQ_V(2, tc.k)))
		var extEnc rangecoding.Encoder
		extEnc.Init(make([]byte, 32))
		extEnc.EncodeUniform(tc.refine, uint32((1<<tc.extraBits)-1))
		cases[i] = algUnquantQEXTOracleCase{
			name:      tc.name,
			payload:   mainEnc.Done(),
			extPacket: extEnc.Done(),
			n:         2,
			k:         tc.k,
			spread:    tc.spread,
			b:         tc.blocks,
			gain:      math.Float32frombits(tc.gain),
			extraBits: tc.extraBits,
		}
	}
	want, err := probeLibopusAlgUnquantQEXT(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "selected QEXT alg_unquant", err)
		return
	}
	for i, tc := range tests {
		var mainDec rangecoding.Decoder
		mainDec.Init(cases[i].payload)
		var extDec rangecoding.Decoder
		extDec.Init(cases[i].extPacket)
		got := make([]celtNorm, 2)
		collapse := algUnquantInto(got, &mainDec, tc.band, 2, tc.k, tc.spread, tc.blocks,
			opusVal16(math.Float32frombits(tc.gain)), &extDec, tc.extraBits, nil)
		if collapse != want[i].collapse {
			t.Fatalf("%s collapse=%d want %d", tc.name, collapse, want[i].collapse)
		}
		for j := range got {
			if gotBits, wantBits := math.Float32bits(float32(got[j])), math.Float32bits(want[i].x[j]); gotBits != wantBits {
				t.Fatalf("%s x[%d]=%08x want %08x", tc.name, j, gotBits, wantBits)
			}
		}
	}
}

func TestAlgUnquantQEXTRefinedEnergyPublicBoundaryMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		n         = 4
		k         = 32
		extraBits = 12
		up        = (1 << extraBits) - 1
	)
	basePulses := []int32{7, 10, 1, -14}
	var pulseScratch []uint32
	index := encodePulsesFast32(basePulses, n, k, &pulseScratch)
	var decoded [n]int32
	decodePulsesInto32(index, n, k, decoded[:], nil)
	for i := range basePulses {
		if decoded[i] != basePulses[i] {
			t.Fatalf("CWRS pulse[%d]=%d want %d", i, decoded[i], basePulses[i])
		}
	}

	var mainEnc rangecoding.Encoder
	mainEnc.Init(make([]byte, 32))
	mainEnc.EncodeUniform(index, uint32(PVQ_V(n, k)))
	var extEnc rangecoding.Encoder
	extEnc.Init(make([]byte, 32))
	for range n - 1 {
		// This live leaf matches the right channel in band 0 and takes the
		// large negative refinement path for each of its first n-1 pulses.
		ecEncRefine(&extEnc, -up, up, extraBits, true)
	}
	caseData := algUnquantQEXTOracleCase{
		name:      "public_band0_right_refined_energy_boundary",
		payload:   mainEnc.Done(),
		extPacket: extEnc.Done(),
		n:         n,
		k:         k,
		spread:    spreadNormal,
		b:         2,
		gain:      math.Float32frombits(0x3f695062),
		extraBits: extraBits,
	}
	want, err := probeLibopusAlgUnquantQEXT([]algUnquantQEXTOracleCase{caseData})
	if err != nil {
		libopustest.HelperUnavailable(t, "selected QEXT alg_unquant", err)
		return
	}

	var mainDec rangecoding.Decoder
	mainDec.Init(caseData.payload)
	var extDec rangecoding.Decoder
	extDec.Init(caseData.extPacket)
	got := make([]celtNorm, n)
	collapse := algUnquantInto(got, &mainDec, 0, n, k, spreadNormal, 2, opusVal16(caseData.gain),
		&extDec, extraBits, nil)
	if collapse != want[0].collapse {
		t.Fatalf("collapse=%d want %d", collapse, want[0].collapse)
	}
	for i := range got {
		if gotBits, wantBits := math.Float32bits(float32(got[i])), math.Float32bits(want[0].x[i]); gotBits != wantBits {
			t.Fatalf("x[%d]=%08x want %08x", i, gotBits, wantBits)
		}
	}
}

func TestAlgUnquantQEXTRefinedEnergyTailMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	// The selected C N=6 tuple exercises four terms in the vector body and a
	// two-element contracted scalar tail on the ARM SIMD oracle.
	caseData := algUnquantQEXTOracleCase{
		name:      "selected_c_n6_scalar_tail",
		payload:   []byte{0x79, 0x01, 0x04, 0x64},
		extPacket: []byte{0x00, 0x07, 0x0b, 0x96, 0x73, 0x99, 0xfb, 0xa8, 0x5f},
		n:         6,
		k:         32,
		spread:    spreadNone,
		b:         1,
		gain:      1,
		extraBits: 12,
	}
	want, err := probeLibopusAlgUnquantQEXT([]algUnquantQEXTOracleCase{caseData})
	if err != nil {
		libopustest.HelperUnavailable(t, "selected QEXT alg_unquant", err)
		return
	}
	var mainDec rangecoding.Decoder
	mainDec.Init(caseData.payload)
	var extDec rangecoding.Decoder
	extDec.Init(caseData.extPacket)
	got := make([]celtNorm, caseData.n)
	collapse := algUnquantInto(got, &mainDec, 0, caseData.n, caseData.k, caseData.spread, caseData.b,
		opusVal16(caseData.gain), &extDec, caseData.extraBits, nil)
	if collapse != want[0].collapse {
		t.Fatalf("collapse=%d want %d", collapse, want[0].collapse)
	}
	for i := range got {
		if gotBits, wantBits := math.Float32bits(float32(got[i])), math.Float32bits(want[0].x[i]); gotBits != wantBits {
			t.Fatalf("x[%d]=%08x want %08x", i, gotBits, wantBits)
		}
	}
}
