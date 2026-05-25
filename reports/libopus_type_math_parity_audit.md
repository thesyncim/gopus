# libopus Type and Math Parity Audit

Date: 2026-05-25

Reference source: `tmp_check/opus-1.6.1`

Target: exact libopus parity for scalar widths, persistent state, signal buffers, temporary scratch buffers, and fixed-point arithmetic. Public Go interfaces may break. Treat this as a coordination file for multiple agents: claim one lane, make the local change, add/adjust parity tests, then mark the lane status.

## Current Goal Integration Notes

Status as of 2026-05-25:

- A01 public PCM API is partial: the top-level `Encode([]float32, ...)` path now enters `encoder.Encoder` through a float32 entry point and stays in `opusRes` through Opus-level input processing instead of building a public-input `[]float64` bridge. The standalone hybrid decoder plus internal encoder, multistream decode, and low-level multistream encode canonical methods now use `[]float32` without persistent `float64` compatibility bridges. The deeper Opus/SILK/CELT core, root compatibility surfaces, and the CELT encoder bridge still carry transitional `float64` PCM buffers, so A01 is not done.
- A02 Opus encoder state is partial but substantially reduced: `StereoWidthMem` now mirrors libopus `StereoWidthState` with `opusVal32`/`opusVal16`, DTX `peakSignalEnergy` is `opusVal32`, hybrid HB gain state is `opusVal16`, hybrid stereo widths are `opus_int16`-width, frame-energy and digital-silence threshold math run in the libopus float domain, DRED/Opus-VAD activity checks no longer widen peak/energy comparisons, and the stereo-width NaN guard now uses an explicit `opusVal32` bit test instead of Go's `float64` math path. CELT-facing delay compensation, mode-transition prefill, SILK transition prefill, hybrid transition redundancy/gain-fade scratch, DC reject scratch, LSB-quantized input scratch, the Opus wrapper input queue, DRED latent input, Opus-VAD subframe input, and internal encoder public encode entry points now use `opusRes`/`opusVal*` or `float32` storage. The remaining A02 debt is the broader internal `[]float64` PCM bridge into the unmigrated CELT core, especially `scratchInputPCM64`.
- A03 CELT core vectors are active and oracle-backed: `aa373dcd` fixes strict CELT VQ oracle cases, and follow-ups move CELT coarse/fine/final energy residual scratch to `[]celtGLog` with no-clear preservation for shared residual state. QEXT encoder- and decoder-side old-band-energy/residual scratch now use `celtGLog`, DRED retained baseline scratch uses `celtSig`, decode-side band/PVQ shape storage, folding scratch, decoded PVQ norm scratch, and silence old-band-energy scratch use CELT aliases, legacy IMDCT scratch routes through the float32 IMDCT path instead of local `complex128` work buffers, dead decoder IMDCT scratch plumbing is gone, periodic PLC FIR now consumes the existing `celtSig` excitation scratch directly instead of copying through `scratchPLCExc32`, alloc-trim's runtime/detail path now uses `celtNorm`/`celtGLog`/`opusVal*` scratch and gates the tonality adjustment on `analysis.valid`, matching `celt_encoder.c:alloc_trim_analysis`, CELT encoder prefilter scratch now stores `prefilterPre`/`prefilterOut` as `celtSig` while postfilter/prefilter gains stay in `float32` width through the comb-filter dispatch, and encoder-side oldBandE snapshots for transient patching, dynalloc, encoded silence, surround dynalloc masks, the hybrid CELT prev/next-energy handoff, `bandLogE2`, analysis-energy snapshots, spread weights, and dynalloc inputs now use `celtGLog`/`float32` scratch instead of reusable `float64` buffers. Dead encoder dynalloc follower/noise/importance scratch was removed. Quant-all-bands norm/lowband state, lowband preparation, lowband-out folding state, zero-pulse lowband input, special hybrid folding state, theta RDO norm save/result buffers, and decoder PLC hybrid noise norm/conceal-energy scratch now use `celtNorm`/`celtGLog`; coefficient vectors and coefficient Hadamard/quant work remain transitional `[]float64` bridges. Encoder/decoder energy accessors now expose `float32`, and a dead double-domain `combFilterWithInput` helper was removed. Broad runtime vector and scratch migration remains open. The strict CELT allocation probe still reports `85/401` allocation mismatches for `CELT-FB-2.5ms-stereo-128k/chirp_sweep_v1`, first at frame 87 (`AllocTrim` 6 vs 7, with TF/spread/offsets matching and `CodedBands`/`Intensity` diverging at 16 vs 15), so the remaining byte-parity bug is concentrated in alloc-trim inputs/upstream analysis rather than dynalloc/TF coding.
- A04/A05 MDCT/FFT is now partially active: `imdctScratch` aliases the float32 scratch shape and the legacy overlap/in-place IMDCT helpers route through the float32 IMDCT path, removing their reusable `complex128` scratch allocation. Short-block per-block MDCT coefficient scratch now uses `[]float32` before widening at the existing legacy output boundary. OSCE LACE feature extraction now shares the float32 KISS FFT path used by BWE (`complex64` plus `celt.KissCpx` scratch), so the remaining FFT debt is concentrated in CELT's legacy `KissFFT64State`, `complex128` twiddle/runtime helpers, `mdctTwiddleSet`, and `mdct_libopus.go`.
- A13 multistream surround scratch is active: surround band SMR, preemphasis/window/input scratch, MDCT analysis, and masking scratch now use float-build storage. The surround mask combiner now mirrors `src/opus_multistream_encoder.c:logSum()` with the float table approximation, and the channel-count offset uses `celt_log2()`-style float32 math instead of Go `math.Log2` double widening. Projection decode float32/int16 demixing no longer round-trips through float64, and `Decode`/`DecodeToFloat32` now stay in float-build storage even for projection-family output and PLC. Multistream DRED PLC scratch now returns `[]float32` directly and no longer keeps a `dredPCM64` side buffer. CVBR burst-bound scaling now stays in float-build storage through multistream policy and Opus/CELT encoder state. The low-level multistream encoder API now accepts `[]float32`, removing the last runtime multistream `float64` allowlist entries.
- A07 SILK FLP storage is active and oracle-backed: `e04a55db` links the SILK LPC oracle to the configured libopus archive and `ffa3a0d3` makes that oracle protocol endian-stable. Follow-ups split LPC/Burg boundaries so persistent `silk_float` storage and FindLPC residual/input scratch use `float32`, while true `burg_modified_FLP.c` C `double` work arrays stay `float64`; the current type-width pass also removes the widened pitch residual copy, feeds pitch analysis/LTP/noise-shaping from `[]float32`, computes sparseness with `energyF32Libopus`, keeps residual-energy/gain-processing scratch in `silk_float` storage, replaces the FindLPC interpolation NLSF-to-LPC float64 polynomial scratch with the libopus-style `silk_NLSF2A_FLP` fixed bridge plus `silk_float` output storage, moves NSQ noise-shaping Q controls (`LambdaQ10`, `HarmShapeGainQ14`, `TiltQ14`, `LTPScaleQ14`) and Opus/SILK VAD Q8/Q15 bridge state from Go `int` to `int32`, keeps SILK pulse input in `[]int8` through `encodePulses`, moves encode-side and decode-side pulse/shell `opus_int` scratch to `int32`, removes dead decode-side shell scratch fields, narrows PLC glue shift state and `silk_sum_sqr_shift` output to `int32` for libopus `opus_int`, removes the legacy SILK stereo double-domain predictor helpers, keeps SILK VAD/activity helper math in `silk_float`/`float32`, and moves the local LTP coefficient solve/quantize/periodicity helpers plus decoder LTP synthesis off `float64`. Recent passes delete stale top-level double-domain LPC/Burg/A2NLSF/window/energy helpers, keep the top-level Burg helper and A2NLSF bridge on `float32`, narrow the Burg energy cache (`lastTotalEnergy`, `lastInvGain`) to `silk_float` storage, remove the encoder warping Q16 setup's double widening, move pitch-analysis thresholds and legacy pitch helper APIs to `silk_float`, delete dead double-domain pitch residual FLP wrappers, replace the legacy LSF encode Chebyshev/root-search scratch with the libopus-style fixed `silk_A2NLSF` bridge, and remove the dead non-libopus `computeLPCFromFrame` Hamming-window helper. Remaining SILK scratch debt is now led by inner-product C-double helper classification and source-cited C-double kernels.
- A08 fixed math is partial: `8f525312` added C-backed oracle coverage for SILK fixed wrapper primitives and corrected the reversed-bound `silk_LIMIT` behavior for `silkLimitInt`/`silkLimit32`; follow-ups check both `silkLShiftSAT32` and `silk_LSHIFT_SAT32` against the C oracle, fix the `shift == 31` saturation edge, and add a C-backed NSQ delayed-decision error-path oracle for the composed `ADD_SAT32`/`ADD32_ovflw`/`SUB_SAT32`/`RSHIFT_ROUND` arithmetic.
- A10 extension validation has support work landed: `4c9a5188` made generated DNN model blob headers explicit little-endian through a shared C writer. CELT DRED's retained neural crossfade baseline now uses `celtSig`, OSCE LACE FFT feature scratch is now float32, OSCE/BWE output-scale float-to-int16 quantizers now share a float32 nearest-even helper for the libopus `[-32767,32767]` clamp, and CELT deep-PLC int16 bridges use the same float32 raw conversion domain. The A10 runtime conversion goal remains open until the remaining OSCE runtime math round-trips, DRED, and LPCNet codec-domain `float64`/`complex128` paths are migrated or source-cited.
- A11 oracle/build infrastructure is active: `32f37ba4`, `e04a55db`, `ffa3a0d3`, `458dd69b`, `4c9a5188`, `aa373dcd`, `24960979`, and `0314e53c` hardened helper cache keys, linked SILK LPC helpers to the configured archive, made SILK oracle protocols endian-stable, stabilized missing-fixture cross-validation behavior, guarded strict CELT VQ oracle cases against unsupported libopus PVQ table pairs, tightened the libopus ensure script so a cached tree is not considered current unless it has a host/compiler-matched stamp and `.libs/libopus.a`, moved the CELT QEXT VQ oracle onto the configured QEXT archive instead of compiling default-tree sources with QEXT flags, and extended scalar DNN/OSCE helper stamps to include native OS/arch plus compiler path/target/version while clearing inherited `CC`/`LDFLAGS`. Direct `FindOrEnsure*` fixture/tool entry points must validate before returning an existing executable; on Windows CI, libopus bootstrap and Go tests now run under the same MSYS2 shell so the validated helper tree, `opus_demo`, and `opus_compare` stay in one native path/toolchain domain. Failed ensure-script runs now log the root, shell, MSYS2 environment, PATH, and script output tail before falling back to stamp validation. Fallback discovery may use an existing helper only if a parsed v5 native build stamp exactly matches the helper flags, Windows host family, Go arch, compiler target, static archive, `opus_demo`, and `opus_compare`. Tool discovery also searches `GITHUB_WORKSPACE` and the compiled source repo root so package-local test working directories still land on the same pinned helper tree.
- A11 fixture provenance is now schema-backed for the core generated fixtures: CI has strong native libopus build stamps and rejects stale helper flags in `FindOrEnsure*`, the primary libopus fixture selectors prefer `GOOS_GOARCH` fixture files when present, and checked-in decoder matrix/loss plus encoder packet/variant fixtures now carry a `provenance` object that records target `goos`/`goarch`, libopus version, and QEXT mode. Newly generated native fixtures can also include host/compiler/build-stamp digest fields from `.gopus-libopus-build`, but the currently checked-in `linux_amd64` fixtures still do not all prove compiler target/stamp provenance, and no checked-in `celt/testdata/opusdec_crossval_fixture_linux_amd64.json` exists yet. Fixture loaders validate the provenance block before use, and `TestGeneratedLibopusFixturesCarryProvenance` guards the checked-in headers. macOS, Windows, push-only Linux arm64, and the Linux amd64 exhaustive Docker lane compile pinned libopus natively, generate native matrix/loss/packet/variant/crossval fixtures, require platform fixture selection, and now run bounded decoder fixture parity plus encoder fixture honesty smokes against those freshly generated outputs before their fast suite or job completion; the Linux amd64 Docker lane also checks the tracked generated fixture files stay clean after regeneration. The encoder smoke intentionally validates fixture generation against the same native `opus_demo`; strict gopus-vs-libopus encoder quality floors remain in the regular Linux parity/safety lanes until platform-specific encoder gaps are fixed. The broader CELT allocation parity fixture remains in the dedicated Linux parity gate/report until the known allocation drift is fixed; hosted Windows/arm64 native fixtures currently exceed the Linux-tuned `22%` CELT allocation mismatch ceiling. Native Windows generation exposed a Windows/amd64 CELT stereo precision drift in `CELT-FB-20ms-stereo-128k`; the precision guard now applies a narrow GOOS/GOARCH floor override for that case instead of weakening all amd64 lanes. Follow-up guardrails split platform fixture read and write paths, require `fixtures-gen-platform` to run against the native Go host, add strict `fixtures-assert-platform` checks for generated native fixture families, and now include CELT opusdec crossval in that native platform target. `tools/gen_opusdec_crossval_fixture.go` decodes generated packets with the pinned `libopus_refdecode_single.c` helper linked against `tmp_check/opus-1.6.1/.libs/libopus.a`, so platform crossval generation no longer silently depends on `PATH` `opusdec`; a host `opusdec` path is available only through an explicit opt-in fallback env. CELT crossval reads also honor `GOPUS_REQUIRE_PLATFORM_FIXTURES=1`, so platform assertion cannot fall back to OS-agnostic amd64/generic fixtures. Long-frame/baseline fixtures are not yet a complete native matrix; do not treat cross-arch generated fixtures as audit-complete until those families also record and validate provenance.
- SILK stereo byte parity remains active: `7bd6529f` added a fixture-derived packet-0 `StereoLRToMSWithRates` oracle case and fixed the `silentSideLen` threshold behavior, matching `stereo_LR_to_MS.c` for that routine. Follow-ups removed Go-only inactive-VAD side-channel bit caps, added a C-backed packet-0 wrapper oracle showing TargetRate/MStargetRates/mid `maxBits`/`useCBR`/`condCoding`/side-info checkpoints match libopus, asserted the packet-0 wrapper VAD/activity/tilt/SNR checkpoint, and moved frame seed selection to the libopus `Seed = frameCounter++ & 3` cadence. The packet-0 mid-channel `silk_encode_frame_FLP` oracle is now range-final clean: the test fixture uses the encoder-mode SILK resampler path (`silk_resampler_init(..., forEnc=1)` equivalent), side-info setup encodes the mid-only flag when the side VAD is inactive, `CODE_INDEPENDENTLY_NO_LTP_SCALING` no longer delta-codes the first gain index, and the trace oracle verifies pitch/gain checkpoints, final indices, pulse hashes/block sums, side-info range states, and post-pulse tell/range against libopus. The full-packet blocker after that was Go-only stereo channel budgeting: the mid frame was kept in CBR unless `!midOnly`, and the side frame was capped to remaining packet bits. Matching `silk/enc_API.c` by switching the mid frame to non-CBR whenever `MStargetRates_bps[1] > 0`, reserving `maxBits/(tot_blocks*2)` for the side target, and letting the side channel keep the frame maxBits makes all local `SILK-WB-20ms-stereo-48k` variant rows byte-clean (`gapQ=0.00`, `payloadMismatch=0/51`, `firstPayloadMismatch=-1`). Do not mark A09 done until the remaining SILK stereo profiles and platform fixture runs are byte/range-final clean or the next blocker is documented here.

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

