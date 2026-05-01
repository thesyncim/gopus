# DRED Parity Plan

Last updated: 2026-04-30

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
- the single-stream decoder now seeds first-loss neural concealment from that CELT bridge, retains a decoder-owned cached-DRED recovery cursor across consecutive losses, and keeps focused tag-gated live-decoder regression coverage around that path while broader stereo/multistream DRED recovery stays out of the supported surface
- the pure-Go full first-loss LPCNet analysis path now has its own libopus-backed oracle, so `GenerateConcealedFrameFloatWithAnalysis(...)` is pinned against libopus for concealed PCM plus retained PLC/FARGAN/analysis state from the same starting queue/history inputs
- the single-stream decoder also now has an explicit processed-DRED PCM recovery helper for the targeted 48 kHz mono CELT seam, with experimental coverage against cached/live loss, resumed-good-packet handoff, libopus `opus_decoder_dred_decode_float()` on a real packet, feature-window offset boundaries, and the broader 48 kHz mono CELT frame-size matrix (`2.5 ms`, `5 ms`, `10 ms`, `20 ms`), including retained PLC/FARGAN runtime state after explicit decode
- the same 48 kHz mono decoder DRED runtime now also has experimental Hybrid SWB/FB carrier coverage on explicit/live first-loss and resumed-good-packet handoff seams, and the current pure-Go Hybrid recovery now follows the libopus-shaped Hybrid PLC base instead of the earlier CELT-only replacement path across the exercised `10 ms` and `20 ms` handoff seams; that Hybrid decoder audio matrix remains in the experimental lane until it is green on Linux
- the current 16 kHz mono decoder seam now keeps CELT PLC chunking in the same 48 kHz internal frame-size domain as the rest of the decoder core, so low-rate cached recovery no longer feeds invalid `320`-sample CELT PLC requests into first-loss decode
- the current 16 kHz mono decoder seam has libopus-backed experimental coverage for resumed-good-packet handoff after first and second loss plus targeted cached/explicit runtime coverage, and the decoder now explicitly primes the CELT-to-LPCNet entry bridge on first neural/DRED concealment for both cached-live and explicit 16 kHz paths; direct neural-concealment PCM and broader explicit matrix checks stay experimental until their Linux parity is stable
- the 16 kHz decoder oracle helpers now use the decoder API sample rate for libopus probes instead of the carrier packet's emitted 48 kHz rate, which avoids false explicit/libopus and explicit/cached drift on the low-rate seams while keeping the internal 48 kHz concealment frame-size domain intact
- the current 16 kHz mono decoder entry bridge now follows libopus `update_plc_state()` more closely by reseeding retained PLC history without pre-replaying LPCNet analysis before first-loss concealment
- the current 16 kHz mono cached/live sequence seam now also has experimental libopus-backed frame-size matrix coverage for resumed-good-packet handoffs after first and second loss across the exercised `10 ms` and `20 ms` carrier sizes, with direct first-loss/second-loss PCM checks kept in the experimental lane instead of relying on a single default carrier size
- decoder-side DRED request/recovery bookkeeping now explicitly tracks the exercised 16 kHz mono seam in the same internal 48 kHz frame-size domain the main decode/PLC path already uses, and the 16 kHz decoder parity oracles now probe libopus in that same runtime domain instead of mixing the public config rate with 48 kHz-sized concealment requests
- the 16 kHz explicit decoder matrix now also treats retained CELT `FRAME_DRED` bridge state as first-class parity surface across explicit/libopus, explicit/cached, second-loss, follow-up packet, and frame-size checks instead of only checking PCM/PLC/FARGAN state; that broader explicit matrix remains experimental until it is green on Linux
- explicit-vs-cached equality is no longer a truthful live decoder gate: libopus uses `FRAME_PLC_NEURAL` on ordinary cached `Decode(nil)` loss recovery and `FRAME_DRED` only on the explicit DRED API, so the real decoder split is explicit-vs-libopus plus live-vs-live-sequence rather than explicit-vs-cached sameness
- the 16 kHz explicit-vs-cached follow-up seam is now exercised both after a single DRED loss and across the 16 kHz follow-up frame-size matrix; these checks remain experimental until their Linux parity is stable
- the 48 kHz mono explicit-vs-cached follow-up seam is now exercised after a single DRED loss and across the first-loss follow-up frame-size matrix too; these checks remain experimental until their Linux parity is stable
- the cached mono CELT 48 kHz decoder seam now also has direct first-loss and resumed-good-packet oracle coverage against the live-sequence helper, with explicit-helper comparisons kept as experimental coverage instead of being used as the required support claim
- the cached mono CELT 48 kHz decoder seam now also has direct second-loss and second-loss-to-next-packet oracle coverage against the live-sequence helper, with explicit-helper comparisons kept as experimental coverage while the cached/live mono matrix moves closer to the Hybrid matrix
- the cached mono CELT 48 kHz decoder seam now also has direct live-sequence-only first-loss and second-loss oracle coverage without requiring a resumed good packet, so the steady cached/live loss path no longer has to lean on the explicit DRED helper as its primary oracle
- the cached mono Hybrid SWB/FB decoder seam now also has direct live-sequence-only first-loss and broader second-loss oracle coverage in the experimental lane, so the cached Hybrid loss path is less dependent on `opus_decoder_dred_decode_float()` as a proxy for `opus_decode_float(NULL, ...)`
- cached recovery-cursor coverage now spans the old 16 kHz seam plus 48 kHz mono CELT advancement/reset behavior, while the 48 kHz mono Hybrid live-sequence seam now pins `dredRecovery` idle because ordinary cached `Decode(nil)` does not queue DRED recovery on the libopus Hybrid path
- libopus-backed cached recovery-window lifecycle checks now also span 48 kHz mono CELT and 48 kHz mono Hybrid, so queued-feature windowing is no longer only cross-checked on the low-rate seam
- dormant-cost hardening now keeps the single-stream decoder's optional DRED runtime behind a lazy sidecar split into payload/cache, recovery queue state, neural runtime, and 48 kHz bridge state: plain decode keeps the sidecar nil, standalone DRED arming wakes payload state only, and main decoder `SetDNNBlob(...)` now validates and retains neural model families without eagerly allocating the single-stream DRED sidecar, materializing the neural runtime only on first real concealment/history use on the eligible mono 16 kHz / 48 kHz seams
- resumed good packets on the single-stream DRED path now follow libopus `PLC_SKIP_UPDATES` more closely by clearing only the retained LPCNet blend flag instead of rewriting PLC history/cursors on every successful decode after concealment
- `Decoder.Reset()` now drops activated single-stream DRED recovery, neural, and 48 kHz bridge runtime back to the dormant control-only state while retaining validated model blobs, so optional extras return to their pre-activation zero-cost posture on a new stream
- the standalone single-stream DRED payload buffer is now allocated lazily on first cached DRED payload instead of at standalone model arm time
- the top-level decoder DRED internals are now build-tag split too, so default `.` builds avoid importing `internal/dred`, RDOVAE, or LPCNet PLC internals while retaining DNN blob readiness flags through no-op default runtime stubs
- multistream optional-state hardening now keeps standalone DRED on a cache/timing-only path and keeps main decoder `SetDNNBlob(...)` capability-only because multistream still has no per-stream neural concealment consumer; the multistream decoder DRED cache/runtime helpers are now build-tag split too, so default `./multistream` builds avoid importing `internal/dred`, RDOVAE, or LPCNet PLC internals
- the current 48 kHz Hybrid DRED path now updates retained CELT waveform/cadence state with a state-only pure-Go DRED concealment pass instead of only relabeling an ordinary Hybrid PLC loss after the fact; this pins the next-good-packet handoff parity that remained after the earlier cadence-only sync work
- a new internal libopus decoder-sequence helper now exists for cached/live DRED decoder parity, covering `carrier good -> first loss -> second loss -> optional next good packet` via `opus_decode_native(..., dred, dred_offset)` instead of only the public explicit `opus_decoder_dred_decode_float()` seam
- that live-sequence helper now drives float recovery steps through `opus_decoder_dred_decode_float()` itself, so the cached/live float oracle matches the same public libopus DRED entrypoint the explicit decoder parity tests already use
- the first decoder tests now use that sequence helper for live/cached handoff parity on the 16 kHz mono seam, including its exercised frame-size matrix, and on the cached Hybrid SWB/FB next-good-packet seam, so those paths are no longer limited to explicit-oracle comparisons only
- the experimental unsupported-controls parity lane now also selects those live-sequence handoff tests, so CI/local sweeps surface real cached/live decoder deltas instead of silently carrying unrun oracle coverage
- the unsupported-controls root-package allowlist now also matches the renamed cached/live decoder parity tests that still use the explicit DRED oracle, so CI no longer silently drops those seams after `...MatchesLibopus` -> `...MatchesExplicitDREDOracle` renames
- decoder ownership tests now treat the main decoder DNN path as a lazy zero-cost sidecar: `SetDNNBlob(...)` keeps model readiness/validation, but the single-stream DRED sidecar may stay nil until real recovery/history work begins
- ordinary good-packet decode no longer wakes `decoderDREDRecoveryState` just because the main decoder DNN blob is armed; first-loss entry now relies on the CELT-to-LPCNet bridge or existing recovery state instead of materializing recovery bookkeeping on the first normal packet
- ordinary cached/live `Decode(nil)` loss no longer reuses the explicit `FRAME_DRED` path just because cached DRED payload is present; the live path now stays on the libopus-shaped `FRAME_PLC_NEURAL` seam and only advances cached DRED recovery/blend bookkeeping after successful concealment
- the cached mono CELT 48 kHz live sequence now also avoids pre-priming LPCNet analysis before first loss, matching libopus `update_plc_state()` seeding followed by analysis inside `lpcnet_plc_conceal()`; the stale cached-vs-explicit CELT oracle tests are disabled because explicit DRED remains a separate `FRAME_DRED` entrypoint
- the cached mono Hybrid 48 kHz live sequence now follows the same split: ordinary `Decode(nil)` does not queue cached DRED FEC or enter the explicit DRED surface; SILK may use the ordinary lowband deep-PLC hook while CELT stays on the libopus Hybrid noise PLC branch (`start != 0`), and the stale cached-vs-explicit Hybrid oracle tests are disabled for the same reason
- the default root decoder tests now explicitly pin that lazy sidecar contract too: a good packet with only the main decoder DNN blob armed must leave the DRED sidecar nil until actual concealment/history work begins, and `Decode(nil)` is the first point allowed to materialize the sidecar on that path
- these decoder parity claims are seam-specific and libopus-backed, but the decoder audio numerical matrix currently remains experimental because Linux still exposes CELT/Hybrid cached-live and explicit DRED drift; the required gate is limited to green non-decoder-audio, bootstrap, and carried-payload seams, while broader decoder, stereo, and multistream packet coverage remains separate work unless explicitly covered by green parity tests
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
- libopus-backed live decoder `Decode(nil)` resumed-good-packet handoff coverage at the current 16 kHz mono seam, with direct first-loss and second-loss neural concealment PCM checks remaining in the experimental lane until their Linux parity is stable
- libopus-backed explicit decoder-owned processed-DRED float coverage at the targeted 48 kHz mono CELT seam, with the broader PCM/state matrix remaining experimental until it is green on Linux
- explicit decoder-owned processed-DRED float decode offset-boundary coverage around the libopus recovery-window transition points for that same seam remains an experimental parity surface
- the libopus-backed explicit decoder helper now seeds real prior decoder state from a good packet, loads separate decoder and standalone DRED model blobs, supports warmup-plus-current explicit decode lifecycles, and captures retained CELT 48 kHz bridge state (`last_frame_type`, `plc_fill`, `plc_duration`, `skip_plc`, `plc_preemphasis_mem`, `preemph_memD`, and queued PLC PCM)
- the required supported DRED tag gate now carries standalone DRED wrapper lifecycle/no-allocation and libopus parse/decode coverage plus the narrow SILK wideband 20 ms encoder carried-payload/primary-budget proof, while the unsupported-controls gate covers the green quarantine/core and selected non-decoder-audio parity surface: API exposure, standalone DRED parse/process/recovery, cached recovery lifecycle/cursor bookkeeping, parser availability, that same narrow carried-payload seam, internal converter/payload/basic-analysis seams, and 48 kHz runtime bootstrap checks
- the wider carried-payload matrix, broader decoder explicit/live numerical parity matrix, and real-model RDOVAE/PitchDNN/conceal-analysis oracles remain in the separate `test-unsupported-controls-parity-experimental` lane outside the production gate until their Linux matrix is green
- fallback-loss bookkeeping no longer forces the pure-Go LPCNet blend state forward when neural concealment did not actually run, but cached recovery scheduling still preserves the libopus-shaped post-loss recovery phase across recache boundaries
- encoder-side `DFrameSize` staging / rollover groundwork exists in pure Go
- encoder-side DNN admission is now stricter than simple manifest booleans: the retained encoder DNN blob must bind both the pure-Go RDOVAE encoder model family and the shared pure-Go PitchDNN analysis model family before `DREDModelLoaded()` reports ready, which gives the encoder path a real model-contract seam before latent generation and bitstream emission land
- the pure-Go RDOVAE encoder runtime now mirrors libopus `dred_rdovae_encode_dframe()` with reusable caller-owned processor state/scratch and focused zero-allocation coverage; its real-model numerical oracle remains experimental until the Linux tolerance matrix is green
- the pure-Go encoder-side same-rate 16 kHz latent generator now mirrors libopus `dred_process_frame()` for mono and stereo-downmix input by feeding two retained LPCNet single-frame feature vectors into the RDOVAE encoder runtime, so the encoder path now has real latent/state generation on the exercised seam instead of only model admission
- encoder optional-extra ownership is now tighter too: DRED controls/runtime sit behind a lazy sidecar, `SetDNNBlob(...)` only binds model families, the runtime materializes only when model+duration+eligible mono or stereo-downmix use actually arms it, disabling/resetting DRED drops that sidecar back to a dormant state, and the internal encoder DRED runtime files are build-tag split so default `./encoder` builds use no-op stubs instead of importing `internal/dred` or `internal/lpcnetplc`
- the internal encoder path now advances the exercised 16 kHz latent generator before DTX on armed mono and stereo frames, which is closer to libopus `opus_encoder.c` ordering
- the pure-Go encoder path now also ports the libopus `dred_convert_to_16k()` front-end for `8/12/24/48 kHz` mono/stereo input with retained DF2T filter memory inside the lazy DRED sidecar instead of hard-rejecting every non-16 kHz input, and the converter has dedicated green libopus-backed parity coverage in the DRED parity lane
- the lazy encoder DRED runtime now retains libopus-shaped latent/state history plus `dred_offset` / `latent_offset` bookkeeping for later emission instead of throwing away every emitted latent after the latest frame
- the lazy encoder DRED runtime now also retains the 2.5 ms activity window plus `last_extra_dred_offset` bookkeeping that libopus uses to delay or suppress payloads around silence transitions
- the pure-Go encoder side now has a byte-for-byte libopus-backed DRED payload builder for the `dred_encode_silk_frame()` seam, including header coding, quantizer signalling, offset bookkeeping, Laplace-coded state/latent chunks, and the same delayed-out-of-silence suppression rules
- the non-CELT encoder path now has bounded pure-Go DRED extension carriers for both the single-frame seam and the repacketized SILK long-packet seam, and the carry path now follows libopus more closely by only padding those extension packets in CBR instead of padding VBR/CVBR packets unconditionally
- encoder carried DRED now reserves the libopus-shaped DRED bitrate from the primary single-frame SILK/short-Hybrid budget, with the narrow SILK wideband 20 ms carried-payload/primary-budget seam pinned in the supported DRED tag gate and mirrored in the required unsupported-controls DRED parity gate
- the wider carried SILK/Hybrid encoder packet-parity matrix remains in the experimental lane through `make test-unsupported-controls-parity-experimental`; remaining encoder work is closing that currently exercised `20/40/60 ms` mono and `20 ms` stereo matrix before it can block production
- the unsupported-controls gate and libopus-backed DRED/PLC helper tests now explicitly bootstrap the pinned libopus source snapshot through `tools/ensure_libopus.sh` instead of assuming a preseeded tarball/cache
- the libopus-backed DRED/PLC helper builds now use scalar OS/arch-specific `build-opus-dred-scalar-*` directories, so Linux/amd64 helper oracles do not drift just because libopus enabled SSE/AVX intrinsics while the pure-Go port is tracking scalar math

