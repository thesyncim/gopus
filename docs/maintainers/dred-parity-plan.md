# DRED Parity Plan

Last updated: 2026-05-18

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
- the single-stream decoder now seeds first-loss neural concealment from that CELT bridge, retains a decoder-owned cached-DRED recovery cursor across consecutive losses, and keeps focused tag-gated live-decoder regression coverage around that path; broader stereo packet/mode matrices and multistream modes beyond the green 48 kHz CELT elementary-stream seam stay out of the supported surface
- the pure-Go full first-loss LPCNet analysis path now has its own libopus-backed oracle, so `GenerateConcealedFrameFloatWithAnalysis(...)` is pinned against libopus for concealed PCM plus retained PLC/FARGAN/analysis state from the same starting queue/history inputs
- the single-stream decoder also now has an explicit processed-DRED PCM recovery helper for the targeted 48 kHz mono CELT seam, with required unsupported-controls parity coverage against cached/live loss, resumed-good-packet handoff, libopus `opus_decoder_dred_decode_float()` on a real packet, feature-window offset boundaries, and the broader 48 kHz mono CELT frame-size matrix (`2.5 ms`, `5 ms`, `10 ms`, `20 ms`), including retained PLC/FARGAN runtime state after explicit decode
- the same 48 kHz mono decoder DRED runtime now also has required unsupported-controls Hybrid SWB/FB carrier coverage on explicit/live first-loss and resumed-good-packet handoff seams, and the current pure-Go Hybrid recovery now follows the libopus-shaped Hybrid PLC base instead of the earlier CELT-only replacement path across the exercised `10 ms` and `20 ms` handoff seams; stereo/multistream Hybrid decoder coverage remains separate work
- the current 16 kHz mono decoder seam now keeps CELT PLC chunking in the same 48 kHz internal frame-size domain as the rest of the decoder core, so low-rate cached recovery no longer feeds invalid `320`-sample CELT PLC requests into first-loss decode
- the current 16 kHz mono decoder seam has required libopus-backed coverage for resumed-good-packet handoff after first and second loss plus targeted cached/explicit runtime coverage, and the decoder now explicitly primes the CELT-to-LPCNet entry bridge on first neural/DRED concealment for both cached-live and explicit 16 kHz paths
- the 16 kHz decoder oracle helpers now use the decoder API sample rate for libopus probes instead of the carrier packet's emitted 48 kHz rate, which avoids false explicit/libopus and explicit/cached drift on the low-rate seams while keeping the internal 48 kHz concealment frame-size domain intact
- the current 16 kHz mono decoder entry bridge now follows libopus `update_plc_state()` more closely by reseeding retained PLC history without pre-replaying LPCNet analysis before first-loss concealment
- the current 16 kHz mono cached/live sequence seam now also has required libopus-backed frame-size matrix coverage for resumed-good-packet handoffs after first and second loss across the exercised `10 ms` and `20 ms` carrier sizes, with direct first-loss/second-loss PCM checks in the required unsupported-controls parity lane instead of relying on a single default carrier size
- decoder-side DRED request/recovery bookkeeping now explicitly tracks the exercised 16 kHz mono seam in the same internal 48 kHz frame-size domain the main decode/PLC path already uses, and the 16 kHz decoder parity oracles now probe libopus in that same runtime domain instead of mixing the public config rate with 48 kHz-sized concealment requests
- the 16 kHz explicit decoder matrix now also treats retained CELT `FRAME_DRED` bridge state as first-class parity surface across explicit/libopus, explicit/cached, second-loss, follow-up packet, and frame-size checks instead of only checking PCM/PLC/FARGAN state; the required mono matrix is now in `make test-unsupported-controls-parity`
- explicit-vs-cached equality is no longer a truthful live decoder gate: libopus uses `FRAME_PLC_NEURAL` on ordinary cached `Decode(nil)` loss recovery and `FRAME_DRED` only on the explicit DRED API, so the real decoder split is explicit-vs-libopus plus live-vs-live-sequence rather than explicit-vs-cached sameness
- the 16 kHz explicit-vs-cached follow-up seam is now exercised both after a single DRED loss and across the 16 kHz follow-up frame-size matrix in the required unsupported-controls parity gate
- the 48 kHz mono explicit-vs-cached follow-up seam is now exercised after a single DRED loss and across the first-loss follow-up frame-size matrix too in the required unsupported-controls parity gate
- the cached mono CELT 48 kHz decoder seam now also has direct first-loss and resumed-good-packet oracle coverage against the live-sequence helper, with explicit-helper comparisons kept as experimental coverage instead of being used as the required support claim
- the cached mono CELT 48 kHz decoder seam now also has direct second-loss and second-loss-to-next-packet oracle coverage against the live-sequence helper, with explicit-helper comparisons kept as experimental coverage while the cached/live mono matrix moves closer to the Hybrid matrix
- the cached mono CELT 48 kHz decoder seam now also has direct live-sequence-only first-loss and second-loss oracle coverage without requiring a resumed good packet, so the steady cached/live loss path no longer has to lean on the explicit DRED helper as its primary oracle
- the cached mono Hybrid SWB/FB decoder seam now also has direct live-sequence-only first-loss and broader second-loss oracle coverage in the required unsupported-controls parity lane, so the cached Hybrid loss path is less dependent on `opus_decoder_dred_decode_float()` as a proxy for `opus_decode_float(NULL, ...)`
- cached recovery-cursor coverage now spans the old 16 kHz seam plus 48 kHz mono CELT advancement/reset behavior, while the 48 kHz mono Hybrid live-sequence seam now pins `dredRecovery` idle because ordinary cached `Decode(nil)` does not queue DRED recovery on the libopus Hybrid path
- libopus-backed cached recovery-window lifecycle checks now also span 48 kHz mono CELT and 48 kHz mono Hybrid, so queued-feature windowing is no longer only cross-checked on the low-rate seam
- dormant-cost hardening now keeps the single-stream decoder's optional DRED runtime behind a lazy sidecar split into payload/cache, recovery queue state, neural runtime, and 48 kHz bridge state: plain decode and core-only decoder blobs keep the sidecar nil, combined core+DRED decoder blobs arm the payload/parser state, and the neural runtime still materializes only on first real concealment/history use on the eligible 16 kHz / 48 kHz seams
- resumed good packets on the single-stream DRED path now follow libopus `PLC_SKIP_UPDATES` more closely by clearing only the retained LPCNet blend flag instead of rewriting PLC history/cursors on every successful decode after concealment
- `Decoder.Reset()` now drops activated single-stream DRED recovery, neural, and 48 kHz bridge runtime back to the dormant control-only state while retaining validated model blobs, so optional extras return to their pre-activation zero-cost posture on a new stream
- the standalone single-stream DRED payload buffer is now allocated lazily on first cached DRED payload instead of at standalone model arm time
- the top-level decoder DRED internals are now build-tag split too, so default `.` builds avoid importing `internal/dred`, RDOVAE, or LPCNet PLC internals while retaining DNN blob readiness flags through no-op default runtime stubs
- multistream optional-state hardening keeps the DRED sidecar build-tag split so default `./multistream` builds avoid importing `internal/dred`, RDOVAE, or LPCNet PLC internals; tagged builds now let a combined core+DRED decoder blob arm RDOVAE and consume cached DRED on uncoupled mono, single-coupled stereo, and non-leading second-coupled CELT/Hybrid/SILK consumer seams while broader packet/mode matrices remain seam-specific
- the current 48 kHz Hybrid DRED path now updates retained CELT waveform/cadence state with a state-only pure-Go DRED concealment pass instead of only relabeling an ordinary Hybrid PLC loss after the fact; this pins the next-good-packet handoff parity that remained after the earlier cadence-only sync work
- a new internal libopus decoder-sequence helper now exists for cached/live DRED decoder parity, covering `carrier good -> first loss -> second loss -> optional next good packet` via `opus_decode_native(..., dred, dred_offset)` instead of only the public explicit `opus_decoder_dred_decode_float()` seam
- that live-sequence helper now drives float recovery steps through `opus_decoder_dred_decode_float()` itself, so the cached/live float oracle matches the same public libopus DRED entrypoint the explicit decoder parity tests already use
- the first decoder tests now use that sequence helper for live/cached handoff parity on the 16 kHz mono seam, including its exercised frame-size matrix, and on the cached Hybrid SWB/FB next-good-packet seam, so those paths are no longer limited to explicit-oracle comparisons only
- the required unsupported-controls parity lane now also selects those live-sequence handoff tests, so CI/local sweeps surface real cached/live decoder deltas instead of silently carrying unrun oracle coverage
- the unsupported-controls root-package allowlist now also matches the renamed cached/live decoder parity tests that still use the explicit DRED oracle, so CI no longer silently drops those seams after `...MatchesLibopus` -> `...MatchesExplicitDREDOracle` renames
- decoder ownership tests now treat the core-only decoder DNN path as a lazy zero-cost sidecar: `SetDNNBlob(...)` keeps model readiness/validation without waking DRED payload scanning unless the blob also carries the DRED decoder family
- ordinary good-packet decode no longer wakes `decoderDREDRecoveryState` just because the main decoder DNN blob is armed; first-loss entry now relies on the CELT-to-LPCNet bridge or existing recovery state instead of materializing recovery bookkeeping on the first normal packet
- ordinary cached/live `Decode(nil)` loss no longer reuses the explicit `FRAME_DRED` path just because cached DRED payload is present; the live path now stays on the libopus-shaped `FRAME_PLC_NEURAL` seam and only advances cached DRED recovery/blend bookkeeping after successful concealment
- the cached mono CELT 48 kHz live sequence now also avoids pre-priming LPCNet analysis before first loss, matching libopus `update_plc_state()` seeding followed by analysis inside `lpcnet_plc_conceal()`; the stale cached-vs-explicit CELT oracle tests are disabled because explicit DRED remains a separate `FRAME_DRED` entrypoint
- the cached mono Hybrid 48 kHz live sequence now follows the same split: ordinary `Decode(nil)` does not queue cached DRED FEC or enter the explicit DRED surface; SILK may use the ordinary lowband deep-PLC hook while CELT stays on the libopus Hybrid noise PLC branch (`start != 0`), and the stale cached-vs-explicit Hybrid oracle tests are disabled for the same reason
- the root decoder tests now explicitly pin that split too: core-only decoder blobs keep the DRED sidecar nil across good-packet decode, while combined core+DRED blobs arm the parser and cache carried DRED payloads
- these decoder parity claims are seam-specific and libopus-backed; the required mono explicit/live decoder numerical matrix plus selected 16 kHz Hybrid mono live-sequence seams, CELT/Hybrid stereo cached/live first/second-loss and next-packet handoff matrices, selected 16 kHz CELT/Hybrid stereo explicit first-loss probes, explicit first-loss and recovery lifecycle/cursor seams, the 48 kHz SILK WB explicit stereo first-loss seam, and uncoupled mono, single-coupled stereo, and non-leading second-coupled multistream CELT/Hybrid/SILK DRED consumers are now required in the DRED gates, while broader SILK stereo packet/mode matrices, broader multistream packet/mode coverage, and packet coverage remain separate work unless explicitly covered by green parity tests
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
- libopus-backed explicit decoder-owned processed-DRED float coverage at the targeted 48 kHz mono and selected 48 kHz stereo CELT seams, with the required mono PCM/state matrix and selected stereo first-loss seam in the required unsupported-controls parity lane
- explicit decoder-owned processed-DRED float decode offset-boundary coverage around the libopus recovery-window transition points for that same seam is part of the required unsupported-controls parity lane
- the libopus-backed explicit decoder helper now seeds real prior decoder state from a good packet, loads separate decoder and standalone DRED model blobs, supports warmup-plus-current explicit decode lifecycles, and captures retained CELT 48 kHz bridge state (`last_frame_type`, `plc_fill`, `plc_duration`, `skip_plc`, `plc_preemphasis_mem`, `preemph_memD`, and queued PLC PCM)
- the required supported DRED tag gate now carries standalone DRED wrapper lifecycle/no-allocation, libopus parse/decode/process metadata coverage, real-packet standalone process state/feature parity, standalone recovery scheduling parity, decoder cached recovery bookkeeping parity, the SILK wideband 20/40/60 ms mono and 20 ms stereo encoder carried-payload proofs, Hybrid fullband 20/40 ms mono and stereo carried-payload/packet-envelope proofs, and uncoupled mono, single-coupled stereo, and non-leading second-coupled multistream CELT/Hybrid/SILK consumers, while the unsupported-controls gate covers the green quarantine/core and selected parity surface: API exposure, parser availability, those same encoder seams, internal converter/payload/basic-analysis seams, the real-model PitchDNN and RDOVAE encoder oracles, the conceal-analysis oracle, OSCE BWE/LACE numerical forward-pass contracts, 48 kHz runtime bootstrap checks, the required mono decoder explicit/live numerical matrix, selected 16 kHz Hybrid mono live-sequence seams, CELT/Hybrid stereo cached/live first/second-loss and next-packet handoff matrices, selected 16 kHz CELT/Hybrid stereo explicit first-loss probes, explicit first-loss and recovery lifecycle/cursor seams, and the 48 kHz SILK WB explicit stereo first-loss seam
- Hybrid fullband 20/40 ms mono and stereo packet-envelope exactness is required in both DRED parity gates; Hybrid/SILK stereo primary-frame byte exactness, broader SILK stereo packet/mode matrices, broader multistream packet/mode coverage, and support-surface graduation remain separate work unless a green libopus-backed seam explicitly claims them
- fallback-loss bookkeeping no longer forces the pure-Go LPCNet blend state forward when neural concealment did not actually run, but cached recovery scheduling still preserves the libopus-shaped post-loss recovery phase across recache boundaries
- encoder-side `DFrameSize` staging / rollover groundwork exists in pure Go
- encoder-side DNN admission is now stricter than simple manifest booleans: the retained encoder DNN blob must bind both the pure-Go RDOVAE encoder model family and the shared pure-Go PitchDNN analysis model family before `DREDModelLoaded()` reports ready, which gives the encoder path a real model-contract seam before latent generation and bitstream emission land
- the pure-Go RDOVAE encoder runtime now mirrors libopus `dred_rdovae_encode_dframe()` with reusable caller-owned processor state/scratch, focused zero-allocation coverage, and a real-model numerical oracle in the required unsupported-controls parity gate
- the pure-Go encoder-side same-rate 16 kHz latent generator now mirrors libopus `dred_process_frame()` for mono and stereo-downmix input by feeding two retained LPCNet single-frame feature vectors into the RDOVAE encoder runtime, so the encoder path now has real latent/state generation on the exercised seam instead of only model admission
- encoder optional-extra ownership is now tighter too: DRED controls/runtime sit behind a lazy sidecar, `SetDNNBlob(...)` only binds model families, the runtime materializes only when model+duration+eligible mono or stereo-downmix use actually arms it, disabling/resetting DRED drops that sidecar back to a dormant state, tagged builds now pin `Encode` with DRED-capable models but no DRED duration as zero-allocation and runtime-dormant, public caller-buffer tests prove core-only decoder control state skips unarmed DRED payload-scan/good-packet marker work while combined core+DRED decoder blobs arm the parser, and the internal encoder DRED runtime files are build-tag split so default `./encoder` builds use no-op stubs instead of importing `internal/dred` or `internal/lpcnetplc`
- the internal encoder path now advances the exercised 16 kHz latent generator before DTX on armed mono and stereo frames, which is closer to libopus `opus_encoder.c` ordering
- the pure-Go encoder path now also ports the libopus `dred_convert_to_16k()` front-end for `8/12/24/48 kHz` mono/stereo input with retained DF2T filter memory inside the lazy DRED sidecar instead of hard-rejecting every non-16 kHz input, and the converter has dedicated green libopus-backed parity coverage in the DRED parity lane
- the lazy encoder DRED runtime now retains libopus-shaped latent/state history plus `dred_offset` / `latent_offset` bookkeeping for later emission instead of throwing away every emitted latent after the latest frame
- the lazy encoder DRED runtime now also retains the 2.5 ms activity window plus `last_extra_dred_offset` bookkeeping that libopus uses to delay or suppress payloads around silence transitions
- the pure-Go encoder side now has a byte-for-byte libopus-backed DRED payload builder for the `dred_encode_silk_frame()` seam, including header coding, quantizer signalling, offset bookkeeping, Laplace-coded state/latent chunks, and the same delayed-out-of-silence suppression rules
- the non-CELT encoder path now has bounded pure-Go DRED extension carriers for both the single-frame seam and the repacketized SILK long-packet seam, and the carry path now follows libopus more closely by only padding those extension packets in CBR instead of padding VBR/CVBR packets unconditionally
- the single-frame CELT-only encoder path now also attaches DRED extensions in VBR/CVBR, matching libopus opus_packet_pad_impl() behavior of tucking DRED into the leftover scratch space (`orig_max_data_bytes - ret - 3`) without subtracting from CELT's bitrate budget; the CELT FB 20 ms mono carried-payload seam is now pinned against the libopus emit helper
- encoder carried DRED now reserves the libopus-shaped DRED bitrate from the primary single-frame SILK/short-Hybrid budget, with the SILK wideband 20/40/60 ms mono and 20 ms stereo carried-payload seams and the Hybrid fullband 20/40 ms mono and stereo carried-payload/packet-envelope seams pinned in the supported DRED tag gate and mirrored in the required unsupported-controls DRED parity gate
- the current Hybrid carried-packet tests assert packet length, frame offset, DRED payload bytes, primary frame count/sizes, and extension padding bytes for the 20/40 ms mono and stereo packet-envelope seams; Hybrid/SILK stereo primary-frame byte exactness and broader Hybrid packet-shape matrices still need separate libopus-backed coverage before support docs can claim them
- the unsupported-controls gate and libopus-backed DRED/PLC helper tests now explicitly bootstrap the pinned libopus source snapshot through `tools/ensure_libopus.sh` instead of assuming a preseeded tarball/cache
- the libopus-backed DRED/PLC helper builds now use stamped scalar OS/arch-specific `build-opus-dred-scalar-*` directories and x86-vector-disabling CFLAGS, including the top-level DRED packet helpers, so Linux/amd64 helper oracles do not drift just because compiler builtins route libopus through SSE/AVX DNN helpers while the pure-Go port is tracking scalar math
- the OSCE BWE (blind bandwidth-extension) helper builds now have a stamped sibling OS/arch-specific `build-opus-osce-scalar-*` directory configured with `--enable-osce --enable-osce-bwe` so the otherwise-quarantined `bbwenet_process_frames` / `osce_bwe_calculate_features` / `osce_bwe` / `osce_load_models` symbols are linkable for parity helpers; the OSCE-enabled build coexists with the DRED-only scalar build because other parity tests still depend on the OSCE-undefined DRED build (configure-time `--enable-osce`/`--enable-osce-bwe` change the BBWENET runtime arming inside libopus and cannot be patched in via a single shared build dir). The OSCE-enabled build is provisioned by `osce_libopus_build_helpers_test.go::ensureLibopusOSCEBuild`, the build stamp is `libopustooling.WriteOSCEScalarDNNBuildStamp(...)`, and the helper compiler entry-point is `buildLibopusOSCEHelper(...)` (mirrors `buildLibopusDREDHelper(...)` but links against the OSCE-enabled `.libs/libopus.a`).
- the OSCE BWE forward-pass (`bbwenet_process_frames` via the public `osce_bwe(...)` entry point) now has a libopus-backed numerical parity contract in `decoder_osce_bwe_forward_pass_libopus_parity_test.go::TestOSCEBWEForwardPassMatchesLibopusNumericalParity`, and the LACE/NoLACE forward pass is required through `decoder_osce_lace_forward_pass_libopus_parity_test.go::TestOSCELACEForwardPassMatchesLibopus`. BWE 10 ms is exact on the exercised delayed wrapper; BWE 20 ms is ratcheted to a one-LSB numerical envelope (`maxAbs <= 3.0517578e-05`, `rms <= 9.8495e-07` on the current 1 kHz sinusoid). LACE/NoLACE remain near float32 roundoff (`maxAbs <= 1.3411045e-07`, `rms <= 3.7193e-08`). These are numerical comparator contracts, not byte/sample-exact claims.