The guard scans runtime Go files for `float64`, `complex128`, `KissFFT64State`, `ensureFloat64Slice`, and `ensureComplexSlice`, then compares the result with `tools/type_parity_allowlist.tsv`. Current legacy findings are allowed only because they are recorded in that baseline. New findings fail. Removed findings also fail until the baseline is refreshed, so cleanup stays visible in review. As of this checkpoint, local `make test-type-parity` passes with 1819 legacy findings, down from 1835 in the previous checkpoint, 1887 before that, 1921 before that, 1955 before that, 1995 before that, 2010 before that, 2014 before that, 2017 before that, 2025 before that, 2029 before that, 2034 before that, 2068 before that, 2077 before that, 2081 before that, 2083 before that, 2096 before that, 2125 before that, 2144 before that, 2197 before that, 2198 before that, 2207 before that, 2209 before that, 2217 before that, 2220 before that, 2222 before that, 2250 before that, 2273 before that, 2285 before that, 2327 before that, and 2509 in the older baseline.

Agents must not run `make update-type-parity-baseline` to hide new debt. Refresh the baseline only after migrating runtime code to libopus-width types, or when a remaining `float64` is tied to a specific libopus C `double` helper with a source citation.

## Current Surface Area

These are burn-down metrics from non-test runtime Go files on 2026-05-25, not a proof of incorrectness. The `float64` table is the weighted type-parity allowlist surface that `make test-type-parity` ratchets; the `int` table is a separate `rg` audit surface and is not currently enforced by that guard.

