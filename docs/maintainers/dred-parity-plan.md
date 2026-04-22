# DRED Parity Plan

Last updated: 2026-04-22

## Goal

Port DRED in a way that is honest, libopus-first, and incrementally shippable:

- follow pinned libopus `1.6.1` on any doubt
- keep the implementation pure Go with no cgo/runtime C dependency
- avoid heuristic rewrites when libopus behavior is known
- keep hot encode/decode paths allocation-free
- add parity tests before claiming feature support

This plan is narrower and deeper than the general public-release plan. It is
the maintainer execution map for closing the real DRED gaps.

Pure-Go policy:

- runtime/library code must stay pure Go
- neural layers, model binding, and bitstream packing must be implemented in Go
- libopus C code is a pinned reference and optional verification harness only
- no shipped feature may require cgo or an external C toolchain

## Current State

Implemented or in progress:

- DRED payload discovery follows libopus extension iteration semantics
- request-bounded availability matches libopus for the current parse-stage matrix
- standalone tag-gated `DREDDecoder` / `DRED` wrapper exists
- low-cost header and payload-latent counting exist
- retained parse-stage `state[]` / `latents[]` parity exists and is libopus-backed
- pure-Go standalone `opus_dred_process()` equivalent exists for retained DRED features
- the pure-Go RDOVAE decoder runtime now uses a reusable processor/scratch layout to keep the standalone process path explicitly allocation-free
- standalone DRED payload discovery now uses a narrow non-alloc packet-padding scan instead of the generic frame parser
- decoder-side recovery scheduling now has a pure-Go LPCNet-style queue/blend state plus a libopus-shaped processed-feature scheduler
- the pure-Go PLC predictor that sits immediately behind queued DRED/FEC features now loads libopus-style PLC blobs and has zero-allocation runtime coverage plus libopus-backed parity checks on realistic FEC-shaped inputs
- the pure-Go FARGAN conditioner that sits immediately ahead of synthesis now loads the libopus conditioning subnetwork and matches `compute_fargan_cond()` against libopus with retained conv-state parity
- the pure-Go PLC first-loss prefill and concealment-feature lifecycle now has a libopus-backed parity oracle covering backup rotation, queued FEC consumption, predicted fallback, continuity-feature updates, and retained GRU state
- the pure-Go FARGAN signal runtime now mirrors libopus `FARGANState`, `fargan_cont()`, and `fargan_synthesize()` with retained pitch/deemphasis/conv/GRU state and libopus-backed parity checks on both continuity priming and synthesized PCM
- the pure-Go bounded post-analysis concealment path now composes the retained PLC predictor state with the pure-Go FARGAN runtime to synthesize one concealed frame and update retained history, with libopus-backed parity checks on the concealed PCM frame plus retained PLC/FARGAN state
- decoder-side DRED payload invalidation now distinguishes metadata visibility from full buffer clears so packet-entry churn stays low when a new packet replaces the previous cached payload
- a pure-Go CELT neural-entry bridge now mirrors libopus `update_plc_state()` history preparation by deriving 16 kHz PLC seed PCM from retained CELT decode history, with zero-allocation steady-state coverage, a libopus-derived reference helper, and live decoder wiring on nil-packet/FEC PLC entry
- the single-stream decoder now seeds first-loss neural concealment from that CELT bridge, retains a decoder-owned cached-DRED recovery cursor across consecutive losses, and keeps focused tag-gated live-decoder regression coverage around that path while the currently-green 16 kHz libopus parity lives on the explicit/cached DRED seam
- the pure-Go full first-loss LPCNet analysis path now has its own libopus-backed oracle, so `GenerateConcealedFrameFloatWithAnalysis(...)` is pinned against libopus for concealed PCM plus retained PLC/FARGAN/analysis state from the same starting queue/history inputs
- the single-stream decoder also now has an explicit processed-DRED PCM recovery helper for the targeted 48 kHz mono CELT seam, with parity coverage against the cached `Decode(nil)` path across first and second losses, against libopus `opus_decoder_dred_decode_float()` on a real packet, across feature-window offset boundaries, across the first resumed good packet after loss, and across a 48 kHz mono CELT frame-size matrix (`2.5 ms`, `5 ms`, `10 ms`, `20 ms`), including retained PLC/FARGAN runtime state after explicit decode
- the current 16 kHz mono decoder seam now keeps CELT PLC chunking in the same 48 kHz internal frame-size domain as the rest of the decoder core, so low-rate cached recovery no longer feeds invalid `320`-sample CELT PLC requests into first-loss decode
- the current 16 kHz mono decoder seam now has libopus-backed explicit DRED parity for first loss, second loss, next-good-packet handoff, and a `10 ms`/`20 ms` carrier matrix, plus cached-vs-explicit first-loss parity and recovery-cursor coverage on the cached path
- dormant-cost hardening now keeps the single-stream decoder's optional DRED runtime behind a lazy sidecar wrapper with split payload/cache, recovery queue state, neural runtime, and 48 kHz bridge state, so plain decoder DNN blobs no longer allocate standalone DRED payload state on unsupported configs, standalone DRED arming no longer allocates neural/48 kHz bridge state, and clearing standalone DRED no longer wipes main decoder neural history
- the standalone single-stream DRED payload buffer is now allocated lazily on first cached DRED payload instead of at standalone model arm time
- multistream optional-state hardening now avoids dead per-stream analysis/predictor/FARGAN sidecars entirely and keeps main decoder `SetDNNBlob(...)` on a capability-only path because multistream still has no per-stream neural concealment consumer
- libopus-backed real-packet parity coverage exists for:
  - parse-stage state and latents
  - processed DRED features
  - recovery-window / feature-offset probing math
  - recovery queue fill/skip scheduling from processed DRED features
  - repeated / clone `Process()` lifecycle semantics on real DRED packets
  - PLC predictor outputs and recurrent-state evolution on realistic post-DRED FEC update shapes
  - PLC first-loss prefill and one-step concealment feature evolution
  - FARGAN conditioning outputs and retained conv-state evolution
  - FARGAN continuity-state priming and synthesized PCM/state evolution
  - bounded post-analysis concealment frame synthesis and retained PLC/FARGAN state evolution