### DNN blob runtime coverage

Decoder-side `SetDNNBlob(...)` is a model-readiness surface for the default
build and a tagged-runtime binding surface for the quarantined DRED/OSCE lanes.
What it currently does:

- validates the incoming blob through `dnnblob.Clone(...)` plus
  `Blob.ValidateDecoderControl(false)` in `dnn_blob.go::cloneDecoderDNNBlobForControl`
- retains the validated `*dnnblob.Blob` on the `Decoder` and mirrors the blob's
  `DecoderModelState` into `pitchDNNLoaded`, `plcModelLoaded`, `farganModelLoaded`,
  `osceModelsLoaded`, and `osceBWEModelLoaded` (see `decoder.go` field block)
- in default `.` builds (`decoder_dred_helpers_default.go::setDNNBlob`) does NOT
  arm any optional neural runtime: no `lpcnetplc` Analysis/Predictor/FARGAN
  binding, no DRED sidecar materialization, and no public OSCE controls; the
  flags are observation-only for
  `SupportsOptionalExtension(OptionalExtensionDNNBlob)` consumers
- in `-tags gopus_dred` (and `-tags gopus_unsupported_controls`) builds
  (`decoder_dred_helpers.go::setDNNBlob`) eagerly binds `lpcnetplc.Analysis`,
  `lpcnetplc.Predictor`, and `lpcnetplc.FARGAN` against the blob to surface load
  failures synchronously, then defers actual DRED neural-state allocation until
  `dredNeuralConfigEligible()` is true (16 kHz / 48 kHz mono or stereo) and real
  concealment/history work begins via `ensureDREDNeuralConcealmentRuntime()`