### Guarded Float/Complex Findings by Area

| Area | Count | Files |
|---|---:|---:|
| `celt` | 1515 | 107 |
| `silk` | 99 | 19 |
| `internal` | 74 | 12 |
| `encoder` | 66 | 8 |
| `plc` | 61 | 3 |
| top-level codec files | 2 | 2 |
| `hybrid` | 2 | 2 |

Examples/tools/testvectors also contain `float64`; those should be lower priority unless they drive runtime behavior.

### Bare `int` Matches by Area

| Area | Count | Files |
|---|---:|---:|
| `celt` | 2176 | 141 |
| `silk` | 981 | 77 |
| top-level codec files | 482 | 45 |
| `encoder` | 425 | 16 |
| `multistream` | 325 | 22 |
| internal non-OSCE/LPCNet packages | 301 | 29 |
| `internal/osce` + `internal/lpcnetplc` | 222 | 15 |
| `rangecoding` | 102 | 2 |
| `plc` | 93 | 3 |
| `hybrid` | 37 | 2 |
| `container` | 32 | 5 |
| `types` | 1 | 1 |
| `util` | 1 | 1 |

Not all `int` is wrong. Use `int` for slice indexes, lengths, loop counters, and public Go ergonomics only after the codec value is already in the right domain. Use `int32`/`uint32` or local aliases for state and arithmetic that matches C fixed-width fields/macros.

### Highest Guarded Float/Complex Hotspots

