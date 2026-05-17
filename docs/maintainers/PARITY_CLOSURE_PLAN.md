# Parity Closure Plan

Last updated: 2026-05-14

## Goal

Close parity gaps against the pinned libopus 1.6.1 reference with evidence that is specific, repeatable, and honest about what is covered.

Parity is only closed for a surface when a libopus-backed test or generated fixture proves it. Passing broad tests is necessary, but it is not by itself a claim that every codec mode, control transition, optional extension, or packet shape is fully byte-identical.

## Current Snapshot

Green local checks after the latest QEXT byte/control cleanup:

```sh
go test ./... -count=1
go test ./... -tags gopus_qext -count=1
```

Recently closed:

- QEXT decode fine-finalise now only skips main energy mutation when a prepared QEXT decode is actually active, not merely when an extension payload byte slice exists.
- QEXT CELT channel-transition parity now matches libopus for the exercised `mono -> stereo -> mono` sequence.
- `handleChannelTransition` no longer rewrites QEXT `oldBandE` or extra stereo-to-mono log/background state during transition pre-processing.
- Focused tests now cover QEXT channel-transition state behavior and libopus-backed QEXT sequence output parity.
- QEXT malformed extension padding is now treated as a non-fatal parse gap in CELT packet extension payload collection (single-stream and multistream), matching libopus behavior to continue decode on opaque padding.

Still required before merge-ready codec claims:

```sh
make verify-production
```

For parity branches that touch encoder quality, packet shape, or performance-sensitive paths, also run the gates listed in `docs/maintainers/ENCODER_PARITY.md`.

## Parity Categories

### 1. Byte-Level Parity

Objective:

Make packet, range-coder, and final-state decisions match libopus wherever the public surface claims exact support.

Work to close:

- Keep packet parsing, TOC interpretation, padding, extension discovery, and frame splitting pinned against libopus.
- Ratchet CELT byte-level decisions around allocation, fine energy, final range, anti-collapse reservations, postfilter signalling, and QEXT payload handoff.
- Ratchet encoder packet shape by mode: CELT, SILK, Hybrid, mono, stereo, VBR, CVBR, CBR, DTX, FEC, and optional extension carriers.
- Track final range parity for decode and encode paths whenever a range-coder decision changes.
- Add narrow regression fixtures before changing bitstream decisions, then broaden only after the focused case is green.

Acceptance:

- Byte fixtures compare packet bytes, raw frames, extension payloads, final range, and decoded PCM where applicable.
- Any intentional non-byte-identical result is documented with the exact fixture evidence and quality/compatibility rationale.

### 2. Control-Transition Parity

Objective:

Match libopus state transitions across packet-to-packet mode changes, not only steady-state frames.

Work to close:

- Exercise `mono <-> stereo` stream-channel transitions in CELT-only, Hybrid, Opus wrapper, and QEXT paths.
- Exercise bandwidth, start/end band, frame-size, sample-rate, and mode transitions across good packets.
- Exercise PLC/loss transitions: good packet, first loss, repeated loss, DTX/silence, FEC, DRED/neural concealment where enabled, then resumed good packet.
- Verify reset, decoder reuse, and control calls leave state matching libopus and keep optional sidecars dormant unless genuinely armed.
- Extend transition matrices to multistream once single-stream claims are green.

Acceptance:

- Transition tests use real libopus-produced packets or pinned fixture sequences.
- Tests compare output PCM and the retained state that influences the next packet, not just the immediate frame.

### 3. Signal-Level Parity

Objective:

Keep decoded and encoded audio quality aligned with libopus for every supported mode, while using byte-level tests for exactness-sensitive decisions.

Work to close:

- Keep CELT, SILK, Hybrid, PLC, resampler, and multistream quality cases represented in the quality report.
- Preserve strict handling for already verified areas: SILK decoder path, SILK/Hybrid resampler parity, CWRS sign handling, MDCT/IMDCT roundtrip, energy coding roundtrip, and NSQ constant-DC amplitude behavior.
- Add targeted quality cases when a bug only appears after several packets or after a control transition.
- Keep reports fail-closed when expected sections parse as empty.

Acceptance:

- `make quality-report` produces non-empty expected sections and stays within agreed thresholds.
- Any regression is tied to a named fixture and either fixed or explicitly quarantined.