Recent closed seams to avoid re-debugging:

- standalone single-stream DRED arm is intentionally payload/model-only; recovery state wakes on first cached DRED payload rather than at arm time
- the 48 kHz cached decoder path must rebuild queued processed DRED features on every loss to match the explicit/libopus second-loss lifecycle; first-loss-only queueing causes small but real drift
- the seeded 48 kHz warmup boundary now matches libopus for `preemph_memD`, `plc_preemphasis_mem`, and the derived 16 kHz PLC update history
- the active 48 kHz `FRAME_DRED` bridge must retain `plc_preemphasis_mem` at the frame boundary, not after the overlap tail; libopus keeps the overlap-only preemphasis state local
- ordinary packet-entry invalidation must drop stale cached DRED payload metadata without clearing live PLC/FARGAN/CELT recovery carry-state; aggressive invalidation causes cached-vs-explicit drift on the first resumed good packet
- decoder PLC chunking in the main decode path must stay in Opus's 48 kHz internal frame-size domain even on lower-rate decoders; chunking by `d.sampleRate` feeds invalid `CELT` PLC frame sizes into the current 16 kHz mono seam
- decoder DRED request/recovery math and the matching libopus parity helpers must use that same exercised 48 kHz runtime frame-size domain on the current 16 kHz mono seam; mixing the public config rate with 48 kHz-sized concealment offsets produces false recovery-window and live-sequence mismatches
- cached non-48 kHz neural concealment must queue the active DRED recovery window before synthesis; otherwise first-loss PCM drifts from explicit/libopus even when the underlying neural runtime is correct
- 16 kHz decoder recovery must reseed LPCNet history from the retained CELT bridge on first neural/DRED entry by feeding `lpcnet_plc_update()`-shaped in-place updates, not by replacing the retained history window or pre-replaying analysis; skipping that entry carry or rebuilding the history wholesale makes the cached-live and explicit 16 kHz decoder paths agree with each other while both still drift from libopus
- good packets after DRED/neural concealment on libopus builds with `PLC_SKIP_UPDATES` clear only `blend`; treating those packets as full PLC history updates makes the cached and explicit paths look internally consistent while both still drift from libopus on the resumed-good-packet handoff
- fallback loss entry still has to mark the real retained PLC blend state, not only the shadow `dredBlend` bookkeeping; otherwise cached recovery-window math may look armed while the underlying PLC lifecycle remains in the good-packet state and cross-platform decoder tests fail
- Hybrid resumed-packet parity depends on CELT-side `FRAME_DRED` cadence/state retention, not just on advancing the DRED feature queue; treating Hybrid loss as ordinary PLC plus hidden neural-state advancement leaves the next good packet far from libopus