- live decoder `Decode(nil)` first-loss and second-loss neural concealment parity at the current supported 16 kHz mono seam
- explicit decoder-owned processed-DRED float decode parity at the targeted 48 kHz mono CELT seam
- explicit decoder-owned processed-DRED float decode offset-boundary coverage around the libopus recovery-window transition points for that same seam
- the libopus-backed explicit decoder helper now seeds real prior decoder state from a good packet, loads separate decoder and standalone DRED model blobs, supports warmup-plus-current explicit decode lifecycles, and captures retained CELT 48 kHz bridge state (`last_frame_type`, `plc_fill`, `plc_duration`, `skip_plc`, `plc_preemphasis_mem`, `preemph_memD`, and queued PLC PCM)
- the required unsupported-controls gate now includes explicit 48 kHz warmup-state parity, first-loss and second-loss CELT bridge-state parity, first resumed good-packet handoff parity, and the 48 kHz mono CELT frame-size matrix, not just final PCM parity
- fallback-loss bookkeeping no longer forces the pure-Go LPCNet blend state forward when neural concealment did not actually run, but cached recovery scheduling still preserves the libopus-shaped post-loss recovery phase across recache boundaries
- encoder-side `DFrameSize` staging / rollover groundwork exists in pure Go

Recent closed seams to avoid re-debugging:

- standalone single-stream DRED arm is intentionally payload/model-only; recovery state wakes on first cached DRED payload rather than at arm time
- the 48 kHz cached decoder path must rebuild queued processed DRED features on every loss to match the explicit/libopus second-loss lifecycle; first-loss-only queueing causes small but real drift
- the seeded 48 kHz warmup boundary now matches libopus for `preemph_memD`, `plc_preemphasis_mem`, and the derived 16 kHz PLC update history
- ordinary packet-entry invalidation must drop stale cached DRED payload metadata without clearing live PLC/FARGAN/CELT recovery carry-state; aggressive invalidation causes cached-vs-explicit drift on the first resumed good packet
- decoder PLC chunking in the main decode path must stay in Opus's 48 kHz internal frame-size domain even on lower-rate decoders; chunking by `d.sampleRate` feeds invalid `CELT` PLC frame sizes into the current 16 kHz mono seam
- cached non-48 kHz neural concealment must queue the active DRED recovery window before synthesis; otherwise first-loss PCM drifts from explicit/libopus even when the underlying neural runtime is correct

Still missing for full parity:

- decoder-level parity beyond the current mono seams, especially Hybrid carriers, broader packet coverage, and the final supported-surface decisions for what graduates from quarantine
- encoder-side DRED latent generation and bitstream emission

## Workstreams

### 1. Parse-Stage Parity

Objective:
- match libopus `dred_find_payload()` and `dred_ec_decode()` exactly before any neural-runtime claims

