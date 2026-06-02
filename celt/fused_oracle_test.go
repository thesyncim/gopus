package celt

import "testing"

// requireBitExactFloat skips a Tier-1 bit-exact oracle (gopus vs scalar libopus)
// on the fused arm64 default build, where the CELT float path is deliberately
// quality-gated (opus_compare) rather than byte-identical to scalar C — the same
// posture libopus's own NEON kernels take vs scalar libopus. Bit-exact coverage
// of these kernels runs on the purego and amd64 builds (celtFusedFloat == false);
// the fused build's correctness is covered by the end-to-end quality gates.
func requireBitExactFloat(t *testing.T) {
	t.Helper()
	if celtFusedFloat {
		t.Skip("bit-exact vs scalar libopus; fused arm64 default build is quality-gated (purego/amd64 hold the bit-exact oracle)")
	}
}