| File | Approx matches |
|---|---:|
| `celt/bands_quant.go` | 211 |
| `celt/encode_frame.go` | 94 |
| `celt/mdct.go` | 80 |
| `celt/encoder.go` | 74 |
| `celt/energy_encode.go` | 65 |
| `celt/mdct_encode.go` | 59 |
| `plc/celt_plc.go` | 52 |
| `celt/postfilter.go` | 49 |
| `celt/preemph.go` | 44 |
| `celt/output_helpers.go` | 41 |
| `internal/lpcnetplc/analysis.go` | 40 |
| `celt/channel_adapters.go` | 40 |
| `celt/hybrid_encode_helpers.go` | 38 |
| `celt/tonality.go` | 38 |
| `celt/synthesis.go` | 33 |
| `celt/stereo.go` | 32 |
| `celt/scratch.go` | 31 |
| `encoder/hybrid.go` | 30 |
| `celt/bands_encode.go` | 28 |
| `celt/bands.go` | 27 |
| `celt/transient.go` | 24 |
| `celt/pvq.go` | 24 |
| `celt/quant_bands.go` | 22 |
| `celt/recovery_helpers.go` | 21 |

### Runtime Scratch Guarded Hotspots

These are non-test runtime matches where `scratch` and `float64`/`complex128` appear on the same line. This is the worklist that makes "even scratch" explicit.

| File | Approx matches |
|---|---:|
| `celt/bands_quant.go` | 25 |
| `celt/channel_adapters.go` | 20 |
| `encoder/hybrid.go` | 16 |
| `celt/hybrid_encode_helpers.go` | 16 |
| `celt/encode_frame.go` | 16 |
| `celt/mdct_encode.go` | 10 |
| `celt/decoder_types.go` | 10 |
| `celt/synthesis.go` | 9 |
| `celt/scratch.go` | 9 |
| `celt/dred_conceal.go` | 9 |
| `celt/mdct.go` | 6 |
| `celt/energy_encode.go` | 6 |
| `silk/lpc_analysis.go` | 5 |
| `silk/encoder.go` | 5 |
| `internal/lpcnetplc/analysis.go` | 3 |
| `celt/decoder_flow_helpers.go` | 3 |
| `celt/decoder_hybrid_helpers.go` | 3 |
| `celt/recovery_helpers.go` | 2 |

## Runtime Scratch Mismatch Manifest

Every entry here must be migrated or explicitly justified against a C `double` reference. This list is intentionally broader than the high-level lanes so agents can split work without leaving scratch behind.

### Shared CELT Scratch Helpers

- `celt/scratch.go`: `ensureFloat64Slice` should be removed from runtime use. It currently feeds encode, decode, MDCT, PVQ, QEXT, PLC, DRED, synthesis, and channel-adapter paths.
- `celt/scratch.go` no longer has `ensureComplexSlice`, and `imdctScratch` now aliases `imdctScratchF32`. Runtime `complex128` debt remains in `celt/mdct.go`, `celt/mdct_libopus.go`, and `celt/kiss_fft.go`; OSCE LACE no longer calls the 64-bit KISS FFT path.

### Top-Level and Multistream PCM Scratch

- `encoder.go`: the top-level `Encoder` no longer owns `scratchPCM64`; int16/int24 conversion scratch now stays in `[]float32` before entering the internal float32 encode path.
- `multistream.go`: `scratchPCM64` is gone. Public multistream float32 input now calls the internal multistream float32 encode path, and int16/int24 conversion scratch stays in `[]float32`.
- `multistream/encoder.go`: low-level `Encode` and `EncodeWithAnalysis` now accept `[]float32`, `EncodeFloat32` is just the compatibility synonym, and the legacy package-boundary conversion scratch field is gone.
- `multistream/encoder_helpers.go`: encoder-side projection mixing no longer stores a projected `[]float64` frame. It routes matrix output through `float32` per-stream buffers before the child encoder float32 entry point.
- `multistream/encoder.go`: surround analysis now keeps `streamSurroundTrim`, `surroundInputScratch`, `surroundBandScratch`, `surroundBandSMR`, `surroundWindowMem`, and `surroundPreemphMem` in libopus-width `float32`. It uses float32-facing CELT MDCT and band-energy helpers, mirrors `src/opus_multistream_encoder.c:logSum()` with the float table approximation, and computes the channel offset through `celt_log2()`-style float32 math.
- `multistream/projection_matrix.go`: projection float/int16 demixing for the float32 decode path now consumes `[]float32` and uses float32 matrix accumulation, matching libopus float output storage before final int16 quantization.
- `multistream/decoder_dred_helpers.go`: `dredPCM64 [][]float64` is gone. DRED PLC concealment now returns float-build `[]float32` scratch, and the canonical multistream `Decode` path no longer widens it for compatibility.
- `decoder_osce_bwe_crossfade.go`, `internal/osce/bwe/runtime.go`, and `multistream/decoder_osce_apply.go`: OSCE output-scale quantization now uses shared float32 nearest-even rounding with the libopus OSCE negative clamp, so these paths no longer widen `tmp` through Go `float64`.

### Unified Encoder Scratch

- `encoder/encoder.go`: `inputBuffer`, `scratchDCPCM`, `scratchInputPCM`, `scratchQuantPCM`, `scratchDelayedPCM`, `scratchTransitionPrefill`, `scratchSilkPrefill`, and `scratchCELTPrefill` now use `[]opusRes`.
- `encoder/encoder.go`: `Encode`, `EncodeWithAnalysis`, and `EncodeWithAnalysisMaxBytes` now accept `[]float32`, copy public float input directly into `scratchInputPCM []opusRes`, and run analysis from `[]float32`; the public/internal float32 entry points no longer populate a reusable `[]float64` input/analysis bridge. Remaining Opus wrapper scratch debt is the still-needed `scratchInputPCM64` bridge into the unmigrated CELT core. Treat it as wrapper debt, not codec-domain storage to copy into new paths.
- `encoder.go`: `scratchPCM32` in the top-level wrapper is now real `float32` conversion scratch for integer input; do not widen those samples before the internal float32 encode entry point.
- `encoder/dtx.go`: DTX energy/peak math and tests now use the `[]opusRes` path; the legacy runtime `[]float64` helper was removed.
- `encoder/dred_runtime.go` and `encoder/dred_runtime_default.go`: DRED latent input now takes `[]opusRes`; keep future DRED/Opus wrapper buffers in that domain.

### Hybrid Encoder/Decoder Scratch

