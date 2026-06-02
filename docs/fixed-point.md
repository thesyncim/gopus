# Fixed-point pipeline

Scope, status, oracle recipe, and ported kernels for the `gopus_fixedpoint`
integer CELT/SILK pipeline. Public decode and encode are bit-exact vs the
`--enable-fixed-point` libopus oracle.

The full fixed-point pipeline mirrors libopus `FIXED_POINT` and lives behind the
`gopus_fixedpoint` build tag, with zero cost to the default (float) build. The
default build does not import the fixed-point packages, so there is no code-size
or runtime impact when the tag is absent.

---

## Status (current)

Under `-tags gopus_fixedpoint`, the public **DECODE** path is bit-exact with the
`--enable-fixed-point` libopus oracle:

- **CELT-only** `DecodeInt16` / `DecodeInt24` — byte-/sample-exact
  (`TestDecoderFixedPointCELTParity`).
- **SILK-only** `DecodeInt16` / `DecodeInt24` — byte-/sample-exact
  (`TestDecoderFixedPointSILKParity`); gopus SILK decode is inherently integer
  (int16 native samples + int16 resampler), so the existing path is already
  FIXED_POINT-exact.
- **Hybrid** `DecodeInt16` / `DecodeInt24` — byte-/sample-exact
  (`TestDecoderFixedPointHybridParity`): the integer SILK lowband (`INT16TORES`)
  is combined with the integer CELT highband (start band 17, `celt_accum`) from
  the shared range decoder.

All three are hard-exact on amd64/CI and bound the documented per-arch ≤1-ULP
CELT drift on arm64 (`project_arm64_celt_1ulp_drift.md`).

The integer FFT/MDCT/bands/PVQ/mathops kernels are **assembled into a full
integer CELT codec** that is byte-/sample-exact vs FIXED_POINT, covering encode
(CBR/VBR/CVBR, LM0–3, complexity 0/5/8/10, 6–510 kb/s, multi-frame stateful) and
decode (mono/stereo, normal/transient, LM1–3, 32k–128k, sub-48k downsample,
periodic+noise PLC).

The public **DECODE** path is now integer-exact for the previously
float-fallback cases as well: Hybrid-with-redundancy and the CELT↔SILK /
mode-transition crossfades (`TestDecoderFixedPointHybridRedundancyTransitionParity`,
`TestDecoderFixedPointHybridTransitionParity`,
`TestDecoderFixedPointStereoRedundancyBothDirectionsParity`,
`TestDecoderFixedPointStereoCELTTransitionParity`), CELT-burst PLC
(`TestDecoderFixedPointCELTPLCParity`, `TestDecoderFixedPointStereoCELTRecoveryParity`,
`TestDecoderFixedPointStereoLongPLCChurnParity`), and a non-zero decode gain
(`TestDecoderFixedPointDecodeGainParity`): the FIXED_POINT decode-gain stage
(`celt_exp2(MULT16_16_P15(QCONST16(6.48814081e-4f,25), decode_gain))` ->
`MULT32_32_Q16` -> `SATURATE`) is applied to the integer opus_res accumulation
(`fixedpoint.DecodeGainQ16` / `ApplyDecodeGainRes`).

The public **ENCODE** path is integer-exact too: the integer SILK
`silk_encode_frame` driver (analysis + payload + stereo) and the CELT encode path
(full-band, the hybrid CELT band subset with start>0, LFE, and surround
energy_mask) are assembled and byte-exact vs FIXED_POINT
(`TestPublicSILKEncodeFrameFixedByteExact`,
`TestPublicStereoSILKEncodeFixedByteExact`, `TestCELTEncode*Oracle`).

QEXT encode under the fixed-point pipeline is out of scope, mirroring the libopus
compile-flag boundary: `ENABLE_QEXT` and the FIXED_POINT reference build are
mutually exclusive.

---

## What "fixed-point Opus" means

libopus 1.6 ships two independently compiled pipelines, selected by `-DFIXED_POINT`
at build time:

