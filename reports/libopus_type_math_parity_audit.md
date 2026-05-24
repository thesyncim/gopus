# libopus Type and Math Parity Audit

Date: 2026-05-24

Reference source: `tmp_check/opus-1.6.1`

Target: exact libopus parity for scalar widths, persistent state, signal buffers, temporary scratch buffers, and fixed-point arithmetic. Public Go interfaces may break. Treat this as a coordination file for multiple agents: claim one lane, make the local change, add/adjust parity tests, then mark the lane status.

## Current Goal Integration Notes

Status as of 2026-05-24:

- A01 public PCM API is partial: the top-level `Encode([]float32, ...)` path now enters `encoder.Encoder` through a float32 entry point instead of setting a separate float-input side channel before widening. The deeper Opus/SILK/CELT core still carries transitional `float64` PCM buffers, so A01 is not done.
- A02 Opus encoder state is partial but substantially reduced: `StereoWidthMem` now mirrors libopus `StereoWidthState` with `opusVal32`/`opusVal16`, DTX `peakSignalEnergy` is `opusVal32`, hybrid HB gain state is `opusVal16`, hybrid stereo widths are `opus_int16`-width, frame-energy and digital-silence threshold math run in the libopus float domain, DRED/Opus-VAD activity checks no longer widen peak/energy comparisons, and the stereo-width NaN guard now uses an explicit `opusVal32` bit test instead of Go's `float64` math path. CELT-facing delay compensation, mode-transition prefill, SILK transition prefill, hybrid transition redundancy/gain-fade scratch, DC reject scratch, LSB-quantized input scratch, the Opus wrapper input queue, DRED latent input, and Opus-VAD subframe input now use `opusRes`/`opusVal*` storage. The remaining A02 debt is the broader public/internal `[]float64` PCM bridge into unmigrated SILK/CELT cores, plus wrapper-only conversion scratch such as `scratchInputPCM64`.
- A03 CELT core vectors are active and oracle-backed: `aa373dcd` fixes strict CELT VQ oracle cases, and the current follow-up moves CELT coarse/fine/final energy residual scratch to `[]celtGLog` with no-clear preservation for shared residual state. QEXT encoder- and decoder-side old-band-energy/residual scratch now use `celtGLog`, DRED retained baseline scratch uses `celtSig`, decode-side band/PVQ shape storage, folding scratch, and decoded PVQ norm scratch use `celtNorm`, and legacy IMDCT scratch routes through the float32 IMDCT path instead of local `complex128` work buffers. Broad runtime vector and scratch migration remains open. The next highest-risk CELT mismatch is the remaining shared energy, encode-side band/PVQ, QEXT spectrum, anti-collapse, MDCT/KISSFFT64, and synthesis scratch that still bridges libopus `celt_glog`/`celt_norm`/`celt_sig` values through `float64` or `complex128`.
- A04/A05 MDCT/FFT is now partially active: `imdctScratch` aliases the float32 scratch shape and the legacy overlap/in-place IMDCT helpers route through the float32 IMDCT path, removing their reusable `complex128` scratch allocation. This is not complete: `KissFFT64State`, `complex128` twiddle/runtime helpers, `mdctTwiddleSet`, `mdct_libopus.go`, and OSCE LACE FFT callers remain 64-bit runtime debt.
- A07 SILK FLP storage is active and oracle-backed: `e04a55db` links the SILK LPC oracle to the configured libopus archive and `ffa3a0d3` makes that oracle protocol endian-stable. Follow-ups split LPC/Burg boundaries so persistent `silk_float` storage and FindLPC residual/input scratch use `float32`, while true `burg_modified_FLP.c` C `double` work arrays stay `float64`; the current type-width pass also removes the widened pitch residual copy, feeds pitch analysis/LTP/noise-shaping from `[]float32`, computes sparseness with `energyF32Libopus`, keeps residual-energy/gain-processing scratch in `silk_float` storage, replaces the FindLPC interpolation NLSF-to-LPC float64 polynomial scratch with the libopus-style `silk_NLSF2A_FLP` fixed bridge plus `silk_float` output storage, and moves NSQ noise-shaping Q controls (`LambdaQ10`, `HarmShapeGainQ14`, `TiltQ14`, `LTPScaleQ14`) from Go `int` to `int32` with float32 round-to-even conversion against the C `silk_float2int` oracle. More SILK scratch remains open: legacy float stereo predictor helpers and stale double LPC/LSF helpers must either migrate to `float32`/fixed wrappers or be deleted so accidental reuse cannot reintroduce double-domain parity drift.
- A08 fixed math is partial: `8f525312` added C-backed oracle coverage for SILK fixed wrapper primitives and corrected the reversed-bound `silk_LIMIT` behavior for `silkLimitInt`/`silkLimit32`; follow-ups check both `silkLShiftSAT32` and `silk_LSHIFT_SAT32` against the C oracle, fix the `shift == 31` saturation edge, and add a C-backed NSQ delayed-decision error-path oracle for the composed `ADD_SAT32`/`ADD32_ovflw`/`SUB_SAT32`/`RSHIFT_ROUND` arithmetic.
- A10 extension validation has support work landed: `4c9a5188` made generated DNN model blob headers explicit little-endian through a shared C writer. CELT DRED's retained neural crossfade baseline now uses `celtSig`, but the A10 runtime conversion goal remains open until the remaining OSCE/DRED/LPCNet codec-domain `float64`/`complex128` paths are migrated or source-cited.
- A11 oracle/build infrastructure is active: `32f37ba4`, `e04a55db`, `ffa3a0d3`, `458dd69b`, `4c9a5188`, `aa373dcd`, `24960979`, and `0314e53c` hardened helper cache keys, linked SILK LPC helpers to the configured archive, made SILK oracle protocols endian-stable, stabilized missing-fixture cross-validation behavior, guarded strict CELT VQ oracle cases against unsupported libopus PVQ table pairs, tightened the libopus ensure script so a cached tree is not considered current unless it has a host/compiler-matched stamp and `.libs/libopus.a`, moved the CELT QEXT VQ oracle onto the configured QEXT archive instead of compiling default-tree sources with QEXT flags, and extended scalar DNN/OSCE helper stamps to include native OS/arch plus compiler path/target/version while clearing inherited `CC`/`LDFLAGS`. Direct `FindOrEnsure*` fixture/tool entry points must validate before returning an existing executable; on Windows CI, libopus bootstrap and Go tests now run under the same MSYS2 shell so the validated helper tree, `opus_demo`, and `opus_compare` stay in one native path/toolchain domain. Failed ensure-script runs now log the root, shell, MSYS2 environment, PATH, and script output tail before falling back to stamp validation. Fallback discovery may use an existing helper only if a parsed v5 native build stamp exactly matches the helper flags, Windows host family, Go arch, compiler target, static archive, `opus_demo`, and `opus_compare`. Tool discovery also searches `GITHUB_WORKSPACE` and the compiled source repo root so package-local test working directories still land on the same pinned helper tree.
- A11 fixture provenance remains open: CI now has strong native libopus build stamps and rejects stale helper flags in `FindOrEnsure*`, and the primary libopus fixture selectors prefer `GOOS_GOARCH` fixture files when present. macOS and Windows CI generate native matrix/loss/packet/variant fixtures from the pinned libopus build before running the fast suite, while the Linux container-generated checked-in fixture set remains `linux_amd64`. Native Windows generation exposed a Windows/amd64 CELT stereo precision drift in `CELT-FB-20ms-stereo-128k`; the precision guard now applies a narrow GOOS/GOARCH floor override for that case instead of weakening all amd64 lanes. Follow-up guardrails split platform fixture read and write paths, require `fixtures-gen-platform` to run against the native Go host, add strict `fixtures-assert-platform` checks for generated native fixture families, and now include CELT opusdec crossval in that native platform target. `tools/gen_opusdec_crossval_fixture.go` decodes generated packets with the pinned `libopus_refdecode_single.c` helper linked against `tmp_check/opus-1.6.1/.libs/libopus.a`, so platform crossval generation no longer silently depends on `PATH` `opusdec`; a host `opusdec` path is available only through an explicit opt-in fallback env. CELT crossval reads also honor `GOPUS_REQUIRE_PLATFORM_FIXTURES=1`, so platform assertion cannot fall back to OS-agnostic amd64/generic fixtures. On CI run `26374043315` for `49acbba3`, macOS and Windows both completed `Generate native libopus fixtures`, including CELT opusdec crossval platform generation, validating the pushed cross-arch fixture path before this local scratch-width checkpoint. Checked-in fixture JSON still lacks deterministic provenance fields (`goos`, `goarch`, host OS/arch, compiler target/version, and libopus build-stamp digest), and long-frame/baseline fixtures are not yet a complete native matrix; do not treat cross-arch generated fixtures as audit-complete until the schema records and validates that data.
- SILK stereo byte parity remains active: `7bd6529f` added a fixture-derived packet-0 `StereoLRToMSWithRates` oracle case and fixed the `silentSideLen` threshold behavior, matching `stereo_LR_to_MS.c` for that routine. Follow-ups removed Go-only inactive-VAD side-channel bit caps, added a C-backed packet-0 wrapper oracle showing TargetRate/MStargetRates/mid `maxBits`/`useCBR`/`condCoding`/`tell` match libopus, asserted the packet-0 wrapper VAD/activity/tilt/SNR checkpoint, and moved frame seed selection to the libopus `Seed = frameCounter++ & 3` cadence. The packet-0 mid-channel `silk_encode_frame_FLP` oracle now reaches the first frame-core divergence and records `nBytesOut=92` versus libopus `94`; use that as the next blocker before deeper stage probes. The `SILK-WB-20ms-stereo-48k` `chirp_sweep_v1` fixture still shows `gapQ=-213.20`, `payloadMismatch=51/51`, and `firstPayloadMismatch=0`. Do not mark A09 done until the full encoder path is byte/range-final clean or the next blocker is documented here.

