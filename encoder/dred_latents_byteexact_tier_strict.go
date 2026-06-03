//go:build purego || amd64

package encoder

// dredLatentsByteExactTier selects whether the encoder DRED RDOVAE latent-trace
// parity tests hold gopus's extracted latents tight to the libopus reference.
//
// This is the bit-exact tier: the pure-Go build (-tags purego, any arch) and the
// amd64 SIMD build both track libopus's DRED RDOVAE float feature extraction
// closely enough that the encoder latents stay within the tight base tolerance.
// It mirrors the root-package dredPayloadByteExactTier so the encoder latent
// trace and the carried-DRED payload sit on the same two-tier model.
const dredLatentsByteExactTier = true