| Path | What it is |
|---|---|
| **float** (default) | All signal-domain values are `float` / `double`.  Twiddles, norms, log2/exp2 are IEEE-754 single-precision throughout CELT.  SILK uses a mix: decoder and range-coder are integer-exact; encoder analysis (Burg, pitch, noise shape) is float. |
| **FIXED_POINT** | `opus_val16/32` become `int16/int32`; signal is Q27 (`celt_sig`); band norms are Q24 (`celt_norm`); energy is Q14 (`celt_ener`).  log2/exp2/rsqrt become integer polynomial kernels.  The MDCT/KissFFT twiddle factors become integer tables.  SILK's shared decode path is unchanged (already integer); SILK encoder analysis switches to the `silk/fixed/` variants (autocorr_FIX, burg_modified_FIX, pitch_analysis_core_FIX, etc.). |

The two pipelines are **bitstream-compatible** (same range coder, same codebooks,
same frame/packet structure) but produce different reconstructed audio — the
fixed-point path has slightly more rounding noise. RFC 6716 conformance is
defined via the float build; the fixed-point build is a mobile/embedded profile.

---

## Module-by-module inventory for gopus

### Already integer-exact

| Module | Status |
|---|---|
| `rangecoding` (encoder + decoder) | Pure integer.  Port of `celt/entenc.c` + `celt/entdec.c`.  No floating-point. |
| `silk` decode core — `libopus_decode.go`, `libopus_state.go`, `libopus_fixed.go`, `libopus_scalar.go` | Integer-exact.  Matches `silk/decode_core.c` (shared between float and fixed-point builds).  The SILK arithmetic primitives (`silkSMULWB`, `silkRSHIFT_ROUND`, `silkDiv32VarQ`, …) are oracle-tested against the float libopus build (those macros compile with no FIXED_POINT guard). |
| `silk` tables, codebooks, NLSF decode, gain dequant, LTP, excitation decode | Integer. |
| CELT CWRS encode/decode (`celt/cwrs.go`) | Integer-exact; oracle-tested. |
| CELT alloc tables, modes, spread counts | Integer constants / tables. |

### Float in the default build; bit-exact integer under `gopus_fixedpoint`

The default (float) build keeps these modules in float32 — the float pipeline is
the RFC-conforming reference and stays untouched. Under `-tags gopus_fixedpoint`,
`internal/fixedpoint` provides bit-exact integer counterparts (oracle-verified
against `--enable-fixed-point` libopus) and assembles them into the full integer
CELT codec described under "Status".

| Module | default-build (float) implementation | integer counterpart under `gopus_fixedpoint` |
|---|---|---|
| CELT log2 / exp2 | `celt/log2_approx.go`, `celt/exp2_approx.go` — float32 FLOAT_APPROX polynomial | integer `celt_log2`/`celt_exp2`/`celt_exp2_frac` — bit-exact |
| CELT rsqrt / rcp / sqrt | `celt/pvq.go`, `celt/bands.go` — via `math.Sqrt` | integer `celt_rsqrt_norm`/`celt_rcp`/`celt_sqrt`/`sqrt32`/`cos_norm`/`frac_div32` — bit-exact |
| CELT MDCT / KissFFT | `celt/kissfft32.go`, `celt/mdct.go` — `float32` arrays and twiddles | integer KISS-FFT `kf_bfly2/3/4/5` + driver and integer MDCT fwd/bwd (`kiss_fft_scalar = int32`, fixed twiddles) — bit-exact |
| CELT band energy quant / normalization | `celt/energy.go`, `celt/bands.go` — `float32` | integer `compute_band_energies`/`normalise_bands`/`denormalise_bands`/`anti_collapse`/`renormalise_vector`/`amp2Log2` (`celt_ener` Q14, `celt_sig` Q27) — bit-exact |
| CELT PVQ search | `celt/pvq_search.go` — `float32` inner products | integer `op_pvq_search` + `alg_quant`/`alg_unquant` (`celt_norm` int32 Q24; range-coder byte-exact) — bit-exact |
| CELT comb filter | `celt/prefilter.go` — `float32` | integer `comb_filter` — bit-exact |
| SILK encoder analysis | `silk/pitch_detect.go`, `silk/lpc_analysis.go`, `silk/noise_shape_analysis.go`, etc. — `float32`/`float64` | integer encoder-analysis kernels (autocorr/Burg LPC, `schur`/`k2a`(+`_Q16`/64), `find_LTP`, `process_gains`, warped autocorr/gain, sine window, residual energy, pitch `calc_energy_st3`, NSQ noise-shape quantizer) — bit-exact, assembled into the byte-exact `silk_encode_frame` driver |
| SILK LPC synthesis output | `silk/lpc.go` — converts int32 excitation to `float32` | gopus SILK decode is inherently integer; SILK-only `DecodeInt16`/`DecodeInt24` are already FIXED_POINT-exact |