## Executive Summary

The repo already has the right idea in a few places, especially `celt/scalar_types.go`, where CELT codec-domain aliases map to `float32`. The main parity issue is that many call paths widen those aliases back to `float64`, then carry double precision through public APIs, scratch buffers, CELT band/PVQ/MDCT helpers, hybrid glue, DTX, and parts of SILK FLP. That does not match libopus 1.6.1 float build, where `opus_val16`, `opus_val32`, `opus_val64`, `opus_res`, `celt_sig`, `celt_norm`, `celt_ener`, `celt_glog`, `celt_coef`, and `silk_float` are C `float`.

The second issue is fixed-point fidelity. A large amount of Go code uses bare `int` for fields and intermediate math that corresponds to libopus `opus_int`, `opus_int32`, `opus_uint32`, or fixed macros with deliberate wraparound/saturation. On 64-bit Go, `int` is not the same execution domain as C `int` plus explicit casts. Fixed math must be audited macro by macro.

The repo contains a lot of `float64` and `int` by design in tests, examples, tools, and some C `double` local helpers. The work is not "delete every float64". The work is to make codec state/signal/control paths match the C typedefs and keep `float64` only where the C reference uses `double` or where Go's `math` package is immediately rounded back to the C destination type.

## Canonical Type Map

Use this as the default mapping unless a local libopus reference file proves otherwise.

| libopus type | Go codec type | Notes |
|---|---:|---|
| `opus_int8` | `int8` | Exact width. |
| `opus_uint8` | `uint8` | Exact width. |
| `opus_int16` | `int16` | Exact width. |
| `opus_uint16` | `uint16` | Exact width. |
| `opus_int32` | `int32` | Exact width. |
| `opus_uint32` | `uint32` | Exact width and wrap domain. |
| `opus_int64` | `int64` | Exact width. |
| `opus_uint64` | `uint64` | Exact width. |
| `opus_int` | `int32` for state/arithmetic, `int` only for Go indexes/lengths | `opus_int` is C `int`; on supported libopus targets this is 32-bit, unlike Go `int` on amd64/arm64. |
| `opus_uint` | `uint32` for state/arithmetic, `int` only after bounds conversion | Same reason as `opus_int`. |
| `opus_val16` | `float32` in float build | `tmp_check/opus-1.6.1/celt/arch.h`. |
| `opus_val32` | `float32` in float build | Do not widen persistent state to `float64`. |
| `opus_val64` | `float32` in float build | Important: the name does not mean C double in float build. |
| `opus_res` | `float32` in float build | Public float API input/output is C `float *`. |
| `celt_sig` | `float32` in float build | Existing alias is correct. |
| `celt_norm` | `float32` in float build | Existing alias is correct. |
| `celt_ener` | `float32` in float build | Existing alias is correct. |
| `celt_glog` | `float32` in float build | Existing alias is correct. |
| `celt_coef` | `float32` in float build | Add alias if needed. |
| `silk_float` | `float32` | `tmp_check/opus-1.6.1/silk/typedef.h`. |
| C `double` | `float64`, but only for proven local helpers | See the allowlist below. |

Acceptable `float64` areas:

