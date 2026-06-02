# libopus 1.6.1 Parity Matrix

Reference: pinned `tmp_check/opus-1.6.1/` (libopus **1.6.1**); pinned behavior wins.
This is the gap list against libopus — see [docs/parity-testing.md](docs/parity-testing.md)
for how parity is verified and [README.md](README.md) for support claims.

gopus is a libopus 1.6.1 drop-in. The default build is core
encode/decode/multistream/Ogg/RED; libopus's optional surface is exposed behind
matching build tags, **zero-cost by default** (enforced by
`TestDefaultBuildIsZeroCostForGatedFeatures`).

## Build tag ↔ libopus flag
| gopus tag | libopus flag |
| --- | --- |
| `gopus_dred` | `--enable-dred` |
| `gopus_extra_controls` | `--enable-osce` (+ `ENABLE_DEEP_PLC`) |
| `gopus_qext` | `--enable-qext` |
| `gopus_custom` | `--enable-custom-modes` |
| `gopus_fixedpoint` | `--enable-fixed-point` |

## How parity is verified
Two tiers ([docs/parity-testing.md](docs/parity-testing.md)): **bit-exact** numeric
oracles for isolated kernels are hard gates on the `purego`/scalar build (the
`build-config matrix`); end-to-end audio uses `opus_compare` (RFC 8251),
**tier-matched** — asm gopus vs SIMD libopus, pure-Go gopus vs scalar libopus.
Near-exact bar: Q≥20, corr≥0.997. SILK decode is bit-exact (Q=100); CELT/Hybrid
sit at Q≥99.77 (corr/RMS=1.0).

## Status vs libopus (Y = parity · T = parity under build tag · gap noted)
| libopus feature | gopus | Gap vs libopus |
| --- | --- | --- |
| Encode/Decode `float32`/`int16`/`int24`; SILK/CELT/Hybrid/auto | **Y** | — |
| API rates 8/12/16/24/48 kHz (incl. native sub-48k encode) | **Y** | — |
| Mono, stereo, multistream, projection (families 0/1/3/255) | **Y** | unsupported ambisonics orders → `ErrProjectionOrderUnsupported` (no libopus matrices) |
| Frame sizes 2.5–120 ms; CBR/VBR/CVBR/low-delay/DTX | **Y** | — |
| PLC, in-band FEC/LBRR, RTP RED (`container/red`) | **Y** | — |
| Ogg read/write (`container/ogg`); packet parse; CTLs; error codes | **Y** | — |
| 24-bit `EncodeInt24`/`DecodeInt24` (incl. DRED `DecodeInt24`) | **Y** | — |
| DRED (RDOVAE) | **T** `gopus_dred` | — |
| OSCE BWE / LACE / NoLACE (BWE sample-aligned bit-exact) | **T** `gopus_extra_controls` | — |
| QEXT / Opus HD 96 kHz: decode + native encode byte-exact; Hybrid QEXT framing | **T** `gopus_qext` | — |
| Opus Custom modes | **T** `gopus_custom` | `NbEBands>21` layouts → `ErrNonStandard` (gap 1) |
| Fixed-point (`FIXED_POINT`) decode + encode | **T** `gopus_fixedpoint` | — |
| `opus_pcm_soft_clip` / `opus_strerror` / version | **Y** | — |

## Open gaps against libopus
1. **Opus Custom `NbEBands>21`** (high-Fs + small short-MDCT, e.g. 32000/100): the
   shared CELT energy/history buffers are sized by `MaxBands`(21), so 22–23-band
   custom layouts return `custom.ErrNonStandard` instead of a non-conformant
   bitstream. Native parity needs a multi-session resize of the shared CELT data
   plane. Standard and all within-cap (`≤21`-band) custom modes are byte-exact.
2. **arm64 ≤1-ULP per-arch budget** (amd64/CI bit-exact): a few CELT float kernels
   diverge ≤1 ULP on darwin/arm64 — the `fma32` IMDCT-twiddle FMADD contraction
   (decode), the SILK `hp_cutoff` biquad / warped-shaping AR (encode), and CELT
   `alloc_trim` tonality. The default arm64 (asm) build is quality-gated for this
   tail (like libopus's own NEON vs scalar); the `purego` + amd64 builds remain the
   bit-exact oracle. Governed by the quality bar, documented per-arch.

The optional/ML **feature scope** above (DRED, OSCE, QEXT, Opus Custom,
fixed-point) mirrors libopus's compile-flag surface exactly and links zero code in
the default build.

## Verify
`make verify-production` (+ `verify-production-exhaustive`). Required CI checks and
release process: [README.md](README.md). Design docs:
[docs/parity-testing.md](docs/parity-testing.md),
[docs/fixed-point.md](docs/fixed-point.md),
[docs/opus-hd-96k.md](docs/opus-hd-96k.md).
