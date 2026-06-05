//go:build arm64

package celt

import "testing"

//go:noinline
func fusedMulAddProbe(a, b, c float32) float32 { return a*b + c }

// TestRound32DefeatsFusion guards the codegen contract: round32(a*b) must stop
// the arm64 backend from contracting a*b+c into a single FMADD, so mulAdd32Ref
// matches scalar libopus. If the compiler ever elided the float32 conversion,
// mulAdd32Ref would start fusing and this would find zero divergences.
func TestRound32DefeatsFusion(t *testing.T) {
	rng := uint64(0x9e3779b97f4a7c15)
	next := func() float32 {
		rng = rng*6364136223846793005 + 1442695040888963407
		return (float32(rng>>40)/float32(1<<24))*8 - 4
	}
	var diverged int
	const n = 5_000_000
	for range n {
		a, b, c := next(), next(), next()
		if fusedMulAddProbe(a, b, c) != mulAdd32Ref(a, b, c) {
			diverged++
		}
	}
	if diverged == 0 {
		t.Fatalf("round32(a*b)+c never diverged from fused a*b+c over %d cases — round32 is NOT defeating FMADD contraction on this build", n)
	}
	t.Logf("round32 barrier confirmed: diverged from fused FMADD in %d/%d cases", diverged, n)
}
