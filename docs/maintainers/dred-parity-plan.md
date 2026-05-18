# DRED Parity Plan

Last updated: 2026-05-17

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
- the single-stream decoder also now has an explicit processed-DRED PCM recovery helper for the targeted 48 kHz mono CELT seam, with required unsupported-controls parity coverage against cached/live loss, resumed-good-packet handoff, libopus `opus_decoder_dred_decode_float()` on a real packet, feature-window offset boundaries, and the broader 48 kHz mono CELT frame-size matrix (`2.5 ms`, `5 ms`, `10 ms`, `20 ms`), including retained PLC/FARGAN runtime state after explicit decode
- the same 48 kHz mono decoder DRED runtime now also has required unsupported-controls Hybrid SWB/FB carrier coverage on explicit/live first-loss and resumed-good-packet handoff seams, and the current pure-Go Hybrid recovery now follows the libopus-shaped Hybrid PLC base instead of the earlier CELT-only replacement path across the exercised `10 ms` and `20 ms` handoff seams; stereo/multistream Hybrid decoder coverage remains separate work
- the current 16 kHz mono decoder seam now keeps CELT PLC chunking in the same 48 kHz internal frame-size domain as the rest of the decoder core, so low-rate cached recovery no longer feeds invalid `320`-sample CELT PLC requests into first-loss decode
- the current 16 kHz mono decoder seam has required libopus-backed coverage for resumed-good-packet handoff after first and second loss plus targeted cached/explicit runtime coverage, and the decoder now explicitly primes the CELT-to-LPCNet entry bridge on first neural/DRED concealment for both cached-live and explicit 16 kHz paths
- the 16 kHz decoder oracle helpers now use the decoder API sample rate for libopus probes instead of the carrier packet's emitted 48 kHz rate, which avoids false explicit/libopus and explicit/cached drift on the low-rate seams while keeping the internal 48 kHz concealment frame-size domain intact
- the current 16 kHz mono decoder entry bridge now follows libopus `update_plc_state()` more closely by reseeding retained PLC history without pre-replaying LPCNet analysis before first-loss concealment
- the current 16 kHz mono cached/live sequence seam now also has required libopus-backed frame-size matrix coverage for resumed-good-packet handoffs after first and second loss across the exercised `10 ms` and `20 ms` carrier sizes, with direct first-loss/second-loss PCM checks in the required unsupported-controls parity lane instead of relying on a single default carrier size
- decoder-side DRED request/recovery bookkeeping now explicitly tracks the exercised 16 kHz mono seam in the same internal 48 kHz frame-size domain the main decode/PLC path already uses, and the 16 kHz decoder parity oracles now probe libopus in that same runtime domain instead of mixing the public config rate with 48 kHz-sized concealment requests
- the 16 kHz explicit decoder matrix now also treats retained CELT `FRAME_DRED` bridge state as first-class parity surface across explicit/libopus, explicit/cached, second-loss, follow-up packet, and frame-size checks instead of only checking PCM/PLC/FARGAN state; the current mono matrix is now required in `make test-unsupported-controls-parity`
- explicit-vs-cached equality is no longer a truthful live decoder gate: libopus uses `FRAME_PLC_NEURAL` on ordinary cached `Decode(nil)` loss recovery and `FRAME_DRED` only on the explicit DRED API, so the real decoder split is explicit-vs-libopus plus live-vs-live-sequence rather than explicit-vs-cached sameness
- the 16 kHz explicit-vs-cached follow-up seam is now exercised both after a single DRED loss and across the 16 kHz follow-up frame-size matrix in the required unsupported-controls parity gate
- the 48 kHz mono explicit-vs-cached follow-up seam is now exercised after a single DRED loss and across the first-loss follow-up frame-size matrix too in the required unsupported-controls parity gate
- the cached mono CELT 48 kHz decoder seam now also has direct first-loss and resumed-good-packet oracle coverage against the live-sequence helper, with explicit-helper comparisons kept as experimental coverage instead of being used as the required support claim
- the cached mono CELT 48 kHz decoder seam now also has direct second-loss and second-loss-to-next-packet oracle coverage against the live-sequence helper, with explicit-helper comparisons kept as experimental coverage while the cached/live mono matrix moves closer to the Hybrid matrix
- the cached mono CELT 48 kHz decoder seam now also has direct live-sequence-only first-loss and second-loss oracle coverage without requiring a resumed good packet, so the steady cached/live loss path no longer has to lean on the explicit DRED helper as its primary oracle
- the cached mono Hybrid SWB/FB decoder seam now also has direct live-sequence-only first-loss and broader second-loss oracle coverage in the required unsupported-controls parity lane, so the cached Hybrid loss path is less dependent on `opus_decoder_dred_decode_float()` as a proxy for `opus_decode_float(NULL, ...)`
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
- the required unsupported-controls parity lane now also selects those live-sequence handoff tests, so CI/local sweeps surface real cached/live decoder deltas instead of silently carrying unrun oracle coverage
- the unsupported-controls root-package allowlist now also matches the renamed cached/live decoder parity tests that still use the explicit DRED oracle, so CI no longer silently drops those seams after `...MatchesLibopus` -> `...MatchesExplicitDREDOracle` renames
- decoder ownership tests now treat the main decoder DNN path as a lazy zero-cost sidecar: `SetDNNBlob(...)` keeps model readiness/validation, but the single-stream DRED sidecar may stay nil until real recovery/history work begins
- ordinary good-packet decode no longer wakes `decoderDREDRecoveryState` just because the main decoder DNN blob is armed; first-loss entry now relies on the CELT-to-LPCNet bridge or existing recovery state instead of materializing recovery bookkeeping on the first normal packet
- ordinary cached/live `Decode(nil)` loss no longer reuses the explicit `FRAME_DRED` path just because cached DRED payload is present; the live path now stays on the libopus-shaped `FRAME_PLC_NEURAL` seam and only advances cached DRED recovery/blend bookkeeping after successful concealment
- the cached mono CELT 48 kHz live sequence now also avoids pre-priming LPCNet analysis before first loss, matching libopus `update_plc_state()` seeding followed by analysis inside `lpcnet_plc_conceal()`; the stale cached-vs-explicit CELT oracle tests are disabled because explicit DRED remains a separate `FRAME_DRED` entrypoint
- the cached mono Hybrid 48 kHz live sequence now follows the same split: ordinary `Decode(nil)` does not queue cached DRED FEC or enter the explicit DRED surface; SILK may use the ordinary lowband deep-PLC hook while CELT stays on the libopus Hybrid noise PLC branch (`start != 0`), and the stale cached-vs-explicit Hybrid oracle tests are disabled for the same reason
- the default root decoder tests now explicitly pin that lazy sidecar contract too: a good packet with only the main decoder DNN blob armed must leave the DRED sidecar nil until actual concealment/history work begins, and `Decode(nil)` is the first point allowed to materialize the sidecar on that path
- these decoder parity claims are seam-specific and libopus-backed; the current mono explicit/live decoder numerical matrix is now required in `make test-unsupported-controls-parity`, while stereo, multistream, and broader packet coverage remains separate work unless explicitly covered by green parity tests
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
- libopus-backed live decoder `Decode(nil)` resumed-good-packet handoff coverage at the current 16 kHz mono seam, with direct first-loss and second-loss neural concealment PCM checks in the required unsupported-controls parity lane
- libopus-backed explicit decoder-owned processed-DRED float coverage at the targeted 48 kHz mono CELT seam, with the current mono PCM/state matrix in the required unsupported-controls parity lane
- explicit decoder-owned processed-DRED float decode offset-boundary coverage around the libopus recovery-window transition points for that same seam is part of the required unsupported-controls parity lane
- the libopus-backed explicit decoder helper now seeds real prior decoder state from a good packet, loads separate decoder and standalone DRED model blobs, supports warmup-plus-current explicit decode lifecycles, and captures retained CELT 48 kHz bridge state (`last_frame_type`, `plc_fill`, `plc_duration`, `skip_plc`, `plc_preemphasis_mem`, `preemph_memD`, and queued PLC PCM)
- the required supported DRED tag gate now carries standalone DRED wrapper lifecycle/no-allocation, libopus parse/decode/process metadata coverage, real-packet standalone process state/feature parity, standalone recovery scheduling parity, decoder cached recovery bookkeeping parity, the SILK wideband 20/40/60 ms mono and 20 ms stereo encoder carried-payload proofs, and the Hybrid fullband 20/40 ms mono carried-payload/packet-envelope proofs plus 20/40 ms stereo carried-payload/primary-frame proofs, while the unsupported-controls gate covers the green quarantine/core and selected parity surface: API exposure, parser availability, those same encoder seams, internal converter/payload/basic-analysis seams, the real-model PitchDNN and RDOVAE encoder oracles, the conceal-analysis oracle, 48 kHz runtime bootstrap checks, and the current mono decoder explicit/live numerical matrix
- Hybrid primary-frame sizes are covered for the current stereo carried-packet seams; broader stereo/multistream decoder coverage and support-surface graduation remain open
- fallback-loss bookkeeping no longer forces the pure-Go LPCNet blend state forward when neural concealment did not actually run, but cached recovery scheduling still preserves the libopus-shaped post-loss recovery phase across recache boundaries
- encoder-side `DFrameSize` staging / rollover groundwork exists in pure Go
- encoder-side DNN admission is now stricter than simple manifest booleans: the retained encoder DNN blob must bind both the pure-Go RDOVAE encoder model family and the shared pure-Go PitchDNN analysis model family before `DREDModelLoaded()` reports ready, which gives the encoder path a real model-contract seam before latent generation and bitstream emission land
- the pure-Go RDOVAE encoder runtime now mirrors libopus `dred_rdovae_encode_dframe()` with reusable caller-owned processor state/scratch, focused zero-allocation coverage, and a real-model numerical oracle in the required unsupported-controls parity gate
- the pure-Go encoder-side same-rate 16 kHz latent generator now mirrors libopus `dred_process_frame()` for mono and stereo-downmix input by feeding two retained LPCNet single-frame feature vectors into the RDOVAE encoder runtime, so the encoder path now has real latent/state generation on the exercised seam instead of only model admission
- encoder optional-extra ownership is now tighter too: DRED controls/runtime sit behind a lazy sidecar, `SetDNNBlob(...)` only binds model families, the runtime materializes only when model+duration+eligible mono or stereo-downmix use actually arms it, disabling/resetting DRED drops that sidecar back to a dormant state, tagged builds now pin `Encode` with DRED-capable models but no DRED duration as zero-allocation and runtime-dormant, public caller-buffer encode/decode tests now also prove model-only control state skips unarmed DRED latent/payload-scan/good-packet marker work, and the internal encoder DRED runtime files are build-tag split so default `./encoder` builds use no-op stubs instead of importing `internal/dred` or `internal/lpcnetplc`
- the internal encoder path now advances the exercised 16 kHz latent generator before DTX on armed mono and stereo frames, which is closer to libopus `opus_encoder.c` ordering
- the pure-Go encoder path now also ports the libopus `dred_convert_to_16k()` front-end for `8/12/24/48 kHz` mono/stereo input with retained DF2T filter memory inside the lazy DRED sidecar instead of hard-rejecting every non-16 kHz input, and the converter has dedicated green libopus-backed parity coverage in the DRED parity lane
- the lazy encoder DRED runtime now retains libopus-shaped latent/state history plus `dred_offset` / `latent_offset` bookkeeping for later emission instead of throwing away every emitted latent after the latest frame
- the lazy encoder DRED runtime now also retains the 2.5 ms activity window plus `last_extra_dred_offset` bookkeeping that libopus uses to delay or suppress payloads around silence transitions
- the pure-Go encoder side now has a byte-for-byte libopus-backed DRED payload builder for the `dred_encode_silk_frame()` seam, including header coding, quantizer signalling, offset bookkeeping, Laplace-coded state/latent chunks, and the same delayed-out-of-silence suppression rules
- the non-CELT encoder path now has bounded pure-Go DRED extension carriers for both the single-frame seam and the repacketized SILK long-packet seam, and the carry path now follows libopus more closely by only padding those extension packets in CBR instead of padding VBR/CVBR packets unconditionally
- the single-frame CELT-only encoder path now also attaches DRED extensions in VBR/CVBR, matching libopus opus_packet_pad_impl() behavior of tucking DRED into the leftover scratch space (`orig_max_data_bytes - ret - 3`) without subtracting from CELT's bitrate budget; the CELT FB 20 ms mono carried-payload seam is now pinned against the libopus emit helper
- encoder carried DRED now reserves the libopus-shaped DRED bitrate from the primary single-frame SILK/short-Hybrid budget, with the SILK wideband 20/40/60 ms mono and 20 ms stereo carried-payload seams and Hybrid fullband 20/40 ms mono carried-payload/packet-envelope seams plus 20/40 ms stereo carried-payload/primary-frame seams pinned in the supported DRED tag gate and mirrored in the required unsupported-controls DRED parity gate
- the current Hybrid carried-packet tests cover packet length, offset, DRED payload, and primary-frame sizes on the green libopus-backed stereo seams; Hybrid stereo primary-frame byte exactness, broader Hybrid packet-shape matrices, and SILK primary-frame byte exactness still need separate libopus-backed coverage before support docs can claim them
- the unsupported-controls gate and libopus-backed DRED/PLC helper tests now explicitly bootstrap the pinned libopus source snapshot through `tools/ensure_libopus.sh` instead of assuming a preseeded tarball/cache
- the libopus-backed DRED/PLC helper builds now use stamped scalar OS/arch-specific `build-opus-dred-scalar-*` directories and x86-vector-disabling CFLAGS, including the top-level DRED packet helpers, so Linux/amd64 helper oracles do not drift just because compiler builtins route libopus through SSE/AVX DNN helpers while the pure-Go port is tracking scalar math

