# Gopus Agent Context

Use `program.md` as the canonical autonomous experiment workflow.
Use this file for project facts and guardrails.
If they differ, `program.md` wins for workflow and this file wins for codec context.

## Project

- `gopus` is a pure Go implementation of Opus (RFC 6716).
- The pinned reference implementation is `tmp_check/opus-1.6.1/` (libopus 1.6.1).
- The main public API target remains zero-allocation caller-owned buffers:
  - `func (d *Decoder) Decode(data []byte, pcm []float32) (int, error)`
  - `func (e *Encoder) Encode(pcm []float32, data []byte) (int, error)`

## Current Focus

1. Run libopus-parity-first mixed quality+feature work by default.
2. Keep quality as the primary score, using libopus parity and compliance as the reference.
3. Treat throughput as optional unless the current change is explicitly performance-facing.
4. Preserve zero-allocation guarantees in encoder and decoder hot paths.
5. Coordinate active work under three top-level lanes: `performance`, `libopus parity`, and `code quality / maintainability`.

## Verified Areas

Do not start by re-debugging these without new evidence:

- SILK decoder correctness path.
- Resampler parity path used for SILK and Hybrid downsampling.
- CWRS sign handling, MDCT/IMDCT roundtrip, and energy coding roundtrip.
- NSQ constant-DC amplitude behavior (~0.576 RMS ratio).

## Hard Rules

- Cross-check codec math and bitstream decisions against `tmp_check/opus-1.6.1/` before trying heuristic fixes.
- If behavior is uncertain, align to libopus 1.6.1 first and only diverge with explicit fixture evidence.
- Preserve zero allocations in real-time encode/decode hot paths.
- Do not mention `codex` in branch names, commit messages, PR titles, or PR descriptions; use generic change-focused wording instead.
- For branch names and PR titles/descriptions, use generic change-focused wording and do not mention codec-specific subsystems or audio internals unless the user explicitly asks for that wording.
- When a loop benefits from parallel scouting, prefer one read-only quality/compliance scout and one read-only unimplemented-feature scout.
- Do not turn raw `ErrUnimplemented` stubs into loop targets unless they have a pinned judge; the current safe unimplemented seed is `ogg-seek`.
- During an experiment loop, treat these as fixed judge surfaces unless you are intentionally changing the workflow itself:
  - `program.md`
  - `tools/autoresearch.sh`
  - `tools/benchguard/main.go`
  - `tools/bench_guardrails.json`
  - `testvectors/testdata/`
  - `tmp_check/`

## Parallel Coordination

- When multiple researchers are active, use an open draft PR as the shared claim surface whenever possible.
- A claim must name the top-level lane, editable surface, owner, and current hypothesis.
- Open the draft PR before the first editable code change on the branch; use an empty claim commit if the branch needs visible history first.
- Keep the PR body current with attempts, failures, results, blockers, and next action so other workers can see progress before the slice is done.
- Do not start overlapping editable work on the same `(lane, editable surface)` pair when an active claim already exists.
- If the pair is already occupied, switch to read-only scouting, review, or another pair instead of competing edits.
- Keep one active editable branch per researcher.
- Merge kept slices sequentially, not in batches, and revalidate after rebasing onto the current queue head.

## Quick Start

1. Read `program.md`, `AGENTS.md`, and `README.md`.
2. Run `make autoresearch-init`.
3. Run `make autoresearch-preflight`.
4. Run `make autoresearch-eval DESCRIPTION=baseline`.
5. Before proposing merge-ready codec changes, run `make verify-production`.