---

## First tractable increment — CELT fixed-point math kernels

**Location**: `internal/fixedpoint/celt_math.go`

The four most self-contained fixed-point kernels from `celt/mathops.h` are
implemented as pure-integer Go, with no impact on the existing float pipeline:

| Function | libopus source | Description |
|---|---|---|
| `CeltILog2(int32) int16` | `celt_ilog2()` | `floor(log2(x))` via `bits.LeadingZeros32` |
| `CeltLog2(int32) int16` | `celt_log2()` Q14→Q10 | Degree-4 polynomial log2 approximation |
| `CeltExp2Frac(int16) int16` | `celt_exp2_frac()` | Degree-3 polynomial fractional exp2 |
| `CeltExp2(int16) int32` | `celt_exp2()` Q10→Q16 | exp2 via CeltExp2Frac + integer exponent |
| `CeltRsqrtNorm(int32) int16` | `celt_rsqrt_norm()` Q16→Q14 | Reciprocal sqrt via quadratic + Householder |
| `CeltSqrt(int32) int32` | `celt_sqrt()` Q16→Q16 | Degree-5 polynomial sqrt over .25<x<1 with ilog2 range reduction |

The polynomial kernels are verified by `internal/fixedpoint/celt_math_test.go`:

- `CeltILog2`: exact on 10 cases including powers of 2 and Q14 boundaries.
- `CeltLog2`: exact on powers of 2 (`2^{-1}` through `2^3`); within ±1.5 Q10
  units (< 0.15%) across 200 Q14 inputs from 0.5 to 16.0.
- `CeltExp2`: within 0.007% relative error for positive exponents, within 5 Q16
  units absolute for negative exponents; reproduces the known D0=16383 bias that
  matches libopus bit-for-bit.
- Round-trip `exp2(log2(x)) ≈ x`: within 0.5% for Q14 inputs 0.5–4.0.
- `CeltRsqrtNorm`: within 0.05% relative error across the full Q16 range [0.25, 1.0).

### FIXED_POINT oracle

Bit-exact validation against libopus uses a dedicated `--enable-fixed-point`
reference build, parallel to the existing float and QEXT reference trees:

```
make ensure-libopus-fixed     # builds tmp_check/opus-1.6.1-fixed/ (config.h: FIXED_POINT 1)
make test-fixedpoint-parity   # ensure-libopus-fixed + the gopus_fixedpoint oracle tests
```

Mechanics (reuses the existing `ensure-libopus` / `BuildCHelper` pattern, no new
mechanism):

- `tools/ensure_libopus.sh` gained `LIBOPUS_ENABLE_FIXED=1`, mirroring
  `LIBOPUS_ENABLE_QEXT`. It configures with `--enable-fixed-point` into
  `tmp_check/opus-$(VERSION)-fixed/`, whose generated `config.h` defines
  `FIXED_POINT 1`. `LIBOPUS_ENABLE_QEXT` and `LIBOPUS_ENABLE_FIXED` are mutually
  exclusive.
- `libopustooling.EnsureLibopusFixed` and `libopustest.FixedRefPath` expose the
  fixed tree; `CHelperConfig.FixedRef=true` builds a C oracle helper against it.
- `tools/csrc/libopus_celt_fixed_math_info.c` is the C oracle helper. It is
  compiled with the fixed `config.h`, so the `celt_*` symbols resolve to their
  integer paths, and links `opus-1.6.1-fixed/.libs/libopus.a`.
- `internal/fixedpoint/celt_sqrt_oracle_test.go` (build tag `gopus_fixedpoint`)
  drives the helper and asserts bit-exact equality across 20,535 inputs
  (special cases, every `ilog2`/`k` bucket, a dense low-magnitude sweep, and a
  strided sweep of the full non-negative `int32` range).