- Tests, examples, testvector analysis, and offline tooling unless they feed codec state.
- Go calls to `math.*` where Go requires `float64`, if the input is first rounded to the C source type and the result is immediately rounded to the C destination type.
- SILK FLP local kernels where libopus uses C `double` accumulators: `inner_product_FLP.c`, `energy_FLP.c`, `burg_modified_FLP.c`, `schur_FLP.c`, `LPC_inv_pred_gain_FLP.c`, `pitch_analysis_core_FLP.c`, `corrMatrix_FLP.c`, and `warped_autocorrelation_FLP.c`.
- Table/twiddle generation if the generated runtime table type matches libopus storage.

Non-acceptable `float64` areas:

- Public codec float input/output if it is meant to mirror `opus_encode_float` or `opus_decode_float`.
- Persistent encoder/decoder state for `opus_val*`, `opus_res`, `celt_*`, and `silk_float`.
- Signal, MDCT, PVQ, band-shape, energy, postfilter, preemphasis, stereo, PLC, and hybrid PCM scratch buffers that correspond to libopus codec-domain arrays.
- DTX/auto-mode state fields whose C type is `opus_val16` or `opus_val32`.

## Scratch Is In Scope

Scratch is not a carve-out. In this audit, runtime scratch must use the exact type libopus uses for the value stored in it.

Hard rule:

- If libopus would allocate it with `VARDECL(opus_res, ...)`, `VARDECL(opus_val32, ...)`, `VARDECL(celt_norm, ...)`, `VARDECL(celt_sig, ...)`, `VARDECL(celt_glog, ...)`, or `silk_float`, the Go scratch buffer must be `float32` or the matching local alias.
- If libopus would allocate it as `opus_int16`, `opus_int32`, `opus_uint32`, or `opus_int`, the Go scratch buffer must use the matching fixed-width integer domain, except for pure Go slice indexes/lengths.
- If libopus uses C `double` for a local work array, `float64` is allowed only in that specific helper and must cite the C function/file.
- Reusable struct fields, local `make([]float64, ...)` buffers, conversion scratch, temporary output buffers, and "only a cache" buffers all count. Any one of them can change branch decisions, PVQ pulse choices, energy quantization, or range-final parity.
- `ensureFloat64Slice` is transition debt in runtime codec code, and `ensureComplexSlice` must not be reintroduced. Runtime scratch helpers should disappear from runtime paths or be replaced by type-specific helpers such as `ensureSigSlice`, `ensureNormSlice`, `ensureGLogSlice`, `ensureOpusResSlice`, `ensureKissCpxSlice`, and fixed-width integer helpers.

## Enforcement

The repo now has a ratcheting guard for this rule:

```sh
make test-type-parity
```

The guard scans runtime Go files for `float64`, `complex128`, `KissFFT64State`, `ensureFloat64Slice`, and `ensureComplexSlice`, then compares the result with `tools/type_parity_allowlist.tsv`. Current legacy findings are allowed only because they are recorded in that baseline. New findings fail. Removed findings also fail until the baseline is refreshed, so cleanup stays visible in review. As of this checkpoint, local `make test-type-parity` passes with 2390 legacy findings, down from the previous 2509 baseline. CI lint/static-analysis on `49acbba3` also passed the type parity guard, but that CI run predates these local scratch reductions.

Agents must not run `make update-type-parity-baseline` to hide new debt. Refresh the baseline only after migrating runtime code to libopus-width types, or when a remaining `float64` is tied to a specific libopus C `double` helper with a source citation.

## Current Surface Area

These are rough grep counts from non-test Go files on 2026-05-24. They are a burn-down metric, not a proof of incorrectness.

### `float64` Matches by Area

| Area | Count | Files |
|---|---:|---:|
| `celt` | 1557 | 109 |
| `silk` | 269 | 28 |
| `encoder` | 91 | 9 |
| `internal` | 166 | 20 |
| `multistream` | 113 | 10 |
| `plc` | 74 | 3 |
| `hybrid` | 32 | 2 |
| top-level codec files | 16 | 8 |

Examples/tools/testvectors also contain `float64`; those should be lower priority unless they drive runtime behavior.

### Bare `int` Matches by Area

| Area | Count | Files |
|---|---:|---:|
| `celt` | 2172 | 141 |
| `silk` | 1049 | 77 |
| `internal` | 523 | 44 |
| `encoder` | 428 | 16 |
| `multistream` | 330 | 22 |
| `rangecoding` | 102 | 2 |
| `plc` | 93 | 3 |
| `hybrid` | 43 | 2 |

Not all `int` is wrong. Use `int` for slice indexes, lengths, loop counters, and public Go ergonomics only after the codec value is already in the right domain. Use `int32`/`uint32` or local aliases for state and arithmetic that matches C fixed-width fields/macros.

### Highest `float64` Hotspots

| File | Approx matches |
|---|---:|
| `celt/bands_quant.go` | 189 |
| `encoder/encoder.go` | 64 |
| `celt/encode_frame.go` | 57 |
| `celt/mdct_encode.go` | 54 |
| `celt/encoder.go` | 52 |
| `celt/scratch.go` | 52 |
| `encoder/hybrid.go` | 52 |
| `celt/energy_encode.go` | 51 |
| `celt/mdct.go` | 48 |
| `celt/synthesis.go` | 40 |
| `celt/hybrid_encode_helpers.go` | 39 |
| `celt/postfilter.go` | 38 |
| `celt/stereo.go` | 37 |
| `celt/bands.go` | 34 |
| `silk/encoder.go` | 29 |
| `silk/lpc_analysis.go` | 29 |

### Runtime Scratch `float64` Hotspots

These are non-test runtime matches where `scratch` and `float64`/`complex128` appear on the same line. This is the worklist that makes "even scratch" explicit.

| File | Approx matches |
|---|---:|
| `celt/bands_quant.go` | 33 |
| `encoder/hybrid.go` | 26 |
| `silk/encoder.go` | 26 |
| `celt/encode_frame.go` | 24 |
| `celt/scratch.go` | 22 |
| `celt/channel_adapters.go` | 21 |
| `celt/hybrid_encode_helpers.go` | 19 |
| `silk/lpc_analysis.go` | 19 |
| `celt/decoder_types.go` | 15 |
| `celt/energy_encode.go` | 13 |
| `encoder/encoder.go` | 13 |
| `celt/mdct_encode.go` | 12 |
| `celt/recovery_helpers.go` | 11 |
| `celt/dred_conceal.go` | 9 |
| `celt/synthesis.go` | 9 |
| `celt/mdct.go` | 8 |
| `multistream/encoder.go` | 5 |
| `hybrid/decoder.go` | 3 |
| `celt/decoder_qext_state.go` | 3 |
| `celt/preemph.go` | 3 |
| `celt/transient.go` | 3 |

## Runtime Scratch Mismatch Manifest

