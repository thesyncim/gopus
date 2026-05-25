# Libopus Byte Parity Focus

Date: 2026-05-25

Active goal: exact libopus 1.6.1 packet-byte and final-range parity. Type parity is not the current objective unless a type/math-width mismatch directly explains a byte divergence.

## Current CELT Encoder Blocker

Focused fixture:

```sh
GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 go test ./testvectors -run '^TestEncoderVariantProfileParityAgainstLibopusFixture/cases/CELT-FB-20ms-stereo-128k-' -count=1 -v
```

Current local result on darwin/arm64:

| Case | Payload Mismatch | First Mismatch |
| --- | ---: | ---: |
| `CELT-FB-20ms-stereo-128k-chirp_sweep_v1` | `41/51` | frame `1` |
| `CELT-FB-20ms-stereo-128k-am_multisine_v1` | `24/51` | frame `4` |
| `CELT-FB-20ms-stereo-128k-speech_like_v1` | `0/51` | `-1` |
| `CELT-FB-20ms-stereo-128k-impulse_train_v1` | `0/51` | `-1` |

Quality, packet length, mode histogram, and decoded waveform metrics already match. The remaining bug is byte/final-range drift inside CELT entropy coding.

## First Divergence Evidence

Temporary probe against `encoder_compliance_libopus_variants_fixture.json` showed:

- Case: `CELT-FB-20ms-stereo-128k/chirp_sweep_v1`
- First divergent packet: frame `1`
- Packet length: gopus `320`, libopus `320`
- First payload byte mismatch: byte offset `109`
- Final range: gopus `0x34890200`, libopus `0x00c19600`

The packet prefix is identical through byte `108`, so TOC, frame setup, coarse/fine energy, allocation, and early band coding are already aligned for this frame. The divergence begins during CELT band quantization around the band whose range coder tell crosses byte `109`.

Band tell trace from the temporary probe for frame `1`:

| Band | Tell Before | Tell After | Budget `b` |
| ---: | ---: | ---: | ---: |
| 4 | `5879` | `6481` | `619` |
| 5 | `6481` | `6949` | `477` |
| 6 | `6949` | `7276` | `331` |
| 7 | `7276` | `7590` | `314` |

Because byte `109` is crossed inside band `6`, the next useful probe should compare libopus and gopus `quant_band_stereo()` inputs/outputs for frame `1`, band `6`, including theta RDO choice, PVQ pulse vector/index, collapse mask, and saved/restored range-coder bytes.

## Rules For Agents

- Do not edit quality thresholds, payload mismatch baselines, fixture bytes, or type allowlists to make this pass.
- Do not switch back to type-parity cleanup unless the byte probe proves a width/order mismatch is the root cause.
- Prefer C-backed probes over guessing. The relevant libopus functions are `celt/bands.c:quant_all_bands()`, `quant_band_stereo()`, `compute_theta()`, and `celt/vq.c:op_pvq_search_c()`.
- For a fix commit, include the focused fixture output before and after. A useful commit should reduce payload mismatch counts or make a first mismatch frame byte-clean.

## Suggested Next Probe

Add a temporary oracle around `quant_all_bands()` or `quant_band_stereo()` that accepts the normalized `X/Y`, `bandE`, `pulses`, `tf_res`, `balance`, `LM`, `codedBands`, and range-coder prefix for one frame. Compare:

- theta RDO down/up decisions and selected branch
- `itheta`, `qn`, `delta`, `mbits`, `sbits`
- PVQ `K`, pulse vector, CWRS index, and encoded uniform range
- collapse mask
- range-coder `tell`, `offs`, `rng`, `val`, `rem`, `ext`

Remove the temporary probe before committing unless it is converted into a reusable focused oracle test.