- DRED runtime arming additionally requires `SetDREDDuration(...)` on the
  encoder-side recovery loop and an active DRED payload/cache or first-loss
  entry on the decoder side; `SetDNNBlob(...)` alone keeps the DRED sidecar nil
- `-tags gopus_unsupported_controls` widens the test surface that exercises the
  PLC/DRED code and also exposes the quarantined OSCE BWE / LACE controls; those
  OSCE paths bind their libopus-style models and run the model-backed post-SILK
  forward passes in the explicit parity/quarantine lane, without changing default
  public support probes

Runtime paths not claimed as `SetDNNBlob(...)`-only support:

- ordinary (non-DRED) model-backed PLC: `lpcnetplc.FARGAN` is invoked inside the
  DRED concealment path (`decoder_dred_helpers.go::generateDREDNeuralFrames16k`
  and `decoder_dred_48k.go`), but there is no supported stand-alone
  model-backed PLC seam wired into `decoder_plc_helpers.go`
- multistream DRED/PLC neural concealment: tagged multistream decoders can now
  bind RDOVAE from a combined core+DRED decoder blob and consume cached DRED on
  the 48 kHz CELT elementary-stream seam. Broader multistream Hybrid/SILK paths
  still need green seam-specific parity. Single-stream DRED runtime now accepts
  16 kHz / 48 kHz one- or two-channel decoders; stereo uses the libopus-shaped
  mono-downmix-in / mono-duplicate-out neural path and remains
  quarantine-scoped until the broader stereo parity matrix is green.
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
- SILK stereo 40 ms DRED carrier residual gap (after `4012400a` stereo target-rate fix and `41adbcf6` forceSideCoding override removal) is **not** in the SILK encoder bits accounting: with `SetBitrate(40000)`, `SetPacketLoss(20)`, no `SetFEC`, stereo WB at `frameSize=1920` now produces a **byte-exact 126 B primary SILK frame** versus libopus (no LBRR pulses, no header divergence). The 9 B residual gap (gopus packet 151 B vs libopus 160 B) lives entirely in the DRED extension payload (gopus 19 B vs libopus 28 B), not in the SILK primary frame. Both encoders compute identical DRED plan parameters (`q0=6, dQ=5, qmax=15, maxChunks=21`), identical `activity_mem` (32 active bytes after frame[1], matching libopus byte-for-byte), and the same `latents_buffer_fill=4, latent_offset=1, dred_offset=10` runtime state, so the loop-bound `limit = IMIN(2*max_chunks, fill - latent_offset - 1) = 2` and `i=0` chunk count match exactly. Direct latent dump (via temporary `DRED_DEBUG_TRACE_LATENTS` hook on gopus side and `fprintf` patch into `dred_encode_silk_frame` on the libopus side) shows:
  - frame[0] latentsFill=2, position 1 (DFrame 1 latent) matches libopus byte-for-byte (`-0.3780, 1.4877, -37.7881, ...`)
  - frame[0] latentsFill=2, position 0 (DFrame 2 latent) **diverges**: gopus `-1.6028, -0.1098, -49.0587, ...` vs libopus `-1.3432, 19.3172, -50.8415, ...`
  - frame[1] latentsFill=4, position 1 (DFrame 3 latent, encoded at i=0 with q=6) diverges accordingly: gopus produces ec_tell≈152 bits (19 B payload) vs libopus 219 bits (28 B payload)
  
  So the root cause is: **the DRED encoder model's latent output for the second consecutive `dred_process_frame` call within a single `dred_compute_latents` invocation diverges from libopus**. 20 ms tests are unaffected because each Opus frame emits exactly one DFrame; 60 ms tests show byte-exact DRED bytes because the position-2 latent (the carried `dredOffset=10`-shifted target after 3 DFrames per call) lands at a different audio span where the precision drift hasn't yet rotated into the encoded chunk. Investigate the RDOVAE encoder recurrent state (`internal/dred/rdovae/encoder_runtime.go::EncoderProcessor.state.gru[5]`/`.conv[5]`) or LPCNet analysis state (`internal/lpcnetplc/analysis.go::Analysis`) propagation between consecutive DFrame emissions inside one `Process16k` callback loop; the first DFrame's output already matches libopus, so the divergence is in the state update from DFrame N → DFrame N+1 within the same `processDREDLatents` call. Candidates: (a) `dredFilterDF2TStep` precision when the same `mem` is updated across chunk boundaries vs a single sweep; (b) `EncodeDFrameWithProcessor` recurrent-state writeback ordering (each call updates `processor.state.gru[i]` and `processor.state.conv[i]` in place via `computeGenericGRU` / `computeGenericConv1D`); (c) `lpcnet_compute_single_frame_features_float` retained state (`analysisMem`, `memPreemph`, `pitchMem`, `excBuf`, `lpBuf`, `lpc`) when called four times back-to-back (DFrame 1's two LPCNet frames, then DFrame 2's two) vs the libopus reference. The fix does **not** belong in `silk/silk_encode.go`; the bits accounting (`stereoAllocationTargetRate`, `nBitsExceeded` rollover, `nBitsUsedLBRR` accumulation) already mirrors libopus exactly for the 40 ms primary frame.
