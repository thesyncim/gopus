package libopustest

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestFixedCELTOracleHelpersUsePairedReference(t *testing.T) {
	for _, helper := range []struct {
		name string
		cfg  CHelperConfig
	}{
		{name: "decoder", cfg: celtDecodeHelperConfig()},
		{name: "synthesis", cfg: celtSynthesisHelperConfig()},
	} {
		t.Run(helper.name, func(t *testing.T) {
			cfg := helper.cfg
			if !cfg.FixedRef || cfg.FixedQEXTRef {
				t.Fatalf("non-QEXT CELT probe selects wrong C config: FixedRef=%t FixedQEXTRef=%t", cfg.FixedRef, cfg.FixedQEXTRef)
			}
			if got, want := filepath.Clean(cfg.Libs[0]), filepath.Clean(FixedRefPath(".libs", "libopus.a")); got != want {
				t.Fatalf("reference archive = %q, want fixed archive %q", got, want)
			}
			if slices.Contains(cfg.CFlags, "-DENABLE_QEXT") {
				t.Fatalf("non-QEXT CELT probe enables QEXT in C helper: %v", cfg.CFlags)
			}
		})
	}
}