### DNN blob runtime coverage

Decoder-side `SetDNNBlob(...)` is intentionally a model-readiness surface, not a
full model-backed runtime arm. What it currently does:

- validates the incoming blob through `dnnblob.Clone(...)` plus
  `Blob.ValidateDecoderControl(false)` in `dnn_blob.go::cloneDecoderDNNBlobForControl`
- retains the validated `*dnnblob.Blob` on the `Decoder` and mirrors the blob's
  `DecoderModelState` into `pitchDNNLoaded`, `plcModelLoaded`, `farganModelLoaded`,
  `osceModelsLoaded`, and `osceBWEModelLoaded` (see `decoder.go` field block)
- in default `.` builds (`decoder_dred_helpers_default.go::setDNNBlob`) does NOT
  arm any neural runtime: no `lpcnetplc` Analysis/Predictor/FARGAN binding, no
  DRED sidecar materialization, no OSCE/OSCE-BWE postfilter; the flags are
  observation-only for `SupportsOptionalExtension(OptionalExtensionDNNBlob)`
  consumers
- in `-tags gopus_dred` (and `-tags gopus_unsupported_controls`) builds
  (`decoder_dred_helpers.go::setDNNBlob`) eagerly binds `lpcnetplc.Analysis`,
  `lpcnetplc.Predictor`, and `lpcnetplc.FARGAN` against the blob to surface load
  failures synchronously, then defers actual DRED neural-state allocation until
  `dredNeuralConfigEligible()` is true (mono 16 kHz or 48 kHz) and real
  concealment/history work begins via `ensureDREDNeuralConcealmentRuntime()`
