//go:build amd64.v3 && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package celt

import (
	"strconv"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

func TestCELTV3IIRFeedbackTailsMatchLibopus(t *testing.T) {
	requireCELTV3OracleTarget(t)
	if variant := requirePairedCELTOracleMode(t); variant != libopustooling.LibopusReferenceSIMD && variant != libopustooling.LibopusReferenceScalar {
		t.Fatalf("unsupported libopus reference variant %q", variant)
	}
	libopustest.RequireOracle(t)

	hist := makeCELTPLCTestSignal(plcDecodeBufferSize, 0x11a5011, 1300)
	lpc := makeCELTPLPCTestCoeffs()
	for _, length := range []int{1, 2, 3, 4, 5, 241, 242, 243} {
		t.Run(strconv.Itoa(length), func(t *testing.T) {
			in := makeCELTPLCTestSignal(length, 0x119911+uint32(length), 900)
			want := probeLibopusPLCIIR(t, in, hist, lpc)

			dec := NewDecoder(1)
			gotSig := append([]celtSig(nil), in...)
			dec.celtIIRFloat32(gotSig, hist, lpc, len(gotSig))
			got := make([]float32, len(gotSig))
			copySigToFloat32(got, gotSig)
			assertFloat32Bits(t, "iir tail", got, want)

			if allocs := testing.AllocsPerRun(100, func() {
				dec.celtIIRFloat32(gotSig, hist, lpc, len(gotSig))
			}); allocs != 0 {
				t.Fatalf("celtIIRFloat32 length=%d allocs/run=%g want 0", length, allocs)
			}
		})
	}
}