Still missing for full parity:

- decoder-level parity beyond the current mono seams after CELT + Hybrid 48 kHz mono and the exercised 16 kHz mono seam, especially stereo/multistream coverage, broader packet coverage, and the final supported-surface decisions for what graduates from quarantine
- model-backed `opus_decoder_dred_decode*()` parity beyond the currently exercised mono explicit seams, including broader `LPCNetEncState`-shaped analysis/runtime coverage and decoder-owned integration for any surfaces that graduate from quarantine
- broader live-oracle adoption beyond the covered mono cached seams; some cached/live tests still compare against the explicit `opus_decoder_dred_decode_float()` helper, and stereo/multistream paths plus wider packet matrices still need migration to live-sequence coverage where those surfaces become supported
- encoder-side DRED beyond the current exercised latent-generation and carried-payload seams: broadening rate / packet-shape / stereo / multistream coverage around the payload-emission path and finalizing which surfaces graduate from quarantine
- clean runtime re-verification after the current macOS AppleSystemPolicy issue: freshly built local Go test binaries on this machine are being rejected by Gatekeeper (`spctl --assess --type execute` rejects them, `syspolicyd` logs repeated `Unable to initialize qtn_proc: 3`, and the processes stall at `_dyld_start`), so CI remains the reliable runtime oracle while that host-policy issue is open

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
- the current pure-Go encoder seam now covers retained `8/12/16/24/48 kHz` mono and stereo-downmix conversion into the 16 kHz latent/state path, the libopus `dred_encode_silk_frame()` payload-coding seam, bounded single-frame carried extensions, exercised SILK/Hybrid carried-extension seams, and primary DRED budget reservation on the covered cases, while broader packet-shape coverage and final support-surface decisions remain open