- DRED runtime arming additionally requires `SetDREDDuration(...)` on the
  encoder-side recovery loop and an active DRED payload/cache or first-loss
  entry on the decoder side; `SetDNNBlob(...)` alone keeps the DRED sidecar nil
- `-tags gopus_unsupported_controls` does not add new runtime paths beyond
  `gopus_dred`; it only widens the test surface that exercises the same PLC/DRED
  code (e.g. the `unsupported-controls-parity` matrix)

Runtime paths NOT wired by `SetDNNBlob(...)` in any build:

- OSCE LACE / NoLACE postfilter consumption of `osceModelsLoaded` (the flag is
  retained but no decoder code consumes it; `decoder_dred_helpers.go` ignores it
  entirely)
- OSCE BWE upsampler consumption of `osceBWEModelLoaded` and `osceBWEEnabled`
  (no decoder runtime references either field; `SetOSCEBWE(...)` is a
  capability-only quarantine control)
- ordinary (non-DRED) FARGAN PLC: `lpcnetplc.FARGAN` is only invoked inside the
  DRED concealment path (`decoder_dred_helpers.go::generateDREDNeuralFrames16k`
  and `decoder_dred_48k.go`); there is no stand-alone FARGAN-as-PLC seam wired
  into `decoder_plc_helpers.go`
