# Agent Instructions

## Libopus Int Type Parity Rule

This repository targets libopus 1.6.1 parity. The active goal for this lane is integer-width parity across runtime codec state, fixed-point arithmetic, helper state, and scratch buffers.

- Use fixed-width Go integer types for libopus state and arithmetic: C `int`/`opus_int` runtime fields and scratch should be `int32`, `opus_int16` should be `int16`, `opus_uint32` should be `uint32`, and so on.
- Go `int` is allowed for indexes, lengths, loop counters, slice capacities, public Go ergonomics, and table indexing only. Do not keep reusable scratch or codec-domain arithmetic in `int` just because it compiles.
- Scratch is in scope. Reusable scratch fields, temporary buffers, pulse/CWRS/PVQ state, range-coder state, allocation work buffers, SILK pitch/NLSF/NSQ state, and helper allocators must follow libopus width.
- Do not add conversion bridges, compatibility wrappers, or cast-heavy shims to preserve old internal surfaces. Break internal and public interfaces when needed, then fix the callers.
- When a remaining `int` is deliberate, it must be an index/length/public-boundary use or have a source-cited reason tied to a matching libopus type.
- Before finishing this lane, run `go test` for touched packages and `make test-type-parity`. Do not weaken baselines or thresholds to hide failures.
- Byte parity still matters: do not change quality thresholds, fixture baselines, or oracle expectations to make type work appear correct. Fix the root type or arithmetic mismatch.

Treat fixture/baseline edits as review-visible evidence, not a shortcut.
