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

1. Close the fair `gopus` vs `libopus` throughput gap on the speech encode path.
2. Preserve fixture-backed libopus parity and quality while optimizing.
3. Preserve zero-allocation guarantees in encoder and decoder hot paths.

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
- During an experiment loop, treat these as fixed judge surfaces unless you are intentionally changing the workflow itself:
  - `program.md`
  - `tools/autoresearch.sh`
  - `tools/benchguard/main.go`
  - `tools/bench_guardrails.json`
  - `testvectors/testdata/`
  - `tmp_check/`

## Quick Start

1. Read `program.md`, `AGENTS.md`, and `README.md`.
2. Run `make autoresearch-init`.
3. Run `make autoresearch-preflight`.
4. Run `make autoresearch-eval DESCRIPTION=baseline`.
5. Before proposing merge-ready codec changes, run `make verify-production`.