- stereo and multistream DRED/PLC neural concealment: `setDNNBlob(...)` on the
  multistream decoder is capability-only by design (no per-stream neural
  consumer yet); single-stream DRED runtime gates on `d.channels != 1` in
  `dredNeuralConcealmentAvailable()` / `dredNeuralConfigEligible()`
- non-16/48 kHz mono DRED runtime: the eligible-config gate rejects 8/12/24 kHz
  mono decoders even when the blob, duration, and recovery state are otherwise
  ready
- encoder-side `SetDNNBlob(...)` and decoder-side blob retention are wired, but
  encoder DRED latent generation still requires `SetDREDDuration(...)` plus an
  eligible mono/stereo-downmix carrier (see `encoder/dred_runtime.go`)

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
- explicit Hybrid DRED entry must lazily seed LPCNet/DRED PCM history from the retained SILK lowband tail when no raw Hybrid history hook has already run; waking the sidecar on ordinary good packets just because a DNN blob is loaded violates the zero-cost optional-feature contract, while skipping this lazy seed leaves FARGAN continuity and the next good packet far from libopus
- SILK stereo `bitRate` is the total stream rate, not per-channel: `silk/enc_API.c` splits it inside `silk_stereo_LR_to_MS` into `MStargetRates_bps[0/1]`, mirroring `encoder/encoder.go` stereo SILK setup at `totalSilkRate := e.silkInputBitrate(frameSize)`; the `/ e.channels` divide on the mono branch at `encoder/encoder.go:2175` is a no-op (only reached when `e.channels == 1`) and is not the source of stereo 40/60 ms DRED carrier divergence — investigate primary-frame budgeting / `maybeBuildSingleFrameDREDPacket` carrier sizing instead
- SILK stereo 40 ms DRED carrier under-fill is upstream of the DRED carrier path: at 40 kbps WB without DRED, gopus stereo 40 ms SILK packets are ~83-113 B while gopus mono packets at the same rate are ~115-153 B; libopus stereo at the same setup emits ~120-160 B, larger than mono since stereo carries both mid+side SILK frames. The stereo SILK encoder in `silk/silk_encode.go` inlines LBRR header + flag encoding but never emits the per-frame LBRR indices/pulses block that libopus `silk_Encode` writes for each channel with `lbrrFlags[i]==1` (`silk/enc_API.c` lines 376-400); with `SetPacketLoss(20)` enabled libopus carries that side LBRR payload and gopus does not, which accounts for the entire stereo 40 ms WB 40 kbps packet gap. The mid/side rate split (`stereoAllocationTargetRate`, `StereoLRToMSWithRates`) already mirrors libopus. Closing stereo 40/60 ms DRED carrier length parity therefore requires wiring `encodeLBRRData(..., false)` (or the equivalent indices/pulses tail) into both mid and side channels in `silk/silk_encode.go` around lines 212-241, not a change in `maybeBuildSingleFrameDREDPacket` or `hybridDREDPrimaryBudget`. Mono SILK 40/60 ms DRED carrier parity already matches libopus byte-for-byte.
- every remaining `probeLibopusDecoderDREDDecodeFloat` caller in `decoder_dred_decode_float_libopus_parity_test.go` lives under a `TestDecoderExplicit*MatchesLibopus` test that drives the gopus side through `dec.decodeExplicitDREDFloat(...)` (the `FRAME_DRED` entrypoint), so swapping those to `probeLibopusDecoderDREDSequence` would change which libopus seam is probed — `FRAME_DRED` vs `FRAME_PLC_NEURAL` — rather than just swapping oracles, and the broader live-oracle adoption gap belongs on stereo/multistream/wider-packet cached coverage instead of those explicit-API tests
- Hybrid stereo 20 ms DRED primary-frame byte divergence is **not a CELT-half problem** despite the label: the gopus 65-byte primary frame for `TestEncoderCarriedDREDPayloadMatchesLibopusHybridFullband20msStereo` diverges from libopus at byte 0 itself. The top 4 bits of byte 0 are the patched stereo SILK header `[VADmid, LBRRmid, VADside, LBRRside]` (see `encoder/hybrid.go:1319` `re.PatchInitialBits(...)`); gopus emits `vadMid=0` while libopus emits `vadMid=1`, and all 63 subsequent bytes diverge transitively through the shared range coder. The CELT-side encoding logic is **not** the first divergence point — investigate the Mid-channel SILK VAD path instead: `encoder/hybrid.go:1232` `computeSilkVAD(mid, ...)`, the `applyOpusVADToSilkState` clamp at `encoder/encoder.go:2791` (uses `e.lastOpusVADActive` from `updateOpusVAD()` through the gopus tonality analyzer), and/or the float-input `mid` signal that comes out of `StereoLRToMSWithRates` versus libopus's int16 post-resampler `state_Fxx[0].inputBuf` feeding `silk_VAD_GetSA_Q8`.
- Root cause for the stereo Mid VAD divergence (above) is **NOT** the VAD input alignment: `silk.Encoder.StereoLRToMSWithRates` (`silk/stereo_encode.go:706-708`) already produces a `mid` array semantically identical to libopus `state_Fxx[0].inputBuf+1`, and SILK VAD itself returns `silkAQ8=255` (max active) on the divergent frame. The actual bug is in `encoder/encoder.go::updateOpusVAD` (line 2660): it calls `isDigitalSilence(pcm, e.lsbDepth)` on the caller-provided `float64` slice, but in the **LSBDepth=24 float32 fast path** that `pcm64` scratch buffer is sliced from `e.scratchPCM64` and never populated (the inner encoder operates on `floatInputFrame` directly per `encoder_encode.go:20-25`). So `isDigitalSilence` sees all-zeros, classifies as silence, and forces `lastOpusVADActive=false` for every frame — including strong music signals. The `applyOpusVADToSilkState` clamp at `encoder/encoder.go:2797` then forces `vadMid=false` despite `silkAQ8=255`. Commit `e568c9c9` introduced `currentInputIsDigitalSilence` (`encoder/encoder.go:2751`) that prefers `e.floatInputFrame` when available, but applied it **only** to the restricted-SILK path; `updateOpusVAD`, `shouldUseDTX` (`encoder/dtx.go:145`), and `currentDREDActivity` (`encoder/dred_payload.go:18`) all have the identical bug. Extending the `currentInputIsDigitalSilence` pattern to those three call-sites fixes byte 0 of the Hybrid stereo 20 ms primary frame (matches libopus `88 02` exactly), but exposes **3 secondary divergences** previously masked by the broken Opus VAD path: `Hybrid20ms` mono 103 vs 102 (off by 1), `Hybrid20msStereo` 100 vs 98 (off by 2), `Hybrid40msStereo` 152 vs 147 (off by 5). These secondary divergences are independent gopus↔libopus drifts (likely SILK signal-type-dependent index/pulse coding, CELT rate allocation under active VAD, or DRED queue/bitrate scheduling) that need separate chases before the VAD-fix can land cleanly.
- CELT NB DRED is not a parity item: `libopusDREDBandwidthEnv` in `dred_libopus_packet_helpers_test.go` only maps `wb`/`swb`/`fb` (rejecting `BandwidthNarrowband` with an error), and the C helper `tools/csrc/libopus_dred_emit_packet.c` `parse_bandwidth_env` accepts only `wb`/`swb`/`fb` strings, mirroring that libopus does not emit DRED for NB packets in standard usage since CELT operates at SWB/FB and NB is SILK-only territory
- libopus does emit DRED extensions on SILK-only mode packets too: the `tools/csrc/libopus_dred_emit_packet.c` helper produces a valid 48 kHz SILK WB 20 ms DRED-bearing packet (`packetLen=81`, `maxDREDSamples=960`) on `GOPUS_DRED_FORCE_MODE=silk`, and libopus `opus_decoder_dred_decode_float()` returns the full `ret=960` PCM frame from that packet (probe in `TestProbeDecoderExplicitSILKDRED` under `gopus_unsupported_controls`). The gopus explicit DRED path also returns `ret=960` without error, but the resulting PCM is essentially silent (~`1e-6` magnitude) while libopus produces a normal voiced waveform (~`0.15` peak amplitude). The decoder gap is therefore not a hard rejection but a missing entry-history seed: `decoder_dred_explicit_unsupported.go::decodeExplicitDREDFloat` only branches into the Hybrid lowband-history priming path when `d.prevMode == ModeHybrid`, so a SILK-only prior packet enters explicit `FRAME_DRED` concealment with empty LPCNet/FARGAN history. Closing SILK explicit DRED parity at 48 kHz WB will likely need an analogous `primeExplicitSILKDREDEntryHistory(...)` that lazily seeds the LPCNet/FARGAN history from the retained SILK lowband tail before queueing the DRED features, mirroring the Hybrid path. Until that seed lands, do not add a full `SILK WB 20/40/60 ms` decoder explicit DRED matrix — the probe will look like the decoder is producing silence rather than a small numerical drift.

