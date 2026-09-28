//go:build gopus_fixed_point && gopus_qext && gopus_custom_modes

package celt

import (
	"testing"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

func TestQEXTDecodeExtraAllocationModeExportBoundsGeometry(t *testing.T) {
	edges := []int16{0, 1, 2}
	pulses := make([]int32, 2+nbQEXTBands)
	quant := make([]int32, len(pulses))
	var dec rangecoding.Decoder
	dec.Init([]byte{0xff})

	if !QEXTDecodeExtraAllocationModeExport(0, 2, 0, 0, 1, 0, &dec,
		pulses[:2], quant[:2], 44100, 64, edges) {
		t.Fatal("valid main-only custom geometry was rejected")
	}

	tests := []struct {
		name     string
		start    int
		end      int
		extraEnd int
		channels int
		lm       int
		fs       int
		short    int
		main     []int16
		pulses   []int32
		quant    []int32
	}{
		{name: "negative start", start: -1, end: 2, channels: 1, lm: 0, fs: 44100, short: 64, main: edges, pulses: pulses, quant: quant},
		{name: "end beyond main bands", start: 0, end: 3, channels: 1, lm: 0, fs: 44100, short: 64, main: edges, pulses: pulses, quant: quant},
		{name: "too many extra bands", start: 0, end: 2, extraEnd: nbQEXTBands + 1, channels: 1, lm: 0, fs: 48000, short: 120, main: edges, pulses: pulses, quant: quant},
		{name: "unsupported side-mode rate", start: 0, end: 2, extraEnd: 2, channels: 1, lm: 0, fs: 24000, short: 60, main: edges, pulses: pulses, quant: quant},
		{name: "too many channels", start: 0, end: 2, channels: 3, lm: 0, fs: 44100, short: 64, main: edges, pulses: pulses, quant: quant},
		{name: "invalid LM", start: 0, end: 2, channels: 1, lm: 4, fs: 44100, short: 64, main: edges, pulses: pulses, quant: quant},
		{name: "short pulse arrays", start: 0, end: 2, extraEnd: 2, channels: 1, lm: 0, fs: 48000, short: 120, main: edges, pulses: pulses[:3], quant: quant[:3]},
		{name: "base bands exceed fixed bound", start: 0, end: 2, channels: 1, lm: 0, fs: 44100, short: 64, main: make([]int16, qextMaxBaseBands+2), pulses: make([]int32, qextMaxBaseBands+1), quant: make([]int32, qextMaxBaseBands+1)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if QEXTDecodeExtraAllocationModeExport(tc.start, tc.end, tc.extraEnd, 0,
				tc.channels, tc.lm, &dec, tc.pulses, tc.quant, tc.fs, tc.short, tc.main) {
				t.Fatal("invalid custom geometry was accepted")
			}
		})
	}
}
