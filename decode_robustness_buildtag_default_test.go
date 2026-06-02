//go:build !gopus_fixedpoint

package gopus

// robustFixedPointDecode reports whether the integer FIXED_POINT decode path is
// active (build tag gopus_fixedpoint). The robustness gross-PCM-on-corrupt-input
// guard compares gopus float decode against the libopus FLOAT multistream oracle,
// an apples-to-apples comparison only in the default build. Under gopus_fixedpoint
// gopus decodes via the integer path while this oracle is the float build, so the
// PCM-value guard would compare two different arithmetics on garbage input (the
// accept/reject + no-panic invariants stay enforced under both builds). The
// integer decode's PCM parity is covered by decode_differential_fuzz_fixedpoint_test.go
// against the FIXED_POINT oracle on clean packets.
const robustFixedPointDecode = false
