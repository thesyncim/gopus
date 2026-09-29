//go:build amd64 && goexperiment.simd && !nosimd && !purego

package encoder

import (
	"simd/archsimd"
	"testing"
)

// The disabled dispatch must leave the complete bin range to scalar analysis,
// including the phase history and exceptional-value cases in its oracle.
func TestAnalysisBinsCPUFallback(t *testing.T) {
	if analysisBinsUseAVX2 && !archsimd.X86.AVX2() {
		t.Fatal("analysis vector dispatch requires AVX2")
	}
	saved := analysisBinsUseAVX2
	analysisBinsUseAVX2 = false
	t.Cleanup(func() { analysisBinsUseAVX2 = saved })
	var s TonalityAnalysisState
	if first := s.analysisBinsSIMD(nil, nil, nil, nil); first != 1 {
		t.Fatalf("fallback starts at bin %d, want 1", first)
	}
	t.Run("exact", TestAnalysisBinsMatchesScalar)
	t.Run("allocations", TestAnalysisBinsZeroAllocs)
}
