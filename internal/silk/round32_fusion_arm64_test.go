//go:build arm64

package silk

import "testing"

//go:noinline
func fusedMulAddProbe(a, b, c float32) float32 { return a*b + c }

// TestRound32DefeatsFusion guards the codegen contract behind noFMA32: round32
// must stop the arm64 backend from contracting a*b+c into a single FMADD, so the
// silk float paths keep the FMUL + FADD (two roundings) of scalar libopus that
// the byte-exact integer core depends on. If the compiler ever elided the
// float32 conversion in round32, noFMA32 would start fusing and this would find
// zero divergences.
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
		if fusedMulAddProbe(a, b, c) != noFMA32(a, b)+c {
			diverged++
		}
	}
	if diverged == 0 {
		t.Fatalf("noFMA32(a,b)+c never diverged from fused a*b+c over %d cases — round32 is NOT defeating FMADD contraction on this build", n)
	}
	t.Logf("round32 barrier confirmed: noFMA32 diverged from fused FMADD in %d/%d cases", diverged, n)
}

//go:noinline
func fusedMulAddProbe64(a, b, c silkCReal) silkCReal { return a*b + c }

// TestNoFMA64DefeatsFusion guards noFMA64 the same way: the conversion of the
// product must keep the arm64 backend from contracting it into an FMADD.
func TestNoFMA64DefeatsFusion(t *testing.T) {
	rng := uint64(0x9e3779b97f4a7c15)
	next := func() silkCReal {
		rng = rng*6364136223846793005 + 1442695040888963407
		return (silkCReal(rng>>11)/silkCReal(1<<53))*8 - 4
	}
	var diverged int
	const n = 5_000_000
	for range n {
		a, b, c := next(), next(), next()
		if fusedMulAddProbe64(a, b, c) != noFMA64(a, b)+c {
			diverged++
		}
	}
	if diverged == 0 {
		t.Fatalf("noFMA64(a,b)+c never diverged from fused a*b+c over %d cases; the conversion is not defeating FMADD contraction on this build", n)
	}
	t.Logf("noFMA64 barrier confirmed: diverged from fused FMADD in %d/%d cases", diverged, n)
}