### 4. Optional Extension Parity

Objective:

Keep optional features honest: dormant by default, zero-cost when unused, and only claimed when libopus-backed parity is green.

Work to close:

- QEXT: broaden byte-level and transition matrices beyond the current exercised CELT cases, including Hybrid and wrapper-level edge cases.
- DRED: continue the plan in `docs/maintainers/dred-parity-plan.md`; keep unsupported/staged surfaces quarantined until decoder, encoder, and live-sequence coverage is green.
- DNN-backed controls: ensure loading model/control state does not wake decode or encode sidecars unless the feature is armed and used.
- Multistream optional extras: keep capability exposure separate from feature support until per-stream parity coverage exists.

Acceptance:

- Default builds do not import or allocate optional runtimes on ordinary encode/decode paths.
- Tagged or experimental paths have dedicated libopus-backed tests and docs that state the covered surface precisely.

### 5. Encoder Parity

Objective:

Move encoder parity from quality-compatible to exact where exactness is required, without sacrificing zero-allocation caller-buffer APIs.

Work to close:

- Maintain strict CELT header/postfilter ratchets.
- Broaden packet-shape coverage for SILK and Hybrid, especially around bit allocation, carried extension budgets, FEC/DTX decisions, and stereo decisions.
- Keep deterministic scalar paths where architecture vectorization would perturb byte-level choices.
- Re-run quality and performance guardrails after any encoder math or packet-budget change.

Acceptance:

- `make quality-report`, `make bench-guard`, `make bench-libopus-guard`, and `make verify-production` are green.
- Public `Encode` hot paths remain `0 B/op`, `0 allocs/op`.

## Closure Workflow

Use this loop for every parity gap:

1. Name the exact surface: mode, sample rate, channels, packet sequence, control state, build tag, and expected oracle.
2. Reproduce the gap with the smallest libopus-backed fixture or test.
3. Cross-check the behavior against `tmp_check/opus-1.6.1/` before changing code.
4. Fix the narrow path without waking unrelated optional sidecars or changing verified areas.
5. Add or update the focused parity test first.
6. Run the focused test, then the relevant tagged package suite.
7. Run broad validation before calling the change merge-ready.
8. Document closed surfaces and any remaining quarantined surfaces.

## Required Gates

Minimum local gates for ordinary parity work:

```sh
go test ./... -count=1
go test ./... -tags gopus_qext -count=1
make verify-production
```

Additional gates for encoder or performance-sensitive parity work:

```sh
make quality-report
make bench-guard
make bench-libopus-guard
```

Additional gates for DRED or unsupported optional controls:

```sh
make test-unsupported-controls-parity
```

Use `GOPUS_STRICT_LIBOPUS_REF=1` for strict fixture-backed parity lanes when the test supports it.

## Open Priority Matrix

Highest-priority remaining work:

- Broaden QEXT transition coverage from the current CELT sequence into Hybrid and wrapper-level packet sequences.
- Add byte-level QEXT packet/final-range ratchets around extension-present-but-inactive cases.
- Run `make verify-production` after the current QEXT transition fix.
- Continue DRED parity closure from `docs/maintainers/dred-parity-plan.md`, especially stereo, multistream, and Hybrid packet-shape gaps.
- Broaden live-sequence decoder oracles for cached/loss recovery paths that still rely on explicit helper comparisons.
- Keep encoder Hybrid packet-shape exactness tracked until byte-level fixtures prove it.

## Work-to-close checklist (byte-level + control-transition focus)

1. Add parity-locked fixtures for QEXT malformed/opaque padding in wrapper-level multi-stream packets and validate no crash plus decode PCM equivalence.
2. Extend this parity-locked coverage to SILK/Hybrid transition frames (when the decoder receives QEXT-parseable padding on CELT-branching packet sequences).
3. Capture packet/final-range and extension-state checkpoints for extension-present-but-inactive byte shapes, then compare to libopus fixtures.
4. Map control-transition matrices around `mono <-> stereo` under packet-loss recovery, including `SetIgnoreExtensions` toggles across frame boundaries.
5. Run `go test ./...` and `go test ./... -tags gopus_qext` on every change, then `make verify-production` before merge-ready marking.

Do not claim full parity for a surface just because an adjacent helper, control, or tag-gated path exists. The claim follows the green libopus-backed test, not the API exposure.
