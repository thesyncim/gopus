package celt

import "github.com/thesyncim/gopus/rangecoding"

// Decoder tracing is intentionally compiled out.
// All trace hooks are no-ops.

func traceHeader(_ int, _ int, _ int, _ int, _ int) {}

func traceEnergy(_ int, _ float64, _ float64, _ float64) {}

func traceAllocation(_ int, _ int, _ int) {}

func tracePVQ(_ int, _ uint32, _ int, _ int, _ []int) {}

func traceCoeffs(_ int, _ []float64) {}

func traceSynthesis(_ string, _ []float64) {}

func traceRange(_ string, _ *rangecoding.Decoder) {}

func traceFlag(_ string, _ int) {}

func traceFineBits(_ int, _ int) {}

func traceTF(_ int, _ int) {}

func traceLowband(_ int, _ int, _ int, _ []float64) {}

func traceEnergyFine(_ int, _ int, _ float64) {}

func traceEnergyFinal(_ int, _ int, _ float64) {}

// boolToInt converts a boolean to int (0 or 1).
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