Every entry here must be migrated or explicitly justified against a C `double` reference. This list is intentionally broader than the high-level lanes so agents can split work without leaving scratch behind.

### Shared CELT Scratch Helpers

- `celt/scratch.go`: `ensureFloat64Slice` should be removed from runtime use. It currently feeds encode, decode, MDCT, PVQ, QEXT, PLC, DRED, synthesis, and channel-adapter paths.
- `celt/scratch.go` no longer has `ensureComplexSlice`, and `imdctScratch` now aliases `imdctScratchF32`. Runtime `complex128` debt remains in `celt/mdct.go`, `celt/mdct_libopus.go`, `celt/kiss_fft.go`, and `internal/osce/lace/features.go`.

### Top-Level and Multistream PCM Scratch

- `encoder.go`: `scratchPCM64 []float64` should become canonical `[]opusRes`/`[]float32`, or a legacy wrapper-only conversion buffer outside codec execution.
- `multistream.go`: `scratchPCM64 []float64` has the same issue.
- `multistream/encoder_helpers.go`: `projectionScratch []float64` and stream projection scratch should match the projection source/destination domain. If the matrix math intentionally uses `float32` PCM plus fixed S16 coefficients, storage must not widen by default.
- `multistream/encoder.go`: `surroundInputScratch`, `surroundBandScratch`, `surroundBandSMR`, `surroundWindowMem`, `surroundPreemphMem`, and `streamSurroundTrim` are runtime analysis state/scratch. Match libopus surround analysis types, not convenience `float64`.
- `multistream/decoder_dred_helpers.go`: `dredPCM64 [][]float64` should follow the canonical DRED/Opus PCM type after A01/A10.

### Unified Encoder Scratch

- `encoder/encoder.go`: `inputBuffer`, `scratchDCPCM`, `scratchInputPCM`, `scratchQuantPCM`, `scratchDelayedPCM`, `scratchTransitionPrefill`, `scratchSilkPrefill`, and `scratchCELTPrefill` now use `[]opusRes`.
- `encoder/encoder.go`: remaining Opus wrapper scratch debt is the still-needed `scratchInputPCM64` bridge into the unmigrated CELT/SILK float64 core. Treat it as wrapper debt, not codec-domain storage to copy into new paths.
- `encoder/encoder.go`: `scratchPCM32` is named as a conversion from `float64`; after the canonical API moves to `float32`, either delete it or repurpose it as a real codec-domain buffer.
- `encoder/dtx.go`: production DTX now has an `[]opusRes` path for energy/peak math. The older `[]float64` helper remains only for legacy tests/transitional callers and should not receive new runtime use.
- `encoder/dred_runtime.go` and `encoder/dred_runtime_default.go`: DRED latent input now takes `[]opusRes`; keep future DRED/Opus wrapper buffers in that domain.

### Hybrid Encoder/Decoder Scratch

- `encoder/hybrid.go`: `prevHBGain` now uses `opusVal16` and `scratchTransitionPCM` now uses `[]opusRes`; gain/stereo fade math operates on `opusRes` instead of casting samples through float64.
- `encoder/hybrid.go`: `scratchBandLogE2`, `scratchAnalysisE`, `scratchPrevEnergy`, `scratchNextEnergy`, `scratchMDCTInput`, `scratchMDCTHist`, `scratchMDCTResult`, `scratchDeintLeft`, and `scratchDeintRight` still must match the CELT/Opus types they store once the CELT MDCT/energy path is migrated.
- `encoder/hybrid.go`: `scratchLookahead32` comment says `float64 -> float32`; remove that conversion path when canonical PCM is `float32`.
- `hybrid/decoder.go`: `scratchOutput []float64` and `upsample3x` output should be `[]opusRes`/`[]float32`.
- `hybrid/hybrid.go`: `decodedFloat64`, `float32ToFloat64`, PLC return buffers, and final decode surfaces should become wrapper-only or canonical `float32`.

### CELT Decoder Scratch

- `celt/decoder_types.go`: `scratchPrevEnergy`, `scratchPrevLogE`, `scratchPrevLogE2`, `scratchEnergies`, `scratchSilenceE`, `scratchSynth`, `scratchSynthR`, `scratchStereo`, `scratchShortCoeffs`, `scratchMonoToStereoR`, `scratchMonoMix`, `postfilterScratch`, `scratchPLC`, `scratchPLCHybridNormL`, and `scratchPLCHybridNormR` should use `celt_glog`, `celt_sig`, `celt_norm`, `opus_val32`, or `opus_res` as appropriate.
- `celt/decoder_qext_state.go`: `scratchEnergies` now uses `celtGLog`; `scratchSpectrumL` and `scratchSpectrumR` still carry transitional denormalized spectrum data as `[]float64`.
- `celt/decoder_dred_state_enabled.go`: `scratchPLCDREDBase` now uses `celtSig`; `scratchPLC` and the surrounding DRED/PLC helper signatures still carry transitional `[]float64`.
- `celt/channel_adapters.go`, `celt/recovery_helpers.go`, `celt/dred_conceal.go`, `celt/silence_helpers.go`, and `celt/state_helpers.go`: all `ensureFloat64Slice` scratch use is runtime decode/PLC scratch and must migrate.

### CELT Band/PVQ/QEXT Scratch

- `celt/scratch.go` `bandDecodeScratch`: `bandVectors`, `bandVectorsL`, `bandVectorsR`, `bandStorage`, `bandStorageL`, `bandStorageR`, `pvqNorm`, `pvqNorm32`, and `foldResult` now use `celtNorm`; `left`, `right`, `norm`, `lowband`, `coeffs`, `hadamardTmp`, and `quantWork` remain transitional `float64` bridges.
- `celt/scratch.go` `bandEncodeScratch`: `thetaX`, `thetaY`, `pvqX`, and selected PVQ scratch now use `celtNorm`; `norm`, `lowbandScratch`, `xSave`, `ySave`, `normSave`, `xResult0`, `yResult0`, `normResult0`, `hadamardTmp`, and `quantWork` are still codec-domain vectors and should not remain `float64`.
- `celt/bands_quant.go`: local `lowbandScratch`, Hadamard scratch, quant/dequant scratch, and stereo theta scratch must use `celt_norm`/`opus_val16`/`opus_val32` semantics.
- `celt/qext_cubic.go`, `celt/qext_decode.go`, `celt/decoder_flow_helpers.go`, and `celt/decoder_hybrid_helpers.go`: QEXT scratch spectra/energies must follow the same CELT aliases.
- `celt/pvq_search.go` and dispatch helpers: PVQ input scratch should use `celt_norm`/`float32`, not `[]float64` plus extraction casts.

### CELT MDCT/Synthesis/Postfilter Scratch

