# Agent Instructions

## Libopus Type Parity Rule

This repository targets libopus 1.6.1 parity. Runtime codec-domain storage must use the same scalar width as libopus, including temporary scratch buffers.

- In the libopus float build, `opus_val16`, `opus_val32`, `opus_val64`, `opus_res`, `celt_sig`, `celt_norm`, `celt_ener`, `celt_glog`, `celt_coef`, and `silk_float` are C `float`; use Go `float32` or the matching local alias.
- Scratch is in scope. Reusable scratch fields, local temporary slices, conversion buffers, MDCT/PVQ/energy/PLC/QEXT/DRED buffers, and helper allocators must follow libopus width too.
- Do not add runtime `float64`, `complex128`, `KissFFT64State`, `ensureFloat64Slice`, or `ensureComplexSlice` unless the matching libopus source uses C `double` for that exact helper. Cite the C file/function in the code or the baseline reason.
- Use `int32`/`uint32`/fixed-width integer helpers for libopus arithmetic and state. Use Go `int` only for indexes, lengths, loop counters, or deliberate public Go API ergonomics.
- Before finishing a codec/runtime change, run `make test-type-parity`.
- Do not run `make update-type-parity-baseline` to hide new debt. Refresh the baseline only when cleanup removed legacy findings, or when a remaining `float64` is intentionally tied to a specific C `double` helper.

If the guard fails, migrate the code to the libopus-width type first. Treat allowlist edits as review-visible evidence, not a shortcut.
