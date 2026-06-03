//go:build !purego && !amd64

package encoder

// dredLatentsByteExactTier is false on the fused/SIMD non-amd64 build (the
// default arm64 NEON build): DRED RDOVAE feature extraction is float, and the
// quality-gated fused kernels round differently than the scalar reference across
// Apple-Silicon/clang NEON revisions. A small per-feature drift accumulates
// through the RDOVAE encoder GRU/conv stack, so the extracted latents are not
// guaranteed to match the libopus reference to the tight base tolerance across
// arm64 runners.
//
// The latent-trace parity tests therefore allow a small justified tolerance on
// this tier (the same arm64-NEON-drift rationale as dredPayloadByteExactTier);
// the bit-exact base tolerance is enforced on the amd64 and purego tier.
const dredLatentsByteExactTier = false