- `celt/mdct.go`: legacy IMDCT scratch now aliases the float32 IMDCT scratch shape, removing local `complex128` work buffers from that path. IMDCT function signatures still accept/return `[]float64`, and the remaining spectrum/overlap boundaries must match `celt_sig`/`celt_norm`/`opus_res`.
- `celt/mdct_encode.go`: `mdctScratch`, `mdctForwardOverlapScratch`, short-block scratch, `mdctBlockCoeffs`, and overlap work buffers should use CELT aliases. The current `mdctForwardOverlapF32Scratch(samples []float64, coeffs []float64, ...)` is transitional and should become fully float32.
- `celt/synthesis.go`: synth scratch `scratchSynth`, `scratchSynthR`, `scratchShortCoeffs`, and stereo output scratch must be `opus_res`/`celt_sig`.
- `celt/postfilter.go`, `celt/preemph.go`, `celt/prefilter.go`, and `celt/prefilter_*`: inner product and postfilter scratch must match libopus float or fixed helper types.
- `celt/window_tables_static.go`: runtime window scratch/table values should be stored as `float32`/alias.

### CELT Transient/Stereo/TF Scratch

- `celt/transient.go`: `toneDetectScratch`, `transientAnalysisScratch`, and `PatchTransientDecisionWithScratch` should not take or mutate `[]float64` unless a specific C `double` reference exists.
- `celt/tf.go`: `TFAnalysisWithScratch` should operate on `celt_norm`/`float32` data.
- `celt/stereo.go` and `celt/stereo_encode.go`: mid/side/interleave/deinterleave scratch should be `opus_res`/`celt_sig`/`celt_norm`.
- `celt/alloc_trim.go` and `celt/spread_decision.go`: energy/norm scratch should use CELT aliases; final public diagnostic helpers may wrap.

### SILK Scratch

- Follow-ups converted the LPC/Burg boundary: removed transitional `scratchLpcBurg` and `scratchLpcXF64`, changed Burg result storage to `[]float32` (`silk_float`), and changed FindLPC input/residual scratch to `[]float32`. They also removed unused float64 pitch-window/autocorrelation/reflection/LPC scratch fields that no longer participate in runtime analysis.
- Current SILK type-width work removes `scratchLtpRes`, returns pitch residual as `[]float32`, makes sparseness analysis consume `[]float32`, stores `lastLPCGain` as `silk_float`, keeps the noise-shaping SNR boundary in `float32`, and stores residual energies/gain-processing inputs as `[]float32` (`silk_float`) instead of widening back through `float64`.
- `silk/encoder.go`: `scratchBurgAf`, `scratchBurgCFirstRow`, `scratchBurgCLastRow`, `scratchBurgCAf`, and `scratchBurgCAb` may remain `float64` only where they directly mirror `burg_modified_FLP.c` C `double` arrays. Add source comments/tests.
- `silk/encoder.go`: FindLPC interpolation NLSF-to-LPC scratch now mirrors `silk_NLSF2A_FLP`: `scratchLpcAQ12` stores the fixed bridge coefficients and `scratchLpcATmp` stores the resulting `silk_float` coefficients as `float32`; the old `scratchNlsfCos`/`scratchNlsfP`/`scratchNlsfQ` float64 polynomial scratch was removed.
- `silk/lpc_analysis.go`: remaining `ensureFloat64Slice` use must be justified function by function. A comment like "analysis buffer as float64" is not enough.
- `silk/ltp_quant.go`, `silk/gain_encode.go`, and `silk/float_cast.go`: verify fixed/float conversion scratch and rounding against libopus source before keeping `float64`.

### Extension Scratch

- `internal/osce/lace/features.go`: `KissFFT64State`, `complex128` FFT input/output, and FFT feature scratch must become float32 unless the extension reference uses double storage.
- `internal/osce/lace/runtime.go`, `internal/osce/bwe/runtime.go`, and `internal/osce/bwe/features.go`: `math.*` calls are fine only as immediate float32 round-trips; runtime scratch tensors remain `float32`.
- `internal/lpcnetplc/analysis.go`: Burg scratch arrays are `float64`; keep only if extension reference uses double, with a citation. Other analysis scratch should remain `float32`.

## Confirmed Mismatches

### P0. Public PCM APIs and Opus encoder buffers are `float64`

Files:

- `encoder.go`
- `encoder/encoder.go`
- `hybrid/hybrid.go`
- `hybrid/decoder.go`
- `celt/celt_encode.go`
- `celt/channel_adapters.go`
- `multistream*.go`
- `pcm.go`

Reference:

- `tmp_check/opus-1.6.1/src/opus_encoder.c`: float API uses `opus_res *`, which is `float` in float build.
- `tmp_check/opus-1.6.1/src/opus_decoder.c`: decode float output uses `opus_res *`, also `float`.
- `tmp_check/opus-1.6.1/celt/celt.h`: CELT encode/decode PCM is `opus_res *`.

Current symptoms:

- Top-level `Encoder` has `scratchPCM64 []float64`.
- `encoder/Encoder.Encode`, `EncodeWithAnalysis`, `EncodeWithAnalysisMaxBytes`, prefill, delay-buffer, DC reject, DRED, and mode-transition paths pass `[]float64`.
- Hybrid decode returns `[]float64` even though much of the sub-decoder work is already `[]float32`.

Fix direction:

- Make canonical float API and internal PCM paths `[]float32`.
- Keep legacy `[]float64` wrappers only if explicitly desired, and name them as conversion wrappers so agents do not treat them as codec-domain APIs.
- Move delay buffer, DC reject, prefill, DRED frame PCM, hybrid transition PCM, and quantized input scratch to `[]opusRes`/`[]float32`.
- Ensure public float32 input is not widened before DTX, analysis, CELT, SILK, hybrid, or DRED.

Verification:

- Add a compile-time or runtime test that all canonical encode/decode float APIs operate on `[]float32`.
- Add parity tests that compare packets/range-final before and after conversion on existing fixtures.
- Add a grep gate that fails if canonical runtime API files introduce new `[]float64` buffers.

### P0. CELT signal path still carries `float64`

Files:

- `celt/bands_quant.go`
- `celt/bands.go`
- `celt/bands_encode.go`
- `celt/encode_frame.go`
- `celt/energy.go`
- `celt/energy_encode.go`
- `celt/folding.go`
- `celt/mdct.go`
- `celt/mdct_encode.go`
- `celt/postfilter.go`
- `celt/preemph.go`
- `celt/pvq.go`
- `celt/pvq_search.go`
- `celt/qext_cubic.go`
- `celt/quant_bands.go`
- `celt/scratch.go`
- `celt/stereo.go`
- `celt/stereo_encode.go`
- `celt/synthesis.go`
- `celt/tf.go`
- `celt/transient.go`
- `celt/window_tables_static.go`