- RESOLVED: the SILK stereo 40 ms DRED carrier residual gap above was **not** an LPCNet or RDOVAE state-propagation bug. The root cause is a libopus quirk in `dred_compute_latents()` at `dred_encoder.c:240`: the loop advance is `pcm += process_size` without a channels multiplier. For interleaved stereo at 48 kHz that under-advances the PCM pointer by a factor of 2 per iteration, so DFrame 2's input window overlaps DFrame 1's window. Verified with `tools/csrc/libopus_dred_latents_trace.c`: libopus mono and stereo agree on DFrame 1 latents but diverge on DFrame 2. Mirroring this behavior in `encoder/dred_runtime.go::convertDREDFrameTo16k` (replacing `input = input[processSamples:]` with `input = input[processSize:]`) closes the 40 ms stereo DRED packet length gap to byte-exact 160 B / 28 B payload. 60 ms stereo was already byte-exact because the under-advanced pointer happens to land DFrame 2 at audio content that quantizes the same way for the encoded chunk. The diagnostic `TestLibopusDREDLatentsTraceStereoDivergesFromMono` (under `gopus_unsupported_controls` in `internal/dred/`) pins the libopus stereo-vs-mono divergence so a future libopus update that fixes the missing channels multiplier will surface here.
- RESOLVED: Hybrid DRED latent generation now mirrors libopus's `nb_frames`
  split path for Hybrid frames > 20 ms, so Hybrid 40 ms stereo emits one
  DRED-latent pass per 20 ms sub-frame and the carried DRED payload/offset
  matches libopus. The required
  `TestEncoderCarriedDREDPayloadMatchesLibopusHybridFullband40msStereo`
  parity test now asserts the full packet envelope: packet length, frame
  offset, DRED payload bytes, primary frame count/sizes, and extension padding
  bytes. Primary-frame byte equality remains a separate out-of-scope parity lane
  unless a test explicitly claims it.
