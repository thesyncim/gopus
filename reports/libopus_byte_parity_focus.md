# Libopus Byte Parity Focus

Date: 2026-05-26

Active goal: exact libopus 1.6.1 packet-byte and final-range parity. Type parity is not the current objective unless a type/math-width mismatch directly explains a byte divergence.

## Current CELT Encoder Blocker

Focused fixture:

```sh
GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 go test ./testvectors -run '^TestEncoderVariantProfileParityAgainstLibopusFixture/cases/CELT-FB-20ms-stereo-128k-' -count=1 -v
```

Current local result on darwin/arm64:

| Case | Payload Mismatch | First Mismatch |
| --- | ---: | ---: |
| `CELT-FB-20ms-stereo-128k-chirp_sweep_v1` | `13/51` | frame `2` |
| `CELT-FB-20ms-stereo-128k-am_multisine_v1` | `8/51` | frame `6` |
| `CELT-FB-20ms-stereo-128k-speech_like_v1` | `0/51` | `-1` |
| `CELT-FB-20ms-stereo-128k-impulse_train_v1` | `0/51` | `-1` |

Quality, packet length, mode histogram, and decoded waveform metrics already match. The remaining bug is byte/final-range drift inside CELT entropy coding.

2026-05-26 checkpoint: strict C-backed CELT `exp_rotation`, `alg_quant`, and `alg_unquant` oracle coverage is green again after matching libopus float-build `celt_cos_norm()` coefficient generation. The focused byte lane above is unchanged on a clean working tree, so the next blocker is still the frame `1`, band `6` quantization/range-coder divergence rather than the standalone PVQ rotation helper.

2026-05-26 range-state checkpoint: `rangecoding.EncoderState` keeps full-buffer restore coverage for decision probes that discard speculative bytes, while CELT theta RDO now uses libopus-style shallow `ec_ctx` restore before the second trial. A C-backed synthetic range-coder oracle covers the trial-1-wins dirty-buffer case. `make test-byte-parity-focus` remains `13/51` for `chirp_sweep_v1` and `8/51` for `am_multisine_v1`, so the remaining blocker is still upstream high-band theta/PVQ drift rather than range-state byte preservation alone.

2026-05-26 MDCT source-shape checkpoint: C-backed probes showed the Go and libopus MDCT input buffers were bit-identical for the focused chirp frame, moving the first byte drift root into forward MDCT math. Matching the libopus arm64 float source shape with a float32 `FMADDS` helper in the long-frame MDCT mix reduced the focused payload mismatches from `41/51` to `15/51` for `chirp_sweep_v1` and from `24/51` to `9/51` for `am_multisine_v1`. Speech-like and impulse-train remain byte-clean.

2026-05-26 CELT inner-product checkpoint: the float-build CELT band-energy and normalized-vector RDO inner products now use the same ARM64 FMA-shaped lane accumulation as libopus `celt_inner_prod_neon()`. This reduces focused payload mismatches from `15/51` to `13/51` for `chirp_sweep_v1` and from `9/51` to `8/51` for `am_multisine_v1`; speech-like and impulse-train remain byte-clean.

2026-05-26 linux/amd64 PVQ checkpoint: native amd64 `opPVQSearch` now follows libopus `celt/x86/vq_sse2.c:op_pvq_search_sse2()` instead of the scalar squared-ratio search. The implementation preserves the SSE2 pre-search lane sums, padded tail lanes, strict lane-local max updates, cross-lane tie selection, and x86 `rcp`/`rsqrt` approximation points. The C VQ oracle helper now calls the real x86 `op_pvq_search_sse2()` symbol and allocates the padded `iy[N+3]` lanes, so `TestOPPVQSearchMatchesLibopusFloatPath` covers the architecture helper on linux/amd64. This targets the CI provenance failure where freshly generated native linux/amd64 fixtures gave stronger libopus CELT stereo quality than the scalar Go PVQ path.

2026-05-26 alloc-trim inner-product checkpoint: `alloc_trim_analysis()` now uses the same per-architecture `celt_inner_prod()` order as libopus float builds instead of a local four-lane reduction. On linux/amd64, the C-backed allocation probe for `CELT-FB-20ms-stereo-128k-chirp_sweep_v1` drops from three allocation mismatches (`45/48/49`) to one (`30`). The focused packet-byte lane remains `37/51` for `chirp_sweep_v1`, `30/51` for `am_multisine_v1`, and clean for speech/impulse, so this is a real allocation fix but not the remaining entropy-byte fix.

2026-05-26 linux/amd64 CI checkpoint: amd64 float builds now use the same x86/SSE-shaped `celt_inner_prod()` order as libopus for normalized-vector energy/theta decisions, while `purego` keeps the scalar path. CELT coarse-energy encoding also follows `celt/quant_bands.c:quant_coarse_energy_impl()` float expression order for `f = x - coef*oldE - prev[c]` and the `prev[c] + q - beta*q` update. This fixes the fresh `test-linux-parity` regression for `CELT-FB-20ms-stereo-128k-impulse_train_v1` (`gapQ -53.32`, `payloadMismatch=43/51` -> `gapQ=0.00`, `payloadMismatch=0/51`) and keeps speech clean. On linux/amd64 the focused byte lane is now `35/51` for `chirp_sweep_v1` (first mismatch frame `1`) and `30/51` for `am_multisine_v1` (first mismatch frame `2`). The frame `30` allocation mismatch remains one frame, so the next allocation investigation should stay on the coarse/tell trace rather than changing `clt_compute_allocation()`.