Reference:

- In libopus float build, CELT signal, norm, energy, log-energy, coefficient, and residual types are all C `float`.
- Existing Go aliases in `celt/scalar_types.go` are correct: `celtNorm`, `celtSig`, `celtEner`, `celtGLog`, `opusVal16`, `opusVal32`, and `opusRes` are `float32`.

Current symptoms:

- The aliases exist but bridge helpers convert to/from `float64`.
- Many CELT functions expose `[]float64` shapes, MDCT coefficients, energies, stereo samples, transient inputs, and scratch buffers.
- Assembly dispatch and legacy helpers are still typed around `float64`.
- Window tables are stored as `[N]float64`, while libopus runtime storage and operations are float.

Fix direction:

- Convert CELT runtime vectors to aliases: `[]celtSig`, `[]celtNorm`, `[]celtGLog`, `[]opusVal16`, `[]opusVal32`, and `[]opusRes`.
- Change function signatures first at package boundaries, then burn inward through scratch structs.
- Do not "float64 then cast at end"; carry `float32` through every operation where C carries `float`.
- For Go `math.*` equivalents of C float math, explicitly round inputs and outputs to `float32` at the same points the C code stores to `float`.
- Replace `window_tables_static.go` arrays with `float32`/alias tables or generated constants matching C float values.

Verification:

- Add CELT packet/range-final parity tests for mono, stereo, transient, non-transient, short-block, PLC, and hybrid-start-band paths.
- Add targeted tests for PVQ band resynthesis and normalized vectors against libopus traces.
- Add a grep gate for runtime `celt` package: no `[]float64`, `[N]float64`, `complex128`, or `KissFFT64State` outside explicitly named legacy wrappers/tests.

### P0. CELT FFT/MDCT has a parallel 64-bit implementation

Files:

- `celt/kiss_fft.go`
- `celt/kissfft32.go`
- `celt/mdct.go`
- `celt/mdct_libopus.go`
- `internal/osce/lace/features.go`

Reference:

- libopus float build uses `kiss_fft_scalar` as float for runtime FFT data.
- C may use double while generating twiddles, but stored/runtime FFT values are float in the float build.

Current symptoms:

- `KissFFT64State` uses `float64`/`complex128`.
- `kissfft32.go` already has `kissCpx` and `kissFFTState` using `float32`.
- `internal/osce/lace/features.go` calls `celt.GetKissFFT64State` and allocates `complex128` FFT input/output.

Fix direction:

- Delete or quarantine the 64-bit FFT implementation after migrating callers.
- Make OSCE/LACE feature extraction use the float32 KISS path or a dedicated float32 FFT.
- Make MDCT operate on alias slices and ensure twiddle/window tables store float32 values.

Verification:

- Add FFT impulse/sinusoid tests comparing bins against libopus float traces.
- Add MDCT/IMDCT round-trip and range-final parity tests after the migration.
- Add a grep gate for `KissFFT64State` and `complex128` in runtime codec packages.

### P0. Opus encoder state has float64 where libopus uses `opus_val*`

Files:

- `encoder/auto_mode.go`
- `encoder/dtx.go`
- `encoder/hybrid.go`
- `encoder/encoder.go`

Reference:

- `StereoWidthState` fields are `opus_val32 XX, XY, YY`, `opus_val16 smoothed_width`, and `opus_val16 max_follower`.
- `OpusEncoder.prev_HB_gain` is `opus_val16`.
- `OpusEncoder.peak_signal_energy` is `opus_val32`.
- `OpusEncoder.delay_buffer` is `opus_res`.

Current symptoms:

- `StereoWidthMem` is now `opusVal32`/`opusVal16`; keep future edits in that domain.
- `dtxState.peakSignalEnergy`, production frame energy, and production DTX input now use `opusVal32`/`opusRes`; keep the legacy `[]float64` helper out of new runtime paths.
- `HybridState.prevHBGain` is `opusVal16`, and hybrid gain/stereo fade scratch now stays in `opusRes`.
- `encoder/encoder.go` now carries DC-rejected input, original input scratch, LSB-quantized input, delay compensation, transition prefill, SILK prefill, CELT prefill, and the packet input queue as `[]opusRes`.
- `celtCVBRBoundScale` and `celtSurroundTrim` are `float64`; verify reference type before converting.

Fix direction:

- Keep `StereoWidthMem` and DTX peak/energy math in `opusVal32`/`opusVal16`; do not regress these fields back to `float64`.
- Keep high-band gain state/fades, delay compensation, transition prefill, DRED latent input, and Opus-VAD subframe input in `opusVal16`/`opusRes`; only use `float64` inside `math.*` calls if immediately rounded.
- Remove the temporary bridge into CELT/SILK when those cores accept libopus-width input directly, then delete or quarantine the remaining wrapper-only `scratchInputPCM64` bridge.

Verification:

- Add tests for `compute_stereo_width`, DTX pseudo-SNR gating, high-band gain fade, and delay compensation against libopus traces.
- Include threshold-side cases where float64 vs float32 can flip a branch.

### P1. SILK FLP state/control uses blanket `float64` scratch

Files:

- `silk/encoder.go`
- `silk/pitch_residual.go`
- `silk/lpc_analysis.go`
- `silk/pred_coefs.go`
- `silk/noise_shape_analysis.go`
- `silk/ltp_encode.go`
- `silk/gain_encode.go`
- `silk/vad.go`
- `silk/stereo_lp_filter.go`

Reference:

- `silk_float` is C `float`.
- `silk_encoder_state_FLP.x_buf`, `LTPCorr`, shape smoother fields, and `silk_encoder_control_FLP` arrays are `silk_float`.
- Some SILK FLP local analysis kernels deliberately use C `double` accumulators.

Current symptoms:

- `silk/encoder.go` still contains some `[]float64` scratch buffers and state-like fields that need per-reference classification.
- `pitch_residual.go` now returns `[]float32` residuals, but stale float64 LPC/autocorrelation helpers remain and should be deleted or fenced from runtime reuse.
- Some helper files correctly use `float32` inputs with `float64` accumulators, but the boundary between C `silk_float` and C `double` is not documented per function.

Fix direction:

- Split every SILK FLP buffer into one of two categories:
  - C `silk_float` storage/control/scratch: `float32`.
  - C `double` local accumulator/work array in a specific reference function: `float64`.
- Keep `float64` only in functions that cite the exact C file/function using `double`.
- Ensure any value assigned back to C `silk_float` state/control is rounded to `float32` at that assignment point.
- Convert `x_buf`, `LTPCorr`, Gains, PredCoef, LTPCoef, AR, LF, Tilt, HarmShapeGain, Lambda, input/coding quality, predGain, LTPredCodGain, and ResNrg equivalents to `float32`.