Reference files:
- `tmp_check/opus-1.6.1/dnn/dred_config.h`
- `tmp_check/opus-1.6.1/dnn/dred_encoder.h`
- `tmp_check/opus-1.6.1/dnn/dred_encoder.c`
- `tmp_check/opus-1.6.1/dnn/dred_rdovae_enc.c`
- `tmp_check/opus-1.6.1/src/opus_encoder.c`

Acceptance:
- Go-generated DRED extension payloads round-trip against libopus parser expectations and the encoder-side payload bytes match libopus on focused history/activity fixtures
- encoder-side packet fixtures match libopus on chosen parity cases before public support claims

### 5. Decoder Recovery Integration

Objective:
- connect processed DRED features to the missing-audio recovery path

Deliverables:
- port the feature consumption path used by `opus_decoder_dred_decode*()`
- match concealment window selection, feature offsets, and output staging
- keep default-build unsupported surfaces absent and broader quarantine surfaces fenced until their audio-path parity is real

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
- helper or fixture path that compares the cached/live decoder loss path (`opus_decode_float(..., NULL, ...)`) directly, not just the explicit `opus_decoder_dred_decode*()` entrypoint
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
- the current 16 kHz mono cached/live seam has experimental resumed-good-packet handoff PCM coverage after first and repeated loss across the exercised frame-size matrix; direct concealment PCM and broader explicit matrix checks remain in the experimental lane until their Linux parity is stable
- the 48 kHz mono CELT `FRAME_DRED` bridge and the 48 kHz mono Hybrid SWB/FB first-loss/handoff seams remain experimental on the explicit/live decoder audio matrix, with Hybrid now following the libopus-shaped Hybrid PLC base; the next decoder gap is broader repeated-loss, packet, stereo, and multistream coverage
- explicit 48 kHz DRED now drives the dedicated `FRAME_DRED` bridge instead of accidentally reusing the live `FRAME_PLC_NEURAL` bridge, but the exercised explicit decoder matrix remains experimental until it is green on Linux

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