- every remaining `probeLibopusDecoderDREDDecodeFloat` caller in `decoder_dred_decode_float_libopus_parity_test.go` lives under a `TestDecoderExplicit*MatchesLibopus` test that drives the gopus side through `dec.decodeExplicitDREDFloat(...)` (the `FRAME_DRED` entrypoint), so swapping those to `probeLibopusDecoderDREDSequence` would change which libopus seam is probed — `FRAME_DRED` vs `FRAME_PLC_NEURAL` — rather than just swapping oracles, and the broader live-oracle adoption gap belongs on stereo/multistream/wider-packet cached coverage instead of those explicit-API tests
- Hybrid stereo 20 ms DRED primary-frame byte divergence is **not a CELT-half problem** despite the label: the gopus 65-byte primary frame for `TestEncoderCarriedDREDPayloadMatchesLibopusHybridFullband20msStereo` diverges from libopus at byte 0 itself. The top 4 bits of byte 0 are the patched stereo SILK header `[VADmid, LBRRmid, VADside, LBRRside]` (see `encoder/hybrid.go:1319` `re.PatchInitialBits(...)`); gopus emits `vadMid=0` while libopus emits `vadMid=1`, and all 63 subsequent bytes diverge transitively through the shared range coder. The CELT-side encoding logic is **not** the first divergence point — investigate the Mid-channel SILK VAD path instead: `encoder/hybrid.go:1232` `computeSilkVAD(mid, ...)`, the `applyOpusVADToSilkState` clamp at `encoder/encoder.go:2791` (uses `e.lastOpusVADActive` from `updateOpusVAD()` through the gopus tonality analyzer), and/or the float-input `mid` signal that comes out of `StereoLRToMSWithRates` versus libopus's int16 post-resampler `state_Fxx[0].inputBuf` feeding `silk_VAD_GetSA_Q8`.
- Root cause for the stereo Mid VAD divergence (above) is **NOT** the VAD input alignment: `silk.Encoder.StereoLRToMSWithRates` (`silk/stereo_encode.go:706-708`) already produces a `mid` array semantically identical to libopus `state_Fxx[0].inputBuf+1` (after `silk_stereo_LR_to_MS`, `inputBuf[0..1] = state->sMid` carry and `inputBuf[2..frame_length+1]` = current mid; gopus `midOut[0..frameLength-1] = midQ0[1..frameLength]` byte-equivalent). Per-frame diagnostic on the synthetic harmonic input used by `encodeUntilDREDPacket` shows Opus-level VAD stays active throughout (`opusVADProb >= 0.99` from frame 4 onward), so `applyOpusVADToSilkState` is **not** the clamp source. The actual bug is in the SILK VAD itself (`encoder/vad.go::VADState.getSpeechActivityFast`): `speechActivityQ8` drifts from `255 -> 234 (frame 17) -> 96 (frame 19) -> 28 (frame 22) -> 2 (frame 24)` and stays in 2-5 range on a steady harmonic, well below the `SILK_FIX_CONST(0.05, 8) = 13` threshold. libopus on the same source signal stays above 13. Candidates to investigate: noise-level estimator (`silk_VAD_GetNoiseLevels`) drift — gopus collapsing 255 -> 5 over ~10 frames on steady harmonics strongly suggests `VADState.NL`/`InvNL` smoothing converges faster than libopus; per-band energy quantization in `anaFiltBank1Exact`, the `XnrgSubfr` accumulation, and the `SNR_Q7` aggregation may be missing `silk_LSHIFT`/`silk_RSHIFT_ROUND` rounding modes. Easiest validation: dump `midQ0[1..frame_length]` for frame 80 of `encoderDREDFrame`, feed those *same int16* samples to both libopus `silk_VAD_GetSA_Q8` (via a small C helper) and `encoder/vad.go::VADState`; divergent outputs locate the bug entirely inside `encoder/vad.go`. Fix is **out of scope** of `encoder/hybrid.go:1232` — once SILK VAD parity is closed, the existing `computeSilkVAD(mid, ...)` call needs no change.
- CELT NB DRED is not a parity item: `libopusDREDBandwidthEnv` in `dred_libopus_packet_helpers_test.go` only maps `wb`/`swb`/`fb` (rejecting `BandwidthNarrowband` with an error), and the C helper `tools/csrc/libopus_dred_emit_packet.c` `parse_bandwidth_env` accepts only `wb`/`swb`/`fb` strings, mirroring that libopus does not emit DRED for NB packets in standard usage since CELT operates at SWB/FB and NB is SILK-only territory
- RESOLVED: SILK-only explicit DRED now seeds the neural entry history from the
  retained SILK native lowband through `primeExplicitSILKDREDEntryHistory(...)`
  before queueing FEC features, mirroring the Hybrid lazy-history pattern. The
  48 kHz SILK WB explicit stereo first-loss seam is now required in the
  unsupported-controls parity gate.

