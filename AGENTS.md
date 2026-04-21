# gopus Project Brief

## Project

- `gopus` is a pure Go implementation of Opus (RFC 6716).
- The pinned reference implementation is `tmp_check/opus-1.6.1/` (libopus 1.6.1).
- The main public API target remains zero-allocation caller-owned buffers:
  - `func (d *Decoder) Decode(data []byte, pcm []float32) (int, error)`
  - `func (e *Encoder) Encode(pcm []float32, data []byte) (int, error)`

## Priorities

1. Parity with libopus in quality and features.
2. Performance.
3. Maintainability.
4. Documentation.
5. Dead-test cleanup.

## Rules

- Cross-check codec math and bitstream decisions against libopus 1.6.1 before trying heuristic fixes.
- If behavior is uncertain, align to libopus first and only diverge with explicit fixture evidence.
- Preserve zero allocations in real-time encode/decode hot paths.
- Treat `testvectors/testdata/` and `tmp_check/` as fixed references unless the change is explicitly about fixtures or the pinned libopus snapshot.
- Keep branch names, commit messages, and PR titles/descriptions generic and change-focused.

## Verified Areas

Do not start by re-debugging these without new evidence:

- SILK decoder correctness path.
- Resampler parity path used for SILK and Hybrid downsampling.
- CWRS sign handling, MDCT/IMDCT roundtrip, and energy coding roundtrip.
- NSQ constant-DC amplitude behavior (~0.576 RMS ratio).

## Merge Gate

- Run focused tests for the touched area while iterating.
- Run `make verify-production` before proposing merge-ready codec changes.
