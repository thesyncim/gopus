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