Deliverables:
- parse the same packet extension payload libopus would choose
- decode the same `dred_offset`, `process_stage`, `nb_latents`, `state[50]`, and retained latent vectors
- preserve libopus request-bounded behavior from `opus_dred_parse(..., defer_processing=1)`

Reference files:
- `tmp_check/opus-1.6.1/src/opus_decoder.c`
- `tmp_check/opus-1.6.1/dnn/dred_decoder.c`
- `tmp_check/opus-1.6.1/dnn/dred_decoder.h`
- `tmp_check/opus-1.6.1/dnn/dred_rdovae_stats_data.c`

Acceptance:
- libopus-backed helper tests compare parse-stage floats and metadata bit-for-bit or with exact `float32` bit equality where applicable

### 2. Neural Runtime Port

Objective:
- port the DRED decoder neural runtime needed for `opus_dred_process()` and later audio recovery

Subtasks:
- port Dense layer math
- port GRU layer math
- port GLU path used by RDOVAE decoder
- port Conv1D path used by the DRED/LACE-family runtime where applicable
- preserve libopus approximation functions and accumulation order

Implementation rules:
- stay in `float32`
- do not replace libopus `tanh` / sigmoid approximations with generic `math` versions
- preserve sparse and packed weight layouts exactly
- keep any debug/parity hooks nil or zero-cost when unused

Likely Go landing zone:
- `internal/dred/rdovae/*`
- possibly shared internal DNN helpers only if they reduce duplication without changing math

Reference files:
- `tmp_check/opus-1.6.1/dnn/dred_rdovae_dec.c`
- `tmp_check/opus-1.6.1/dnn/dred_rdovae_dec.h`
- `tmp_check/opus-1.6.1/dnn/nnet.c`
- `tmp_check/opus-1.6.1/dnn/nnet.h`
- `tmp_check/opus-1.6.1/dnn/vec.h`

Acceptance:
- `opus_dred_process()`-level feature generation matches libopus on fixed packets and fixed model weights
- processor/runtime reuse remains zero-allocation for repeated standalone `Process()` calls

### 3. Model Weights And Loader Binding

Objective:
- bind libopus DRED model weights into Go without inventing a second incompatible loader

Deliverables:
- keep using libopus-style blob framing and record naming through `internal/dnnblob`
- generate or validate the exact record family needed by the standalone DRED decoder
- add typed binders for RDOVAE decoder layers rather than re-parsing the blob format ad hoc

Notes:
- the intelligence is in the weights, not just the layer code
- numeric parity depends on matching both record layout and math
- builtin/generated arrays are acceptable for parity fixtures and tests, but public runtime loading should keep mirroring libopus control behavior

Reference files:
- `tmp_check/opus-1.6.1/dnn/parse_lpcnet_weights.c`
- `tmp_check/opus-1.6.1/dnn/dred_rdovae_dec_data.c`
- `tmp_check/opus-1.6.1/dnn/dred_rdovae_dec_data.h`
- `internal/dnnblob/*`

Acceptance:
- a validated DRED decoder blob can populate the Go RDOVAE decoder model with the same logical tensors libopus would load

### 4. Encoder-Side Staging And Bitstream Packing

Objective:
- port the encoder-side DRED staging, latent generation, and entropy/extension packing path

Subtasks:
- mirror `DRED_DFRAME_SIZE` sample staging and buffer rollover
- port encoder-side latent/state generation flow
- port entropy coding for state and latent vectors
- pack the resulting DRED payload into the same Opus extension/padding representation libopus emits

Important note:
- `DFrameSize = 2 * FrameSize` now has a matching pure-Go staging helper for encoder buffering and rollover
- full encoder-side latent generation and payload emission are still not implemented

Reference files:
- `tmp_check/opus-1.6.1/dnn/dred_config.h`
- `tmp_check/opus-1.6.1/dnn/dred_encoder.h`
- `tmp_check/opus-1.6.1/dnn/dred_encoder.c`
- `tmp_check/opus-1.6.1/dnn/dred_rdovae_enc.c`
- `tmp_check/opus-1.6.1/src/opus_encoder.c`

Acceptance:
- Go-generated DRED extension payloads round-trip against libopus parser expectations
- encoder-side packet fixtures match libopus on chosen parity cases before public support claims

### 5. Decoder Recovery Integration

Objective:
- connect processed DRED features to the missing-audio recovery path

Deliverables:
- port the feature consumption path used by `opus_decoder_dred_decode*()`
- match concealment window selection, feature offsets, and output staging
- keep default-build unsupported surfaces quarantined until audio-path parity is real