Still missing for full parity:

- decoder-level parity beyond the required mono seams, selected 16 kHz Hybrid mono live-sequence seams, CELT/Hybrid stereo cached/live first/second-loss and next-packet handoff matrices, selected 16 kHz CELT/Hybrid stereo explicit first-loss probes, explicit first-loss and recovery lifecycle/cursor seams, the required 48 kHz SILK WB explicit stereo first-loss seam, and the covered multistream CELT/Hybrid/SILK DRED consumers, especially broader SILK stereo packet/mode matrices, broader multistream packet/mode coverage, broader packet coverage, and the final supported-surface decisions for what graduates from quarantine
- single-stream and multistream `SetDNNBlob(...)` validate the blob and retain the model-loaded flags (`pitchDNNLoaded`, `plcModelLoaded`, `farganModelLoaded`, `osceModelsLoaded`, `osceBWEModelLoaded`); in tag-gated builds, a retained blob that also contains the DRED decoder family arms RDOVAE for packet-extension recovery, while core-only blobs leave DRED payload scanning and nil-packet DRED PLC dormant. OSCE LACE/NoLACE postfilter and OSCE BWE upsampler runtime paths are wired in the quarantined OSCE lane with numerical comparator contracts and runtime smoke coverage; ordinary non-DRED model-backed PLC is not a supported standalone surface yet, while non-16-or-48 kHz DRED neural concealment remains gated off independently of blob readiness
- model-backed `opus_decoder_dred_decode*()` parity beyond the currently exercised mono explicit seams, including broader `LPCNetEncState`-shaped analysis/runtime coverage and decoder-owned integration for any surfaces that graduate from quarantine
- broader live-oracle adoption beyond the covered mono cached seams, CELT/Hybrid stereo first/second-loss and next-packet handoff matrices, and the covered multistream CELT/Hybrid/SILK consumers; broader SILK stereo packet/mode matrices and broader multistream packet/mode paths still need live-sequence coverage where those surfaces become supported
- encoder-side DRED beyond the current exercised latent-generation and carried-payload seams: broadening rate / packet-shape / stereo / multistream coverage around the payload-emission path and finalizing which surfaces graduate from quarantine

