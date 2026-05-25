# Agent Instructions

## Libopus Type Parity Rule

This repository targets libopus 1.6.1 parity. Runtime codec-domain storage must use the same scalar width as libopus, including temporary scratch buffers.

- In the libopus float build, `opus_val16`, `opus_val32`, `opus_val64`, `opus_res`, `celt_sig`, `celt_norm`, `celt_ener`, `celt_glog`, `celt_coef`, and `silk_float` are C `float`; use Go `float32` or the matching local alias.
- Scratch is in scope. Reusable scratch fields, local temporary slices, conversion buffers, MDCT/PVQ/energy/PLC/QEXT/DRED buffers, SILK pitch/NLSF/NSQ state, helper allocators, and test-only runtime probes must follow libopus width too.
- Do not add runtime `float64`, `complex128`, `KissFFT64State`, `ensureFloat64Slice`, or `ensureComplexSlice` unless the matching libopus source uses C `double` for that exact helper. Cite the C file/function in the code or the baseline reason.
- Use fixed-width Go integer types for libopus state and arithmetic: C `int`/`opus_int` runtime fields and scratch should be `int32`, `opus_int16` should be `int16`, `opus_uint32` should be `uint32`, and so on.
- Go `int` is allowed for indexes, lengths, loop counters, slice capacities, public Go ergonomics, and table indexing only. Do not keep reusable scratch or codec-domain arithmetic in `int` just because it compiles.
- Do not add conversion bridges, compatibility wrappers, or cast-heavy shims to preserve old internal surfaces. Break internal and public interfaces when needed, then fix the callers.
- When a remaining wide scalar or generic `int` is deliberate, it must be an index/length/public-boundary use or have a source-cited reason tied to a matching libopus type.
- Before finishing a codec/runtime change, run `go test` for touched packages and `make test-type-parity`. Do not run `make update-type-parity-baseline` to hide new debt. Refresh the baseline only when cleanup removed legacy findings, or when a remaining wide scalar is intentionally tied to a specific C helper.
- Byte parity still matters: do not change quality thresholds, fixture baselines, or oracle expectations to make type work appear correct. Fix the root type or arithmetic mismatch.

Treat fixture/baseline edits as review-visible evidence, not a shortcut.
