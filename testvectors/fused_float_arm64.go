//go:build arm64 && !purego

package testvectors

// fusedFloat reports whether this build lets the compiler fuse a*b+c into FMADD
// in the CELT float path (the default arm64/asm build, see celt/fma32_arm64_fast.go).
// Such a build is quality-gated (opus_compare), not byte-identical to scalar
// libopus, exactly like libopus's own NEON kernels. The byte-exact oracle is the
// purego build (and amd64, which does not fuse), where fusedFloat is false.
const fusedFloat = true