### Stereo And Multistream DRED Runtime Scope

Decoder-side DRED neural concealment follows libopus's mono-internal model:
there is one retained LPCNet/FARGAN/DRED-feature pipeline, and stereo output is
made by downmixing on entry and duplicating the concealed mono result on exit.
The CELT/Hybrid cached/live first/second-loss and next-packet handoff matrices,
explicit first-loss and recovery lifecycle/cursor seams, selected 16 kHz
CELT/Hybrid stereo explicit first-loss probes, plus the 48 kHz SILK WB explicit
stereo first-loss seam now follow that shape in tag-gated parity tests.

What is green today:

- `dredNeuralConfigEligible()` admits 16 kHz / 48 kHz one- or two-channel
  single-stream decoders, and stereo runtime readiness is retained across
  `Reset()` without eagerly allocating the DRED sidecar on `SetDNNBlob()`.
- `applyDREDNeuralConcealment48kMono()` and
  `applyPLCNeuralConcealment48kMono()` accept mono or stereo caller buffers while
  preserving the mono-internal runtime contract.
- The CELT and Hybrid wrappers mirror channel-0 neural state into channel 1 and
  interleave duplicated mono PCM for the stereo cached/live first/second-loss
  and next-packet handoff matrices.
- Cached stereo recovery lifecycle/cursor tests verify libopus-style recovery
  bookkeeping, including the Hybrid live-loss idle cursor behavior.
