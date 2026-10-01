//go:build !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package celt

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

var celtLPCGuardOracle libopustest.HelperCache

// TestCELTLPCInactiveInputMatchesLibopus checks the ordered float comparison
// in celt/celt_lpc.c:_celt_lpc. Zero, sub-threshold and NaN inputs leave every
// coefficient zero, independent of the selected scalar or SIMD recurrence.
func TestCELTLPCInactiveInputMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requirePairedCELTOracleMode(t)
	helper, err := celtLPCGuardOracle.Path(func() (string, error) {
		cfg := libopustest.CHelperConfig{
			Label:       "CELT LPC float guard",
			OutputBase:  "gopus_libopus_celt_lpc_guard",
			SourceFile:  "libopus_celt_lpc_kernel.c",
			CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
			RefIncludes: []string{"src", "celt", "silk"},
			DeadStrip:   true,
		}
		configureCELTOracleReference(&cfg)
		return libopustest.BuildCHelper(cfg)
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT LPC float guard", err)
	}
	threshold := math.Float32bits(float32(1e-10))
	cases := []struct {
		name string
		bits uint32
	}{
		{"zero", 0},
		{"negative-zero", 0x80000000},
		{"below-threshold", threshold - 1},
		{"threshold", threshold},
		{"quiet-NaN", 0x7fc00000},
	}
	const order = celtPLCLPCOrder
	payload := libopustest.NewOraclePayloadVersion("GCLK", 1, uint32(len(cases)))
	for _, tc := range cases {
		var ac [order + 1]float32
		ac[0] = math.Float32frombits(tc.bits)
		ac[1] = 1 // A skipped recurrence must not consume this nonzero lag.
		payload.U32(order)
		payload.Float32s(ac[:]...)
	}
	reader, err := libopustest.RunOracleVersion(helper, payload.Bytes(), "CELT LPC float guard", "GCKO", 1)
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT LPC float guard", err)
	}
	if got := reader.Count(len(cases)); got != len(cases) {
		t.Fatalf("C guard case count=%d, want %d", got, len(cases))
	}
	for _, tc := range cases {
		if got := reader.Count(order); got != order {
			t.Fatalf("%s: C order=%d, want %d", tc.name, got, order)
		}
		var ac [order + 1]float32
		var got [order]float32
		ac[0] = math.Float32frombits(tc.bits)
		ac[1] = 1
		for i := range got {
			got[i] = 1 // Also check that skipped input clears existing output.
		}
		plcLPCFromAutocorr(ac[:], got[:])
		for i := range got {
			wantBits := math.Float32bits(reader.Float32())
			if gotBits := math.Float32bits(got[i]); wantBits != 0 || gotBits != wantBits {
				t.Fatalf("%s: LPC[%d]=%08x, C=%08x, want cleared output", tc.name, i, gotBits, wantBits)
			}
		}
	}
	if err := reader.Err(); err != nil {
		t.Fatal(err)
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
}