- `encoder/hybrid.go`: `prevHBGain` now uses `opusVal16` and `scratchTransitionPCM` now uses `[]opusRes`; gain/stereo fade math operates on `opusRes` instead of casting samples through float64.
- `encoder/hybrid.go`: `scratchPrevEnergy`, `scratchNextEnergy`, `scratchBandLogE2`, and `scratchAnalysisE` now keep CELT oldBandE/log-energy handoffs in `float32`; `scratchMDCTInput`, `scratchMDCTHist`, `scratchMDCTResult`, `scratchDeintLeft`, and `scratchDeintRight` still must match the CELT/Opus types they store once the CELT MDCT path is migrated.
- `encoder/hybrid.go`: `scratchLookahead32` comment says `float64 -> float32`; remove that conversion path when canonical PCM is `float32`.
- `hybrid/decoder.go`: `scratchOutput []float64` is gone, `upsample3x` uses `[]float32`, and `decodeFrame`/`decodeFrameWithHook` now return `[]float32`.
- `hybrid/hybrid.go`: `decodedFloat64` and `float32ToFloat64` are gone; the canonical standalone hybrid decode surfaces now return `[]float32`. Remaining hybrid float64 debt is the CELT PLC bridge (`celtConcealed []float64`) until the CELT PLC path itself is migrated.

### CELT Decoder Scratch

- `celt/decoder_types.go`: `scratchSilenceE`, `scratchPLCHybridNormL`, and `scratchPLCHybridNormR` now use CELT aliases; `scratchPrevEnergy`, `scratchEnergies`, `scratchSynth`, `scratchSynthR`, `scratchStereo`, `scratchShortCoeffs`, `scratchMonoToStereoR`, `scratchMonoMix`, `postfilterScratch`, and `scratchPLC` still should use `celt_glog`, `celt_sig`, `opus_val32`, or `opus_res` as appropriate. Dead `scratchPrevLogE`/`scratchPrevLogE2`, `scratchIMDCT`, and `scratchPLCExc32` fields were removed.
- `celt/decoder_qext_state.go`: `scratchEnergies` now uses `celtGLog`; `scratchSpectrumL` and `scratchSpectrumR` still carry transitional denormalized spectrum data as `[]float64`.
- `celt/decoder_dred_state_enabled.go`: `scratchPLCDREDBase` now uses `celtSig`; `scratchPLC` and the surrounding DRED/PLC helper signatures still carry transitional `[]float64`.
- `celt/recovery_helpers.go`: periodic PLC now reuses `scratchPLCExc []celtSig` directly for FIR input instead of maintaining a duplicate float32 excitation conversion buffer. Hybrid/noise PLC random norm coefficients and conceal-energy scratch now use `celtNorm`/`celtGLog`; remaining recovery-helper float64 debt is the legacy PCM/output bridge, periodic autocorr math, and overlap/history helpers that still need a float-build path.

### CELT Band/PVQ/QEXT Scratch

- `celt/scratch.go` `bandDecodeScratch`: `bandVectors`, `bandVectorsL`, `bandVectorsR`, `bandStorage`, `bandStorageL`, `bandStorageR`, `pvqNorm`, `pvqNorm32`, `foldResult`, all-bands `norm`, all-bands `lowband`, and `hadamardTmpNorm` now use `celtNorm`; `left`, `right`, `coeffs`, coefficient `hadamardTmp`, and `quantWork` remain transitional `float64` bridges.
- `celt/scratch.go` `bandEncodeScratch`: all-bands `norm`, `lowbandScratch`, `normSave`, `normResult0`, `thetaX`, `thetaY`, `pvqX`, `hadamardTmpNorm`, and selected PVQ scratch now use `celtNorm`; `xSave`, `ySave`, `xResult0`, `yResult0`, coefficient `hadamardTmp`, and `quantWork` remain transitional coefficient-vector bridges.
- `celt/bands_quant.go`: local all-bands norm/lowband/lowbandOut, lowband preparation, zero-pulse lowband input, special hybrid folding state, and theta RDO norm snapshots now use `celtNorm`. Remaining local Hadamard/quant work for coefficient vectors and theta RDO coefficient snapshots still need migration with the surrounding coefficient path.
- `celt/qext_cubic.go`, `celt/qext_decode.go`, `celt/decoder_flow_helpers.go`, and `celt/decoder_hybrid_helpers.go`: QEXT scratch spectra/energies must follow the same CELT aliases.
- `celt/pvq_search.go` and dispatch helpers: PVQ input scratch should use `celt_norm`/`float32`, not `[]float64` plus extraction casts.

### CELT MDCT/Synthesis/Postfilter Scratch

- `celt/mdct.go`: legacy IMDCT scratch now aliases the float32 IMDCT scratch shape, removing local `complex128` work buffers from that path; unused decoder IMDCT/FFT output scratch has been deleted. IMDCT function signatures still accept/return `[]float64`, and the remaining spectrum/overlap boundaries must match `celt_sig`/`celt_norm`/`opus_res`.
- `celt/mdct_encode.go`: `mdctForwardOverlapF32Scratch` is now generic over float32/float64 callers, `MDCTForwardWithOverlapFloat32` keeps surround-analysis MDCT coefficients in float-build storage, and short-block per-block coefficient scratch now uses `[]float32`. Remaining `mdctScratch`, overlap work buffers, and public float64 boundaries should continue moving to CELT aliases.
- `celt/synthesis.go`: synth scratch `scratchSynth`, `scratchSynthR`, `scratchShortCoeffs`, and stereo output scratch must be `opus_res`/`celt_sig`.
- `celt/prefilter.go`: encoder prefilter `prefilterPre`/`prefilterOut` scratch now uses `celtSig`, cancellation-energy sums use `opusVal32`, and prefilter/postfilter gain parameters stay `float32` through the active comb-filter paths. Remaining postfilter/preemphasis/interleave scratch still crosses legacy `[]float64` synthesis boundaries and must continue moving to `celtSig`/`opus_res`.
- `celt/postfilter.go`, `celt/preemph.go`, and `celt/prefilter_*`: inner product and postfilter scratch must match libopus float or fixed helper types.
- `celt/window_tables_static.go`: runtime window scratch/table values should be stored as `float32`/alias.

### CELT Transient/Stereo/TF Scratch

- `celt/transient.go`: `toneDetectScratch`, `transientAnalysisScratch`, and `PatchTransientDecisionWithScratch` should not take or mutate `[]float64` unless a specific C `double` reference exists.
- `celt/tf.go`: `TFAnalysisWithScratch` should operate on `celt_norm`/`float32` data.
- `celt/stereo.go` and `celt/stereo_encode.go`: mid/side/interleave/deinterleave scratch should be `opus_res`/`celt_sig`/`celt_norm`.
- `celt/alloc_trim.go`: runtime/detail paths use CELT aliases and the libopus `analysis.valid` tonality gate; public compatibility wrappers still accept legacy `float64` inputs. `celt/spread_decision.go` energy/norm scratch should use CELT aliases.