Verification:

- Add SILK FLP trace tests for LPC analysis, pitch residual, LTP quant, gains, NLSF, noise shaping, and stereo LP.
- Add comments or small doc map in `silk` listing each remaining `float64` helper and its C `double` source.

### P1. Fixed-point math helpers need macro-level parity

Files:

- `silk/libopus_fixed.go`
- `silk/float_cast.go`
- `silk/ltp_quant.go`
- `silk/resample_libopus.go`
- `silk/nsq*.go`
- `silk/silk.go`
- `celt/fixed*.go` if present/added
- `encoder/vad.go`
- `rangecoding/*.go`

Reference:

- CELT fixed macros in `tmp_check/opus-1.6.1/celt/fixed_generic.h`.
- SILK fixed macros in `tmp_check/opus-1.6.1/silk/macros.h`.

Critical macro semantics:

- `ADD32_ovflw`, `SUB32_ovflw`, and `SHL32` intentionally use `uint32` wrap and cast back.
- `PSHR32` adds the half-LSB before arithmetic right shift.
- `MULT16_32_Q15`, `MULT32_32_Q31`, `MULT32_32_P31`, and `MULT32_32_Q32` depend on exact operand casts and shift order.
- `silk_SMULWB`, `silk_SMLAWB`, `silk_SMULWW`, and `silk_SMLAWW` must truncate/cast exactly as the C macros do.
- `silk_ADD_SAT32` and `silk_SUB_SAT32` must saturate based on 32-bit signed overflow, not Go `int` overflow.

Fix direction:

- Introduce or enforce fixed helper functions whose signatures use `int16`, `int32`, `uint32`, and `int64` exactly.
- Avoid bare `int` in fixed arithmetic helpers.
- When C casts to unsigned before add/sub/shift, mirror with `uint32`.
- When C casts a product to 64-bit before shift, mirror with `int64`.
- When C relies on signed 32-bit truncation, cast through `int32` at the same point.

Verification:

- Add oracle tests for each macro with edge vectors: min/max, negative rounding halves, overflow wrap, saturation boundaries, and randomized fuzz against compiled libopus helper shims.
- Run these tests under amd64 and arm64 if possible because assembly and Go integer assumptions may diverge.

### P1. SILK common structs have width mismatches

Files:

- `silk/libopus_types.go`
- related SILK codebook tables

Reference:

- `silk_NLSF_CB_struct` first four fields are `const opus_int16`.
- `stereo_enc_state` contains `mid_side_amp_Q0[4] opus_int32`, `smth_width_Q14`, `width_prev_Q14`, `silent_side_len` as `opus_int16`, and per-frame `predIx`/`mid_only_flags` arrays as `opus_int8`.

Current symptoms:

- Go `nlsfCB.nVectors`, `order`, `quantStepSizeQ16`, and `invQuantStepSizeQ6` are `int`, but should be `int16` unless proven safer at the table boundary.
- Many decoder state fields use bare `int` where the C field is `opus_int`; decide whether the field is true state/arithmetic (`int32`) or just Go indexing (`int`).
- `stereoEncState` stores LBRR metadata as Go-specific fields, not the exact C layout. This may be OK behaviorally, but needs a parity note and trace coverage.

Fix direction:

- Convert C-width struct fields to fixed-width Go types.
- Keep helper indexes as `int` only at local loop/slice boundaries.
- For tables, convert once at initialization if Go needs an index, rather than storing state as `int`.

Verification:

- Add table/type tests for NLSF codebooks and stereo encoder side info.
- Add range-final tests for stereo SILK LBRR and mid-only transitions.

### P1. Tonality analysis is mostly float32, but extra state needs review

Files:

- `encoder/analysis.go`

Reference:

- `TonalityAnalysisState` uses `opus_val32`/`float` arrays, `float` feature fields, and integer counters.
- `AnalysisInfo` uses integer flags/counters, float probabilities/features, and `unsigned char leak_boost[19]`.

Current symptoms:

- Most Go fields are already `float32`, which is good.
- `SqrtE [NbFrames][NbTBands]float32` appears to be derived scratch/state not present in libopus `TonalityAnalysisState`.
- Several `math.*` calls are cast back to `float32`; review branch points and assignment points against C.

Fix direction:

- Keep analysis state in `float32`.
- Verify `SqrtE` does not change reset behavior, serialized trace shape, or cross-frame state compared with libopus. If it is only derived scratch, move it to scratch or document it as non-reference cache.
- Add explicit rounding at C assignment points.

Verification:

- Trace `AnalysisInfo` and `TonalityAnalysisState` over multiple frames, including silence, music-like, speech-like, and bandwidth transitions.

### P1. OSCE/DRED/DNN/LPCNet extension paths mix float32 and float64

Files:

- `internal/osce/lace/features.go`
- `internal/osce/lace/runtime.go`
- `internal/osce/bwe/features.go`
- `internal/osce/bwe/runtime.go`
- `internal/lpcnetplc/analysis.go`
- `internal/lpcnetplc/predictor.go`
- `encoder/dred_runtime.go`

Current symptoms:

- Many runtime tensors are `float32`, but feature extraction and helper math still use `float64`, especially LACE FFT features via `KissFFT64State`/`complex128`.
- LPCNet analysis contains real `float64` Burg arrays; this may or may not match the extension reference, but it needs a source citation.
- DRED runtime takes `[]float64` PCM from the main encoder path.

Fix direction:

- Make extension PCM and FFT feature inputs `float32`.
- Keep `float64` only where the corresponding libopus extension source uses `double`.
- Convert DRED entry points after the main encoder PCM lane moves to `[]float32`.

Verification:

- Add extension feature trace tests where fixtures exist.
- Add grep gates for `complex128` and `KissFFT64State`.

### P2. Public enums and control values use compressed Go types

Files:

- `types/types.go`
- top-level controls files

Reference:

- Public libopus controls and enums are C `int`.

Current symptoms:

- `types.Mode` and `types.Bandwidth` use `uint8`; this is compact and works for TOC-like values, but it does not mirror libopus public control type width.
- `types.Signal` uses Go `int` and constants with libopus values.

Fix direction:

- Decide if these types are public-Go ergonomic wrappers or exact libopus control mirrors.
- If exact parity is the priority, use an `int32` or `opusInt`-style type for controls and convert to small types only when packing TOC bits.

Verification:

- Add control round-trip tests around invalid values, forced mode/bandwidth, and CTL compatibility.

## Multi-Agent Work Lanes

