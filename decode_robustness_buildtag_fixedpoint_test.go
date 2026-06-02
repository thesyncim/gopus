//go:build gopus_fixedpoint

package gopus

// robustFixedPointDecode is true under gopus_fixedpoint: gopus decodes int16/int24
// through the integer path, so the float-oracle PCM-value guard does not apply
// (see decode_robustness_buildtag_default_test.go for the rationale). The no-panic
// and accept/reject invariants are still enforced under this build.
const robustFixedPointDecode = true