### SILK Scratch

- Follow-ups converted the LPC/Burg boundary: removed transitional `scratchLpcBurg` and `scratchLpcXF64`, changed Burg result storage to `[]float32` (`silk_float`), and changed FindLPC input/residual scratch to `[]float32`. They also removed unused float64 pitch-window/autocorrelation/reflection/LPC scratch fields that no longer participate in runtime analysis.
- Current SILK type-width work removes `scratchLtpRes`, returns pitch residual as `[]float32`, makes sparseness analysis consume `[]float32`, stores `lastLPCGain` as `silk_float`, keeps the noise-shaping SNR boundary in `float32`, and stores residual energies/gain-processing inputs as `[]float32` (`silk_float`) instead of widening back through `float64`.
- SILK stereo analysis cleanup removed the unused double-domain `stereoFindPredictorFloatWithRatio`, keeps `stereoFindPredictorFloat` accumulators in `float32`, and keeps the legacy `encodeStereo` predictor solve in the float-build width. The libopus-aligned stereo entry point remains the fixed `StereoLRToMSWithRates` path.
- `silk/vad.go` no longer carries frame activity, spectral tilt, or pitch-periodicity analysis through `float64`; those helper returns and accumulators now stay in `float32` and use the existing float32 sqrt helper.
- `silk/ltp_encode.go` and `silk/ltp.go` no longer carry local LTP coefficient solve, codebook distance, periodicity, or decoder LTP synthesis through `float64`; those codec-domain values now stay in `float32`.
- `silk/ltp_quant.go` no longer widens the `silk_float2int(XX[i] * 131072.0f)` Q17 bridge through `float64`; it uses float32 round-to-even. The two remaining `float64` lines in that file are the rolling accumulator in `silk_corrMatrix_FLP`, which is explicitly C `double` in `silk/float/corrMatrix_FLP.c`.
- `silk/gain_encode.go` no longer widens gain scratch/control estimates through `float64`; PCM subframe energy, residual-energy scaling, and RMS conversion now stay in `silk_float`/`float32`, with the existing Burg C-double stats rounded at the gain-control boundary.
- `silk/noise_shape.go` no longer widens LF shaping Q14 pack rounding through `float64`; `LF_MA`/`LF_AR` now use the float32 round-to-even helper that mirrors `silk_float2int` on `silk_float` inputs.
- `silk/pred_coefs.go` now keeps min inverse gain and the FindLPC min-gain boundary as `silk_float`/`float32`, uses float32 round-to-even for LPC Q12/Q16 fixed bridges, and routes final gain square-rooting through the float32 helper. `silk/noise_shape_analysis.go` also uses the same float32 helper for AR Q13 fixed-bridge rounding and shaping RMS square-rooting.
- `silk/float_cast.go` no longer keeps a runtime `float64ToInt16Round` bridge; the oracle tests now exercise the shared `opusmath.Float32ToInt16Raw` helper directly.
- `silk/lpc_analysis.go` and `silk/encoder.go`: stale top-level double-domain LPC/Burg/A2NLSF/window/energy helpers were deleted or migrated to `float32`; `lastTotalEnergy` and `lastInvGain` are now `silk_float` storage; encoder warping Q16 setup no longer widens through `float64`. `scratchBurgAf`, `scratchBurgCFirstRow`, `scratchBurgCLastRow`, `scratchBurgCAf`, and `scratchBurgCAb` remain `float64` only where they directly mirror `burg_modified_FLP.c` C `double` arrays, with source comments on the struct and core helper.
- `silk/pitch_detect.go` and `silk/encode_frame.go`: pitch-analysis thresholds now stay `silk_float`/`float32`, the backward-compatible autocorrelation/interpolation helpers no longer widen to `float64`, and remaining `float64` use is limited to source-cited `pitch_analysis_core_FLP.c` C `double` accumulators plus `silk_energy_FLP`/`silk_inner_product_FLP`/`silk_log2` helper behavior.
- `silk/pitch_residual.go`: dead double-domain `autocorrelationFLP`, `schurFLP`, `k2aFLP`, and `bwexpanderFLP` wrappers were deleted. The active Schur helper still uses a C-double work array only to mirror `silk/float/schur_FLP.c`.
- `silk/lsf_encode.go`: the old float64 Chebyshev/root-search scratch was removed. `lpcToLSFEncodeInto` now converts Q12 LPC coefficients to Q16 and calls the fixed `silk_A2NLSF` bridge, matching `silk/float/wrappers_FLP.c:silk_A2NLSF_FLP`.
- `silk/encoder.go`: FindLPC interpolation NLSF-to-LPC scratch now mirrors `silk_NLSF2A_FLP`: `scratchLpcAQ12` stores the fixed bridge coefficients and `scratchLpcATmp` stores the resulting `silk_float` coefficients as `float32`; the old `scratchNlsfCos`/`scratchNlsfP`/`scratchNlsfQ` float64 polynomial scratch was removed.
- `silk/libopus_decode.go` and `silk/decoder.go`: decode-side pulse block sums, LSB-shift counts, and shell split pulse-count arithmetic now use `int32` for libopus `opus_int` arithmetic, while decoded pulse storage remains `[]int16`; dead decoder-owned shell scratch arrays were removed.
- `silk/plc_glue.go`, `silk/libopus_types.go`, and `silk/stereo_lp_filter.go`: PLC glue `conc_energy_shift` and the shared `silkSumSqrShift` shift result now use `int32`, matching libopus `opus_int` in `silk/structs.h` and `silk/sum_sqr_shift.c`; casts are limited to Go shift-helper call sites.
- `silk/resample_sinc.go`: the dead custom non-libopus sinc resampler was deleted. The active SILK resampler parity work remains the encoder-mode libopus resampler path referenced by the SILK stereo byte-parity notes.
- `silk/lpc_analysis.go`: the dead non-libopus `computeLPCFromFrame` Hamming-window helper and its window scratch were removed. Remaining `ensureFloat64Slice` use is isolated to `burgModifiedFLPZeroAllocF32`, where it mirrors libopus `silk/float/burg_modified_FLP.c` C `double` work arrays.
- `silk/float_cast.go`: remaining runtime `float64` is the C-double helper surface used by Burg/LPC code; do not add new float/int conversion bridges here.