Still missing for full parity:

- decoder-level parity beyond the current mono seams after CELT + Hybrid 48 kHz mono and the exercised 16 kHz mono seam, especially stereo/multistream coverage, broader packet coverage, and the final supported-surface decisions for what graduates from quarantine
- `SetDNNBlob(...)` only validates the blob and retains the model-loaded flags (`pitchDNNLoaded`, `plcModelLoaded`, `farganModelLoaded`, `osceModelsLoaded`, `osceBWEModelLoaded`) plus, in tag-gated builds, the eligible-mono DRED neural-runtime arming inside `ensureDREDNeuralConcealmentRuntime()`; OSCE LACE/NoLACE postfilter, OSCE BWE upsampler, and any FARGAN-as-ordinary-PLC seam stay un-wired in every build, and stereo/multistream/non-16-or-48 kHz mono DRED neural concealment remains gated off independently of blob readiness
- model-backed `opus_decoder_dred_decode*()` parity beyond the currently exercised mono explicit seams, including broader `LPCNetEncState`-shaped analysis/runtime coverage and decoder-owned integration for any surfaces that graduate from quarantine
- broader live-oracle adoption beyond the covered mono cached seams; some cached/live tests still compare against the explicit `opus_decoder_dred_decode_float()` helper, and stereo/multistream paths plus wider packet matrices still need migration to live-sequence coverage where those surfaces become supported
- encoder-side DRED beyond the current exercised latent-generation and carried-payload seams: broadening rate / packet-shape / stereo / multistream coverage around the payload-emission path and finalizing which surfaces graduate from quarantine
- clean runtime re-verification after the current macOS AppleSystemPolicy issue: freshly built local Go test binaries on this machine are being rejected by Gatekeeper (`spctl --assess --type execute` rejects them, `syspolicyd` logs repeated `Unable to initialize qtn_proc: 3`, and the processes stall at `_dyld_start`), so CI remains the reliable runtime oracle while that host-policy issue is open; see [PLATFORM_NOTES.md](PLATFORM_NOTES.md#macos-local-test-binary-quarantine) for documented local workarounds
- CELT-mode DRED attachment now works in gopus too: removed the `actualMode != ModeCELT` short-circuit in `encoder/encoder.go:743` (DRED plan computation) and `encoder/dred_plan.go:214,275` (`maybeBuildSingleFrameDREDPacket` / `maybeBuildMultiFrameDREDPacket`); kept the SILK/Hybrid-only `encodingBitrate -= dredPlan.bitrate` reservation (CELT keeps its full primary bitrate, matching libopus VBR-with-DRED). Verified byte-exact against libopus for CELT FB 20 ms mono at 40 kbps via `TestEncoderCarriedDREDPayloadMatchesLibopusCELTFullband20ms`

### Stereo DRED runtime port (scope)

Decoder-side DRED neural concealment is mono-only today. The single-stream
decoder runtime hard-bails out of every neural path the instant `d.channels != 1`,
so a stereo (`channels == 2`) decoder with a DRED-armed DNN blob silently falls
back to legacy non-neural PLC even when there is a valid DRED payload on the
wire. This sub-section maps the bail sites and pins the libopus-shaped strategy
so the port can land incrementally instead of as one large stereo rewrite.

Current decoder-side bail sites that gate on `d.channels != 1`
(`gopus_dred || gopus_unsupported_controls` build tag):

- `decoder_dred_48k.go:9` — `applyDREDNeuralConcealment48kMono(pcm, samplesPerChannel)`:
  cached/explicit DRED entry into the 48 kHz CELT neural concealment seam. Used
  by every `decodeExplicitDREDFloat()` path and indirectly by
  `applyDREDNeuralConcealment()` at `decoder_dred_helpers.go:870`.
- `decoder_dred_48k.go:51` — `applyPLCNeuralConcealment48kMono(pcm, samplesPerChannel)`:
  cached/live `Decode(nil)` `FRAME_PLC_NEURAL` entry into the 48 kHz CELT neural
  concealment seam. Used by `applyDREDNeuralConcealment()` at
  `decoder_dred_helpers.go:885`.
- `decoder_dred_helpers.go:260` — `dredNeuralConfigEligible()`: gates the entire
  sidecar arming surface (`dredRuntimeSampleRate`, `setDNNBlob`, neural model
  admission). With `channels != 1` the decoder never marks DRED as eligible,
  so model loading silently degrades to control-only state.
- `decoder_dred_helpers.go:465` — `dredNeedsCELTFloatPath()`: tells the CELT
  decoder to stay on the float path while neural concealment is in flight.
  With stereo bailed, retained neural state in the bridge never forces the
  float path back in.
- `decoder_dred_helpers.go:631` — `beginHybridDREDLowbandHook()`: installs the
  SILK lowband deep-PLC mono hook that lets cached DRED features advance
  through the Hybrid loss path.
- `decoder_dred_helpers.go:727` — `beginDREDRawMonoGoodFrameCapture()`: installs
  the SILK raw mono frame hook used to seed retained DRED history on good
  packets in Hybrid/SILK modes.
- `decoder_dred_helpers.go:753` — `refreshDREDHistoryFromHybridDecoder()`:
  fills the retained 16 kHz DRED PLC update buffer from the SILK decoder's
  mono out-buffer tail.
- `decoder_dred_helpers.go:812` — `prepareDRED48kNeuralEntry()`: primes the
  48 kHz CELT-to-LPCNet bridge before the first neural concealment step.
- `decoder_dred_helpers.go:902` — `advanceHybridDREDLowbandState()`: advances
  the Hybrid lowband deep-PLC bridge after a concealed Hybrid loss.
- `decoder_dred_helpers.go:945` — `markDREDUpdatedPCM()`'s SILK branch only
  records 16 kHz DRED PCM history when `d.channels == 1`. Stereo good packets
  on SILK never update the retained DRED PCM history.

Ancillary surfaces that do not hard-bail on `channels != 1` but that the
current mono runtime implicitly depends on:

- `decoder_dred_explicit_unsupported.go:64` — `decodeExplicitDREDFloat()`
  allocates a `needed = frameSizeSamples * d.channels` slice, so it already
  shapes its caller buffer for stereo, but it then hands that buffer to
  `applyDREDNeuralConcealment48kMono()` which only writes mono.
- `decoder_dred_explicit_unsupported.go:107` —
  `decodeExplicitHybridDREDFloat()` calls `silkDecoder.SnapshotDeepPLCLowbandMono()`,
  which is a mono helper today.
- `decoder_decode.go:46,71` and `decoder_fec.go:47` already call
  `applyDREDNeuralConcealment()` with `pcm[:frameSize*d.channels]`, so the
  caller-buffer side is already stereo-shaped; the bail is purely in the
  callee.

What libopus does for stereo DRED (pinned reference: `tmp_check/opus-1.6.1`):

- `struct OpusDecoder` has a single `LPCNetPLCState lpcnet` field regardless
  of channel count (`src/opus_decoder.c:65-77`). There is no per-channel
  LPCNet/DRED state in libopus.
- `silk_Decode()` only attaches the `lpcnet` state to channel 0 of its
  internal stereo loop (`silk/dec_API.c:357`, `n == 0 ? lpcnet : NULL`). The
  deep-PLC predictor therefore always runs on the mid/mono SILK signal, and
  stereo recovery happens through the ordinary `silk_stereo_MS_to_LR()`
  upmix that follows on the next good packet.
- `update_plc_state()` in `celt/celt_decoder.c:639-672` explicitly mixes
  both decode memories down to mono before feeding the LPCNet PLC update
  (`0.5*(decode_mem[0][i] + decode_mem[1][i])`), and `celt_decode_lost()`
  in the same file at lines 1066-1067 has the canonical libopus comment:
  `/* For now, we just do mono PLC. */ if (C==2) OPUS_COPY(decode_mem[1],
  decode_mem[0], decode_buffer_size+overlap);` — i.e. libopus generates one
  mono concealed buffer and duplicates it into both channels.
- `lpcnet_plc_fec_add()` / `lpcnet_plc_fec_clear()` in
  `src/opus_decoder.c:741-754` operate on the single `st->lpcnet` instance
  regardless of `st->channels`. Cached DRED features are queued once, not
  per channel.

So the libopus DRED neural runtime is **fundamentally mono**: the retained
LPCNet PLC / FARGAN / DRED-feature state is one mono pipeline, and stereo
output is produced by mono-downmix on entry and mono-duplication on exit.
There is no separate per-channel DRED state to maintain.

Suggested phased port:

- Phase A — keep the runtime mono, only refactor the public surface so
  `applyDREDNeuralConcealment48kMono()` and `applyPLCNeuralConcealment48kMono()`
  no longer EARLY-RETURN on stereo. They should instead route stereo through
  a clearly-marked `// TODO: stereo DRED — mirror libopus mono-downmix +
  duplicate-to-both-channels` code path that still returns `false` (so callers
  fall back to non-neural PLC) but documents the intended libopus-shaped fix
  in-place. This is a no-behavior-change refactor: the bail moves from
  hard-return at function entry to an explicit unimplemented branch with the
  libopus reference inline. Do not lift the gate on `dredNeuralConfigEligible()`
  yet, because the sidecar arming still needs the eligibility check to stay
  zero-cost when stereo DRED is not supported.
- Phase B — implement stereo entry into the existing mono LPCNet/FARGAN
  pipeline by mono-downmixing the retained CELT decode memory (mirroring
  `update_plc_state()` at `celt/celt_decoder.c:646-651`) before
  `primeDREDCELTEntryHistory()` / `refreshDREDHistoryFromHybridDecoder()`
  run. The downmix needs to live in the CELT bridge (`celtDecoder.FillPLCUpdate16kMonoWithPreemphasisMem`)
  rather than in the DRED helpers, so the SILK mid-only path stays unchanged.
  This makes the `markDREDUpdatedPCM()` SILK gate at `decoder_dred_helpers.go:945`
  trivial — SILK already runs the deep-PLC predictor on the mid channel, so
  stereo SILK can record retained DRED PCM history from the mid channel
  without per-channel state.
- Phase C — implement stereo exit by mono-duplicating the concealed 48 kHz
  PCM into both output channels (mirroring `celt/celt_decoder.c:1066-1067`).
  This is the only step that touches the caller PCM layout: today the mono
  apply helpers write `pcm[:samplesPerChannel]`; the stereo wrapper has to
  write `pcm[:samplesPerChannel*2]` with interleaved L/R copies of the same
  mono concealed buffer. The output-gain pass at
  `decoder_dred_explicit_unsupported.go:80,91` already iterates per-channel
  so it does not need changes.
- Phase D — extend the Hybrid SILK lowband hooks
  (`beginHybridDREDLowbandHook`, `advanceHybridDREDLowbandState`,
  `beginDREDRawMonoGoodFrameCapture`, `refreshDREDHistoryFromHybridDecoder`)
  to the stereo mid-channel SILK frame. Like the libopus split, the lpcnet
  hook only runs on `n == 0`, so the work is "convince the SILK stereo
  decoder to expose the mid lowband / raw mono tail through the existing
  mono hook API" rather than "build a parallel stereo hook stack". The
  `silk.DeepPLCLowbandSnapshot` type already exists as a mono snapshot; the
  stereo extension is upstream in `silk/`, not in the DRED helpers.
- Phase E — add stereo-specific parity tests (live-sequence carrier good /
  first loss / resumed good packet, both CELT-only 48 kHz and Hybrid
  SWB/FB, plus the SILK wideband stereo 20 ms seam) against libopus before
  graduating the surface from `gopus_unsupported_controls` quarantine into
  the `gopus_dred` supported tag gate.

Acceptance milestones:

- Phase A landing leaves all existing mono parity tests green and adds an
  explicit "stereo DRED not yet implemented" assertion test that pins the
  fall-through-to-non-neural-PLC behavior in the quarantine lane.
- Phase B + C landing has direct stereo-vs-libopus PCM parity on the
  48 kHz CELT live-sequence first-loss / second-loss / resumed-good-packet
  seam, with the retained CELT bridge state checked the same way the mono
  matrix already pins it.
- Phase D landing extends that coverage to the Hybrid SWB/FB and SILK
  stereo seams and to retained SILK lowband bridge state.
- Phase E landing is the support-surface decision: only after all of the
  above are green in `make test-unsupported-controls-parity` can stereo
  DRED move from quarantine into the supported `gopus_dred` tag gate.

Out of scope for this port:

- multistream decoder DRED, which still keeps standalone DRED on a
  cache/timing-only path (see the existing "multistream optional-state
  hardening" entry above). Multistream stereo DRED is a separate workstream.
- 16 kHz mono Hybrid DRED concealment currently produces near-zero PCM (probe values in the `1e-4` to `1e-6` range while libopus delivers values around `0.05` to `0.16` on the same packet), so the low-rate Hybrid loss output is effectively silence. Probe localization: `decoder_decode.go` `Decode(nil)` routes 16 kHz mono `Decode(nil)` through `applyDREDNeuralConcealment`, which in `decoder_dred_helpers.go` (`applyDREDNeuralConcealment` around lines 870-894 and `advanceHybridDREDLowbandState` at lines 896-925) is currently gated to `d.sampleRate == 48000` for the Hybrid SILK lowband stitching path. At 16 kHz the helper falls through to `applyPLCNeuralConcealment48kMono` / `concealNeural48kMono` (`decoder_dred_48k.go` plus `celt/dred_conceal.go`), which generates a CELT-only neural waveform at 48 kHz frame-size domain and skips the libopus-shaped SILK 0-8 kHz reconstruction the Hybrid lost frame needs. `decoder_dred_explicit_unsupported.go` `decodeExplicitDREDFloat` shows the same shape: the 48 kHz branch (lines 71-85) routes Hybrid through the dedicated `decodeExplicitHybridDREDFloat` helper that snapshots the SILK lowband, runs the libopus-shaped Hybrid PLC base, and then advances `advanceHybridDREDLowbandState`, while the 16 kHz branch (lines 86-95) does none of that and only invokes the CELT-domain `applyDREDNeuralConcealment48kMono`. Suggested fix area: port the 48 kHz `decodeExplicitHybridDREDFloat` / `beginHybridDREDLowbandHook` / `advanceHybridDREDLowbandState` lowband-stitching path to the 16 kHz seam (relaxing the `d.sampleRate != 48000` gates and matching the libopus `opus_decode_frame` Hybrid SILK + CELT split that runs at `st->Fs` with `celt_decode_with_ec_dred` + `st->downsample` in `tmp_check/opus-1.6.1/src/opus_decoder.c` and `celt/celt_decoder.c`), and add a 16 kHz mono Hybrid DRED parity test mirroring `TestDecoderExplicitHybridDREDDecodeMatrixMatchesLibopus` but built on top of `prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, ...)` so the regression is pinned before claiming low-rate Hybrid DRED parity.

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
- the current pure-Go encoder seam now covers retained `8/12/16/24/48 kHz` mono and stereo-downmix conversion into the 16 kHz latent/state path, the libopus `dred_encode_silk_frame()` payload-coding seam, bounded single-frame carried extensions, exercised SILK carried-payload seams, Hybrid fullband 20/40 ms mono carried-extension payload bytes plus envelope sizes, and Hybrid fullband 20/40 ms stereo carried-extension payload bytes plus primary-frame sizes, while broader packet-shape coverage, primary-frame byte exactness, and final support-surface decisions remain open

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
- the current 16 kHz mono cached/live seam has required resumed-good-packet handoff PCM coverage after first and repeated loss across the exercised frame-size matrix, and direct concealment PCM plus explicit matrix checks are now in the required unsupported-controls parity lane for the current mono seams
- the 48 kHz mono CELT `FRAME_DRED` bridge and the 48 kHz mono Hybrid SWB/FB first-loss/handoff seams are covered by the required explicit/live decoder audio matrix, with Hybrid now following the libopus-shaped Hybrid PLC base; the next decoder gap is broader repeated-loss, packet, stereo, and multistream coverage
- explicit 48 kHz DRED now drives the dedicated `FRAME_DRED` bridge instead of accidentally reusing the live `FRAME_PLC_NEURAL` bridge, and the exercised explicit decoder matrix is now required in `make test-unsupported-controls-parity`

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