Reference files:
- `tmp_check/opus-1.6.1/src/opus_decoder.c`
- `tmp_check/opus-1.6.1/src/opus_private.h`

Acceptance:
- fixed-packet DRED recovery output matches libopus closely enough for declared parity goals
- recovery progress across consecutive losses is retained by the decoder instead of being treated as first-loss-only bookkeeping

## Parity Test Plan

### Required Before Claiming Parse Parity

- helper that compares `availableSamples` and `dred_end`
- helper that compares `process_stage`, `dred_offset`, `nb_latents`, `state[]`, and `latents[]`
- packet matrix covering:
  - valid payload
  - first invalid extension then later valid extension
  - short experimental payload
  - positive and negative DRED offsets
  - multiframe packet placement

### Required Before Claiming Process Parity

- helper that compares `opus_dred_process()` outputs, not just parse-stage state
- fixed-model fixture coverage for generated DRED features
- real-packet recovery-window comparison against the feature-offset math used by `opus_decode_native()`
- lifecycle checks for repeated in-place `Process()` and `Process(srcProcessed, dst)` clone behavior
- zero-allocation coverage for standalone parse/process and processed-feature queue scheduling

### Required Before Claiming Recovery/Audio Parity

- helper or fixture path that compares the full `opus_decoder_dred_decode*()` output, not just the bounded post-analysis concealment path
- decoder-level live first-loss and repeated-loss neural concealment tests that start from a good-packet decoder state and compare concealed PCM plus retained cursor/state against libopus at each supported seam
- explicit decoder-owned DRED decode tests that compare processed-DRED PCM output, retained PLC/FARGAN decoder state, retained CELT 48 kHz bridge state, the first resumed good packet after loss, and the 48 kHz mono CELT frame-size matrix against libopus for at least the current supported seam and selected offset-boundary cases
- lost-packet recovery cases across at least:
  - 8 kHz
  - 16 kHz
  - 24 kHz
  - 48 kHz

## Milestones

### M0. Parser Honesty

- payload discovery and availability parity
- no fake claims about model-backed processing

### M1. Retained State Parity

- exact parse-stage `state[]` and `latents[]`
- standalone DRED wrapper exposes retained decoded state cleanly

### M2. RDOVAE Process Parity

- `opus_dred_process()` equivalent exists in Go
- generated feature tensors match libopus
- recovery-window / feature-offset probing matches libopus on real DRED packets
- processed-feature recovery queue scheduling matches libopus and remains allocation-free
- PLC predictor, first-loss prefill, concealment-feature evolution, and FARGAN conditioning/continuity/synthesis parity all match libopus on bounded state-oracle checks
- bounded post-analysis concealment frame synthesis matches libopus on retained-state parity checks
- full first-loss LPCNet analysis concealment now also matches libopus on retained PLC/FARGAN/analysis state when started from the same queue/history inputs
- CELT neural-entry history preparation now matches libopus-derived `update_plc_state()` math closely enough for the 48 kHz -> 16 kHz bridge to stay deterministic and allocation-free in steady state
- the current 16 kHz mono explicit/cached DRED seam now matches libopus across the implemented first-loss, second-loss, next-good-packet, and `10 ms`/`20 ms` matrix coverage
- the 48 kHz mono CELT `FRAME_DRED` bridge is now green through first loss, repeated loss, the first resumed good packet, and the `2.5 ms`/`5 ms`/`10 ms`/`20 ms` frame-size matrix, while the 16 kHz mono seam is now green for explicit first loss, second loss, next-good-packet handoff, the `10 ms`/`20 ms` carrier matrix, cached-vs-explicit first loss, and cached recovery-cursor progression; the next decoder gap is widening libopus-backed lifecycle parity beyond the current mono seams

### M3. Encoder Staging Parity

- `DRED_DFRAME_SIZE` path is real in Go
- encoder buffer rollover and latent scheduling match libopus

### M4. Bitstream And Recovery Parity

- encoder emits real DRED payloads
- decoder recovery path consumes processed DRED features for audio output

## Guardrails

- Do not expose a feature publicly just because controls exist.
- Do not claim DRED support from parser-only or metadata-only work.
- If a result disagrees with libopus and there is no explicit fixture reason, treat libopus as correct first.
- Keep production/runtime code pure Go; any C use must stay in optional reference tooling or parity tests only.
- Run focused tests while iterating and `make verify-production` before merge-ready codec changes.