2026-05-26 stereo-split source-shape checkpoint: `stereoSplit()` now rounds the two `.70710678f` multiplies separately before add/sub, matching `celt/bands.c:stereo_split()` `MULT32_32_Q31(QCONST32(.70710678f,31), ...)` source shape in the float build. The focused byte lane is unchanged, so this is covered as a source-parity guard rather than the current 128k blocker.

Use the dedicated focused target for before/after checks:

```sh
make test-byte-parity-focus
```

## First Divergence Evidence

Historical pre-MDCT-fix probe against `encoder_compliance_libopus_variants_fixture.json` showed:

- Case: `CELT-FB-20ms-stereo-128k/chirp_sweep_v1`
- First divergent packet: frame `1`
- Packet length: gopus `320`, libopus `320`
- First payload byte mismatch: byte offset `109`
- Final range: gopus `0x34890200`, libopus `0x00c19600`

That frame is now byte-clean after the MDCT source-shape fix, so this trace is retained only as historical evidence. The current first focused mismatch is frame `2` for `chirp_sweep_v1` and frame `6` for `am_multisine_v1`; refresh the band/range trace at those frames before making the next byte-parity claim.

Band tell trace from the temporary probe for frame `1`:

| Band | Tell Before | Tell After | Budget `b` |
| ---: | ---: | ---: | ---: |
| 4 | `5879` | `6481` | `619` |
| 5 | `6481` | `6949` | `477` |
| 6 | `6949` | `7276` | `331` |
| 7 | `7276` | `7590` | `314` |

Because byte `109` is crossed inside band `6`, the next useful probe should compare libopus and gopus `quant_band_stereo()` inputs/outputs for frame `1`, band `6`, including theta RDO choice, PVQ pulse vector/index, collapse mask, and saved/restored range-coder bytes.

## Conversion, Cast, And Copy Audit

Status as of 2026-05-25:

- The active coordination rule is byte parity, not standalone type parity. `AGENTS.md` now tells agents to run `make test-byte-parity-focus` around CELT band/PVQ/RDO changes.
- Theta RDO is confirmed to be on the byte path. Changing the Go RDO inner-product accumulator order moved the focused mismatch counts from `41/51` and `24/51` to `42/51` and `23/51`, so small math-order changes can flip packet bytes even when quality remains identical.
- That accumulator-order change was not kept: `go test ./celt -run '^TestThetaRDODistortionMatchesLibopusFloatPath$' -count=1 -v` failed against the existing C-backed `libopus_celt_vq_info.c:eval_theta_dist()` oracle. Keep the current `innerProductNorm` order unless a fixture-specific libopus trace proves the target fixture was generated with a different inner-product kernel.
- Range-coder trial save/restore has source-shaped coverage now: Go keeps full-buffer restore for non-libopus-shaped decision probes, and CELT theta RDO shallow-restores scalar `ec_ctx` state before trial 1 while restoring trial-0 bytes only when trial 0 wins. The next trace should move past generic range-state restore and compare focused frame `2`/band high-band `X/Y`, `norm`, theta, PVQ pulse/index, and collapse-mask inputs.
- Redundant `float32(...)` and alias casts remain in CELT hot paths, but they are not by themselves byte-parity fixes. Remove or rewrite them only when the focused fixture or C oracle shows the conversion changes a range event, PVQ pulse/index, theta decision, collapse mask, or final range.
- The only copy cleanup that matters for this blocker is stateful copy/restore around `quant_band_stereo()`: `X/Y`, `norm`, range bytes, extension range bytes, and lowband folding state. Broad cleanup of unrelated `copy()` calls is a distraction unless a trace ties the copy to byte drift.

## Rules For Agents

- Do not edit quality thresholds, payload mismatch baselines, fixture bytes, or type allowlists to make this pass.
- Do not switch back to type-parity cleanup unless the byte probe proves a width/order mismatch is the root cause.
- Match the libopus architecture helper, not just the typedef. If libopus routes a focused path through NEON/SSE/etc., preserve that helper's lane update, reduction, and scalar-tail behavior in Go before claiming byte parity.
- Prefer C-backed probes over guessing. The relevant libopus functions are `celt/bands.c:quant_all_bands()`, `quant_band_stereo()`, `compute_theta()`, `celt/vq.c:op_pvq_search_c()`, and architecture-selected helpers such as `celt/x86/vq_sse2.c:op_pvq_search_sse2()`.
- For a fix commit, include the focused fixture output before and after. A useful commit should reduce payload mismatch counts or make a first mismatch frame byte-clean.

## Suggested Next Probe

Add a temporary oracle around `quant_all_bands()` or `quant_band_stereo()` that accepts the normalized `X/Y`, `bandE`, `pulses`, `tf_res`, `balance`, `LM`, `codedBands`, and range-coder prefix for the current first divergent frames (`chirp_sweep_v1` frame `2`, `am_multisine_v1` frame `6`). Compare:

- theta RDO down/up decisions and selected branch
- `itheta`, `qn`, `delta`, `mbits`, `sbits`
- PVQ `K`, pulse vector, CWRS index, and encoded uniform range
- collapse mask
- range-coder `tell`, `offs`, `rng`, `val`, `rem`, `ext`

Remove the temporary probe before committing unless it is converted into a reusable focused oracle test.