The accuracy-only tests in `celt_math_test.go` remain as a fast, tag-free
sanity layer; the oracle test is the bit-exact gate.

---

## Roadmap (status)

### Stage 0 — CELT integer math kernels (done)
- `internal/fixedpoint`: CELT integer math kernels + property tests.

### Stage 1 — CELT fixed-point oracle (done)
- `FIXED_POINT` libopus reference build wired into the oracle infrastructure
  (`make ensure-libopus-fixed`, `FixedRefPath`, `CHelperConfig.FixedRef`).
- `celt_sqrt`/`celt_log2`/`celt_exp2`/`celt_rsqrt_norm`/`celt_rcp` covered by
  bit-exact oracle tests.

### Stage 2 — SILK decoder integer output (done)
- gopus SILK decode is inherently integer; SILK-only `DecodeInt16`/`DecodeInt24`
  are FIXED_POINT-exact (`TestDecoderFixedPointSILKParity`).

### Stage 3 — CELT MDCT / KissFFT integer (done)
- Integer KISS-FFT butterflies (`kf_bfly2/3/4/5`) + driver and integer MDCT
  fwd/bwd, oracle-verified bit-exact.

### Stage 4 — CELT band processing integer (done)
- Integer band energy, normalization, PVQ search, `alg_quant`/`alg_unquant`,
  anti-collapse, renormalise — assembled into the full integer CELT codec
  (encode CBR/VBR/CVBR + decode incl. PLC), oracle-verified bit-exact.

### Stage 5 — Public DECODE integer-exact (done)
- `DecodeInt16`/`DecodeInt24` are bit-exact vs FIXED_POINT for CELT-only,
  SILK-only, and Hybrid (`TestDecoderFixedPoint{CELT,SILK,Hybrid}Parity`),
  including Hybrid-with-redundancy, CELT↔SILK / mode-transition crossfades,
  CELT-burst PLC, and a non-zero decode gain (no float fallback remains for these).

### Stage 6 — Public ENCODE integer-exact (done)
- The `silk_encode_frame` integer driver (analysis + payload + stereo) is
  assembled and byte-exact vs FIXED_POINT
  (`TestSILKEncodeFrameFIXLibopusParity`,
  `TestSILKEncodeFramePayloadFIXLibopusParity`,
  `TestPublicSILKEncodeFrameFixedByteExact`,
  `TestPublicStereoSILKEncodeFixedByteExact`).
- CELT encode covers the full-band path, the hybrid CELT band subset
  (start>0), LFE and surround energy_mask, oracle-verified
  (`TestCELTEncode{WithEC,StartBand,LFE,EnergyMask}Oracle`,
  `TestCELTEncodeWithECVBROracle`, `TestCELTEncodeWithECCBRSeqOracle`).

### Out of scope
- **QEXT encode** under the fixed-point pipeline mirrors the libopus compile-flag
  boundary: `ENABLE_QEXT` is mutually exclusive with the FIXED_POINT reference
  build.

---

## Approach

The fixed-point pipeline is built incrementally, each step gated by a bit-exact
`FIXED_POINT` oracle, behind the `gopus_fixedpoint` tag so the default float
build stays untouched and zero-cost. Public decode and encode are bit-exact
today; QEXT encode follows the same discipline if/when its compile-flag boundary
is brought in.

Build discipline:

1. **Oracle first.** Every kernel lands with a bit-exact test against the
   `--enable-fixed-point` libopus reference. Integer rounding, shifts, and
   saturation match libopus exactly; an approximation that is not bit-exact is
   not committed.
2. **Idiomatic Go, exact integer semantics.** Kernels are written as readable Go
   that reproduces the libopus integer arithmetic (`MULT16_16_Q15`, `VSHR32`,
   `ADD16`/`ADD32` truncation, etc.) rather than transliterating C.
3. **Zero default-build cost.** `internal/fixedpoint` is not imported by any
   default-build package, and the oracle tests carry the `gopus_fixedpoint`
   build tag, so `go build ./...` and tag-free `go test` see no fixed-point
   code.

The float pipeline remains the RFC-conforming reference and is unchanged; the
fixed-point pipeline is a separate, bitstream-compatible profile selected by the
build tag.