| Lane | Status | Scope | Goal | Suggested first files |
|---|---|---|---|---|
| A00 | Open | Type policy and gates | Add shared aliases/docs and CI grep gates with explicit allowlists. | `celt/scalar_types.go`, new report/test helper |
| A01 | Partial | Public PCM API | Make canonical encode/decode float API `[]float32`; isolate optional `float64` wrappers. | `encoder.go`, `encoder/encoder.go`, `hybrid/*.go`, `pcm.go`, `multistream*.go` |
| A02 | Partial | Opus encoder state | Width, DTX peak/energy/input, Opus-VAD input, DRED latent input, hybrid HB gain, delay compensation, transition prefill, DC reject, quantized PCM, `inputBuffer`, and hybrid gain/stereo fade scratch are now libopus-width; the CELT/SILK bridge is next. | `encoder/hybrid.go`, `encoder/encoder.go` |
| A03 | Active | CELT core vectors | Convert runtime signal/norm/energy/band/PVQ vectors from `float64` to aliases. | `celt/bands_quant.go`, `celt/bands.go`, `celt/pvq*.go`, `celt/energy*.go` |
| A04 | Active | CELT MDCT/synthesis/postfilter | Convert MDCT, synthesis, preemphasis, postfilter, windows, and scratch to `float32` aliases. | `celt/mdct*.go`, `celt/synthesis.go`, `celt/preemph.go`, `celt/postfilter.go`, `celt/window_tables_static.go` |
| A05 | Active | CELT FFT | Remove runtime dependence on `KissFFT64State`/`complex128`. | `celt/kiss_fft.go`, `celt/kissfft32.go`, `internal/osce/lace/features.go` |
| A06 | Open | CELT transient/stereo/TF | Convert transient, tone, stereo, TF, spread, and alloc helper math to float32 with correct rounding. | `celt/transient.go`, `celt/stereo*.go`, `celt/tf.go`, `celt/spread_decision.go`, `celt/alloc_trim.go` |
| A07 | Active | SILK FLP storage | Split `silk_float` storage from true C `double` local helpers. | `silk/encoder.go`, `silk/pitch_residual.go`, `silk/lpc_analysis.go`, `silk/pred_coefs.go` |
| A08 | Active | Fixed math | Build exact fixed helper tests and replace mismatched `int`/overflow arithmetic. | `silk/libopus_fixed.go`, `silk/float_cast.go`, `silk/ltp_quant.go`, `silk/nsq*.go`, `encoder/vad.go` |
| A09 | Active | SILK structs/tables | Convert state/table fields to exact widths and document stereo side-info layout deviations. | `silk/libopus_types.go`, NLSF table files, stereo files |
| A10 | Partial | Extensions | Convert OSCE/DRED/LPCNet codec-domain float64/complex128 to float32 unless source uses C double. | `internal/osce`, `internal/lpcnetplc`, `encoder/dred_runtime.go` |
| A11 | Active | Oracle/build tests | Add C shim/oracle traces for type sizes, fixed macros, build provenance, and threshold-sensitive branches. | `tmp_check`, `tools`, package tests |
| A12 | Open | Assembly cleanup | Retire or rewrite float64 assembly paths after their Go callers move to float32. | `celt/*_asm.go`, `celt/amd64_dispatch.go`, `celt/*float64*` |
| A13 | Active | Runtime scratch enforcement | Sweep every remaining runtime `scratch` + `float64`/`complex128` match and either migrate it or cite the exact C `double` source. | `celt/scratch.go`, `celt/decoder_types.go`, `encoder/encoder.go`, `encoder/hybrid.go`, `silk/encoder.go`, `multistream/encoder.go` |

Suggested coordination rule: one agent takes one lane and updates this table plus any lane-specific notes. Each lane owns scratch buffers in the files it touches. A13 is the final cross-lane validator for scratch that falls between package boundaries.

## Suggested Burn-Down Commands

Run these before and after each lane:

```sh
rg -n "float64|complex128|KissFFT64State" --glob '*.go' --glob '!*_test.go' celt silk encoder hybrid internal/osce internal/lpcnetplc
rg -n "(?i)scratch[^\n]*(float64|complex128)|(?:float64|complex128)[^\n]*(?i:scratch)" --glob '*.go' --glob '!*_test.go' celt silk encoder hybrid internal/osce internal/lpcnetplc plc multistream *.go
rg -n "ensureFloat64Slice|ensureComplexSlice|make\(\[\]float64|\[[^]]*\]float64|complex128|KissFFT64State" --glob '*.go' --glob '!*_test.go' celt silk encoder hybrid internal/osce internal/lpcnetplc plc multistream *.go
rg -n "\bint\b" --glob '*.go' --glob '!*_test.go' celt silk encoder hybrid rangecoding
rg --count-matches "float64" --glob '*.go' --glob '!*_test.go' | awk -F: '{split($1,a,"/"); c[a[1]]+=$2; f[a[1]]++} END {for (d in c) print d, c[d] " matches", f[d] " files"}' | sort -k2,2nr
rg --count-matches "\bint\b" --glob '*.go' --glob '!*_test.go' | awk -F: '{split($1,a,"/"); c[a[1]]+=$2; f[a[1]]++} END {for (d in c) print d, c[d] " matches", f[d] " files"}' | sort -k2,2nr
```

Add allowlists only after reading the matching libopus source. Do not hide a mismatch with an allowlist just because changing it is large.

## Definition of Done

A lane is done only when all of the following are true:

- Runtime codec state and signal/control buffers use the same scalar width as libopus.
- Runtime scratch buffers, local temporary slices, reusable scratch fields, and scratch helper functions use the same scalar width as libopus.
- Any remaining `float64` in touched runtime code is tied to a specific C `double` reference or an immediate Go `math.*` round-trip.
- Any remaining bare `int` in touched runtime state is either a true Go index/length or documented as a deliberate public-Go API type.
- Fixed-point helpers have edge-case tests for wrap, saturation, rounding, and signed shifts.
- Existing parity/range-final tests pass, or the report is updated with a precise blocker.
- The lane owner updates the work-lane table status and notes any new mismatches discovered.

## Immediate Priority Order

1. A01 and A02 first: public/internal PCM and Opus encoder state determine the types that flow everywhere else.
2. A03/A04/A05 next: CELT has the largest `float64` surface and many downstream packages depend on it.
3. A07/A08/A09 in parallel: SILK has a real mix of `silk_float` and C `double`, so it needs careful per-function classification.
4. A10 after A01/A05: extension paths should inherit the canonical PCM/FFT types.
5. A13 runs after every package lane and before completion: no runtime scratch mismatch can be left as "later".
6. A11 continuously: every lane should add a small oracle rather than waiting for one giant parity test pass.