### Extension Scratch

- `internal/osce/lace/features.go`: LACE feature FFT scratch now uses `complex64` plus `celt.KissCpx` and `celt.KissFFT32ToWithScratch`, matching libopus float-build runtime storage.
- `internal/osce/lace/runtime.go`, `internal/osce/bwe/runtime.go`, and `internal/osce/bwe/features.go`: `math.*` calls are fine only as immediate float32 round-trips; runtime scratch tensors remain `float32`. BWE/OSCE int16 output conversion now stays in float32 and uses `opusmath.Float32ToInt16OSCEOutputScale` for the libopus OSCE clamp.
- `internal/lpcnetplc/analysis.go`: Burg scratch arrays are `float64`; keep only if extension reference uses double, with a citation. Other analysis scratch should remain `float32`.

## Confirmed Mismatches

### P0. Remaining PCM Bridges and Opus/CELT Encoder Buffers

Files:

- `encoder.go`
- `encoder/encoder.go`
- `celt/celt_encode.go`
- `celt/channel_adapters.go`
- `multistream*.go`
- `pcm.go`

Reference:

- `tmp_check/opus-1.6.1/src/opus_encoder.c`: float API uses `opus_res *`, which is `float` in float build.
- `tmp_check/opus-1.6.1/src/opus_decoder.c`: decode float output uses `opus_res *`, also `float`.
- `tmp_check/opus-1.6.1/celt/celt.h`: CELT encode/decode PCM is `opus_res *`.

Current symptoms:

- Top-level `Encoder` and `MultistreamEncoder` no longer have `scratchPCM64`.
- `encoder/Encoder.Encode`, `EncodeWithAnalysis`, and `EncodeWithAnalysisMaxBytes` now accept `[]float32`; wrapper-only conversion scratch such as `scratchInputPCM64` remains until the CELT core boundary is migrated.
- Standalone hybrid decode now returns `[]float32`; the remaining hybrid `float64` debt is the CELT PLC bridge and compatibility surfaces.

Fix direction:

- Make canonical float API and internal PCM paths `[]float32`.
- Keep legacy `[]float64` wrappers only if explicitly desired, and name them as conversion wrappers so agents do not treat them as codec-domain APIs.
- Keep already-migrated delay buffer, DC reject, prefill, DRED frame PCM, hybrid transition PCM, and quantized input scratch in `[]opusRes`/`[]float32`, and remove the remaining compatibility bridges as core package boundaries move.
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
- Replace `celt/window_tables_static.go` arrays with `float32`/alias tables or generated constants matching C float values.

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

- `KissFFT64State` still uses `float64`/`complex128` inside the legacy CELT FFT file.
- `kissfft32.go` already has `kissCpx` and `kissFFTState` using `float32`.
- `internal/osce/lace/features.go` has been migrated to the float32 KISS FFT path; keep future OSCE feature scratch on that path.

Fix direction:

- Delete or quarantine the 64-bit FFT implementation after migrating callers.
- Keep OSCE/LACE feature extraction on the float32 KISS path and avoid reintroducing the legacy 64-bit FFT there.
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
- `dtxState.peakSignalEnergy`, production frame energy, DTX tests, and production DTX input now use `opusVal32`/`opusRes`; do not reintroduce a runtime `[]float64` DTX helper.
- `HybridState.prevHBGain` is `opusVal16`, and hybrid gain/stereo fade scratch now stays in `opusRes`.
- `encoder/encoder.go` now carries DC-rejected input, original input scratch, LSB-quantized input, delay compensation, transition prefill, SILK prefill, CELT prefill, and the packet input queue as `[]opusRes`.
- `celtSurroundTrim` now stays in `opusVal32`/`celtGLog` storage through the wrapper and CELT encoder. `celtCVBRBoundScale` and CELT `constrainedVBRBoundScale` now stay in `opusVal16`, and multistream's CVBR bound-scale helper returns `float32`; do not reintroduce double-domain CVBR rate control.

Fix direction:

- Keep `StereoWidthMem` and DTX peak/energy math in `opusVal32`/`opusVal16`; do not regress these fields back to `float64`.
- Keep high-band gain state/fades, delay compensation, transition prefill, DRED latent input, and Opus-VAD subframe input in `opusVal16`/`opusRes`; only use `float64` inside `math.*` calls if immediately rounded.
- Remove the temporary bridge into CELT when that core accepts libopus-width input directly, then delete or quarantine the remaining wrapper-only `scratchInputPCM64` bridge.

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
- `silk/gain_encode.go`

Reference:

- `silk_float` is C `float`.
- `silk_encoder_state_FLP.x_buf`, `LTPCorr`, shape smoother fields, and `silk_encoder_control_FLP` arrays are `silk_float`.
- Some SILK FLP local analysis kernels deliberately use C `double` accumulators.

Current symptoms:

- `silk/encoder.go` still contains some `[]float64` scratch buffers and state-like fields that need per-reference classification.
- `silk/pitch_detect.go` now has only source-cited C-double helper/accumulator findings; do not reintroduce `float64` threshold or legacy pitch-helper APIs.
- `silk/stereo_lp_filter.go` is no longer a runtime `float64` source; keep future stereo predictor work on the fixed/int16 `stereo_LR_to_MS.c` path or `silk_float`/`float32` analysis only.
- `silk/vad.go` is no longer a runtime `float64` source; keep activity and pitch-periodicity scratch in `silk_float`/`float32`.
- `silk/ltp_encode.go` and `silk/ltp.go` are no longer runtime `float64` sources; keep LTP coefficient/control math in `silk_float`/`float32` unless a specific libopus C `double` helper is cited.
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

