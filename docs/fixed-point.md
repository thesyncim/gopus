# Fixed-point pipeline

Scope, oracle recipe, ported kernels, and staged roadmap toward a full
`gopus_fixedpoint` integer CELT/SILK encode+decode pipeline.

The full fixed-point pipeline is the approved goal: it mirrors libopus
`FIXED_POINT` and lives behind the `gopus_fixedpoint` build tag, with zero cost
to the default (float) build. The default build does not import the fixed-point
packages, so there is no code-size or runtime impact when the tag is absent.

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

### Float in gopus, integer in libopus FIXED_POINT build

| Module | gopus implementation | libopus FIXED_POINT equivalent |
|---|---|---|
| CELT log2 / exp2 | `celt/log2_approx.go`, `celt/exp2_approx.go` — float32 FLOAT_APPROX polynomial | `celt/mathops.h` `celt_log2(int32)→int16`, `celt_exp2(int16)→int32` — integer polynomial |
| CELT rsqrt / rcp | `celt/pvq.go`, `celt/bands.go` — `1/sqrt(x)` via `math.Sqrt` | `celt/mathops.c` `celt_rsqrt_norm`, `celt_rcp` — integer Newton iteration |
| CELT MDCT / KissFFT | `celt/kissfft32.go`, `celt/mdct.go` — `float32` arrays and twiddles | `celt/kiss_fft.c` + `celt/mdct.c` — `kiss_fft_scalar = int32`, twiddles from `static_modes_fixed.h` |
| CELT band energy quant / normalization | `celt/energy.go`, `celt/bands.go` — `float32` | Uses integer log2/exp2 via `celt_ener` (Q14) and `celt_sig` (Q27) |
| CELT PVQ search | `celt/pvq_search.go` — `float32` inner products | `celt/vq.c` FIXED_POINT path — `celt_norm` (int32 Q24) inner products |
| SILK encoder analysis | `silk/pitch_detect.go`, `silk/lpc_analysis.go`, `silk/noise_shape_analysis.go`, etc. — `float32`/`float64` | `silk/fixed/` — 20+ files replacing every encoder analysis step with integer equivalents |
| SILK LPC synthesis output | `silk/lpc.go` — converts int32 excitation to `float32` normalized output | `silk/decode_core.c` keeps everything in int16 until final `silk_SMULWW(…, Gain_Q10)>>8` into `opus_int16` |

**Rough line count of float-domain work in gopus signal path** (non-test files):
- `celt/`: ~2 700 float references
- `silk/` encoder: ~740 float references
- `silk/` decoder output stage: ~60 float references (output conversion, LPC synthesis, resampler)

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

## Staged roadmap

### Stage 0 (done)
- `internal/fixedpoint`: CELT integer math kernels, property tests.

### Stage 1 — CELT fixed-point oracle (done)
- `FIXED_POINT` libopus reference build wired into the oracle infrastructure
  (`make ensure-libopus-fixed`, `FixedRefPath`, `CHelperConfig.FixedRef`).
- `celt_sqrt` ported (`CeltSqrt`) and covered by a bit-exact oracle test.
- Remaining within this stage: wire `celt_log2`, `celt_exp2`,
  `celt_rsqrt_norm`, `celt_rcp` into the same fixed oracle helper to replace
  their accuracy-only tests with bit-exact ones.

### Stage 2 — SILK decoder integer output (medium)
- Replace `silk/lpc.go`'s float32 synthesis with a direct Q14 accumulation that
  stays in int32 until the final `>>8` (matching `silk/decode_core.c`).
- Replace the float32 output buffer with an int16 buffer matching libopus's
  `outBuf [maxFrameLength + 2*maxSubFrameLength]int16`.
- Remove float normalization from `silk/decode.go` (or keep as a thin wrapper
  for the public float32 API).
- Effort: ~2–3 days.  Risk: the OSCE/DeepPLC hooks pass `[]float32` to the PLC
  bridge; the boundary needs careful handling.

### Stage 3 — CELT MDCT / KissFFT integer (large)
- Replace the `float32` twiddle tables with the fixed-point tables from
  `celt/static_modes_fixed.h`.
- Rewrite the butterfly kernels to use `MULT16_32_Q15` style integer arithmetic.
- This is the most invasive CELT change: ~1 000 lines across `kissfft32.go` and
  `mdct.go`, plus assembly paths on arm64 and amd64.
- Effort: 1–2 weeks.

### Stage 4 — CELT band processing integer (large)
- Convert band energy, normalization, PVQ search, alloc-trim, and spread to use
  `celt_norm` (int32 Q24) and `celt_ener` (int32 Q14) types.
- Effort: 2–3 weeks.

### Stage 5 — SILK encoder analysis integer (very large)
- Port `silk/fixed/` (~20 files: autocorr_FIX, burg_modified_FIX,
  pitch_analysis_core_FIX, noise_shape_analysis_FIX, find_LPC_FIX, etc.).
- These are structurally independent of the decoder; they only affect the
  encoder side.
- Effort: 3–4 weeks.

Full fixed-point parity (all five stages complete) is a 6–8 week effort.

---

## Approach

The full fixed-point pipeline is the approved goal. It is built incrementally,
each step gated by a bit-exact `FIXED_POINT` oracle, behind the
`gopus_fixedpoint` tag so the default float build stays untouched and zero-cost.

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
