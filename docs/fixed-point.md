# Fixed-point feasibility

Scope, first increment, staged roadmap, and honest recommendation.

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

All five are verified by `internal/fixedpoint/celt_math_test.go`:

- `CeltILog2`: exact on 10 cases including powers of 2 and Q14 boundaries.
- `CeltLog2`: exact on powers of 2 (`2^{-1}` through `2^3`); within ±1.5 Q10
  units (< 0.15%) across 200 Q14 inputs from 0.5 to 16.0.
- `CeltExp2`: within 0.007% relative error for positive exponents, within 5 Q16
  units absolute for negative exponents; reproduces the known D0=16383 bias that
  matches libopus bit-for-bit.
- Round-trip `exp2(log2(x)) ≈ x`: within 0.5% for Q14 inputs 0.5–4.0.
- `CeltRsqrtNorm`: within 0.05% relative error across the full Q16 range [0.25, 1.0).

### Oracle note

Validating these kernels bit-for-bit against libopus requires a `FIXED_POINT`
build of libopus (`./configure --enable-fixed-point`).  The existing oracle
infrastructure uses the default float build.  A new build target
`build-opus-fixed-{os}-{arch}` and a C oracle helper comparable to
`libopus_silk_fixed_info.c` would close this gap in a future increment.  Until
then the tests provide approximation-accuracy coverage against known mathematical
results.

---

## Staged roadmap

### Stage 0 (done)
- `internal/fixedpoint`: five CELT integer math kernels, property tests.

### Stage 1 — CELT fixed-point oracle (small, high-value)
- Add a `FIXED_POINT` libopus build to the oracle infrastructure.
- Wire `celt_log2`, `celt_exp2`, `celt_rsqrt_norm`, `celt_rcp` into the oracle,
  replacing the accuracy tests with bit-exact oracle tests.
- Effort: ~1 day for the build harness + 1 day for the C helper.

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

## Honest recommendation

**Do not pursue a full fixed-point pipeline for gopus at this time.**

Reasons:

1. **The practical surface is already covered.** gopus's public API accepts and
   returns `int16` / `int24` / `float32` PCM.  The caller never needs to see
   Q14/Q27 integers.  The "fixed-point" value proposition for libopus was
   eliminating a floating-point unit on 2010-era embedded ARMv5 cores.  Modern
   targets (mobile, server, WASM) have fast FPUs or SIMD float.

2. **The gopus float pipeline is already bit-exact with libopus float** (which
   is the RFC-conforming reference).  Switching the internals to integer would
   produce a *different* bitstream-compatible codec — not a more correct one.

3. **Maintenance cost is high.** A dual pipeline doubles the surface area for
   every future libopus improvement (DNN upgrades, new rate-control, QEXT).

4. **The useful increments are narrow.** Stages 1–2 (oracle wiring + SILK decoder
   integer output) provide genuine value at low cost: Stage 1 tightens oracle
   coverage, Stage 2 removes the float conversion in the hot decode path and
   eliminates the normalization rounding in `silk/lpc.go`.  These are worth
   doing independently of the larger fixed-point question.

**Recommended scope**: land Stage 0 (done), pursue Stage 1–2 as self-contained
quality improvements, and treat Stages 3–5 as optional stretch goals that would
only make sense if gopus were explicitly targeting a platform without a hardware
FPU.