- Many runtime tensors are `float32`, but extension feature extraction and helper math still have `float64` debt outside the migrated OSCE LACE FFT/output-scale paths.
- LPCNet analysis contains real `float64` Burg arrays; this may or may not match the extension reference, but it needs a source citation.
- The main DRED encoder runtime now takes `[]opusRes` frame PCM; legacy public DRED conversion helpers such as `ConvertTo16kMonoFloat64` remain compatibility bridges.

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
| A01 | Partial | Public PCM API | Make canonical encode/decode float API `[]float32`; isolate optional `float64` wrappers. Hybrid standalone plus internal encoder, multistream decode, and low-level multistream encode are now float32; root/CELT/SILK compatibility surfaces remain. | `encoder.go`, `pcm.go`, `celt`, `silk` |
| A02 | Partial | Opus encoder state | Width, DTX peak/energy/input, Opus-VAD input, DRED latent input, hybrid HB gain, delay compensation, transition prefill, DC reject, quantized PCM, `inputBuffer`, and hybrid gain/stereo fade scratch are now libopus-width; the CELT bridge is next. | `encoder/hybrid.go`, `encoder/encoder.go` |
| A03 | Active | CELT core vectors | Convert runtime signal/norm/energy/band/PVQ vectors from `float64` to aliases. | `celt/bands_quant.go`, `celt/bands.go`, `celt/pvq*.go`, `celt/energy*.go` |
| A04 | Active | CELT MDCT/synthesis/postfilter | Convert MDCT, synthesis, preemphasis, postfilter, windows, and scratch to `float32` aliases. | `celt/mdct*.go`, `celt/synthesis.go`, `celt/preemph.go`, `celt/postfilter.go`, `celt/window_tables_static.go` |
| A05 | Active | CELT FFT | Remove runtime dependence on `KissFFT64State`/`complex128`; OSCE LACE now uses the float32 KISS FFT path. | `celt/kiss_fft.go`, `celt/kissfft32.go`, `celt/mdct*.go` |
| A06 | Active | CELT transient/stereo/TF | Convert transient, tone, stereo, TF, spread, and alloc helper math to float32 with correct rounding; alloc-trim runtime/detail paths now use aliases and `analysis.valid` gating. | `celt/transient.go`, `celt/stereo*.go`, `celt/tf.go`, `celt/spread_decision.go`, `celt/alloc_trim.go` |
| A07 | Active | SILK FLP storage | Split `silk_float` storage from true C `double` local helpers. | `silk/encoder.go`, `silk/pitch_residual.go`, `silk/lpc_analysis.go`, `silk/pred_coefs.go` |
| A08 | Active | Fixed math | Build exact fixed helper tests and replace mismatched `int`/overflow arithmetic. | `silk/libopus_fixed.go`, `silk/float_cast.go`, `silk/ltp_quant.go`, `silk/nsq*.go`, `encoder/vad.go` |
| A09 | Active | SILK structs/tables | Convert state/table fields to exact widths and document stereo side-info layout deviations. | `silk/libopus_types.go`, NLSF table files, stereo files |
| A10 | Partial | Extensions | Convert OSCE/DRED/LPCNet codec-domain float64/complex128 to float32 unless source uses C double; OSCE/BWE int16 output bridges now use the shared float32 OSCE output-scale converter. | `internal/osce`, `internal/lpcnetplc`, `encoder/dred_runtime.go` |
| A11 | Active | Oracle/build tests | Add C shim/oracle traces for type sizes, fixed macros, build provenance, and threshold-sensitive branches; scheduled release evidence now retains the full bundle logs. | `tmp_check`, `tools`, package tests |
| A12 | Open | Assembly cleanup | Retire or rewrite float64 assembly paths after their Go callers move to float32. | `celt/*_asm.go`, `celt/amd64_dispatch.go`, `celt/*float64*` |
| A13 | Active | Runtime scratch enforcement | Sweep every remaining runtime `scratch` + `float64`/`complex128` match and either migrate it or cite the exact C `double` source. | `celt/scratch.go`, `celt/decoder_types.go`, `encoder/encoder.go`, `encoder/hybrid.go`, `silk/encoder.go` |

Suggested coordination rule: one agent takes one lane and updates this table plus any lane-specific notes. Each lane owns scratch buffers in the files it touches. A13 is the final cross-lane validator for scratch that falls between package boundaries.

## Suggested Burn-Down Commands

Run these before and after each lane:

```sh
rg -n "float64|complex128|KissFFT64State" --glob '*.go' --glob '!*_test.go' celt silk encoder hybrid internal/osce internal/lpcnetplc
rg -n "(?i)scratch[^\n]*(float64|complex128)|(?:float64|complex128)[^\n]*(?i:scratch)" --glob '*.go' --glob '!*_test.go' --glob '!testvectors/**' --glob '!tools/**' .
rg -n "ensureFloat64Slice|ensureComplexSlice|make\(\[\]float64|\[[^]]*\]float64|complex128|KissFFT64State" --glob '*.go' --glob '!*_test.go' --glob '!testvectors/**' --glob '!tools/**' .
rg -n "\bint\b" --glob '*.go' --glob '!*_test.go' celt silk encoder hybrid rangecoding
rg --count-matches "float64" --glob '*.go' --glob '!*_test.go' | awk -F: '{split($1,a,"/"); c[a[1]]+=$2; f[a[1]]++} END {for (d in c) print d, c[d] " matches", f[d] " files"}' | sort -k2,2nr
rg --count-matches "\bint\b" --glob '*.go' --glob '!*_test.go' | awk -F: '{split($1,a,"/"); c[a[1]]+=$2; f[a[1]]++} END {for (d in c) print d, c[d] " matches", f[d] " files"}' | sort -k2,2nr
```

Add allowlists only after reading the matching libopus source. Do not hide a mismatch with an allowlist just because changing it is large.

## Definition of Done

Current CI blocker follow-up: the scheduled `Verify Production Exhaustive` run on 2026-05-25 failed in release evidence for the package test suite, production exhaustive gate, and assembly safety matrix on run `26391166380`. That run uploaded only the summary markdown, so the detailed per-command logs were discarded; the workflow upload path is now set to retain `reports/release/**` before the next scheduled diagnosis. Local reproduction fixed the visible blockers: type-width QEXT multistream expectations, the fuzz harness repeat-expansion bound, the cross-arch SILK pitch helper/oracle build, and the delay-buffer opus-res size parser. The manual `Verify Safety` rerun on 2026-05-25 reached assembly safety and exposed a duration-based `FuzzSilkAssemblyKernelsMatchReference` deadline flake on Go 1.25, so the assembly safety smoke now uses a deterministic execution-count fuzz budget. The Go 1.26 safety lane then reached `GOAMD64=v3` parity and failed the full encoder quality-floor summary, so assembly safety now keeps final-range, decoder parity, and encoder fixture-shape guards instead of running the broader known-gap quality floor. Local `make verify-production-exhaustive` and `make test-assembly-safety` passed on darwin/arm64 before the final upstream encoder-API rebase; after that rebase, local `make test-type-parity`, the QEXT parity focus gate, `GOWORK=off go test ./... -count=1`, and the SILK pitch oracle all pass.

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