- Explicit 48 kHz CELT FB stereo DRED first-loss parity exercises downmixed
  entry history and mono-duplicated output against libopus in the required
  quarantine gate.
- Explicit 48 kHz SILK WB stereo DRED first-loss parity is required in the
  quarantine gate.
- Explicit 16 kHz CELT/Hybrid stereo DRED first-loss parity now uses the
  libopus packet helper's forced-channel mode so those carrier shapes are
  required no-skip probes in the quarantine gate.
- Uncoupled mono, single-coupled stereo, and non-leading second-coupled
  multistream CELT/Hybrid/SILK DRED consumers are required in both the
  supported DRED and quarantine gates.

What remains open:

- Broader SILK stereo decoder DRED matrices, especially repeated-loss,
  resumed-good-packet, and packet-shape coverage around the SILK mid/lowband
  hook path.
- Broader multistream decoder DRED packet/mode matrices and live-sequence
  parity beyond the focused CELT/Hybrid/SILK consumer gates.
- Support-surface graduation. Broader stereo DRED should stay quarantine-only
  until the above seams are green in libopus-backed parity tests.

What libopus does for stereo DRED (pinned reference: `tmp_check/opus-1.6.1`):

- `struct OpusDecoder` has a single `LPCNetPLCState lpcnet` field regardless of
  channel count (`src/opus_decoder.c:65-77`).
- `silk_Decode()` only attaches the `lpcnet` state to channel 0 of its internal
  stereo loop (`silk/dec_API.c:357`, `n == 0 ? lpcnet : NULL`).
- `update_plc_state()` in `celt/celt_decoder.c:639-672` mixes both decode
  memories down to mono before feeding the LPCNet PLC update, and
  `celt_decode_lost()` duplicates the mono concealed buffer into channel 1
  (`celt/celt_decoder.c:1066-1067`).
- `lpcnet_plc_fec_add()` / `lpcnet_plc_fec_clear()` in
  `src/opus_decoder.c:741-754` operate on the single `st->lpcnet` instance
  regardless of `st->channels`.

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
- the current pure-Go encoder seam now covers retained `8/12/16/24/48 kHz` mono and stereo-downmix conversion into the 16 kHz latent/state path, the libopus `dred_encode_silk_frame()` payload-coding seam, bounded single-frame carried extensions, exercised SILK carried-payload seams, and the Hybrid fullband 20/40 ms mono and stereo carried-extension packet-envelope seams, while broader packet-shape coverage, primary-frame byte exactness, and final support-surface decisions remain open

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
- the current 16 kHz mono cached/live seam has required resumed-good-packet handoff PCM coverage after first and repeated loss across the exercised frame-size matrix, and direct concealment PCM plus explicit matrix checks are now in the required unsupported-controls parity lane for the required mono seams
- the 48 kHz mono CELT `FRAME_DRED` bridge, selected 16 kHz CELT/Hybrid stereo explicit first-loss probes, CELT/Hybrid stereo first/second-loss, next-packet, and recovery matrices, the 48 kHz SILK WB explicit stereo first-loss seam, the 48 kHz mono Hybrid SWB/FB first-loss/handoff seams, and the uncoupled mono, single-coupled stereo, and non-leading second-coupled multistream CELT/Hybrid/SILK consumers are covered by required gates, with Hybrid now following the libopus-shaped Hybrid PLC base; the next decoder gap is broader SILK stereo and multistream packet/mode coverage
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
